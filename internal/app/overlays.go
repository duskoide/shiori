package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// paletteState is the command palette: every command, and every space
// and tab to go to.
type paletteState struct {
	open     bool
	query    string
	sel      int
	reveal   bool
	switcher bool       // opened as the picker: spaces and tabs first
	pick     *pickState // opened by app.pick: its items alone
}

// pickState is the palette as app.pick shows it: a list to choose from.
type pickState struct {
	title, placeholder string
	items              []pickItem
	done               func(id string) // with the id chosen, or "" when none was
}

type pickItem struct{ id, title, detail string }

// paletteRows is how many rows Page Up and Page Down move in the palette.
const paletteRows = 12

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
	if pk := a.palette.pick; pk != nil {
		var out []paletteItem
		for _, it := range pk.items {
			if match(it.title + " " + it.detail) {
				id := it.id
				out = append(out, paletteItem{title: it.title, detail: it.detail, run: func() { pk.done(id) }})
			}
		}
		return out
	}
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
	for _, c := range a.allCommands() {
		if c.hidden || !match(c.Title+" "+c.Name+" "+c.Mod) {
			continue
		}
		name := c.Name
		cmds = append(cmds, paletteItem{title: c.Title, detail: c.Name, keys: a.keysFor(name), run: func() {
			cmd, value, _ := cutColon(name)
			if a.findCommand(name) != nil {
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
	pick := a.palette.pick
	closeAndRun := func(it paletteItem) {
		a.palette.open, a.palette.pick = false, nil
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
		a.post(it.run)
	}
	placeholder := "Type a command, a space or a tab…"
	if pick != nil {
		placeholder = pick.placeholder
		if placeholder == "" {
			placeholder = "Type to filter…"
		}
	}
	ui.DialogBase(c, &a.palette.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35)).Justify(ui.Start).Padding(64, 0, 0, 0)
		panel.Width(640).MaxHeight(480).Radius(12).Background(k.popover).Border(1, k.border).
			Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.4)).Clip()
		panel.Children(func() {
			if pick != nil && pick.title != "" {
				ui.Text(c, pick.title).FontSize(ts(12)).FontWeight(600).TextColor(k.muted).Padding(10, 14, 0, 14)
			}
			ui.Row(c).Padding(10, 14).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
				label := "Search commands"
				if pick != nil {
					label = "Filter " + pick.title
				}
				in := ui.TextInputBase(c, &a.palette.query).Grow(1).FontSize(ts(14)).TextColor(k.foreground).
					Placeholder(placeholder).Label(label).AutoFocus()
				if in.Changed() {
					a.palette.sel, a.palette.reveal = 0, true
				}
				// Up and Down go round; Page Up and Page Down move a page.
				move := func(d int, wrap bool) {
					n := len(items)
					if n == 0 {
						return
					}
					sel := a.palette.sel + d
					if wrap {
						sel = (sel%n + n) % n
					}
					a.palette.sel, a.palette.reveal = min(max(sel, 0), n-1), true
				}
				if in.Shortcut(0, ui.KeyDown) {
					move(1, true)
				}
				if in.Shortcut(0, ui.KeyUp) {
					move(-1, true)
				}
				if in.Shortcut(0, ui.KeyPageDown) {
					move(paletteRows, false)
				}
				if in.Shortcut(0, ui.KeyPageUp) {
					move(-paletteRows, false)
				}
				if in.Submitted() && len(items) > 0 {
					closeAndRun(items[a.palette.sel])
				}
			})
			ui.Scroll(c).MaxHeight(420).Padding(6).Children(func() {
				if len(items) == 0 {
					ui.Text(c, "Nothing matches").FontSize(ts(12.5)).TextColor(k.dim).Padding(10)
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
						ui.Text(c, it.title).FontSize(ts(13)).TextColor(k.foreground).SingleLine()
						if it.detail != "" {
							ui.Text(c, it.detail).FontSize(ts(11.5)).TextColor(k.dim).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
						}
						ui.Spacer(c)
						if it.keys != "" {
							ui.Box(c).Padding(2, 6).Radius(5).Background(k.raised).Children(func() {
								ui.Text(c, it.keys).FontSize(ts(11)).TextColor(k.muted)
							})
						}
					})
				}
			})
			a.palette.reveal = false
			// The keys that toggle the palette or open the picker work in
			// it: behind it, in the window, they are inert.
			if pick == nil {
				a.shortcuts(c)
			}
		})
	})
	if !a.palette.open {
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
		if a.palette.pick != nil {
			// Closed without a choice.
			pk := a.palette.pick
			a.palette.pick = nil
			a.post(func() { pk.done("") })
		}
	}
}

// promptState asks for a line of text, as a new name.
type promptState struct {
	title, value, placeholder, hint string
	done                            func(string)
	cancel                          func() // when closed without an answer, if set
	open, answered                  bool
}

// askText asks for a line of text: title heads the dialog, value is the
// text to start from, placeholder shows while it is empty and hint says
// what an empty answer does.
func (a *App) askText(title, value, placeholder, hint string, done func(string)) {
	a.prompt = &promptState{title: title, value: value, placeholder: placeholder, hint: hint, done: done, open: true}
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
			ui.Text(c, p.title).FontSize(ts(14)).FontWeight(600).TextColor(k.foreground)
			submit := func() {
				p.open, p.answered = false, true
				value, done := strings.TrimSpace(p.value), p.done
				a.post(func() { done(value) })
			}
			in := ui.TextInput(c, &p.value).Placeholder(p.placeholder).Label(p.title).AutoFocus()
			if in.Submitted() {
				submit()
			}
			if p.hint != "" {
				ui.Text(c, p.hint).FontSize(ts(11.5)).TextColor(k.dim).LineHeight(1.35)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Tooltip("Esc").Clicked() {
					p.open = false
				}
				if ui.PrimaryButton(c, "Save").Tooltip("Enter").Clicked() {
					submit()
				}
			})
		})
	})
	if !p.open {
		a.prompt = nil
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
		if !p.answered && p.cancel != nil {
			a.post(p.cancel)
		}
	}
}

// confirmState asks before something ends terminals.
type confirmState struct {
	title, detail, yesLabel string
	yes                     func()
	no                      func() // when closed without yes, if set
	open, answered          bool
}

func (a *App) askConfirm(title, detail string, yes func()) {
	a.confirm = &confirmState{title: title, detail: detail, yesLabel: "Close", yes: yes, open: true}
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
			ui.Text(c, p.title).FontSize(ts(14)).FontWeight(600).TextColor(k.foreground)
			if p.detail != "" {
				ui.Text(c, p.detail).FontSize(ts(12.5)).TextColor(k.muted).LineHeight(1.35)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					p.open = false
				}
				yes := ui.PrimaryButton(c, p.yesLabel).AutoFocus()
				if yes.Clicked() {
					p.open, p.answered = false, true
					a.post(p.yes)
				}
			})
		})
	})
	if !p.open {
		a.confirm = nil
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
		if !p.answered && p.no != nil {
			a.post(p.no)
		}
	}
}
