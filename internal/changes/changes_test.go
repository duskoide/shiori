package changes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func age(t *testing.T, dir, name string, ago time.Duration) {
	t.Helper()
	when := time.Now().Add(-ago)
	if err := os.Chtimes(filepath.Join(dir, filepath.FromSlash(name)), when, when); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func kinds(s Snapshot) string {
	var out []string
	for _, f := range s.Files {
		k := f.Kind.Letter() + " " + f.Path
		if f.OldPath != "" {
			k += " <- " + f.OldPath
		}
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, "a.txt", "a")
	write(t, dir, "b/c.txt", "c")
	write(t, dir, "b/d.txt", "d")
	write(t, dir, "old.txt", "o")
	write(t, dir, ".gitignore", "*.log\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func TestGitScan(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, "a.txt", "a2")
	write(t, dir, "new.txt", "n")
	write(t, dir, "b/e.txt", "e")
	write(t, dir, "noise.log", "ignored")
	os.Remove(filepath.Join(dir, "b", "c.txt"))
	git(t, dir, "mv", "old.txt", "renamed.txt")
	age(t, dir, "a.txt", 3*time.Hour)
	age(t, dir, "new.txt", 2*time.Hour)
	age(t, dir, "b/e.txt", time.Hour)
	age(t, dir, "renamed.txt", 4*time.Hour)
	s := Scan(context.Background(), dir, nil, Options{})
	if s.Err != nil || !s.Git {
		t.Fatalf("%+v", s)
	}
	// The deleted file was missed just now: it comes first.
	want := "D b/c.txt, U b/e.txt, U new.txt, M a.txt, R renamed.txt <- old.txt"
	if got := kinds(s); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// A deleted file keeps the time it was first missed.
	again := Scan(context.Background(), dir, &s, Options{})
	if !again.Files[0].Time.Equal(s.Files[0].Time) {
		t.Fatal("the deletion's time moved")
	}
	// A subfolder lists its own files, relative to it.
	sub := Scan(context.Background(), filepath.Join(dir, "b"), nil, Options{})
	if got := kinds(sub); got != "D c.txt, U e.txt" {
		t.Fatalf("subfolder: %s", got)
	}
	// A clean tree has nothing.
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "second")
	if s := Scan(context.Background(), dir, nil, Options{}); len(s.Files) != 0 || s.Err != nil {
		t.Fatalf("clean: %s %v", kinds(s), s.Err)
	}
}

func TestWalkScan(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "fresh.txt", "x")
	write(t, dir, "src/fresh.go", "x")
	write(t, dir, "stale.txt", "x")
	write(t, dir, "node_modules/dep/index.js", "x")
	write(t, dir, ".cache/blob", "x")
	age(t, dir, "fresh.txt", time.Minute)
	age(t, dir, "src/fresh.go", 2*time.Minute)
	age(t, dir, "stale.txt", 48*time.Hour)
	s := Scan(context.Background(), dir, nil, Options{NoGit: true})
	if s.Git || s.Err != nil || kinds(s) != "M fresh.txt, M src/fresh.go" {
		t.Fatalf("%s %v", kinds(s), s.Err)
	}
	os.Remove(filepath.Join(dir, "fresh.txt"))
	again := Scan(context.Background(), dir, &s, Options{NoGit: true})
	if kinds(again) != "D fresh.txt, M src/fresh.go" {
		t.Fatalf("deleted: %s", kinds(again))
	}
	cut := Scan(context.Background(), dir, nil, Options{NoGit: true, MaxFiles: 1})
	if !cut.Truncated {
		t.Fatal("not truncated")
	}
	if s := Scan(context.Background(), filepath.Join(dir, "missing"), nil, Options{}); s.Err == nil {
		t.Fatal("a missing folder scanned")
	}
}

func TestWatcher(t *testing.T) {
	old := Interval
	Interval = 20 * time.Millisecond
	defer func() { Interval = old }()
	dir := t.TempDir()
	got := make(chan Snapshot, 10)
	w := Watch(dir, Options{NoGit: true}, func(s Snapshot) { got <- s })
	defer w.Stop()
	if s := <-got; len(s.Files) != 0 {
		t.Fatalf("first %s", kinds(s))
	}
	write(t, dir, "x.txt", "x")
	w.Refresh()
	select {
	case s := <-got:
		if kinds(s) != "M x.txt" {
			t.Fatalf("change %s", kinds(s))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change came")
	}
	select {
	case s := <-got:
		t.Fatalf("a scan without change came: %s", kinds(s))
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSince(t *testing.T) {
	now := time.Now()
	for d, want := range map[time.Duration]string{time.Second: "now", 30 * time.Second: "<1m", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 50 * time.Hour: "2d"} {
		if got := Since(now, now.Add(-d)); got != want {
			t.Errorf("%s: %s, want %s", d, got, want)
		}
	}
}
