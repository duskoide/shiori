package app

import (
	"strings"
	"testing"
)

func modsPageApp(t *testing.T) *App {
	t.Helper()
	a, _ := uxApp(t)
	writeMod(t, a.home, "tools", toolsManifest, toolsScripts)
	writeMod(t, a.home, "broken", `{"api":1,"name":"Broken","commands":[{"name":"x","title":"X"}]}`, nil)
	a.reloadMods()
	return a
}

func TestNewModsAreToldOfTogether(t *testing.T) {
	a, _ := uxApp(t)
	writeMod(t, a.home, "one", `{"api":1,"name":"One","commands":[{"name":"x","title":"X","run":["true"]}]}`, nil)
	writeMod(t, a.home, "two", `{"api":1,"name":"Two","commands":[{"name":"x","title":"X","run":["true"]}]}`, nil)
	a.reloadMods()
	if len(a.toasts) != 1 || a.toasts[0].text != `New mods "One", "Two": turn them on in Settings → Mods` {
		t.Fatalf("toasts %v", a.toasts)
	}
}

func TestModsPageTurnsAModOn(t *testing.T) {
	a := modsPageApp(t)
	tt := uxTester(a)
	a.openSettings("mods")
	settle(tt)
	if !tt.HasText("Off") || !tt.HasText("Does not load") || !tt.HasText(`command "x" has no "run", and the mod has no service to run it`) {
		t.Fatalf("texts %q", tt.Texts())
	}
	// What it would do shows before it runs.
	if !tt.HasText("Tools: where  ·  ./where.sh") {
		t.Fatalf("texts %q", tt.Texts())
	}
	mustClick(t, tt, "Turn Tools on")
	if !a.mods["tools"].on || !tt.HasText("Tools: where  ·  ./where.sh  ·  Ctrl+Alt+W") {
		t.Fatalf("on %v; texts %q", a.mods["tools"].on, tt.Texts())
	}
	if !tt.HasText("Ctrl+Shift+T is New tab already, so the mod's binding of it is not used.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	// A mod that does not load does not turn on.
	mustClick(t, tt, "Turn broken on")
	if a.mods["broken"].on {
		t.Fatal("a broken mod turned on")
	}
	mustClick(t, tt, "Keys of tools")
	if a.set.section != "keyboard" || a.set.query != "Tools" || !tt.HasText("Tools: where") {
		t.Fatalf("section %q query %q", a.set.section, a.set.query)
	}
}

func TestModsPageShowsTheLog(t *testing.T) {
	a := modsPageApp(t)
	a.setModEnabled("tools", true)
	runMod(t, a, "tools.fail", nil)
	tt := uxTester(a)
	a.openSettings("mods")
	settle(tt)
	mustClick(t, tt, "Log of tools")
	found := false
	for _, s := range tt.Texts() {
		found = found || strings.Contains(s, "no repository here")
	}
	if !found {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestModsPageRestartsAService(t *testing.T) {
	a := svcApp(t, "echo", "startup")
	a.startServices()
	pumpUntilTrue(t, a, "the service", func() bool { return a.mods["helper"].svc.state == svcRunning })
	tt := uxTester(a)
	a.openSettings("mods")
	settle(tt)
	if !tt.HasText("On · running") {
		t.Fatalf("texts %q", tt.Texts())
	}
	first := a.mods["helper"].svc.token
	mustClick(t, tt, "Restart helper")
	pumpUntilTrue(t, a, "the restart", func() bool {
		tt.Frame()
		svc := a.mods["helper"].svc
		return svc.state == svcRunning && svc.token != first
	})
}

func TestModsPageWithoutMods(t *testing.T) {
	a, _ := uxApp(t)
	tt := uxTester(a)
	a.openSettings("extensions")
	settle(tt)
	if a.set.section != "mods" || !tt.HasText("No mods yet") {
		t.Fatalf("section %q", a.set.section)
	}
}
