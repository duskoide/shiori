package keys

import (
	"reflect"
	"testing"
)

func TestParseChord(t *testing.T) {
	tests := []struct {
		in      string
		want    Chord
		wantErr bool
	}{
		{"ctrl-shift-t", Chord{Ctrl: true, Shift: true, Key: "t"}, false},
		{"SHIFT-Ctrl-T", Chord{Ctrl: true, Shift: true, Key: "t"}, false},
		{"alt-[", Chord{Alt: true, Key: "["}, false},
		{"alt-]", Chord{Alt: true, Key: "]"}, false},
		{"ctrl-+", Chord{Ctrl: true, Key: "+"}, false},
		{"ctrl--", Chord{Ctrl: true, Key: "-"}, false},
		{"ctrl-,", Chord{Ctrl: true, Key: ","}, false},
		{"ctrl-shift-?", Chord{Ctrl: true, Shift: true, Key: "?"}, false},
		{"ctrl-shift-/", Chord{Ctrl: true, Shift: true, Key: "/"}, false},
		{"f2", Chord{Key: "f2"}, false},
		{"shift-f2", Chord{Shift: true, Key: "f2"}, false},
		{"ctrl-pagedown", Chord{Ctrl: true, Key: "pagedown"}, false},
		{"Ctrl-PageUp", Chord{Ctrl: true, Key: "pageup"}, false},
		{"cmd-k", Chord{Super: true, Key: "k"}, false},
		{"super-alt-left", Chord{Super: true, Alt: true, Key: "left"}, false},
		{"ctrl-alt-shift-escape", Chord{Ctrl: true, Alt: true, Shift: true, Key: "escape"}, false},
		{"ctrl-backspace", Chord{Ctrl: true, Key: "backspace"}, false},
		{"ctrl-insert", Chord{Ctrl: true, Key: "insert"}, false},
		{"alt-1", Chord{Alt: true, Key: "1"}, false},
		{"", Chord{}, true},
		{"ctrl-", Chord{}, true},
		{"ctrl-shift", Chord{}, true},
		{"ctrl-nokey", Chord{}, true},
		{"f0", Chord{}, true},
		{"f25", Chord{}, true},
	}
	for _, tc := range tests {
		got, err := ParseChord(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseChord(%q) = %+v, %v; want %+v err=%v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestParseAction(t *testing.T) {
	tests := []struct {
		in      string
		want    Action
		wantErr bool
	}{
		{"tab.new", Action{Command: "tab.new"}, false},
		{"pane.go:left", Action{Command: "pane.go", Arg: "left"}, false},
		{"mod.open:picker", Action{Command: "mod.open", Arg: "picker"}, false},
		{"send:\x1bb", Action{Send: "\x1bb"}, false},
		{"send:", Action{}, false},
		{"terminal", Action{Terminal: true}, false},
		{"", Action{}, true},
		{":x", Action{}, true},
	}
	for _, tc := range tests {
		got, err := ParseAction(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseAction(%q) = %+v, %v", tc.in, got, err)
		}
	}
}

func TestChordString(t *testing.T) {
	for in, want := range map[string]string{
		"ctrl-shift-t":  "Ctrl+Shift+T",
		"shift-ctrl-t":  "Ctrl+Shift+T",
		"alt-1":         "Alt+1",
		"ctrl-pagedown": "Ctrl+PageDown",
		"f2":            "F2",
		"ctrl-+":        "Ctrl++",
		"alt-[":         "Alt+[",
		"cmd-shift-k":   "Shift+Super+K",
		"alt-left":      "Alt+Left",
	} {
		c, err := ParseChord(in)
		if err != nil || c.String() != want {
			t.Errorf("%q -> %q (%v), want %q", in, c.String(), err, want)
		}
	}
}

func TestDefaultParsesCleanly(t *testing.T) {
	km, err := Parse(defaultKeys)
	if err != nil {
		t.Fatal(err)
	}
	if len(km.Sections["global"]) < 50 || len(km.Sections["terminal"]) != 8 {
		t.Errorf("sections: global=%d terminal=%d", len(km.Sections["global"]), len(km.Sections["terminal"]))
	}
	want := Action{Command: "tab.new"}
	if a, ok := Default().Lookup("global", Chord{Ctrl: true, Shift: true, Key: "t"}); !ok || a != want {
		t.Errorf("lookup ctrl-shift-t = %+v %v", a, ok)
	}
	if a, ok := km.Lookup("terminal", Chord{Ctrl: true, Key: "left"}); !ok || a.Send != "\x1bb" {
		t.Errorf("ctrl-left = %+v", a)
	}
	if a, ok := km.Lookup("terminal", Chord{Ctrl: true, Key: "backspace"}); !ok || a.Send != "\x17" {
		t.Errorf("ctrl-backspace = %+v", a)
	}
	if _, ok := km.Lookup("global", Chord{Key: "q"}); ok {
		t.Error("unexpected binding")
	}
	if _, ok := km.Lookup("nosuch", Chord{Key: "q"}); ok {
		t.Error("unexpected section")
	}
}

func TestKeysFor(t *testing.T) {
	km := Default()
	tests := []struct {
		cmd  string
		want []string
	}{
		{"tab.new", []string{"Ctrl+Shift+T"}},
		{"tab.next", []string{"Alt+]", "Ctrl+PageDown", "Ctrl+Tab"}},
		{"app.zoom_in", []string{"Ctrl++", "Ctrl+="}},
		{"space.select:1", []string{"Alt+1"}},
		{"space.select:10", nil},
		{"app.help", []string{"Ctrl+Shift+/", "Ctrl+Shift+?"}},
		{"terminal.copy", []string{"Ctrl+Insert", "Ctrl+Shift+C"}},
		{"rename.nothing", nil},
		{"tab.rename", []string{"F2"}},
	}
	for _, tc := range tests {
		if got := km.KeysFor(tc.cmd); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("KeysFor(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
	if got := km.KeysFor("space.select"); len(got) != 9 || got[0] != "Alt+1" {
		t.Errorf("space.select = %v", got)
	}
}

func TestKeysForGlobalFirst(t *testing.T) {
	km, err := Parse([]byte(`{"mod:a":{"ctrl-x":"do.it"},"global":{"alt-x":"do.it"},"layout:z":{"f5":"do.it"},"terminal":{"f6":"terminal"}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Alt+X", "F5", "Ctrl+X"}
	if got := km.KeysFor("do.it"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("nope")); err == nil {
		t.Error("expected JSON error")
	}
	if _, err := Parse([]byte(`{"global":{"ctrl-x":5}}`)); err == nil {
		t.Error("expected type error")
	}
	km, err := Parse([]byte(`{"global":{"ctrl-x":"a.b","bad-key":"a.c","ctrl-y":"","Ctrl-X":"dup.dup"}}`))
	if err == nil {
		t.Fatal("expected errors")
	}
	if len(km.Sections["global"]) != 1 {
		t.Errorf("valid bindings should survive: %v", km.Sections["global"])
	}
}

func TestEncodeRoundTrips(t *testing.T) {
	km := Default()
	b, err := km.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(km, back) {
		t.Fatal("the encoded keymap reads back different")
	}
	for _, s := range []string{"ctrl-shift-t", "alt-[", "ctrl--", "ctrl-+", "f2", "shift-f2", "ctrl-alt-shift-super-pagedown"} {
		c, err := ParseChord(s)
		if err != nil {
			t.Fatal(err)
		}
		if again, err := ParseChord(c.Spec()); err != nil || again != c {
			t.Errorf("%s: spec %q reads back %v, %v", s, c.Spec(), again, err)
		}
	}
	for _, s := range []string{"terminal", "send:\u0017", "send:", "tab.new", "pane.go:left"} {
		a, err := ParseAction(s)
		if err != nil {
			t.Fatal(err)
		}
		if a.Spec() != s {
			t.Errorf("%q spec %q", s, a.Spec())
		}
	}
}

func TestBindUnbindChordsFor(t *testing.T) {
	km := Default().Clone()
	c := Chord{Ctrl: true, Shift: true, Key: "t"}
	old, had := km.Bind("global", c, Action{Command: "pane.zoom"})
	if !had || old.Command != "tab.new" {
		t.Fatalf("old %v %v", old, had)
	}
	if got := km.ChordsFor("global", "pane.zoom"); len(got) != 2 {
		t.Fatalf("zoom chords %v", got)
	}
	if !km.Unbind("global", c) || km.Unbind("global", c) {
		t.Fatal("unbind")
	}
	if Default().Sections["global"][c].Command != "tab.new" {
		t.Fatal("the clone shares its maps")
	}
	if _, had := (&Keymap{}).Bind("new", c, Action{Terminal: true}); had {
		t.Fatal("an empty keymap had a binding")
	}
}

func TestOverlayAddFreeDiff(t *testing.T) {
	base := Default()
	mod := &Keymap{}
	mod.Bind("global", Chord{Ctrl: true, Alt: true, Key: "g"}, Action{Command: "git.branch"})
	mod.Bind("global", Chord{Ctrl: true, Shift: true, Key: "t"}, Action{Command: "git.tab"}) // taken
	taken := base.AddFree(mod)
	if len(taken["global"]) != 1 || taken["global"][0].String() != "Ctrl+Shift+T" {
		t.Fatalf("taken %v", taken)
	}
	if got := base.KeysFor("git.branch"); len(got) != 1 {
		t.Fatalf("git keys %v", got)
	}

	user, err := Parse([]byte(`{"global":{"ctrl-shift-t":"none","ctrl-alt-n":"tab.new","ctrl-alt-g":"none"}}`))
	if err != nil {
		t.Fatal(err)
	}
	eff := base.Clone()
	eff.Overlay(user)
	if got := eff.KeysFor("tab.new"); len(got) != 1 || got[0] != "Ctrl+Alt+N" {
		t.Fatalf("tab.new %v", got)
	}
	if len(eff.KeysFor("git.branch")) != 0 {
		t.Fatal("none kept a mod's key")
	}
	d := Diff(base, eff)
	if !reflect.DeepEqual(d, user) {
		t.Fatalf("diff %v\nwant %v", d.Sections, user.Sections)
	}
	again := base.Clone()
	again.Overlay(d)
	if !reflect.DeepEqual(again, eff) {
		t.Fatal("base with the diff is not the target")
	}
	if !Diff(base, base.Clone()).Empty() || (&Keymap{}).Empty() == false {
		t.Fatal("empty")
	}
	if a, _ := ParseAction("none"); !a.None || a.Spec() != "none" {
		t.Fatalf("none %+v", a)
	}
}

func TestPrefixKeys(t *testing.T) {
	km := Default()
	if len(km.PrefixChords()) != 0 {
		t.Fatal("no prefix key by default")
	}
	if got := km.PrefixKeysFor("tab.new"); got != nil {
		t.Fatalf("no prefix key, yet %v", got)
	}
	if len(km.Sections[PrefixSection]) == 0 {
		t.Fatal("the default keys have a prefix section")
	}
	km.Bind("global", Chord{Ctrl: true, Key: "b"}, Action{Command: PrefixCommand})
	if got := km.PrefixKeysFor("tab.new"); len(got) != 1 || got[0] != "Ctrl+B C" {
		t.Fatalf("PrefixKeysFor = %v", got)
	}
	if got := km.PrefixKeysFor("pane.go:left"); len(got) != 1 || got[0] != "Ctrl+B H" {
		t.Fatalf("PrefixKeysFor with arg = %v", got)
	}
	// The prefix section's keys are not the window's.
	for _, s := range km.KeysFor("tab.new") {
		if s == "C" {
			t.Fatal("KeysFor returned a prefix key")
		}
	}
}
