package layout

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// Test tree helpers. dump renders a tree compactly: R(...)/C(...)/S(...) for
// containers, id:weight for blocks (px sizes as id:50px).
func blk(id string, w float64) *Node {
	return &Node{ID: id, Type: TypeBlock, Content: &Content{Terminal: "t-" + id}, Size: weight(w)}
}

func pxBlk(id string, px float64) *Node {
	return &Node{ID: id, Type: TypeBlock, Content: &Content{Terminal: "t-" + id}, Size: &Size{Px: &px}}
}

func cont(typ, id string, w float64, kids ...*Node) *Node {
	n := &Node{ID: id, Type: typ, Children: kids}
	if w >= 0 {
		n.Size = weight(w)
	}
	return n
}

func row(id string, w float64, kids ...*Node) *Node { return cont(TypeRow, id, w, kids...) }
func col(id string, w float64, kids ...*Node) *Node { return cont(TypeColumn, id, w, kids...) }

func tabOf(root *Node) *Tab { root.ID, root.Size = "T", nil; return &Tab{ID: "T", Root: root} }

func dump(n *Node) string {
	if n == nil {
		return "nil"
	}
	var sz string
	switch {
	case n.Size == nil:
	case n.Size.Px != nil:
		sz = fmt.Sprintf(":%gpx", *n.Size.Px)
	case n.Size.Weight != nil:
		sz = fmt.Sprintf(":%.4g", *n.Size.Weight)
	}
	if n.Type == TypeBlock {
		return n.ID + sz
	}
	p := map[string]string{TypeRow: "R", TypeColumn: "C", TypeStack: "S"}[n.Type]
	var kids []string
	for _, c := range n.Children {
		kids = append(kids, dump(c))
	}
	return p + n.ID + sz + "(" + strings.Join(kids, " ") + ")"
}

func check(t *testing.T, tab *Tab, want string) {
	t.Helper()
	if got := dump(tab.Root); got != want {
		t.Fatalf("tree\n got %s\nwant %s", got, want)
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		name string
		tab  *Tab
		pane string
		dir  Direction
		want string // new pane is rendered as NEW
	}{
		{"right in row", tabOf(row("", 1, blk("a", 1))), "a", Right, "RT(a:0.5 NEW:0.5)"},
		{"left in row", tabOf(row("", 1, blk("a", 1))), "a", Left, "RT(NEW:0.5 a:0.5)"},
		{"right middle", tabOf(row("", 1, blk("a", 0.2), blk("b", 0.6), blk("c", 0.2))), "b", Right,
			"RT(a:0.2 b:0.3 NEW:0.3 c:0.2)"},
		{"down wraps", tabOf(row("", 1, blk("a", 0.5), blk("b", 0.5))), "a", Down,
			"RT(Cw:0.5(a:0.5 NEW:0.5) b:0.5)"},
		{"up wraps", tabOf(row("", 1, blk("a", 0.5), blk("b", 0.5))), "b", Up,
			"RT(a:0.5 Cw:0.5(NEW:0.5 b:0.5))"},
		{"down in column", tabOf(row("", 1, col("c", 1, blk("a", 0.4), blk("b", 0.6)))), "b", Down,
			"RT(Cc:1(a:0.4 b:0.3 NEW:0.3))"},
		{"up in column", tabOf(row("", 1, col("c", 1, blk("a", 0.4), blk("b", 0.6)))), "a", Up,
			"RT(Cc:1(NEW:0.2 a:0.2 b:0.6))"},
		{"right in column wraps in row", tabOf(row("", 1, col("c", 1, blk("a", 0.4), blk("b", 0.6)))), "a", Right,
			"RT(Cc:1(Rw:0.4(a:0.5 NEW:0.5) b:0.6))"},
		{"right in column root", tabOf(col("", 1, blk("a", 1))), "a", Right,
			"CT(Rw:1(a:0.5 NEW:0.5))"},
		{"px halves px", tabOf(row("", 1, pxBlk("a", 200), blk("b", 1))), "a", Right,
			"RT(a:100px NEW:100px b:1)"},
		{"no size counts as 1", tabOf(row("", 1, &Node{ID: "a", Type: TypeBlock})), "a", Right,
			"RT(a:0.5 NEW:0.5)"},
		{"nested", tabOf(row("", 1, blk("x", 0.5), col("c", 0.5, blk("a", 0.5), row("r", 0.5, blk("b", 0.5), blk("d", 0.5))))), "d", Right,
			"RT(x:0.5 Cc:0.5(a:0.5 Rr:0.5(b:0.5 d:0.25 NEW:0.25)))"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nb, err := tt.tab.Split(tt.pane, tt.dir, "tnew")
			if err != nil {
				t.Fatal(err)
			}
			if nb.Type != TypeBlock || nb.Content.Terminal != "tnew" || !strings.HasPrefix(nb.ID, "p") || len(nb.ID) != 33 {
				t.Fatalf("bad new pane %+v", nb)
			}
			// Normalise generated ids.
			orig := nb.ID
			nb.ID = "NEW"
			for _, n := range allNodes(tt.tab.Root) {
				if n.Type != TypeBlock && strings.HasPrefix(n.ID, "c") && len(n.ID) == 33 {
					n.ID = "w"
				}
			}
			check(t, tt.tab, tt.want)
			nb.ID = orig
		})
	}
}

