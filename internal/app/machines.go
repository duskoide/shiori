package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
	"github.com/duskoide/shiori/internal/update"
)

// Other machines are managed with herdr-core's command line, as herdr-app
// does: hc adds a machine over SSH, renames, removes, updates and logs in
// to it. When adding needs a person (a password, a host key, a core to
// install) the same command runs in a tab, where they answer it, and the
// machine shows once it is there.

// machineOp is a machine command in flight, or how the last one went, for
// the Machines page.
type machineOp struct {
	what string // the last command: "add", "rename", "rm", "update"
	busy string // what is being done, as what, while it is
	msg  string // how it went
	ok   bool
}

// machinesState is what the window keeps of machines besides the core's
// list.
type machinesState struct {
	ops       map[string]*machineOp // by machine name, or target while adding
	hcVersion string                // this machine's hc, to tell an older core
	versionAt time.Time             // when hcVersion was asked
	// The Machines page's fields.
	target   string
	renaming string // the machine being renamed
	newName  string
	removing string // the machine whose removal waits to be confirmed
}

// hcRunner is the hc command the window runs, for the core it talks to.
func (a *App) hcRunner() *core.HC {
	if a.hc == nil {
		a.hc = &core.HC{}
		if a.session != "" {
			a.hc.Env = []string{"HC_SESSION=" + a.session}
		}
	}
	return a.hc
}

func (a *App) machineOp(key string) *machineOp {
	if a.mach.ops == nil {
		a.mach.ops = map[string]*machineOp{}
	}
	op := a.mach.ops[key]
	if op == nil {
		op = &machineOp{}
		a.mach.ops[key] = op
	}
	return op
}

// pollMachines asks the core for its machines now, not at the next poll.
func (a *App) pollMachines() {
	be, gen := a.core, a.sy.gen
	if be == nil {
		return
	}
	go func() {
		ms, err := machineList(context.Background(), be)
		if err != nil {
			return
		}
		a.post(func() {
			if gen == a.sy.gen {
				a.setMachines(ms)
			}
		})
	}()
}

// machineWord is a machine's state for people, and whether it needs them.
func machineWord(m core.Machine) (string, bool) {
	switch m.State {
	case core.MachineConnected:
		return "Connected", false
	case core.MachineNeedsLogin:
		return "Needs login", true
	case core.MachineNeedsUpdate:
		return "Needs update", true
	case "":
		return "Unknown", true
	}
	return "Reconnecting", false
}

// machineDot is the color of a machine's state.
func machineDot(k *colors, m core.Machine) ui.Color {
	switch word, bad := machineWord(m); {
	case bad:
		return k.blocked
	case word == "Connected":
		return k.done
	}
	return k.working
}

// localHC is this machine's hc version, asked once a minute at most.
func (a *App) localHC() string {
	if time.Since(a.mach.versionAt) > time.Minute {
		a.mach.versionAt = time.Now()
		hc := a.hcRunner()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if v, err := hc.Version(ctx); err == nil {
				a.post(func() { a.mach.hcVersion = v })
			}
		}()
	}
	return a.mach.hcVersion
}

// olderCore reports whether a machine's core is older than this one's, so
// it can be updated.
func (a *App) olderCore(m core.Machine) bool {
	local := a.localHC()
	return local != "" && m.Version != "" && update.Compare(m.Version, local) < 0
}

