// Package mods reads mods: folders of $SHIORI_HOME/mods, each with a
// manifest (mod.json) that names the commands, keys and events a mod adds
// and the programs that serve them. See examples/mods/README.md.
package mods

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/duskoide/shiori/internal/keys"
)

// API is the version of the manifest and protocol this app takes.
const API = 1

// MaxTimeout is the longest a one-shot command may run, in seconds.
const MaxTimeout = 600

// DefaultTimeout is how long a one-shot command runs when its manifest
// says nothing, in seconds.
const DefaultTimeout = 30

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	commandPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// Manifest is a mod.json.
type Manifest struct {
	API         int                          `json:"api"`
	Name        string                       `json:"name"`
	Version     string                       `json:"version,omitempty"`
	Description string                       `json:"description,omitempty"`
	Run         []string                     `json:"run,omitempty"`
	Start       string                       `json:"start,omitempty"`
	Commands    []Command                    `json:"commands,omitempty"`
	Keys        map[string]map[string]string `json:"keys,omitempty"`
	Events      []string                     `json:"events,omitempty"`
}

// Command is a command a mod adds; its name is <mod>.<Name>.
type Command struct {
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	About   string   `json:"about,omitempty"`
	Args    []Arg    `json:"args,omitempty"`
	Run     []string `json:"run,omitempty"`
	Timeout float64  `json:"timeout,omitempty"`
	Hidden  bool     `json:"hidden,omitempty"`
}

// Arg is an argument of a command.
type Arg struct {
	Name     string `json:"name"`
	Required bool   `json:"required,omitempty"`
}

// Mod is a folder of the mods folder, read.
type Mod struct {
	ID       string
	Dir      string
	Manifest *Manifest // nil when it did not read
	Hash     string    // "sha256:<hex>" of mod.json, "" when there is none
	Err      error     // why it did not read
}

// StartsAtStartup reports whether the service starts with the window.
func (m *Manifest) StartsAtStartup() bool { return m.Start == "startup" }

// TimeoutSeconds is how long the command may run, in seconds.
func (c Command) TimeoutSeconds() float64 {
	if c.Timeout <= 0 {
		return DefaultTimeout
	}
	return c.Timeout
}

// ValidID reports whether a folder's name is a mod's id.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Load reads every mod in dir, sorted by id. A folder whose name is not an
// id, or that starts with a dot, is not a mod. events are the names of the
// events a manifest may name. A missing dir is no mods.
func Load(dir string, events []string) []Mod {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Mod
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
			continue
		}
		m := Mod{ID: name, Dir: path}
		if !ValidID(name) {
			m.Err = fmt.Errorf("the folder's name %q is not a mod id: lower case letters, digits and -, at most 32", name)
			out = append(out, m)
			continue
		}
		b, err := os.ReadFile(filepath.Join(path, "mod.json"))
		if err != nil {
			if os.IsNotExist(err) {
				err = errors.New("no mod.json")
			}
			m.Err = err
			out = append(out, m)
			continue
		}
		sum := sha256.Sum256(b)
		m.Hash = "sha256:" + hex.EncodeToString(sum[:])
		m.Manifest, m.Err = Parse(name, b, events)
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Parse reads and checks the manifest of the mod id.
func Parse(id string, b []byte, events []string) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("mod.json: %w", err)
	}
	if dec.More() {
		return nil, errors.New("mod.json: more than one JSON value")
	}
	if err := m.check(id, events); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) check(id string, events []string) error {
	switch {
	case m.API == 0:
		return errors.New(`mod.json has no "api"; this app takes 1`)
	case m.API != API:
		return fmt.Errorf("mod.json is for api %d; this app takes %d", m.API, API)
	case strings.TrimSpace(m.Name) == "":
		return errors.New(`mod.json has no "name"`)
	case len(m.Commands) == 0 && len(m.Events) == 0:
		return errors.New("a mod adds at least one command or event")
	}
	if len(m.Run) > 0 && strings.TrimSpace(m.Run[0]) == "" {
		return errors.New(`"run" names no program`)
	}
	switch m.Start {
	case "", "demand":
	case "startup":
		if len(m.Run) == 0 {
			return errors.New(`"start": "startup" needs a "run"`)
		}
	default:
		return fmt.Errorf(`"start" is "demand" or "startup", not %q`, m.Start)
	}
	if len(m.Events) > 0 && len(m.Run) == 0 {
		return errors.New(`"events" needs a "run": a service to send them to`)
	}
	for _, e := range m.Events {
		if !slices.Contains(events, e) {
			return fmt.Errorf("no event %q; events: %s", e, strings.Join(events, ", "))
		}
	}
	names := map[string]bool{}
	for i, c := range m.Commands {
		where := fmt.Sprintf("commands[%d]", i)
		if !commandPattern.MatchString(c.Name) {
			return fmt.Errorf("%s: the name %q is not letters, digits, - and _", where, c.Name)
		}
		where = fmt.Sprintf("command %q", c.Name)
		if names[c.Name] {
			return fmt.Errorf("%s is there twice", where)
		}
		names[c.Name] = true
		if strings.TrimSpace(c.Title) == "" {
			return fmt.Errorf("%s has no title", where)
		}
		if len(c.Run) == 0 && len(m.Run) == 0 {
			return fmt.Errorf(`%s has no "run", and the mod has no service to run it`, where)
		}
		if len(c.Run) > 0 && strings.TrimSpace(c.Run[0]) == "" {
			return fmt.Errorf(`%s: "run" names no program`, where)
		}
		if c.Timeout < 0 || c.Timeout > MaxTimeout {
			return fmt.Errorf("%s: timeout is 0 to %d seconds", where, MaxTimeout)
		}
		args := map[string]bool{}
		for _, a := range c.Args {
			if strings.TrimSpace(a.Name) == "" || args[a.Name] {
				return fmt.Errorf("%s: arguments need names, each once", where)
			}
			args[a.Name] = true
		}
	}
	if _, err := m.Keymap(id); err != nil {
		return err
	}
	return nil
}

// Keymap is the mod's default key bindings. They may bind the mod's own
// commands alone.
func (m *Manifest) Keymap(id string) (*keys.Keymap, error) {
	km := &keys.Keymap{Sections: map[string]map[keys.Chord]keys.Action{}}
	for section, binds := range m.Keys {
		for chord, action := range binds {
			c, err := keys.ParseChord(chord)
			if err != nil {
				return nil, fmt.Errorf("keys: %w", err)
			}
			act, err := keys.ParseAction(action)
			if err != nil {
				return nil, fmt.Errorf("keys: %w", err)
			}
			name, ok := strings.CutPrefix(act.Command, id+".")
			if !ok || !slices.ContainsFunc(m.Commands, func(c Command) bool { return c.Name == name }) {
				return nil, fmt.Errorf("keys: %s binds %q, which is not one of the mod's commands", chord, action)
			}
			km.Bind(section, c, act)
		}
	}
	return km, nil
}

// Resolve is a program's argv with a path that starts with ./ taken in
// the mod's folder.
func Resolve(dir string, argv []string) []string {
	out := slices.Clone(argv)
	if len(out) > 0 && strings.HasPrefix(out[0], "./") {
		out[0] = filepath.Join(dir, out[0])
	}
	return out
}
