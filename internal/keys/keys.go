// Package keys parses herdr's keys.json: key chords, actions and sections.
package keys

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

//go:embed default_keys.json
var defaultKeys []byte

// Chord is a key with modifiers. Key is normalised to lower case: "a".."z",
// "0".."9", punctuation as typed ("/", "=", "-", ",", "[", "+", "?"), or a name
// such as "enter", "tab", "escape", "space", "backspace", "delete", "insert",
// "home", "end", "pageup", "pagedown", "up", "down", "left", "right", "f1".."f24".
type Chord struct {
	Ctrl, Alt, Shift, Super bool
	Key                     string
}

var modifiers = map[string]func(*Chord){
	"ctrl":    func(c *Chord) { c.Ctrl = true },
	"control": func(c *Chord) { c.Ctrl = true },
	"alt":     func(c *Chord) { c.Alt = true },
	"option":  func(c *Chord) { c.Alt = true },
	"opt":     func(c *Chord) { c.Alt = true },
	"shift":   func(c *Chord) { c.Shift = true },
	"cmd":     func(c *Chord) { c.Super = true },
	"super":   func(c *Chord) { c.Super = true },
	"meta":    func(c *Chord) { c.Super = true },
	"win":     func(c *Chord) { c.Super = true },
}

// keyNames maps accepted spellings to the normalised key name.
var keyNames = map[string]string{
	"enter": "enter", "return": "enter", "tab": "tab", "escape": "escape", "esc": "escape",
	"space": "space", "backspace": "backspace", "delete": "delete", "del": "delete",
	"insert": "insert", "ins": "insert", "home": "home", "end": "end",
	"pageup": "pageup", "pgup": "pageup", "pagedown": "pagedown", "pgdn": "pagedown",
	"up": "up", "down": "down", "left": "left", "right": "right",
}

func normalizeKey(k string) (string, bool) {
	l := strings.ToLower(k)
	if utf8.RuneCountInString(l) == 1 {
		return l, true
	}
	if n, ok := keyNames[l]; ok {
		return n, true
	}
	if len(l) >= 2 && len(l) <= 3 && l[0] == 'f' {
		n := 0
		for _, r := range l[1:] {
			if r < '0' || r > '9' {
				return "", false
			}
			n = n*10 + int(r-'0')
		}
		if n >= 1 && n <= 24 && l[1] != '0' {
			return l, true
		}
	}
	return "", false
}

// ParseChord parses herdr's chord syntax: modifiers and a key joined by '-',
// modifiers in any order, case-insensitive ("ctrl-shift-t", "alt-[", "ctrl-+",
// "ctrl--", "f2", "ctrl-pagedown"). "cmd" and "super" set Super.
func ParseChord(s string) (Chord, error) {
	var c Chord
	rest := strings.TrimSpace(s)
	for {
		i := strings.IndexByte(rest, '-')
		if i <= 0 {
			break
		}
		set, ok := modifiers[strings.ToLower(rest[:i])]
		if !ok {
			break
		}
		set(&c)
		rest = rest[i+1:]
	}
	if rest == "" {
		return Chord{}, fmt.Errorf("keys: %q has no key", s)
	}
	key, ok := normalizeKey(rest)
	if !ok {
		return Chord{}, fmt.Errorf("keys: %q: unknown key %q", s, rest)
	}
	c.Key = key
	return c, nil
}

var keyLabels = map[string]string{
	"pageup": "PageUp", "pagedown": "PageDown", "enter": "Enter", "tab": "Tab", "escape": "Esc",
	"space": "Space", "backspace": "Backspace", "delete": "Delete", "insert": "Insert",
	"home": "Home", "end": "End", "up": "Up", "down": "Down", "left": "Left", "right": "Right",
}

// String formats the chord for display, e.g. "Ctrl+Shift+T", "Alt+1", "Ctrl+PageDown".
func (c Chord) String() string {
	var parts []string
	if c.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if c.Alt {
		parts = append(parts, "Alt")
	}
	if c.Shift {
		parts = append(parts, "Shift")
	}
	if c.Super {
		parts = append(parts, "Super")
	}
	key := c.Key
	if l, ok := keyLabels[key]; ok {
		key = l
	} else {
		key = strings.ToUpper(key) // single characters and F-keys
	}
	return strings.Join(append(parts, key), "+")
}

