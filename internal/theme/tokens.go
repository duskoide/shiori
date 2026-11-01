package theme

import "strconv"

// Tokens is every colour the chrome uses, resolved from a Palette.
type Tokens struct {
	Background, TerminalForeground, Foreground, Surface, Canvas, Chrome, Popover, Raised,
	Hover, Selected, Border, Input, Muted, Dim, Accent, AccentForeground, Solid, SolidForeground,
	Ring, Selection, Cursor, Working, Blocked, Done, Idle Color
	// Unknown is the colour of an unrecognised agent status (same as Idle by default).
	Unknown Color
	ANSI    [16]Color
	// Dark reports whether the palette background is dark.
	Dark bool
}

var (
	black = Color{0, 0, 0, 255}
	white = Color{255, 255, 255, 255}
	warm  = rgb(0xe8e8e2)
)

// Resolve derives the chrome tokens from p, then applies p.Overrides.
// A light palette is one whose background luminance exceeds 0.5.
func Resolve(p Palette) Tokens {
	bg, light := p.Background, luminance(p.Background) > 0.5
	t := Tokens{
		Dark:               !light,
		Background:         bg,
		TerminalForeground: p.Foreground,
		Surface:            bg,
		AccentForeground:   bg,
		Accent:             p.ANSI[4],
		Selection:          p.Selection,
		Cursor:             p.Cursor,
		ANSI:               p.ANSI,
	}
	if t.Cursor.A == 0 {
		t.Cursor = p.Foreground
	}
	st := [3]Color{p.ANSI[3], p.ANSI[1], p.ANSI[4]}
	if p.Status != nil {
		st = *p.Status
	}
	t.Working, t.Blocked, t.Done = st[0], st[1], st[2]

	fg, mutedT, dimT := rgb(0xeeeeef), 0.6, 0.38
	if light {
		fg, mutedT, dimT = rgb(0x1b1b1e), 0.66, 0.46
		t.Popover = bg
		t.Raised = Mix(bg, black, 0.035)
		t.Hover, t.Selected = Color{0, 0, 0, 0x0c}, Color{0, 0, 0, 0x12}
		t.Border, t.Input = Color{0, 0, 0, 0x17}, Color{0, 0, 0, 0x26}
		t.Canvas, t.Chrome = Mix(bg, warm, 0.2), Mix(bg, warm, 0.6)
		t.SolidForeground = white
	} else {
		t.Popover = Mix(bg, white, 0.06)
		t.Raised = Mix(bg, white, 0.09)
		t.Hover, t.Selected = Color{255, 255, 255, 0x0e}, Color{255, 255, 255, 0x17}
		t.Border, t.Input = Color{255, 255, 255, 0x18}, Color{255, 255, 255, 0x24}
		t.Canvas, t.Chrome = Mix(bg, black, 0.16), Mix(bg, black, 0.32)
		t.SolidForeground = Mix(bg, black, 0.28)
	}
	t.Foreground, t.Solid = fg, fg
	t.Muted, t.Dim = Mix(bg, fg, mutedT), Mix(bg, fg, dimT)
	t.Ring = Mix(bg, fg, 0.7)
	t.Idle = Mix(bg, fg, 0.38)
	t.Unknown = t.Idle

	for name, c := range p.Overrides {
		if f := t.field(name); f != nil {
			*f = c
		}
	}
	return t
}

// field returns a pointer to the token with the given snake_case name
// ("accent_foreground", "ansi3"), or nil.
func (t *Tokens) field(name string) *Color {
	switch name {
	case "background":
		return &t.Background
	case "terminal_foreground":
		return &t.TerminalForeground
	case "foreground":
		return &t.Foreground
	case "surface":
		return &t.Surface
	case "canvas":
		return &t.Canvas
	case "chrome":
		return &t.Chrome
	case "popover":
		return &t.Popover
	case "raised":
		return &t.Raised
	case "hover":
		return &t.Hover
	case "selected":
		return &t.Selected
	case "border":
		return &t.Border
	case "input":
		return &t.Input
	case "muted":
		return &t.Muted
	case "dim":
		return &t.Dim
	case "accent":
		return &t.Accent
	case "accent_foreground":
		return &t.AccentForeground
	case "solid":
		return &t.Solid
	case "solid_foreground":
		return &t.SolidForeground
	case "ring":
		return &t.Ring
	case "selection":
		return &t.Selection
	case "cursor":
		return &t.Cursor
	case "working":
		return &t.Working
	case "blocked":
		return &t.Blocked
	case "done":
		return &t.Done
	case "idle":
		return &t.Idle
	case "unknown":
		return &t.Unknown
	}
	if n, ok := cutPrefix(name, "ansi"); ok {
		if i, err := strconv.Atoi(n); err == nil && i >= 0 && i < 16 && strconv.Itoa(i) == n {
			return &t.ANSI[i]
		}
	}
	return nil
}

func cutPrefix(s, p string) (string, bool) {
	if len(s) > len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return "", false
}
