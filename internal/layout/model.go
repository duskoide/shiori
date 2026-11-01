// Package layout models the machine layout document ("herdr-app") shared by
// both frontends: spaces, tabs and tiled panes. Unknown JSON fields are kept
// at every level and written back, so editing never destroys data written by
// the other frontend.
package layout

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

// Document is the whole layout document.
type Document struct {
	Spaces []*Space
	Extra  map[string]json.RawMessage
}

// Space is a project (folder) holding tabs.
type Space struct {
	ID, Name, Folder string
	Tabs             []*Tab
	Extra            map[string]json.RawMessage
	// Machine is whose space it is: "" this machine's. The window knows
	// it; no document holds it (see machines.go).
	Machine string
}

// Tab is one tiled arrangement. Root.ID equals the tab ID.
type Tab struct {
	ID, Name string
	Root     *Node
	Extra    map[string]json.RawMessage
}

// Node is a tile tree node: type "block" (leaf), "row", "column" or "stack".
// Fields not modelled here (such as a stack's "active") live in Extra.
type Node struct {
	ID, Type string
	Children []*Node
	Content  *Content
	Size     *Size
	Extra    map[string]json.RawMessage
}

// Content is what a block holds: a terminal address or a mod with props.
type Content struct {
	Terminal, Mod string
	Props         json.RawMessage
	Extra         map[string]json.RawMessage
}

// Size is either a pixel size or a relative weight.
type Size struct {
	Px, Weight *float64
	extra      map[string]json.RawMessage
}

// Node type names.
const (
	TypeBlock  = "block"
	TypeRow    = "row"
	TypeColumn = "column"
	TypeStack  = "stack"
)

// NewID returns prefix followed by 32 lowercase hex characters.
func NewID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}

// NewIDOn is NewID qualified for a machine ("" this one).
func NewIDOn(machine, prefix string) string { return Qualify(NewID(prefix), machine) }

// Decode parses a document. Empty input and JSON null yield an empty document.
func Decode(b []byte) (*Document, error) {
	d := &Document{}
	if t := bytes.TrimSpace(b); len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return d, nil
	}
	if err := json.Unmarshal(b, d); err != nil {
		return nil, err
	}
	return d, nil
}

// Encode serialises the document deterministically (sorted keys).
func (d *Document) Encode() ([]byte, error) { return json.Marshal(d) }

// Clone returns a deep copy.
func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}
	c := &Document{Extra: cloneExtra(d.Extra)}
	for _, s := range d.Spaces {
		c.Spaces = append(c.Spaces, s.clone())
	}
	return c
}

func (s *Space) clone() *Space {
	c := &Space{ID: s.ID, Name: s.Name, Folder: s.Folder, Extra: cloneExtra(s.Extra), Machine: s.Machine}
	for _, t := range s.Tabs {
		c.Tabs = append(c.Tabs, &Tab{ID: t.ID, Name: t.Name, Root: t.Root.clone(), Extra: cloneExtra(t.Extra)})
	}
	return c
}

func (n *Node) clone() *Node {
	if n == nil {
		return nil
	}
	c := &Node{ID: n.ID, Type: n.Type, Extra: cloneExtra(n.Extra)}
	if n.Children != nil {
		c.Children = make([]*Node, len(n.Children))
		for i, ch := range n.Children {
			c.Children[i] = ch.clone()
		}
	}
	if n.Content != nil {
		c.Content = &Content{Terminal: n.Content.Terminal, Mod: n.Content.Mod,
			Props: cloneRaw(n.Content.Props), Extra: cloneExtra(n.Content.Extra)}
	}
	if n.Size != nil {
		c.Size = n.Size.clone()
	}
	return c
}

func (z *Size) clone() *Size {
	c := &Size{extra: cloneExtra(z.extra)}
	if z.Px != nil {
		v := *z.Px
		c.Px = &v
	}
	if z.Weight != nil {
		v := *z.Weight
		c.Weight = &v
	}
	return c
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Document) UnmarshalJSON(b []byte) error {
	o, err := parseObj(b)
	if err != nil {
		return err
	}
	*d = Document{}
	if err := o.pop("spaces", &d.Spaces); err != nil {
		return err
	}
	d.Extra = o.extra()
	return nil
}

// MarshalJSON implements json.Marshaler.
func (d *Document) MarshalJSON() ([]byte, error) {
	e := newEncoder(d.Extra)
	spaces := d.Spaces
	if spaces == nil {
		spaces = []*Space{}
	}
	if err := e.put("spaces", spaces); err != nil {
		return nil, err
	}
	return e.bytes()
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *Space) UnmarshalJSON(b []byte) error {
	o, err := parseObj(b)
	if err != nil {
		return err
	}
	*s = Space{}
	for k, p := range map[string]any{"id": &s.ID, "name": &s.Name, "folder": &s.Folder, "tabs": &s.Tabs} {
		if err := o.pop(k, p); err != nil {
			return err
		}
	}
	s.Extra = o.extra()
	return nil
}

