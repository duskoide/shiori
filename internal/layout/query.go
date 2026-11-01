package layout

import (
	"path/filepath"
	"slices"
)

// Space returns the space with the given ID, or nil.
func (d *Document) Space(id string) *Space {
	for _, s := range d.Spaces {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Tab returns the tab with the given ID and its space, or nils.
func (d *Document) Tab(id string) (*Space, *Tab) {
	for _, s := range d.Spaces {
		for _, t := range s.Tabs {
			if t.ID == id {
				return s, t
			}
		}
	}
	return nil, nil
}

// Pane returns the block node with the given ID with its tab and space.
func (d *Document) Pane(id string) (*Space, *Tab, *Node) {
	return d.findPane(func(n *Node) bool { return n.ID == id })
}

// PaneOfTerminal returns the pane holding the terminal address addr.
func (d *Document) PaneOfTerminal(addr string) (*Space, *Tab, *Node) {
	return d.findPane(func(n *Node) bool { return n.Content != nil && n.Content.Terminal == addr })
}

func (d *Document) findPane(match func(*Node) bool) (*Space, *Tab, *Node) {
	for _, s := range d.Spaces {
		for _, t := range s.Tabs {
			for _, p := range t.Panes() {
				if match(p) {
					return s, t, p
				}
			}
		}
	}
	return nil, nil, nil
}

// Panes returns the block leaves of the tab in document order.
func (t *Tab) Panes() []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Type == TypeBlock {
			out = append(out, n)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(t.Root)
	return out
}

// Label is the display name of the space: Name if set, else the folder's base
// name, "~" for the home directory, and "Space" when there is no folder.
func (s *Space) Label(home string) string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Folder == "":
		return "Space"
	case home != "" && filepath.Clean(s.Folder) == filepath.Clean(home):
		return "~"
	}
	return filepath.Base(s.Folder)
}

func clampIndex(i, n int) int { return min(max(i, 0), n-1) }

// AddSpace appends a new, empty space of this machine's.
func (d *Document) AddSpace(name, folder string) *Space {
	return d.AddSpaceOn("", name, folder)
}

// AddSpaceOn adds a new, empty space on a machine ("" this one), last of
// that machine's.
func (d *Document) AddSpaceOn(machine, name, folder string) *Space {
	s := &Space{ID: NewIDOn(machine, "sp"), Name: name, Folder: folder, Tabs: []*Tab{}, Machine: machine}
	d.Spaces = append(d.Spaces, s)
	d.OrderSpaces()
	return s
}

// RemoveSpace deletes a space; it reports whether it existed.
func (d *Document) RemoveSpace(id string) bool {
	i := slices.IndexFunc(d.Spaces, func(s *Space) bool { return s.ID == id })
	if i < 0 {
		return false
	}
	d.Spaces = slices.Delete(d.Spaces, i, i+1)
	return true
}

// MoveSpace moves a space to index, clamped to the places of its
// machine's spaces: a machine's spaces stay together.
func (d *Document) MoveSpace(id string, index int) bool {
	s := d.Space(id)
	if s == nil {
		return false
	}
	lo, hi := d.block(s.Machine)
	return moveItem(&d.Spaces, func(x *Space) bool { return x == s }, min(max(index, lo), hi-1))
}

// AddTab appends a tab whose root row holds one block running terminal.
func (s *Space) AddTab(name, terminal string) (*Tab, *Node) {
	id := NewIDOn(s.Machine, "tb")
	pane := &Node{ID: NewIDOn(s.Machine, "p"), Type: TypeBlock, Content: &Content{Terminal: terminal}, Size: weight(1)}
	t := &Tab{ID: id, Name: name, Root: &Node{ID: id, Type: TypeRow, Children: []*Node{pane}}}
	s.Tabs = append(s.Tabs, t)
	return t, pane
}

// RemoveTab deletes a tab; it reports whether it existed.
func (s *Space) RemoveTab(id string) bool {
	i := slices.IndexFunc(s.Tabs, func(t *Tab) bool { return t.ID == id })
	if i < 0 {
		return false
	}
	s.Tabs = slices.Delete(s.Tabs, i, i+1)
	return true
}

// MoveTab moves a tab to index (clamped to the valid range).
func (s *Space) MoveTab(id string, index int) bool {
	return moveItem(&s.Tabs, func(t *Tab) bool { return t.ID == id }, index)
}

func moveItem[T any](list *[]T, match func(T) bool, index int) bool {
	i := slices.IndexFunc(*list, match)
	if i < 0 {
		return false
	}
	item := (*list)[i]
	*list = slices.Delete(*list, i, i+1)
	j := clampIndex(index, len(*list)+1)
	*list = slices.Insert(*list, j, item)
	return true
}

func weight(w float64) *Size { return &Size{Weight: &w} }
