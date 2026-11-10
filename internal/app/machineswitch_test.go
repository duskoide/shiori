package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
)

// switchApp is machineApp with a second space on each machine ("s2" here,
// "mac/sp8" on mac) and a machine without spaces, "pve", that needs a
// login.
func switchApp(t *testing.T) (*App, *fakeCore) {
	t.Helper()
	a, f := machineApp(t)
	f.runOK = true
	s := a.doc.AddSpace("two", "/src/two")
	s.ID = "s2"
	s.AddTab("", "t2")
	m := a.doc.AddSpaceOn("mac", "other", "/home/me/other")
	m.ID = "mac/sp8"
	m.AddTab("", "mac/t8")
	a.fixView()
	f.machines = append(f.machines, core.Machine{Name: "pve", State: core.MachineNeedsLogin, Why: "the SSH key was refused"})
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	a.activeSpace = "s1"
	a.noteSpace()
	return a, f
}

func itemIDs(items []paletteItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.id)
	}
	return out
}

// Going to a machine shows the space last shown there.
func TestMachineSelectRemembersTheSpace(t *testing.T) {
	a, _ := switchApp(t)
	if _, err := a.do("machine.select", args{"name": "mac"}); err != nil || a.activeSpace != "mac/sp9" {
		t.Fatal(a.activeSpace, err)
	}
	a.activeSpace = "mac/sp8" // as a click in the sidebar
	if _, err := a.do("machine.select", args{"name": ""}); err != nil || a.activeSpace != "s1" {
		t.Fatal(a.activeSpace, err)
	}
	if r, err := a.do("machine.select", args{"index": 2}); err != nil || a.activeSpace != "mac/sp8" || r.(map[string]string)["space"] != "mac/sp8" {
		t.Fatal(a.activeSpace, r, err)
	}
	// This computer by its names; index 1 is it.
	for _, v := range []args{{"name": "local"}, {"index": 1}} {
		a.activeSpace = "mac/sp8"
		if _, err := a.do("machine.select", v); err != nil || a.activeSpace != "s1" {
			t.Fatal(v, a.activeSpace, err)
		}
	}
	// A space that was closed: its machine's first.
	a.lastSpace["mac"] = "mac/gone"
	if _, err := a.do("machine.select", args{"name": "mac"}); err != nil || a.activeSpace != "mac/sp9" {
		t.Fatal(a.activeSpace, err)
	}
	if _, err := a.do("machine.select", args{"name": "nowhere"}); err == nil {
		t.Fatal("went to a machine that is not there")
	}
	if _, err := a.do("machine.select", nil); err == nil {
		t.Fatal("no name and no index, yet no error")
	}
	if _, err := a.do("machine.select", args{"index": 9}); err != nil || a.activeSpace != "mac/sp9" {
		t.Fatal("an index past the last is not nothing", err)
	}
}

// Next and previous go round the machines that have spaces.
func TestMachineNextAndPrev(t *testing.T) {
	a, f := switchApp(t)
	for _, want := range []string{"mac/sp9", "s1", "mac/sp9"} {
		if _, err := a.do("machine.next", nil); err != nil || a.activeSpace != want {
			t.Fatal(a.activeSpace, want, err)
		}
	}
	if _, err := a.do("machine.prev", nil); err != nil || a.activeSpace != "s1" {
		t.Fatal(a.activeSpace, err)
	}
	// pve has no spaces: it is passed over, and nothing starts there.
	if len(f.runs) != 0 {
		t.Fatalf("%+v", f.runs)
	}
	// A machine that is not connected still shows its spaces.
	f.machines[0].State = "connecting"
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	if _, err := a.do("machine.next", nil); err != nil || a.activeSpace != "mac/sp9" {
		t.Fatal(a.activeSpace, err)
	}
	a.doc.Drop("mac")
	a.fixView()
	if _, err := a.do("machine.next", nil); err == nil || !strings.Contains(err.Error(), "no other machine has spaces") {
		t.Fatal(err)
	}
}

// Going to a machine without spaces makes one there, when it can.
func TestMachineSelectMakesASpace(t *testing.T) {
	a, f := switchApp(t)
	if _, err := a.do("machine.select", args{"name": "pve"}); err == nil || !strings.Contains(err.Error(), "log in") {
		t.Fatal(err)
	}
	f.machines[2].State = core.MachineConnected
	f.put(t, "pve", &layout.Document{})
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	until(t, a, "pve's document", func() bool { return a.machineReady("pve") == nil })
	r, err := a.do("machine.select", args{"name": "pve"})
	if err != nil {
		t.Fatal(err)
	}
	id := r.(map[string]string)["space"]
	until(t, a, "the space on pve", func() bool { return a.doc.Space(id) != nil })
	if a.activeSpace != id || a.doc.Space(id).Machine != "pve" || f.runs[0].On != "pve" {
		t.Fatal(a.activeSpace, f.runs)
	}
}

