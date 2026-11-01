package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
)

// labelBackend is a backend that only takes labels.
type labelBackend struct {
	mu     sync.Mutex
	err    error
	labels []map[string]string
	addrs  []string
}

func (b *labelBackend) Label(_ context.Context, address string, labels map[string]string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addrs = append(b.addrs, address)
	b.labels = append(b.labels, labels)
	return b.err
}

func (b *labelBackend) setErr(err error) {
	b.mu.Lock()
	b.err = err
	b.mu.Unlock()
}

func (b *labelBackend) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.addrs)
}

func (*labelBackend) Run(context.Context, core.RunParams) (string, error) { return "", nil }
func (*labelBackend) Kill(context.Context, string) error                  { return nil }
func (*labelBackend) Keep(context.Context, string, bool) error            { return nil }
func (*labelBackend) Get(context.Context, string) (core.Terminal, error)  { return core.Terminal{}, nil }
func (*labelBackend) DocSet(context.Context, string, string, uint64, json.RawMessage) (uint64, error) {
	return 0, nil
}
func (*labelBackend) DocGet(context.Context, string, string) (json.RawMessage, uint64, error) {
	return nil, 0, nil
}
func (*labelBackend) Machines(context.Context) ([]core.Machine, error) { return nil, nil }

// twoPaneApp has a tab of two panes showing t1 and t2, t1 focused.
func twoPaneApp(t *testing.T) (*App, *labelBackend) {
	t.Helper()
	a := newApp(t.TempDir(), "x")
	a.doc = &layout.Document{}
	s := a.doc.AddSpace("s", "/tmp")
	tab, p1 := s.AddTab("t", "t1")
	if _, err := tab.Split(p1.ID, layout.Right, "t2"); err != nil {
		t.Fatal(err)
	}
	a.activeSpace = s.ID
	a.activeTab[s.ID] = tab.ID
	a.focus[tab.ID] = p1.ID
	be := &labelBackend{}
	a.core = be
	return a, be
}

func idle(addr string, turn uint64, labels map[string]string) core.Terminal {
	return core.Terminal{Address: addr, State: "live", Turn: turn, Labels: labels, Status: &core.Status{State: "idle"}}
}

