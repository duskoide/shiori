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
	focusReq := a.focusReq
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	if !a.prefixPending() {
		t.Fatal("the prefix key did not enter command mode")
	}
	if !tt.HasText("COMMAND") || tt.HasText("NORMAL") {
		t.Fatalf("command mode is not shown: %q", tt.Texts())
	}
	// Only the mode changes: no dialog, the focus where it was.
	if tt.Focused("Command mode") || a.focusReq != focusReq {
		t.Fatal("command mode took the focus")
	}
	if !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.Contains(s, "· ? lists them") }) {
		t.Errorf("no hint of the key list: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyA)
	settle(tt)
	if a.prefixPending() || !a.settings {
		t.Fatalf("pending %v, settings %v", a.prefixPending(), a.settings)
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