// A tab on a machine goes to the space last shown there, and is shown.
func TestTabNewOnMachine(t *testing.T) {
	a, f := switchApp(t)
	a.lastSpace["mac"] = "mac/sp8"
	r, err := a.do("tab.new", args{"machine": "mac", "name": "logs"})
	if err != nil {
		t.Fatal(err)
	}
	id := r.(map[string]string)["tab"]
	until(t, a, "the tab", func() bool { return len(a.doc.Space("mac/sp8").Tabs) == 2 })
	if a.activeSpace != "mac/sp8" || a.activeTab["mac/sp8"] != id || f.runs[0].On != "mac" || f.runs[0].Cwd != "/home/me/other" {
		t.Fatalf("%s %+v", a.activeSpace, f.runs[0])
	}
	// On this computer, from another machine's space.
	if _, err := a.do("tab.new", args{"machine": ""}); err != nil {
		t.Fatal(err)
	}
	until(t, a, "the tab here", func() bool { return len(a.doc.Space("s1").Tabs) == 2 })
	if a.activeSpace != "s1" || f.runs[1].On != "" {
		t.Fatalf("%s %+v", a.activeSpace, f.runs[1])
	}
	if _, err := a.do("tab.new", args{"machine": "mac", "space": "s1"}); err == nil {
		t.Fatal("a tab in a space of another machine than the one named")
	}
	if _, err := a.do("tab.new", args{"machine": "pve"}); err == nil || !strings.Contains(err.Error(), "log in") {
		t.Fatal(err)
	}
	if _, err := a.do("tab.new", args{"machine": "nowhere"}); err == nil {
		t.Fatal("a tab on a machine that is not there")
	}
	// A machine without spaces gets exactly one, with the tab.
	a.doc.Drop("mac")
	a.fixView()
	n := len(f.runs)
	r, err = a.do("tab.new", args{"machine": "mac", "name": "first"})
	if err != nil {
		t.Fatal(err)
	}
	sid := r.(map[string]string)["space"]
	until(t, a, "the space", func() bool { return a.doc.Space(sid) != nil })
	if a.spacesOn("mac") != 1 || len(f.runs) != n+1 || a.doc.Space(sid).Tabs[0].Name != "first" {
		t.Fatalf("%d spaces, %d runs", a.spacesOn("mac"), len(f.runs)-n)
	}
}

// The switcher lists the machines, what starts on each, and what to add.
func TestMachineSwitcherRows(t *testing.T) {
	a, _ := switchApp(t)
	t.Setenv("HOME", t.TempDir())
	if _, err := a.do("machine.switch", nil); err != nil {
		t.Fatal(err)
	}
	a.palette.hosts = []string{"mac", "nas"} // mac is added already
	items := a.paletteItems()
	want := []string{"machine:", "machine:mac", "machine:pve", "tab:", "space:", "tab:mac", "space:mac", "login:pve", "add", "add:nas", "manage"}
	if got := itemIDs(items); !slices.Equal(got, want) {
		t.Fatalf("rows\n%q\nwant\n%q", got, want)
	}
	if items[0].title != "This computer" || items[0].detail != "2 spaces · shown" || items[1].detail != "2 spaces" || items[2].detail != "no spaces yet · needs login" {
		t.Fatalf("%q %q %q", items[0].detail, items[1].detail, items[2].detail)
	}
	if items[3].title != "New tab on This computer" || items[5].detail != "in remote work" {
		t.Fatalf("%q %q", items[3].title, items[5].detail)
	}
	// Enter goes to the next machine with something to show.
	if a.palette.sel != 1 {
		t.Fatal(a.palette.sel)
	}
	a.activeSpace = "mac/sp8"
	a.do("machine.switch", nil)
	if a.palette.sel != 0 { // pve cannot be gone to: round to this computer
		t.Fatal(a.palette.sel)
	}
	// What starts on the machine looked at comes first.
	if got := itemIDs(a.paletteItems()); got[3] != "tab:mac" || got[5] != "tab:" {
		t.Fatal(got)
	}
	a.palette.query = "new space"
	if got := itemIDs(a.paletteItems()); !slices.Equal(got, []string{"space:mac", "space:"}) {
		t.Fatal(got)
	}
	// Alone, Enter adds a machine.
	b, _ := testApp(t)
	b.do("machine.switch", nil)
	// No space yet: no tab row.
	if got := itemIDs(b.paletteItems()); !slices.Equal(got, []string{"machine:", "space:", "add", "manage"}) || b.palette.sel != 2 {
		t.Fatal(got, b.palette.sel)
	}
}

