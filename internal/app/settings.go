package app

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/keys"
	"github.com/duskoide/shiori/internal/theme"
)

// The settings panel shows and changes everything settings.json and
// keys.json hold: the theme, the terminals' font and text size, the
// sidebar, and every key binding of every section. What it changes it
// writes back at once, as the files' own edits are read back.

// settingsSections are the panel's pages, in its order.
var settingsSections = []struct{ id, title, icon string }{
	{"appearance", "Appearance", "palette"},
	{"terminal", "Terminal", "terminal"},
	{"layout", "Layout", "sidebar"},
	{"keyboard", "Keyboard", "keyboard"},
	{"files", "Files", "file"},
}

// settingsState is what the panel keeps while it is open.
type settingsState struct {
	section      string
	font         string // the font field, applied on Enter or Apply
	fontSeen     string // the font settings.json had as the field last took it
	query        string // the keyboard page's filter
	confirmReset bool   // Reset all asks before it removes keys.json
	rec          *recording
	// The form adding a binding of any action.
	addSection, addAction string
}

// recording is a binding waiting for its keys: of action spec in section,
// in place of chord replace when it is set.
type recording struct {
	section, spec string
	replace       *keys.Chord
	got           *keys.Chord // the keys pressed, applied with the next frame
	cancel        bool
}

// openSettings opens the panel at a section ("" for the first).
func (a *App) openSettings(section string) {
	a.settings = true
	id, _ := settingsSectionOf(section)
	a.set = settingsState{section: id, font: a.cfg.Font, fontSeen: a.cfg.Font, addSection: "global"}
}

// settingsSectionOf is the section a name means: its id, or a word for
// what is in it ("keys", "theme"). "" is the first; one it does not know
// is too, and not ok.
func settingsSectionOf(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return settingsSections[0].id, true
	}
	for _, s := range settingsSections {
		if s.id == name {
			return s.id, true
		}
	}
	switch name {
	case "keys", "keybindings", "bindings", "shortcuts":
		return "keyboard", true
	case "theme", "themes", "colors":
		return "appearance", true
	case "font", "fonts", "text":
		return "terminal", true
	case "sidebar":
		return "layout", true
	case "about", "config":
		return "files", true
	}
	return settingsSections[0].id, false
}

func (a *App) cmdAppSettings(v args) (any, error) {
	if _, ok := settingsSectionOf(v.str("section")); !ok {
		return nil, fmt.Errorf("no settings section %q; sections: appearance, terminal, layout, keyboard, files", v.str("section"))
	}
	a.openSettings(v.str("section"))
	return nil, nil
}

// --- What the panel changes -------------------------------------------

// setFont sets the terminals' font family; "" is the default.
func (a *App) setFont(family string) {
	a.cfg.Font = strings.TrimSpace(family)
	a.set.font, a.set.fontSeen = a.cfg.Font, a.cfg.Font
	for _, pt := range a.terms {
		pt.term.SetFont(a.termFont())
	}
	a.saveSettingsSoon()
}

// setFontSize sets the terminals' text size, the default's being kept as
// none.
func (a *App) setFontSize(size float32) {
	size = clampFontSize(float32(math.Round(float64(size))))
	if size == defaultFontSize {
		size = 0
	}
	if size == a.cfg.FontSize {
		return
	}
	a.cfg.FontSize = size
	for _, pt := range a.terms {
		pt.term.SetFont(a.termFont())
	}
	a.saveSettingsSoon()
}

func (a *App) setSidebar(shown bool) {
	if shown == a.cfg.sidebarShown() {
		return
	}
	a.cfg.Sidebar = &shown
	a.saveSettings()
}

// saveKeymap writes km to keys.json and uses it.
// keys.json holds what differs from the default keys (and the mods'):
// the chords bound otherwise, and "none" for those removed. When nothing
// differs, there is no keys.json.
func (a *App) saveKeymap(km *keys.Keymap) error {
	user := keys.Diff(a.keysBase, km)
	path := filepath.Join(a.home, "keys.json")
	var b []byte
	var err error
	if user.Empty() {
		if err = os.Remove(path); os.IsNotExist(err) {
			err = nil
		}
	} else {
		b, err = user.Encode()
		if err == nil {
			err = os.MkdirAll(a.home, 0o755)
		}
		if err == nil {
			err = os.WriteFile(path, b, 0o644)
		}
	}
	if err != nil {
		a.notify("keys.json: " + err.Error())
		return err
	}
	// Read back by the watcher, the same bytes are no change.
	a.keysUser, a.keysRaw, a.keysErr = user, b, ""
	a.buildKeymap()
	return nil
}

// resetKeys removes keys.json: the default bindings apply again.
func (a *App) resetKeys() {
	if err := os.Remove(filepath.Join(a.home, "keys.json")); err != nil && !os.IsNotExist(err) {
		a.notify("keys.json: " + err.Error())
		return
	}
	a.loadKeys()
	a.notify("Key bindings reset to the defaults")
}

