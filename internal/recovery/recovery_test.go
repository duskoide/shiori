package recovery

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSameCore(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	a := At(100, 60_000, now)
	for _, tc := range []struct {
		name string
		b    Core
		want bool
	}{
		{"the same call", a, true},
		{"a later call", At(100, 70_250, now.Add(10*time.Second)), true},
		{"another pid", At(101, 60_000, now), false},
		{"the same pid after a reboot", At(100, 1_000, now.Add(time.Hour)), false},
	} {
		if got := a.Same(tc.b); got != tc.want {
			t.Errorf("%s: Same = %v", tc.name, got)
		}
	}
	if (Core{}).Known() || At(0, 5, now).Known() || !a.Known() {
		t.Fatal("Known")
	}
}

func TestLost(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	old, cur := At(100, 60_000, now.Add(-time.Hour)), At(200, 1_000, now)
	s := &Snapshot{Panes: map[string]Pane{
		"p1": {Terminal: "t1", CreatedMs: 5, Cwd: "/a", Core: old},
		"p2": {Terminal: "t2", CreatedMs: 6, Cwd: "/b", Core: old},
		"p3": {Terminal: "t3", CreatedMs: 7, Cwd: "/c", Core: cur},
	}}
	if got, ok := s.Lost("p1", "t1", cur, false, 0); !ok || got.Cwd != "/a" {
		t.Fatalf("a terminal the new core does not have: %v %+v", ok, got)
	}
	if _, ok := s.Lost("p1", "t1", cur, true, 99); !ok {
		t.Fatal("an address the new core gave to another terminal")
	}
	if _, ok := s.Lost("p2", "t2", cur, true, 6); ok {
		t.Fatal("a terminal that outlived its core")
	}
	if _, ok := s.Lost("p3", "t3", cur, false, 0); ok {
		t.Fatal("a terminal of the current core is not lost: it ended")
	}
	if _, ok := s.Lost("p9", "t9", cur, false, 0); ok {
		t.Fatal("a pane the snapshot does not know")
	}
	if _, ok := s.Lost("p1", "t5", cur, false, 0); ok {
		t.Fatal("a pane that shows another terminal since")
	}
	if _, ok := s.Lost("p1", "t1", Core{}, false, 0); ok {
		t.Fatal("lost without knowing the current core")
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "home", FileName(""))
	s, err := Load(path)
	if err != nil || s.Version != Version || len(s.Panes) != 0 {
		t.Fatalf("missing file: %+v %v", s, err)
	}
	s.Core = Core{Pid: 3, StartMs: 4}
	s.Layout = json.RawMessage(`{"spaces":[]}`)
	s.Panes = map[string]Pane{"p1": {Terminal: "t1", CreatedMs: 1, Cwd: "/x", Core: s.Core}}
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, b); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var layout bytes.Buffer
	json.Compact(&layout, got.Layout)
	if got.Core != s.Core || got.Panes["p1"].Cwd != "/x" || layout.String() != `{"spaces":[]}` {
		t.Fatalf("%+v", got)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestLoadRefusesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{"), 0o600)
	if s, err := Load(bad); err == nil || s == nil {
		t.Fatal("a broken file reads")
	}
	newer := filepath.Join(dir, "newer.json")
	os.WriteFile(newer, []byte(`{"version":99}`), 0o600)
	if _, err := Load(newer); err == nil {
		t.Fatal("a newer version reads")
	}
}

func TestFileName(t *testing.T) {
	if FileName("") != "session.json" || FileName("default") != "session.json" || FileName("work") != "session-work.json" {
		t.Fatal(FileName("work"))
	}
}
