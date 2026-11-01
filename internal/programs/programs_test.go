package programs

import (
	"strings"
	"testing"
)

func TestResolveKeys(t *testing.T) {
	ps := func(w ...string) string {
		q := make([]string, len(w))
		for i, s := range w {
			q[i] = "'" + strings.ReplaceAll(s, "'", "''") + "'"
		}
		return "& " + strings.Join(q, " ")
	}
	tests := []struct {
		name  string
		app   string
		argv  []string
		key   string
		pname string
		shell bool
		agent bool
	}{
		{"app alias", "claude-code", []string{"/bin/zsh"}, "claude", "Claude Code", false, true},
		{"app key case", "CODEX", nil, "codex", "Codex", false, true},
		{"unknown app", "newagent", nil, "agent", "newagent", false, true},
		{"empty argv", "", nil, "shell", "Shell", true, false},
		{"login shell", "", []string{"-zsh"}, "shell", "zsh", true, false},
		{"windows shell", "", []string{`C:\Windows\powershell.exe`}, "shell", "powershell", true, false},
		{"nvim", "", []string{"nvim", "x.rs"}, "nvim", "Neovim", false, false},
		{"alias vi", "", []string{"/usr/bin/vi"}, "vim", "Vim", false, false},
		{"node script", "", []string{"node", "/x/node_modules/.bin/vite", "dev"}, "vite", "", false, false},
		{"npx", "", []string{"npx", "vitest"}, "vitest", "", false, false},
		{"node plain", "", []string{"node", "server.js"}, "node", "", false, false},
		{"node script stem", "", []string{"node", "--inspect", "vite.js"}, "vite", "", false, false},
		{"python module", "", []string{"python3", "-m", "http.server", "8799"}, "python", "", false, false},
		{"uv run", "", []string{"uv", "run", "pytest"}, "pytest", "", false, false},
		{"unknown", "", []string{"/opt/mytool"}, "program", "mytool", false, false},
		{"zsh wrapper", "", []string{"/bin/zsh", "-ic", `"$@"; exec "$0" -i`, "/bin/zsh", "vi", "x"}, "vim", "", false, false},
		{"bash wrapper agent", "", []string{"bash", "-ic", `"$@"; exec "$0" -i`, "bash", "claude"}, "claude", "", false, false},
		{"fish wrapper", "", []string{"/usr/bin/fish", "-ic", "command $argv[2..]; exec $argv[1] -i", "/usr/bin/fish", "vi", "x"}, "vim", "", false, false},
		{"pwsh wrapper", "", []string{"pwsh.exe", "-NoExit", "-Command", ps("codex", "fix it")}, "codex", "", false, false},
		{"shell with args not wrapper", "", []string{"bash", "-c", "ls"}, "shell", "bash", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := Resolve(tc.app, tc.argv)
			if p.Key != tc.key || p.Shell != tc.shell || p.Agent != tc.agent {
				t.Fatalf("got %+v", p)
			}
			if tc.pname != "" && p.Name != tc.pname {
				t.Errorf("name = %q, want %q", p.Name, tc.pname)
			}
		})
	}
}

func TestProgramFields(t *testing.T) {
	c := Resolve("claude", nil)
	if c.Tint || c.Tile != "#1c1d21" || c.Color != "#ffffff" || c.Icon != "claude" || len(c.Run) != 1 || c.Run[0] != "claude" {
		t.Errorf("claude: %+v", c)
	}
	if IconSVG(c.Icon) == nil {
		t.Error("claude icon missing")
	}
	s := Resolve("", nil)
	if !s.Tint || s.Tile != "#16241a" || s.Color != "#4ec95f" {
		t.Errorf("shell: %+v", s)
	}
	a := Resolve("zzz", nil)
	if a.Tile != "#1c1d21" || a.Color != "#d6d8db" || a.Icon != "agent" {
		t.Errorf("agent: %+v", a)
	}
}

func TestGenericTile(t *testing.T) {
	// FNV-1a 32 of "a" is 0xe40c292c; 0xe40c292c % 8 == 4.
	p := Resolve("", []string{"a"})
	if p.Key != "program" || p.Tile != tilePalette[4] || p.Color != "#ffffff" || p.Name != "a" {
		t.Errorf("a: %+v", p)
	}
	if hashTile("") != tilePalette[2166136261%8] {
		t.Error("empty hash")
	}
	if Resolve("", []string{"mytool"}).Tile != Resolve("", []string{"/usr/bin/mytool"}).Tile {
		t.Error("tile not stable")
	}
}

