package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// The Mods page of the settings: every mod of the mods folder, what it
// runs and adds, and the switch that turns it on. It is where a mod is
// looked at before it runs, so it shows what a mod would do as plainly
// as what it does.

// modTrouble is what is wrong with the mods, for the page's mark in the
// settings' list, or "".
func (a *App) modTrouble() string {
	for _, id := range a.modOrder {
		st := a.mods[id]
		switch {
		case st.Err != nil && st.Hash != "":
			return "A mod does not load"
		case st.svc != nil && st.svc.state == svcCrashed:
			return "A mod's service crashed"
		}
	}
	return ""
}

// modStateWord is a mod's state for people, and whether it is a problem.
func (a *App) modStateWord(st *modState) (string, bool) {
	switch {
	case st.Err != nil:
		return "Does not load", true
	case st.manifestChanged(a.cfg) && !st.on:
		return "Changed", true
	case !st.on:
		return "Off", false
	case st.svc == nil || st.svc.state == svcStopped:
		if len(st.Manifest.Run) > 0 {
			return "On · service idle", false
		}
		return "On", false
	}
	switch st.svc.state {
	case svcRunning:
		return "On · running", false
	case svcStarting:
		return "On · starting", false
	case svcRestarting:
		return "On · restarting", true
	case svcCrashed:
		return "Crashed", true
	}
	return "On", false
}

func (a *App) modsPage(c *ui.Context, k *colors) {
	dir := a.modsDir()
	settingsGroup(c, k, "Mods", "Mods add commands, keys and handlers of the window's events, with programs that run outside it. They run as you, with everything you can reach: turn on the ones you trust.", func() {
		settingRow(c, k, "Folder", tildePath(dir)+": a folder per mod, with its mod.json. Shiori reads it as it changes.", func() {
			if ui.Button(c, "Copy path").Clicked() {
				c.WriteClipboard(dir)
				a.post(func() { a.notify("Copied " + tildePath(dir)) })
			}
			if ui.Button(c, "Open").Clicked() {
				c.OpenURL("file://" + dir)
			}
		})
	})
	if len(a.modOrder) == 0 {
		ui.Column(c).Padding(28, 16).Gap(6).AlignItems(ui.Center).Radius(10).Border(1, k.border).Children(func() {
			ui.Icon(c, glyph("puzzle")).Size(26, 26).TextColor(k.dim)
			ui.Text(c, "No mods yet").FontSize(ts(13)).FontWeight(600).TextColor(k.foreground)
			ui.Text(c, "Put a mod's folder in the mods folder; examples/mods/README.md in Shiori's repository says what goes in it.").FontSize(ts(12)).TextColor(k.muted)
		})
		return
	}
	for _, id := range a.modOrder {
		a.modCard(c, k, a.mods[id])
	}
	fileNote(c, k, `settings.json  "mods": {"<id>": {"enabled": …, "manifest": "sha256:…"}}`)
}

func (a *App) modCard(c *ui.Context, k *colors, st *modState) {
	id := st.ID
	name := id
	if st.Manifest != nil {
		name = st.Manifest.Name
	}
	word, bad := a.modStateWord(st)
	ui.Column(c.Key("mod:"+id)).Padding(14).Gap(10).Radius(10).Background(k.raised).Border(1, k.border).Shrink(0).Label("Mod " + name).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, glyph("puzzle")).Size(16, 16).TextColor(k.muted).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, name).FontSize(ts(13.5)).FontWeight(600).TextColor(k.foreground).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
					if st.Manifest != nil && st.Manifest.Version != "" {
						ui.Text(c, st.Manifest.Version).FontSize(ts(11.5)).TextColor(k.dim).SingleLine()
					}
					col, bg := k.muted, k.hover
					if bad {
						col, bg = k.blocked, k.blocked.Alpha(0.14)
					} else if st.on {
						col, bg = k.done, k.done.Alpha(0.14)
					}
					ui.Box(c).Padding(1, 7).Radius(9).Background(bg).Shrink(0).Children(func() {
						ui.Text(c, word).FontSize(ts(11)).FontWeight(600).TextColor(col).SingleLine()
					})
				})
				ui.Text(c, id).FontSize(ts(11)).TextColor(k.dim).Font("monospace")
			})
			on := st.on
			sw := ui.Switch(c, &on).Label("Turn " + name + " on").Disabled(st.Err != nil)
			if st.Err == nil {
				sw.Tooltip(map[bool]string{true: "Turn it off", false: "Turn it on"}[st.on])
			}
			if on != st.on {
				a.post(func() {
					if err := a.setModEnabled(id, on); err != nil {
						a.notifyLevel(err.Error(), levelError)
					}
				})
			}
		})
		if st.Err != nil {
			ui.Text(c, st.Err.Error()).FontSize(ts(12)).TextColor(k.blocked).LineHeight(1.35)
			a.modButtons(c, k, st)
			return
		}
		m := st.Manifest
		if m.Description != "" {
			ui.Text(c, m.Description).FontSize(ts(12.5)).TextColor(k.muted).LineHeight(1.35)
		}
		if st.manifestChanged(a.cfg) && !st.on {
			ui.Text(c, "Its manifest changed since it was turned on. Look at what it does now, then turn it on again.").
				FontSize(ts(12)).TextColor(k.blocked).LineHeight(1.35)
		}
		a.modDetails(c, k, st)
		if st.svc != nil && st.svc.err != "" && st.svc.state != svcRunning {
			ui.Text(c, "Its service: "+st.svc.err).FontSize(ts(12)).TextColor(k.blocked).LineHeight(1.35)
		}
		a.modButtons(c, k, st)
		if a.set.logs[id] {
			log := modLogTail(st, 200)
			if log == "" {
				log = "Nothing yet."
			}
			ui.Scroll(c).MaxHeight(220).Padding(8, 10).Radius(8).Background(k.background).Border(1, k.border).Children(func() {
				ui.Text(c, log).FontSize(ts(11.5)).Font("monospace").TextColor(k.terminalForeground).Selectable()
			})
		}
	})
}