// bindKey binds chord to the action spec in section, in place of replace
// when it is set. A chord taken by another action moves, and says so.
func (a *App) bindKey(section string, chord keys.Chord, spec string, replace *keys.Chord) error {
	act, err := keys.ParseAction(spec)
	if err != nil {
		return err
	}
	km := a.keymap.Clone()
	if replace != nil {
		km.Unbind(section, *replace)
	}
	old, had := km.Bind(section, chord, act)
	if err := a.saveKeymap(km); err != nil {
		return err
	}
	if had && old.Spec() != act.Spec() {
		a.notify(fmt.Sprintf("%s now runs %s, no longer %s", chord, a.actionTitle(section, act.Spec()), a.actionTitle(section, old.Spec())))
	}
	return nil
}

func (a *App) unbindKey(section string, chord keys.Chord) {
	km := a.keymap.Clone()
	if km.Unbind(section, chord) {
		a.saveKeymap(km)
	}
}

// resetBinding gives an action its default chords again, and only them.
func (a *App) resetBinding(section, spec string) {
	km := a.keymap.Clone()
	for _, c := range km.ChordsFor(section, spec) {
		km.Unbind(section, c)
	}
	act, err := keys.ParseAction(spec)
	if err != nil {
		return
	}
	for _, c := range a.keysBase.ChordsFor(section, spec) {
		if old, had := km.Bind(section, c, act); had && old.Spec() != spec {
			a.notify(fmt.Sprintf("%s now runs %s, no longer %s", c, a.actionTitle(section, spec), a.actionTitle(section, old.Spec())))
		}
	}
	a.saveKeymap(km)
}

// --- Describing bindings ----------------------------------------------

// sendTitles name the text the default terminal keys type.
var sendTitles = map[string]string{
	"\x17":  "Delete the word before the cursor",
	"\x1bb": "Move the cursor a word left",
	"\x1bf": "Move the cursor a word right",
	"\x01":  "Move the cursor to the line's start",
	"\x05":  "Move the cursor to the line's end",
	"\x15":  "Delete to the line's start",
	"\x0b":  "Delete to the line's end",
}

// foreignTitles name herdr-app's commands this app does not run.
var foreignTitles = map[string]string{
	"layout.switch": "Switch layout", "terminal.copy": "Copy", "terminal.paste": "Paste",
	"terminal.paste_image": "Paste an image", "terminal.peek": "Peek at a terminal",
}

var directionWords = map[string]string{"left": "left", "right": "right", "up": "up", "down": "down"}

// actionTitle names what an action does, for people.
func (a *App) actionTitle(section, spec string) string {
	act, err := keys.ParseAction(spec)
	switch {
	case err != nil:
		return spec
	case act.Terminal:
		return "Pass the key to the program"
	case act.Command == "":
		if t, ok := sendTitles[act.Send]; ok {
			return t
		}
		return "Type " + strconv.QuoteToASCII(act.Send)
	}
	if c := a.findCommand(spec); c != nil {
		return c.Title
	}
	switch act.Command {
	case "space.select":
		return "Go to space " + act.Arg
	case "tab.select":
		return "Go to tab " + act.Arg
	case "pane.go":
		return "Focus the pane " + directionWords[act.Arg]
	case "pane.swap":
		return "Swap with the pane " + directionWords[act.Arg]
	case "layout.toggle":
		return "Toggle the sidebar"
	case "mod.open":
		switch act.Arg {
		case "picker":
			return "Search spaces and tabs"
		case "settings":
			return "Settings"
		}
	case "app.settings":
		return "Settings: " + act.Arg
	case "app.theme":
		return "Theme: " + act.Arg
	}
	if t, ok := foreignTitles[act.Command]; ok {
		return t
	}
	if c := a.findCommand(act.Command); c != nil {
		if act.Arg != "" {
			return c.Title + ": " + act.Arg
		}
		return c.Title
	}
	return spec
}

// actionRuns reports whether this app runs the action.
func (a *App) actionRuns(section, spec string) bool {
	act, err := keys.ParseAction(spec)
	switch {
	case err != nil:
		return false
	case act.Terminal || act.Command == "":
		return section == "terminal"
	case section != "global":
		return false // the terminal section's commands are herdr-app's
	}
	return a.findCommand(spec) != nil || a.findCommand(act.Command) != nil
}

// bindingGroups names the groups of the global section, by command prefix.
var bindingGroups = []struct{ prefix, title string }{
	{"space", "Spaces"}, {"tab", "Tabs"}, {"pane", "Panes"}, {"agent", "Agents"},
	{"layout", "Window"}, {"mod", "Window"}, {"app", "Window"}, {"window", "Window"},
}

func (a *App) groupOf(spec string) string {
	act, _ := keys.ParseAction(spec)
	if c := a.findCommand(act.Command); c != nil && c.Mod != "" {
		return a.modName(c.Mod) // a mod's commands go under its name
	}
	prefix, _, _ := strings.Cut(act.Command, ".")
	for _, g := range bindingGroups {
		if g.prefix == prefix {
			return g.title
		}
	}
	return "Other"
}

// bindingRow is an action of a section, with its chords now and by default.
type bindingRow struct {
	section, spec, title, group string
	chords, defaults            []keys.Chord
	runs                        bool
}

// rank orders actions as the palette does, numbers by value.
func (a *App) rank(spec string) (int, string, int) {
	act, _ := keys.ParseAction(spec)
	all := a.allCommands()
	i := slices.IndexFunc(all, func(c *command) bool { return c.Name == spec || c.Name == act.Command })
	if i < 0 {
		i = len(all)
	}
	n, err := strconv.Atoi(act.Arg)
	if err != nil {
		n = -1
	}
	return i, act.Command, n
}

