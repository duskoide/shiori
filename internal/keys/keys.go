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
}

// ParseAction parses "command", "command:value", "send:<text>" or "terminal".
func ParseAction(s string) (Action, error) {
	switch {
	case s == "terminal":
		return Action{Terminal: true}, nil
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
			if a.Terminal || a.Send != "" || a.Command != name || (withArg && a.Arg != arg) {
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
