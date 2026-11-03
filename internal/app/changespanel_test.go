package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// changesRepo is a git work tree with a commit, then a changed file and a
// new one.
func changesRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("a"), 0o644)
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "x")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("n"), 0o644)
	return dir
}

// frameUntil draws frames until cond holds.
func frameUntil(t *testing.T, tt *ui.Tester, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); !cond(); {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
		tt.Frame()
	}
}

func TestChangesPanel(t *testing.T) {
	a, f := uxApp(t)
	dir := changesRepo(t)
	a.space().Folder = dir
	tt := uxTester(a)
	t.Cleanup(a.stopChanges)
	if a.chg.w != nil {
		t.Fatal("a hidden panel watches")
	}
	if got := a.keysFor("layout.toggle:changes"); got != "Ctrl+B G" {
		t.Fatalf("key %q", got)
	}
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	tt.Key(0, ui.KeyG)
	frameUntil(t, tt, "the changes", func() bool { return tt.HasText("Change main.go") && tt.HasText("Change new.go") })
	b, _ := os.ReadFile(filepath.Join(a.home, "settings.json"))
	if !strings.Contains(string(b), `"changes": true`) {
		t.Fatalf("settings.json %s", b)
	}
	if !tt.HasText(tildePath(dir) + " · what differs from the last commit") {
		t.Fatalf("texts %q", tt.Texts())
	}

	// A click shows the diff in a new tab, in the folder.
	mustClick(t, tt, "Change main.go")
	drain(t, a, func() bool { return len(f.runs) == 1 })
	if r := f.runs[0]; r.Cwd != dir || !slices.Equal(r.Argv[len(r.Argv)-5:], []string{"git", "diff", "HEAD", "--", "main.go"}) {
		t.Fatalf("run %+v", r)
	}
	mustClick(t, tt, "Change new.go")
	drain(t, a, func() bool { return len(f.runs) == 2 })
	if r := f.runs[1]; !slices.Contains(r.Argv, "--no-index") {
		t.Fatalf("untracked run %+v", r)
	}

	// A file changed now shows, first.
	os.WriteFile(filepath.Join(dir, "fresh.go"), []byte("f"), 0o644)
	a.chg.w.Refresh()
	frameUntil(t, tt, "the new file", func() bool { return a.chg.have && len(a.chg.snap.Files) == 3 })
	if a.chg.snap.Files[0].Path != "fresh.go" || !tt.HasText("now") {
		t.Fatalf("files %+v", a.chg.snap.Files)
	}

	// The panel follows the active space, not the focused pane.
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "notes.txt"), []byte("x"), 0o644)
	a.doc.Spaces[1].Folder = other
	mustClick(t, tt, "Space web")
	frameUntil(t, tt, "the other folder", func() bool { return a.chg.have && a.chg.snap.Folder == other })
	if a.chg.snap.Git || !tt.HasText("Change notes.txt") || !tt.HasText(tildePath(other)+" · files modified in the last day") {
		t.Fatalf("texts %q", tt.Texts())
	}

	// Hidden, it stops watching.
	mustClick(t, tt, "Hide changes")
	if a.cfg.changesShown() || a.chg.w != nil || tt.HasText("Change notes.txt") {
		t.Fatal("still watching")
	}
}

func TestChangesPanelStates(t *testing.T) {
	a, _ := uxApp(t)
	a.space().Folder = filepath.Join(t.TempDir(), "gone")
	tt := uxTester(a)
	t.Cleanup(a.stopChanges)
	a.do("layout.toggle", args{"id": "changes"})
	frameUntil(t, tt, "the error", func() bool { return tt.HasText("Cannot look at this folder") })
	a.space().Folder = t.TempDir()
	frameUntil(t, tt, "no changes", func() bool { return tt.HasText("No file here was modified in the last day.") })
	if _, err := a.do("layout.toggle", args{"id": "dock"}); err == nil {
		t.Fatal("an unknown panel toggled")
	}
}

func TestChangesFromTheLayoutPage(t *testing.T) {
	a, _ := uxApp(t)
	a.space().Folder = t.TempDir()
	tt := uxTester(a)
	t.Cleanup(a.stopChanges)
	a.openSettings("layout")
	settle(tt)
	mustClick(t, tt, "Show the changes panel")
	if !a.cfg.changesShown() {
		t.Fatal("not shown")
	}
	mustClick(t, tt, "Changes panel shown")
	if a.cfg.changesShown() {
		t.Fatal("still shown")
	}
}
