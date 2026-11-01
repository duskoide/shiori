package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/agent"
	"github.com/duskoide/shiori/internal/layout"
)

// sidebar is the brand, the spaces and agents lists, and the footer.
func (a *App) sidebar(c *ui.Context, k *colors) {
	ui.Column(c).Width(a.sidebarW).Shrink(0).MinHeight(0).Background(k.chrome).Children(func() {
		a.brand(c, k)
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Grow(1).Basis(0).MinHeight(90).Children(func() { a.spacesList(c, k) })
			ui.Box(c).Height(1).Background(k.border)
			ui.Column(c).Grow(1).Basis(0).MinHeight(90).Children(func() { a.agentsList(c, k) })
		})
		a.sidebarFooter(c, k)
	})
}

func (a *App) brand(c *ui.Context, k *colors) {
	ui.Row(c).Height(brandHeight).Padding(0, 8, 0, 14).Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Icon(c, glyph("logo")).Size(20, 20).TextColor(k.accent)
		ui.Text(c, "Shiori").FontSize(14).FontWeight(600).TextColor(k.foreground)
		ui.Spacer(c)
		if iconButton(c, k, "search", "Search", 28, 15).Tooltip(withKeys("Search", a.keysFor("mod.open:picker"))).Clicked() {
			a.post(func() { a.doAction("mod.open", "picker") })
		}
		if iconButton(c, k, "sidebar", "Hide sidebar", 28, 15).Tooltip(withKeys("Hide sidebar", a.keysFor("layout.toggle:sidebar"))).Clicked() {
			a.post(func() { a.doAction("layout.toggle", "sidebar") })
		}
	})
}

// sectionHeader is the title of a sidebar list, with a button.
func sectionHeader(c *ui.Context, k *colors, title string, button func()) {
	ui.Row(c).Height(30).Padding(0, 8, 0, 14).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, title).FontSize(11.5).FontWeight(600).TextColor(k.muted)
		ui.Spacer(c)
		if button != nil {
			button()
		}
	})
}

func (a *App) spacesList(c *ui.Context, k *colors) {
	sectionHeader(c, k, "Spaces", func() {
		if iconButton(c, k, "plus", "New space", 24, 14).Tooltip(withKeys("New space", a.keysFor("space.new"))).Clicked() {
			a.post(func() { a.doAction("space.new", "") })
		}
	})
	if a.doc == nil {
		return
	}
	groups := a.spaceGroups()
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, 6, 6, 6).Gap(1).Children(func() {
		for _, m := range groups {
			if len(groups) > 1 {
				a.machineHeader(c, k, m)
			}
			for i, s := range a.doc.Spaces {
				if s.Machine == m {
					a.spaceRow(c, k, i, s)
				}
			}
		}
	})
}

// spaceGroups are the machines the spaces list shows, in its order: this
// one, then the others by name, those without spaces too.
func (a *App) spaceGroups() []string {
	groups := []string{""}
	for _, m := range a.machines {
		groups = append(groups, m.Name)
	}
	for _, m := range a.doc.Machines() {
		if !slices.Contains(groups, m) {
			groups = append(groups, m)
		}
	}
	slices.Sort(groups[1:])
	return groups
}

// machineHeader heads a machine's spaces in the list: its name and state,
// and a button for a new space there.
func (a *App) machineHeader(c *ui.Context, k *colors, name string) {
	m, listed := a.machine(name)
	title, icon, tip := name, "server", ""
	dot := k.done
	switch {
	case name == "":
		title, icon = "This computer", "monitor"
		if !a.connected {
			dot = k.blocked
		}
	case !listed:
		dot, tip = k.dim, "Not listed by herdr-core"
	default:
		word, _ := machineWord(m)
		dot, tip = machineDot(k, m), word
		if m.Why != "" && !m.Usable() {
			tip += ": " + m.Why
		}
	}
	row := ui.Row(c.Key("machine:"+name)).Height(26).Padding(6, 4, 0, 8).Gap(6).AlignItems(ui.Center).Shrink(0).Label("Machine " + title)
	if tip != "" {
		row.Tooltip(tip)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("New space").Disabled(a.machineReady(name) != nil).Chosen() {
			a.post(func() { a.newSpaceOn(name) })
		}
		if m.Item("Machines…").Chosen() {
			a.post(func() { a.openSettings("machines") })
		}
	})
	row.Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(dot).Shrink(0)
		ui.Icon(c, glyph(icon)).Size(12, 12).TextColor(k.dim).Shrink(0)
		ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(k.muted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		ui.Spacer(c)
		plus := iconButton(c, k, "plus", "New space on "+title, 20, 12)
		if err := a.machineReady(name); err != nil {
			plus.Opacity(0.4).Tooltip(err.Error())
		} else {
			plus.Tooltip("New space on " + title)
			if plus.Clicked() {
				a.post(func() { a.newSpaceOn(name) })
			}
		}
	})
}