func allNodes(n *Node) []*Node {
	out := []*Node{n}
	for _, c := range n.Children {
		out = append(out, allNodes(c)...)
	}
	return out
}

func TestSplitErrors(t *testing.T) {
	tab := tabOf(row("", 1, blk("a", 1)))
	if _, err := tab.Split("zz", Right, "t"); err == nil {
		t.Fatal("missing pane")
	}
	if _, err := tab.Split("T", Right, "t"); err == nil {
		t.Fatal("splitting a container")
	}
	if _, err := (&Tab{ID: "T"}).Split("a", Right, "t"); err == nil {
		t.Fatal("empty tab")
	}
}

func TestBlockRoot(t *testing.T) {
	solo := &Tab{ID: "T", Root: blk("a", 1)}
	nb, err := solo.Split("a", Left, "t")
	if err != nil {
		t.Fatal(err)
	}
	if r := solo.Root; r.Type != TypeRow || len(r.Children) != 2 || r.Children[0] != nb || r.Children[1].ID != "a" {
		t.Fatal(dump(solo.Root))
	}
	solo = &Tab{ID: "T", Root: blk("a", 1)}
	if !solo.RemovePane("a") || solo.Root != nil || len(solo.Panes()) != 0 {
		t.Fatal("removing a block root should empty the tab")
	}
}

func TestSplitStackActive(t *testing.T) {
	st := cont(TypeStack, "s", 1, blk("a", 1), blk("b", 1))
	setStackActive(st, "a")
	tab := tabOf(row("", 1, st))
	nb, err := tab.Split("a", Down, "t")
	if err != nil {
		t.Fatal(err)
	}
	if got := stackActive(st); got != st.Children[0].ID || got == "a" {
		t.Fatalf("active = %q", got)
	}
	if st.Children[0].Children[1] != nb {
		t.Fatal("new pane misplaced")
	}
}

func TestRemovePane(t *testing.T) {
	tests := []struct {
		name   string
		tab    *Tab
		remove string
		ok     bool
		want   string
	}{
		{"redistribute proportionally", tabOf(row("", 1, blk("a", 0.5), blk("b", 0.25), blk("c", 0.25))), "a", true,
			"RT(b:0.5 c:0.5)"},
		{"redistribute uneven", tabOf(row("", 1, blk("a", 0.2), blk("b", 0.2), blk("c", 0.6))), "a", true,
			"RT(b:0.25 c:0.75)"},
		{"last pane leaves empty root", tabOf(row("", 1, blk("a", 1))), "a", true, "RT()"},
		{"unknown", tabOf(row("", 1, blk("a", 1))), "x", false, "RT(a:1)"},
		{"container is not a pane", tabOf(row("", 1, col("c", 1, blk("a", 1), blk("b", 1)))), "c", false,
			"RT(Cc:1(a:1 b:1))"},
		{"collapse container to its child", tabOf(row("", 1, blk("a", 0.5), col("c", 0.5, blk("b", 0.5), blk("d", 0.5)))), "d", true,
			"RT(a:0.5 b:0.5)"},
		{"collapse then flatten", tabOf(row("", 1, blk("a", 0.5), col("c", 0.5, blk("b", 0.5), row("r", 0.5, blk("d", 0.5), blk("e", 0.5))))), "b", true,
			"RT(a:0.5 d:0.25 e:0.25)"},
		{"root with lone container child takes over", tabOf(row("", 1, blk("a", 0.5), col("c", 0.5, blk("b", 0.5), blk("d", 0.5)))), "a", true,
			"CT(b:0.5 d:0.5)"},
		{"deep cascade", tabOf(row("", 1, blk("z", 0.5), col("c", 0.5, blk("y", 0.2), row("r", 0.8, blk("a", 0.5), col("k", 0.5, blk("b", 0.5), blk("d", 0.5)))))), "b", true,
			"RT(z:0.5 Cc:0.5(y:0.2 Rr:0.8(a:0.5 d:0.5)))"},
		{"px removal leaves weights", tabOf(row("", 1, pxBlk("a", 100), blk("b", 0.5), blk("c", 0.5))), "a", true,
			"RT(b:0.5 c:0.5)"},
		{"px sibling untouched", tabOf(row("", 1, blk("a", 0.5), pxBlk("b", 100), blk("c", 0.5))), "a", true,
			"RT(b:100px c:1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tab.RemovePane(tt.remove); got != tt.ok {
				t.Fatalf("ok = %v", got)
			}
			check(t, tt.tab, tt.want)
			if tt.tab.Root.ID != "T" {
				t.Fatal("root id changed")
			}
		})
	}
}