// bindingRows lists the actions of a section: those bound now or by
// default, and, in the global section, every command of the palette.
func (a *App) bindingRows(section string) []bindingRow {
	def := a.keysBase
	specs := map[string]bool{}
	for _, km := range []*keys.Keymap{def, a.keymap} {
		for _, act := range km.Sections[section] {
			specs[act.Spec()] = true
		}
	}
	if section == "global" {
		for _, c := range a.allCommands() {
			// A mod's hidden commands are only out of the palette: keys still run them.
			if (c.hidden && c.Mod == "") || specs[c.Name] {
				continue
			}
			// An unbound command whose bound form names its value
			// (layout.toggle, beside layout.toggle:sidebar) is that one.
			variant := false
			for spec := range specs {
				variant = variant || strings.HasPrefix(spec, c.Name+":")
			}
			if !variant {
				specs[c.Name] = true
			}
		}
	}
	rows := make([]bindingRow, 0, len(specs))
	for spec := range specs {
		r := bindingRow{section: section, spec: spec, title: a.actionTitle(section, spec), runs: a.actionRuns(section, spec),
			chords: a.keymap.ChordsFor(section, spec), defaults: def.ChordsFor(section, spec)}
		if section == "global" {
			r.group = a.groupOf(spec)
		}
		rows = append(rows, r)
	}
	groupIndex := func(g string) int {
		for i, x := range []string{"Spaces", "Tabs", "Panes", "Agents", "Window"} {
			if x == g {
				return i
			}
		}
		if g == "Other" {
			return 99
		}
		return 50 // a mod's, after the app's own
	}
	sort.Slice(rows, func(i, j int) bool {
		gi, gj := groupIndex(rows[i].group), groupIndex(rows[j].group)
		if gi != gj {
			return gi < gj
		}
		ci, ni, ai := a.rank(rows[i].spec)
		cj, nj, aj := a.rank(rows[j].spec)
		if ci != cj {
			return ci < cj
		}
		if ni != nj {
			return ni < nj
		}
		if ai != aj {
			return ai < aj
		}
		return rows[i].spec < rows[j].spec
	})
	return rows
}

func sameChords(x, y []keys.Chord) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func (r bindingRow) matches(q string) bool {
	if q == "" {
		return true
	}
	hay := strings.ToLower(r.title + " " + r.spec + " " + r.group)
	for _, c := range r.chords {
		hay += " " + strings.ToLower(c.String())
	}
	return fuzzy(hay, q)
}

// otherSections are keys.json's sections besides global and terminal:
// herdr-app's layouts' and mods', kept as they are.
func (a *App) otherSections() []string {
	var out []string
	for s := range a.keymap.Sections {
		if s != "global" && s != "terminal" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// --- The panel --------------------------------------------------------

// settingsView is the settings panel: its pages on the left, the page
// on the right.
func (a *App) settingsView(c *ui.Context, k *colors) {
	if !a.settings {
		return
	}
	if a.set.section == "" {
		a.set = settingsState{section: settingsSections[0].id, font: a.cfg.Font, fontSeen: a.cfg.Font, addSection: "global"}
	}
	a.applyRecording()
	w, h := c.Size()
	ui.DialogBase(c, &a.settings, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.4))
		panel.Width(min(860, w-48)).Height(min(620, h-64)).Radius(14).Background(k.popover).Border(1, k.border).
			Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.45)).Clip().Label("Settings")
		panel.Children(func() {
			ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
				a.settingsNav(c, k)
				ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
					ui.Row(c).Height(52).Padding(0, 12, 0, 24).AlignItems(ui.Center).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(k.border).Children(func() {
						for _, s := range settingsSections {
							if s.id == a.set.section {
								ui.Text(c, s.title).FontSize(15).FontWeight(600).TextColor(k.foreground)
							}
						}
						ui.Spacer(c)
						if iconButton(c, k, "x", "Close settings", 28, 15).Tooltip("Close  Esc").Clicked() {
							a.settings = false
						}
					})
					ui.Scroll(c).Grow(1).MinHeight(0).Padding(20, 24, 24, 24).Gap(18).Children(func() {
						switch a.set.section {
						case "appearance":
							a.appearancePage(c, k)
						case "terminal":
							a.terminalPage(c, k)
						case "layout":
							a.layoutPage(c, k)
						case "keyboard":
							a.keyboardPage(c, k)
						case "files":
							a.filesPage(c, k)
						}
					})
				})
			})
		})
	})
	if !a.settings {
		a.set.rec = nil
		a.flushSettings()
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	}
}