// sshHosts are the hosts of ~/.ssh/config to add a machine from: its Host
// names, without patterns.
func sshHosts() []string {
	f, err := os.Open(filepath.Join(homeDir(), ".ssh", "config"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !strings.EqualFold(fields[0], "host") {
			continue
		}
		for _, h := range fields[1:] {
			if strings.HasPrefix(h, "#") {
				break
			}
			if !strings.ContainsAny(h, "*?!") && !slices.Contains(out, h) {
				out = append(out, h)
			}
		}
	}
	return out
}

// added reports whether a target is one of the machines already.
func (a *App) added(target string) bool {
	for _, m := range a.machines {
		if m.Target == target || m.Name == target || m.Host == target {
			return true
		}
	}
	return false
}

// localTab runs argv in a new tab of a space of this machine's: the shown
// one when it is, else the first, else a new one.
func (a *App) localTab(argv []string, name string) (any, error) {
	var space string
	if s := a.space(); s != nil && s.Machine == "" {
		space = s.ID
	} else if a.doc != nil {
		for _, s := range a.doc.Spaces {
			if s.Machine == "" {
				space = s.ID
				break
			}
		}
	}
	if space == "" {
		return a.do("space.new", args{"argv": toAny(argv), "tab": name, "folder": homeDir()})
	}
	return a.do("tab.new", args{"space": space, "argv": toAny(argv), "name": name, "folder": homeDir()})
}

// runMachine runs a machine command off the main thread for the op of
// key, then fn with how it went on the main thread, and answers with
// fn's result.
func (a *App) runMachine(key, busy string, work func(ctx context.Context, hc *core.HC) (any, error), fn func(v any, err error) (any, error)) *deferred {
	op := a.machineOp(key)
	d := a.newDeferred(core.HCSlowTimeout+10*time.Second, nil)
	if op.busy != "" {
		d.resolve(nil, fmt.Errorf("%s is busy: %s", key, op.busy))
		return d
	}
	op.what, op.busy, op.msg = busy, busy, ""
	hc := a.hcRunner()
	go func() {
		v, err := work(context.Background(), hc)
		a.post(func() {
			op.busy = ""
			v, err := fn(v, err)
			op.ok = err == nil
			if err != nil {
				op.msg = err.Error()
			}
			a.pollMachines()
			d.resolve(v, err)
		})
	}()
	return d
}

func machineJSON(m core.Machine) map[string]any {
	return map[string]any{"name": m.Name, "target": m.Target, "host": m.Host, "version": m.Version,
		"state": m.State, "why": m.Why, "connected": m.Usable()}
}

func (a *App) cmdMachineList(args) (any, error) {
	out := []map[string]any{}
	for _, m := range a.machines {
		out = append(out, machineJSON(m))
	}
	return map[string]any{"machines": out}, nil
}

// cmdMachineAdd adds a machine over SSH. When that needs a person, it goes
// on in a tab, and answers {in_tab: true}.
func (a *App) cmdMachineAdd(v args) (any, error) {
	if !given(v["target"]) {
		a.askText("Add a machine", "", "user@host", "How SSH reaches it: user@host, or a host of your ~/.ssh/config. Your SSH keys are used; when it needs a password, a host key confirmed or herdr-core installed there, it goes on in a tab where you answer.", func(target string) {
			if target != "" {
				a.doToast("machine.add", args{"target": target})
			}
		})
		a.prompt.yesLabel = "Add"
		return nil, nil
	}
	target := v.str("target")
	if target == "" || strings.ContainsAny(target, " \t\n") {
		return nil, errors.New("target is how SSH reaches the machine: user@host, or a host of ~/.ssh/config")
	}
	if a.added(target) {
		return nil, fmt.Errorf("%s is added already", target)
	}
	return a.runMachine(target, "add", func(ctx context.Context, hc *core.HC) (any, error) {
		return nil, hc.MachineAdd(ctx, target)
	}, func(_ any, err error) (any, error) {
		op := a.machineOp(target)
		switch {
		case err == nil:
			op.msg = "Added " + target + "."
			a.notify(withKeys("Added "+target+": Switch machine goes to it", a.keysFor("machine.switch")))
			a.mach.target = ""
			return map[string]any{"added": target}, nil
		case core.AddNeedsPerson(err):
			argv, aerr := a.hcRunner().Argv("machine", "add", target)
			if aerr != nil {
				return nil, aerr
			}
			if _, terr := a.localTab(argv, "Add "+target); terr != nil {
				return nil, terr
			}
			op.msg = "Adding " + target + " needs you: answer in its tab. It shows here once it is added."
			a.mach.target = ""
			return map[string]any{"in_tab": true, "why": err.Error()}, nil
		}
		return nil, err
	}), nil
}

// machineArg is the machine a command names.
func (a *App) machineArg(v args) (core.Machine, error) {
	name := v.str("name")
	m, ok := a.machine(name)
	if !ok {
		return m, fmt.Errorf("no machine %q", name)
	}
	return m, nil
}

func (a *App) cmdMachineRename(v args) (any, error) {
	m, err := a.machineArg(v)
	if err != nil {
		return nil, err
	}
	to := v.str("to")
	if to == "" || to == m.Name || strings.ContainsAny(to, "/ \t") {
		return nil, errors.New("to is the new name: no spaces, no slashes")
	}
	if _, taken := a.machine(to); taken {
		return nil, fmt.Errorf("there is a machine %q already", to)
	}
	name := m.Name
	return a.runMachine(name, "rename", func(ctx context.Context, hc *core.HC) (any, error) {
		return nil, hc.MachineRename(ctx, name, to)
	}, func(_ any, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		delete(a.mach.ops, name)
		if id := a.lastSpace[name]; id != "" {
			// Its spaces come back under the new name.
			a.lastSpace[to] = layout.Qualify(layout.Local(id, name), to)
			delete(a.lastSpace, name)
		}
		a.notify("Renamed " + name + " to " + to)
		return map[string]string{"name": to}, nil
	}), nil
}

func (a *App) cmdMachineRemove(v args) (any, error) {
	m, err := a.machineArg(v)
	if err != nil {
		return nil, err
	}
	name := m.Name
	return a.runMachine(name, "rm", func(ctx context.Context, hc *core.HC) (any, error) {
		return nil, hc.MachineRemove(ctx, name)
	}, func(_ any, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		delete(a.mach.ops, name)
		delete(a.lastSpace, name)
		a.notify("Removed " + name + "; its terminals keep running there")
		return map[string]string{"removed": name}, nil
	}), nil
}

func (a *App) cmdMachineUpdate(v args) (any, error) {
	m, err := a.machineArg(v)
	if err != nil {
		return nil, err
	}
	name := m.Name
	return a.runMachine(name, "update", func(ctx context.Context, hc *core.HC) (any, error) {
		return hc.MachineUpdate(ctx, name)
	}, func(v any, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		version, _ := v.(string)
		msg := "Updated " + name
		if version != "" {
			msg += " to hc " + version
		}
		a.machineOp(name).msg = msg + "; its terminals kept running."
		return map[string]string{"version": version}, nil
	}), nil
}

// cmdMachineLogin logs in to a machine again in a tab, where SSH asks.
func (a *App) cmdMachineLogin(v args) (any, error) {
	m, err := a.machineArg(v)
	if err != nil {
		return nil, err
	}
	argv, err := a.hcRunner().Argv("machine", "login", m.Name)
	if err != nil {
		return nil, err
	}
	return a.localTab(argv, "Log in to "+m.Name)
}
