// Package programs maps what runs in a terminal (an agent's reported app, or
// the process argv) to a display identity: name, icon and tile colours.
//
// A built-in table (programs.json) is embedded. A user's programs.json can be
// merged over it with Load; the package-level functions use the active table,
// which is safe for concurrent use. Use Default and Table.Merge for private tables.
package programs

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed programs.json
var builtIn []byte

//go:embed icons
var iconFS embed.FS

// Program is how a terminal's program is shown.
type Program struct {
	// Key is the table key ("claude", "shell", "program", ...).
	Key string
	// Name is the display name.
	Name string
	// Icon is the icon base name for IconSVG ("claude"); empty if none.
	Icon string
	// Tint reports a one-colour glyph painted in Color; false means a full-colour image.
	Tint bool
	// Tile and Color are the tile background and glyph colour (#rrggbb).
	Tile, Color string
	// Shell and Agent classify the program.
	Shell, Agent bool
	// Run is the argv that starts an agent from a menu; nil otherwise.
	Run []string
}

type entry struct {
	Name  string   `json:"name"`
	Icon  string   `json:"icon"`
	Tint  *bool    `json:"tint"`
	Tile  string   `json:"tile"`
	Color string   `json:"color"`
	Match []string `json:"match"`
	Run   []string `json:"run"`
}

// Table is a program table: shells, interpreters and per-key entries.
type Table struct {
	shells       []string
	interpreters []string
	programs     map[string]entry
	keys         []string // sorted keys, for deterministic lookup
}

type tableFile struct {
	Shells       []string         `json:"shells"`
	Interpreters []string         `json:"interpreters"`
	Programs     map[string]entry `json:"programs"`
}

func parse(b []byte) (tableFile, error) {
	var f tableFile
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("programs: %w", err)
	}
	return f, nil
}

// Default returns a fresh copy of the built-in table.
func Default() *Table {
	f, err := parse(builtIn)
	if err != nil {
		panic(err) // the embedded file is covered by tests
	}
	t := &Table{shells: f.Shells, interpreters: f.Interpreters, programs: f.Programs}
	t.reindex()
	return t
}

func (t *Table) reindex() {
	t.keys = t.keys[:0]
	for k := range t.programs {
		t.keys = append(t.keys, k)
	}
	sort.Strings(t.keys)
}

// Merge applies a user programs.json over t: shells and interpreters are
// appended, entries merge field by field (a non-empty field replaces the old
// one; match and run replace as a whole). On error t is unchanged.
func (t *Table) Merge(user []byte) error {
	f, err := parse(user)
	if err != nil {
		return err
	}
	t.shells = append(t.shells[:len(t.shells):len(t.shells)], f.Shells...)
	t.interpreters = append(t.interpreters[:len(t.interpreters):len(t.interpreters)], f.Interpreters...)
	next := make(map[string]entry, len(t.programs)+len(f.Programs))
	for k, e := range t.programs {
		next[k] = e
	}
	for k, m := range f.Programs {
		e := next[k]
		if m.Name != "" {
			e.Name = m.Name
		}
		if m.Icon != "" {
			e.Icon = m.Icon
		}
		if m.Tint != nil {
			e.Tint = m.Tint
		}
		if m.Tile != "" {
			e.Tile = m.Tile
		}
		if m.Color != "" {
			e.Color = m.Color
		}
		if len(m.Match) > 0 {
			e.Match = m.Match
		}
		if len(m.Run) > 0 {
			e.Run = m.Run
		}
		next[k] = e
	}
	t.programs = next
	t.reindex()
	return nil
}

var (
	mu     sync.RWMutex
	active = Default()
)

// Load resets the active table to the built-in one and merges user (the
// contents of the user's programs.json) over it. A nil or empty user just
// resets. On error the active table is the built-in one and the error is returned.
func Load(user []byte) error {
	t := Default()
	var err error
	if len(strings.TrimSpace(string(user))) > 0 {
		err = t.Merge(user)
		if err != nil {
			t = Default()
		}
	}
	mu.Lock()
	active = t
	mu.Unlock()
	return err
}

func current() *Table {
	mu.RLock()
	defer mu.RUnlock()
	return active
}

// Resolve describes a terminal using the active table. app is the agent's
// OSC 7501 app name (may be empty), argv the process command line.
func Resolve(app string, argv []string) Program { return current().Resolve(app, argv) }

// Startable lists agents with a run argv in menu order, from the active table.
func Startable() []Program { return current().Startable() }

// IconSVG returns the embedded icon icons/<name>.svg, or nil. name may also be
// "x.svg" or "icons/x.svg".
func IconSVG(name string) []byte {
	name = strings.TrimSuffix(strings.TrimPrefix(name, "icons/"), ".svg")
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil
	}
	b, err := iconFS.ReadFile("icons/" + name + ".svg")
	if err != nil {
		return nil
	}
	return b
}

// menuFirst are the agents shown first in menus.
var menuFirst = []string{"claude", "codex", "pi", "opencode", "cursor", "copilot", "amp", "gemini", "grok"}

// Startable lists the agents that have a run argv in menu order: the popular
// ones first, then the rest alphabetically by key.
func (t *Table) Startable() []Program {
	rank := func(k string) int {
		for i, f := range menuFirst {
			if f == k {
				return i
			}
		}
		return len(menuFirst)
	}
	var keys []string
	for _, k := range t.keys {
		if len(t.programs[k].Run) > 0 {
			keys = append(keys, k)
		}
	}
	sort.SliceStable(keys, func(i, j int) bool { return rank(keys[i]) < rank(keys[j]) })
	out := make([]Program, len(keys))
	for i, k := range keys {
		out[i] = t.show(k, t.programs[k], "", false, true)
	}
	return out
}

