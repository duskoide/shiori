package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
)

func macSpace(name string) *layout.Document {
	d := &layout.Document{}
	s := d.AddSpace(name, "/home/me/src")
	s.ID = "sp9"
	t, _ := s.AddTab("", "t7")
	t.ID = "tb9"
	t.Root.ID = "tb9"
	return d
}

func spaceIDs(d *layout.Document) []string {
	var out []string
	for _, s := range d.Spaces {
		out = append(out, s.ID)
	}
	return out
}

// machineApp is a synced app whose core knows machine "mac", connected,
// with a document of one space.
func machineApp(t *testing.T) (*App, *fakeCore) {
	t.Helper()
	a, f := syncApp(t, oneSpace(), live("t1", "s1", "tab1"), live("mac/t7", "sp9", "tb9"))
	f.put(t, "mac", macSpace("remote work"))
	f.machines = []core.Machine{
		{Name: "mac", State: core.MachineConnected, SinceMs: 100},
		{Name: "sandbox", State: core.MachineConnected, Temporary: true},
	}
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	until(t, a, "mac's document", func() bool { return a.doc.Space("mac/sp9") != nil })
	return a, f
}

func TestMachinesSpacesAreShown(t *testing.T) {
	a, f := machineApp(t)
	if got := spaceIDs(a.doc); !slices.Equal(got, []string{"s1", "mac/sp9"}) {
		t.Fatal(got)
	}
	s := a.doc.Space("mac/sp9")
	if s.Machine != "mac" || s.Tabs[0].ID != "mac/tb9" || s.Tabs[0].Panes()[0].Content.Terminal != "mac/t7" {
		t.Fatalf("%+v", s.Tabs[0].Panes()[0].Content)
	}
	// Temporary machines are left out, as herdr-app does.
	if len(a.machines) != 1 || a.machines[0].Name != "mac" || a.docs["sandbox"] != nil {
		t.Fatalf("%+v", a.machines)
	}
	f.mu.Lock()
	gets := f.gets["sandbox"]
	f.mu.Unlock()
	if gets != 0 {
		t.Fatal("read a temporary machine's document")
	}
	// Nothing of mac's goes into this machine's document.
	a.doc.Spaces[0].Name = "edited"
	a.changed()
	until(t, a, "the save", func() bool { return f.stored(t).Spaces[0].Name == "edited" })
	if got := spaceIDs(f.stored(t)); !slices.Equal(got, []string{"s1"}) {
		t.Fatal(got)
	}
}

// An edit of a machine's space goes to that machine's document, in its
// own ids.
func TestMachineEditIsSavedThere(t *testing.T) {
	a, f := machineApp(t)
	before := f.stored(t)
	a.doc.Space("mac/sp9").Name = "renamed"
	a.changed()
	until(t, a, "mac's save", func() bool {
		d := f.storedOn(t, "mac")
		return len(d.Spaces) == 1 && d.Spaces[0].Name == "renamed"
	})
	d := f.storedOn(t, "mac")
	if d.Spaces[0].ID != "sp9" || d.Spaces[0].Tabs[0].ID != "tb9" || d.Spaces[0].Tabs[0].Panes()[0].Content.Terminal != "t7" {
		t.Fatalf("%+v", d.Spaces[0])
	}
	if !layout.Equal(f.stored(t), before) {
		t.Fatal("this machine's document changed")
	}
	until(t, a, "the reply", func() bool { return !a.docs["mac"].saving })
	if ds := a.docs["mac"]; ds.version != 2 || ds.dirty {
		t.Fatalf("%+v", ds)
	}
}

// A machine connected again (its since_ms moved) is read again; another
// app's change to it shows.
func TestMachineReconnectRereads(t *testing.T) {
	a, f := machineApp(t)
	f.put(t, "mac", macSpace("changed there"))
	ms, _ := f.Machines(nil)
	a.setMachines(ms) // same since_ms: no read
	a.runPosted()
	if a.doc.Space("mac/sp9").Name != "remote work" {
		t.Fatal("read again without a reconnect")
	}
	f.machines[0].SinceMs = 200
	ms, _ = f.Machines(nil)
	a.setMachines(ms)
	until(t, a, "the new copy", func() bool { return a.doc.Space("mac/sp9").Name == "changed there" })
	if a.docs["mac"].since != 200 {
		t.Fatal(a.docs["mac"].since)
	}
}

