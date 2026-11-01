package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duskoide/shiori/internal/keys"
	"github.com/duskoide/shiori/internal/mods"
)

// Mods are folders of $SHIORI_HOME/mods that add commands, keys and event
// handlers, served by programs that run outside the window. See
// docs/MODS.md. A mod runs once the person turns it on, and again after a
// change to its manifest only once they look at it again.

// ModSetting is a mod's entry in settings.json: whether it is on, for the
// manifest the person looked at.
type ModSetting struct {
	Enabled  bool   `json:"enabled"`
	Manifest string `json:"manifest,omitempty"` // mods.Mod.Hash
}

// reservedIDs are the first words of the app's own commands: a mod of such
// an id would hide among them.
var reservedIDs = []string{"space", "tab", "pane", "agent", "terminal", "layout", "mod", "app", "window"}

// modState is a mod as the window knows it.
type modState struct {
	mods.Mod
	on    bool                    // turned on, readable, its manifest as looked at
	taken map[string][]keys.Chord // its default keys that other bindings had first
	svc   *service                // its service, once started
}

// manifestChanged reports whether the mod was turned on for another
// version of its manifest.
func (st *modState) manifestChanged(cfg Settings) bool {
	s, ok := cfg.Mods[st.ID]
	return ok && s.Enabled && st.Hash != "" && s.Manifest != st.Hash
}

func (a *App) modsDir() string { return filepath.Join(a.home, "mods") }

// reloadMods reads the mods folder again and applies what changed: the
// commands and keys of the mods on, their services (main thread).
func (a *App) reloadMods() {
	list := mods.Load(a.modsDir(), eventNames)
	old := a.mods
	var fresh []string // the names of new mods, told of together
	a.mods = map[string]*modState{}
	a.modOrder = a.modOrder[:0]
	for _, m := range list {
		if m.Err == nil && slices.Contains(reservedIDs, m.ID) {
			m.Err = fmt.Errorf("%q is a word of the app's own commands; name the folder otherwise", m.ID)
			m.Manifest = nil
		}
		st := &modState{Mod: m}
		s, known := a.cfg.Mods[m.ID]
		st.on = m.Err == nil && known && s.Enabled && s.Manifest == m.Hash
		if prev := old[m.ID]; prev != nil && prev.svc != nil {
			if st.on && prev.Hash == m.Hash {
				st.svc = prev.svc
				st.svc.st = st
			} else {
				a.stopService(prev, "its manifest changed or it was turned off")
			}
		}
		a.mods[m.ID] = st
		a.modOrder = append(a.modOrder, m.ID)
		switch {
		case m.Err != nil || st.on || a.modNoticed[m.ID+m.Hash]:
		case !known:
			fresh = append(fresh, strconv.Quote(m.Manifest.Name))
		case st.manifestChanged(a.cfg):
			a.notify(fmt.Sprintf("Mod %q changed: look at it in Settings → Mods to turn it on again", m.Manifest.Name))
		}
		a.modNoticed[m.ID+m.Hash] = true
	}
	switch len(fresh) {
	case 0:
	case 1:
		a.notify("New mod " + fresh[0] + ": turn it on in Settings → Mods")
	default:
		a.notify("New mods " + strings.Join(fresh, ", ") + ": turn them on in Settings → Mods")
	}
	for id, prev := range old {
		if a.mods[id] == nil && prev.svc != nil {
			a.stopService(prev, "it was removed")
		}
	}
	a.modCommands()
	a.buildKeymap()
	a.startServices()
}

// modCommands registers the commands of the mods that are on.
func (a *App) modCommands() {
	a.extra = a.extra[:0]
	for _, id := range a.modOrder {
		st := a.mods[id]
		if !st.on {
			continue
		}
		for _, mc := range st.Manifest.Commands {
			c := &command{Name: id + "." + mc.Name, Title: mc.Title, About: mc.About, Mod: id, hidden: mc.Hidden}
			for _, ar := range mc.Args {
				c.Args = append(c.Args, argDef{Name: ar.Name, Required: ar.Required})
			}
			mc := mc
			c.run = func(a *App, v args) (any, error) {
				st := a.mods[id]
				if st == nil || !st.on {
					return nil, fmt.Errorf("mod %q is off", id)
				}
				if len(mc.Run) > 0 {
					return a.runOneShot(st, mc, v)
				}
				return a.runOnService(st, mc, v)
			}
			a.extra = append(a.extra, c)
		}
	}
}

// modName is a mod's name for people.
func (a *App) modName(id string) string {
	if st := a.mods[id]; st != nil && st.Manifest != nil {
		return st.Manifest.Name
	}
	return id
}

// offModError explains why a command of a mod does not run, or is nil when
// name is no mod's.
func (a *App) offModError(name string) error {
	id, _, ok := strings.Cut(name, ".")
	st := a.mods[id]
	switch {
	case !ok || st == nil:
		return nil
	case st.Err != nil:
		return fmt.Errorf("mod %q does not load: %v", id, st.Err)
	case !st.on:
		return fmt.Errorf("mod %q is off: turn it on in Settings → Mods", a.modName(id))
	}
	return fmt.Errorf("mod %q has no command %q", a.modName(id), name)
}

