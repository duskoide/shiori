package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/keys"
)

func TestPrefixKeyThenKey(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	km := a.keymap.Clone()
	km.Bind("global", chord(t, "ctrl-b"), keys.Action{Command: keys.PrefixCommand})
	km.Bind(keys.PrefixSection, chord(t, "a"), keys.Action{Command: "app.settings"})
	a.keymap = km
	settle(tt)

	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	if !a.prefixPending() {
		t.Fatal("the prefix key did not wait for another")
	}
	if !tt.HasText("Ctrl+B …") {
		t.Fatal("the status bar does not show the pending prefix")
	}
	tt.Key(0, ui.KeyA)
	settle(tt)
	if a.prefixPending() || !a.settings {
		t.Fatalf("pending %v, settings %v", a.prefixPending(), a.settings)
	}
}

func TestPrefixUnboundKeyCancels(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	km := a.keymap.Clone()
	km.Bind("global", chord(t, "ctrl-b"), keys.Action{Command: keys.PrefixCommand})
	a.keymap = km
	settle(tt)
	tt.Key(ui.Ctrl, ui.KeyB)
	settle(tt)
	tt.Key(0, ui.KeyQ)
	settle(tt)
	if a.prefixPending() || a.settings {
		t.Fatal("an unbound key did not end the wait")
	}
	// Without the prefix key, the prefix section's keys do nothing.
	tt.Key(0, ui.KeyC)
	settle(tt)
}