func (a *App) settingsNav(c *ui.Context, k *colors) {
	ui.Column(c).Width(196).Shrink(0).Padding(14, 10).Gap(2).Background(k.chrome).BorderWidth(0, 1, 0, 0).BorderColor(k.border).Role(ui.RoleTabList).Children(func() {
		ui.Text(c, "Settings").FontSize(12).FontWeight(600).TextColor(k.muted).Padding(4, 10, 10, 10)
		for _, s := range settingsSections {
			active := s.id == a.set.section
			row := ui.Row(c.Key(s.id)).Height(32).Padding(0, 10).Gap(10).Radius(8).AlignItems(ui.Center).
				Cursor(ui.CursorPointer).Role(ui.RoleTab).Selected(active).Label(s.title)
			col := k.muted
			if active {
				row.Background(k.selected)
				col = k.foreground
			} else if row.Hovered() {
				row.Background(k.hover)
			}
			row.Children(func() {
				ui.Icon(c, glyph(s.icon)).Size(15, 15).TextColor(col)
				ui.Text(c, s.title).FontSize(13).TextColor(k.foreground)
				if s.id == "keyboard" && a.keysErr != "" {
					ui.Spacer(c)
					ui.Icon(c, glyph("alert")).Size(13, 13).TextColor(k.blocked).Tooltip("keys.json has problems")
				}
			})
			if row.Clicked() && !active {
				id := s.id
				a.set.section, a.set.rec = id, nil
			}
		}
		ui.Spacer(c)
		ui.Text(c, "Saved as you change it").FontSize(11).TextColor(k.dim).Padding(0, 10)
	})
}

// group is a titled card of settings.
func settingsGroup(c *ui.Context, k *colors, title, detail string, fn func()) {
	ui.Column(c).Gap(8).Shrink(0).Children(func() {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(k.foreground)
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(k.muted).LineHeight(1.35)
			}
		})
		fn()
	})
}

// settingRow is a setting with its control on the right.
func settingRow(c *ui.Context, k *colors, title, detail string, control func()) ui.Element {
	row := ui.Row(c).Padding(12, 14).Gap(16).Radius(10).Background(k.raised).Border(1, k.border).AlignItems(ui.Center).Shrink(0)
	return row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(13).TextColor(k.foreground)
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(k.muted).LineHeight(1.35)
			}
		})
		control()
	})
}

// fileNote names the file and field a setting is kept in.
func fileNote(c *ui.Context, k *colors, text string) {
	ui.Text(c, text).FontSize(11.5).TextColor(k.dim).Font("monospace")
}

// --- Appearance -------------------------------------------------------

func (a *App) appearancePage(c *ui.Context, k *colors) {
	current := a.cfg.Theme
	if _, ok := theme.Preset(current); !ok {
		current = "graphite"
	}
	settingsGroup(c, k, "Theme", "The colors of the window and its terminals. Programs' colors follow the theme's palette.", func() {
		if a.cfg.Theme != "" && a.cfg.Theme != current {
			ui.Text(c, fmt.Sprintf("settings.json names the theme %q, which this app does not have: graphite is in use.", a.cfg.Theme)).
				FontSize(12).TextColor(k.blocked)
		}
		ui.Grid(c).Columns(3).Gap(12).Children(func() {
			for _, name := range theme.PresetNames {
				a.themeCard(c, k, name, name == current)
			}
		})
		fileNote(c, k, `settings.json  "theme"`)
	})
}

// themeCard shows a theme in small: its chrome, its background, a prompt
// and a few lines in its colors.
func (a *App) themeCard(c *ui.Context, k *colors, name string, selected bool) {
	p, _ := theme.Preset(name)
	t := theme.Resolve(p)
	card := ui.Column(c.Key(name)).Radius(10).Border(1, k.border).Clip().Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label("Theme " + name).Selected(selected)
	if selected {
		card.Border(2, k.accent)
	} else if card.Hovered() {
		card.Border(1, k.muted)
	}
	card.Children(func() {
		ui.Column(c).Height(78).Background(uiColor(t.Background)).Children(func() {
			ui.Row(c).Height(14).Padding(0, 6).Gap(4).AlignItems(ui.Center).Background(uiColor(t.Chrome)).Children(func() {
				for _, i := range []int{1, 3, 2} {
					ui.Box(c).Size(5, 5).Radius(3).Background(uiColor(t.ANSI[i]))
				}
			})
			ui.Column(c).Padding(7, 9).Gap(5).Children(func() {
				type seg struct {
					w float32
					c theme.Color
				}
				line := func(parts ...seg) {
					ui.Row(c).Gap(4).Children(func() {
						for _, p := range parts {
							ui.Box(c).Size(p.w, 5).Radius(3).Background(uiColor(p.c))
						}
					})
				}
				line(seg{10, t.ANSI[2]}, seg{34, t.Foreground}, seg{18, t.ANSI[4]})
				line(seg{22, t.ANSI[5]}, seg{40, t.Muted})
				line(seg{14, t.ANSI[3]}, seg{26, t.Foreground}, seg{12, t.ANSI[6]})
				line(seg{10, t.ANSI[2]}, seg{6, t.Cursor})
			})
		})
		ui.Row(c).Padding(8, 10).Gap(6).AlignItems(ui.Center).Background(k.raised).Children(func() {
			ui.Text(c, name).FontSize(12.5).TextColor(k.foreground).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			ui.Spacer(c)
			if selected {
				ui.Icon(c, glyph("check")).Size(14, 14).TextColor(k.accent)
			}
		})
	})
	if card.Clicked() && !selected {
		a.post(func() { a.do("app.theme", args{"name": name}) })
	}
}

// --- Terminal ---------------------------------------------------------

