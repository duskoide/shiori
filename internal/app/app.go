package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/keys"
	"github.com/duskoide/shiori/internal/layout"
	"github.com/duskoide/shiori/internal/theme"
	"github.com/duskoide/shiori/internal/update"
)

// docName is the core document the layout lives in: the one herdr-app
// keeps, so that both apps show the same spaces, tabs and splits.
const docName = "herdr-app"

// backend is what the app asks of herdr-core. *core.Client is one; tests
// use a fake.
type backend interface {
	Run(ctx context.Context, p core.RunParams) (string, error)
	Kill(ctx context.Context, address string) error
	Label(ctx context.Context, address string, labels map[string]string) error
	Keep(ctx context.Context, address string, keep bool) error
	Get(ctx context.Context, address string) (core.Terminal, error)
	DocSet(ctx context.Context, name, on string, version uint64, value json.RawMessage) (uint64, error)
	DocGet(ctx context.Context, name, on string) (json.RawMessage, uint64, error)
	Machines(ctx context.Context) ([]core.Machine, error)
}

// App is the window's state. Everything in it belongs to the main thread:
// other goroutines hand work over with post.
type App struct {
	home    string
	session string

	win     *mygo.Window // main thread only; goroutines use invWin
	focused bool         // the window has the keyboard focus

	// What goroutines may touch: they wake the window with invalidate.
	// invWin is the window once the first frame bound it, closed is set
	// when it closed, and services (kept under postMu) are the frame's.
	invWin   atomic.Pointer[mygo.Window]
	closed   atomic.Bool
	services ui.Services

	postMu sync.Mutex
	posted []func()

	saveTimer *time.Timer // the delayed write of settings.json, if one is due

	// The connection to the core; nil while there is none.
	client    *core.Client
	core      backend
	connected bool
	connErr   string
	stop      context.CancelFunc

	// The core's terminals, as its event stream describes them.
	terminals map[string]core.Terminal

	// The layout: doc is what the window shows, every machine's spaces
	// (see layout/machines.go), and docs each machine's document's sync
	// ("" this one's). machines are the core's machines, temporary ones
	// left out, by name.
	doc       *layout.Document
	docs      map[string]*docSync
	machines  []core.Machine
	mach      machinesState
	hc        *core.HC // the hc command, for machines (machines.go)
	dragging  bool
	pending   map[string]time.Time // terminals started here, not yet in a pane
	spawning  map[string]int       // tabs whose terminals are starting here
	gone      map[string]bool      // terminals whose panes were closed here
	seenTurns map[string]uint64    // the herdr.seen this window wrote, once the core took it
	seenBusy  map[string]time.Time // herdr.seen writes in flight or to retry, no new one before then
	sy        syncState
	rc        recoveryState // terminals to start again after the core stopped (recovery.go)

	// What this window looks at, which the document does not keep.
	activeSpace string
	activeTab   map[string]string   // space → tab
	focus       map[string]string   // tab → pane
	history     map[string][]string // tab → panes focused before, latest last
	zoomed      map[string]string   // tab → pane
	focusReq    string              // the pane to give the keyboard focus next frame
	localSeen   map[string]bool     // agents looked at in this window
	agentOrder  []string            // the agents list's order, for agent.next/prev

	terms map[string]*paneTerm // by terminal address
	retry map[string]time.Time // terminals not to attach again before then

	// Commands besides the app's own (the mods'), and the replies still
	// waiting for an answer that can be cancelled.
	extra   []*command
	waiting map[*deferred]bool
	dialogs []*queuedDialog // the dialog commands' dialogs, in line

	// The control socket's sessions, and the state events are worked out
	// from (see events.go).
	sessions map[*rpcSession]bool
	snap     *stateSnap
	eventSeq uint64

	// Overlays.
	palette  paletteState
	prompt   *promptState
	confirm  *confirmState
	settings bool
	set      settingsState // the settings panel's own state
	chg      changesState  // the changes panel

	// The look for new versions (updates.go).
	upd         updateState
	updOpts     update.Options
	updTimer    *time.Timer
	updChecking bool
	updWaiters  []*deferred
	toasts      []toast

	keymap  *keys.Keymap
	keysRaw []byte // keys.json as last read, to tell a change from a rewrite
	keysErr string // what was wrong with keys.json as last read
	// The keymap in use is keysBase (the default keys and the mods') with
	// keysUser (keys.json) over it.
	keysBase, keysUser *keys.Keymap

	// The mods folder as last read, by id and in order, and the new or
	// changed mods already told of.
	mods       map[string]*modState
	started    bool // start ran: services may start
	modOrder   []string
	modNoticed map[string]bool
	cfg        Settings
	k          *colors
	sidebarW   float32
}

