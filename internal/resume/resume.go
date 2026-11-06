// Package resume finds the conversation a coding agent running in a
// terminal is in, so that a terminal lost with its core (a crash, a kill, a
// reboot) can start the agent again on that conversation instead of a new
// one.
//
// Agents do not say which session they are in, so it is worked out from
// their session files: the one a process names (Claude Code's file per
// process, a file it holds open), else the session made in its folder
// since it started that was written to last, else the one its command line
// named. Two agents of the same kind in the same folder are told apart by
// their starts: the one started last takes the sessions made since first,
// and a session one went back to comes after those made anew.
// What cannot be told is no session: the agent then continues the latest
// in its folder.
package resume

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Proc is an agent's process, as a terminal's foreground.
type Proc struct {
	// Key is the agent's key in programs.json: "claude", "codex", "pi".
	Key     string
	Pid     int
	Argv    []string
	Cwd     string
	StartMs int64 // when the process started, 0 if not known
}

// Session is an agent's conversation.
type Session struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"` // its file, if known
}

// Known reports whether s names a session.
func (s Session) Known() bool { return s.ID != "" || s.Path != "" }

// Env is where the agents keep their files: a home and environment.
type Env struct {
	Home   string
	Getenv func(string) string
	Proc   string // Linux's /proc, "" for none
}

// System is this machine's.
func System() Env {
	home, _ := os.UserHomeDir()
	e := Env{Home: home, Getenv: os.Getenv}
	if runtime.GOOS == "linux" {
		e.Proc = "/proc"
	}
	return e
}

