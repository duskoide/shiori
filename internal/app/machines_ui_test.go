package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/duskoide/shiori/internal/core"
)

// fakeHCScript stands in for hc: it logs its arguments and answers by
// them, as the real one does with --json.
const fakeHCScript = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_HC_LOG"
case "$*" in
"--version") echo "hc 0.2.0" ;;
"machine add me@box --json") ;;
"machine add new@box --json") echo '{"error":{"kind":"host_key","message":"the host key of box is not known"}}'; exit 1 ;;
"machine add bad@box --json") echo '{"error":{"kind":"unreachable","message":"cannot reach box"}}'; exit 1 ;;
"machine rename mac laptop") ;;
"machine rm mac") ;;
"machine update mac") echo '{"version":"0.2.0"}' ;;
*) echo "unexpected: $*" >&2; exit 3 ;;
esac
`

// withFakeHC gives the app a fake hc, and what it was asked.
func withFakeHC(t *testing.T, a *App) func() []string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "hc")
	if err := os.WriteFile(bin, []byte(fakeHCScript), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	a.hc = &core.HC{Bin: bin, Env: []string{"FAKE_HC_LOG=" + log}}
	return func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func TestMachineCommands(t *testing.T) {
	a, f := machineApp(t)
	f.runOK = true
	calls := withFakeHC(t, a)

	v, err := doWait(t, a, "machine.list", nil)
	if err != nil || len(v.(map[string]any)["machines"].([]map[string]any)) != 1 {
		t.Fatal(v, err)
	}
	if v, err = doWait(t, a, "machine.add", args{"target": "me@box"}); err != nil || v.(map[string]any)["added"] != "me@box" {
		t.Fatal(v, err)
	}
	if _, err = doWait(t, a, "machine.add", args{"target": "bad@box"}); err == nil || err.Error() != "cannot reach box" {
		t.Fatal(err)
	}
	if a.mach.ops["bad@box"].msg != "cannot reach box" {
		t.Fatalf("%+v", a.mach.ops["bad@box"])
	}
	if _, err = a.do("machine.add", args{"target": "mac"}); err == nil {
		t.Fatal("added a machine twice")
	}
	if _, err = a.do("machine.add", args{"target": "two words"}); err == nil {
		t.Fatal("took a target with a space")
	}

	// Adding that needs a person goes on in a tab of this machine's,
	// running hc machine add without --json.
	a.activeSpace = "mac/sp9"
	tabs := len(a.doc.Space("s1").Tabs)
	v, err = doWait(t, a, "machine.add", args{"target": "new@box"})
	if err != nil || v.(map[string]any)["in_tab"] != true {
		t.Fatal(v, err)
	}
	until(t, a, "the tab", func() bool { return len(a.doc.Space("s1").Tabs) == tabs+1 })
	f.mu.Lock()
	run := f.runs[len(f.runs)-1]
	f.mu.Unlock()
	if run.On != "" || !slices.Contains(run.Argv, a.hc.Bin) || !strings.Contains(strings.Join(run.Argv, " "), "machine add new@box") || strings.Contains(strings.Join(run.Argv, " "), "--json") {
		t.Fatalf("%+v", run)
	}

	if _, err = doWait(t, a, "machine.update", args{"name": "mac"}); err != nil || !strings.Contains(a.mach.ops["mac"].msg, "to hc 0.2.0") {
		t.Fatal(err, a.mach.ops["mac"])
	}
	if _, err = a.do("machine.rename", args{"name": "mac", "to": "has/slash"}); err == nil {
		t.Fatal("took a name with a slash")
	}
	if _, err = doWait(t, a, "machine.rename", args{"name": "mac", "to": "laptop"}); err != nil {
		t.Fatal(err)
	}
	if _, err = doWait(t, a, "machine.remove", args{"name": "mac"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.do("machine.remove", args{"name": "nowhere"}); err == nil {
		t.Fatal("removed a machine that is not there")
	}
	want := []string{"machine add me@box --json", "machine add bad@box --json", "machine add new@box --json", "machine update mac", "machine rename mac laptop", "machine rm mac"}
	if got := calls(); !slices.Equal(got, want) {
		t.Fatalf("calls\n%q\nwant\n%q", got, want)
	}
}

func TestMachineLoginInATab(t *testing.T) {
	a, f := machineApp(t)
	f.runOK = true
	withFakeHC(t, a)
	if _, err := a.do("machine.login", args{"name": "mac"}); err != nil {
		t.Fatal(err)
	}
	until(t, a, "the tab", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.runs) == 1 })
	if argv := strings.Join(f.runs[0].Argv, " "); !strings.Contains(argv, "machine login mac") || f.runs[0].On != "" {
		t.Fatal(argv)
	}
}

func TestSSHHosts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host *\n  User me\nHost pve pve2 # lab\n  HostName 10.0.0.2\nhost  work-*  desk !bad\nMatch host x\n"), 0o600)
	if got := sshHosts(); !slices.Equal(got, []string{"pve", "pve2", "desk"}) {
		t.Fatal(got)
	}
}

// The sidebar heads each machine's spaces, and its + makes a space there;
// the status bar names the shown space's machine and opens the switcher,
// whose last row is their page.
func TestMachinesInTheWindow(t *testing.T) {
	a, f := machineApp(t)
	f.runOK = true
	f.machines = append(f.machines, core.Machine{Name: "pve", State: core.MachineNeedsLogin, Why: "the SSH key was refused"})
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	tt := uxTester(a)
	for _, s := range []string{"Machine This computer", "Machine mac", "Machine pve", "Space remote work"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	shot(t, tt, "machines-sidebar")
	mustClick(t, tt, "New space on mac")
	until(t, a, "the space on mac", func() bool { return len(a.doc.Part("mac").Spaces) == 2 })
	if f.runs[0].On != "mac" {
		t.Fatalf("%+v", f.runs[0])
	}

	// The new space is shown: the status bar names its machine.
	mustClick(t, tt, "Machine of the space mac")
	if !a.palette.open || !a.palette.machines {
		t.Fatal("the machine switcher did not open")
	}
	mustClick(t, tt, "Manage machines…")
	if !a.settings || a.set.section != "machines" {
		t.Fatal("the machines page did not open")
	}
	settle(tt)
	for _, s := range []string{"Machine mac", "Machine pve", "Needs login", "the SSH key was refused", "Log in to pve", "Add machine"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	shot(t, tt, "machines-page")
	if a.machineTrouble() != "pve needs you" {
		t.Fatal(a.machineTrouble())
	}
}

func TestMachinesPageActions(t *testing.T) {
	a, _ := machineApp(t)
	calls := withFakeHC(t, a)
	a.mach.hcVersion, a.mach.versionAt = "0.2.0", time.Now()
	a.machines[0].Version = "0.1.0"
	a.openSettings("machines")
	tt := uxTester(a)
	mustClick(t, tt, "Update mac")
	frameUntil(t, tt, "the update", func() bool { return tt.HasText("Updated mac to hc 0.2.0; its terminals kept running.") })

	mustClick(t, tt, "Remove machine mac")
	if !tt.HasText("Remove mac?") {
		t.Fatal(tt.Texts())
	}
	mustClick(t, tt, "Remove mac")
	frameUntil(t, tt, "the removal", func() bool { return slices.Contains(calls(), "machine rm mac") })

	mustClick(t, tt, "Machine to add")
	tt.Type("me@box")
	mustClick(t, tt, "Add machine")
	frameUntil(t, tt, "the add", func() bool { return slices.Contains(calls(), "machine add me@box --json") })
}

// ls and status tell of machines, and a machine changing state is an
// event.
func TestMachinesInLsStatusAndEvents(t *testing.T) {
	a, f := machineApp(t)
	b, _ := json.Marshal(a.ls())
	if !strings.Contains(string(b), `"machine":"mac"`) || !strings.Contains(string(b), `"machines":[{"connected":true`) {
		t.Fatal(string(b))
	}
	b, _ = json.Marshal(a.statusReport())
	if !strings.Contains(string(b), `"layout_loaded":true`) || !strings.Contains(string(b), `"name":"mac"`) {
		t.Fatal(string(b))
	}
	old := a.snapshot()
	f.machines[0].State, f.machines[0].Why = "connecting", "the network is down"
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	evs := diffEvents(old, a.snapshot())
	if len(evs) != 1 || evs[0].name != "machine.changed" {
		t.Fatalf("%+v", evs)
	}
	old = a.snapshot()
	a.setMachines(nil)
	evs = diffEvents(old, a.snapshot())
	if last := evs[len(evs)-1]; last.name != "machine.changed" || last.data.(map[string]any)["state"] != "removed" {
		t.Fatalf("%+v", evs)
	}
}

// fakeExecScript stands in for hc exec on mac: /srv/app is a git work
// tree with a change, /srv/plain is not.
const fakeExecScript = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_HC_LOG"
b64() { printf '%s' "$1" | base64 | tr -d '\n'; }
case "$*" in
*"--cwd /srv/app -- git --no-optional-locks rev-parse"*) echo "{\"code\":0,\"stdout\":\"$(b64 '/srv/app
')\",\"stderr\":\"\"}" ;;
*"--cwd /srv/app -- git --no-optional-locks status"*) printf '{"code":0,"stdout":"%s","stderr":""}\n' "$(printf ' M main.go\000?? new.go\000' | base64 | tr -d '\n')" ;;
*"--cwd /srv/plain -- git"*) echo '{"code":128,"stdout":"","stderr":""}' ;;
*) echo '{"error":{"message":"unexpected"}}'; exit 1 ;;
esac
`

// The changes panel of another machine's space asks git there, through
// hc exec, and its rows open what they show on that machine.
func TestChangesPanelOnMachine(t *testing.T) {
	a, f := machineApp(t)
	f.runOK = true
	dir := t.TempDir()
	bin, log := filepath.Join(dir, "hc"), filepath.Join(dir, "log")
	os.WriteFile(bin, []byte(fakeExecScript), 0o755)
	a.hc = &core.HC{Bin: bin, Env: []string{"FAKE_HC_LOG=" + log}}
	a.doc.Space("mac/sp9").Folder = "/srv/app"
	a.activeSpace = "mac/sp9"
	on := true
	a.cfg.Changes = &on
	t.Cleanup(a.stopChanges)
	tt := uxTester(a)
	frameUntil(t, tt, "mac's changes", func() bool { return tt.HasText("Change main.go") && tt.HasText("Change new.go") })
	if !tt.HasText("mac:/srv/app · what differs from the last commit") {
		t.Fatal(tt.Texts())
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "exec --json --on mac --timeout 20 --cwd /srv/app -- git --no-optional-locks status") {
		t.Fatal(string(b))
	}
	// Changes seen before the panel looked have no age.
	if !a.chg.snap.Files[0].Time.IsZero() {
		t.Fatalf("%+v", a.chg.snap.Files)
	}
	if err := tt.RightClick("Change main.go"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); slices.Contains(menu, "Open in editor") || !slices.Contains(menu, "Show changes") {
		t.Fatal(menu)
	}
	tt.CloseMenu()
	mustClick(t, tt, "Change main.go")
	until(t, a, "the diff tab", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.runs) == 1 })
	if r := f.runs[0]; r.On != "mac" || r.Cwd != "/srv/app" || !strings.Contains(strings.Join(r.Argv, " "), "git diff HEAD -- main.go") {
		t.Fatalf("%+v", r)
	}

	a.doc.Space("mac/sp9").Folder = "/srv/plain"
	frameUntil(t, tt, "not in git", func() bool { return tt.HasText("Changes outside git are not shown for other machines.") })
}

