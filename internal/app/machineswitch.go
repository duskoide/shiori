package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
)

// Machine switching: the window looks at one space, and so at one machine.
// Going to a machine shows the space last shown there, as going to a space
// shows its last tab. The switcher lists the machines to go to, and what
// can be started from it: a tab or a space on any of them, or a machine
// to add.

// machineTitle is a machine's name for people ("" is this one).
func machineTitle(name string) string {
	if name == "" {
		return "This computer"
	}
	return name
}

// here is the machine of the shown space.
func (a *App) here() string {
	if s := a.space(); s != nil {
		return s.Machine
	}
	return ""
}

// noteSpace remembers the shown space as its machine's.
func (a *App) noteSpace() {
	if s := a.space(); s != nil && a.lastSpace[s.Machine] != s.ID {
		a.lastSpace[s.Machine] = s.ID
	}
}

// machineSpace is the space to show of a machine: the one last shown
// there, else its first, or nil when it has none.
func (a *App) machineSpace(machine string) *layout.Space {
	if a.doc == nil {
		return nil
	}
	if s := a.doc.Space(a.lastSpace[machine]); s != nil && s.Machine == machine {
		return s
	}
	for _, s := range a.doc.Spaces {
		if s.Machine == machine {
			return s
		}
	}
	return nil
}

func (a *App) spacesOn(machine string) int {
	n := 0
	if a.doc != nil {
		for _, s := range a.doc.Spaces {
			if s.Machine == machine {
				n++
			}
		}
	}
	return n
}

// machineNamed is the machine a command's argument names, "" for this one.
func (a *App) machineNamed(name string) (string, error) {
	if _, listed := a.machine(name); !listed && localMachine(name) {
		return "", nil
	}
	if a.doc != nil && slices.Contains(a.spaceGroups(), name) {
		return name, nil
	}
	return "", fmt.Errorf("no machine %q: hc machine ls lists them (temporary machines stay out of Shiori)", name)
}

// goMachine shows a machine: the space last shown there, or, when it has
// none, a new one.
func (a *App) goMachine(machine string) (any, error) {
	a.noteSpace()
	if s := a.machineSpace(machine); s != nil {
		a.activeSpace = s.ID
		a.lastSpace[machine] = s.ID
		a.focusReq = a.focus[a.activeTab[s.ID]]
		return map[string]string{"machine": machine, "space": s.ID}, nil
	}
	if err := a.machineReady(machine); err != nil {
		return nil, err
	}
	r, err := a.cmdSpaceNew(args{"machine": machine})
	if ids, ok := r.(map[string]string); ok {
		ids["machine"] = machine
	}
	return r, err
}

func (a *App) cmdMachineSelect(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if _, named := v["name"]; named { // "" is this computer
		m, err := a.machineNamed(v.str("name"))
		if err != nil {
			return nil, err
		}
		return a.goMachine(m)
	}
	n, ok := v.int("index")
	if !ok {
		return nil, errors.New("machine.select needs name, or index (1 is this computer, then the others by name)")
	}
	groups := a.spaceGroups()
	if n < 1 || n > len(groups) {
		return nil, nil
	}
	return a.goMachine(groups[n-1])
}

// cycleMachine goes to the next or previous machine that has spaces.
func (a *App) cycleMachine(d int) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	var with []string
	for _, m := range a.spaceGroups() {
		if a.spacesOn(m) > 0 {
			with = append(with, m)
		}
	}
	n := len(with)
	if n < 2 {
		return nil, errors.New("no other machine has spaces: Switch machine makes one there, or adds a machine")
	}
	i := max(slices.Index(with, a.here()), 0)
	return a.goMachine(with[((i+d)%n+n)%n])
}

// doToast runs a command for the window's own callers: what goes wrong is
// a toast, now or once it answers.
func (a *App) doToast(name string, v args) {
	r, err := a.do(name, v)
	if err != nil {
		a.notifyLevel(err.Error(), levelError)
		return
	}
	a.reportLater(name, r)
}

