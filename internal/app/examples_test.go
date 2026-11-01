package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/mods"
)

// copyExample puts examples/mods/<id> in home's mods folder.
func copyExample(t *testing.T, home, id string) {
	t.Helper()
	src := filepath.Join("..", "..", "examples", "mods", id)
	dst := filepath.Join(home, "mods", id)
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dst, 0o755)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fi, _ := e.Info()
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, fi.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExampleModsLoad(t *testing.T) {
	for _, m := range mods.Load(filepath.Join("..", "..", "examples", "mods"), eventNames) {
		if m.Err != nil {
			t.Errorf("%s: %v", m.ID, m.Err)
		}
	}
}

func TestExampleOpenInEditor(t *testing.T) {
	a, _ := uxApp(t)
	copyExample(t, a.home, "open-in-editor")
	a.reloadMods()
	if err := a.setModEnabled("open-in-editor", true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL_GUI", "true") // an editor that opens nothing
	if _, err := runMod(t, a, "open-in-editor.open", nil); err != nil {
		t.Fatal(err)
	}
	if got := a.keymap.KeysFor("open-in-editor.open"); len(got) != 1 || got[0] != "Ctrl+Alt+E" {
		t.Fatalf("keys %v", got)
	}
}

func TestExampleAgentNotify(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin") // no notify-send: it writes to its log
	if p, _ := exec.LookPath("notify-send"); p != "" {
		t.Skip("notify-send would show a notification")
	}
	a, _ := uxApp(t)
	serveApp(t, a)
	copyExample(t, a.home, "agent-notify")
	a.reloadMods()
	if err := a.setModEnabled("agent-notify", true); err != nil {
		t.Fatal(err)
	}
	a.started = true
	t.Cleanup(func() { a.stopServices() })
	a.startServices()
	pumpUntilTrue(t, a, "the service", func() bool { return a.mods["agent-notify"].svc.state == svcRunning })
	a.emitEvents()
	a.focused = false
	a.emitEvents()
	a.terminals["a1"] = core.Terminal{Address: "a1", State: "live", Argv: []string{"claude"}, Status: &core.Status{State: "blocked", App: "claude"}}
	a.emitEvents()
	pumpUntilTrue(t, a, "the notification", func() bool {
		return strings.Contains(modLogTail(a.mods["agent-notify"], 5), "An agent needs you: claude")
	})
}
