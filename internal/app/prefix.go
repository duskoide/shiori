package app

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/keys"
)

// Shiori's keys all go through the prefix key: it is pressed, and the
// window goes from normal mode to command mode, as vim's modes, which the
// status bar shows. Nothing else changes: the focus stays where it was,
// and the window stays as it was. The next key is the window's, not the
// terminal's: it runs its action from the "prefix" section of keys.json,
// and the window is back in normal mode; Esc, or a key that is not
// bound, only goes back. The prefix key is whatever "global" binds to
// app.prefix, and the only key the window itself reads in normal mode.

// prefixMode is command mode while it lasts.
type prefixMode struct{}

// prefixPending reports whether command mode is on.
func (a *App) prefixPending() bool { return a.prefix != nil }

// startPrefix enters command mode.
func (a *App) startPrefix() {
	if a.prefix == nil {
		a.prefix = &prefixMode{}
		a.invalidate()
	}
}

// cancelPrefix goes back to normal mode.
func (a *App) cancelPrefix() {
	if a.prefix != nil {
		a.prefix = nil
		a.invalidate()
	}
}

// prefixMods are the modifier sets a key after the prefix may come with.
var prefixMods = func() []ui.Modifiers {
	var out []ui.Modifiers
	for i := 0; i < 16; i++ {
		var m ui.Modifiers
		if i&1 != 0 {
			m |= ui.Ctrl
		}
		if i&2 != 0 {
			m |= ui.Alt
		}
		if i&4 != 0 {
			m |= ui.Shift
		}
		if i&8 != 0 {
			m |= ui.Super
		}
		out = append(out, m)
	}
	return out
}()

// prefixKeys is the keys a chord can be made of, once each.
func prefixKeys() []ui.Key {
	seen := map[ui.Key]bool{}
	var out []ui.Key
	for _, k := range uiKeys {
		if !seen[k.key] {
			seen[k.key] = true
			out = append(out, k.key)
		}
	}
	return out
}

// prefixShortcuts takes the key of command mode, before the focused
// terminal sees it: it ends the mode and runs what the "prefix" section
// binds to it.
func (a *App) prefixShortcuts(c *ui.Context) {
	for _, key := range prefixKeys() {
		for _, mods := range prefixMods {
			if !c.Shortcut(mods, key) {
				continue
			}
			chord, ok := chordOf(mods, key)
			a.cancelPrefix()
			if ok {
				a.post(func() { a.runPrefixKey(chord) })
			}
			return
		}
	}
}

// runPrefixKey does what the key of command mode means. The prefix key
// again types itself. A key that is not bound is tried without the
// modifiers of the prefix key, which are often still held (Ctrl+B, then
// Ctrl+C for C).
func (a *App) runPrefixKey(chord keys.Chord) {
	act, ok := a.keymap.Lookup(keys.PrefixSection, chord)
	leaders := a.keymap.PrefixChords()
	if !ok {
		for _, l := range leaders {
			if l == chord {
				a.sendChord(chord)
				return
			}
		}
		for _, l := range leaders {
			bare := chord
			bare.Ctrl, bare.Alt, bare.Super = chord.Ctrl && !l.Ctrl, chord.Alt && !l.Alt, chord.Super && !l.Super
			if l.Shift && !chord.Shift {
				continue
			}
			bare.Shift = chord.Shift && !l.Shift
			if bare == chord {
				continue
			}
			if act, ok = a.keymap.Lookup(keys.PrefixSection, bare); ok {
				break
			}
		}
		if !ok {
			return
		}
	}
	switch {
	case act.None:
	case act.Terminal:
		a.sendChord(chord)
	case act.Send != "":
		a.sendFocused([]byte(act.Send))
	case act.Command != "":
		cmd, value := act.Command, act.Arg
		if a.findCommand(cmd+":"+value) != nil {
			cmd, value = cmd+":"+value, ""
		}
		a.doAction(cmd, value)
	}
}

// sendFocused types b in the focused pane's terminal.
func (a *App) sendFocused(b []byte) {
	p := a.pane()
	if p == nil || p.Content == nil {
		return
	}
	if pt := a.terms[p.Content.Terminal]; pt != nil && pt.term != nil {
		pt.term.Send(b)
	}
}

// sendChord types what the chord would type in a terminal, where that is
// plain: Ctrl and a letter, Alt and a character, a character.
func (a *App) sendChord(c keys.Chord) {
	if c.Super {
		return
	}
	if c.Key == "space" && c.Ctrl {
		a.sendFocused([]byte{0})
		return
	}
	if len(c.Key) != 1 {
		return
	}
	ch := c.Key[0]
	if c.Shift && ch >= 'a' && ch <= 'z' {
		ch -= 'a' - 'A'
	}
	switch {
	case c.Ctrl && ch >= 'a' && ch <= 'z':
		ch -= 'a' - 1
	case c.Ctrl && ch >= 'A' && ch <= 'Z':
		ch -= 'A' - 1
	case c.Ctrl:
		return
	}
	b := []byte{ch}
	if c.Alt {
		b = append([]byte{0x1b}, b...)
	}
	a.sendFocused(b)
}

// modeChip is the status bar's mode, as vim's: NORMAL, or COMMAND while
// the window waits for the key after the prefix key.
func (a *App) modeChip(c *ui.Context, k *colors) {
	leader := ""
	if l := a.keymap.PrefixChords(); len(l) > 0 {
		leader = l[0].String()
	}
	if a.prefixPending() {
		ui.Row(c).Gap(6).Padding(1, 8).Radius(5).Background(k.accent).AlignItems(ui.Center).Shrink(0).
			Role(ui.RoleStatus).Label("Command mode").Children(func() {
			ui.Text(c, "COMMAND").FontSize(11).FontWeight(700).TextColor(k.chrome).SingleLine()
		})
		hint := "Press a key · Esc to go back"
		if l := a.keymap.PrefixKeysFor("app.help"); len(l) > 0 {
			hint = "Press a key · " + typedLabel(strings.TrimPrefix(l[0], leader+" ")) + " lists them · Esc to go back"
		}
		ui.Text(c, hint).FontSize(11.5).TextColor(k.muted).SingleLine().Shrink(1).MinWidth(0).Ellipsis("…")
		return
	}
	tip := "Normal mode: keys go to the terminal"
	if leader != "" {
		tip += "; " + leader + " enters command mode"
	}
	ui.Box(c).Padding(1, 8).Radius(5).Border(1, k.border).Shrink(0).Role(ui.RoleStatus).Label("Normal mode").Tooltip(tip).Children(func() {
		ui.Text(c, "NORMAL").FontSize(11).FontWeight(700).TextColor(k.dim).SingleLine()
	})
}

// typedLabel is a key as a person types it: ? and not Shift+/.
func typedLabel(key string) string {
	rest, ok := strings.CutPrefix(key, "Shift+")
	if !ok || len(rest) != 1 {
		return key
	}
	if s, ok := map[string]string{"/": "?", ",": "<", ".": ">", "=": "+", ";": ":", "'": "\"", "[": "{", "]": "}", "-": "_"}[rest]; ok {
		return s
	}
	return strings.ToUpper(rest)
}
