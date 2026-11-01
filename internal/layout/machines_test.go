package layout

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func spaceIDs(d *Document) []string {
	var out []string
	for _, s := range d.Spaces {
		out = append(out, s.ID)
	}
	return out
}

func TestQualify(t *testing.T) {
	if Qualify("t7", "mac") != "mac/t7" || Qualify("t7", "") != "t7" || Qualify("", "mac") != "" {
		t.Fatal("qualify")
	}
	if Local("mac/t7", "mac") != "t7" || Local("t7", "mac") != "t7" || Local("box/t7", "mac") != "box/t7" || Local("mac/t7", "") != "mac/t7" {
		t.Fatal("local")
	}
	if MachineOf("mac/t7") != "mac" || MachineOf("t7") != "" || MachineOf("mac/box/t7") != "mac" {
		t.Fatal("machineOf")
	}
}

// A machine's document goes into the window's qualified, and comes out as
// it was, other fields and all; a mod's props are the mod's.
func TestPartReplaceRoundTrip(t *testing.T) {
	raw, err := os.ReadFile("testdata/herdr-app.json")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	theirs.Spaces[0].Tabs[0].Root.Children = append(theirs.Spaces[0].Tabs[0].Root.Children,
		&Node{ID: "pm", Type: TypeBlock, Content: &Content{Mod: "notes", Props: []byte(`{"terminal":"t1"}`)}})
	stack := &Node{ID: "st", Type: TypeStack, Children: []*Node{blk("x", 1), blk("y", 1)}}
	setStackActive(stack, "y")
	theirs.Spaces[0].Tabs[0].Root.Children = append(theirs.Spaces[0].Tabs[0].Root.Children, stack)

	d := &Document{}
	d.AddSpace("here", "/src")
	d.Replace(theirs, "mac")
	if d.Machines()[0] != "mac" || d.Spaces[0].Machine != "" {
		t.Fatal(d.Machines())
	}
	s := d.Spaces[1]
	if s.Machine != "mac" || s.ID != "mac/"+theirs.Spaces[0].ID {
		t.Fatalf("%+v", s)
	}
	for _, p := range s.Tabs[0].Panes() {
		if MachineOf(p.ID) != "mac" || (p.Content.Terminal != "" && MachineOf(p.Content.Terminal) != "mac") {
			t.Fatalf("pane %s %+v", p.ID, p.Content)
		}
		if p.ID == "mac/pm" && string(p.Content.Props) != `{"terminal":"t1"}` {
			t.Fatalf("props %s", p.Content.Props)
		}
	}
	_, _, st := d.Pane("mac/st")
	if st != nil {
		t.Fatal("a stack is no pane")
	}
	if got := stackActive(findNode(s.Tabs[0].Root, "mac/st")); got != "mac/y" {
		t.Fatal(got)
	}
	// The window's own changes to its copy do not touch theirs.
	if theirs.Spaces[0].Tabs[0].Panes()[0].ID == s.Tabs[0].Panes()[0].ID {
		t.Fatal("not a copy")
	}

	back := d.Part("mac")
	back.Extra = theirs.Extra // another machine's fields are the caller's
	if !Equal(back, theirs) {
		a, _ := back.Encode()
		b, _ := theirs.Encode()
		t.Fatalf("round trip\n%s\n%s", a, b)
	}
	if local := d.Part(""); len(local.Spaces) != 1 || local.Spaces[0].Name != "here" {
		t.Fatalf("%+v", local.Spaces)
	}
	if len(d.Part("box").Spaces) != 0 {
		t.Fatal("box has no spaces")
	}
}

func findNode(n *Node, id string) *Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if f := findNode(c, id); f != nil {
			return f
		}
	}
	return nil
}

func doc(ids ...string) *Document {
	d := &Document{}
	for _, id := range ids {
		d.Spaces = append(d.Spaces, &Space{ID: id, Tabs: []*Tab{}})
	}
	return d
}

// As herdr-app: this machine's spaces first, then each machine's by name;
// a machine's spaces stay where they were when replaced, and moves stay
// inside a machine's block.
func TestMachineBlocks(t *testing.T) {
	theirs := doc("s")
	d := &Document{}
	d.Replace(theirs, "z")
	d.Replace(theirs, "a")
	d.Replace(theirs, "")
	if got := spaceIDs(d); !slices.Equal(got, []string{"s", "a/s", "z/s"}) {
		t.Fatal(got)
	}
	s2 := d.AddSpace("", "")
	if got := spaceIDs(d); !slices.Equal(got, []string{"s", s2.ID, "a/s", "z/s"}) {
		t.Fatal(got)
	}
	d.MoveSpace("s", 99)
	if got := spaceIDs(d); !slices.Equal(got, []string{s2.ID, "s", "a/s", "z/s"}) {
		t.Fatal(got)
	}
	d.MoveSpace("z/s", 0)
	if got := spaceIDs(d); got[3] != "z/s" {
		t.Fatal(got)
	}
	d.Replace(doc("s", "t"), "a")
	if got := spaceIDs(d); !slices.Equal(got, []string{s2.ID, "s", "a/s", "a/t", "z/s"}) {
		t.Fatal(got)
	}
	m := d.AddSpaceOn("a", "new", "/home/me")
	if MachineOf(m.ID) != "a" || m.Machine != "a" || d.Spaces[4] != m {
		t.Fatal(spaceIDs(d))
	}
	tab, pane := m.AddTab("", "a/t9")
	if MachineOf(tab.ID) != "a" || MachineOf(pane.ID) != "a" || tab.Root.ID != tab.ID {
		t.Fatal(tab.ID, pane.ID)
	}
	split, err := tab.Split(pane.ID, Right, "a/t10")
	if err != nil || MachineOf(split.ID) != "a" {
		t.Fatal(split, err)
	}
	d.Drop("a")
	if got := spaceIDs(d); !slices.Equal(got, []string{s2.ID, "s", "z/s"}) {
		t.Fatal(got)
	}
	d.Drop("") // never this machine's
	if len(d.Spaces) != 3 {
		t.Fatal(spaceIDs(d))
	}
}

func TestReplaceLocalTakesFields(t *testing.T) {
	d := &Document{}
	d.Replace(doc("m"), "mac")
	mine, _ := Decode([]byte(`{"spaces":[{"id":"s","tabs":[]}],"theme":"x"}`))
	d.Replace(mine, "")
	if string(d.Extra["theme"]) != `"x"` || d.Spaces[0].ID != "s" {
		t.Fatal(d.Extra, spaceIDs(d))
	}
	if p := d.Part(""); string(p.Extra["theme"]) != `"x"` {
		t.Fatal(p.Extra)
	}
	// Space.Machine is the window's: no document holds it.
	b, _ := d.Encode()
	if strings.Contains(string(b), "machine") {
		t.Fatal(string(b))
	}
}