// Action is what a chord does. Exactly one of the forms is set: Terminal
// (the key goes to the program), Send (text typed into the terminal), or
// Command with an optional Arg ("pane.go:left" -> {pane.go, left}).
type Action struct {
	Command, Arg string
	Send         string
	Terminal     bool
	// None removes a default binding: keys.json's "none", which Overlay
	// applies. A keymap in use holds none.
	None bool
}

// ParseAction parses "command", "command:value", "send:<text>" or "terminal".
func ParseAction(s string) (Action, error) {
	switch {
	case s == "terminal":
		return Action{Terminal: true}, nil
	case s == "none":
		return Action{None: true}, nil
	case strings.HasPrefix(s, "send:"):
		return Action{Send: s[len("send:"):]}, nil
	case strings.TrimSpace(s) == "":
		return Action{}, errors.New("keys: empty action")
	}
	cmd, arg, _ := strings.Cut(s, ":")
	if cmd == "" {
		return Action{}, fmt.Errorf("keys: action %q has no command", s)
	}
	return Action{Command: cmd, Arg: arg}, nil
}

// Keymap holds bindings per section ("global", "terminal", "layout:<name>", "mod:<name>").
type Keymap struct {
	Sections map[string]map[Chord]Action
}

// Parse reads a keys.json document. Every problem is reported (joined); the
// returned keymap then still holds the valid bindings.
func Parse(b []byte) (*Keymap, error) {
	var raw map[string]map[string]string
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	km := &Keymap{Sections: make(map[string]map[Chord]Action, len(raw))}
	var errs []error
	sections := make([]string, 0, len(raw))
	for s := range raw {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	for _, sec := range sections {
		m := make(map[Chord]Action, len(raw[sec]))
		km.Sections[sec] = m
		names := make([]string, 0, len(raw[sec]))
		for k := range raw[sec] {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			c, err := ParseChord(name)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", sec, err))
				continue
			}
			a, err := ParseAction(raw[sec][name])
			if err != nil {
				errs = append(errs, fmt.Errorf("%s.%s: %w", sec, name, err))
				continue
			}
			if _, dup := m[c]; dup {
				errs = append(errs, fmt.Errorf("keys: %s: %q duplicates another binding for %s", sec, name, c))
				continue
			}
			m[c] = a
		}
	}
	return km, errors.Join(errs...)
}

// Default returns a fresh keymap parsed from the embedded default_keys.json (Linux).
func Default() *Keymap {
	km, err := Parse(defaultKeys)
	if err != nil {
		panic(err) // the embedded file is covered by tests
	}
	return km
}

// Lookup returns the action bound to c in section.
func (k *Keymap) Lookup(section string, c Chord) (Action, bool) {
	a, ok := k.Sections[section][c]
	return a, ok
}