// Settings are the person's preferences, settings.json in the app's home.
type Settings struct {
	Theme    string  `json:"theme,omitempty"`
	Font     string  `json:"font,omitempty"`
	FontSize float32 `json:"font_size,omitempty"`
	Sidebar  *bool   `json:"sidebar,omitempty"`
	// Changes shows the changes panel.
	Changes *bool `json:"changes,omitempty"`
	// UpdateCheck false stops the daily look for a new version.
	UpdateCheck *bool `json:"update_check,omitempty"`
	// Restore false closes the panes whose terminals herdr-core lost when
	// it stopped, instead of starting fresh shells in them.
	Restore *bool `json:"restore,omitempty"`
	// Mods are the mods turned on or off, by id.
	Mods map[string]ModSetting `json:"mods,omitempty"`
}

// The terminals' text size: the default, and the range it keeps to.
const (
	defaultFontSize = 13
	minFontSize     = 6
	maxFontSize     = 40
)

func (s Settings) fontSize() float32 {
	if s.FontSize <= 0 {
		return defaultFontSize
	}
	return clampFontSize(s.FontSize)
}

func clampFontSize(v float32) float32 { return min(max(v, minFontSize), maxFontSize) }

func (s Settings) sidebarShown() bool { return s.Sidebar == nil || *s.Sidebar }

// newApp makes the app's state, its settings and keys read from home.
func newApp(home, session string) *App {
	a := &App{
		home:       home,
		session:    session,
		focused:    true,
		terminals:  map[string]core.Terminal{},
		docs:       map[string]*docSync{"": {}},
		pending:    map[string]time.Time{},
		spawning:   map[string]int{},
		gone:       map[string]bool{},
		seenTurns:  map[string]uint64{},
		seenBusy:   map[string]time.Time{},
		activeTab:  map[string]string{},
		focus:      map[string]string{},
		history:    map[string][]string{},
		zoomed:     map[string]string{},
		localSeen:  map[string]bool{},
		terms:      map[string]*paneTerm{},
		retry:      map[string]time.Time{},
		waiting:    map[*deferred]bool{},
		sessions:   map[*rpcSession]bool{},
		mods:       map[string]*modState{},
		modNoticed: map[string]bool{},
		sidebarW:   sidebarWidth,
	}
	a.loadSettings()
	a.loadSnapshot()
	a.upd, a.updOpts = loadUpdateState(home), update.FromEnv()
	a.reloadMods()
	a.loadKeys()
	a.applyTheme()
	return a
}

