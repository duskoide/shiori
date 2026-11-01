package app

import (
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/keys"
)

// paneTerm is a terminal of the core shown in a pane: a local emulator fed
// by the core's stream of it.
type paneTerm struct {
	addr    string
	term    *terminal.Terminal
	stream  *core.Stream
	closing bool // its pane closed here: its end is no news
	since   time.Time
	focus   bool // what the core was last told about the viewer's focus
}

// termFor returns the terminal a pane shows, attaching to the core's
// terminal the first time a pane needs it.
func (a *App) termFor(c *ui.Context, addr string) *paneTerm {
	if pt := a.terms[addr]; pt != nil {
		return pt
	}
	if a.client == nil {
		return nil
	}
	if at, ok := a.retry[addr]; ok {
		if wait := time.Until(at); wait > 0 {
			c.After(wait)
			return nil
		}
		delete(a.retry, addr)
	}
	k := a.k
	d := a.display()
	st := a.client.Stream(addr, &d)
	pt := &paneTerm{addr: addr, stream: st, since: time.Now()}
	t, err := terminal.New(terminal.Options{
		Conn:      st,
		Font:      a.termFont(),
		Theme:     k.terminalTheme(),
		DarkTheme: k.terminalTheme(),
		Padding:   terminal.Padding{X: 10, Y: 6},
		// herdr-app's cursor does not blink, and a blinking one redraws
		// the window twice a second.
		NoBlink: true,
		OnExit: func(int) {
			a.post(func() { a.termEnded(pt) })
		},
		OnBell: func() {
			a.post(func() { a.invalidate() })
		},
		OnKey: func(mods ui.Modifiers, key ui.Key) bool {
			return a.terminalKey(pt, mods, key)
		},
		OnZoom: func(z terminal.Zoom) bool {
			a.post(func() { a.zoomSteps(z) })
			return true
		},
	})
	if err != nil {
		a.notify("terminal: " + err.Error())
		a.retry[addr] = time.Now().Add(5 * time.Second)
		st.Close()
		return nil
	}
	pt.term = t
	a.terms[addr] = pt
	return pt
}

// zoomSteps changes the text size of the terminals by the steps of z, or
// back to the default, and saves the settings once the steps are over.
func (a *App) zoomSteps(z terminal.Zoom) {
	switch {
	case z.Reset:
		a.cfg.FontSize = 0
	case z.Steps != 0:
		a.cfg.FontSize = clampFontSize(a.cfg.fontSize() + float32(z.Steps))
	default:
		return
	}
	for _, pt := range a.terms {
		pt.term.SetFont(a.termFont())
	}
	a.saveSettingsSoon()
}

// syncFocus tells the core which terminal this window has the focus on:
// the focused pane of the active tab while the window has the focus. A
// terminal hears of it when that changes, not every frame.
func (a *App) syncFocus() {
	var want string
	if a.focused {
		if p := a.pane(); p != nil && p.Content != nil {
			want = p.Content.Terminal
		}
	}
	for addr, pt := range a.terms {
		if f := want != "" && addr == want; f != pt.focus {
			pt.focus = f
			pt.stream.Focus(f)
		}
	}
}

// termEnded handles the end of a terminal's stream: the program ended,
// and its pane closes, or the stream broke, and it attaches again soon.
func (a *App) termEnded(pt *paneTerm) {
	if a.terms[pt.addr] == pt {
		delete(a.terms, pt.addr)
	}
	pt.term.Close()
	if pt.closing {
		return
	}
	if why := pt.stream.CloseReason(); why != nil && (why.Reason == "exited" || why.Reason == "killed" || why.Reason == "signaled") {
		a.closeTerminalPanes(pt.addr)
		return
	}
	// Attach again, waiting longer while attaching keeps failing.
	if time.Since(pt.since) > attachSettled {
		delete(a.sy.attachFails, pt.addr)
	}
	if a.sy.attachFails == nil {
		a.sy.attachFails = map[string]int{}
	}
	n := a.sy.attachFails[pt.addr]
	a.sy.attachFails[pt.addr] = n + 1
	a.retry[pt.addr] = time.Now().Add(attachBackoff[min(n, len(attachBackoff)-1)])
}