// KeysFor returns display strings for the chords bound to command, e.g.
// ["Ctrl+Shift+T"]. command is a bare name ("tab.select" matches any
// argument) or "name:arg" to match one argument. The "global" section comes
// first, then the others by name; within a section chords are sorted by
// display string.
func (k *Keymap) KeysFor(command string) []string {
	name, arg, withArg := strings.Cut(command, ":")
	sections := make([]string, 0, len(k.Sections))
	for s := range k.Sections {
		if s != "global" {
			sections = append(sections, s)
		}
	}
	sort.Strings(sections)
	if _, ok := k.Sections["global"]; ok {
		sections = append([]string{"global"}, sections...)
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range sections {
		var here []string
		for c, a := range k.Sections[s] {
			if a.Terminal || a.None || a.Send != "" || a.Command != name || (withArg && a.Arg != arg) {
				continue
			}
			here = append(here, c.String())
		}
		sort.Strings(here)
		for _, d := range here {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// Spec formats the chord in herdr's syntax, as keys.json writes it:
// "ctrl-shift-t", "alt-[", "ctrl--", "f2". ParseChord reads it back.
func (c Chord) Spec() string {
	var b strings.Builder
	for _, m := range []struct {
		on   bool
		name string
	}{{c.Ctrl, "ctrl"}, {c.Alt, "alt"}, {c.Shift, "shift"}, {c.Super, "super"}} {
		if m.on {
			b.WriteString(m.name)
			b.WriteByte('-')
		}
	}
	b.WriteString(c.Key)
	return b.String()
}

// Spec formats the action as keys.json writes it: "terminal",
// "send:<text>", "command" or "command:arg". ParseAction reads it back.
func (a Action) Spec() string {
	switch {
	case a.None:
		return "none"
	case a.Terminal:
		return "terminal"
	case a.Command == "":
		return "send:" + a.Send
	case a.Arg != "":
		return a.Command + ":" + a.Arg
	}
	return a.Command
}

// Clone returns a copy of the keymap that shares nothing with it.
func (k *Keymap) Clone() *Keymap {
	out := &Keymap{Sections: make(map[string]map[Chord]Action, len(k.Sections))}
	for s, m := range k.Sections {
		cp := make(map[Chord]Action, len(m))
		for c, a := range m {
			cp[c] = a
		}
		out.Sections[s] = cp
	}
	return out
}

// Encode writes the keymap as a keys.json document, sections and chords
// sorted, which Parse reads back to an equal keymap.
func (k *Keymap) Encode() ([]byte, error) {
	raw := make(map[string]map[string]string, len(k.Sections))
	for s, m := range k.Sections {
		sec := make(map[string]string, len(m))
		for c, a := range m {
			sec[c.Spec()] = a.Spec()
		}
		raw[s] = sec
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Bind binds c to a in section, creating the section, and returns the
// action c was bound to before, if any.
func (k *Keymap) Bind(section string, c Chord, a Action) (Action, bool) {
	if k.Sections == nil {
		k.Sections = map[string]map[Chord]Action{}
	}
	m := k.Sections[section]
	if m == nil {
		m = map[Chord]Action{}
		k.Sections[section] = m
	}
	old, had := m[c]
	m[c] = a
	return old, had
}

// Unbind removes the binding of c in section; it reports whether there
// was one.
func (k *Keymap) Unbind(section string, c Chord) bool {
	if _, ok := k.Sections[section][c]; !ok {
		return false
	}
	delete(k.Sections[section], c)
	return true
}

// ChordsFor returns the chords of section bound to the action spec, as
// Action.Spec formats it, sorted by display string.
func (k *Keymap) ChordsFor(section, spec string) []Chord {
	var out []Chord
	for c, a := range k.Sections[section] {
		if a.Spec() == spec {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// Overlay applies over to k: its bindings replace k's, and its "none"
// bindings remove k's.
func (k *Keymap) Overlay(over *Keymap) {
	for s, m := range over.Sections {
		for c, a := range m {
			if a.None {
				k.Unbind(s, c)
			} else {
				k.Bind(s, c, a)
			}
		}
	}
}

// AddFree adds the bindings of other whose chords k does not bind, and
// returns, by section, the chords it left out because k had them.
func (k *Keymap) AddFree(other *Keymap) map[string][]Chord {
	taken := map[string][]Chord{}
	for s, m := range other.Sections {
		for c, a := range m {
			if _, ok := k.Sections[s][c]; ok {
				taken[s] = append(taken[s], c)
				continue
			}
			k.Bind(s, c, a)
		}
		sort.Slice(taken[s], func(i, j int) bool { return taken[s][i].String() < taken[s][j].String() })
	}
	for s, cs := range taken {
		if len(cs) == 0 {
			delete(taken, s)
		}
	}
	return taken
}

// Diff is what turns base into target when applied with Overlay: the
// bindings target adds or changes, and "none" for those it lacks.
func Diff(base, target *Keymap) *Keymap {
	out := &Keymap{Sections: map[string]map[Chord]Action{}}
	for s, m := range target.Sections {
		for c, a := range m {
			if b, ok := base.Sections[s][c]; !ok || b != a {
				out.Bind(s, c, a)
			}
		}
	}
	for s, m := range base.Sections {
		for c := range m {
			if _, ok := target.Sections[s][c]; !ok {
				out.Bind(s, c, Action{None: true})
			}
		}
	}
	return out
}

// Empty reports whether the keymap binds nothing.
func (k *Keymap) Empty() bool {
	for _, m := range k.Sections {
		if len(m) > 0 {
			return false
		}
	}
	return true
}
