package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duskoide/shiori/internal/keys"
)

// writeMod puts a mod in home's mods folder: its manifest and scripts.
func writeMod(t *testing.T, home, id, manifest string, scripts map[string]string) {
	t.Helper()
	dir := filepath.Join(home, "mods", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mod.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// runMod runs a command and waits for what it answers.
func runMod(t *testing.T, a *App, name string, v args) (any, error) {
	t.Helper()
	r, err := a.do(name, v)
	if err != nil {
		return nil, err
	}
	if d, ok := r.(*deferred); ok {
		return a.await(d, nil)
	}
	return r, nil
}

const toolsManifest = `{
  "api": 1, "name": "Tools", "version": "1.0",
  "commands": [
    {"name": "where", "title": "Tools: where", "run": ["./where.sh"], "args": [{"name": "x"}]},
    {"name": "say", "title": "Tools: say", "run": ["./say.sh"]},
    {"name": "fail", "title": "Tools: fail", "run": ["./fail.sh"], "hidden": true},
    {"name": "slow", "title": "Tools: slow", "run": ["./slow.sh"], "timeout": 0.3}
  ],
  "keys": {"global": {"ctrl-alt-w": "tools.where", "c": "tools.say"}}
}`

var toolsScripts = map[string]string{
	"where.sh": `printf '{"mod":"%s","cwd":"%s","args":%s,"api":"%s"}' "$SHIORI_MOD" "$SHIORI_CWD" "$SHIORI_ARGS" "$SHIORI_API"`,
	"say.sh":   `echo hello there`,
	"fail.sh":  `echo "warming up" >&2; echo "no repository here" >&2; exit 3`,
	"slow.sh":  `sleep 5`,
}

func toolsApp(t *testing.T) *App {
	t.Helper()
	a, _ := uxApp(t)
	writeMod(t, a.home, "tools", toolsManifest, toolsScripts)
	a.reloadMods()
	a.loadKeys()
	return a
}

func TestANewModIsOff(t *testing.T) {
	a := toolsApp(t)
	if len(a.toasts) != 1 || !strings.Contains(a.toasts[0].text, `New mod "Tools"`) {
		t.Fatalf("toasts %v", a.toasts)
	}
	if a.findCommand("tools.where") != nil {
		t.Fatal("an off mod's command is there")
	}
	if _, err := a.do("tools.where", nil); err == nil || !strings.Contains(err.Error(), "is off") {
		t.Fatalf("err %v", err)
	}
	if len(a.keymap.KeysFor("tools.where")) != 0 {
		t.Fatal("an off mod's keys are bound")
	}
	a.reloadMods()
	if len(a.toasts) != 1 {
		t.Fatal("told twice")
	}
}

func TestTurningAModOn(t *testing.T) {
	a := toolsApp(t)
	if err := a.setModEnabled("tools", true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(a.home, "settings.json"))
	var s Settings
	json.Unmarshal(b, &s)
	if m := s.Mods["tools"]; !m.Enabled || m.Manifest != a.mods["tools"].Hash {
		t.Fatalf("settings.json %s", b)
	}
	if c := a.findCommand("tools.where"); c == nil || c.Mod != "tools" {
		t.Fatal("no command")
	}
	titles := map[string]bool{}
	for _, it := range a.paletteItems() {
		titles[it.title] = true
	}
	if !titles["Tools: where"] || titles["Tools: fail"] {
		t.Fatalf("palette %v", titles)
	}
	// Its keys where they are free; the one the app has stays the app's.
	if got := a.keymap.PrefixKeysFor("tools.where"); len(got) != 1 || got[0] != "Ctrl+B Ctrl+Alt+W" {
		t.Fatalf("keys %v", got)
	}
	if len(a.keymap.PrefixKeysFor("tools.say")) != 0 || a.mods["tools"].taken[keys.PrefixSection][0].String() != "C" {
		t.Fatalf("taken %v", a.mods["tools"].taken)
	}
	if err := a.setModEnabled("tools", false); err != nil || a.findCommand("tools.where") != nil {
		t.Fatalf("off: %v", err)
	}
}

func TestOneShotCommands(t *testing.T) {
	a := toolsApp(t)
	a.setModEnabled("tools", true)
	r, err := runMod(t, a, "tools.where", args{"x": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(r.(json.RawMessage), &got)
	if got["mod"] != "tools" || got["cwd"] != "/src/api" || got["api"] != "1" || got["args"].(map[string]any)["x"] != 1.0 {
		t.Fatalf("result %v", got)
	}
	if r, err := runMod(t, a, "tools.say", nil); err != nil || r.(map[string]string)["output"] != "hello there" {
		t.Fatalf("say %v %v", r, err)
	}
	if _, err := runMod(t, a, "tools.fail", nil); err == nil || err.Error() != "no repository here" {
		t.Fatalf("fail %v", err)
	}
	start := time.Now()
	if _, err := runMod(t, a, "tools.slow", nil); err == nil || !strings.Contains(err.Error(), "took longer than 300ms") || time.Since(start) > 4*time.Second {
		t.Fatalf("slow %v after %s", err, time.Since(start))
	}
	if log := modLogTail(a.mods["tools"], 10); !strings.Contains(log, "no repository here") {
		t.Fatalf("log %q", log)
	}
	// From a key, an error is a toast.
	a.toasts = nil
	a.doAction("tools.fail", "")
	drain(t, a, func() bool { return len(a.toasts) == 1 })
	if a.toasts[0].text != "tools.fail: no repository here" || a.toasts[0].level != levelError {
		t.Fatalf("toast %+v", a.toasts[0])
	}
}

func TestAChangedManifestTurnsTheModOff(t *testing.T) {
	a := toolsApp(t)
	a.setModEnabled("tools", true)
	// A new program alone keeps it on.
	toolsScripts2 := map[string]string{"say.sh": "echo changed"}
	writeMod(t, a.home, "tools", toolsManifest, toolsScripts2)
	a.reloadMods()
	if !a.mods["tools"].on {
		t.Fatal("a changed program turned it off")
	}
	writeMod(t, a.home, "tools", strings.Replace(toolsManifest, `"1.0"`, `"1.1"`, 1), nil)
	a.toasts = nil
	a.reloadMods()
	if a.mods["tools"].on || a.findCommand("tools.where") != nil {
		t.Fatal("a changed manifest kept it on")
	}
	if len(a.toasts) != 1 || !strings.Contains(a.toasts[0].text, "changed") {
		t.Fatalf("toasts %v", a.toasts)
	}
	if err := a.setModEnabled("tools", true); err != nil || !a.mods["tools"].on {
		t.Fatalf("on again: %v", err)
	}
}

func TestModsThatDoNotLoad(t *testing.T) {
	a, _ := testApp(t)
	writeMod(t, a.home, "tab", `{"api":1,"name":"Sneaky","commands":[{"name":"new","title":"x","run":["x"]}]}`, nil)
	writeMod(t, a.home, "broken", `{"api":1}`, nil)
	a.reloadMods()
	for _, id := range []string{"tab", "broken"} {
		if a.mods[id].Err == nil {
			t.Errorf("%s loaded", id)
		}
		if err := a.setModEnabled(id, true); err == nil {
			t.Errorf("%s turned on", id)
		}
	}
	if _, err := a.do("broken.x", nil); err == nil || !strings.Contains(err.Error(), "does not load") {
		t.Fatalf("err %v", err)
	}
}

func TestKeysJSONOverTheModsKeys(t *testing.T) {
	a := toolsApp(t)
	a.setModEnabled("tools", true)
	os.WriteFile(filepath.Join(a.home, "keys.json"), []byte(`{"prefix":{"ctrl-alt-w":"none","ctrl-alt-s":"tools.say","c":"none"}}`), 0o644)
	a.loadKeys()
	if len(a.keymap.PrefixKeysFor("tools.where")) != 0 || len(a.keymap.PrefixKeysFor("tab.new")) != 0 {
		t.Fatalf("none kept: %v %v", a.keymap.PrefixKeysFor("tools.where"), a.keymap.PrefixKeysFor("tab.new"))
	}
	if got := a.keymap.PrefixKeysFor("tools.say"); len(got) != 1 {
		t.Fatalf("say %v", got)
	}
	// Removing a default key in the panel writes none.
	os.Remove(filepath.Join(a.home, "keys.json"))
	a.loadKeys()
	c, _ := keys.ParseChord("shift-c")
	a.unbindKey(keys.PrefixSection, c)
	if act := readKeysFile(t, a).Sections[keys.PrefixSection][c]; !act.None {
		t.Fatalf("keys.json has %+v", act)
	}
	// Bringing it back leaves nothing to keep.
	a.resetBinding(keys.PrefixSection, "space.new")
	if _, err := os.Stat(filepath.Join(a.home, "keys.json")); !os.IsNotExist(err) {
		t.Fatalf("keys.json stays: %v", err)
	}
}

func TestModCommandsGroupInTheKeyboardPage(t *testing.T) {
	a := toolsApp(t)
	a.setModEnabled("tools", true)
	groups := map[string]string{}
	for _, r := range a.bindingRows(keys.PrefixSection) {
		groups[r.spec] = r.group
	}
	if groups["tools.where"] != "Tools" || groups["tools.fail"] != "Tools" {
		t.Fatalf("groups %v", groups)
	}
}

func TestModsFolderStamp(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "mods")
	before := modsStamp(dir)
	writeMod(t, home, "x", `{}`, nil)
	mid := modsStamp(dir)
	time.Sleep(10 * time.Millisecond)
	writeMod(t, home, "x", `{"api":1}`, nil)
	if before == mid || mid == modsStamp(dir) {
		t.Fatal("the stamp did not change")
	}
}