func TestRemoveLastKeepsRootAndStackActive(t *testing.T) {
	tab := tabOf(row("", 1, blk("a", 1)))
	tab.RemovePane("a")
	if tab.Root == nil || tab.Root.Type != TypeRow || len(tab.Panes()) != 0 || len(tab.Rects()) != 0 {
		t.Fatal("empty root expected")
	}
	st := cont(TypeStack, "s", 1, blk("a", 1), blk("b", 1))
	setStackActive(st, "a")
	tab = tabOf(row("", 1, st))
	tab.RemovePane("a")
	if stackActive(st) != "b" || dump(tab.Root) != "RT(Ss:1(b:2))" {
		t.Fatal(dump(tab.Root), stackActive(st))
	}
}

func TestRects(t *testing.T) {
	tab := tabOf(row("", 1,
		blk("a", 1),
		col("c", 3, blk("b", 1), blk("d", 3))))
	got := tab.Rects()
	want := map[string]Rect{
		"a": {0, 0, 0.25, 1},
		"b": {0.25, 0, 0.75, 0.25},
		"d": {0.25, 0.25, 0.75, 0.75},
	}
	assertRects(t, got, want)

	px := tabOf(row("", 1, pxBlk("a", 100), pxBlk("b", 300)))
	assertRects(t, px.Rects(), map[string]Rect{"a": {0, 0, 0.25, 1}, "b": {0.25, 0, 0.75, 1}})

	zero := tabOf(row("", 1, blk("a", 0), blk("b", 0)))
	assertRects(t, zero.Rects(), map[string]Rect{"a": {0, 0, 0.5, 1}, "b": {0.5, 0, 0.5, 1}})

	st := cont(TypeStack, "s", 1, blk("a", 1), blk("b", 1))
	tab = tabOf(row("", 1, blk("x", 1), st))
	assertRects(t, tab.Rects(), map[string]Rect{"x": {0, 0, 0.5, 1}, "a": {0.5, 0, 0.5, 1}})
	setStackActive(st, "b")
	assertRects(t, tab.Rects(), map[string]Rect{"x": {0, 0, 0.5, 1}, "b": {0.5, 0, 0.5, 1}})

	if len((&Tab{}).Rects()) != 0 {
		t.Fatal("nil root")
	}
}

func assertRects(t *testing.T, got, want map[string]Rect) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for id, w := range want {
		g := got[id]
		if math.Abs(g.X-w.X)+math.Abs(g.Y-w.Y)+math.Abs(g.W-w.W)+math.Abs(g.H-w.H) > 1e-9 {
			t.Fatalf("%s: got %v want %v", id, g, w)
		}
	}
}

// L-shaped layout:
//
//	+---+-------+
//	| a |   b   |
//	|   +---+---+
//	|   | c | d |
//	+---+---+---+
//	|     e     |
//	+-----------+
func lShape() *Tab {
	return tabOf(col("", 1,
		row("top", 0.8, blk("a", 0.3),
			col("rc", 0.7, blk("b", 0.5), row("bot", 0.5, blk("c", 0.5), blk("d", 0.5)))),
		blk("e", 0.2)))
}

