package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// view builds the window: the sidebar beside the tab bar, the panes and
// the status bar, then the overlays.
func (a *App) view(c *ui.Context) {
	a.services = c.Services()
	a.runPosted()
	if a.win != nil {
		a.focused = a.win.IsFocused()
	}
	k := a.k
	c.SetTheme(k.uiTheme(c.Theme()))
	c.Root().Background(k.canvas)
	a.dragging = false
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		if a.cfg.sidebarShown() {
			a.sidebar(c, k)
			a.sidebarDivider(c, k)
		}
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(k.canvas).Children(func() {
			a.tabBar(c, k)
			ui.Column(c).Grow(1).MinHeight(0).Padding(paneGap).Children(func() {
				a.workArea(c, k)
			})
			a.statusBar(c, k)
		})
	})
	a.shortcuts(c)
	a.paletteView(c, k)
	a.promptView(c, k)
	a.confirmView(c, k)
	a.settingsView(c, k)
	a.toastView(c, k)
	a.lookedAt()
	a.flush()
}

// sidebarDivider is the edge of the sidebar that resizes it.
func (a *App) sidebarDivider(c *ui.Context, k *colors) {
	div := ui.Box(c).Width(1).Background(k.border).Cursor(ui.CursorResizeColumn).Role(ui.RoleSplitter).Label("Sidebar edge")
	if dx, _, ok := div.Dragged(); ok {
		a.sidebarW = min(max(a.sidebarW+dx, sidebarMin), sidebarMax)
	}
	if div.DoubleClicked() {
		a.sidebarW = sidebarWidth
	}
}

// workArea shows the active tab's panes, or what keeps it from them.
func (a *App) workArea(c *ui.Context, k *colors) {
	t := a.tab()
	switch {
	case !a.connected && a.doc == nil:
		a.emptyState(c, k, "Connecting to herdr-core…", a.connErr)
	case t == nil:
		a.emptyState(c, k, "No spaces yet", "")
	default:
		a.tabContent(c, k, t)
	}
}

// emptyState fills the work area with a message, and a way to start.
func (a *App) emptyState(c *ui.Context, k *colors, title, detail string) {
	ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
		ui.Icon(c, glyph("logo")).Size(40, 40).TextColor(k.dim)
		ui.Text(c, title).FontSize(15).FontWeight(600).TextColor(k.foreground)
		if detail != "" {
			ui.Text(c, detail).FontSize(12.5).TextColor(k.muted).MaxWidth(520)
		}
		if a.connected {
			b := ui.Row(c).Padding(6, 12).Radius(7).Background(k.raised).Border(1, k.border).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("New space")
			if b.Hovered() {
				b.Background(k.hover)
			}
			b.Children(func() {
				ui.Text(c, "New space").FontSize(12.8).TextColor(k.foreground)
				if keys := a.keysFor("space.new"); keys != "" {
					ui.Text(c, "  "+keys).FontSize(12).TextColor(k.dim)
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
	c.After(time.Until(a.toasts[0].until))
	ui.Overlay(c, func() {
		ui.Column(c).Absolute().Bottom(statusHeight + 16).Right(16).Gap(8).AlignItems(ui.End).PassThrough().Children(func() {
			for i, t := range a.toasts {
				ui.Row(c.Key(i)).Gap(10).Padding(8, 12).Radius(10).Background(k.popover).Border(1, k.border).
					Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.3)).AlignItems(ui.Center).MaxWidth(460).Children(func() {
					ui.Text(c, t.text).FontSize(12.5).TextColor(k.foreground)
				})
			}
		})
	})
}