// defaultFontFamily is the terminals' font when settings.json names none.
const defaultFontFamily = "JetBrains Mono, Geist Mono, DejaVu Sans Mono, monospace"

var (
	monoOnce  sync.Once
	monoFonts []string
)

// monospaceFonts lists the system's monospaced families, once.
func monospaceFonts() []string {
	monoOnce.Do(func() {
		for _, f := range ui.FontFamilies() {
			// Emoji fonts are monospaced too, and no terminal's font.
			if f.Monospaced && !strings.Contains(strings.ToLower(f.Name), "emoji") {
				monoFonts = append(monoFonts, f.Name)
			}
		}
	})
	return monoFonts
}

func (a *App) terminalPage(c *ui.Context, k *colors) {
	settingsGroup(c, k, "Font", "A family, or a list of families separated by commas: the first one installed draws, the others fill in what it lacks.", func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			// A font set elsewhere (the file, a reload) shows, unless it is
			// being typed over.
			if a.cfg.Font != a.set.fontSeen {
				if strings.TrimSpace(a.set.font) == a.set.fontSeen {
					a.set.font = a.cfg.Font
				}
				a.set.fontSeen = a.cfg.Font
			}
			in := ui.TextInput(c, &a.set.font).Grow(1).Placeholder(defaultFontFamily).Label("Font family")
			if in.Submitted() {
				font := a.set.font
				a.post(func() { a.setFont(font) })
			}
			changed := strings.TrimSpace(a.set.font) != a.cfg.Font
			if ui.PrimaryButton(c, "Apply").Disabled(!changed).Clicked() {
				font := a.set.font
				a.post(func() { a.setFont(font) })
			}
			if ui.Button(c, "Default").Disabled(a.cfg.Font == "" && a.set.font == "").Tooltip(defaultFontFamily).Clicked() {
				a.post(func() { a.setFont("") })
			}
		})
		// Installed monospaced families, those matching what is typed.
		typed := a.set.font
		if i := strings.LastIndexByte(typed, ','); i >= 0 {
			typed = typed[i+1:]
		}
		q := strings.ToLower(strings.TrimSpace(typed))
		var found []string
		for _, f := range monospaceFonts() {
			if q == "" || strings.Contains(strings.ToLower(f), q) {
				found = append(found, f)
			}
			if len(found) == 12 {
				break
			}
		}
		if len(found) > 0 {
			ui.Row(c).Gap(6).Wrap().Children(func() {
				for _, f := range found {
					chip := ui.Row(c.Key("font:"+f)).Padding(4, 9).Radius(7).Border(1, k.border).Cursor(ui.CursorPointer).
						Role(ui.RoleButton).Label("Font " + f)
					if f == a.cfg.Font {
						chip.Background(k.selected).Border(1, k.accent)
					} else if chip.Hovered() {
						chip.Background(k.hover)
					}
					chip.Children(func() { ui.Text(c, f).FontSize(12.5).Font(f + ", monospace").TextColor(k.foreground) })
					if chip.Clicked() {
						a.post(func() { a.setFont(f) })
					}
				}
			})
		}
		fileNote(c, k, `settings.json  "font"`)
	})

	settingsGroup(c, k, "Text size", "Also Ctrl and the mouse wheel over a terminal, or the zoom keys.", func() {
		size := a.cfg.fontSize()
		ui.Row(c).Padding(12, 14).Gap(12).Radius(10).Background(k.raised).Border(1, k.border).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "−").Label("Smaller text").Tooltip(withKeys("Smaller text", a.keysFor("app.zoom_out"))).Disabled(size <= minFontSize).Clicked() {
				a.post(func() { a.setFontSize(size - 1) })
			}
			v := float64(size)
			if ui.Slider(c, &v, minFontSize, maxFontSize).Grow(1).Label("Text size"); float32(math.Round(v)) != size {
				a.setFontSize(float32(v))
			}
			if ui.Button(c, "+").Label("Larger text").Tooltip(withKeys("Larger text", a.keysFor("app.zoom_in"))).Disabled(size >= maxFontSize).Clicked() {
				a.post(func() { a.setFontSize(size + 1) })
			}
			ui.Textf(c, "%g pt", size).FontSize(13).TextColor(k.foreground).Width(46).TextAlign(ui.End)
			if ui.Button(c, "Reset").Tooltip(withKeys(fmt.Sprintf("Reset to %d", defaultFontSize), a.keysFor("app.zoom_reset"))).Disabled(a.cfg.FontSize == 0).Clicked() {
				a.post(func() { a.setFontSize(defaultFontSize) })
			}
		})
		fileNote(c, k, `settings.json  "font_size"`)
	})

	settingsGroup(c, k, "Preview", "", func() {
		family := a.termFont().Family
		ui.Column(c).Padding(12, 14).Gap(2).Radius(10).Background(k.background).Border(1, k.border).Clip().Children(func() {
			fs := a.cfg.fontSize()
			ui.Row(c).Children(func() {
				ui.Text(c, "~/src/shiori").Font(family).FontSize(fs).TextColor(k.ansi[4]).SingleLine().Shrink(0)
				ui.Text(c, " $ go test ./...").Font(family).FontSize(fs).TextColor(k.terminalForeground).SingleLine().Shrink(0)
			})
			ui.Row(c).Children(func() {
				ui.Text(c, "ok").Font(family).FontSize(fs).TextColor(k.ansi[2]).SingleLine().Shrink(0)
				ui.Text(c, "   internal/app  0O 1lI |{}[] => != ->").Font(family).FontSize(fs).TextColor(k.terminalForeground).SingleLine().Shrink(0)
			})
			ui.Row(c).Children(func() {
				ui.Text(c, "FAIL").Font(family).FontSize(fs).TextColor(k.ansi[1]).SingleLine().Shrink(0)
				ui.Text(c, " internal/core").Font(family).FontSize(fs).TextColor(k.terminalForeground).SingleLine().Shrink(0)
				ui.Box(c).Width(fs * 0.6).Height(fs * 1.2).MarginStart(fs * 0.6).Background(k.cursor).Shrink(0)
			})
		})
	})
}