// Prefix M and Enter toggle between two machines; the rows start work.
func TestMachineSwitcherInTheWindow(t *testing.T) {
	a, f := switchApp(t)
	t.Setenv("HOME", t.TempDir())
	tt := uxTester(a)
	toggle := func(want string) {
		t.Helper()
		tt.Key(ui.Ctrl, ui.KeyB)
		settle(tt)
		tt.Key(0, ui.KeyM)
		settle(tt)
		if !a.palette.open || !a.palette.machines {
			t.Fatal("the machine switcher did not open")
		}
		tt.Key(0, ui.KeyEnter)
		settle(tt)
		if a.palette.open || a.activeSpace != want {
			t.Fatalf("open %v, at %s, want %s", a.palette.open, a.activeSpace, want)
		}
	}
	toggle("mac/sp9")
	a.activeSpace = "mac/sp8" // another space of mac
	settle(tt)
	toggle("s1")
	toggle("mac/sp8")

	// ] and [ go round the machines.
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	tt.Key(0, ui.KeyBracketRight)
	settle(tt)
	if a.activeSpace != "s1" {
		t.Fatal(a.activeSpace)
	}
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	tt.Key(0, ui.KeyBracketLeft)
	settle(tt)
	if a.activeSpace != "mac/sp8" {
		t.Fatal(a.activeSpace)
	}

	a.do("machine.switch", nil)
	settle(tt)
	for _, s := range []string{"Machines", "This computer", "New tab on mac", "New space on This computer", "Log in to pve", "Add a machine…", "Manage machines…"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	shot(t, tt, "machine-switcher")
	// Esc leaves it, and the palette after is the commands' again.
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if a.palette.open {
		t.Fatal("Esc did not close the switcher")
	}
	a.openPalette("")
	if a.palette.machines {
		t.Fatal("the palette stayed the switcher")
	}
	a.palette = paletteState{}
	settle(tt)

	a.do("machine.switch", nil)
	settle(tt)
	mustClick(t, tt, "New tab on This computer")
	until(t, a, "the tab here", func() bool { return len(a.doc.Space("s1").Tabs) == 2 })
	if a.activeSpace != "s1" || f.runs[0].On != "" {
		t.Fatalf("%s %+v", a.activeSpace, f.runs)
	}
	a.do("machine.switch", nil)
	settle(tt)
	tt.Type("new space on mac") // the sidebar has a button of that name too
	settle(tt)
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	until(t, a, "the space on mac", func() bool { return a.spacesOn("mac") == 3 })
	if a.here() != "mac" || f.runs[1].On != "mac" {
		t.Fatalf("%s %+v", a.activeSpace, f.runs)
	}
}

// Add a machine asks how SSH reaches it, and adds it.
func TestAddMachineAsks(t *testing.T) {
	a, _ := switchApp(t)
	t.Setenv("HOME", t.TempDir())
	calls := withFakeHC(t, a)
	tt := uxTester(a)
	a.do("machine.switch", nil)
	settle(tt)
	mustClick(t, tt, "Add a machine…")
	if a.prompt == nil || a.palette.open || !tt.HasText("Add") || tt.HasText("Save") {
		t.Fatal("no question")
	}
	// Left without an answer, nothing is added.
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if a.prompt != nil || len(a.mach.ops) != 0 {
		t.Fatalf("prompt %v ops %v", a.prompt, a.mach.ops)
	}
	if r, err := a.do("machine.add", nil); r != nil || err != nil || a.prompt == nil {
		t.Fatal(r, err)
	}
	settle(tt)
	tt.Type("me@box")
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	until(t, a, "the adding", func() bool { op := a.mach.ops["me@box"]; return op != nil && op.busy == "" })
	if got := calls(); !slices.Equal(got, []string{"machine add me@box --json"}) {
		t.Fatal(got)
	}
	settle(tt)
	if !tt.HasText("Added me@box") {
		t.Fatal(tt.Texts())
	}
	// A bad answer is a message, not a silence.
	a.do("machine.add", nil)
	settle(tt)
	tt.Type("bad@box")
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	until(t, a, "the failure", func() bool { op := a.mach.ops["bad@box"]; return op != nil && op.busy == "" })
	for i := 0; i < 20 && !tt.HasText("cannot reach box"); i++ {
		a.runPosted()
		settle(tt)
	}
	if !tt.HasText("cannot reach box") {
		t.Fatal(tt.Texts())
	}
}

// A machine renamed keeps the space last shown there; one removed loses it.
func TestMachineRenameKeepsItsSpace(t *testing.T) {
	a, _ := switchApp(t)
	withFakeHC(t, a)
	a.lastSpace["mac"] = "mac/sp8"
	if _, err := doWait(t, a, "machine.rename", args{"name": "mac", "to": "laptop"}); err != nil {
		t.Fatal(err)
	}
	if a.lastSpace["laptop"] != "laptop/sp8" || a.lastSpace["mac"] != "" {
		t.Fatal(a.lastSpace)
	}
}

// A person's new space is on the machine they look at; a script's on this
// one unless it says.
func TestNewSpaceByKeyIsOnTheShownMachine(t *testing.T) {
	a, f := switchApp(t)
	a.activeSpace = "mac/sp9"
	a.doAction("space.new", "")
	until(t, a, "the space on mac", func() bool { return a.spacesOn("mac") == 3 })
	if f.runs[0].On != "mac" {
		t.Fatalf("%+v", f.runs[0])
	}
	a.activeSpace = "mac/sp9"
	if _, err := a.do("space.new", nil); err != nil {
		t.Fatal(err)
	}
	until(t, a, "the space here", func() bool { return a.spacesOn("") == 3 })
	if f.runs[1].On != "" {
		t.Fatalf("%+v", f.runs[1])
	}
}
