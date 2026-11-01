package main

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// TestIntegration drives the whole window against a real, isolated
// herdr-core session (HC_SESSION=herdr-mygo-it). It is skipped unless
// HERDR_MYGO_IT=1; HERDR_MYGO_SHOTS=<dir> saves screenshots.
func TestIntegration(t *testing.T) {
	if os.Getenv("HERDR_MYGO_IT") != "1" {
		t.Skip("set HERDR_MYGO_IT=1 to run against a real herdr-core")
	}
	// LD_LIBRARY_PATH may point the test binary at system GUI libraries;
	// the loader has read it already, and the core's shells must not see it.
	for _, k := range []string{"HC_SOCKET", "HC_TERMINAL_SOCKET", "HC_TERMINAL_ID", "HC_SCOPE", "LD_LIBRARY_PATH"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("HC_SESSION", "herdr-mygo-it")
	t.Cleanup(func() { exec.Command("hc", "server", "stop").Run() })
	if err := terminal.Load(); err != nil {
		t.Skip("no libghostty-vt:", err)
	}

	a := newApp(t.TempDir(), "herdr-mygo-it")
	tt := ui.NewTester(a.view, 1280, 800)
	a.start()
	t.Cleanup(a.shutdown)
	waitFor(t, tt, "the core", func() bool { return a.connected && a.loaded })
	shot(t, tt, "01-connected")

	// Start from an empty layout.
	for _, s := range append(a.doc.Spaces[:0:0], a.doc.Spaces...) {
		a.do("space.close", args{"id": s.ID, "confirmed": true})
	}
	tt.Frame()
	if _, err := a.do("space.new", args{"name": "it"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "a terminal", func() bool { p := a.pane(); return p != nil && a.terms[p.Content.Terminal] != nil })
	pt := a.terms[a.pane().Content.Terminal]
	waitFor(t, tt, "a prompt", func() bool { return strings.TrimSpace(pt.term.Text()) != "" })
	pt.term.Send([]byte("echo herdr-mygo-$((40+2))\r"))
	waitFor(t, tt, "the echo", func() bool { return strings.Contains(pt.term.Text(), "herdr-mygo-42") })
	shot(t, tt, "02-terminal")

	if _, err := a.do("pane.split", args{"direction": "right"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the split", func() bool { return len(a.tab().Panes()) == 2 })
	if _, err := a.do("pane.split", args{"direction": "down"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the second split", func() bool { return len(a.tab().Panes()) == 3 })
	if _, err := a.do("tab.new", args{"name": "second"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the new tab", func() bool { return len(a.space().Tabs) == 2 })
	waitFor(t, tt, "the saved layout", func() bool { return !a.saving && !a.dirty && a.version > 0 })
	a.do("tab.select", args{"index": 1})
	settle(tt)
	shot(t, tt, "03-splits")

	// The layout is in the core: a fresh reader sees it.
	value, _, err := a.client.DocGet(t.Context(), docName, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(value), "\"second\"") {
		t.Errorf("core document lacks the new tab: %s", value)
	}

	// Closing a pane ends its terminal and the layout follows.
	addr := a.pane().Content.Terminal
	a.do("pane.close", nil)
	waitFor(t, tt, "the pane closed", func() bool { return len(a.tab().Panes()) == 2 })
	waitFor(t, tt, "its terminal ended", func() bool { r, ok := a.terminals[addr]; return !ok || r.State == "finished" })

	// A program that exits closes its pane.
	before := len(a.tab().Panes())
	pt2 := a.terms[a.pane().Content.Terminal]
	waitFor(t, tt, "a prompt", func() bool { return strings.TrimSpace(pt2.term.Text()) != "" })
	pt2.term.Send([]byte("exit\r"))
	waitFor(t, tt, "the exited pane closed", func() bool { return a.tab() != nil && len(a.tab().Panes()) == before-1 })

	// Agents report their status (OSC 7501) and show in the sidebar.
	report := func(pt *paneTerm, state string) {
		pt.term.Send([]byte(`printf '\033]7501;state=` + state + `:app=claude\033\\'` + "\r"))
	}
	agentPt := a.terms[a.pane().Content.Terminal]
	report(agentPt, "working")
	waitFor(t, tt, "a working agent", func() bool { l := a.agents(); return len(l) == 1 && l[0].word.String() == "working" })
	report(agentPt, "blocked")
	waitFor(t, tt, "a blocked agent", func() bool { return tt.HasText("Needs you") })
	settle(tt)
	shot(t, tt, "05-agents")
	report(agentPt, "done")
	waitFor(t, tt, "the agent done", func() bool { l := a.agents(); return len(l) == 1 && l[0].word.String() != "blocked" })

	a.openPalette("split")
	settle(tt)
	shot(t, tt, "04-palette")
	a.palette.open = false
	tt.Frame()

	// The control socket answers ls.
	l, err := a.listen()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan error, 1)
	go func() { _, err := ask(a.home, request{Ls: true}); done <- err }()
	waitFor(t, tt, "the ls reply", func() bool {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return true
		default:
			return false
		}
	})
}

func waitFor(t *testing.T, tt *ui.Tester, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; texts %q", what, tt.Texts())
		}
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
}

func settle(tt *ui.Tester) {
	for range 10 {
		time.Sleep(30 * time.Millisecond)
		tt.Frame()
	}
}

func shot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("HERDR_MYGO_SHOTS")
	if dir == "" {
		return
	}
	os.MkdirAll(dir, 0o755)
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