func (e Env) env(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// slack is how long before a process's start a session made for it may
// say it was made: clocks and rounding.
const slack = 2 * time.Second

// candidate is a session file that may be a process's.
type candidate struct {
	Session
	createdMs, modMs int64
}

// agent is how one kind of agent keeps its sessions.
type agent struct {
	// root is the folder all its session files are under.
	root func(e Env) string
	// own is the session a process itself names, if any.
	own func(e Env, p Proc) (Session, bool)
	// made lists the sessions of a folder written to since a time.
	made func(e Env, cwd string, sinceMs int64) []candidate
	// named is the session a command line names, if any.
	named func(e Env, p Proc) (Session, bool)
	// file is the session a file under root is.
	file func(path string) (Session, bool)
}

var agents = map[string]agent{
	"pi":     piAgent,
	"claude": claudeAgent,
	"codex":  codexAgent,
}

// Supported reports whether sessions of key's agent can be found.
func Supported(key string) bool { _, ok := agents[key]; return ok }

// Find finds the session of each process, in procs' order; a zero
// Session where none can be told.
func Find(e Env, procs []Proc) []Session {
	out := make([]Session, len(procs))
	taken := map[string]bool{}
	take := func(i int, s Session) {
		out[i] = s
		taken[s.key()] = true
	}
	// What a process names is sure.
	for i, p := range procs {
		a, ok := agents[p.Key]
		if !ok {
			continue
		}
		s, ok := Session{}, false
		if a.own != nil {
			s, ok = a.own(e, p)
		}
		if !ok && a.root != nil && a.file != nil {
			s, ok = openFile(e, p.Pid, a.root(e), a.file)
		}
		if ok {
			take(i, s)
		}
	}
	// The rest by their sessions written to since they started, kind and
	// folder apart, the latest started first: first those made since
	// (the agent's own, or one it made anew), then any (one it went back
	// to).
	order := make([]int, 0, len(procs))
	for i := range procs {
		order = append(order, i)
	}
	sort.SliceStable(order, func(x, y int) bool { return procs[order[x]].StartMs > procs[order[y]].StartMs })
	made := map[string][]candidate{}
	list := func(p Proc) []candidate {
		k := p.Key + "\x00" + p.Cwd
		cs, seen := made[k]
		if !seen {
			// Since the earliest start of the group: every one's are in it.
			since := p.StartMs
			for _, q := range procs {
				if q.Key == p.Key && q.Cwd == p.Cwd && q.StartMs > 0 && q.StartMs < since {
					since = q.StartMs
				}
			}
			cs = agents[p.Key].made(e, p.Cwd, since-slack.Milliseconds())
			made[k] = cs
		}
		return cs
	}
	for _, madeSince := range []bool{true, false} {
		for _, i := range order {
			p := procs[i]
			a, ok := agents[p.Key]
			if !ok || out[i].Known() || p.StartMs <= 0 || p.Cwd == "" || a.made == nil {
				continue
			}
			cs := list(p)
			best := -1
			for j, c := range cs {
				from := c.modMs
				if madeSince {
					from = c.createdMs
				}
				if taken[c.key()] || from < p.StartMs-slack.Milliseconds() {
					continue
				}
				if best < 0 || c.modMs > cs[best].modMs {
					best = j
				}
			}
			if best >= 0 {
				take(i, cs[best].Session)
			}
		}
	}
	// Last, what its command line named when it started.
	for i, p := range procs {
		a, ok := agents[p.Key]
		if !ok || out[i].Known() || a.named == nil {
			continue
		}
		if s, ok := a.named(e, p); ok && !taken[s.key()] {
			take(i, s)
		}
	}
	return out
}

func (s Session) key() string {
	if s.Path != "" {
		return s.Path
	}
	return "id:" + s.ID
}

// Spec is how an agent starts again: on a session, its argv with {id} and
// {path} in it ({path} is the id when the file is not known), and on the
// latest session in its folder.
type Spec struct {
	ID   []string `json:"id,omitempty"`
	Last []string `json:"last,omitempty"`
}

// Argv is what starts the agent of spec on s: on s when it is known and
// its file, if known, is still there; else on the latest session, if spec
// has a way to; else nil.
func (spec Spec) Argv(s Session) []string {
	if out, ok := spec.on(s); ok {
		return out
	}
	if len(spec.Last) > 0 {
		return append([]string(nil), spec.Last...)
	}
	return nil
}

// on is spec's argv on s, if s can be started on.
func (spec Spec) on(s Session) ([]string, bool) {
	if !s.Known() || len(spec.ID) == 0 {
		return nil, false
	}
	if s.Path != "" {
		if _, err := os.Stat(s.Path); err != nil {
			return nil, false // gone since
		}
	}
	path := s.Path
	if path == "" {
		path = s.ID
	}
	r := strings.NewReplacer("{id}", s.ID, "{path}", path)
	out := make([]string, len(spec.ID))
	for i, w := range spec.ID {
		if strings.Contains(w, "{id}") && s.ID == "" {
			return nil, false
		}
		out[i] = r.Replace(w)
	}
	return out, true
}

// openFile is a session file under root that the process holds open
// (Linux only).
func openFile(e Env, pid int, root string, file func(string) (Session, bool)) (Session, bool) {
	if e.Proc == "" || pid <= 0 || root == "" {
		return Session{}, false
	}
	dir := filepath.Join(e.Proc, itoa(pid), "fd")
	fds, err := os.ReadDir(dir)
	if err != nil {
		return Session{}, false
	}
	root = filepath.Clean(root) + string(filepath.Separator)
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(dir, fd.Name()))
		if err != nil || !strings.HasPrefix(target, root) || !strings.HasSuffix(target, ".jsonl") {
			continue
		}
		if s, ok := file(target); ok {
			return s, true
		}
	}
	return Session{}, false
}

// firstLine is the first line of a file, up to 64 KiB.
func firstLine(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	if !sc.Scan() {
		return nil, false
	}
	return sc.Bytes(), true
}

// jsonlSince lists the .jsonl files of dir written to since sinceMs.
func jsonlSince(dir string, sinceMs int64) []candidate {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []candidate
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		fi, err := ent.Info()
		if err != nil || fi.ModTime().UnixMilli() < sinceMs {
			continue
		}
		out = append(out, candidate{Session: Session{Path: filepath.Join(dir, ent.Name())}, modMs: fi.ModTime().UnixMilli()})
	}
	return out
}

// isoMs is an ISO 8601 time in ms, 0 if it is not one.
func isoMs(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// flagValue is the value of a flag on a command line: "--x v" or "--x=v".
func flagValue(argv []string, names ...string) (string, bool) {
	for i, w := range argv {
		for _, n := range names {
			if w == n && i+1 < len(argv) {
				return argv[i+1], true
			}
			if v, ok := strings.CutPrefix(w, n+"="); ok && strings.HasPrefix(n, "--") {
				return v, true
			}
		}
	}
	return "", false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