// newSpaceOn makes a space on a machine ("" this one), saying why not.
func (a *App) newSpaceOn(machine string) {
	v := args{}
	if machine != "" {
		v["machine"] = machine
	}
	if _, err := a.do("space.new", v); err != nil {
		a.notifyLevel(err.Error(), levelError)
	}
}

func (a *App) spaceRow(c *ui.Context, k *colors, i int, s *layout.Space) {
	active := s.ID == a.activeSpace
	label := a.spaceLabel(s)
	row := ui.Row(c.Key(s.ID)).Height(sidebarRowH).Padding(0, 8).Gap(9).Radius(8).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Space " + label).Selected(active)
	if !a.usable(s.Machine) {
		// A machine not connected: its spaces show as they were.
		row.Opacity(0.55).Tooltip(s.Machine + " is not connected")
	}
	if active {
		row.Background(k.selected)
	} else if row.Hovered() {
		row.Background(k.hover)
	}
	id := s.ID
	row.Drag(s)
	if dragged, ok := ui.Drop[*layout.Space](row); ok && dragged != s {
		did := dragged.ID
		a.post(func() { a.do("space.move", args{"id": did, "index": i + 1}) })
	}
	if _, over := ui.DragOver[*layout.Space](row); over {
		row.Border(1, k.accent)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename…").Chosen() {
			a.post(func() { a.do("space.rename", args{"id": id}) })
		}
		if m.Item("New tab").Chosen() {
			a.post(func() { a.do("tab.new", args{"space": id}) })
		}
		m.Separator()
		if m.Item("Close space").Chosen() {
			a.post(func() { a.do("space.close", args{"id": id}) })
		}
	})
	row.Children(func() {
		avatar := k.ansi[spaceColors[i%len(spaceColors)]]
		ui.Box(c).Size(18, 18).Radius(5).Background(avatar).Center().Shrink(0).Children(func() {
			ui.Text(c, initial(label)).FontSize(11).FontWeight(700).TextColor(k.solidForeground)
		})
		ui.Text(c, label).FontSize(12.8).TextColor(k.foreground).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		ui.Spacer(c)
		statusMark(c, k, a.spaceWord(s), false)
		if i < 9 {
			ui.Text(c, fmt.Sprint(i+1)).FontSize(11).TextColor(k.dim)
		}
	})
	if row.Clicked() {
		a.activeSpace = id
		a.focusReq = a.focus[a.activeTab[id]]
	}
	if row.DoubleClicked() {
		a.post(func() { a.do("space.rename", args{"id": id}) })
	}
}

func initial(s string) string {
	for _, r := range s {
		if r == '~' {
			return "~"
		}
		return strings.ToUpper(string(r))
	}
	return "?"
}

func (a *App) agentsList(c *ui.Context, k *colors) {
	list := a.agents()
	a.agentOrder = a.agentOrder[:0]
	for _, e := range list {
		a.agentOrder = append(a.agentOrder, e.rec.Address)
	}
	sectionHeader(c, k, "Agents", nil)
	if len(list) == 0 {
		ui.Text(c, "No agent status yet. Agents show up here when they report their status.").
			FontSize(12).TextColor(k.dim).Padding(4, 14).LineHeight(1.35)
		return
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, 6, 6, 6).Gap(1).Children(func() {
		section := ""
		for i, e := range list {
			name := sectionOf(e)
			if name != section {
				section = name
				ui.Text(c.Key("section:"+name), name).FontSize(11).FontWeight(600).TextColor(k.dim).Padding(8, 8, 3, 8)
			}
			a.agentRow(c, k, i, e)
		}
	})
}

// sectionOf groups the agents list by urgency, pinned agents first.
func sectionOf(e agentEntry) string {
	if agent.Pinned(e.rec.Labels) {
		return "Pinned"
	}
	switch e.word {
	case agent.Blocked:
		return "Needs you"
	case agent.Done:
		return "Done"
	case agent.Working:
		return "Working"
	}
	return "Idle"
}