func pumpUntil(t *testing.T, a *App, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		a.runPosted()
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestLookedAtWritesMissingLabelAtTurnZero(t *testing.T) {
	a, be := twoPaneApp(t)
	a.terminals["t1"] = idle("t1", 0, nil)
	a.lookedAt()
	pumpUntil(t, a, func() bool { return be.calls() == 1 && len(a.seenBusy) == 0 })
	if got := be.labels[0]["herdr.seen"]; got != "0" {
		t.Fatalf("label %q", got)
	}
	a.lookedAt() // written: not again
	time.Sleep(30 * time.Millisecond)
	if be.calls() != 1 {
		t.Fatalf("%d writes", be.calls())
	}
}

func TestLookedAtCountsEveryVisiblePane(t *testing.T) {
	a, be := twoPaneApp(t)
	a.terminals["t1"] = idle("t1", 2, map[string]string{"herdr.seen": "2"})
	a.terminals["t2"] = idle("t2", 3, map[string]string{"herdr.seen": "1"})
	a.lookedAt()
	pumpUntil(t, a, func() bool { return be.calls() == 1 && len(a.seenBusy) == 0 })
	if be.addrs[0] != "t2" || be.labels[0]["herdr.seen"] != "3" || !a.localSeen["t1"] || !a.localSeen["t2"] {
		t.Fatalf("%v %v %v", be.addrs, be.labels, a.localSeen)
	}
	// A zoomed tab shows one pane.
	a2, be2 := twoPaneApp(t)
	tab := a2.tab()
	a2.zoomed[tab.ID] = a2.focus[tab.ID]
	a2.terminals["t2"] = idle("t2", 3, nil)
	a2.lookedAt()
	time.Sleep(30 * time.Millisecond)
	if be2.calls() != 0 || a2.localSeen["t2"] {
		t.Fatal("a hidden pane was looked at")
	}
}

func TestLookedAtNeedsTheFocusedWindow(t *testing.T) {
	a, be := twoPaneApp(t)
	a.focused = false
	a.terminals["t1"] = idle("t1", 1, nil)
	a.lookedAt()
	time.Sleep(30 * time.Millisecond)
	if be.calls() != 0 {
		t.Fatal("unfocused window wrote")
	}
}

func TestLookedAtRetriesAFailedWrite(t *testing.T) {
	a, be := twoPaneApp(t)
	be.setErr(errors.New("down"))
	a.terminals["t1"] = idle("t1", 4, map[string]string{"herdr.seen": "1"})
	a.lookedAt()
	pumpUntil(t, a, func() bool {
		return be.calls() == 1 && a.seenBusy["t1"].After(time.Now().Add(time.Second)) == false && len(a.seenBusy) == 1
	})
	if _, ok := a.seenTurns["t1"]; ok {
		t.Fatal("a failed write counted")
	}
	be.setErr(nil)
	a.seenBusy["t1"] = time.Now().Add(-time.Second) // the retry time came
	a.lookedAt()
	pumpUntil(t, a, func() bool { return be.calls() == 2 && len(a.seenBusy) == 0 })
	if a.seenTurns["t1"] != 4 {
		t.Fatalf("seen %v", a.seenTurns)
	}
}

func TestRunPostedSurvivesAPanic(t *testing.T) {
	a := newApp(t.TempDir(), "x")
	ran := false
	a.post(func() { panic("boom") })
	a.post(func() { ran = true })
	a.runPosted()
	if !ran || len(a.toasts) != 1 {
		t.Fatalf("ran=%v toasts=%v", ran, a.toasts)
	}
}

func TestLoadKeysKeepsValidBindings(t *testing.T) {
	home := t.TempDir()
	doc := `{"global":{"ctrl-shift-y":"tab.new","nonsense-???":"tab.close"}}`
	if err := os.WriteFile(filepath.Join(home, "keys.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newApp(home, "x")
	if len(a.toasts) == 0 {
		t.Fatal("the error was not shown")
	}
	found := false
	for _, act := range a.keymap.Sections["global"] {
		found = found || act.Command == "tab.new"
	}
	if !found {
		t.Fatal("the valid binding was dropped")
	}
}

func TestShortcutAllowed(t *testing.T) {
	a := newApp(t.TempDir(), "x")
	if !a.shortcutAllowed("tab.close") {
		t.Fatal("blocked with nothing open")
	}
	a.palette.open = true
	if a.shortcutAllowed("tab.close") || !a.shortcutAllowed("app.palette") || !a.shortcutAllowed("mod.open") {
		t.Fatal("palette rules")
	}
	a.palette.open = false
	for _, set := range []func(){
		func() { a.prompt = &promptState{} },
		func() { a.confirm = &confirmState{} },
		func() { a.settings = true },
	} {
		a.prompt, a.confirm, a.settings = nil, nil, false
		set()
		if a.shortcutAllowed("tab.close") || a.shortcutAllowed("app.palette") {
			t.Fatal("a modal lets shortcuts through")
		}
	}
}

func TestFixedSizeNodesAreNoWeights(t *testing.T) {
	px, w := 268.0, 2.0
	fixed := &layout.Node{Size: &layout.Size{Px: &px}}
	if v, ok := fixedPx(fixed); !ok || v != 268 || weight(fixed) != 0 {
		t.Fatal("px only is fixed")
	}
	both := &layout.Node{Size: &layout.Size{Px: &px, Weight: &w}}
	if _, ok := fixedPx(both); ok || weight(both) != 2 {
		t.Fatal("a weight wins")
	}
	if weight(&layout.Node{}) != 1 {
		t.Fatal("unsized is 1")
	}
	setWeight(both, 3)
	if both.Size.Px == nil || *both.Size.Weight != 3 {
		t.Fatal("setWeight erased px")
	}
}

func TestCloseTerminalPanesRemovesABlockRootTab(t *testing.T) {
	a := newApp(t.TempDir(), "x")
	a.doc = &layout.Document{}
	s := a.doc.AddSpace("s", "/tmp")
	tab, _ := s.AddTab("t", "t1")
	s.AddTab("u", "t2")
	tab.Root = &layout.Node{ID: "p", Type: layout.TypeBlock, Content: &layout.Content{Terminal: "t1"}}
	a.closeTerminalPanes("t1")
	if len(s.Tabs) != 1 || s.Tabs[0].Panes()[0].Content.Terminal != "t2" {
		t.Fatalf("tabs %v", s.Tabs)
	}
}

func TestZoomStepsSavesOnceLater(t *testing.T) {
	a := newApp(t.TempDir(), "x")
	for i := 0; i < 5; i++ {
		a.zoomSteps(terminal.Zoom{Steps: 2})
	}
	if a.cfg.FontSize != 23 {
		t.Fatalf("size %v", a.cfg.FontSize)
	}
	path := filepath.Join(a.home, "settings.json")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("written at once")
	}
	pumpUntil(t, a, func() bool { _, err := os.Stat(path); return err == nil })
	if a.saveTimer != nil {
		t.Fatal("timer left")
	}
}
