package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
)

// uxApp is a window of three spaces: "api" with a tab of two panes (a
// working agent beside a shell) and a tab "logs" (an agent that needs the
// person), "web" with an idle agent and "docs" with a shell.
func uxApp(t *testing.T) (*App, *fakeBackend) {
	t.Helper()
	a, f := testApp(t)
	a.connected = true
	s := a.doc.AddSpace("api", "/src/api")
	tab, p1 := s.AddTab("", "a1")
	if _, err := tab.Split(p1.ID, layout.Right, "a2"); err != nil {
		t.Fatal(err)
	}
	s.AddTab("logs", "a3")
	a.doc.AddSpace("", "/src/web").AddTab("", "a4")
	a.doc.AddSpace("docs", "/tmp").AddTab("", "a5")
	now := time.Now().UnixMilli()
	a.terminals = map[string]core.Terminal{
		"a1": {Address: "a1", State: "live", Argv: []string{"claude"}, Cwd: "/src/api", Status: &core.Status{State: "working", App: "claude"}, StatusMs: now},
		"a2": {Address: "a2", State: "live", Argv: []string{"zsh"}, Cwd: "/src/api"},
		"a3": {Address: "a3", State: "live", Argv: []string{"codex"}, Cwd: "/src/api", Status: &core.Status{State: "blocked", App: "codex"}, StatusMs: now},
		"a4": {Address: "a4", State: "live", Argv: []string{"zsh"}, Cwd: "/src/web", Status: &core.Status{State: "idle", App: "claude"}, StatusMs: now},
		"a5": {Address: "a5", State: "live", Argv: []string{"zsh"}, Cwd: "/tmp"},
	}
	a.activeSpace = s.ID
	a.activeTab[s.ID] = tab.ID
	a.focus[tab.ID] = p1.ID
	a.fixView()
	return a, f
}

func uxTester(a *App) *ui.Tester {
	tt := ui.NewTester(a.view, 1280, 800)
	settle(tt)
	return tt
}

func mustClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	settle(tt)
}

func TestNewTabButtonOpensATerminalTab(t *testing.T) {
	a, f := uxApp(t)
	tt := uxTester(a)
	before := len(a.space().Tabs)
	mustClick(t, tt, "New tab")
	drain(t, a, func() bool { return len(a.space().Tabs) == before+1 })
	if len(f.runs) != 1 || f.runs[0].Cwd != "/src/api" {
		t.Fatalf("runs %+v", f.runs)
	}
	if a.tab() != a.space().Tabs[before] {
		t.Fatal("the new tab is not the active one")
	}
}

func TestNewMenuButtonOffersAgentsAndASpace(t *testing.T) {
	a, f := uxApp(t)
	tt := uxTester(a)
	mustClick(t, tt, "New…")
	if !tt.HasText("Terminal") || !slices.Contains(tt.Texts(), "Claude Code") {
		t.Fatalf("no menu: %q", tt.Texts())
	}
	before := len(a.space().Tabs)
	mustClick(t, tt, "Terminal")
	drain(t, a, func() bool { return len(a.space().Tabs) == before+1 })
	if len(f.runs) != 1 {
		t.Fatalf("runs %+v", f.runs)
	}
}

func TestPaletteShortcutClosesThePalette(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyP)
	settle(tt)
	if !a.palette.open {
		t.Fatal("the palette did not open")
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyP)
	settle(tt)
	if a.palette.open {
		t.Fatal("the key that opened the palette did not close it")
	}
}

func TestPaletteKeysGoRoundAndPage(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openPalette("")
	settle(tt)
	n := len(a.paletteItems())
	tt.Key(0, ui.KeyUp)
	settle(tt)
	if a.palette.sel != n-1 {
		t.Fatalf("Up from the first: %d of %d", a.palette.sel, n)
	}
	tt.Key(0, ui.KeyDown)
	settle(tt)
	if a.palette.sel != 0 {
		t.Fatalf("Down from the last: %d", a.palette.sel)
	}
	tt.Key(0, ui.KeyPageDown)
	settle(tt)
	if a.palette.sel != min(paletteRows, n-1) {
		t.Fatalf("Page Down: %d", a.palette.sel)
	}
	tt.Key(0, ui.KeyPageUp)
	settle(tt)
	if a.palette.sel != 0 {
		t.Fatalf("Page Up: %d", a.palette.sel)
	}
}

func TestSettingsHaveACloseButton(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	mustClick(t, tt, "Settings")
	if !a.settings {
		t.Fatal("settings did not open")
	}
	mustClick(t, tt, "Theme ink")
	if a.cfg.Theme != "ink" {
		t.Fatalf("theme %q", a.cfg.Theme)
	}
	mustClick(t, tt, "Close settings")
	if a.settings {
		t.Fatal("settings still open")
	}
}

