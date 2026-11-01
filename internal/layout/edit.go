package layout

import (
	"encoding/json"
	"fmt"
	"math"
)

// Direction is a spatial direction.
type Direction int

// Directions.
const (
	Right Direction = iota
	Down
	Left
	Up
)

func (d Direction) String() string {
	switch d {
	case Right:
		return "right"
	case Down:
		return "down"
	case Left:
		return "left"
	case Up:
		return "up"
	}
	return fmt.Sprintf("Direction(%d)", int(d))
}

// horizontal reports whether d moves along a row.
func (d Direction) horizontal() bool { return d == Right || d == Left }

// after reports whether d places things after (right of / below) the origin.
func (d Direction) after() bool { return d == Right || d == Down }

func (d Direction) containerType() string {
	if d.horizontal() {
		return TypeRow
	}
	return TypeColumn
}

func isContainer(n *Node) bool {
	return n.Type == TypeRow || n.Type == TypeColumn || n.Type == TypeStack
}

// find locates the node with id, returning it and its parent (nil for root).
func (t *Tab) find(id string) (node, parent *Node) {
	var walk func(n, p *Node) bool
	walk = func(n, p *Node) bool {
		if n.ID == id {
			node, parent = n, p
			return true
		}
		for _, c := range n.Children {
			if walk(c, n) {
				return true
			}
		}
		return false
	}
	if t.Root != nil {
		walk(t.Root, nil)
	}
	return
}

func indexOf(parent, child *Node) int {
	for i, c := range parent.Children {
		if c == child {
			return i
		}
	}
	return -1
}

// share is the effective layout weight of a node: its weight, else its px
// (so px-sized nodes count proportionally to their size), else 1.
func share(n *Node) float64 {
	switch {
	case n.Size != nil && n.Size.Weight != nil:
		return *n.Size.Weight
	case n.Size != nil && n.Size.Px != nil:
		return *n.Size.Px
	}
	return 1
}

func stackActive(n *Node) string {
	var id string
	if raw, ok := n.Extra["active"]; ok {
		_ = json.Unmarshal(raw, &id)
	}
	return id
}

func setStackActive(n *Node, id string) {
	raw, _ := json.Marshal(id)
	if n.Extra == nil {
		n.Extra = map[string]json.RawMessage{}
	}
	n.Extra["active"] = raw
}

// Split places a new pane running terminal beside paneID in direction dir
// (Right/Down: after; Left/Up: before) and returns it. When the parent
// container already has the matching orientation the pane's size is halved
// between the two; otherwise the pane is wrapped in a new container that
// inherits its size, and the two children get weight 0.5.
func (t *Tab) Split(paneID string, dir Direction, terminal string) (*Node, error) {
	pane, parent := t.find(paneID)
	if pane == nil || pane.Type != TypeBlock {
		return nil, fmt.Errorf("layout: pane %q not found", paneID)
	}
	nb := &Node{ID: NewIDOn(MachineOf(t.ID), "p"), Type: TypeBlock, Content: &Content{Terminal: terminal}}
	if parent == nil {
		// A block root (other apps may write one) becomes the first child
		// of a new root container.
		pane.Size, nb.Size = weight(0.5), weight(0.5)
		kids := []*Node{pane, nb}
		if !dir.after() {
			kids[0], kids[1] = nb, pane
		}
		t.Root = &Node{ID: NewIDOn(MachineOf(t.ID), "c"), Type: dir.containerType(), Children: kids}
		return nb, nil
	}
	i := indexOf(parent, pane)
	if parent.Type == dir.containerType() {
		if pane.Size != nil && pane.Size.Px != nil && pane.Size.Weight == nil {
			half := *pane.Size.Px / 2
			pane.Size.Px = &half
			nb.Size = &Size{Px: &half}
		} else {
			half := share(pane) / 2
			pane.Size = weight(half)
			nb.Size = weight(half)
		}
		if dir.after() {
			i++
		}
		parent.Children = insertAt(parent.Children, i, nb)
		return nb, nil
	}
	wrap := &Node{ID: NewIDOn(MachineOf(t.ID), "c"), Type: dir.containerType(), Size: pane.Size}
	if parent.Type == TypeStack && stackActive(parent) == pane.ID {
		setStackActive(parent, wrap.ID)
	}
	pane.Size, nb.Size = weight(0.5), weight(0.5)
	if dir.after() {
		wrap.Children = []*Node{pane, nb}
	} else {
		wrap.Children = []*Node{nb, pane}
	}
	parent.Children[i] = wrap
	return nb, nil
}