// A machine not connected keeps its spaces shown, and its edits wait; one
// that leaves the list takes its spaces away.
func TestMachineDisconnectAndRemove(t *testing.T) {
	a, f := machineApp(t)
	f.machines[0].State = "connecting"
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	if a.doc.Space("mac/sp9") == nil {
		t.Fatal("a disconnected machine's spaces went")
	}
	a.doc.Space("mac/sp9").Name = "offline edit"
	a.changed()
	a.flush()
	a.runPosted()
	if f.storedOn(t, "mac").Spaces[0].Name != "remote work" {
		t.Fatal("saved to a machine not connected")
	}
	f.machines[0].State = core.MachineConnected
	ms, _ = f.Machines(nil)
	a.setMachines(ms)
	until(t, a, "the waiting save", func() bool { return f.storedOn(t, "mac").Spaces[0].Name == "offline edit" })

	f.machines = nil
	a.setMachines(nil)
	if a.doc.Space("mac/sp9") != nil || a.docs["mac"] != nil {
		t.Fatal("a removed machine's spaces stay")
	}
	if got := spaceIDs(a.doc); !slices.Equal(got, []string{"s1"}) {
		t.Fatal(got)
	}
}

// A conflict on a machine takes that machine's copy and leaves the rest.
func TestMachineConflict(t *testing.T) {
	a, f := machineApp(t)
	f.put(t, "mac", macSpace("theirs")) // version 2, unseen
	a.doc.Space("mac/sp9").Name = "mine"
	a.doc.Spaces[0].Name = "local edit"
	a.changed()
	until(t, a, "the conflict", func() bool { return a.docs["mac"].version == 2 && !a.docs["mac"].saving })
	if a.doc.Space("mac/sp9").Name != "theirs" {
		t.Fatal(a.doc.Space("mac/sp9").Name)
	}
	until(t, a, "this machine's save", func() bool { return f.stored(t).Spaces[0].Name == "local edit" })
	if a.doc.Spaces[0].Name != "local edit" {
		t.Fatal("this machine's edit was lost")
	}
}

// Moves keep a machine's spaces together, and a new space of this
// machine's comes before theirs.
func TestMachineSpacesStayTogether(t *testing.T) {
	a, _ := machineApp(t)
	if _, err := a.do("space.move", args{"id": "s1", "index": float64(5)}); err != nil {
		t.Fatal(err)
	}
	if got := spaceIDs(a.doc); !slices.Equal(got, []string{"s1", "mac/sp9"}) {
		t.Fatal(got)
	}
	s := a.doc.AddSpace("new", "/tmp")
	if got := spaceIDs(a.doc); !slices.Equal(got, []string{"s1", s.ID, "mac/sp9"}) {
		t.Fatal(got)
	}
}

func TestMachineDrainSavesThere(t *testing.T) {
	a, f := machineApp(t)
	a.doc.Space("mac/sp9").Name = "last"
	a.changed()
	a.drain()
	if f.storedOn(t, "mac").Spaces[0].Name != "last" {
		t.Fatal("the last edit of mac was lost")
	}
}