func (a *App) cmdMachineSwitch(args) (any, error) {
	a.palette = paletteState{open: true, reveal: true, machines: true, hosts: sshHosts()}
	// Enter goes to the next machine, as a toggle between two; with no
	// other machine, to adding one.
	items := a.machineItems()
	if a.doc == nil {
		return nil, nil
	}
	groups := a.spaceGroups() // the first rows, in this order
	at, n := max(slices.Index(groups, a.here()), 0), len(groups)
	sel := slices.IndexFunc(items, func(it paletteItem) bool { return it.id == "add" })
	for i := 1; i < n; i++ {
		// One to go to: with spaces, or where one can be made.
		if j := (at + i) % n; a.spacesOn(groups[j]) > 0 || a.machineReady(groups[j]) == nil {
			sel = j
			break
		}
	}
	a.palette.sel = max(sel, 0)
	return nil, nil
}

// machineItems are the switcher's rows: the machines, then what can be
// started on each, then machines to add.
func (a *App) machineItems() []paletteItem {
	if a.doc == nil {
		return nil
	}
	here := a.here()
	groups := a.spaceGroups()
	var out []paletteItem
	for _, m := range groups {
		out = append(out, paletteItem{id: "machine:" + m, icon: machineIcon(m), title: machineTitle(m), detail: a.machineDetail(m, m == here),
			run: func() { a.doToast("machine.select", args{"name": m}) }})
	}
	// What starts on the machine looked at comes first.
	order := append([]string{here}, slices.DeleteFunc(slices.Clone(groups), func(m string) bool { return m == here })...)
	for _, m := range order {
		if a.ready() != nil || a.machineReady(m) != nil {
			continue
		}
		tabKeys, spaceKeys := "", ""
		if m == here {
			tabKeys = a.keysFor("tab.new")
		}
		if m == "" {
			spaceKeys = a.keysFor("space.new")
		}
		if s := a.machineSpace(m); s != nil {
			out = append(out, paletteItem{id: "tab:" + m, icon: "plus", title: "New tab on " + machineTitle(m), detail: "in " + a.spaceLabel(s), keys: tabKeys,
				run: func() { a.doToast("tab.new", args{"machine": m}) }})
		}
		out = append(out, paletteItem{id: "space:" + m, icon: "plus", title: "New space on " + machineTitle(m), keys: spaceKeys,
			run: func() { a.doToast("space.new", args{"machine": m}) }})
	}
	for _, m := range a.machines {
		if m.State == core.MachineNeedsLogin {
			out = append(out, paletteItem{id: "login:" + m.Name, icon: "alert", title: "Log in to " + m.Name, detail: m.Why,
				run: func() { a.doToast("machine.login", args{"name": m.Name}) }})
		}
	}
	out = append(out, paletteItem{id: "add", icon: "server", title: "Add a machine…", detail: "user@host, over SSH",
		run: func() { a.doToast("machine.add", nil) }})
	for _, h := range a.palette.hosts {
		if !a.added(h) {
			out = append(out, paletteItem{id: "add:" + h, icon: "server", title: "Add " + h, detail: "from ~/.ssh/config",
				run: func() { a.doToast("machine.add", args{"target": h}) }})
		}
	}
	return append(out, paletteItem{id: "manage", icon: "settings", title: "Manage machines…", detail: "Settings → Machines", keys: a.keysFor("app.machines"),
		run: func() { a.openSettings("machines") }})
}

func machineIcon(machine string) string {
	if machine == "" {
		return "monitor"
	}
	return "server"
}

// machineDetail is a machine's row in the switcher after its name: its
// spaces, its state when it is not connected, and whether it is shown.
func (a *App) machineDetail(machine string, shown bool) string {
	var parts []string
	if n := a.spacesOn(machine); n > 0 {
		parts = append(parts, plural(n, "space", "spaces"))
	} else {
		parts = append(parts, "no spaces yet")
	}
	switch m, listed := a.machine(machine); {
	case machine == "":
		if !a.connected {
			parts = append(parts, "disconnected")
		}
	case !listed:
		parts = append(parts, "not listed")
	case !m.Usable():
		word, _ := machineWord(m)
		parts = append(parts, strings.ToLower(word))
	}
	if shown {
		parts = append(parts, "shown")
	}
	return strings.Join(parts, " · ")
}
