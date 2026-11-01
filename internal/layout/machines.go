package layout

import (
	"cmp"
	"slices"
	"strings"
)

// Each machine keeps a document of its own, whose ids and terminal
// addresses are that machine's ("sp…", "t7"). The window shows them in one
// Document: another machine's spaces carry ids qualified with its name
// ("mac/sp…", "mac/t7", which is also how the core addresses its
// terminals), and Space.Machine says whose they are. This machine's spaces
// come first, then each machine's together, by name, as in herdr-app.

// MachineOf is the machine of a qualified id or terminal address: "mac"
// for "mac/t7", "" (this machine) for "t7".
func MachineOf(id string) string {
	m, _, ok := strings.Cut(id, "/")
	if !ok {
		return ""
	}
	return m
}

// Qualify is id as the window names it: "mac/id" on machine "mac", id on
// this one.
func Qualify(id, machine string) string {
	if machine == "" || id == "" {
		return id
	}
	return machine + "/" + id
}

// Local is a qualified id as its machine names it: Qualify undone.
func Local(id, machine string) string {
	if machine == "" {
		return id
	}
	if rest, ok := strings.CutPrefix(id, machine+"/"); ok {
		return rest
	}
	return id
}

// Machines are the machines with spaces in d, other than this one, in
// order.
func (d *Document) Machines() []string {
	var out []string
	for _, s := range d.Spaces {
		if s.Machine != "" && !slices.Contains(out, s.Machine) {
			out = append(out, s.Machine)
		}
	}
	return out
}

// Part is one machine's document, as that machine keeps it: its spaces,
// with ids and addresses its own. This machine's carries d's other fields;
// another machine's has none, which the caller keeps from its last copy.
func (d *Document) Part(machine string) *Document {
	p := &Document{}
	if machine == "" {
		p.Extra = cloneExtra(d.Extra)
	}
	for _, s := range d.Spaces {
		if s.Machine == machine {
			c := s.clone()
			c.Machine = ""
			mapSpace(c, func(id string) string { return Local(id, machine) })
			p.Spaces = append(p.Spaces, c)
		}
	}
	return p
}

// Replace puts a machine's document in place of that machine's spaces,
// qualified, where they were. This machine's also brings its other fields.
func (d *Document) Replace(part *Document, machine string) {
	at := slices.IndexFunc(d.Spaces, func(s *Space) bool { return s.Machine == machine })
	if at < 0 {
		at = len(d.Spaces)
	}
	d.Spaces = slices.DeleteFunc(d.Spaces, func(s *Space) bool { return s.Machine == machine })
	at = min(at, len(d.Spaces))
	var spaces []*Space
	for _, s := range part.Spaces {
		c := s.clone()
		c.Machine = machine
		mapSpace(c, func(id string) string { return Qualify(id, machine) })
		spaces = append(spaces, c)
	}
	d.Spaces = slices.Insert(d.Spaces, at, spaces...)
	if machine == "" {
		d.Extra = cloneExtra(part.Extra)
	}
	d.OrderSpaces()
}

// Drop takes a machine's spaces away.
func (d *Document) Drop(machine string) {
	if machine == "" {
		return
	}
	d.Spaces = slices.DeleteFunc(d.Spaces, func(s *Space) bool { return s.Machine == machine })
}

// OrderSpaces puts this machine's spaces first, then each other machine's
// together, by name, keeping their order within a machine.
func (d *Document) OrderSpaces() {
	slices.SortStableFunc(d.Spaces, func(a, b *Space) int {
		switch {
		case a.Machine == b.Machine:
			return 0
		case a.Machine == "":
			return -1
		case b.Machine == "":
			return 1
		}
		return cmp.Compare(a.Machine, b.Machine)
	})
}

// block is the range of d.Spaces holding the machine's spaces.
func (d *Document) block(machine string) (lo, hi int) {
	lo = slices.IndexFunc(d.Spaces, func(s *Space) bool { return s.Machine == machine })
	if lo < 0 {
		return -1, -1
	}
	hi = lo
	for hi < len(d.Spaces) && d.Spaces[hi].Machine == machine {
		hi++
	}
	return lo, hi
}

func mapSpace(s *Space, fn func(string) string) {
	s.ID = fn(s.ID)
	for _, t := range s.Tabs {
		t.ID = fn(t.ID)
		mapNode(t.Root, fn)
	}
}

// mapNode renames a tile tree's ids and terminal addresses: not a mod's
// props, which are the mod's.
func mapNode(n *Node, fn func(string) string) {
	if n == nil {
		return
	}
	n.ID = fn(n.ID)
	if n.Content != nil && n.Content.Terminal != "" {
		n.Content.Terminal = fn(n.Content.Terminal)
	}
	if active := stackActive(n); active != "" {
		setStackActive(n, fn(active))
	}
	for _, c := range n.Children {
		mapNode(c, fn)
	}
}
