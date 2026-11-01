package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
)

// fakeBackend is a backend that records what the app asks of it.
type fakeBackend struct {
	mu     sync.Mutex
	runs   []core.RunParams
	labels map[string]map[string]string
	keeps  map[string]bool
	err    error
}

func (f *fakeBackend) Run(_ context.Context, p core.RunParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, p)
	return "t" + string(rune('0'+len(f.runs))), f.err
}
func (f *fakeBackend) Kill(context.Context, string) error { return nil }
func (f *fakeBackend) Label(_ context.Context, addr string, l map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.labels == nil {
		f.labels = map[string]map[string]string{}
	}
	f.labels[addr] = l
	return f.err
}
func (f *fakeBackend) Keep(_ context.Context, addr string, keep bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keeps == nil {
		f.keeps = map[string]bool{}
	}
	f.keeps[addr] = keep
	return f.err
}
func (f *fakeBackend) Get(context.Context, string) (core.Terminal, error) {
	return core.Terminal{}, errors.New("no")
}
func (f *fakeBackend) DocSet(context.Context, string, string, uint64, json.RawMessage) (uint64, error) {
	return 1, nil
}
func (f *fakeBackend) DocGet(context.Context, string, string) (json.RawMessage, uint64, error) {
	return nil, 0, errors.New("no")
}

// testApp is an app with a loaded, empty document and a fake backend.
func testApp(t *testing.T) (*App, *fakeBackend) {
	t.Helper()
	a := newApp(t.TempDir(), "x")
	f := &fakeBackend{}
	a.core = f
	a.loaded = true
	a.doc = &layout.Document{}
	return a, f
}

// drain waits for the goroutines of a command and runs what they posted.
func drain(t *testing.T, a *App, done func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		a.runPosted()
		if done() {
			return
		}
	}
	t.Fatal("timed out")
}

func TestArgvOf(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want []string
		bad  bool
	}{
		{in: nil, want: nil},
		{in: "claude --x", want: []string{"claude", "--x"}},
		{in: `["claude","--x y"]`, want: []string{"claude", "--x y"}},
		{in: []any{"a", "b"}, want: []string{"a", "b"}},
		{in: []any{"a", 1.0}, bad: true},
		{in: `["a",`, bad: true},
	} {
		got, err := argvOf(args{"argv": tc.in})
		if (err != nil) != tc.bad || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("argvOf(%v) = %v, %v", tc.in, got, err)
		}
	}
}

func TestLaunchArgv(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	if got, _ := launchArgv(args{"task": "x"}); got != nil {
		t.Errorf("no argv: %v", got)
	}
	got, err := launchArgv(args{"argv": `["claude"]`, "task": " fix it "})
	if err != nil || len(got) < 6 || got[0] != "/bin/zsh" || got[len(got)-2] != "claude" || got[len(got)-1] != "fix it" {
		t.Errorf("launchArgv = %v, %v", got, err)
	}
}

func TestUnsupportedAndUnknown(t *testing.T) {
	a, _ := testApp(t)
	for _, n := range []string{"terminal.peek", "terminal.show", "terminal.right_click", "tab.settle", "tab.unsettle", "overlay.close", "layout.switch", "layout.resize"} {
		if _, err := a.do(n, nil); err == nil || !strings.Contains(err.Error(), n+" is not supported by this app") {
			t.Errorf("%s: %v", n, err)
		}
	}
	if _, err := a.do("nope.nope", nil); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown: %v", err)
	}
}

