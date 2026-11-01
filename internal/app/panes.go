package app

import (
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/layout"
)

// tabContent lays out a tab's panes, or the one zoomed.
func (a *App) tabContent(c *ui.Context, k *colors, t *layout.Tab) {
	if z := a.zoomed[t.ID]; z != "" {
		for _, p := range t.Panes() {
			if p.ID == z {
				a.paneCard(c, k, t, p, true).Grow(1)
				return
			}
		}
	}
	if t.Root == nil {
		return
	}
	lone := len(t.Panes()) == 1
	a.node(c, k, t, t.Root, lone).Grow(1)
}

// fixedPx is the size in pixels of a node that has only that: it keeps it,
// and is no share of its container.
func fixedPx(n *layout.Node) (float32, bool) {
	if s := n.Size; s != nil && (s.Weight == nil || *s.Weight <= 0) && s.Px != nil && *s.Px > 0 {
		return float32(*s.Px), true
	}
	return 0, false
}

// weight is a node's share of its container; a node of fixed size has none.
func weight(n *layout.Node) float32 {
	if _, ok := fixedPx(n); ok {
		return 0
	}
	if n.Size != nil && n.Size.Weight != nil && *n.Size.Weight > 0 {
		return float32(*n.Size.Weight)
	}
	return 1
}

// setWeight sets the share of a node (not one of fixed size); a size in
// pixels it also has stays.
func setWeight(n *layout.Node, w float32) {
	v := float64(w)
	if n.Size == nil {
		n.Size = &layout.Size{}
	}
	n.Size.Weight = &v
}

// node lays out a node of a tab's tree: a row or column of children with
// dividers between them, or a pane.
func (a *App) node(c *ui.Context, k *colors, t *layout.Tab, n *layout.Node, lone bool) ui.Element {
	switch n.Type {
	case "row", "column":
	case "stack":
		// A stack shows one child; this layout keeps none, so show the
		// first.
		if len(n.Children) > 0 {
			return a.node(c, k, t, n.Children[0], lone)
		}
		return ui.Box(c.Key(n.ID))
	default:
		return a.paneCard(c, k, t, n, lone)
	}
	vertical := n.Type == "column"
	var box ui.Element
	if vertical {
		box = ui.Column(c.Key(n.ID))
	} else {
		box = ui.Row(c.Key(n.ID))
	}
	box.MinWidth(0).MinHeight(0).AlignItems(ui.Stretch)
	bounds := box.Bounds()
	children := append([]*layout.Node(nil), n.Children...)
	var total, fixed float32
	for _, ch := range children {
		total += weight(ch)
		fixed += func() float32 { px, _ := fixedPx(ch); return px }()
	}
	box.Children(func() {
		for i, ch := range children {
			if i > 0 {
				a.divider(c, k, vertical, bounds, total, fixed, len(children), children[i-1], ch)
			}
			el := a.node(c, k, t, ch, lone)
			if px, ok := fixedPx(ch); ok {
				el.Basis(px).Grow(0).Shrink(0)
			} else {
				el.Grow(weight(ch)).Basis(0)
			}
			el.MinWidth(0).MinHeight(0)
		}
	})
	return box
}

// divider sits between two siblings; dragging it moves weight between
// them. Next to a node of fixed size it does nothing: that size stays.
func (a *App) divider(c *ui.Context, k *colors, vertical bool, bounds ui.Rect, total, fixed float32, count int, before, after *layout.Node) {
	div := ui.Box(c.Key("div:" + after.ID)).Role(ui.RoleSplitter).Label("Divider")
	if vertical {
		div.Height(paneGap).Cursor(ui.CursorResizeRow)
	} else {
		div.Width(paneGap).Cursor(ui.CursorResizeColumn)
	}
	if div.Dragging() {
		a.dragging = true
	}
	_, fixedBefore := fixedPx(before)
	_, fixedAfter := fixedPx(after)
	movable := !fixedBefore && !fixedAfter
	if dx, dy, ok := div.Dragged(); ok && movable {
		span := bounds.W
		d := dx
		if vertical {
			span, d = bounds.H, dy
		}
		span -= float32(count-1)*paneGap + fixed
		if span > 0 && total > 0 {
			dw := d / span * total
			wb, wa := weight(before), weight(after)
			floor := total * 0.06
			dw = min(max(dw, floor-wb), wa-floor)
			setWeight(before, wb+dw)
			setWeight(after, wa-dw)
			a.changed()
		}
	}
	if div.DoubleClicked() && movable {
		sum := weight(before) + weight(after)
		setWeight(before, sum/2)
		setWeight(after, sum/2)
		a.changed()
	}
	if div.Hovered() || div.Dragging() {
		div.Draw(func(p *ui.Painter, r ui.Rect) {
			if vertical {
				p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H/2 - 1, W: r.W, H: 2}, k.accent.Alpha(0.6), 1)
			} else {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 1, Y: r.Y, W: 2, H: r.H}, k.accent.Alpha(0.6), 1)
			}
		})
	}
}

