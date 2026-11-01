package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/agent"
	"github.com/duskoide/shiori/internal/layout"
	"github.com/duskoide/shiori/internal/programs"
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
		// A split button: + opens a terminal tab at once, the chevron
		// (or a right click on +) offers the agents and a space.
		ui.Row(c).Gap(0).AlignItems(ui.Center).Shrink(0).Children(func() {
			plus := iconButton(c, k, "plus", "New tab", 28, 16).Tooltip(withKeys("New tab", a.keysFor("tab.new")))
			plus.PopoverMenu(func(m *ui.Menu) { a.newMenu(m) })
			if plus.Clicked() {
				a.post(func() { a.doAction("tab.new", "") })
			}
			more := iconButton(c, k, "chevron-down", "New…", 16, 12).Height(28).Tooltip("Start an agent or a space")
			more.PopoverMenuButton(func(m *ui.Menu) { a.newMenu(m) })
		})
		ui.Spacer(c)
		label := "Show changes"
		if a.cfg.changesShown() {
			label = "Hide changes"
		}
		ch := iconButton(c, k, "diff", label, 28, 15).Tooltip(withKeys(label, a.keysFor("layout.toggle:changes")))
		if a.cfg.changesShown() {
			ch.Background(k.selected)
		}
		if ch.Clicked() {
			a.post(a.toggleChanges)
		}
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
	zoomed := a.zoomed[t.ID] != ""
	chip := ui.Row(c.Key(t.ID)).Height(29).Padding(0, 4, 0, 9).Gap(7).Radius(7).AlignItems(ui.Center).
		Border(1, ui.Color{}).Cursor(ui.CursorPointer).Role(ui.RoleTab).Label("Tab " + label).Selected(active).Shrink(0)
	hovered := chip.Hovered()
	if active {
		chip.Background(k.raised).Border(1, k.border)
	} else if hovered {
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
		if zoomed {
			ui.Icon(c, glyph("maximize")).Size(11, 11).TextColor(k.accent).Tooltip("A pane of this tab is zoomed")
		}
		statusMark(c, k, a.tabWord(t), false)
		// The close button keeps its place on every tab, so that tabs do
		// not shift as the pointer moves over them; it shows on the active
		// tab and the one under the pointer.
		show := active || hovered
		closeLabel := ""
		if show {
			closeLabel = "Close tab"
		}
		x := iconButton(c, k, "x", closeLabel, 18, 12).Opacity(map[bool]float32{true: 1, false: 0}[show]).IgnoreInput(!show)
		if show {
			x.Tooltip(withKeys("Close tab", a.keysFor("tab.close")))
		}
		if x.Clicked() {
			a.post(func() { a.do("tab.close", args{"id": id}) })
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
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
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
			ui.Text(c, name).FontSize(11.5).TextColor(k.muted).SingleLine()
		})
		if len(a.machines) > 0 {
			var tips []string
			row := ui.Row(c).Gap(5).AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Machines")
			row.Children(func() {
				ui.Icon(c, glyph("server")).Size(13, 13).TextColor(k.muted)
				for _, m := range a.machines {
					ui.Box(c.Key("mdot:"+m.Name)).Size(6, 6).Radius(3).Background(machineDot(k, m))
					word, _ := machineWord(m)
					tips = append(tips, m.Name+": "+word)
				}
				ui.Text(c, plural(len(a.machines), "machine", "machines")).FontSize(11.5).TextColor(k.muted).SingleLine()
			})
			row.Tooltip(strings.Join(tips, "\n"))
			if row.Clicked() {
				a.post(func() { a.openSettings("machines") })
			}
		}
		list := a.agents()
		if len(list) > 0 {
			var waiting []string // the panes of the agents that need the person
			for _, e := range list {
				if e.word == agent.Blocked {
					waiting = append(waiting, e.pane)
				}
			}
			ui.Row(c).Gap(4).AlignItems(ui.Center).MinWidth(0).Shrink(1).ClipX().Children(func() {
				ui.Text(c, plural(len(list), "agent", "agents")).FontSize(11.5).TextColor(k.muted).SingleLine()
				if n := len(waiting); n > 0 {
					verb := "need"
					if n == 1 {
						verb = "needs"
					}
					need := ui.Text(c, fmt.Sprintf("· %d %s you", n, verb)).FontSize(11.5).TextColor(k.blocked).SingleLine().
						Cursor(ui.CursorPointer).Role(ui.RoleButton).Tooltip("Go to the agent that needs you")
					if need.Clicked() {
						pane := waiting[0]
						a.post(func() { a.show(pane) })
					}
				}
			})
		}
		ui.Spacer(c)
		if v, ok := a.newerRelease(); ok {
			up := ui.Row(c).Gap(5).AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer).Role(ui.RoleButton).
				Label("Update to " + v).Tooltip("A newer Shiori is out: see Settings → About")
			up.Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(k.accent)
				ui.Text(c, "Update to "+v).FontSize(11.5).TextColor(k.accent).SingleLine()
			})
			if up.Clicked() {
				a.post(func() { a.openSettings("about") })
			}
		}
		if keys := a.keysFor("app.palette"); keys != "" {
			hint := ui.Text(c, keys+"  commands").FontSize(11.5).TextColor(k.dim).SingleLine().Shrink(0).
				Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Command palette")
			if hint.Hovered() {
				hint.TextColor(k.muted)
			}
			if hint.Clicked() {
				a.post(func() { a.doAction("app.palette", "") })
			}
		}
	})
}
