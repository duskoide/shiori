package main

import (
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/duskoide/herdr-mygo/internal/theme"
)

// Sizes of the window's regions, in DIPs, as the Herdr layout has them.
const (
	sidebarWidth   = 268
	sidebarMin     = 180
	sidebarMax     = 480
	brandHeight    = 46
	tabBarHeight   = 42
	statusHeight   = 28
	paneHeaderH    = 30
	paneGap        = 8
	paneRadius     = 10
	sidebarRowH    = 30
	sidebarFooterH = 69
)

// colors are the theme's tokens as the interface paints them.
type colors struct {
	dark                                         bool
	background, foreground, canvas, chrome       ui.Color
	popover, raised, hover, selected, border     ui.Color
	input, muted, dim, accent, accentForeground  ui.Color
	ring, working, blocked, done, idle, solid    ui.Color
	solidForeground, terminalForeground, surface ui.Color
	ansi                                         [16]ui.Color
	selection, cursor                            ui.Color
}

func uiColor(c theme.Color) ui.Color { return ui.Color{R: c.R, G: c.G, B: c.B, A: c.A} }

func colorsFrom(t theme.Tokens) *colors {
	k := &colors{
		dark:               t.Dark,
		background:         uiColor(t.Background),
		foreground:         uiColor(t.Foreground),
		canvas:             uiColor(t.Canvas),
		chrome:             uiColor(t.Chrome),
		popover:            uiColor(t.Popover),
		raised:             uiColor(t.Raised),
		hover:              uiColor(t.Hover),
		selected:           uiColor(t.Selected),
		border:             uiColor(t.Border),
		input:              uiColor(t.Input),
		muted:              uiColor(t.Muted),
		dim:                uiColor(t.Dim),
		accent:             uiColor(t.Accent),
		accentForeground:   uiColor(t.AccentForeground),
		ring:               uiColor(t.Ring),
		working:            uiColor(t.Working),
		blocked:            uiColor(t.Blocked),
		done:               uiColor(t.Done),
		idle:               uiColor(t.Idle),
		solid:              uiColor(t.Solid),
		solidForeground:    uiColor(t.SolidForeground),
		terminalForeground: uiColor(t.TerminalForeground),
		surface:            uiColor(t.Surface),
		selection:          uiColor(t.Selection),
		cursor:             uiColor(t.Cursor),
	}
	for i, c := range t.ANSI {
		k.ansi[i] = uiColor(c)
	}
	return k
}

// uiTheme is MyGo's theme for stock widgets (inputs, menus, tooltips)
// in the app's colors.
func (k *colors) uiTheme(base *ui.Theme) *ui.Theme {
	t := *base
	t.Dark = k.dark
	t.Background = k.popover
	t.Surface = k.input
	t.SurfaceHover = k.hover
	t.SurfacePressed = k.selected
	t.Border = k.border
	t.Text = k.foreground
	t.TextMuted = k.muted
	t.Accent = k.accent
	t.AccentHover = k.accent
	t.AccentPressed = k.accent
	t.AccentText = k.accentForeground
	t.Selection = k.selection
	t.Focus = k.ring
	return &t
}

// terminalTheme is the colors terminals draw with.
func (k *colors) terminalTheme() *terminal.Theme {
	return &terminal.Theme{
		Foreground: k.terminalForeground,
		Background: k.background,
		Cursor:     k.cursor,
		Selection:  k.selection,
		Palette:    k.ansi,
	}
}

// spaceColors are the avatar colors of spaces, by ANSI index: teal,
// orange, blue, violet, green, pink.
var spaceColors = []int{6, 3, 4, 5, 2, 1}