func insertAt(s []*Node, i int, n *Node) []*Node {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = n
	return s
}

// RemovePane removes a pane. Its weight goes to the weight-sized siblings in
// proportion, and containers left with a single child are collapsed into
// their parent (merging same-orientation nesting). The tab root is kept (with
// id == tab ID); an empty root means the tab is empty. It reports whether the
// pane existed.
func (t *Tab) RemovePane(paneID string) bool {
	pane, parent := t.find(paneID)
	if pane == nil || pane.Type != TypeBlock {
		return false
	}
	if parent == nil {
		// The tab's only pane was its root: the tab is now empty.
		t.Root = nil
		return true
	}
	i := indexOf(parent, pane)
	parent.Children = append(parent.Children[:i:i], parent.Children[i+1:]...)
	if parent.Type == TypeStack && stackActive(parent) == paneID && len(parent.Children) > 0 {
		setStackActive(parent, parent.Children[0].ID)
	}
	if pane.Size == nil || pane.Size.Weight != nil {
		redistribute(parent.Children, share(pane))
	}
	t.collapse()
	return true
}

// redistribute hands w to the weight-based (or unsized) nodes proportionally.
func redistribute(sibs []*Node, w float64) {
	var sum float64
	var targets []*Node
	for _, s := range sibs {
		if s.Size == nil || s.Size.Weight != nil {
			sum += share(s)
			targets = append(targets, s)
		}
	}
	if sum <= 0 || w == 0 {
		return
	}
	for _, s := range targets {
		s.Size = weight(share(s) * (1 + w/sum))
	}
}

// collapse removes single-child row/column containers below the root and
// flattens same-orientation nesting, repeating until stable.
func (t *Tab) collapse() {
	if t.Root == nil {
		return
	}
	var fix func(n *Node)
	fix = func(n *Node) {
		for changed := true; changed; {
			changed = false
			for i, c := range n.Children {
				fix(c)
				switch {
				case c.Type == TypeBlock || !isContainer(c):
				case len(c.Children) == 0:
					n.Children = append(n.Children[:i:i], n.Children[i+1:]...)
					changed = true
				case len(c.Children) == 1 && c.Type != TypeStack:
					only := c.Children[0]
					only.Size = c.Size
					n.Children[i] = only
					changed = true
				case c.Type == n.Type && n.Type != TypeStack && flattenable(c):
					n.Children = spliceChildren(n.Children, i, c)
					changed = true
				}
				if changed {
					break
				}
			}
		}
	}
	fix(t.Root)
	// A root with a lone row/column child becomes that child, keeping its id.
	r := t.Root
	for r.Type != TypeStack && len(r.Children) == 1 && isContainer(r.Children[0]) && r.Children[0].Type != TypeStack {
		c := r.Children[0]
		r.Type, r.Children = c.Type, c.Children
	}
}

func flattenable(c *Node) bool {
	for _, k := range c.Children {
		if k.Size != nil && k.Size.Px != nil && k.Size.Weight == nil {
			return false
		}
	}
	return true
}

// spliceChildren replaces children[i] (container c) by c's children, scaling
// their weights to the slot c occupied.
func spliceChildren(list []*Node, i int, c *Node) []*Node {
	slot := share(c)
	var sum float64
	for _, k := range c.Children {
		sum += share(k)
	}
	for _, k := range c.Children {
		if sum > 0 {
			k.Size = weight(share(k) / sum * slot)
		}
	}
	out := append([]*Node{}, list[:i]...)
	out = append(out, c.Children...)
	return append(out, list[i+1:]...)
}

