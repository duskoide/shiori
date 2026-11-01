package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/theme"
)

// paletteState is the command palette: every command, and every space
// and tab to go to.
type paletteState struct {
	open     bool
	query    string
	sel      int
	reveal   bool
	switcher bool // opened as the picker: spaces and tabs first
}

type paletteItem struct {
	title, detail, keys string
	run                 func()
}

func (a *App) openPalette(query string) {
	a.palette = paletteState{open: true, query: query, reveal: true}
}

func (a *App) paletteItems() []paletteItem {
	q := strings.ToLower(strings.TrimSpace(a.palette.query))
	match := func(s string) bool { return q == "" || fuzzy(strings.ToLower(s), q) }
	var places, cmds []paletteItem
	if a.doc != nil {
		for _, s := range a.doc.Spaces {
			for i, t := range s.Tabs {
				title := a.spaceLabel(s) + " / " + a.tabLabel(t)
				if !match(title) {
					continue
				}
				tabID := t.ID
				places = append(places, paletteItem{title: title, detail: fmt.Sprintf("tab %d", i+1), run: func() {
					a.do("tab.select", args{"id": tabID})
				}})
			}
		}
	}
	for _, c := range commands {
		if c.hidden || !match(c.Title+" "+c.Name) {
			continue
		}
		name := c.Name
		cmds = append(cmds, paletteItem{title: c.Title, detail: c.Name, keys: a.keysFor(name), run: func() {
			cmd, value, _ := cutColon(name)
			if findCommand(name) != nil {
				cmd, value = name, ""
			}
			a.doAction(cmd, value)
		}})
	}
	if a.palette.switcher {
		return append(places, cmds...)
	}
	return append(cmds, places...)
}

// fuzzy reports whether q's letters appear in s in order.
func fuzzy(s, q string) bool {
	if strings.Contains(s, q) {
		return true
	}
	i := 0
	for _, r := range s {
		if i < len(q) && rune(q[i]) == r {
			i++
		}
	}
	return i == len(q)
}

func (a *App) paletteView(c *ui.Context, k *colors) {
	if !a.palette.open {
		return
	}
	items := a.paletteItems()
	a.palette.sel = min(max(a.palette.sel, 0), max(len(items)-1, 0))
	closeAndRun := func(it paletteItem) {
		a.palette.open = false
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
		a.post(it.run)
	}
	ui.DialogBase(c, &a.palette.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35)).Justify(ui.Start).Padding(64, 0, 0, 0)
		panel.Width(640).MaxHeight(480).Radius(12).Background(k.popover).Border(1, k.border).
			Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.4)).Clip()
		panel.Children(func() {
			ui.Row(c).Padding(10, 14).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
				in := ui.TextInputBase(c, &a.palette.query).Grow(1).FontSize(14).TextColor(k.foreground).
					Placeholder("Type a command, a space or a tab…").Label("Search commands").AutoFocus()
				if in.Changed() {
					a.palette.sel, a.palette.reveal = 0, true
				}
				if in.Shortcut(0, ui.KeyDown) {
					a.palette.sel, a.palette.reveal = min(a.palette.sel+1, len(items)-1), true
				}
				if in.Shortcut(0, ui.KeyUp) {
					a.palette.sel, a.palette.reveal = max(a.palette.sel-1, 0), true
				}
				if in.Submitted() && len(items) > 0 {
					closeAndRun(items[a.palette.sel])
				}
			})
			ui.Scroll(c).MaxHeight(420).Padding(6).Children(func() {
				if len(items) == 0 {
					ui.Text(c, "Nothing matches").FontSize(12.5).TextColor(k.dim).Padding(10)
				}
				for i, it := range items {
					row := ui.Row(c.Key(i)).Height(32).Padding(0, 10).Gap(10).Radius(7).AlignItems(ui.Center).Cursor(ui.CursorPointer)
					if i == a.palette.sel {
						row.Background(k.selected)
						if a.palette.reveal {
							row.ScrollIntoView()
						}
					} else if row.Hovered() {
						row.Background(k.hover)
					}
					if row.Clicked() {
						closeAndRun(it)
					}
					row.Children(func() {
						ui.Text(c, it.title).FontSize(13).TextColor(k.foreground).SingleLine()
						ui.Text(c, it.detail).FontSize(11.5).TextColor(k.dim).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
						ui.Spacer(c)
						if it.keys != "" {
							ui.Box(c).Padding(2, 6).Radius(5).Background(k.raised).Children(func() {
								ui.Text(c, it.keys).FontSize(11).TextColor(k.muted)
							})
						}
					})
				}
			})
			a.palette.reveal = false
		})
	})
	if !a.palette.open {
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	}
}