func (a *App) agentRow(c *ui.Context, k *colors, i int, e agentEntry) {
	current := false
	if rec, ok := a.record(a.pane()); ok {
		current = rec.Address == e.rec.Address
	}
	label := a.spaceLabel(e.space)
	if len(e.space.Tabs) > 1 {
		label += " / " + a.tabLabel(e.tab)
	}
	if e.space.Machine != "" {
		label = e.space.Machine + " · " + label
	}
	row := ui.Row(c.Key(e.pane)).Height(sidebarRowH).Padding(0, 8).Gap(8).Radius(8).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Agent " + e.prog.Name + " " + label)
	if current {
		row.Background(k.selected)
	} else if row.Hovered() {
		row.Background(k.hover)
	}
	addr := e.rec.Address
	pinned := agent.Pinned(e.rec.Labels)
	row.ContextMenu(func(m *ui.Menu) {
		title := "Pin"
		if pinned {
			title = "Unpin"
		}
		if m.Item(title).Chosen() {
			a.post(func() { a.do("terminal.pin", args{"terminal": addr, "pinned": !pinned}) })
		}
	})
	hovered := row.Hovered()
	row.Children(func() {
		programTile(c, k, e.prog, 16)
		ui.Text(c, label).FontSize(12.8).TextColor(k.foreground).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		ui.Spacer(c)
		statusMark(c, k, e.word, true)
		if hovered {
			if iconButton(c, k, "pin", map[bool]string{true: "Unpin", false: "Pin"}[pinned], 20, 12).Clicked() {
				a.post(func() { a.do("terminal.pin", args{"terminal": addr, "pinned": !pinned}) })
			}
		} else if e.rec.StatusMs > 0 {
			ui.Text(c, since(time.UnixMilli(e.rec.StatusMs))).FontSize(11).TextColor(k.dim)
		}
	})
	if row.Clicked() {
		a.post(func() { a.do("agent.focus", args{"terminal": addr}) })
	}
}

// since is how long ago something changed: <1m, 12m, 3h, 2d.
func since(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func (a *App) sidebarFooter(c *ui.Context, k *colors) {
	ui.Column(c).Height(sidebarFooterH).Padding(6).Gap(1).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(k.border).Children(func() {
		if footerRow(c, k, "settings", "Settings").Clicked() {
			a.post(func() { a.doAction("app.settings", "") })
		}
		if footerRow(c, k, "command", "Commands").Clicked() {
			a.post(func() { a.doAction("app.palette", "") })
		}
	})
}

func footerRow(c *ui.Context, k *colors, icon, label string) ui.Element {
	row := ui.Row(c).Height(27).Padding(0, 8).Gap(9).Radius(7).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	if row.Hovered() {
		row.Background(k.hover)
	}
	row.Children(func() {
		ui.Icon(c, glyph(icon)).Size(14, 14).TextColor(k.muted)
		ui.Text(c, label).FontSize(12.8).TextColor(k.foreground)
	})
	return row
}

// statusMark shows an agent status: a spinner while working, a "Needs
// you" pill, or a dot when done.
func statusMark(c *ui.Context, k *colors, w agent.Word, pill bool) {
	switch w {
	case agent.Working:
		ui.Spinner(c).Size(12, 12).TextColor(k.working)
	case agent.Blocked:
		if pill {
			ui.Box(c).Padding(1, 7).Radius(9).Background(k.blocked.Alpha(0.15)).Children(func() {
				ui.Text(c, "Needs you").FontSize(11).FontWeight(600).TextColor(k.blocked)
			})
		} else {
			ui.Box(c).Size(6, 6).Radius(3).Background(k.blocked).Tooltip("Needs you")
		}
	case agent.Done:
		ui.Box(c).Size(6, 6).Radius(3).Background(k.done).Tooltip("Done")
	}
}

// iconButton is a borderless button of one of the interface's icons.
func iconButton(c *ui.Context, k *colors, icon, label string, size, iconSize float32) ui.Element {
	b := ui.Box(c).Size(size, size).Center().Radius(6).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).Shrink(0)
	col := k.muted
	if b.Pressed() {
		b.Background(k.selected)
		col = k.foreground
	} else if b.Hovered() {
		b.Background(k.hover)
		col = k.foreground
	}
	b.Children(func() {
		ui.Icon(c, glyph(icon)).Size(iconSize, iconSize).TextColor(col)
	})
	return b
}

// programTile is a program's icon on its tile.
func programTile(c *ui.Context, k *colors, p programTileInfo, size float32) {
	tile, glyphColor := k.raised, k.foreground
	if p.Tile != "" {
		tile = parseHex(p.Tile, tile)
	}
	if p.Color != "" {
		glyphColor = parseHex(p.Color, glyphColor)
	}
	ui.Box(c).Size(size, size).Radius(size / 4).Background(tile).Center().Shrink(0).Children(func() {
		s := programIcon(p.Icon)
		inner := size * 0.7
		switch {
		case s == nil:
			ui.Text(c, initial(p.Name)).FontSize(size * 0.6).FontWeight(700).TextColor(glyphColor)
		case p.Tint:
			ui.Icon(c, s).Size(inner, inner).TextColor(glyphColor)
		default:
			ui.Image(c, s).Size(inner, inner)
		}
	})
}

func withKeys(label, keys string) string {
	if keys == "" {
		return label
	}
	return label + "  " + keys
}

func parseHex(s string, fallback ui.Color) (c ui.Color) {
	defer func() {
		if recover() != nil {
			c = fallback
		}
	}()
	return ui.Hex(s)
}