func TestPathsOfMachines(t *testing.T) {
	home := homeDir()
	if pathOn("", home+"/src") != "~/src" || pathOn("mac", home+"/src") != "mac:"+home+"/src" {
		t.Fatal(pathOn("", home+"/src"), pathOn("mac", home+"/src"))
	}
	if folderName(home, "") != "~" || folderName(home, "mac") == "~" {
		t.Fatal("another machine's home is not known here")
	}
	a, _ := machineApp(t)
	a.activeSpace = "mac/sp9"
	env := strings.Join(a.modEnv(&modState{ID: "x"}), "\n")
	if !strings.Contains(env, "\nSHIORI_MACHINE=mac") || !strings.Contains(env, "\nSHIORI_SPACE=mac/sp9") {
		t.Fatal(env)
	}
}

// The status bar names the machine of the shown space, and its state.
func TestStatusBarFollowsTheSpacesMachine(t *testing.T) {
	a, f := machineApp(t)
	a.activeSpace = "s1"
	tt := uxTester(a)
	if !tt.HasText("Machine of the space This computer") {
		t.Fatal(tt.Texts())
	}
	a.activeSpace = "mac/sp9"
	settle(tt)
	if !tt.HasText("Machine of the space mac") || tt.HasText("Machine of the space This computer") {
		t.Fatal(tt.Texts())
	}
	f.machines[0].State = "connecting"
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	settle(tt)
	if !tt.HasText("mac · reconnecting") {
		t.Fatal(tt.Texts())
	}
	shot(t, tt, "status-machine")
	mustClick(t, tt, "Machine of the space mac · reconnecting")
	if !a.palette.open || !a.palette.machines {
		t.Fatal("the machine switcher did not open")
	}
	// This computer's opens it too.
	a.palette = paletteState{}
	a.activeSpace = "s1"
	settle(tt)
	mustClick(t, tt, "Machine of the space This computer")
	if !a.palette.open || !a.palette.machines {
		t.Fatal("the machine switcher did not open from this computer's")
	}
}
