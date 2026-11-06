package resume

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// piSession writes a pi session made at made, last written at mod.
func piSession(t *testing.T, e Env, cwd, id string, made, mod time.Time) string {
	path := filepath.Join(piSessions(e, cwd), strings.ReplaceAll(iso(made), ":", "-")+"_"+id+".jsonl")
	write(t, path, `{"type":"session","version":3,"id":"`+id+`","timestamp":"`+iso(made)+`","cwd":"`+cwd+`"}`+"\n", mod)
	return path
}

func testEnv(t *testing.T) Env {
	return Env{Home: t.TempDir(), Getenv: func(string) string { return "" }}
}

func TestPiSessionsDir(t *testing.T) {
	e := Env{Home: "/h"}
	if got := piSessions(e, "/home/pn/shiori"); got != "/h/.pi/agent/sessions/--home-pn-shiori--" {
		t.Fatal(got)
	}
}

func TestTwoAgentsInOneFolderTakeTheirOwn(t *testing.T) {
	e := testEnv(t)
	t0 := time.Now().Add(-time.Hour)
	a := piSession(t, e, "/w", "aaa", t0.Add(time.Second), t0.Add(50*time.Minute))
	b := piSession(t, e, "/w", "bbb", t0.Add(10*time.Minute), t0.Add(40*time.Minute))
	piSession(t, e, "/w", "old", t0.Add(-time.Hour), t0.Add(-time.Minute)) // before both
	got := Find(e, []Proc{
		{Key: "pi", Pid: 1, Cwd: "/w", StartMs: t0.UnixMilli()},
		{Key: "pi", Pid: 2, Cwd: "/w", StartMs: t0.Add(10 * time.Minute).UnixMilli()},
	})
	want := []Session{{ID: "aaa", Path: a}, {ID: "bbb", Path: b}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestANewConversationIsTheLatest(t *testing.T) {
	e := testEnv(t)
	t0 := time.Now().Add(-time.Hour)
	piSession(t, e, "/w", "first", t0, t0.Add(10*time.Minute))
	next := piSession(t, e, "/w", "next", t0.Add(20*time.Minute), t0.Add(30*time.Minute))
	got := Find(e, []Proc{{Key: "pi", Pid: 1, Cwd: "/w", StartMs: t0.UnixMilli()}})
	if got[0].ID != "next" || got[0].Path != next {
		t.Fatalf("%+v", got)
	}
}

func TestAConversationGoneBackTo(t *testing.T) {
	e := testEnv(t)
	t0 := time.Now().Add(-time.Hour)
	old := piSession(t, e, "/w", "old", t0.Add(-24*time.Hour), t0.Add(5*time.Minute))
	// Another agent, started later, made its own: it is not taken.
	mine := piSession(t, e, "/w", "mine", t0.Add(10*time.Minute), t0.Add(20*time.Minute))
	got := Find(e, []Proc{
		{Key: "pi", Pid: 1, Cwd: "/w", StartMs: t0.UnixMilli()},
		{Key: "pi", Pid: 2, Cwd: "/w", StartMs: t0.Add(10 * time.Minute).UnixMilli()},
	})
	if got[0].Path != old || got[1].Path != mine {
		t.Fatalf("%+v", got)
	}
}

func TestNothingToTell(t *testing.T) {
	e := testEnv(t)
	t0 := time.Now().Add(-time.Hour)
	piSession(t, e, "/w", "old", t0.Add(-24*time.Hour), t0.Add(-time.Minute))
	got := Find(e, []Proc{{Key: "pi", Pid: 1, Cwd: "/w", StartMs: t0.UnixMilli()}, {Key: "amp", Pid: 2, Cwd: "/w", StartMs: t0.UnixMilli()}})
	if got[0].Known() || got[1].Known() {
		t.Fatalf("%+v", got)
	}
}

func TestCommandLineNames(t *testing.T) {
	e := testEnv(t)
	got := Find(e, []Proc{
		{Key: "pi", Argv: []string{"pi", "--session", "abc123"}, Cwd: "/w"},
		{Key: "codex", Argv: []string{"codex", "resume", "0199-aa"}, Cwd: "/w"},
		{Key: "claude", Argv: []string{"claude", "--resume", "1f2e3d4c-aaaa"}, Cwd: "/w"},
		{Key: "claude", Argv: []string{"claude", "--resume", "fix the bug"}, Cwd: "/w"},
	})
	want := []Session{{ID: "abc123"}, {ID: "0199-aa"}, {ID: "1f2e3d4c-aaaa"}, {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestClaudeFilePerProcess(t *testing.T) {
	e := testEnv(t)
	t0 := time.Now()
	tr := filepath.Join(e.Home, ".claude", "projects", "-w-my-app", "5a5a-1111.jsonl")
	write(t, tr, `{"type":"user","timestamp":"`+iso(t0)+`"}`+"\n", t0)
	write(t, filepath.Join(e.Home, ".claude", "sessions", "42.json"), `{"pid":42,"sessionId":"5a5a-1111","cwd":"/w/my.app"}`, t0)
	got := Find(e, []Proc{{Key: "claude", Pid: 42, Cwd: "/w/my.app", StartMs: t0.Add(-time.Hour).UnixMilli()}})
	if got[0] != (Session{ID: "5a5a-1111", Path: tr}) {
		t.Fatalf("%+v", got)
	}
}

func TestClaudeByStart(t *testing.T) {
	e := testEnv(t)
	t0 := time.Now().Add(-time.Hour)
	tr := filepath.Join(e.Home, ".claude", "projects", "-w", "77aa.jsonl")
	write(t, tr, `{"type":"summary"}`+"\n"+`{"type":"user","timestamp":"`+iso(t0.Add(time.Minute))+`"}`+"\n", t0.Add(2*time.Minute))
	got := Find(e, []Proc{{Key: "claude", Pid: 42, Cwd: "/w", StartMs: t0.UnixMilli()}})
	if got[0] != (Session{ID: "77aa", Path: tr}) {
		t.Fatalf("%+v", got)
	}
}

func TestCodexByStart(t *testing.T) {
	e := testEnv(t)
	t0 := time.Now().Add(-time.Hour)
	day := filepath.Join(e.Home, ".codex", "sessions", t0.Format("2006"), t0.Format("01"), t0.Format("02"))
	mine := filepath.Join(day, "rollout-x-0199.jsonl")
	write(t, mine, `{"timestamp":"`+iso(t0)+`","type":"session_meta","payload":{"id":"0199","timestamp":"`+iso(t0.Add(time.Second))+`","cwd":"/w"}}`+"\n", t0.Add(time.Minute))
	write(t, filepath.Join(day, "rollout-y-0200.jsonl"), `{"type":"session_meta","payload":{"id":"0200","timestamp":"`+iso(t0.Add(time.Second))+`","cwd":"/elsewhere"}}`+"\n", t0.Add(2*time.Minute))
	got := Find(e, []Proc{{Key: "codex", Pid: 9, Cwd: "/w", StartMs: t0.UnixMilli()}})
	if got[0] != (Session{ID: "0199", Path: mine}) {
		t.Fatalf("%+v", got)
	}
}

func TestSpecArgv(t *testing.T) {
	dir := t.TempDir()
	there := filepath.Join(dir, "s.jsonl")
	write(t, there, "{}", time.Now())
	pi := Spec{ID: []string{"pi", "--session", "{path}"}, Last: []string{"pi", "-c"}}
	codex := Spec{ID: []string{"codex", "resume", "{id}"}, Last: []string{"codex", "resume", "--last"}}
	for _, c := range []struct {
		spec Spec
		s    Session
		want []string
	}{
		{pi, Session{ID: "a", Path: there}, []string{"pi", "--session", there}},
		{pi, Session{ID: "a"}, []string{"pi", "--session", "a"}},
		{pi, Session{ID: "a", Path: filepath.Join(dir, "gone.jsonl")}, []string{"pi", "-c"}},
		{pi, Session{}, []string{"pi", "-c"}},
		{codex, Session{Path: there}, []string{"codex", "resume", "--last"}},
		{Spec{Last: []string{"opencode", "--continue"}}, Session{ID: "x"}, []string{"opencode", "--continue"}},
		{Spec{}, Session{ID: "x"}, nil},
	} {
		if got := c.spec.Argv(c.s); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v on %+v: %q, want %q", c.spec, c.s, got, c.want)
		}
	}
}

func TestStartOfThisProcess(t *testing.T) {
	ms := System().StartMs(os.Getpid())
	if ms <= 0 || time.Since(time.UnixMilli(ms)) > time.Hour || time.Until(time.UnixMilli(ms)) > 2*time.Second {
		t.Fatalf("start %v", time.UnixMilli(ms))
	}
}

func TestAFileHeldOpen(t *testing.T) {
	e := testEnv(t)
	e.Proc = t.TempDir()
	path := filepath.Join(e.Home, ".codex", "sessions", "2020", "01", "01", "rollout-x-0300.jsonl")
	write(t, path, `{"type":"session_meta","payload":{"id":"0300","cwd":"/w"}}`+"\n", time.Now())
	fd := filepath.Join(e.Proc, "9", "fd")
	if err := os.MkdirAll(fd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(fd, "7")); err != nil {
		t.Fatal(err)
	}
	// Made long before the start: only the open file tells.
	got := Find(e, []Proc{{Key: "codex", Pid: 9, Cwd: "/w", StartMs: time.Now().UnixMilli()}})
	if got[0] != (Session{ID: "0300", Path: path}) {
		t.Fatalf("%+v", got)
	}
}