// promptState asks for a line of text, as a new name.
type promptState struct {
	title, value, placeholder string
	done                      func(string)
	open                      bool
}

func (a *App) askText(title, value, placeholder string, done func(string)) {
	a.prompt = &promptState{title: title, value: value, placeholder: placeholder, done: done, open: true}
}

func (a *App) promptView(c *ui.Context, k *colors) {
	p := a.prompt
	if p == nil {
		return
	}
	ui.DialogBase(c, &p.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35)).Justify(ui.Start).Padding(120, 0, 0, 0)
		panel.Width(420).Padding(16).Gap(12).Radius(12).Background(k.popover).Border(1, k.border).Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.4))
		panel.Children(func() {
			ui.Text(c, p.title).FontSize(14).FontWeight(600).TextColor(k.foreground)
			in := ui.TextInput(c, &p.value).Placeholder(p.placeholder).Label(p.title).AutoFocus()
			if in.Submitted() {
				p.open = false
				value, done := strings.TrimSpace(p.value), p.done
				a.post(func() { done(value) })
			}
			ui.Text(c, "Enter to save, empty to name it after its folder or program, Esc to cancel").FontSize(11.5).TextColor(k.dim)
		})
	})
	if !p.open {
		a.prompt = nil
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	}
}

// confirmState asks before something ends terminals.
type confirmState struct {
	title, detail string
	yes           func()
	open          bool
}

func (a *App) askConfirm(title, detail string, yes func()) {
	a.confirm = &confirmState{title: title, detail: detail, yes: yes, open: true}
}

func (a *App) confirmView(c *ui.Context, k *colors) {
	p := a.confirm
	if p == nil {
		return
	}
	ui.DialogBase(c, &p.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35)).Justify(ui.Start).Padding(120, 0, 0, 0)
		panel.Width(400).Padding(16).Gap(12).Radius(12).Background(k.popover).Border(1, k.border).Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.4))
		panel.Children(func() {
			ui.Text(c, p.title).FontSize(14).FontWeight(600).TextColor(k.foreground)
			ui.Text(c, p.detail).FontSize(12.5).TextColor(k.muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					p.open = false
				}
				yes := ui.PrimaryButton(c, "Close").AutoFocus()
				if yes.Clicked() {
					p.open = false
					a.post(p.yes)
				}
			})
		})
	})
	if !p.open {
		a.confirm = nil
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	}
}

// settingsView picks the theme and the terminals' text size.
func (a *App) settingsView(c *ui.Context, k *colors) {
	if !a.settings {
		return
	}
	ui.DialogBase(c, &a.settings, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35))
		panel.Width(460).Padding(18).Gap(14).Radius(12).Background(k.popover).Border(1, k.border).Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.4))
		panel.Children(func() {
			ui.Text(c, "Settings").FontSize(15).FontWeight(600).TextColor(k.foreground)
			ui.Text(c, "Theme").FontSize(12).FontWeight(600).TextColor(k.muted)
			ui.Row(c).Gap(6).Wrap().Children(func() {
				current := a.cfg.Theme
				if current == "" {
					current = "graphite"
				}
				for _, name := range theme.PresetNames {
					chip := ui.Row(c.Key(name)).Padding(5, 10).Radius(7).Border(1, k.border).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Theme " + name)
					if name == current {
						chip.Background(k.selected).Border(1, k.accent)
					} else if chip.Hovered() {
						chip.Background(k.hover)
					}
					chip.Children(func() { ui.Text(c, name).FontSize(12.5).TextColor(k.foreground) })
					if chip.Clicked() {
						a.post(func() { a.do("app.theme", args{"name": name}) })
					}
				}
			})
			ui.Text(c, "Terminal text size").FontSize(12).FontWeight(600).TextColor(k.muted)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "−").Label("Smaller text").Clicked() {
					a.post(func() { a.zoomBy(-1) })
				}
				ui.Textf(c, "%g", a.cfg.fontSize()).FontSize(13).TextColor(k.foreground).Width(36)
				if ui.Button(c, "+").Label("Larger text").Clicked() {
					a.post(func() { a.zoomBy(1) })
				}
				if ui.Button(c, "Reset").Clicked() {
					a.post(func() { a.zoomBy(0) })
				}
			})
			ui.Text(c, "Keys: "+a.home+"/keys.json (herdr-app's format)").FontSize(11.5).TextColor(k.dim)
		})
	})
	if !a.settings {
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	}
}
