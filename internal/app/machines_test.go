package app

import (
	"slices"
	"testing"

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
	a, f := syncApp(t, oneSpace(), live("t1", "s1", "tab1"))
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
