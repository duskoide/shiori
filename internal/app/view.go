package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// view builds the window: the sidebar beside the tab bar, the panes and
// the status bar, then the overlays.
func (a *App) view(c *ui.Context) {
	a.setServices(c.Services())
	if w := a.win; w != nil {
		a.bindWindow(w)
		a.focused = w.IsFocused()
	}
	a.runPosted()
	k := a.k
	textScale = a.cfg.uiFontSize() / defaultUIFontSize
	c.SetZoom(a.cfg.uiScale())
	th := k.uiTheme(c.Theme())
	th.Font = a.cfg.UIFont // "" is the system's
	th.FontSize = ts(th.FontSize)
	c.SetTheme(th)
	c.Root().Background(k.canvas)
	a.dragging = false
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		if a.cfg.sidebarShown() {
			a.sidebar(c, k)
			a.sidebarDivider(c, k)
		}
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(k.canvas).Children(func() {
			a.tabBar(c, k)
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(paneGap).Children(func() {
					a.workArea(c, k)
				})
				if a.cfg.changesShown() {
					a.changesDivider(c, k)
					a.changesPanel(c, k)
				}
			})
			a.statusBar(c, k)
		})
	})
	a.shortcuts(c)
	a.syncFocus()
	a.syncChanges()
	a.nextDialog()
	a.paletteView(c, k)
	a.promptView(c, k)
	a.confirmView(c, k)
	a.settingsView(c, k)
	a.toastView(c, k)
	a.lookedAt()
	a.flush()
	a.emitEvents()
}

// sidebarDivider is the edge of the sidebar that resizes it.
func (a *App) sidebarDivider(c *ui.Context, k *colors) {
	// One pixel of the layout, but a grip a few pixels wide over the
	// sidebar's edge, so that it is easy to catch.
	div := ui.Box(c).Width(sidebarGrip).Margin(0, 0, 0, 1-sidebarGrip).Shrink(0).
		Cursor(ui.CursorResizeColumn).Role(ui.RoleSplitter).Label("Sidebar edge").Tooltip("Drag to resize, double-click to reset")
	if dx, _, ok := div.Dragged(); ok {
		a.sidebarW = min(max(a.sidebarW+dx, sidebarMin), sidebarMax)
	}
	if div.DoubleClicked() {
		a.sidebarW = sidebarWidth
	}
	active := div.Hovered() || div.Dragging()
	div.Draw(func(p *ui.Painter, r ui.Rect) {
		if active {
			p.Fill(ui.Rect{X: r.X + r.W - 2, Y: r.Y, W: 2, H: r.H}, k.accent.Alpha(0.6), 0)
			return
		}
		p.Fill(ui.Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: r.H}, k.border, 0)
	})
}

// workArea shows the active tab's panes, or what keeps it from them.
func (a *App) workArea(c *ui.Context, k *colors) {
	t := a.tab()
	switch {
	case !a.connected && a.doc == nil && a.connErr != "":
		a.emptyState(c, k, "Cannot reach herdr-core", a.connErr+"\n\nShiori keeps trying. It starts the core itself when hc is on your PATH.")
	case !a.connected && a.doc == nil:
		a.emptyState(c, k, "Connecting to herdr-core…", "")
	case t == nil:
		a.emptyState(c, k, "No spaces yet", "A space is a folder with its own tabs and terminals.")
	default:
		a.tabContent(c, k, t)
	}
}

// emptyState fills the work area with a message, and a way to start.
func (a *App) emptyState(c *ui.Context, k *colors, title, detail string) {
	ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
		ui.Icon(c, glyph("logo")).Size(40, 40).TextColor(k.dim)
		ui.Text(c, title).FontSize(ts(15)).FontWeight(600).TextColor(k.foreground)
		if detail != "" {
			ui.Text(c, detail).FontSize(ts(12.5)).TextColor(k.muted).MaxWidth(520).TextAlign(ui.Center).LineHeight(1.4)
		}
		if a.connected {
			b := ui.Row(c).Padding(6, 12).Radius(7).Background(k.raised).Border(1, k.border).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("New space")
			if b.Hovered() {
				b.Background(k.hover)
			}
			b.Children(func() {
				ui.Text(c, "New space").FontSize(ts(12.8)).TextColor(k.foreground)
				if keys := a.keysFor("space.new"); keys != "" {
					ui.Text(c, "  "+keys).FontSize(ts(12)).TextColor(k.dim)
				}
			})
			if b.Clicked() {
				a.post(func() { a.doAction("space.new", "") })
			}
		}
	})
}

// toastView shows the latest messages at the bottom of the window.
func (a *App) toastView(c *ui.Context, k *colors) {
	now := time.Now()
	live := a.toasts[:0]
	for _, t := range a.toasts {
		if t.until.After(now) {
			live = append(live, t)
		}
	}
	a.toasts = live
	if len(a.toasts) == 0 {
		return
	}
	next := a.toasts[0].until
	for _, t := range a.toasts {
		if t.until.Before(next) {
			next = t.until
		}
	}
	c.After(time.Until(next))
	ui.Overlay(c, func() {
		ui.Column(c).Absolute().Bottom(statusHeight + 16).Right(16).Gap(8).AlignItems(ui.End).PassThrough().Children(func() {
			for i, t := range a.toasts {
				border := k.border
				if t.level == levelError {
					border = k.blocked.Alpha(0.6)
				}
				ui.Row(c.Key(i)).Gap(10).Padding(8, 12).Radius(10).Background(k.popover).Border(1, border).
					Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.3)).AlignItems(ui.Center).MaxWidth(460).Children(func() {
					if t.level == levelError {
						ui.Icon(c, glyph("alert")).Size(14, 14).TextColor(k.blocked).Shrink(0)
					} else if t.icon != "" {
						ui.Icon(c, glyph(t.icon)).Size(14, 14).TextColor(k.done).Shrink(0)
					}
					ui.Text(c, t.text).FontSize(ts(12.5)).TextColor(k.foreground)
				})
			}
		})
	})
}
