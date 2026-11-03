package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/keys"
)

// prefixTimeout is how long the window waits for the key after the prefix key.
const prefixTimeout = 2 * time.Second

// prefixPending reports whether the prefix key was pressed and the key
// that follows it is awaited.
func (a *App) prefixPending() bool { return !a.prefixUntil.IsZero() }

// startPrefix makes the next key press one of the "prefix" section.
func (a *App) startPrefix() {
	a.prefixUntil = time.Now().Add(prefixTimeout)
	a.invalidate()
}

func (a *App) cancelPrefix() {
	if a.prefixPending() {
		a.prefixUntil = time.Time{}
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

// prefixKeys is the keys a chord can be made of, once each (uiKeys is filled by init).
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

// prefixShortcuts takes the key after the prefix key: it runs the action
// the "prefix" section binds to it. A key it does not bind ends the wait
// and is not typed; the prefix key again types itself. Esc ends the wait.
func (a *App) prefixShortcuts(c *ui.Context) {
	if a.prompt != nil || a.confirm != nil || a.settings || a.palette.open {
		a.prefixUntil = time.Time{}
		return
	}
	if wait := time.Until(a.prefixUntil); wait <= 0 {
		a.prefixUntil = time.Time{}
		return
	} else {
		c.After(wait)
	}
	for _, key := range prefixKeys() {
		for _, mods := range prefixMods {
			if !c.Shortcut(mods, key) {
				continue
			}
			chord, ok := chordOf(mods, key)
			a.prefixUntil = time.Time{}
			if ok {
				a.post(func() { a.runPrefixKey(chord) })
			}
			return
		}
	}
}

// runPrefixKey does what the key after the prefix key means.
func (a *App) runPrefixKey(chord keys.Chord) {
	act, ok := a.keymap.Lookup(keys.PrefixSection, chord)
	switch {
	case !ok:
		for _, l := range a.keymap.PrefixChords() {
			if l == chord {
				a.sendChord(chord) // the prefix key twice types it
			}
		}
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
	if len(c.Key) != 1 || c.Super {
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

// prefixLabel is how the status bar shows a pending prefix: the prefix
// key and what may follow.
func (a *App) prefixLabel() string {
	if l := a.keymap.PrefixChords(); len(l) > 0 {
		return l[0].String() + " …"
	}
	return "Prefix …"
}