// setModEnabled turns a mod on or off, for its manifest as it is now.
func (a *App) setModEnabled(id string, on bool) error {
	st := a.mods[id]
	if st == nil {
		return fmt.Errorf("no mod %q", id)
	}
	if on && st.Err != nil {
		return fmt.Errorf("mod %q does not load: %v", id, st.Err)
	}
	if a.cfg.Mods == nil {
		a.cfg.Mods = map[string]ModSetting{}
	}
	a.cfg.Mods[id] = ModSetting{Enabled: on, Manifest: st.Hash}
	a.saveSettings()
	a.reloadMods()
	return nil
}

// buildKeymap makes the keymap in use: the default keys, the keys of the
// mods that are on where their chords are free, and keys.json over them.
func (a *App) buildKeymap() {
	base := keys.Default()
	for _, id := range a.modOrder {
		st := a.mods[id]
		st.taken = nil
		if !st.on {
			continue
		}
		if km, err := st.Manifest.Keymap(id); err == nil {
			st.taken = base.AddFree(km)
		}
	}
	a.keysBase = base
	km := base.Clone()
	if a.keysUser != nil {
		km.Overlay(a.keysUser)
	}
	a.keymap = km
}

// --- One-shot commands --------------------------------------------------

// modEnv is the environment of a mod's programs: the window's, with where
// to reach it and what has the focus.
func (a *App) modEnv(st *modState) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "SHIORI_") })
	set := func(k, v string) { env = append(env, k+"="+v) }
	set("SHIORI_API", fmt.Sprint(mods.API))
	set("SHIORI_SOCKET", socketPath(a.home))
	set("SHIORI_HOME", a.home)
	set("SHIORI_MOD", st.ID)
	if exe, err := os.Executable(); err == nil {
		set("SHIORI_BIN", exe)
	}
	if a.session != "" {
		set("HC_SESSION", a.session)
	}
	f := a.snapshot().focus
	set("SHIORI_SPACE", f.space)
	set("SHIORI_TAB", f.tab)
	set("SHIORI_PANE", f.pane)
	set("SHIORI_TERMINAL", f.terminal)
	set("SHIORI_CWD", f.cwd)
	return env
}

// runOneShot runs a command's own program and answers with what it prints.
func (a *App) runOneShot(st *modState, mc mods.Command, v args) (any, error) {
	argv := mods.Resolve(st.Dir, mc.Run)
	name := st.ID + "." + mc.Name
	argsJSON, _ := json.Marshal(v)
	env := append(a.modEnv(st), "SHIORI_COMMAND="+name, "SHIORI_ARGS="+string(argsJSON))
	limit := time.Duration(mc.TimeoutSeconds() * float64(time.Second))
	d := a.newDeferred(limit+10*time.Second, nil)
	log := a.modLog(st)
	dir := st.Dir
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = dir, env
		out := &capped{max: 4 << 20}
		errs := &capped{max: 64 << 10}
		cmd.Stdout = out
		cmd.Stderr = io.MultiWriter(errs, log)
		ownGroup(cmd)
		err := cmd.Run()
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			d.resolve(nil, fmt.Errorf("took longer than %s", limit))
		case err != nil:
			msg := lastLine(errs.Bytes())
			if msg == "" {
				msg = err.Error()
			}
			d.resolve(nil, errors.New(msg))
		default:
			d.resolve(resultOf(out.Bytes()), nil)
		}
	}()
	return d, nil
}

// resultOf is what a program printed as a command's result: its JSON, or
// its text as output.
func resultOf(out []byte) any {
	t := bytes.TrimSpace(out)
	switch {
	case len(t) == 0:
		return nil
	case json.Valid(t):
		return json.RawMessage(t)
	}
	return map[string]string{"output": string(t)}
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// capped keeps what is written to it, up to max bytes.
type capped struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.buf.Bytes())
}

// --- Logs -----------------------------------------------------------------

// logMax is how large a mod's log grows before its older half goes.
const logMax = 1 << 20

// modLog is where a mod's programs' stderr goes: mods/<id>/log.
func (a *App) modLog(st *modState) io.Writer {
	return &logWriter{path: filepath.Join(st.Dir, "log")}
}

type logWriter struct{ path string }

var logLocks sync.Map // path → *sync.Mutex: writers of one log take turns

func (w *logWriter) Write(p []byte) (int, error) {
	l, _ := logLocks.LoadOrStore(w.path, &sync.Mutex{})
	mu := l.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return len(p), nil // a log that cannot be written is no reason to fail
	}
	f.Write(p)
	size := int64(0)
	if fi, err := f.Stat(); err == nil {
		size = fi.Size()
	}
	f.Close()
	if size > logMax {
		if b, err := os.ReadFile(w.path); err == nil && len(b) > logMax/2 {
			tail := b[len(b)-logMax/2:]
			if i := bytes.IndexByte(tail, '\n'); i >= 0 {
				tail = tail[i+1:]
			}
			os.WriteFile(w.path, tail, 0o644)
		}
	}
	return len(p), nil
}

// modLogTail is the last lines of a mod's log.
func modLogTail(st *modState, lines int) string {
	b, err := os.ReadFile(filepath.Join(st.Dir, "log"))
	if err != nil {
		return ""
	}
	all := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return strings.Join(all[max(0, len(all)-lines):], "\n")
}