// MarshalJSON implements json.Marshaler.
func (s *Space) MarshalJSON() ([]byte, error) {
	e := newEncoder(s.Extra)
	tabs := s.Tabs
	if tabs == nil {
		tabs = []*Tab{}
	}
	for k, v := range map[string]any{"id": s.ID, "name": s.Name, "folder": s.Folder, "tabs": tabs} {
		if err := e.put(k, v); err != nil {
			return nil, err
		}
	}
	return e.bytes()
}

// UnmarshalJSON implements json.Unmarshaler.
func (t *Tab) UnmarshalJSON(b []byte) error {
	o, err := parseObj(b)
	if err != nil {
		return err
	}
	*t = Tab{}
	for k, p := range map[string]any{"id": &t.ID, "name": &t.Name, "root": &t.Root} {
		if err := o.pop(k, p); err != nil {
			return err
		}
	}
	t.Extra = o.extra()
	return nil
}

// MarshalJSON implements json.Marshaler.
func (t *Tab) MarshalJSON() ([]byte, error) {
	e := newEncoder(t.Extra)
	if err := e.put("id", t.ID); err != nil {
		return nil, err
	}
	if err := e.put("name", t.Name); err != nil {
		return nil, err
	}
	if t.Root != nil {
		if err := e.put("root", t.Root); err != nil {
			return nil, err
		}
	}
	return e.bytes()
}

// UnmarshalJSON implements json.Unmarshaler.
func (n *Node) UnmarshalJSON(b []byte) error {
	o, err := parseObj(b)
	if err != nil {
		return err
	}
	*n = Node{}
	for k, p := range map[string]any{"id": &n.ID, "type": &n.Type, "children": &n.Children,
		"content": &n.Content, "size": &n.Size} {
		if err := o.pop(k, p); err != nil {
			return err
		}
	}
	n.Extra = o.extra()
	return nil
}

// MarshalJSON implements json.Marshaler.
func (n *Node) MarshalJSON() ([]byte, error) {
	e := newEncoder(n.Extra)
	if err := e.put("id", n.ID); err != nil {
		return nil, err
	}
	if err := e.put("type", n.Type); err != nil {
		return nil, err
	}
	if n.Children != nil {
		if err := e.put("children", n.Children); err != nil {
			return nil, err
		}
	}
	if n.Content != nil {
		if err := e.put("content", n.Content); err != nil {
			return nil, err
		}
	}
	if n.Size != nil {
		if err := e.put("size", n.Size); err != nil {
			return nil, err
		}
	}
	return e.bytes()
}

// UnmarshalJSON implements json.Unmarshaler.
func (c *Content) UnmarshalJSON(b []byte) error {
	o, err := parseObj(b)
	if err != nil {
		return err
	}
	*c = Content{}
	if err := o.pop("terminal", &c.Terminal); err != nil {
		return err
	}
	if err := o.pop("mod", &c.Mod); err != nil {
		return err
	}
	if raw, ok := o["props"]; ok {
		c.Props = cloneRaw(raw)
		delete(o, "props")
	}
	c.Extra = o.extra()
	return nil
}

// MarshalJSON implements json.Marshaler.
func (c *Content) MarshalJSON() ([]byte, error) {
	e := newEncoder(c.Extra)
	if c.Terminal != "" {
		if err := e.put("terminal", c.Terminal); err != nil {
			return nil, err
		}
	}
	if c.Mod != "" {
		if err := e.put("mod", c.Mod); err != nil {
			return nil, err
		}
	}
	if len(c.Props) > 0 {
		e["props"] = c.Props
	}
	return e.bytes()
}

// UnmarshalJSON implements json.Unmarshaler.
func (z *Size) UnmarshalJSON(b []byte) error {
	o, err := parseObj(b)
	if err != nil {
		return err
	}
	*z = Size{}
	if err := o.pop("px", &z.Px); err != nil {
		return err
	}
	if err := o.pop("weight", &z.Weight); err != nil {
		return err
	}
	z.extra = o.extra()
	return nil
}

// MarshalJSON implements json.Marshaler.
func (z *Size) MarshalJSON() ([]byte, error) {
	e := newEncoder(z.extra)
	if z.Px != nil {
		if err := e.put("px", *z.Px); err != nil {
			return nil, err
		}
	}
	if z.Weight != nil {
		if err := e.put("weight", *z.Weight); err != nil {
			return nil, err
		}
	}
	return e.bytes()
}
