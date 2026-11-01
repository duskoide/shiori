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
// the status bar counts the machines and opens their page.
func TestMachinesInTheWindow(t *testing.T) {
	a, f := machineApp(t)
	f.runOK = true
	f.machines = append(f.machines, core.Machine{Name: "pve", State: core.MachineNeedsLogin, Why: "the SSH key was refused"})
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	tt := uxTester(a)
	for _, s := range []string{"Machine This computer", "Machine mac", "Machine pve", "Space remote work", "1 machine"} {
		if s == "1 machine" {
			s = "2 machines"
		}
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

	mustClick(t, tt, "Machines")
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