// --- Layout -----------------------------------------------------------

func (a *App) layoutPage(c *ui.Context, k *colors) {
	settingsGroup(c, k, "Sidebar", "", func() {
		// The whole row toggles; the switch's own clicks are the switch's.
		shown := a.cfg.sidebarShown()
		overSwitch := false
		row := settingRow(c, k, "Show the sidebar", withKeys("Spaces and agents, on the left.", a.keysFor("layout.toggle:sidebar")), func() {
			on := shown
			sw := ui.Switch(c, &on).Label("Sidebar shown")
			overSwitch = sw.Hovered()
			if on != shown {
				a.post(func() { a.setSidebar(on) })
			}
		})
		row.Cursor(ui.CursorPointer).Label("Show the sidebar")
		if row.Clicked() && !overSwitch {
			a.post(func() { a.setSidebar(!shown) })
		}
		fileNote(c, k, `settings.json  "sidebar"`)
		ui.Text(c, "Drag the sidebar's edge to resize it; double-click the edge for its first width.").FontSize(12).TextColor(k.muted)
	})
}

// --- Keyboard ---------------------------------------------------------

func (a *App) keyboardPage(c *ui.Context, k *colors) {
	if a.keysErr != "" {
		ui.Row(c).Padding(10, 12).Gap(10).Radius(10).Background(k.blocked.Alpha(0.12)).Border(1, k.blocked.Alpha(0.4)).Children(func() {
			ui.Icon(c, glyph("alert")).Size(15, 15).TextColor(k.blocked)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, "keys.json has problems; the bindings it could read are in use.").FontSize(12.5).FontWeight(600).TextColor(k.foreground)
				ui.Text(c, a.keysErr).FontSize(11.5).TextColor(k.muted).Font("monospace").LineHeight(1.35)
				ui.Text(c, "Changing a binding here rewrites the file with the readable bindings.").FontSize(11.5).TextColor(k.muted)
			})
		})
	}
	custom := len(a.keysRaw) > 0
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c, &a.set.query).Grow(1).Placeholder("Filter by action, command or keys").Label("Filter bindings")
		if ui.Button(c, "Reset all").Disabled(!custom || a.set.confirmReset).Tooltip("Remove keys.json and use the default bindings").Clicked() {
			a.set.confirmReset = true
		}
	})
	if a.set.confirmReset && custom {
		// Asked here: a dialog over the panel would not take the keys.
		ui.Row(c).Padding(10, 12).Gap(10).Radius(10).Background(k.raised).Border(1, k.blocked.Alpha(0.5)).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, "Reset every key binding?").FontSize(13).FontWeight(600).TextColor(k.foreground)
				ui.Text(c, "keys.json is removed and the default bindings apply.").FontSize(12).TextColor(k.muted)
			})
			if ui.Button(c, "Cancel").Clicked() {
				a.set.confirmReset = false
			}
			if ui.PrimaryButton(c, "Reset").Label("Reset every binding").AutoFocus().Clicked() {
				a.set.confirmReset = false
				a.post(a.resetKeys)
			}
		})
	} else {
		a.set.confirmReset = false
	}
	ui.Text(c, "Click + to add keys to an action, a key to change it, its × to remove it. Esc cancels.").FontSize(12).TextColor(k.muted)

	q := strings.ToLower(strings.TrimSpace(a.set.query))
	a.bindingSection(c, k, "Window", "Keys that work wherever the focus is.", "global", q)
	a.bindingSection(c, k, "Terminal", "Keys a terminal takes before the program sees them.", "terminal", q)
	for _, s := range a.otherSections() {
		a.bindingSection(c, k, s, "A section of herdr-app's, kept in keys.json as it is.", s, q)
	}
	a.addBindingForm(c, k)
	fileNote(c, k, "keys.json  (herdr-app's format: {\"section\": {\"ctrl-shift-t\": \"tab.new\"}})")
}