// modDetails lists what a mod runs and adds.
func (a *App) modDetails(c *ui.Context, k *colors, st *modState) {
	m := st.Manifest
	line := func(label string, fn func()) {
		ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
			ui.Text(c, label).FontSize(ts(11.5)).FontWeight(600).TextColor(k.dim).Width(74).Shrink(0).Padding(3, 0, 0, 0)
			ui.Row(c).Grow(1).MinWidth(0).Gap(6).Wrap().AlignItems(ui.Center).Children(fn)
		})
	}
	chip := func(text string, mono bool) {
		ui.Box(c).Padding(2, 7).Radius(6).Background(k.hover).Children(func() {
			t := ui.Text(c, text).FontSize(ts(11.5)).TextColor(k.foreground)
			if mono {
				t.Font("monospace")
			}
		})
	}
	if len(m.Run) > 0 {
		line("Service", func() {
			chip(strings.Join(m.Run, " "), true)
			when := "when first needed"
			if m.StartsAtStartup() {
				when = "with the window"
			}
			ui.Text(c, "starts "+when).FontSize(ts(11.5)).TextColor(k.muted)
		})
	}
	if len(m.Commands) > 0 {
		line("Commands", func() {
			for _, mc := range m.Commands {
				label := mc.Title
				if len(mc.Run) > 0 {
					label += "  ·  " + strings.Join(mc.Run, " ")
				}
				for _, ks := range a.keymap.PrefixKeysFor(st.ID + "." + mc.Name) {
					label += "  ·  " + ks
				}
				chip(label, false)
			}
		})
	}
	if len(m.Events) > 0 {
		line("Events", func() {
			for _, e := range m.Events {
				chip(e, true)
			}
		})
	}
	if km, err := m.Keymap(st.ID); err == nil && !km.Empty() {
		line("Keys", func() {
			var all []string
			for sec, binds := range km.Sections {
				for ch, act := range binds {
					all = append(all, fmt.Sprintf("%s → %s%s", ch, a.actionTitle(sec, act.Spec()), map[bool]string{true: "", false: " (" + sec + ")"}[sec == "global"]))
				}
			}
			slices.Sort(all)
			for _, s := range all {
				chip(s, false)
			}
		})
	}
	for sec, chords := range st.taken {
		for _, ch := range chords {
			other := ""
			if act, ok := a.keysBase.Lookup(sec, ch); ok {
				other = a.actionTitle(sec, act.Spec())
			}
			ui.Text(c, fmt.Sprintf("%s is %s already, so the mod's binding of it is not used.", ch, other)).FontSize(ts(11.5)).TextColor(k.muted)
		}
	}
}

// modButtons are what can be done with a mod.
func (a *App) modButtons(c *ui.Context, k *colors, st *modState) {
	id := st.ID
	ui.Row(c).Gap(8).Wrap().Children(func() {
		if st.on && len(st.Manifest.Run) > 0 {
			if ui.Button(c, "Restart").Label("Restart " + id).Tooltip("Start its service afresh").Clicked() {
				a.post(func() {
					if st := a.mods[id]; st != nil && st.on {
						a.restartService(st)
					}
				})
			}
		}
		if st.Manifest != nil && len(st.Manifest.Commands) > 0 && st.on {
			if ui.Button(c, "Keys").Label("Keys of " + id).Tooltip("Its commands on the Keyboard page").Clicked() {
				a.set.section, a.set.query = "keyboard", a.modName(id)
			}
		}
		showing := a.set.logs[id]
		if ui.Button(c, map[bool]string{true: "Hide log", false: "Show log"}[showing]).Label("Log of " + id).Clicked() {
			if a.set.logs == nil {
				a.set.logs = map[string]bool{}
			}
			a.set.logs[id] = !showing
		}
		if ui.Button(c, "Open folder").Label("Folder of " + id).Clicked() {
			c.OpenURL("file://" + st.Dir)
		}
	})
}
