package app

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/keys"
)

// Shiori's keys all go through the prefix key: it is pressed, and the
// window enters command mode, a dialog that takes the keyboard from the
// terminal and shows what the next key does. The next key runs its action
// from the "prefix" section of keys.json, and ends the mode; so does Esc.
// The prefix key is whatever "global" binds to app.prefix, and the only
// key the window itself reads.

// prefixMode is command mode while it lasts.
type prefixMode struct {
	open bool
}

// prefixPending reports whether command mode is on.
func (a *App) prefixPending() bool { return a.prefix != nil }

// startPrefix enters command mode.
func (a *App) startPrefix() {
	if a.prefix == nil {
		a.prefix = &prefixMode{open: true}
		a.invalidate()
	}
}

func (a *App) cancelPrefix() {
	if a.prefix != nil {
		a.prefix = nil
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
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

// prefixShortcuts takes the key of command mode: it ends the mode and
// runs what the "prefix" section binds to it. Called inside the mode's
// dialog.
func (a *App) prefixShortcuts(c *ui.Context) {
	for _, key := range prefixKeys() {
		for _, mods := range prefixMods {
			if !c.Shortcut(mods, key) {
				continue
			}
			chord, ok := chordOf(mods, key)
			a.prefix.open = false
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

// prefixLabel is how the status bar shows command mode.
func (a *App) prefixLabel() string {
	if l := a.keymap.PrefixChords(); len(l) > 0 {
		return "COMMAND · " + l[0].String()
	}
	return "COMMAND"
}

// prefixEntry is a key of command mode as the dialog lists it.
type prefixEntry struct {
	keys, title string
}

// prefixEntries are the keys the mode offers, by group, in the order of
// the settings; the numbered ones (tab 1 to 9) are one entry.
func (a *App) prefixEntries() (groups []string, by map[string][]prefixEntry) {
	by = map[string][]prefixEntry{}
	type item struct {
		chord keys.Chord
		spec  string
	}
	var items []item
	for ch, act := range a.keymap.Sections[keys.PrefixSection] {
		if act.None || act.Command == "" && act.Send == "" && !act.Terminal {
			continue
		}
		items = append(items, item{ch, act.Spec()})
	}
	sort.Slice(items, func(i, j int) bool {
		ci, ni, ai := a.rank(items[i].spec)
		cj, nj, aj := a.rank(items[j].spec)
		if ci != cj {
			return ci < cj
		}
		if ni != nj {
			return ni < nj
		}
		if ai != aj {
			return ai < aj
		}
		return items[i].chord.String() < items[j].chord.String()
	})
	seen := map[string]int{} // a command's entry, for its extra keys
	for _, it := range items {
		act, _ := keys.ParseAction(it.spec)
		title := a.actionTitle(keys.PrefixSection, it.spec)
		key := prefixKeyLabel(it.chord)
		group := "Other"
		if act.Command != "" {
			group = a.groupOf(it.spec)
		}
		if n := strings.HasSuffix(act.Command, ".select") && act.Arg != ""; n {
			if act.Arg != "1" {
				if i, ok := seen[act.Command]; ok {
					by[group][i].keys = "1-9"
				}
				continue
			}
			title = strings.TrimSuffix(title, " 1")
			seen[act.Command] = len(by[group])
		}
		if i, ok := seen[it.spec]; ok {
			by[group][i].keys += " " + key
			continue
		}
		seen[it.spec] = len(by[group])
		if _, ok := by[group]; !ok {
			groups = append(groups, group)
		}
		by[group] = append(by[group], prefixEntry{keys: key, title: title})
	}
	order := func(g string) int {
		for i, x := range []string{"Spaces", "Tabs", "Panes", "Agents", "Window"} {
			if x == g {
				return i
			}
		}
		if g == "Other" {
			return 99
		}
		return 50
	}
	sort.SliceStable(groups, func(i, j int) bool { return order(groups[i]) < order(groups[j]) })
	return groups, by
}

// prefixKeyLabel is a chord of the mode as a person reads it: ? and not
// Shift+/.
func prefixKeyLabel(c keys.Chord) string {
	if c.Shift && !c.Ctrl && !c.Alt && !c.Super {
		if s, ok := map[string]string{"/": "?", ",": "<", ".": ">", "=": "+", ";": ":", "'": "\"", "[": "{", "]": "}", "-": "_"}[c.Key]; ok {
			return s
		}
		if len(c.Key) == 1 {
			return strings.ToUpper(c.Key)
		}
	}
	if !c.Ctrl && !c.Alt && !c.Shift && !c.Super && len(c.Key) == 1 {
		return c.Key
	}
	return c.String()
}

// prefixView is command mode: a dialog at the foot of the window that
// holds the keyboard and lists the keys.
func (a *App) prefixView(c *ui.Context, k *colors) {
	p := a.prefix
	if p == nil {
		return
	}
	if a.prompt != nil || a.confirm != nil || a.settings || a.palette.open {
		a.prefix = nil
		return
	}
	groups, by := a.prefixEntries()
	ui.DialogBase(c, &p.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32)).Justify(ui.End).Padding(0, 0, statusHeight+24, 0)
		panel.Width(820).MaxWidthPercent(94).Padding(14, 16).Gap(12).Radius(12).Background(k.popover).Border(2, k.accent).
			Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.5))
		panel.Children(func() {
			// Holds the focus, so the terminal does not see the keys.
			ui.Box(c).Size(0, 0).Focusable().FocusRing(false).Label("Command mode").AutoFocus()
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Padding(2, 9).Radius(6).Background(k.accent).Children(func() {
					ui.Text(c, "COMMAND MODE").FontSize(11.5).FontWeight(700).TextColor(k.chrome).SingleLine()
				})
				if l := a.keymap.PrefixChords(); len(l) > 0 {
					ui.Text(c, l[0].String()+" was pressed").FontSize(12).TextColor(k.muted)
				}
				ui.Spacer(c)
				ui.Text(c, "Press a key to run it · Esc to cancel").FontSize(12).TextColor(k.muted)
			})
			if len(groups) == 0 {
				ui.Text(c, "The prefix section of keys.json binds no keys.").FontSize(12.5).TextColor(k.muted)
			}
			ui.Row(c).Gap(22).Children(func() {
				for _, g := range groups {
					ui.Column(c.Key(g)).Gap(3).MinWidth(0).Grow(1).Children(func() {
						ui.Text(c, g).FontSize(11).FontWeight(600).TextColor(k.dim).Padding(0, 0, 3, 0)
						for _, e := range by[g] {
							ui.Row(c.Key(e.keys + e.title)).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Box(c).MinWidth(22).Padding(1, 6).Radius(5).Background(k.hover).Border(1, k.border).Center().Shrink(0).Children(func() {
									ui.Text(c, e.keys).FontSize(11.5).FontWeight(700).TextColor(k.accent).SingleLine()
								})
								ui.Text(c, e.title).FontSize(12).TextColor(k.foreground).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
							})
						}
					})
				}
			})
			a.prefixShortcuts(c)
		})
	})
	if !p.open {
		a.cancelPrefix()
	}
}