// A space made on another machine starts its terminal there, in its home,
// labelled as that machine's document names the space and tab.
func TestSpaceOnMachine(t *testing.T) {
	a, f := machineApp(t)
	f.runOK = true
	res, err := a.do("space.new", args{"machine": "mac", "argv": []any{"claude"}, "task": "fix it"})
	if err != nil {
		t.Fatal(err)
	}
	ids := res.(map[string]string)
	if layout.MachineOf(ids["space"]) != "mac" || layout.MachineOf(ids["tab"]) != "mac" {
		t.Fatal(ids)
	}
	until(t, a, "the space", func() bool { return a.doc.Space(ids["space"]) != nil })
	f.mu.Lock()
	run := f.runs[0]
	f.mu.Unlock()
	if run.On != "mac" || run.Cwd != "" || run.Labels["herdr.space"] != layout.Local(ids["space"], "mac") ||
		run.Labels["herdr.tab"] != layout.Local(ids["tab"], "mac") || run.Labels["herdr.seen"] != "0" {
		t.Fatalf("%+v", run)
	}
	// An agent runs in that machine's shell, not this one's.
	if len(run.Argv) != 6 || run.Argv[0] != "sh" || run.Argv[3] != "sh" || run.Argv[4] != "claude" || run.Argv[5] != "fix it" {
		t.Fatalf("%q", run.Argv)
	}
	s := a.doc.Space(ids["space"])
	if s.Machine != "mac" || s.Name != "mac" || s.Tabs[0].ID != ids["tab"] || s.Tabs[0].Panes()[0].Content.Terminal != "mac/t20" {
		t.Fatalf("%+v %+v", s, s.Tabs[0].Panes()[0].Content)
	}
	if got := spaceIDs(a.doc); got[0] != "s1" || got[len(got)-1] != ids["space"] {
		t.Fatal(got)
	}
	until(t, a, "mac's save", func() bool { return len(f.storedOn(t, "mac").Spaces) == 2 })
	saved := f.storedOn(t, "mac").Spaces[1]
	if saved.ID != layout.Local(ids["space"], "mac") || saved.Tabs[0].Panes()[0].Content.Terminal != "t20" {
		t.Fatalf("%+v", saved)
	}

	// A tab in it runs there too, in the space's folder when the focused
	// pane is elsewhere.
	a.doc.Space("mac/sp9").Folder = "/home/me/src"
	a.activeSpace = "s1"
	if _, err := a.do("tab.new", args{"space": "mac/sp9"}); err != nil {
		t.Fatal(err)
	}
	until(t, a, "the tab", func() bool { return len(a.doc.Space("mac/sp9").Tabs) == 2 })
	f.mu.Lock()
	run = f.runs[1]
	f.mu.Unlock()
	if run.On != "mac" || run.Cwd != "/home/me/src" || run.Labels["herdr.space"] != "sp9" || run.Argv != nil {
		t.Fatalf("%+v", run)
	}
	if tab := a.doc.Space("mac/sp9").Tabs[1]; layout.MachineOf(tab.ID) != "mac" || tab.Panes()[0].Content.Terminal != "mac/t21" {
		t.Fatalf("%+v", tab)
	}
}

func TestSpaceOnMachineNotReady(t *testing.T) {
	a, f := machineApp(t)
	f.runOK = true
	if _, err := a.do("space.new", args{"machine": "nowhere"}); err == nil {
		t.Fatal("made a space on a machine the core does not know")
	}
	if _, err := a.do("space.new", args{"machine": "sandbox"}); err == nil {
		t.Fatal("made a space on a temporary machine")
	}
	f.machines[0].State = core.MachineNeedsLogin
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	_, err := a.do("tab.new", args{"space": "mac/sp9"})
	if err == nil || err.Error() != "mac needs you to log in again (hc machine login mac)" {
		t.Fatal(err)
	}
	if len(f.runs) != 0 {
		t.Fatal(f.runs)
	}
	// "local" and this host's name are this machine.
	if _, err := a.do("space.new", args{"machine": "local"}); err != nil {
		t.Fatal(err)
	}
}

