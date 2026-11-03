package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/keys"
)

func TestPrefixKeyThenKey(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	km := a.keymap.Clone()
	km.Bind(keys.PrefixSection, chord(t, "a"), keys.Action{Command: "app.settings"})
	a.keymap = km
	settle(tt)

	if !tt.HasText("NORMAL") || tt.HasText("COMMAND") {
		t.Fatalf("not in normal mode: %q", tt.Texts())
	}
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	if !a.prefixPending() {
		t.Fatal("the prefix key did not enter command mode")
	}
	if !tt.HasText("COMMAND") || tt.HasText("NORMAL") {
		t.Fatalf("command mode is not shown: %q", tt.Texts())
	}
	// No dialog, but the keyboard is command mode's, not the terminal's.
	if !tt.Focused("Command mode") {
		t.Fatal("command mode does not hold the focus")
	}
	if !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.Contains(s, "· ? lists them") }) {
		t.Errorf("no hint of the key list: %q", tt.Texts())
	}
	// As on Linux: the key, then its text apart from it, which the
	// terminal must not get.
	tt.TypeKey(0, ui.KeyA, "a")
	settle(tt)
	if a.prefixPending() || !a.settings {
		t.Fatalf("pending %v, settings %v", a.prefixPending(), a.settings)
	}
	if tt.Focused("Command mode") {
		t.Fatal("command mode kept the focus")
	}
}

func TestPrefixGivesTheFocusBack(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	want := a.focus[a.activeTab[a.activeSpace]]
	a.focusReq = ""
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	if !tt.Focused("Command mode") {
		t.Fatal("command mode does not hold the focus")
	}
	a.cancelPrefix()
	if a.focusReq != want {
		t.Fatalf("focus goes to %q, not the pane %q", a.focusReq, want)
	}
}

func TestPrefixClickAwayGoesBack(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	if !tt.Focused("Command mode") {
		t.Fatal("command mode does not hold the focus")
	}
	mustClick(t, tt, "Settings")
	settle(tt)
	if a.prefixPending() {
		t.Fatal("a click away did not end command mode")
	}
}

func TestPrefixUnboundKeyAndEscCancel(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	for _, key := range []ui.Key{ui.KeyEscape, ui.KeyQ} {
		tt.Key(ui.Ctrl, ui.KeyB)
		settle(tt)
		if !a.prefixPending() {
			t.Fatal("not in command mode")
		}
		tt.Key(0, key)
		settle(tt)
		if a.prefixPending() || a.settings {
			t.Fatalf("key %v did not end command mode", key)
		}
	}
}

func TestDirectKeysAreGone(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	n := len(a.space().Tabs)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyT) // New tab, before
	settle(tt)
	a.runPosted()
	if len(a.space().Tabs) != n || a.prefixPending() {
		t.Fatal("a direct key still runs")
	}
}

func TestPrefixKeyWithModifiersHeld(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	km := a.keymap.Clone()
	km.Bind(keys.PrefixSection, chord(t, "a"), keys.Action{Command: "app.settings"})
	a.keymap = km
	settle(tt)
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	tt.Key(ui.Ctrl, ui.KeyA) // Ctrl still held
	settle(tt)
	if !a.settings {
		t.Fatal("Ctrl+A did not run the binding of A")
	}
}

func TestTypedLabel(t *testing.T) {
	for in, want := range map[string]string{"Shift+/": "?", "Shift+A": "A", "Ctrl+Shift+/": "Ctrl+Shift+/", "Space": "Space"} {
		if got := typedLabel(in); got != want {
			t.Errorf("typedLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// On Linux, ? (Shift and /) comes as its text alone, with no key press.
func TestPrefixKeyAsTextAlone(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	// The system sends text only to an element taking it.
	if _, ok := tt.TextCaret(); !ok || !tt.Focused("Command mode") {
		t.Fatal("command mode does not take text")
	}
	tt.Type("?")
	settle(tt)
	if a.prefixPending() || !a.palette.open {
		t.Fatalf("pending %v, key list open %v", a.prefixPending(), a.palette.open)
	}
}

func TestTextChord(t *testing.T) {
	for text, want := range map[string]string{"?": "shift-/", "v": "v", "V": "shift-v", "%": "", " ": "space", "|": "shift-\\"} {
		got, ok := textChord(text)
		if want == "" {
			if ok {
				t.Errorf("%q: %v, want none", text, got)
			}
			continue
		}
		if w := chord(t, want); !ok || got != w {
			t.Errorf("%q: %v %v, want %v", text, got, ok, w)
		}
	}
}