func (a *App) bindingSection(c *ui.Context, k *colors, title, detail, section, q string) {
	var rows []bindingRow
	for _, r := range a.bindingRows(section) {
		if r.matches(q) {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return
	}
	settingsGroup(c, k, title, detail, func() {
		ui.Column(c.Key("sec:"+section)).Radius(10).Background(k.raised).Border(1, k.border).Clip().Children(func() {
			group := ""
			for i, r := range rows {
				if r.group != group {
					group = r.group
					ui.Text(c.Key("group:"+group), group).FontSize(11).FontWeight(600).TextColor(k.dim).Padding(10, 14, 4, 14)
				} else if i > 0 {
					ui.Box(c.Key("sep:"+r.spec)).Height(1).Background(k.border.Alpha(0.6)).Margin(0, 14)
				}
				a.bindingRowView(c, k, r)
			}
		})
	})
}

func (a *App) bindingRowView(c *ui.Context, k *colors, r bindingRow) {
	row := ui.Row(c.Key("row:"+r.section+"\x00"+r.spec)).MinHeight(38).Padding(4, 8, 4, 14).Gap(10).AlignItems(ui.Center).Label("Binding " + r.title)
	if row.Hovered() {
		row.Background(k.hover.Alpha(0.5))
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				col := k.foreground
				if !r.runs {
					col = k.muted
				}
				ui.Text(c, r.title).FontSize(13).TextColor(col).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				if !r.runs {
					ui.Box(c).Padding(0, 6).Radius(8).Border(1, k.border).Shrink(0).Tooltip("This app does not run it; it stays for herdr-app, which reads the same file").Children(func() {
						ui.Text(c, "herdr-app only").FontSize(10.5).TextColor(k.dim)
					})
				}
			})
			ui.Text(c, r.spec).FontSize(11).TextColor(k.dim).Font("monospace").SingleLine().Ellipsis("…")
		})
		ui.Row(c).Gap(4).AlignItems(ui.Center).Wrap().Justify(ui.End).Shrink(1).MinWidth(0).Children(func() {
			rec := a.set.rec
			recording := rec != nil && rec.section == r.section && rec.spec == r.spec
			for _, ch := range r.chords {
				if recording && rec.replace != nil && *rec.replace == ch {
					a.recorder(c, k, r)
					continue
				}
				a.chordChip(c, k, r, ch)
			}
			if recording && rec.replace == nil {
				a.recorder(c, k, r)
			} else if len(r.chords) == 0 {
				ui.Text(c, "No keys").FontSize(11.5).TextColor(k.dim)
			}
		})
		add := iconButton(c, k, "plus", "Add keys for "+r.title, 24, 13).Tooltip("Add keys")
		if add.Clicked() {
			a.set.rec = &recording{section: r.section, spec: r.spec}
		}
		changed := !sameChords(r.chords, r.defaults)
		reset := iconButton(c, k, "reset", "Reset "+r.title, 24, 13).Opacity(map[bool]float32{true: 1, false: 0}[changed]).IgnoreInput(!changed)
		if changed {
			tip := "Reset to no keys"
			if len(r.defaults) > 0 {
				names := make([]string, len(r.defaults))
				for i, d := range r.defaults {
					names[i] = d.String()
				}
				tip = "Reset to " + strings.Join(names, ", ")
			}
			reset.Tooltip(tip)
		}
		if reset.Clicked() {
			section, spec := r.section, r.spec
			a.post(func() { a.resetBinding(section, spec) })
		}
	})
}

// chordChip is a binding's keys: a click records new ones in its place,
// its × removes it.
func (a *App) chordChip(c *ui.Context, k *colors, r bindingRow, ch keys.Chord) {
	chip := ui.Row(c.Key("chord:"+ch.Spec())).Height(24).Padding(0, 4, 0, 8).Gap(2).Radius(6).AlignItems(ui.Center).
		Background(k.selected).Border(1, k.border).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(ch.String()).Tooltip("Click to change")
	hovered, removed := chip.Hovered(), false
	if hovered {
		chip.Border(1, k.accent)
	}
	chip.Children(func() {
		ui.Text(c, ch.String()).FontSize(11.5).TextColor(k.foreground).SingleLine()
		label := ""
		if hovered {
			label = "Remove " + ch.String()
		}
		x := ui.Box(c).Size(16, 16).Radius(4).Center().Opacity(map[bool]float32{true: 1, false: 0.35}[hovered]).Role(ui.RoleButton).Label(label)
		if x.Hovered() {
			x.Background(k.hover)
		}
		x.Children(func() { ui.Icon(c, glyph("x")).Size(10, 10).TextColor(k.muted) })
		if x.Clicked() {
			removed = true
			section := r.section
			a.post(func() { a.unbindKey(section, ch) })
		}
	})
	if chip.Clicked() && !removed {
		replace := ch
		a.set.rec = &recording{section: r.section, spec: r.spec, replace: &replace}
	}
}

// recorder takes the next key pressed, with its modifiers, as a binding.
func (a *App) recorder(c *ui.Context, k *colors, r bindingRow) {
	rec := a.set.rec
	box := ui.Row(c.Key("recorder")).Height(24).Padding(0, 9).Radius(6).AlignItems(ui.Center).Background(k.accent.Alpha(0.15)).
		Border(1, k.accent).Focusable().FocusRing(false).Label("Press keys for " + r.title).AutoFocus()
	box.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputKeyDown {
			return ev.Kind == ui.InputText // what the keys type is not text here
		}
		if ev.Key == ui.KeyEscape && ev.Mods == 0 {
			rec.cancel = true
			return true
		}
		if chord, ok := chordOf(ev.Mods, ev.Key); ok {
			rec.got = &chord
		}
		return true // a modifier alone waits for its key
	})
	if box.PressedOutside() {
		rec.cancel = true
	}
	box.Children(func() {
		ui.Text(c, "Press keys…").FontSize(11.5).TextColor(k.accent).SingleLine()
	})
}