// A terminal of another machine labelled with one of its spaces, and
// shown by no pane, gets one there; one that finished closes its pane.
func TestMachineTerminalsAdoptedAndClosed(t *testing.T) {
	a, f := machineApp(t)
	orphan := live("mac/t30", "sp9", "tb9")
	f.mu.Lock()
	f.terms["mac/t30"] = orphan
	f.terms["mac/t7"] = live("mac/t7", "sp9", "tb9")
	f.mu.Unlock()
	a.setTerminals([]core.Terminal{live("t1", "s1", "tab1"), live("mac/t7", "sp9", "tb9"), orphan})
	if _, ok := a.sy.orphanSince["mac/t30"]; !ok {
		t.Fatal("the remote orphan is not tracked")
	}
	a.sy.orphanSince["mac/t30"] = time.Now().Add(-adoptGrace)
	a.reconcile()
	until(t, a, "the adoption", func() bool { _, _, p := a.doc.PaneOfTerminal("mac/t30"); return p != nil })
	s, tab, _ := a.doc.PaneOfTerminal("mac/t30")
	if s.ID != "mac/sp9" || tab.ID != "mac/tb9" {
		t.Fatal(s.ID, tab.ID)
	}
	until(t, a, "mac's save", func() bool { _, _, p := f.storedOn(t, "mac").PaneOfTerminal("t30"); return p != nil })

	a.setTerminals([]core.Terminal{live("t1", "s1", "tab1"), {Address: "mac/t7", State: "finished"}, orphan})
	if _, _, p := a.doc.PaneOfTerminal("mac/t7"); p != nil {
		t.Fatal("a finished remote terminal's pane stays")
	}
	until(t, a, "mac's save", func() bool { _, _, p := f.storedOn(t, "mac").PaneOfTerminal("t7"); return p == nil })

	// While mac is not connected, its terminals are not judged.
	f.machines[0].State = "connecting"
	ms, _ := f.Machines(nil)
	a.setMachines(ms)
	a.setTerminals([]core.Terminal{live("t1", "s1", "tab1"), {Address: "mac/t30", State: "finished"}})
	if _, _, p := a.doc.PaneOfTerminal("mac/t30"); p == nil {
		t.Fatal("closed a pane of a machine not connected")
	}
}

// terminal.keep puts another machine's terminal in a space of that
// machine, labelled in its ids.
func TestKeepMachineTerminal(t *testing.T) {
	a, f := machineApp(t)
	a.activeSpace = "s1"
	rec := core.Terminal{Address: "mac/t40", State: "live", Labels: map[string]string{}}
	f.mu.Lock()
	f.terms["mac/t40"] = rec
	f.mu.Unlock()
	a.setTerminals([]core.Terminal{live("t1", "s1", "tab1"), live("mac/t7", "sp9", "tb9"), rec})
	if _, err := a.do("terminal.keep", args{"terminal": "mac/t40"}); err != nil {
		t.Fatal(err)
	}
	until(t, a, "the pane", func() bool { _, _, p := a.doc.PaneOfTerminal("mac/t40"); return p != nil })
	s, tab, _ := a.doc.PaneOfTerminal("mac/t40")
	f.mu.Lock()
	l := f.labels["mac/t40"]
	f.mu.Unlock()
	if s.ID != "mac/sp9" || l["herdr.space"] != "sp9" || l["herdr.tab"] != layout.Local(tab.ID, "mac") || layout.MachineOf(tab.ID) != "mac" {
		t.Fatal(s.ID, tab.ID, l)
	}
	if _, err := a.do("terminal.keep", args{"terminal": "box/t1"}); err == nil {
		t.Fatal("kept a terminal of a machine the core does not list")
	}
}

// The remote launch runs the command in the machine's login shell with
// its words as they are, then stays in that shell.
func TestRemoteLaunch(t *testing.T) {
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(sh); err != nil {
			continue
		}
		home := t.TempDir()
		os.WriteFile(filepath.Join(home, ".profile"), []byte("echo '<loaded>'\n"), 0o644)
		os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("echo '<loaded>'\n"), 0o644)
		argv := remoteLaunch([]string{"printf", "<%s>\\n", "a 'quoted' task; $(not-a-command)", ""})
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = []string{"HOME=" + home, "SHELL=" + sh, "PATH=/usr/bin:/bin", "ENV=" + filepath.Join(home, ".profile")}
		cmd.Stdin = strings.NewReader("") // the shell that stays ends at once
		out, _ := cmd.CombinedOutput()
		if !strings.Contains(string(out), "<loaded>") || !strings.Contains(string(out), "<a 'quoted' task; $(not-a-command)>\n<>\n") {
			t.Fatalf("%s: %q", sh, out)
		}
	}
}