func TestNeighbor(t *testing.T) {
	tests := []struct {
		pane string
		dir  Direction
		want string
	}{
		{"a", Right, "b"}, // b and c both overlap a? a spans y 0..0.8; b is 0.4 tall, c 0.4: tie -> doc order b
		{"a", Down, "e"},
		{"a", Left, ""},
		{"a", Up, ""},
		{"b", Down, "c"}, // b spans x .3..1: c overlaps 0.35, d 0.35 -> first
		{"b", Left, "a"},
		{"b", Right, ""},
		{"c", Up, "b"},
		{"d", Up, "b"},
		{"c", Right, "d"},
		{"d", Left, "c"},
		{"c", Left, "a"},
		{"c", Down, "e"},
		{"d", Down, "e"},
		{"e", Up, "a"}, // e spans all x: a overlap .3, b/c/d: nearest above is c,d (0.35 each) vs a (0.3); most overlap wins
		{"e", Right, ""},
		{"zz", Right, ""},
	}
	for _, tt := range tests {
		tab := lShape()
		want := tt.want
		if tt.pane == "e" && tt.dir == Up {
			want = "c"
		}
		if got := tab.Neighbor(tt.pane, tt.dir); got != want {
			t.Errorf("%s %v: got %q want %q", tt.pane, tt.dir, got, want)
		}
	}
}

func TestNeighborPrefersOverlapThenNearest(t *testing.T) {
	// a (tall) has right neighbours: b (small overlap), c (big overlap).
	tab := tabOf(row("", 1, blk("a", 1), col("k", 1, blk("b", 1), blk("c", 3))))
	if got := tab.Neighbor("a", Right); got != "c" {
		t.Fatalf("got %q", got)
	}
	// Equal overlap, different gap: left pane m must not see p across q.
	tab = tabOf(row("", 1, blk("p", 1), blk("q", 1), blk("m", 1)))
	if got := tab.Neighbor("m", Left); got != "q" {
		t.Fatalf("got %q", got)
	}
	// Panes that merely touch at a corner are not neighbours.
	tab = tabOf(col("", 1, row("r1", 1, blk("a", 1), blk("b", 1)), row("r2", 1, blk("c", 1), blk("d", 1))))
	if got := tab.Neighbor("a", Right); got != "b" {
		t.Fatal(got)
	}
	if got := tab.Neighbor("b", Down); got != "d" {
		t.Fatal(got)
	}
}

func TestSwap(t *testing.T) {
	tab := tabOf(row("", 1, blk("a", 0.3), col("c", 0.7, blk("b", 0.5), blk("d", 0.5))))
	a, _ := tab.find("a")
	a.Content.Terminal = "ta"
	if !tab.Swap("a", Right) { // right neighbour is b (first in doc order)
		t.Fatal("swap failed")
	}
	// Slots keep their sizes; ids and content trade places.
	check(t, tab, "RT(b:0.3 Cc:0.7(a:0.5 d:0.5))")
	if p := tab.Root.Children[0]; p.Content.Terminal != "t-b" {
		t.Fatalf("content: %+v", p.Content)
	}
	if p := tab.Root.Children[1].Children[0]; p.ID != "a" || p.Content.Terminal != "ta" {
		t.Fatalf("a moved incorrectly: %+v", p)
	}
	if tab.Swap("b", Left) || tab.Swap("zz", Right) {
		t.Fatal("swap without neighbour must fail")
	}
	// Swap back restores the original layout.
	if !tab.Swap("a", Left) {
		t.Fatal()
	}
	check(t, tab, "RT(a:0.3 Cc:0.7(b:0.5 d:0.5))")
	if !tab.Swap("b", Down) {
		t.Fatal()
	}
	check(t, tab, "RT(a:0.3 Cc:0.7(d:0.5 b:0.5))")
}