func TestStatusBarCountsAndLeadsToTheBlockedAgent(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	if !tt.HasText("3 agents") || !tt.HasText("· 1 needs you") {
		t.Fatalf("texts %q", tt.Texts())
	}
	mustClick(t, tt, "· 1 needs you")
	if p := a.pane(); p == nil || p.Content.Terminal != "a3" {
		t.Fatalf("pane %+v", p)
	}
	// One agent is one.
	delete(a.terminals, "a1")
	delete(a.terminals, "a3")
	settle(tt)
	if !tt.HasText("1 agent") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestRenameDialogButtons(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	tab := a.tab()
	tt.Key(0, ui.KeyF2)
	settle(tt)
	if a.prompt == nil || !tt.HasText("Leave it empty to name the tab after its program.") {
		t.Fatalf("prompt %+v", a.prompt)
	}
	tt.Type("build")
	mustClick(t, tt, "Cancel")
	if a.prompt != nil || tab.Name != "" {
		t.Fatalf("cancel: %+v %q", a.prompt, tab.Name)
	}
	tt.Key(0, ui.KeyF2)
	settle(tt)
	tt.Type("build")
	mustClick(t, tt, "Save")
	if a.prompt != nil || tab.Name != "build" {
		t.Fatalf("save: %+v %q", a.prompt, tab.Name)
	}
}

func TestCloseTabButtonOnTheTabUnderThePointer(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	closeButtons := func() int {
		n := 0
		for _, s := range tt.Texts() {
			if s == "Close tab" {
				n++
			}
		}
		return n
	}
	if closeButtons() != 1 {
		t.Fatalf("%d close buttons without hover", closeButtons())
	}
	r, _ := tt.Find("Tab logs")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	settle(tt)
	if closeButtons() != 2 {
		t.Fatalf("%d close buttons hovering logs", closeButtons())
	}
}

func TestZoomShowsAndUndoes(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.do("pane.zoom", nil)
	settle(tt)
	if !tt.HasText("Zoomed · 1 of 2 panes") {
		t.Fatalf("texts %q", tt.Texts())
	}
	for _, s := range tt.Texts() {
		if strings.HasSuffix(s, " Z") {
			t.Fatalf("a label %q", s)
		}
	}
	mustClick(t, tt, "Unzoom")
	if len(a.zoomed) != 0 {
		t.Fatal("still zoomed")
	}
}

func TestSidebarEdgeIsEasyToCatch(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	// A few pixels left of the line still catch it.
	x, y := a.sidebarW-3, float32(300)
	tt.Press(x, y)
	tt.Move(x+40, y)
	tt.Release(x+40, y)
	settle(tt)
	if a.sidebarW != sidebarWidth+40 {
		t.Fatalf("width %v", a.sidebarW)
	}
}

func TestDisconnectedPanesSayWhy(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	if !tt.HasText("Waiting for herdr-core… The terminal keeps running there.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	b := newApp(t.TempDir(), "x")
	b.connErr = "herdr-core: no socket"
	tb := uxTester(b)
	if !tb.HasText("Cannot reach herdr-core") {
		t.Fatalf("texts %q", tb.Texts())
	}
}

func TestTwoPanesOfOneTerminal(t *testing.T) {
	a, _ := uxApp(t)
	// herdr-app may show a terminal twice; the agents list keys rows by pane.
	_, tab := a.doc.Tab(a.activeTab[a.activeSpace])
	tab.Panes()[1].Content.Terminal = "a1"
	tt := uxTester(a)
	if n := len(a.agents()); n != 4 {
		t.Fatalf("%d agents", n)
	}
	settle(tt)
}

func TestFontSizeKeepsToItsRange(t *testing.T) {
	a := newApp(t.TempDir(), "x")
	for range 50 {
		a.zoomBy(1)
	}
	if a.cfg.FontSize != maxFontSize {
		t.Fatalf("size %v", a.cfg.FontSize)
	}
	a.zoomBy(-1)
	if a.cfg.FontSize != maxFontSize-1 {
		t.Fatalf("one step down from the top: %v", a.cfg.FontSize)
	}
	for range 50 {
		a.zoomBy(-1)
	}
	if a.cfg.FontSize != minFontSize {
		t.Fatalf("size %v", a.cfg.FontSize)
	}
	a.flushSettings()
	if _, err := os.Stat(filepath.Join(a.home, "settings.json")); err != nil {
		t.Fatal(err)
	}
}

func TestEndsText(t *testing.T) {
	for _, tc := range []struct {
		terms, tabs int
		want        string
	}{
		{1, 1, "Its terminal ends."},
		{2, 1, "Its 2 terminals end."},
		{3, 2, "Its 3 terminals in 2 tabs end."},
	} {
		if got := endsText(tc.terms, tc.tabs); got != tc.want {
			t.Errorf("endsText(%d, %d) = %q", tc.terms, tc.tabs, got)
		}
	}
}