// closeTerminalPanes closes the panes that show a terminal that ended.
func (a *App) closeTerminalPanes(addr string) {
	if a.doc == nil {
		return
	}
	for {
		s, t, p := a.doc.PaneOfTerminal(addr)
		if p == nil {
			break
		}
		if !t.RemovePane(p.ID) {
			break
		}
		if len(t.Panes()) == 0 {
			s.RemoveTab(t.ID)
			if len(s.Tabs) == 0 {
				a.doc.RemoveSpace(s.ID)
			}
		}
	}
	a.changed()
	a.focusReq = a.focus[a.activeTab[a.activeSpace]]
}

func (a *App) termFont() terminal.Font {
	family := a.cfg.Font
	if family == "" {
		family = defaultFontFamily
	}
	return terminal.Font{Family: family, Size: a.cfg.fontSize()}
}

// terminalKey takes the keys of the "terminal" section of keys.json that
// type text (send:…), before the terminal encodes them.
func (a *App) terminalKey(pt *paneTerm, mods ui.Modifiers, key ui.Key) bool {
	chord, ok := chordOf(mods, key)
	if !ok {
		return false
	}
	act, ok := a.keymap.Lookup("terminal", chord)
	if !ok || act.Terminal {
		return false
	}
	if act.Send != "" {
		pt.term.Send([]byte(act.Send))
		return true
	}
	return false
}

func hexOf(c ui.Color) string {
	const digits = "0123456789abcdef"
	b := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint8{c.R, c.G, c.B} {
		b[1+2*i], b[2+2*i] = digits[v>>4], digits[v&15]
	}
	return string(b)
}

// uiKeys maps the keys of keys.json to MyGo's: a key typed with Shift is
// its unshifted key with Shift held, as "?" is Shift and "/".
var uiKeys = map[string]struct {
	key   ui.Key
	shift bool
}{
	"enter": {key: ui.KeyEnter}, "tab": {key: ui.KeyTab}, "escape": {key: ui.KeyEscape},
	"space": {key: ui.KeySpace}, "backspace": {key: ui.KeyBackspace}, "delete": {key: ui.KeyDelete},
	"insert": {key: ui.KeyInsert}, "home": {key: ui.KeyHome}, "end": {key: ui.KeyEnd},
	"pageup": {key: ui.KeyPageUp}, "pagedown": {key: ui.KeyPageDown},
	"up": {key: ui.KeyUp}, "down": {key: ui.KeyDown}, "left": {key: ui.KeyLeft}, "right": {key: ui.KeyRight},
	"-": {key: ui.KeyMinus}, "_": {key: ui.KeyMinus, shift: true},
	"=": {key: ui.KeyEqual}, "+": {key: ui.KeyEqual, shift: true},
	",": {key: ui.KeyComma}, "<": {key: ui.KeyComma, shift: true},
	".": {key: ui.KeyPeriod}, ">": {key: ui.KeyPeriod, shift: true},
	"/": {key: ui.KeySlash}, "?": {key: ui.KeySlash, shift: true},
	";": {key: ui.KeySemicolon}, ":": {key: ui.KeySemicolon, shift: true},
	"'": {key: ui.KeyQuote}, "\"": {key: ui.KeyQuote, shift: true},
	"[": {key: ui.KeyBracketLeft}, "{": {key: ui.KeyBracketLeft, shift: true},
	"]": {key: ui.KeyBracketRight}, "}": {key: ui.KeyBracketRight, shift: true},
	"\\": {key: ui.KeyBackslash}, "|": {key: ui.KeyBackslash, shift: true},
	"`": {key: ui.KeyBackquote}, "~": {key: ui.KeyBackquote, shift: true},
}