// DefaultHome is where the app keeps its files: $SHIORI_HOME, else
// ~/.config/shiori.
func DefaultHome() string {
	if h := os.Getenv("SHIORI_HOME"); h != "" {
		return h
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	home := filepath.Join(dir, "shiori")
	migrateHome(filepath.Join(dir, "herdr-mygo"), home)
	return home
}

// migrateHome moves the files of the app's earlier name (herdr-mygo) to
// home, once, unless a window of that version still runs from them.
func migrateHome(old, home string) {
	if _, err := os.Stat(home); err == nil {
		return
	}
	if _, err := os.Stat(old); err != nil {
		return
	}
	if c, err := net.Dial("unix", filepath.Join(old, "herdr-mygo.sock")); err == nil {
		c.Close()
		return
	}
	os.Remove(filepath.Join(old, "herdr-mygo.sock"))
	_ = os.Rename(old, home)
}

func (a *App) loadSettings() {
	b, err := os.ReadFile(filepath.Join(a.home, "settings.json"))
	if err != nil {
		if os.IsNotExist(err) {
			a.cfg = Settings{}
		}
		return
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		a.notify("settings.json: " + err.Error())
		return
	}
	a.cfg = s
}

func (a *App) saveSettings() {
	b, err := json.MarshalIndent(a.cfg, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(a.home, 0o755); err != nil {
		a.notify(err.Error())
		return
	}
	if err := os.WriteFile(filepath.Join(a.home, "settings.json"), append(b, '\n'), 0o644); err != nil {
		a.notify(err.Error())
	}
}

// loadKeys reads keys.json from home over the default keys.
// keys.json changes the default keys (and the mods'): it binds chords
// anew, and "none" removes a binding.
func (a *App) loadKeys() {
	defer a.buildKeymap()
	b, err := os.ReadFile(filepath.Join(a.home, "keys.json"))
	a.keysRaw, a.keysErr = b, ""
	if err != nil {
		a.keysUser = nil
		return
	}
	km, err := keys.Parse(b)
	if km != nil {
		// The keymap holds the valid bindings even when others failed.
		a.keysUser = km
	} // a file that cannot be read at all keeps the keys in use
	if err != nil {
		a.keysErr = err.Error()
		a.notify("keys.json: " + err.Error())
	}
}

// applyTheme resolves the chosen theme's tokens.
func (a *App) applyTheme() {
	p, ok := theme.Preset(a.cfg.Theme)
	if !ok {
		p, _ = theme.Preset("graphite")
	}
	a.k = colorsFrom(theme.Resolve(p))
	for _, pt := range a.terms {
		pt.term.SetTheme(a.k.terminalTheme(), a.k.terminalTheme())
		// The core answers color queries from the facts it was given.
		_ = pt.stream.SetDisplay(a.display())
	}
}

// display is the colors of the current theme, as the core wants them.
func (a *App) display() core.Display {
	return core.Display{FG: hexOf(a.k.terminalForeground), BG: hexOf(a.k.background), Cursor: hexOf(a.k.cursor)}
}

// post runs fn on the main thread with the next frame.
func (a *App) post(fn func()) {
	a.postMu.Lock()
	a.posted = append(a.posted, fn)
	a.postMu.Unlock()
	a.invalidate()
}

// invalidate asks for a new frame. It may be called from any goroutine.
func (a *App) invalidate() {
	if a.closed.Load() {
		return
	}
	if w := a.invWin.Load(); w != nil {
		w.Invalidate()
		return
	}
	a.postMu.Lock()
	s := a.services
	a.postMu.Unlock()
	s.Invalidate()
}

// bindWindow makes the window known to goroutines (main thread). It is
// called with each frame until it takes, and sees to the window's end: no
// invalidate after it closed, and the settings written.
func (a *App) bindWindow(w *mygo.Window) {
	if w == nil || a.invWin.Load() == w {
		return
	}
	a.invWin.Store(w)
	w.OnClosed(func() {
		a.closed.Store(true)
		a.flushSettings()
	})
}

// setServices keeps the frame's services for invalidate.
func (a *App) setServices(s ui.Services) {
	a.postMu.Lock()
	a.services = s
	a.postMu.Unlock()
}

// settingsDelay is how long the write of settings.json waits for more
// changes, as with the steps of a zoom.
const settingsDelay = 500 * time.Millisecond

// saveSettingsSoon writes settings.json after a short delay, one write for
// all the changes made until then.
func (a *App) saveSettingsSoon() {
	if a.saveTimer != nil {
		a.saveTimer.Reset(settingsDelay)
		return
	}
	a.saveTimer = time.AfterFunc(settingsDelay, func() {
		a.post(a.flushSettings)
	})
}

// flushSettings writes the settings now if a delayed write is due.
func (a *App) flushSettings() {
	if a.saveTimer == nil {
		return
	}
	a.saveTimer.Stop()
	a.saveTimer = nil
	a.saveSettings()
}

// runPosted runs the closures posted since the last frame. One that
// panics is reported and does not stop the others.
func (a *App) runPosted() {
	for {
		a.postMu.Lock()
		fns := a.posted
		a.posted = nil
		a.postMu.Unlock()
		if len(fns) == 0 {
			return
		}
		for _, fn := range fns {
			a.runGuarded(fn)
		}
	}
}

// runGuarded runs fn, turning a panic into a message.
func (a *App) runGuarded(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			a.notify(fmt.Sprintf("internal error: %v", r))
		}
	}()
	fn()
}

// toast is a short message over the window.
type toast struct {
	text  string
	level level
	until time.Time
}

// level is how a toast looks: news, or something that went wrong.
type level int

const (
	levelInfo level = iota
	levelError
)

// notify shows a message for a few seconds.
func (a *App) notify(text string) { a.notifyLevel(text, levelInfo) }

// notifyLevel shows a message for a few seconds, an error for longer.
func (a *App) notifyLevel(text string, lv level) {
	d := 5 * time.Second
	if lv == levelError {
		d = 8 * time.Second
	}
	a.toasts = append(a.toasts, toast{text: text, level: lv, until: time.Now().Add(d)})
	if len(a.toasts) > 4 {
		a.toasts = a.toasts[len(a.toasts)-4:]
	}
}

// homeDir is the person's home directory, for naming folders.
func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