// applyRecording binds what a recorder took, before the frame shows it.
func (a *App) applyRecording() {
	rec := a.set.rec
	switch {
	case rec == nil:
	case rec.cancel:
		a.set.rec = nil
	case rec.got != nil:
		a.set.rec = nil
		if err := a.bindKey(rec.section, *rec.got, rec.spec, rec.replace); err != nil {
			a.notify(err.Error())
		}
	}
}

// addBindingForm binds keys to any action, in any section: what the
// lists do not offer, as text a program should type.
func (a *App) addBindingForm(c *ui.Context, k *colors) {
	settingsGroup(c, k, "Add a binding", "Any action keys.json takes: a command (tab.new), a command with its value (pane.go:left), send:<text> to type text in the terminal, or terminal to give the key to the program.", func() {
		sections := []string{"global", "terminal"}
		sections = append(sections, a.otherSections()...)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Select(c, &a.set.addSection, sections).Width(130).Label("Section")
			ui.TextInput(c, &a.set.addAction).Grow(1).Placeholder("tab.new, pane.go:left, send:…").Label("Action").Font("monospace")
			spec := strings.TrimSpace(a.set.addAction)
			_, err := keys.ParseAction(spec)
			rec := a.set.rec
			adding := rec != nil && rec.section == a.set.addSection && rec.spec == spec && rec.replace == nil && spec != "" && !slices.ContainsFunc(a.bindingRows(rec.section), func(r bindingRow) bool { return r.spec == spec })
			if adding {
				a.recorder(c, k, bindingRow{section: rec.section, spec: spec, title: a.actionTitle(rec.section, spec)})
			} else if ui.PrimaryButton(c, "Record keys").Disabled(spec == "" || err != nil).Clicked() {
				a.set.rec = &recording{section: a.set.addSection, spec: spec}
			}
		})
		if spec := strings.TrimSpace(a.set.addAction); spec != "" {
			if _, err := keys.ParseAction(spec); err != nil {
				ui.Text(c, err.Error()).FontSize(11.5).TextColor(k.blocked)
			} else {
				msg, col := a.actionTitle(a.set.addSection, spec), k.muted
				if !a.actionRuns(a.set.addSection, spec) {
					msg, col = msg+": this app does not run it here, herdr-app may", k.dim
				}
				ui.Text(c, msg).FontSize(11.5).TextColor(col)
			}
		}
	})
}

// --- Files ------------------------------------------------------------

func (a *App) filesPage(c *ui.Context, k *colors) {
	settingsGroup(c, k, "Configuration files", "Edits made in an editor apply within a second; a file that does not read keeps what was in use.", func() {
		a.fileRow(c, k, "settings.json", a.settingsSummary())
		keysState := "Not created: the default bindings are in use."
		if len(a.keysRaw) > 0 {
			n := 0
			for _, m := range a.keymap.Sections {
				n += len(m)
			}
			keysState = fmt.Sprintf("%s in %s.", plural(n, "binding", "bindings"), plural(len(a.keymap.Sections), "section", "sections"))
			if a.keysErr != "" {
				keysState += " It has problems: see Keyboard."
			}
		}
		a.fileRow(c, k, "keys.json", keysState)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "Reload from disk").Tooltip("app.reload").Clicked() {
				a.post(func() { a.do("app.reload", nil) })
			}
		})
	})
	settingsGroup(c, k, "Where they are", "", func() {
		settingRow(c, k, "Folder", tildePath(a.home)+"  ($SHIORI_HOME, else ~/.config/shiori)", func() {
			if ui.Button(c, "Copy path").Clicked() {
				c.WriteClipboard(a.home)
				a.post(func() { a.notify("Copied " + tildePath(a.home)) })
			}
			if ui.Button(c, "Open").Clicked() {
				c.OpenURL("file://" + a.home)
			}
		})
		session := a.session
		if session == "" {
			session = "default"
		}
		settingRow(c, k, "herdr-core session", session+"  ($HC_SESSION)", func() {})
	})
}

func (a *App) settingsSummary() string {
	if _, err := os.Stat(filepath.Join(a.home, "settings.json")); err != nil {
		return "Not created: the defaults are in use."
	}
	themeName := a.cfg.Theme
	if themeName == "" {
		themeName = "graphite"
	}
	font := a.cfg.Font
	if font == "" {
		font = "default font"
	}
	sidebar := "sidebar shown"
	if !a.cfg.sidebarShown() {
		sidebar = "sidebar hidden"
	}
	return fmt.Sprintf("%s theme, %s at %g pt, %s.", themeName, font, a.cfg.fontSize(), sidebar)
}

func (a *App) fileRow(c *ui.Context, k *colors, name, state string) {
	path := filepath.Join(a.home, name)
	settingRow(c, k, name, state, func() {
		if iconButton(c, k, "copy", "Copy the path of "+name, 28, 14).Tooltip(tildePath(path)).Clicked() {
			c.WriteClipboard(path)
			a.post(func() { a.notify("Copied " + tildePath(path)) })
		}
	})
}
