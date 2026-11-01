package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/duskoide/herdr-mygo/internal/core"
	"github.com/duskoide/herdr-mygo/internal/keys"
	"github.com/duskoide/herdr-mygo/internal/layout"
	"github.com/duskoide/herdr-mygo/internal/theme"
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
	DocSet(ctx context.Context, name, on string, version uint64, value json.RawMessage) (uint64, error)
	DocGet(ctx context.Context, name, on string) (json.RawMessage, uint64, error)
}

// App is the window's state. Everything in it belongs to the main thread:
// other goroutines hand work over with post.
type App struct {
	home    string
	session string

	win      *mygo.Window
	services ui.Services
	focused  bool // the window has the keyboard focus

	postMu sync.Mutex
	posted []func()

	// The connection to the core; nil while there is none.
	client    *core.Client
	core      backend
	connected bool
	connErr   string
	stop      context.CancelFunc

	// The core's terminals, as its event stream describes them.
	terminals map[string]core.Terminal

	// The layout document: doc is what the window shows, base the last
	// version the core has, of number version.
	doc       *layout.Document
	base      *layout.Document
	version   uint64
	loaded    bool
	saving    bool
	dirty     bool
	refetch   bool // a newer document arrived while a save was in flight
	dragging  bool
	pending   map[string]time.Time // terminals started here, not yet in a pane
	spawning  map[string]int       // tabs whose terminals are starting here
	gone      map[string]bool      // terminals whose panes were closed here
	seenTurns map[string]uint64    // the herdr.seen this window last wrote

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

	// Overlays.
	palette  paletteState
	prompt   *promptState
	confirm  *confirmState
	settings bool
	toasts   []toast

	keymap   *keys.Keymap
	cfg      Settings
	k        *colors
	sidebarW float32
}

// Settings are the person's preferences, settings.json in the app's home.
type Settings struct {
	Theme    string  `json:"theme,omitempty"`
	Font     string  `json:"font,omitempty"`
	FontSize float32 `json:"font_size,omitempty"`
	Sidebar  *bool   `json:"sidebar,omitempty"`
}

func (s Settings) fontSize() float32 {
	if s.FontSize <= 0 {
		return 13
	}
	return min(max(s.FontSize, 6), 40)
}

func (s Settings) sidebarShown() bool { return s.Sidebar == nil || *s.Sidebar }

// newApp makes the app's state, its settings and keys read from home.
func newApp(home, session string) *App {
	a := &App{
		home:      home,
		session:   session,
		focused:   true,
		terminals: map[string]core.Terminal{},
		pending:   map[string]time.Time{},
		spawning:  map[string]int{},
		gone:      map[string]bool{},
		seenTurns: map[string]uint64{},
		activeTab: map[string]string{},
		focus:     map[string]string{},
		history:   map[string][]string{},
		zoomed:    map[string]string{},
		localSeen: map[string]bool{},
		terms:     map[string]*paneTerm{},
		retry:     map[string]time.Time{},
		sidebarW:  sidebarWidth,
	}
	a.loadSettings()
	a.loadKeys()
	a.applyTheme()
	return a
}

// defaultHome is where the app keeps its files: $HERDR_MYGO_HOME, else
// ~/.config/herdr-mygo.
func defaultHome() string {
	if h := os.Getenv("HERDR_MYGO_HOME"); h != "" {
		return h
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "herdr-mygo")
}

func (a *App) loadSettings() {
	b, err := os.ReadFile(filepath.Join(a.home, "settings.json"))
	if err != nil {
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
func (a *App) loadKeys() {
	a.keymap = keys.Default()
	b, err := os.ReadFile(filepath.Join(a.home, "keys.json"))
	if err != nil {
		return
	}
	km, err := keys.Parse(b)
	if err != nil {
		a.notify("keys.json: " + err.Error())
		return
	}
	a.keymap = km
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
	}
}

// post runs fn on the main thread with the next frame.
func (a *App) post(fn func()) {
	a.postMu.Lock()
	a.posted = append(a.posted, fn)
	a.postMu.Unlock()
	a.invalidate()
}

func (a *App) invalidate() {
	if a.win != nil {
		a.win.Invalidate()
		return
	}
	a.services.Invalidate()
}

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
			fn()
		}
	}
}

// toast is a short message over the window.
type toast struct {
	text  string
	until time.Time
}

// notify shows a message for a few seconds.
func (a *App) notify(text string) {
	a.toasts = append(a.toasts, toast{text: text, until: time.Now().Add(5 * time.Second)})
	if len(a.toasts) > 4 {
		a.toasts = a.toasts[len(a.toasts)-4:]
	}
}

// homeDir is the person's home directory, for naming folders.
func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
