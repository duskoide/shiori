package mods

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var events = []string{"focus.changed", "agent.status"}

func TestParse(t *testing.T) {
	good := `{"api":1,"name":"Git tools","run":["./git-tools"],
		"commands":[{"name":"branch","title":"Git: switch branch","args":[{"name":"branch"}]},
		            {"name":"open-pr","title":"Open PR","run":["./open-pr.sh"],"timeout":10}],
		"keys":{"global":{"ctrl-alt-b":"git-tools.branch"}},
		"events":["focus.changed"]}`
	m, err := Parse("git-tools", []byte(good), events)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Commands) != 2 || m.Commands[1].TimeoutSeconds() != 10 || m.Commands[0].TimeoutSeconds() != DefaultTimeout || m.StartsAtStartup() {
		t.Fatalf("%+v", m)
	}
	km, _ := m.Keymap("git-tools")
	if got := km.KeysFor("git-tools.branch"); len(got) != 1 || got[0] != "Ctrl+Alt+B" {
		t.Fatalf("keys %v", got)
	}

	for _, tc := range []struct{ doc, want string }{
		{`{"name":"x","commands":[{"name":"a","title":"A","run":["x"]}]}`, `no "api"`},
		{`{"api":2,"name":"x"}`, "api 2"},
		{`{"api":1,"commands":[{"name":"a","title":"A","run":["x"]}]}`, `no "name"`},
		{`{"api":1,"name":"x"}`, "at least one command or event"},
		{`{"api":1,"name":"x","colour":"red","commands":[]}`, `unknown field "colour"`},
		{`{"api":1,"name":"x","commands":[{"name":"a","title":"A"}]}`, "no service to run it"},
		{`{"api":1,"name":"x","commands":[{"name":"a b","title":"A","run":["x"]}]}`, "not letters"},
		{`{"api":1,"name":"x","commands":[{"name":"a","title":"A","run":["x"]},{"name":"a","title":"B","run":["x"]}]}`, "twice"},
		{`{"api":1,"name":"x","commands":[{"name":"a","run":["x"]}]}`, "no title"},
		{`{"api":1,"name":"x","commands":[{"name":"a","title":"A","run":["x"],"timeout":601}]}`, "timeout"},
		{`{"api":1,"name":"x","commands":[{"name":"a","title":"A","run":["x"],"args":[{"name":"b"},{"name":"b"}]}]}`, "each once"},
		{`{"api":1,"name":"x","start":"later","run":["x"],"events":["focus.changed"]}`, `"start"`},
		{`{"api":1,"name":"x","start":"startup","commands":[{"name":"a","title":"A","run":["x"]}]}`, `needs a "run"`},
		{`{"api":1,"name":"x","commands":[{"name":"a","title":"A","run":["x"]}],"events":["focus.changed"]}`, `"events" needs a "run"`},
		{`{"api":1,"name":"x","run":["x"],"events":["tab.exploded"]}`, `no event "tab.exploded"`},
		{`{"api":1,"name":"x","commands":[{"name":"a","title":"A","run":["x"]}],"keys":{"global":{"ctrl-alt-t":"tab.new"}}}`, "not one of the mod's commands"},
		{`{"api":1,"name":"x","commands":[{"name":"a","title":"A","run":["x"]}],"keys":{"global":{"hyper-q":"x.a"}}}`, "keys:"},
		{`{"api":1,"name":"x","commands":[{"name":"a","title":"A","run":["x"]}]} {}`, "more than one"},
	} {
		if _, err := Parse("x", []byte(tc.doc), events); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.doc, err, tc.want)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(name, doc string) {
		os.MkdirAll(filepath.Join(dir, name), 0o755)
		if doc != "" {
			os.WriteFile(filepath.Join(dir, name, "mod.json"), []byte(doc), 0o644)
		}
	}
	write("zeta", `{"api":1,"name":"Z","commands":[{"name":"a","title":"A","run":["./a"]}]}`)
	write("alpha", `{"api":1,"name":"A"}`)
	write("empty", "")
	write("Bad_Name", `{}`)
	write(".hidden", `{}`)
	os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0o644)
	got := Load(dir, events)
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "Bad_Name,alpha,empty,zeta" {
		t.Fatalf("ids %v", ids)
	}
	if got[0].Err == nil || got[1].Err == nil || got[2].Err == nil || got[2].Err.Error() != "no mod.json" {
		t.Fatalf("errs %v %v %v", got[0].Err, got[1].Err, got[2].Err)
	}
	z := got[3]
	if z.Err != nil || z.Manifest.Name != "Z" || !strings.HasPrefix(z.Hash, "sha256:") {
		t.Fatalf("%+v", z)
	}
	if r := Resolve(z.Dir, []string{"./a", "x"}); r[0] != filepath.Join(dir, "zeta", "a") || r[1] != "x" {
		t.Fatalf("resolve %v", r)
	}
	if r := Resolve(z.Dir, []string{"sh"}); r[0] != "sh" {
		t.Fatalf("resolve %v", r)
	}
	if Load(filepath.Join(dir, "missing"), events) != nil {
		t.Fatal("a missing folder has mods")
	}
}