func (t *Table) show(key string, e entry, name string, shell, agent bool) Program {
	if name == "" {
		name = e.Name
	}
	if name == "" {
		name = key
	}
	p := Program{
		Key: key, Name: name, Tint: e.Tint == nil || *e.Tint,
		Tile: e.Tile, Color: e.Color, Shell: shell, Agent: agent,
		Icon: strings.TrimSuffix(strings.TrimPrefix(e.Icon, "icons/"), ".svg"),
	}
	if len(e.Run) > 0 {
		p.Run = append([]string(nil), e.Run...)
	}
	return p
}

// find looks a name up by key, then by match alias, case-insensitively.
func (t *Table) find(name string) (string, entry, bool) {
	lower := strings.ToLower(name)
	for _, k := range t.keys {
		if k == name || strings.ToLower(k) == lower {
			return k, t.programs[k], true
		}
	}
	for _, k := range t.keys {
		for _, a := range t.programs[k].Match {
			if a == name || strings.ToLower(a) == lower {
				return k, t.programs[k], true
			}
		}
	}
	return "", entry{}, false
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// base is the program name of a path: last element, leading '-' (login
// shells) and a ".exe" suffix removed.
func base(s string) string {
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimLeft(s, "-")
	if len(s) > 4 && strings.EqualFold(s[len(s)-4:], ".exe") {
		s = s[:len(s)-4]
	}
	return s
}

// Resolve describes a terminal: the agent-reported app wins; otherwise the
// argv is inspected (see package docs of the rules in the app spec).
func (t *Table) Resolve(app string, argv []string) Program {
	if app != "" {
		if k, e, ok := t.find(app); ok {
			return t.show(k, e, "", false, true)
		}
		return t.show("agent", t.programs["agent"], app, false, true)
	}
	if inner, ok := t.unwrap(argv); ok {
		return t.Resolve("", inner)
	}
	if len(argv) == 0 {
		return t.show("shell", t.programs["shell"], "", true, false)
	}
	first := base(argv[0])
	if has(t.interpreters, first) {
		for _, a := range argv[1:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			name := base(a)
			for _, x := range []string{".js", ".mjs", ".cjs", ".ts", ".py", ".rb"} {
				if s, ok := strings.CutSuffix(name, x); ok {
					name = s
					break
				}
			}
			if k, e, ok := t.find(name); ok && !has(t.interpreters, k) {
				return t.show(k, e, "", false, false)
			}
		}
	}
	if k, e, ok := t.find(first); ok {
		return t.show(k, e, "", false, false)
	}
	if has(t.shells, first) {
		return t.show("shell", t.programs["shell"], first, true, false)
	}
	e := t.programs["program"]
	e.Tile, e.Color = hashTile(first), "#ffffff"
	return t.show("program", e, first, false, false)
}

// unwrap recognises the commands herdr wraps in the user's shell:
// zsh/bash "-ic '"$@"; exec "$0" -i' sh CMD...", fish
// "-ic 'command $argv[2..]; exec $argv[1] -i' fish CMD...", and
// PowerShell "-NoExit -Command \"& 'cmd' 'arg'\"".
func (t *Table) unwrap(argv []string) ([]string, bool) {
	if len(argv) > 4 && has(t.shells, base(argv[0])) && strings.HasPrefix(argv[1], "-") && strings.HasSuffix(argv[1], "c") &&
		(strings.Contains(argv[2], `"$@"`) || argv[2] == "command $argv[2..]; exec $argv[1] -i") {
		return argv[4:], true
	}
	if len(argv) == 4 && has(t.shells, base(argv[0])) && argv[1] == "-NoExit" && argv[2] == "-Command" {
		if w, ok := powershellWords(argv[3]); ok {
			return w, true
		}
	}
	return nil, false
}

func isPSQuote(r rune) bool {
	return r == '\'' || r == '\u2018' || r == '\u2019' || r == '\u201a' || r == '\u201b'
}

// powershellWords splits "& 'a' 'b c'" into words; a doubled quote is a literal quote.
func powershellWords(line string) ([]string, bool) {
	rest, ok := strings.CutPrefix(line, "& ")
	if !ok {
		return nil, false
	}
	r := []rune(rest)
	var words []string
	for i := 0; i < len(r); i++ {
		if r[i] == ' ' {
			continue
		}
		if !isPSQuote(r[i]) {
			return nil, false
		}
		var w []rune
		for i++; i < len(r); i++ {
			if isPSQuote(r[i]) {
				if i+1 < len(r) && isPSQuote(r[i+1]) {
					w = append(w, r[i])
					i++
					continue
				}
				break
			}
			w = append(w, r[i])
		}
		words = append(words, string(w))
	}
	return words, true
}

var tilePalette = [8]string{"#6d5bd0", "#2f80ed", "#0f9d8a", "#d9822b", "#c2417d", "#3a7d44", "#8a5a44", "#4e5d94"}

// hashTile picks a stable tile colour for an unlisted program (FNV-1a 32).
func hashTile(name string) string {
	h := uint32(2166136261)
	for i := 0; i < len(name); i++ {
		h = (h ^ uint32(name[i])) * 16777619
	}
	return tilePalette[h%uint32(len(tilePalette))]
}