func TestSpaceNewArgs(t *testing.T) {
	a, f := testApp(t)
	if _, err := a.do("space.new", args{"machine": "mac"}); err == nil || !strings.Contains(err.Error(), "machine") {
		t.Errorf("machine: %v", err)
	}
	if _, err := a.do("space.new", args{"machine": "local"}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, func() bool { return len(a.doc.Spaces) == 1 })
	if _, err := a.do("space.new", args{"name": "n", "tab": "first", "folder": "/tmp", "argv": `["sh"]`, "task": "hi"}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, func() bool { return len(a.doc.Spaces) == 2 })
	if got := a.doc.Spaces[1].Tabs[0].Name; got != "first" {
		t.Errorf("tab name = %q", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runs[1].Cwd != "/tmp" || f.runs[1].Argv[len(f.runs[1].Argv)-1] != "hi" {
		t.Errorf("run = %+v", f.runs[1])
	}
}

func TestModsRejected(t *testing.T) {
	a, _ := testApp(t)
	for _, c := range []string{"tab.new", "pane.split"} {
		v := args{"mod": "board"}
		if c == "pane.split" {
			v["direction"] = "right"
		}
		if _, err := a.do(c, v); err == nil || !strings.Contains(err.Error(), "mods are not supported") {
			t.Errorf("%s: %v", c, err)
		}
	}
}

func TestMoveIsOneBased(t *testing.T) {
	a, _ := testApp(t)
	for _, n := range []string{"a", "b", "c"} {
		s := a.doc.AddSpace(n, "/")
		s.AddTab("t"+n, "x"+n)
	}
	if _, err := a.do("space.move", args{"id": a.doc.Spaces[2].ID, "index": 1}); err != nil {
		t.Fatal(err)
	}
	if a.doc.Spaces[0].Name != "c" {
		t.Errorf("space.move 1: %v", a.doc.Spaces[0].Name)
	}
	if _, err := a.do("space.move", args{"id": a.doc.Spaces[0].ID, "index": 0}); err == nil {
		t.Error("index 0 should fail")
	}
	s := a.doc.Spaces[0]
	s.AddTab("u", "y")
	s.AddTab("v", "z")
	if _, err := a.do("tab.move", args{"id": s.Tabs[2].ID, "index": "2"}); err != nil {
		t.Fatal(err)
	}
	if s.Tabs[1].Name != "v" {
		t.Errorf("tab.move 2: %v", s.Tabs[1].Name)
	}
}

func TestPaneSplitTarget(t *testing.T) {
	a, f := testApp(t)
	if _, err := a.do("pane.split", args{"direction": "right", "pane": "nope"}); err == nil {
		t.Error("an unknown pane is an error")
	}
	// Nothing focused and no pane given: a new tab (and space).
	if _, err := a.do("pane.split", args{"direction": "down"}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, func() bool { return len(a.doc.Spaces) == 1 })
	f.mu.Lock()
	n := len(f.runs)
	f.mu.Unlock()
	if n != 1 {
		t.Errorf("runs = %d", n)
	}
}

func TestPaneCloseRemovesEmptyTab(t *testing.T) {
	a, _ := testApp(t)
	s := a.doc.AddSpace("s", "/")
	s.AddTab("one", "t1")
	t2, p := s.AddTab("two", "t2")
	t2.Root = p // a block root, as other apps write
	a.activeSpace, a.activeTab[s.ID] = s.ID, t2.ID
	if _, err := a.do("pane.close", args{"pane": p.ID}); err != nil {
		t.Fatal(err)
	}
	if len(s.Tabs) != 1 {
		t.Fatalf("tabs = %d", len(s.Tabs))
	}
	_, p1 := s.Tabs[0], s.Tabs[0].Panes()[0]
	if _, err := a.do("pane.close", args{"pane": p1.ID}); err != nil {
		t.Fatal(err)
	}
	if len(a.doc.Spaces) != 0 {
		t.Errorf("the empty space stays: %d", len(a.doc.Spaces))
	}
}

func TestThemeRejectsUnknown(t *testing.T) {
	a, _ := testApp(t)
	if _, err := a.do("app.theme", args{"name": "nope"}); err == nil {
		t.Error("unknown theme accepted")
	}
}

func TestTerminalKeepAndPark(t *testing.T) {
	a, f := testApp(t)
	s := a.doc.AddSpace("s", "/")
	tb, p := s.AddTab("one", "t1")
	a.activeSpace, a.activeTab[s.ID] = s.ID, tb.ID
	a.focus[tb.ID] = p.ID
	a.terminals["t9"] = core.Terminal{Address: "t9", State: "live"}
	if _, err := a.do("terminal.keep", args{"terminal": "m/t1"}); err == nil {
		t.Error("another machine accepted")
	}
	if _, err := a.do("terminal.keep", args{"terminal": "t8"}); err == nil {
		t.Error("unknown terminal accepted")
	}
	if _, err := a.do("terminal.keep", args{"terminal": "t9", "place": "right"}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, func() bool { return len(tb.Panes()) == 2 })
	f.mu.Lock()
	kept := f.keeps["t9"]
	f.mu.Unlock()
	if !kept {
		t.Error("terminal.keep did not keep")
	}
	if _, err := a.do("terminal.park", args{"terminal": "t9"}); err != nil {
		t.Fatal(err)
	}
	drain(t, a, func() bool { return len(tb.Panes()) == 1 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keeps["t9"] || f.labels["t9"]["herdr.space"] != "" {
		t.Errorf("park: keep %v labels %v", f.keeps["t9"], f.labels["t9"])
	}
}

func TestAnswerGivesUp(t *testing.T) {
	a, _ := testApp(t)
	// Nothing runs posted functions: the request must time out, and when the
	// window wakes up later the request must not run.
	old := answerTimeout
	answerTimeout = 20 * time.Millisecond
	defer func() { answerTimeout = old }()
	r := a.answer(request{Do: "space.new"})
	if m, ok := r.(map[string]string); !ok || m["error"] != "the window did not answer" {
		t.Fatalf("reply = %v", r)
	}
	a.runPosted()
	if len(a.spawning) != 0 || len(a.doc.Spaces) != 0 {
		t.Error("the cancelled request ran")
	}
}
