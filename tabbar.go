package main

import (
	"github.com/egoist/mygo/ui"

	"github.com/duskoide/herdr-mygo/internal/layout"
	"github.com/duskoide/herdr-mygo/internal/programs"
)

// programTileInfo is what a program tile shows.
type programTileInfo = programs.Program

// tabBar is the active space's tabs, the new-tab button and the
// window-dragging space beside them.
func (a *App) tabBar(c *ui.Context, k *colors) {
	bar := c.TitleBar()
	ui.Row(c).Height(tabBarHeight).Padding(0, max(bar.Right, 8), 0, 8).Gap(4).AlignItems(ui.Center).Shrink(0).
		Background(k.chrome).BorderWidth(0, 0, 1, 0).BorderColor(k.border).DragWindow().Children(func() {
		if !a.cfg.sidebarShown() {
			if iconButton(c, k, "sidebar", "Show sidebar", 28, 15).Clicked() {
				a.post(func() { a.doAction("layout.toggle", "sidebar") })
			}
		}
		s := a.space()
		if s != nil {
			ui.Row(c).Gap(4).AlignItems(ui.Center).MinWidth(0).Shrink(1).ClipX().Role(ui.RoleTabList).Children(func() {
				for i, t := range s.Tabs {
					a.tabChip(c, k, s, i, t)
				}
			})
		}
		plus := iconButton(c, k, "plus", "New tab", 28, 16).Tooltip(withKeys("New tab", a.keysFor("tab.new")))
		plus.PopoverMenu(func(m *ui.Menu) { a.newMenu(m) })
		ui.Spacer(c)
	})
}

// newMenu is what the + button offers: a terminal, the agents that can
// start, or a space.
func (a *App) newMenu(m *ui.Menu) {
	if m.Item("Terminal").Chosen() {
		a.post(func() { a.do("tab.new", nil) })
	}
	m.Separator()
	for _, p := range programs.Startable() {
		argv := append([]string(nil), p.Run...)
		if m.Item(p.Name).Chosen() {
			a.post(func() { a.do("tab.new", args{"argv": toAny(argv)}) })
		}
	}
	m.Separator()
	if m.Item("New space").Chosen() {
		a.post(func() { a.do("space.new", nil) })
	}
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func (a *App) tabChip(c *ui.Context, k *colors, s *layout.Space, i int, t *layout.Tab) {
	active := t.ID == a.activeTab[s.ID]
	label := a.tabLabel(t)
	if a.zoomed[t.ID] != "" {
		label += " Z"
	}
	chip := ui.Row(c.Key(t.ID)).Height(29).Padding(0, 8, 0, 9).Gap(7).Radius(7).AlignItems(ui.Center).
		Border(1, ui.Color{}).Cursor(ui.CursorPointer).Role(ui.RoleTab).Label("Tab " + label).Selected(active).Shrink(0)
	if active {
		chip.Background(k.raised).Border(1, k.border)
	} else if chip.Hovered() {
		chip.Background(k.hover)
	}
	id := t.ID
	chip.Drag(t)
	if dragged, ok := ui.Drop[*layout.Tab](chip); ok && dragged != t {
		did := dragged.ID
		a.post(func() { a.do("tab.move", args{"id": did, "index": i + 1}) })
	}
	if _, over := ui.DragOver[*layout.Tab](chip); over {
		chip.Border(1, k.accent)
	}
	chip.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename…").Chosen() {
			a.post(func() { a.do("tab.rename", args{"id": id}) })
		}
		if m.Item("New tab").Chosen() {
			a.post(func() { a.do("tab.new", nil) })
		}
		m.Separator()
		if m.Item("Close tab").Chosen() {
			a.post(func() { a.do("tab.close", args{"id": id}) })
		}
	})
	var prog programs.Program
	for _, p := range t.Panes() {
		if p.ID == a.focus[t.ID] {
			if rec, ok := a.record(p); ok {
				prog = programOf(rec)
			}
		}
	}
	chip.Children(func() {
		if prog.Key != "" {
			programTile(c, k, prog, 14)
		}
		col := k.muted
		if active {
			col = k.foreground
		}
		ui.Text(c, label).FontSize(12.8).TextColor(col).SingleLine().Ellipsis("…").MaxWidth(170)
		statusMark(c, k, a.tabWord(t), false)
		if active {
			if iconButton(c, k, "x", "Close tab", 18, 12).Clicked() {
				a.post(func() { a.do("tab.close", args{"id": id}) })
			}
		}
	})
	if chip.Clicked() {
		a.activeTab[s.ID] = id
		a.focusReq = a.focus[id]
	}
	if chip.DoubleClicked() {
		a.post(func() { a.do("tab.rename", args{"id": id}) })
	}
}

// statusBar is the machine, the agents' count and a hint of the palette.
func (a *App) statusBar(c *ui.Context, k *colors) {
	ui.Row(c).Height(statusHeight).Padding(0, 12).Gap(14).AlignItems(ui.Center).Shrink(0).
		Background(k.chrome).BorderWidth(1, 0, 0, 0).BorderColor(k.border).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			dot := k.done
			if !a.connected {
				dot = k.blocked
			}
			ui.Box(c).Size(6, 6).Radius(3).Background(dot)
			ui.Icon(c, glyph("monitor")).Size(13, 13).TextColor(k.muted)
			name := "This computer"
			if !a.connected {
				name += " · disconnected"
			}
			ui.Text(c, name).FontSize(11.5).TextColor(k.muted)
		})
		list := a.agents()
		if len(list) > 0 {
			blocked := 0
			for _, e := range list {
				if statusWord(e.word) == "Needs you" {
					blocked++
				}
			}
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "%d agents", len(list)).FontSize(11.5).TextColor(k.muted)
				if blocked > 0 {
					ui.Textf(c, "· %d needs you", blocked).FontSize(11.5).TextColor(k.blocked)
				}
			})
		}
		ui.Spacer(c)
		if keys := a.keysFor("app.palette"); keys != "" {
			ui.Text(c, keys+"  commands").FontSize(11.5).TextColor(k.dim)
		}
	})
}
