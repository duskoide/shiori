package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/keys"
)

func readKeysFile(t *testing.T, a *App) *keys.Keymap {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a.home, "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	km, err := keys.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return km
}

func chord(t *testing.T, s string) keys.Chord {
	t.Helper()
	c, err := keys.ParseChord(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSettingsOpenAtASection(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	if _, err := a.do("app.settings", args{"section": "keys"}); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.set.section != "keyboard" || !tt.HasText("Filter bindings") {
		t.Fatalf("section %q", a.set.section)
	}
	mustClick(t, tt, "Files")
	if a.set.section != "files" || !tt.HasText("Reload from disk") {
		t.Fatalf("section %q", a.set.section)
	}
	if _, err := a.do("app.settings", args{"section": "nope"}); err == nil {
		t.Fatal("an unknown section opened")
	}
}

func TestSettingsFont(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("terminal")
	settle(tt)
	mustClick(t, tt, "Font family")
	tt.Type("Liberation Mono, monospace")
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	if a.cfg.Font != "Liberation Mono, monospace" {
		t.Fatalf("font %q", a.cfg.Font)
	}
	mustClick(t, tt, "Default font family")
	if a.cfg.Font != "" || a.set.font != "" {
		t.Fatalf("font %q, field %q", a.cfg.Font, a.set.font)
	}
	// A font set in the file shows in the field.
	a.cfg.Font = "Fira Code"
	settle(tt)
	if a.set.font != "Fira Code" {
		t.Fatalf("field %q", a.set.font)
	}
}

// The app font is the window's own text's, kept as ui_font; empty, the
// system's.
func TestSettingsAppFont(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("app font")
	settle(tt)
	if a.set.section != "appearance" || !tt.HasText("App font") {
		t.Fatal(a.set.section, tt.Texts())
	}
	mustClick(t, tt, "App font family")
	tt.Type("DejaVu Sans")
	mustClick(t, tt, "Apply app font family")
	settle(tt)
	shot(t, tt, "app-font")
	if a.cfg.UIFont != "DejaVu Sans" || a.cfg.Font != "" {
		t.Fatalf("ui font %q, font %q", a.cfg.UIFont, a.cfg.Font)
	}
	if got := a.snapshot().settings["ui_font"]; got != "DejaVu Sans" {
		t.Fatalf("settings.changed has %v", got)
	}
	a.flushSettings()
	b, _ := os.ReadFile(filepath.Join(a.home, "settings.json"))
	if !strings.Contains(string(b), `"ui_font": "DejaVu Sans"`) {
		t.Fatalf("settings.json %s", b)
	}
	mustClick(t, tt, "Default app font family")
	if a.cfg.UIFont != "" || a.set.uiFont != "" {
		t.Fatalf("ui font %q, field %q", a.cfg.UIFont, a.set.uiFont)
	}
	// A font set in the file shows in the field.
	a.cfg.UIFont = "Noto Sans"
	settle(tt)
	if a.set.uiFont != "Noto Sans" {
		t.Fatalf("field %q", a.set.uiFont)
	}
}

func TestSettingsTextSize(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("terminal")
	settle(tt)
	mustClick(t, tt, "Larger text")
	if a.cfg.fontSize() != 14 || !tt.HasText("14 pt") {
		t.Fatalf("size %v", a.cfg.fontSize())
	}
	mustClick(t, tt, "Reset")
	if a.cfg.FontSize != 0 {
		t.Fatalf("size %v after reset", a.cfg.FontSize)
	}
	// Closing the panel writes what is due.
	a.setFontSize(20)
	mustClick(t, tt, "Close settings")
	b, err := os.ReadFile(filepath.Join(a.home, "settings.json"))
	if err != nil || !strings.Contains(string(b), `"font_size": 20`) {
		t.Fatalf("settings.json %s, %v", b, err)
	}
}

func TestSettingsSidebarSwitch(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("layout")
	settle(tt)
	mustClick(t, tt, "Show the sidebar")
	if a.cfg.sidebarShown() {
		t.Fatal("the sidebar still shows")
	}
	mustClick(t, tt, "Sidebar shown") // the switch itself
	if !a.cfg.sidebarShown() {
		t.Fatal("the sidebar is hidden")
	}
}

func TestSettingsRecordAKey(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("keyboard")
	settle(tt)
	tt.Type("new tab")
	settle(tt)
	mustClick(t, tt, "Add keys for New tab")
	if a.set.rec == nil {
		t.Fatal("not recording")
	}
	// A modifier alone waits for its key.
	tt.Key(ui.Ctrl, ui.KeyUnknown)
	settle(tt)
	if a.set.rec == nil {
		t.Fatal("a modifier alone ended the recording")
	}
	tt.Key(ui.Ctrl|ui.Alt, ui.KeyN)
	settle(tt)
	if a.set.rec != nil {
		t.Fatal("still recording")
	}
	want := []string{"Ctrl+Alt+N", "Ctrl+Shift+T"}
	if got := a.keymap.KeysFor("tab.new"); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("keys %v", got)
	}
	// keys.json holds what differs from the defaults.
	if got := readKeysFile(t, a).KeysFor("tab.new"); strings.Join(got, ",") != "Ctrl+Alt+N" {
		t.Fatalf("keys.json %v", got)
	}
	// The window's own write reads back as no change.
	n := len(a.toasts)
	a.reloadConfig(false)
	if len(a.toasts) != n {
		t.Fatalf("toasts %v", a.toasts)
	}

	// A click on a key records in its place; Esc cancels and keeps the
	// panel open.
	mustClick(t, tt, "Ctrl+Alt+N")
	if a.set.rec == nil || a.set.rec.replace == nil {
		t.Fatalf("rec %+v", a.set.rec)
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if a.set.rec != nil || !a.settings {
		t.Fatalf("rec %+v, settings %v", a.set.rec, a.settings)
	}
	mustClick(t, tt, "Ctrl+Alt+N")
	tt.Key(ui.Ctrl|ui.Alt, ui.KeyM)
	settle(tt)
	if got := a.keymap.KeysFor("tab.new"); strings.Join(got, ",") != "Ctrl+Alt+M,Ctrl+Shift+T" {
		t.Fatalf("replaced: %v", got)
	}
}

func TestSettingsKeyTakenElsewhereMoves(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("keyboard")
	settle(tt)
	tt.Type("new tab")
	settle(tt)
	mustClick(t, tt, "Add keys for New tab")
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyP)
	settle(tt)
	if len(a.keymap.KeysFor("app.palette")) != 0 {
		t.Fatalf("the palette kept its key: %v", a.keymap.KeysFor("app.palette"))
	}
	if !tt.HasText("Ctrl+Shift+P now runs New tab, no longer Command palette") {
		t.Fatalf("texts %q", tt.Texts())
	}
	a.resetBinding("global", "app.palette")
	if got := a.keymap.KeysFor("app.palette"); len(got) != 1 || got[0] != "Ctrl+Shift+P" {
		t.Fatalf("palette after reset %v", got)
	}
	if strings.Contains(strings.Join(a.keymap.KeysFor("tab.new"), ","), "Ctrl+Shift+P") {
		t.Fatal("New tab kept the key reset to the palette")
	}
}

func TestSettingsRemoveAndResetAKey(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("keyboard")
	settle(tt)
	tt.Type("new tab")
	settle(tt)
	r, _ := tt.Find("Ctrl+Shift+T")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	settle(tt)
	mustClick(t, tt, "Remove Ctrl+Shift+T")
	if len(a.keymap.KeysFor("tab.new")) != 0 || a.set.rec != nil {
		t.Fatalf("keys %v, rec %+v", a.keymap.KeysFor("tab.new"), a.set.rec)
	}
	mustClick(t, tt, "Reset New tab")
	if got := a.keymap.KeysFor("tab.new"); len(got) != 1 {
		t.Fatalf("keys after reset %v", got)
	}
}

func TestSettingsResetAllKeys(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.bindKey("global", chord(t, "ctrl-alt-q"), "app.quit", nil)
	a.openSettings("keyboard")
	settle(tt)
	mustClick(t, tt, "Reset all")
	if !tt.HasText("Reset every key binding?") {
		t.Fatal("no confirmation")
	}
	mustClick(t, tt, "Cancel")
	if _, err := os.Stat(filepath.Join(a.home, "keys.json")); err != nil {
		t.Fatalf("cancelled, keys.json went: %v", err)
	}
	mustClick(t, tt, "Reset all")
	tt.Key(0, ui.KeyEnter) // the confirmation's button has the focus
	settle(tt)
	if _, err := os.Stat(filepath.Join(a.home, "keys.json")); !os.IsNotExist(err) {
		t.Fatalf("keys.json stays: %v", err)
	}
	if len(a.keymap.KeysFor("app.quit")) != 0 || !a.settings {
		t.Fatalf("quit %v, settings %v", a.keymap.KeysFor("app.quit"), a.settings)
	}
}

func TestSettingsShowKeysProblemsAndOtherSections(t *testing.T) {
	home := t.TempDir()
	doc := `{"global":{"ctrl-shift-y":"tab.new","nonsense-???":"tab.close"},"layout:tiles":{"ctrl-j":"layout.select:1"}}`
	if err := os.WriteFile(filepath.Join(home, "keys.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newApp(home, "x")
	tt := uxTester(a)
	a.openSettings("keyboard")
	settle(tt)
	if !tt.HasText("keys.json has problems; the bindings it could read are in use.") || !tt.HasText("layout:tiles") {
		t.Fatalf("texts %q", tt.Texts())
	}
	// A change keeps the sections this app does not use.
	a.bindKey("global", chord(t, "ctrl-alt-q"), "app.quit", nil)
	if km := readKeysFile(t, a); km.Sections["layout:tiles"][chord(t, "ctrl-j")].Spec() != "layout.select:1" {
		t.Fatalf("sections %v", km.Sections)
	}
	settle(tt)
	if tt.HasText("keys.json has problems; the bindings it could read are in use.") {
		t.Fatal("the problems outlived the rewrite")
	}
}

func TestSettingsAddABindingOfAnyAction(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("keyboard")
	a.set.addSection = "terminal"
	settle(tt)
	tt.Scroll(800, 500, 0, 5000)
	settle(tt)
	mustClick(t, tt, "Action")
	tt.Type("send:hi")
	settle(tt)
	if !tt.HasText(`Type "hi"`) {
		t.Fatalf("texts %q", tt.Texts())
	}
	mustClick(t, tt, "Record keys")
	tt.Key(ui.Ctrl|ui.Alt, ui.KeyH)
	settle(tt)
	if act, ok := a.keymap.Lookup("terminal", chord(t, "ctrl-alt-h")); !ok || act.Send != "hi" {
		t.Fatalf("binding %+v %v", act, ok)
	}
}

func TestBindingRowsCoverEveryBinding(t *testing.T) {
	a := newApp(t.TempDir(), "x")
	for section, m := range a.keymap.Sections {
		listed := map[string]bool{}
		for _, r := range a.bindingRows(section) {
			listed[r.spec] = true
		}
		for _, act := range m {
			if !listed[act.Spec()] {
				t.Errorf("%s: %s is not listed", section, act.Spec())
			}
		}
	}
	for _, r := range a.bindingRows("global") {
		if r.spec == "layout.toggle" {
			t.Error("layout.toggle is listed beside layout.toggle:sidebar")
		}
		if r.title == r.spec {
			t.Errorf("%s has no title", r.spec)
		}
	}
}

func TestSettingsCopyOnSelect(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	if a.cfg.copyOnSelect() != terminal.CopyOnSelectClipboard {
		t.Fatal("copying what the mouse selects is off by default")
	}
	a.openSettings("copy on select")
	settle(tt)
	copyKeys := a.keysFor("terminal.copy")
	if copyKeys == "" || !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasSuffix(s, "  "+copyKeys) }) {
		t.Errorf("the row does not name the copy keys %q", copyKeys)
	}
	mustClick(t, tt, "Copy on select")
	if a.cfg.copiesOnSelect() || a.cfg.copyOnSelect() != terminal.CopyOnSelectNone {
		t.Fatal("still copies on select")
	}
	b, err := os.ReadFile(filepath.Join(a.home, "settings.json"))
	if err != nil || !strings.Contains(string(b), `"copy_on_select": false`) {
		t.Fatalf("settings.json %s, %v", b, err)
	}
	mustClick(t, tt, "Copy selected text") // the switch itself
	if !a.cfg.copiesOnSelect() || a.cfg.CopyOnSelect != nil {
		t.Fatalf("copy on select %v, stored %v", a.cfg.copiesOnSelect(), a.cfg.CopyOnSelect)
	}
}

func TestCopiedToast(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.notify("Settings reloaded")
	a.termCopied("x", terminal.CopyProgram)
	settle(tt)
	if tt.HasText("Copied") {
		t.Fatal("a program's copy says Copied")
	}
	a.termCopied("x", terminal.CopySelect)
	a.termCopied("y", terminal.CopyUser)
	settle(tt)
	n := 0
	for _, s := range tt.Texts() {
		if s == "Copied" {
			n++
		}
	}
	if n != 1 || !tt.HasText("Settings reloaded") {
		t.Fatalf("%d Copied toasts; texts %q", n, tt.Texts())
	}
	// It passes before the others.
	for i := range a.toasts {
		a.toasts[i].until = a.toasts[i].until.Add(-briefToast)
	}
	settle(tt)
	if tt.HasText("Copied") || !tt.HasText("Settings reloaded") {
		t.Fatalf("texts %q", tt.Texts())
	}
}