// Swap exchanges a pane with its spatial neighbour in direction dir. The
// panes' identities (ID and Content) trade places while the layout slots
// (type, size, other fields) stay, so a pane ID keeps pointing at the same
// terminal and focus follows the content. It reports whether a swap happened.
func (t *Tab) Swap(paneID string, dir Direction) bool {
	other := t.Neighbor(paneID, dir)
	if other == "" {
		return false
	}
	a, _ := t.find(paneID)
	b, _ := t.find(other)
	if a == nil || b == nil {
		return false
	}
	a.ID, b.ID = b.ID, a.ID
	a.Content, b.Content = b.Content, a.Content
	return true
}

// Rect is a rectangle in the unit square.
type Rect struct{ X, Y, W, H float64 }

// Rects computes the 0..1 rectangle of every visible pane. Children share a
// container in proportion to their weights (px sizes count as weights equal
// to the px value; no size counts as 1). A stack shows only its active child
// (the first when unset). Zero total weight divides space equally.
func (t *Tab) Rects() map[string]Rect {
	out := map[string]Rect{}
	var walk func(n *Node, r Rect)
	walk = func(n *Node, r Rect) {
		switch n.Type {
		case TypeBlock:
			out[n.ID] = r
		case TypeStack:
			if len(n.Children) == 0 {
				return
			}
			act := n.Children[0]
			for _, c := range n.Children {
				if c.ID == stackActive(n) {
					act = c
				}
			}
			walk(act, r)
		default:
			var sum float64
			for _, c := range n.Children {
				sum += math.Max(share(c), 0)
			}
			pos := 0.0
			for _, c := range n.Children {
				f := 1 / float64(len(n.Children))
				if sum > 0 {
					f = math.Max(share(c), 0) / sum
				}
				cr := r
				if n.Type == TypeColumn {
					cr.Y, cr.H = r.Y+pos*r.H, f*r.H
				} else {
					cr.X, cr.W = r.X+pos*r.W, f*r.W
				}
				pos += f
				walk(c, cr)
			}
		}
	}
	if t.Root != nil {
		walk(t.Root, Rect{0, 0, 1, 1})
	}
	return out
}

const eps = 1e-9

// Neighbor returns the ID of the pane closest to paneID in direction dir:
// among panes lying beyond its edge whose span overlaps it and that are not
// hidden behind a nearer pane, the one with the most overlap wins, then the
// smallest gap, then document order. "" if there is none.
func (t *Tab) Neighbor(paneID string, dir Direction) string {
	rects := t.Rects()
	r, ok := rects[paneID]
	if !ok {
		return ""
	}
	type cand struct {
		id          string
		gap, lo, hi float64 // gap to origin; overlap interval on the cross axis
	}
	var cands []cand
	for _, p := range t.Panes() {
		c, ok := rects[p.ID]
		if !ok || p.ID == paneID {
			continue
		}
		var gap, lo, hi float64
		switch dir {
		case Right:
			gap, lo, hi = c.X-(r.X+r.W), math.Max(r.Y, c.Y), math.Min(r.Y+r.H, c.Y+c.H)
		case Left:
			gap, lo, hi = r.X-(c.X+c.W), math.Max(r.Y, c.Y), math.Min(r.Y+r.H, c.Y+c.H)
		case Down:
			gap, lo, hi = c.Y-(r.Y+r.H), math.Max(r.X, c.X), math.Min(r.X+r.W, c.X+c.W)
		case Up:
			gap, lo, hi = r.Y-(c.Y+c.H), math.Max(r.X, c.X), math.Min(r.X+r.W, c.X+c.W)
		}
		if gap >= -eps && hi-lo > eps {
			cands = append(cands, cand{p.ID, gap, lo, hi})
		}
	}
	best, bestOverlap, bestGap := "", 0.0, 0.0
	for _, c := range cands {
		hidden := false
		for _, o := range cands {
			// o hides c when it is nearer and covers part of c's overlap.
			if o.gap < c.gap-eps && math.Min(o.hi, c.hi)-math.Max(o.lo, c.lo) > eps {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}
		ov := c.hi - c.lo
		if best == "" || ov > bestOverlap+eps || (ov > bestOverlap-eps && c.gap < bestGap-eps) {
			best, bestOverlap, bestGap = c.id, ov, c.gap
		}
	}
	return best
}
