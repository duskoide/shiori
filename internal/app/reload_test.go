package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReloadConfig(t *testing.T) {
	home := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := newApp(home, "")
	if a.cfg.Font != "" {
		t.Fatal("expected no font")
	}

	write("settings.json", `{"font":"Iosevka","font_size":16,"theme":"paper"}`)
	write("keys.json", `{"global":{"alt-q":"app.palette"}}`)
	a.reloadConfig(false)
	if a.cfg.Font != "Iosevka" || a.cfg.FontSize != 16 || a.cfg.Theme != "paper" {
		t.Fatalf("settings not applied: %+v", a.cfg)
	}
	if len(a.toasts) != 1 {
		t.Fatalf("want one toast, got %d", len(a.toasts))
	}

	// A rewrite with the same content says nothing.
	write("settings.json", `{"font":"Iosevka","font_size":16,"theme":"paper"}`)
	a.reloadConfig(false)
	if len(a.toasts) != 1 {
		t.Fatal("toast for an unchanged file")
	}

	// A broken file keeps the settings and the keys in use, and says so.
	write("settings.json", "{oops")
	write("keys.json", "{oops")
	km := a.keymap.Clone()
	a.reloadConfig(false)
	if a.cfg.Font != "Iosevka" || !reflect.DeepEqual(a.keymap, km) {
		t.Fatal("a broken file replaced what was in use")
	}

	// A write pending from the window is not overwritten by the file.
	write("settings.json", `{"font":"Other"}`)
	a.saveSettingsSoon()
	a.reloadConfig(false)
	if a.cfg.Font != "Iosevka" {
		t.Fatal("reloaded over a pending write")
	}
	a.saveTimer.Stop()
	a.saveTimer = nil

	// A removed file goes back to the defaults.
	os.Remove(filepath.Join(home, "settings.json"))
	a.reloadConfig(false)
	if a.cfg.Font != "" {
		t.Fatalf("settings kept after removal: %+v", a.cfg)
	}
}