func TestSpaceAndTabEdits(t *testing.T) {
	d := &Document{}
	s1 := d.AddSpace("one", "/x")
	s2 := d.AddSpace("", "/y")
	s3 := d.AddSpace("", "")
	if s1.ID == "" || len(s1.ID) != 34 || !strings.HasPrefix(s1.ID, "sp") || d.Space(s2.ID) != s2 || d.Space("no") != nil {
		t.Fatal("space ids/lookup")
	}
	ids := func() string {
		var out []string
		for _, s := range d.Spaces {
			out = append(out, s.ID)
		}
		return strings.Join(out, ",")
	}
	moves := []struct {
		id   *Space
		idx  int
		want []*Space
	}{
		{s3, 0, []*Space{s3, s1, s2}},
		{s3, 99, []*Space{s1, s2, s3}},
		{s1, -5, []*Space{s1, s2, s3}},
		{s1, 1, []*Space{s2, s1, s3}},
		{s3, 1, []*Space{s2, s3, s1}},
	}
	for i, m := range moves {
		if !d.MoveSpace(m.id.ID, m.idx) {
			t.Fatalf("move %d failed", i)
		}
		var want []string
		for _, s := range m.want {
			want = append(want, s.ID)
		}
		if ids() != strings.Join(want, ",") {
			t.Fatalf("move %d: got order %s", i, ids())
		}
	}
	if d.MoveSpace("nope", 0) || d.RemoveSpace("nope") {
		t.Fatal("unknown space")
	}
	if !d.RemoveSpace(s3.ID) || len(d.Spaces) != 2 {
		t.Fatal("remove")
	}

	t1, p1 := s1.AddTab("a", "t1")
	t2, _ := s1.AddTab("b", "t2")
	t3, _ := s1.AddTab("c", "t3")
	if t1.Root.ID != t1.ID || t1.Root.Type != TypeRow || len(t1.Root.Children) != 1 || t1.Root.Children[0] != p1 ||
		p1.Type != TypeBlock || p1.Content.Terminal != "t1" || *p1.Size.Weight != 1 || !strings.HasPrefix(t1.ID, "tb") {
		t.Fatalf("add tab: %+v", t1.Root)
	}
	if sp, tb := d.Tab(t2.ID); sp != s1 || tb != t2 {
		t.Fatal("Tab lookup")
	}
	if sp, tb, pn := d.Pane(p1.ID); sp != s1 || tb != t1 || pn != p1 {
		t.Fatal("Pane lookup")
	}
	if sp, tb, pn := d.PaneOfTerminal("t1"); sp != s1 || tb != t1 || pn != p1 {
		t.Fatal("PaneOfTerminal lookup")
	}
	if sp, _, pn := d.PaneOfTerminal("nope"); sp != nil || pn != nil {
		t.Fatal("PaneOfTerminal miss")
	}
	if sp, tb := d.Tab("nope"); sp != nil || tb != nil {
		t.Fatal("Tab miss")
	}
	order := func() string {
		var out []string
		for _, tb := range s1.Tabs {
			out = append(out, tb.Name)
		}
		return strings.Join(out, "")
	}
	for _, m := range []struct {
		tab  *Tab
		idx  int
		want string
	}{{t3, 0, "cab"}, {t3, 100, "abc"}, {t1, 1, "bac"}, {t1, -1, "abc"}, {t2, 2, "acb"}} {
		if !s1.MoveTab(m.tab.ID, m.idx) || order() != m.want {
			t.Fatalf("move tab %s to %d: %s want %s", m.tab.Name, m.idx, order(), m.want)
		}
	}
	if s1.MoveTab("nope", 0) || s1.RemoveTab("nope") {
		t.Fatal("unknown tab")
	}
	if !s1.RemoveTab(t1.ID) || order() != "cb" {
		t.Fatal(order())
	}
	if _, err := d.Encode(); err != nil {
		t.Fatal(err)
	}
}

func TestLabel(t *testing.T) {
	tests := []struct {
		s    Space
		home string
		want string
	}{
		{Space{Name: "Work", Folder: "/a/b"}, "/home/pn", "Work"},
		{Space{Folder: "/home/pn/pi-config"}, "/home/pn", "pi-config"},
		{Space{Folder: "/home/pn"}, "/home/pn", "~"},
		{Space{Folder: "/home/pn/"}, "/home/pn", "~"},
		{Space{Folder: "/home/pn"}, "", "pn"},
		{Space{}, "/home/pn", "Space"},
		{Space{Name: "x"}, "", "x"},
		{Space{Folder: "/"}, "/home/pn", "/"},
		{Space{Folder: "/srv/app/"}, "/home/pn", "app"},
	}
	for _, tt := range tests {
		if got := tt.s.Label(tt.home); got != tt.want {
			t.Errorf("%+v: got %q want %q", tt.s, got, tt.want)
		}
	}
}

func TestEditedDocumentRoundTrips(t *testing.T) {
	d := &Document{}
	s := d.AddSpace("n", "/f")
	tab, p := s.AddTab("", "t1")
	if _, err := tab.Split(p.ID, Right, "t2"); err != nil {
		t.Fatal(err)
	}
	b, err := d.Encode()
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Decode(b)
	if err != nil || !Equal(d, d2) {
		t.Fatal("edited document does not round trip", err)
	}
}
