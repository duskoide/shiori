package resume

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- Pi: <agent dir>/sessions/--<cwd>--/<time>_<id>.jsonl, a header
// {"type":"session","id","timestamp","cwd"} first.

func piDir(e Env) string {
	if d := e.env("PI_CODING_AGENT_DIR"); d != "" {
		return d
	}
	return filepath.Join(e.Home, ".pi", "agent")
}

// piSessions is the folder of a cwd's sessions, as pi names it.
func piSessions(e Env, cwd string) string {
	c := filepath.Clean(cwd)
	if strings.HasPrefix(c, "/") || strings.HasPrefix(c, `\`) {
		c = c[1:]
	}
	c = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(c)
	return filepath.Join(piDir(e), "sessions", "--"+c+"--")
}

type piHeader struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

func piHead(path string) (piHeader, bool) {
	line, ok := firstLine(path)
	var h piHeader
	if !ok || json.Unmarshal(line, &h) != nil || h.Type != "session" || h.ID == "" {
		return piHeader{}, false
	}
	return h, true
}

var piAgent = agent{
	root: func(e Env) string { return filepath.Join(piDir(e), "sessions") },
	file: func(path string) (Session, bool) {
		h, ok := piHead(path)
		return Session{ID: h.ID, Path: path}, ok
	},
	made: func(e Env, cwd string, sinceMs int64) []candidate {
		var out []candidate
		for _, c := range jsonlSince(piSessions(e, cwd), sinceMs) {
			h, ok := piHead(c.Path)
			if !ok || (h.Cwd != "" && filepath.Clean(h.Cwd) != filepath.Clean(cwd)) {
				continue
			}
			c.ID, c.createdMs = h.ID, isoMs(h.Timestamp)
			out = append(out, c)
		}
		return out
	},
	named: func(e Env, p Proc) (Session, bool) {
		if v, ok := flagValue(p.Argv, "--session"); ok && v != "" && !strings.HasPrefix(v, "-") {
			if strings.ContainsAny(v, `/\`) || strings.HasSuffix(v, ".jsonl") {
				if !filepath.IsAbs(v) {
					v = filepath.Join(p.Cwd, v)
				}
				if h, ok := piHead(v); ok {
					return Session{ID: h.ID, Path: v}, true
				}
				return Session{}, false
			}
			return Session{ID: v}, true
		}
		if v, ok := flagValue(p.Argv, "--session-id"); ok && v != "" && !strings.HasPrefix(v, "-") {
			return Session{ID: v}, true
		}
		return Session{}, false
	},
}

// --- Claude Code: <config>/projects/<cwd, all but letters and digits
// as '-'>/<id>.jsonl, and <config>/sessions/<pid>.json for each running
// session.

func claudeDir(e Env) string {
	if d := e.env("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(e.Home, ".claude")
}

// claudeProject is the folder of a cwd's sessions, "" when Claude Code
// names it with a hash (a long path).
func claudeProject(e Env, cwd string) string {
	if cwd == "" {
		return ""
	}
	b := []byte(filepath.Clean(cwd))
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	if len(b) > 200 {
		return ""
	}
	return filepath.Join(claudeDir(e), "projects", string(b))
}

// claudeFile is the file of a session in a cwd, if it is there.
func claudeFile(e Env, cwd, id string) string {
	dir := claudeProject(e, cwd)
	if dir == "" || id == "" || strings.ContainsAny(id, `/\`) {
		return ""
	}
	p := filepath.Join(dir, id+".jsonl")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// claudeCreated is when a transcript began: the earliest time of its first
// lines.
func claudeCreated(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var first int64
	for n := 0; n < 50 && sc.Scan(); n++ {
		var l struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if ms := isoMs(l.Timestamp); ms > 0 && (first == 0 || ms < first) {
			first = ms
		}
	}
	return first
}

var claudeAgent = agent{
	root: func(e Env) string { return filepath.Join(claudeDir(e), "projects") },
	file: func(path string) (Session, bool) {
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		return Session{ID: id, Path: path}, id != ""
	},
	own: func(e Env, p Proc) (Session, bool) {
		if p.Pid <= 0 {
			return Session{}, false
		}
		b, err := os.ReadFile(filepath.Join(claudeDir(e), "sessions", itoa(p.Pid)+".json"))
		if err != nil {
			return Session{}, false
		}
		var f map[string]any
		if json.Unmarshal(b, &f) != nil {
			return Session{}, false
		}
		if pid, ok := f["pid"].(float64); ok && int(pid) != p.Pid {
			return Session{}, false
		}
		for _, k := range []string{"sessionId", "session_id", "sessionID"} {
			if id, ok := f[k].(string); ok && id != "" {
				cwd := p.Cwd
				if c, ok := f["cwd"].(string); ok && c != "" {
					cwd = c
				}
				return Session{ID: id, Path: claudeFile(e, cwd, id)}, true
			}
		}
		return Session{}, false
	},
	made: func(e Env, cwd string, sinceMs int64) []candidate {
		dir := claudeProject(e, cwd)
		if dir == "" {
			return nil
		}
		var out []candidate
		for _, c := range jsonlSince(dir, sinceMs) {
			c.ID = strings.TrimSuffix(filepath.Base(c.Path), ".jsonl")
			c.createdMs = claudeCreated(c.Path)
			out = append(out, c)
		}
		return out
	},
	named: func(e Env, p Proc) (Session, bool) {
		for _, names := range [][]string{{"--resume", "-r"}, {"--session-id"}} {
			if v, ok := flagValue(p.Argv, names...); ok && looksLikeID(v) {
				return Session{ID: v, Path: claudeFile(e, p.Cwd, v)}, true
			}
		}
		return Session{}, false
	},
}

// looksLikeID reports whether s is a UUID-like session id.
func looksLikeID(s string) bool {
	if len(s) < 8 || strings.HasPrefix(s, "-") {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-') {
			return false
		}
	}
	return true
}

// --- Codex: <home>/sessions/YYYY/MM/DD/rollout-<time>-<id>.jsonl, a
// session_meta line {"payload":{"id","timestamp","cwd"}} first.

func codexDir(e Env) string {
	if d := e.env("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(e.Home, ".codex")
}

type codexMeta struct {
	ID, Cwd   string
	CreatedMs int64
}

func codexHead(path string) (codexMeta, bool) {
	line, ok := firstLine(path)
	if !ok {
		return codexMeta{}, false
	}
	var h struct {
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			ID        string `json:"id"`
			Timestamp string `json:"timestamp"`
			Cwd       string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &h) != nil {
		return codexMeta{}, false
	}
	m := codexMeta{ID: h.Payload.ID, Cwd: h.Payload.Cwd, CreatedMs: isoMs(h.Payload.Timestamp)}
	if m.ID == "" {
		m.ID = h.ID
	}
	if m.CreatedMs == 0 {
		m.CreatedMs = isoMs(h.Timestamp)
	}
	return m, m.ID != ""
}

var codexAgent = agent{
	root: func(e Env) string { return filepath.Join(codexDir(e), "sessions") },
	file: func(path string) (Session, bool) {
		m, ok := codexHead(path)
		return Session{ID: m.ID, Path: path}, ok
	},
	made: func(e Env, cwd string, sinceMs int64) []candidate {
		root := filepath.Join(codexDir(e), "sessions")
		var out []candidate
		// Its folders are by local day: the day before the start too, for
		// clocks and zones.
		day := time.UnixMilli(sinceMs).AddDate(0, 0, -1)
		for end := time.Now().AddDate(0, 0, 1); day.Before(end); day = day.AddDate(0, 0, 1) {
			dir := filepath.Join(root, day.Format("2006"), day.Format("01"), day.Format("02"))
			for _, c := range jsonlSince(dir, sinceMs) {
				m, ok := codexHead(c.Path)
				if !ok || m.Cwd == "" || filepath.Clean(m.Cwd) != filepath.Clean(cwd) {
					continue
				}
				c.ID, c.createdMs = m.ID, m.CreatedMs
				out = append(out, c)
			}
		}
		return out
	},
	named: func(e Env, p Proc) (Session, bool) {
		for i := 1; i+1 < len(p.Argv); i++ {
			if p.Argv[i] == "resume" && !strings.HasPrefix(p.Argv[i+1], "-") && p.Argv[i+1] != "" {
				return Session{ID: p.Argv[i+1]}, true
			}
		}
		return Session{}, false
	},
}