func init() {
	letters := []ui.Key{ui.KeyA, ui.KeyB, ui.KeyC, ui.KeyD, ui.KeyE, ui.KeyF, ui.KeyG, ui.KeyH, ui.KeyI, ui.KeyJ, ui.KeyK, ui.KeyL, ui.KeyM,
		ui.KeyN, ui.KeyO, ui.KeyP, ui.KeyQ, ui.KeyR, ui.KeyS, ui.KeyT, ui.KeyU, ui.KeyV, ui.KeyW, ui.KeyX, ui.KeyY, ui.KeyZ}
	for i, k := range letters {
		uiKeys[string(rune('a'+i))] = struct {
			key   ui.Key
			shift bool
		}{key: k}
	}
	digits := []ui.Key{ui.Key0, ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}
	for i, k := range digits {
		uiKeys[string(rune('0'+i))] = struct {
			key   ui.Key
			shift bool
		}{key: k}
	}
	fkeys := []ui.Key{ui.KeyF1, ui.KeyF2, ui.KeyF3, ui.KeyF4, ui.KeyF5, ui.KeyF6, ui.KeyF7, ui.KeyF8, ui.KeyF9, ui.KeyF10, ui.KeyF11, ui.KeyF12}
	for i, k := range fkeys {
		uiKeys["f"+itoa(i+1)] = struct {
			key   ui.Key
			shift bool
		}{key: k}
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// uiChord is a chord of keys.json as MyGo reports it.
func uiChord(c keys.Chord) (ui.Modifiers, ui.Key, bool) {
	k, ok := uiKeys[c.Key]
	if !ok {
		return 0, 0, false
	}
	var m ui.Modifiers
	if c.Ctrl {
		m |= ui.Ctrl
	}
	if c.Alt {
		m |= ui.Alt
	}
	if c.Shift || k.shift {
		m |= ui.Shift
	}
	if c.Super {
		m |= ui.Super
	}
	return m, k.key, true
}

// chordOf is the chord of keys.json a key press is, its key unshifted.
func chordOf(mods ui.Modifiers, key ui.Key) (keys.Chord, bool) {
	for name, k := range uiKeys {
		if k.key == key && !k.shift {
			return keys.Chord{Ctrl: mods&ui.Ctrl != 0, Alt: mods&ui.Alt != 0, Shift: mods&ui.Shift != 0, Super: mods&ui.Super != 0, Key: name}, true
		}
	}
	return keys.Chord{}, false
}

// shortcutAllowed says whether a global key's command may run now: not
// while a prompt, a confirmation or the settings are open, and with the
// palette open only the keys that toggle it or open a mod.
func (a *App) shortcutAllowed(cmd string) bool {
	switch {
	case a.prompt != nil || a.confirm != nil || a.settings:
		return false
	case a.palette.open:
		return cmd == "app.palette" || cmd == "mod.open"
	}
	return true
}

// shortcuts runs the "global" keys of keys.json.
func (a *App) shortcuts(c *ui.Context) {
	for chord, act := range a.keymap.Sections["global"] {
		if act.Command == "" || !a.shortcutAllowed(act.Command) || a.findCommand(act.Command) == nil && a.findCommand(act.Command+":"+act.Arg) == nil {
			continue
		}
		mods, key, ok := uiChord(chord)
		if !ok || !c.Shortcut(mods, key) {
			continue
		}
		cmd, value := act.Command, act.Arg
		if a.findCommand(cmd+":"+value) != nil {
			cmd, value = cmd+":"+value, ""
		}
		if cmd == "app.palette" && a.palette.open {
			// The key that opened the palette closes it.
			a.palette.open = false
			a.focusReq = a.focus[a.activeTab[a.activeSpace]]
			continue
		}
		a.post(func() { a.doAction(cmd, value) })
	}
}

// keysFor is how the palette shows a command's keys.
func (a *App) keysFor(name string) string {
	cmd, value, _ := cutColon(name)
	for _, k := range a.keymap.KeysFor(name) {
		return k
	}
	if value != "" {
		for _, k := range a.keymap.KeysFor(cmd) {
			return k
		}
	}
	return ""
}

func cutColon(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// attachBackoff is how long to wait before attaching to a terminal again
// after the stream broke, by the number of breaks in a row.
var attachBackoff = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}

// attachSettled is how long a stream must last to reset the backoff.
const attachSettled = 10 * time.Second