func TestBase(t *testing.T) {
	for in, want := range map[string]string{
		"/bin/zsh": "zsh", "-zsh": "zsh", "C:\\x\\Pwsh.EXE": "Pwsh", "a.exe": "a", ".exe": ".exe", "": "",
	} {
		if got := base(in); got != want {
			t.Errorf("base(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPowershellWords(t *testing.T) {
	line := "& 'pi' 'it''s ’’fine’’' '' 'a  b'"
	w, ok := powershellWords(line)
	want := []string{"pi", "it's ’fine’", "", "a  b"}
	if !ok || len(w) != len(want) {
		t.Fatalf("%q %v", w, ok)
	}
	for i := range want {
		if w[i] != want[i] {
			t.Errorf("word %d = %q, want %q", i, w[i], want[i])
		}
	}
	if _, ok := powershellWords("codex"); ok {
		t.Error("expected failure")
	}
}

func TestStartableOrder(t *testing.T) {
	var keys []string
	for _, p := range Startable() {
		if len(p.Run) == 0 || !p.Agent {
			t.Errorf("not startable agent: %+v", p)
		}
		keys = append(keys, p.Key)
	}
	first := keys[:9]
	want := []string{"claude", "codex", "pi", "opencode", "cursor", "copilot", "amp", "gemini", "grok"}
	for i := range want {
		if first[i] != want[i] {
			t.Fatalf("order = %v", keys)
		}
	}
	rest := keys[9:]
	if len(rest) == 0 {
		t.Fatal("no other agents")
	}
	for i := 1; i < len(rest); i++ {
		if rest[i-1] >= rest[i] {
			t.Errorf("rest not alphabetical: %v", rest)
		}
	}
}

func TestEveryEntryHasIcon(t *testing.T) {
	d := Default()
	for k, e := range d.programs {
		if e.Icon == "" {
			t.Errorf("%s has no icon", k)
			continue
		}
		if b := IconSVG(e.Icon); len(b) == 0 || !strings.Contains(string(b), "<svg") {
			t.Errorf("%s: icon %q missing or invalid", k, e.Icon)
		}
	}
	if IconSVG("nope") != nil || IconSVG("../x") != nil || IconSVG("") != nil {
		t.Error("expected nil icons")
	}
	if IconSVG("icons/claude.svg") == nil || IconSVG("claude.svg") == nil {
		t.Error("prefixed names should resolve")
	}
	for _, f := range []string{"LICENSE-Lucide.txt", "LICENSE-LobeHub.txt", "LICENSES.md"} {
		if b, err := iconFS.ReadFile("icons/" + f); err != nil || len(b) == 0 {
			t.Errorf("missing %s", f)
		}
	}
}

func TestLoadUserTable(t *testing.T) {
	t.Cleanup(func() { _ = Load(nil) })
	user := `{
	  "shells": ["myshell"], "interpreters": ["myrun"],
	  "programs": {
	    "claude": {"name": "Claude!", "tile": "#000000"},
	    "mytool": {"name": "My Tool", "icon": "icons/agent.svg", "match": ["mt"], "run": ["mytool","--go"], "tint": false}
	  }
	}`
	if err := Load([]byte(user)); err != nil {
		t.Fatal(err)
	}
	c := Resolve("claude", nil)
	if c.Name != "Claude!" || c.Tile != "#000000" || c.Color != "#ffffff" || c.Tint {
		t.Errorf("merged claude: %+v", c)
	}
	if p := Resolve("", []string{"mt"}); p.Key != "mytool" || p.Tint || p.Icon != "agent" {
		t.Errorf("mt: %+v", p)
	}
	if p := Resolve("", []string{"myshell"}); !p.Shell {
		t.Errorf("myshell: %+v", p)
	}
	if p := Resolve("", []string{"myrun", "mytool"}); p.Key != "mytool" {
		t.Errorf("myrun: %+v", p)
	}
	found := false
	for _, p := range Startable() {
		found = found || p.Key == "mytool"
	}
	if !found {
		t.Error("mytool not startable")
	}
	// A bad file leaves the built-in table and reports the error.
	if err := Load([]byte("{bad")); err == nil {
		t.Error("expected error")
	}
	if Resolve("claude", nil).Name != "Claude Code" {
		t.Error("table not reset after bad file")
	}
	// The built-in table is unaffected by merges into copies.
	d := Default()
	if err := d.Merge([]byte(`{"programs":{"claude":{"name":"X"}}}`)); err != nil {
		t.Fatal(err)
	}
	if Default().Resolve("claude", nil).Name != "Claude Code" {
		t.Error("Default shares state")
	}
}