// paneCard draws a pane: its header over its terminal.
func (a *App) paneCard(c *ui.Context, k *colors, t *layout.Tab, p *layout.Node, lone bool) ui.Element {
	focused := a.focus[t.ID] == p.ID
	card := ui.Column(c.Key(p.ID)).Radius(paneRadius).Clip().MinWidth(0).MinHeight(0).Background(k.background)
	border := k.border
	if focused && !lone {
		border = k.accent
	}
	card.Border(1, border)
	hovered := card.Hovered()
	paneID := p.ID
	card.Children(func() {
		a.paneHeader(c, k, t, p, focused || hovered)
		body := ui.Box(c).Grow(1).MinHeight(0)
		body.Children(func() {
			if p.Content == nil || p.Content.Terminal == "" {
				ui.Text(c, "This pane shows a panel of herdr-app ("+a.paneName(p)+"), which this app does not draw.").
					FontSize(12).TextColor(k.dim).Padding(12)
				return
			}
			pt := a.termFor(c, p.Content.Terminal)
			if pt == nil {
				msg := "Attaching…"
				if a.client == nil {
					msg = "Waiting for herdr-core… The terminal keeps running there."
				}
				ui.Text(c, msg).FontSize(12).TextColor(k.dim).Padding(12)
				return
			}
			tv := terminal.View(c, pt.term).Fill()
			if a.focusReq == paneID && a.tab() == t {
				tv.Focus()
				a.focusReq = ""
			}
			if tv.Focused() && a.focus[t.ID] != paneID {
				a.setFocus(t, paneID)
			}
		})
	})
	if card.Pressed() && a.focus[t.ID] != paneID {
		a.setFocus(t, paneID)
		a.focusReq = paneID
	}
	return card
}

// paneHeader shows a pane's program, folder and status, and on hover its
// buttons.
func (a *App) paneHeader(c *ui.Context, k *colors, t *layout.Tab, p *layout.Node, showButtons bool) {
	rec, ok := a.record(p)
	paneID := p.ID
	h := ui.Row(c).Height(paneHeaderH).Padding(0, 6, 0, 10).Gap(7).AlignItems(ui.Center).MinWidth(0).Shrink(0).
		BorderWidth(0, 0, 1, 0).BorderColor(k.border)
	h.ContextMenu(func(m *ui.Menu) {
		if m.Item("Split right").Chosen() {
			a.post(func() { a.do("pane.split", args{"direction": "right", "pane": paneID}) })
		}
		if m.Item("Split down").Chosen() {
			a.post(func() { a.do("pane.split", args{"direction": "down", "pane": paneID}) })
		}
		if m.Item("Zoom").Checked(a.zoomed[t.ID] == paneID).Chosen() {
			a.post(func() { a.do("pane.zoom", args{"pane": paneID}) })
		}
		m.Separator()
		if m.Item("Close pane").Chosen() {
			a.post(func() { a.do("pane.close", args{"pane": paneID}) })
		}
	})
	if h.DoubleClicked() {
		a.post(func() { a.do("pane.zoom", args{"pane": paneID}) })
	} else if h.Clicked() {
		a.setFocus(t, paneID)
		a.focusReq = paneID
	}
	h.Children(func() {
		if ok {
			programTile(c, k, programOf(rec), 15)
		}
		ui.Row(c).Grow(1).MinWidth(0).Gap(6).AlignItems(ui.Center).ClipX().Children(func() {
			ui.Text(c, a.paneName(p)).FontSize(12.5).FontWeight(600).TextColor(k.foreground).SingleLine().Ellipsis("…").Shrink(0).MaxWidthPercent(70)
			if cwd := a.cwdOf(p); cwd != "" {
				ui.Text(c, pathOn(layout.MachineOf(rec.Address), cwd)).FontSize(12).TextColor(k.dim).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			}
			if ok {
				statusMark(c, k, a.word(rec), true)
			}
			if a.zoomed[t.ID] == paneID {
				if n := len(t.Panes()); n > 1 {
					// What the zoom hides, and the way back.
					pill := ui.Row(c).Padding(1, 7).Radius(9).Background(k.accent.Alpha(0.14)).Shrink(0).
						Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Unzoom").Tooltip(withKeys("Show all panes", a.keysFor("pane.zoom")))
					if pill.Hovered() {
						pill.Background(k.accent.Alpha(0.24))
					}
					pill.Children(func() {
						ui.Textf(c, "Zoomed · 1 of %d panes", n).FontSize(11).FontWeight(600).TextColor(k.accent).SingleLine()
					})
					if pill.Clicked() {
						a.post(func() { a.do("pane.zoom", args{"pane": paneID}) })
					}
				}
			}
		})
		// Hidden buttons keep their place and take no clicks.
		ui.Row(c).Gap(1).AlignItems(ui.Center).Shrink(0).Opacity(map[bool]float32{true: 1, false: 0}[showButtons]).IgnoreInput(!showButtons).Children(func() {
			if iconButton(c, k, "split-right", "Split right", 22, 13).Tooltip(withKeys("Split right", a.keysFor("pane.split:right"))).Clicked() {
				a.post(func() { a.do("pane.split", args{"direction": "right", "pane": paneID}) })
			}
			if iconButton(c, k, "split-down", "Split down", 22, 13).Tooltip(withKeys("Split down", a.keysFor("pane.split:down"))).Clicked() {
				a.post(func() { a.do("pane.split", args{"direction": "down", "pane": paneID}) })
			}
			zoom, label := "maximize", "Zoom"
			if a.zoomed[t.ID] == paneID {
				zoom, label = "minimize", "Unzoom"
			}
			if iconButton(c, k, zoom, label, 22, 13).Tooltip(withKeys(label, a.keysFor("pane.zoom"))).Clicked() {
				a.post(func() { a.do("pane.zoom", args{"pane": paneID}) })
			}
			if iconButton(c, k, "x", "Close pane", 22, 13).Tooltip(withKeys("Close pane", a.keysFor("pane.close"))).Clicked() {
				a.post(func() { a.do("pane.close", args{"pane": paneID}) })
			}
		})
	})
}
