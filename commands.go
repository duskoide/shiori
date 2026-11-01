package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/duskoide/herdr-mygo/internal/agent"
	"github.com/duskoide/herdr-mygo/internal/core"
	"github.com/duskoide/herdr-mygo/internal/layout"
	"github.com/duskoide/herdr-mygo/internal/theme"
)

// command is something the person or an agent can do: keys, menus, the
// palette and "herdr-mygo do" all run commands by name, as herdr-app's.
type command struct {
	Name   string   `json:"name"`
	Title  string   `json:"title"`
	Args   []argDef `json:"args"`
	About  string   `json:"about"`
	hidden bool     // not in the palette
	run    func(a *App, args args) (any, error)
}

type argDef struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// args are a command's arguments by name.
type args map[string]any

func (v args) str(name string) string {
	switch x := v[name].(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func (v args) int(name string) (int, bool) {
	switch x := v[name].(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return n, err == nil
	}
	return 0, false
}

func arg(name string) argDef      { return argDef{Name: name} }
func required(name string) argDef { return argDef{Name: name, Required: true} }

// commands is every command, in the palette's order.
var commands []*command

func init() {
	commands = []*command{
		{Name: "space.new", Title: "New space", About: "Make a space with a terminal in folder", Args: []argDef{arg("name"), arg("folder"), arg("tab"), arg("argv"), arg("task"), arg("machine")}, run: (*App).cmdSpaceNew},
		{Name: "space.close", Title: "Close space", About: "Close a space and end its terminals", Args: []argDef{arg("id"), arg("confirmed")}, run: (*App).cmdSpaceClose},
		{Name: "space.rename", Title: "Rename space", Args: []argDef{arg("id"), arg("name")}, run: (*App).cmdSpaceRename},
		{Name: "space.select", Title: "Select space", Args: []argDef{arg("index"), arg("id")}, hidden: true, run: (*App).cmdSpaceSelect},
		{Name: "space.next", Title: "Next space", run: func(a *App, _ args) (any, error) { return nil, a.cycleSpace(1) }},
		{Name: "space.prev", Title: "Previous space", run: func(a *App, _ args) (any, error) { return nil, a.cycleSpace(-1) }},
		{Name: "space.move", Title: "Move space", Args: []argDef{required("id"), required("index")}, hidden: true, run: (*App).cmdSpaceMove},
		{Name: "tab.new", Title: "New tab", About: "Open a tab with a terminal in the focused folder", Args: []argDef{arg("name"), arg("space"), arg("folder"), arg("argv"), arg("task"), arg("mod"), arg("props")}, run: (*App).cmdTabNew},
		{Name: "tab.close", Title: "Close tab", Args: []argDef{arg("id"), arg("confirmed")}, run: (*App).cmdTabClose},
		{Name: "tab.rename", Title: "Rename tab", Args: []argDef{arg("id"), arg("name")}, run: (*App).cmdTabRename},
		{Name: "tab.select", Title: "Select tab", Args: []argDef{arg("index"), arg("id")}, hidden: true, run: (*App).cmdTabSelect},
		{Name: "tab.next", Title: "Next tab", run: func(a *App, _ args) (any, error) { return nil, a.cycleTab(1) }},
		{Name: "tab.prev", Title: "Previous tab", run: func(a *App, _ args) (any, error) { return nil, a.cycleTab(-1) }},
		{Name: "tab.move", Title: "Move tab", Args: []argDef{required("id"), required("index")}, hidden: true, run: (*App).cmdTabMove},
		{Name: "pane.split", Title: "Split pane", Args: []argDef{required("direction"), arg("pane"), arg("argv"), arg("task"), arg("mod"), arg("props")}, hidden: true, run: (*App).cmdPaneSplit},
		{Name: "pane.split:right", Title: "Split right", run: func(a *App, _ args) (any, error) { return a.cmdPaneSplit(args{"direction": "right"}) }},
		{Name: "pane.split:down", Title: "Split down", run: func(a *App, _ args) (any, error) { return a.cmdPaneSplit(args{"direction": "down"}) }},
		{Name: "pane.close", Title: "Close pane", Args: []argDef{arg("pane")}, run: (*App).cmdPaneClose},
		{Name: "pane.zoom", Title: "Zoom pane", Args: []argDef{arg("pane")}, run: (*App).cmdPaneZoom},
		{Name: "pane.focus", Title: "Focus pane", Args: []argDef{arg("pane"), arg("terminal")}, hidden: true, run: (*App).cmdPaneFocus},
		{Name: "pane.go", Title: "Go to pane", Args: []argDef{required("direction")}, hidden: true, run: (*App).cmdPaneGo},
		{Name: "pane.swap", Title: "Swap pane", Args: []argDef{required("direction")}, hidden: true, run: (*App).cmdPaneSwap},
		{Name: "agent.focus", Title: "Focus agent", Args: []argDef{required("terminal")}, hidden: true, run: (*App).cmdAgentFocus},
		{Name: "agent.next", Title: "Next agent", run: func(a *App, _ args) (any, error) { return nil, a.cycleAgent(1) }},
		{Name: "agent.prev", Title: "Previous agent", run: func(a *App, _ args) (any, error) { return nil, a.cycleAgent(-1) }},
		{Name: "terminal.open", Title: "Open terminal", About: "Start a terminal as a new tab, or right/down of the focused pane", Args: []argDef{arg("argv"), arg("folder"), arg("place"), arg("name")}, hidden: true, run: (*App).cmdTerminalOpen},
		{Name: "terminal.keep", Title: "Keep terminal", About: "Give a running terminal without a pane a pane", Args: []argDef{required("terminal"), arg("place")}, hidden: true, run: (*App).cmdTerminalKeep},
		{Name: "terminal.park", Title: "Park terminal", About: "Take a terminal's pane away; it keeps running", Args: []argDef{required("terminal")}, hidden: true, run: (*App).cmdTerminalPark},
		{Name: "terminal.kill", Title: "Kill terminal", Args: []argDef{required("terminal")}, hidden: true, run: (*App).cmdTerminalKill},
		{Name: "terminal.pin", Title: "Pin agent", Args: []argDef{required("terminal"), required("pinned")}, hidden: true, run: (*App).cmdTerminalPin},
		{Name: "layout.toggle", Title: "Toggle sidebar", Args: []argDef{arg("id")}, run: (*App).cmdLayoutToggle},
		{Name: "mod.open", Title: "Open", Args: []argDef{required("name")}, hidden: true, run: (*App).cmdModOpen},
		{Name: "app.palette", Title: "Command palette", run: func(a *App, _ args) (any, error) { a.openPalette(""); return nil, nil }},
		{Name: "app.help", Title: "Keyboard shortcuts", run: func(a *App, _ args) (any, error) { a.openPalette(""); return nil, nil }},
		{Name: "app.settings", Title: "Settings", Args: []argDef{arg("section")}, run: func(a *App, _ args) (any, error) { a.settings = true; return nil, nil }},
		{Name: "app.theme", Title: "Theme", Args: []argDef{required("name")}, hidden: true, run: (*App).cmdAppTheme},
		{Name: "app.zoom_in", Title: "Zoom in", run: func(a *App, _ args) (any, error) { a.zoomBy(1); return nil, nil }},
		{Name: "app.zoom_out", Title: "Zoom out", run: func(a *App, _ args) (any, error) { a.zoomBy(-1); return nil, nil }},
		{Name: "app.zoom_reset", Title: "Reset zoom", run: func(a *App, _ args) (any, error) { a.zoomBy(0); return nil, nil }},
		{Name: "app.quit", Title: "Quit", About: "Close the window; terminals keep running", run: func(a *App, _ args) (any, error) { mygo.App.Quit(); return nil, nil }},
		{Name: "window.close", Title: "Close window", hidden: true, run: func(a *App, _ args) (any, error) {
			if a.win != nil {
				a.win.Close()
			}
			return nil, nil
		}},
	}
}

// unsupported are herdr-app's commands this app does not implement.
var unsupported = []string{
	"terminal.peek", "terminal.show", "terminal.right_click", "terminal.copy", "terminal.paste", "terminal.paste_image",
	"tab.settle", "tab.unsettle", "overlay.close", "mod.clone",
	"layout.switch", "layout.clone", "layout.set", "layout.show", "layout.hide", "layout.resize", "layout.select", "layout.reset",
	"app.welcome", "app.whats_new", "app.feedback", "app.check_updates", "app.update_core", "app.reset", "app.update",
	"app.hide", "app.hide_others", "app.show_all", "window.minimize", "window.maximize",
}

func findCommand(name string) *command {
	for _, c := range commands {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// do runs a command by name with its arguments, on the main thread.
func (a *App) do(name string, v args) (any, error) {
	c := findCommand(name)
	if c == nil {
		if slices.Contains(unsupported, name) {
			return nil, fmt.Errorf("%s is not supported by this app", name)
		}
		return nil, fmt.Errorf("unknown command %q", name)
	}
	if v == nil {
		v = args{}
	}
	var unknown []string
	for k := range v {
		ok := false
		for _, d := range c.Args {
			ok = ok || d.Name == k
		}
		if !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		var valid []string
		for _, d := range c.Args {
			valid = append(valid, d.Name)
		}
		return nil, fmt.Errorf("unknown argument(s): %s; valid arguments: %s", strings.Join(unknown, ", "), strings.Join(valid, ", "))
	}
	for _, d := range c.Args {
		if d.Required && v.str(d.Name) == "" {
			return nil, fmt.Errorf("%s needs %s", name, d.Name)
		}
	}
	return c.run(a, v)
}

// doAction runs a key's action: "cmd" or "cmd:value", whose value fills
// the command's first argument, or "index" when it is a number.
func (a *App) doAction(command, value string) {
	v := args{}
	if value != "" {
		c := findCommand(command)
		if c == nil {
			a.notify("unknown command " + command)
			return
		}
		if n, err := strconv.Atoi(value); err == nil && hasArg(c, "index") {
			v["index"] = n
		} else if len(c.Args) > 0 {
			v[c.Args[0].Name] = value
		}
	}
	if _, err := a.do(command, v); err != nil {
		a.notify(err.Error())
	}
}

func hasArg(c *command, name string) bool {
	for _, d := range c.Args {
		if d.Name == name {
			return true
		}
	}
	return false
}

var errNoCore = fmt.Errorf("not connected to herdr-core")

func (a *App) ready() error {
	if a.core == nil || !a.loaded {
		return errNoCore
	}
	return nil
}

// spawn starts a terminal for a pane in a tab of a space, off the main
// thread, and calls place with its address once it runs.
func (a *App) spawn(spaceID, tabID, cwd string, argv []string, place func(addr string)) {
	be := a.core
	labels := map[string]string{"herdr.space": spaceID, "herdr.tab": tabID, "herdr.seen": "0"}
	a.obtain(tabID, func(ctx context.Context) (string, error) {
		return be.Run(ctx, core.RunParams{Argv: argv, Cwd: cwd, Labels: labels, Keep: true})
	}, place)
}

// obtain gets a terminal with work, off the main thread, and calls place
// with its address once it is there and no pane shows it.
func (a *App) obtain(tabID string, work func(ctx context.Context) (string, error), place func(addr string)) {
	// Until place runs, the core may tell of the terminal: it is not an
	// orphan to adopt.
	a.spawning[tabID]++
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		addr, err := work(ctx)
		a.post(func() {
			if a.spawning[tabID]--; a.spawning[tabID] <= 0 {
				delete(a.spawning, tabID)
			}
			if err != nil {
				a.notify("cannot start a terminal: " + err.Error())
				return
			}
			if a.doc == nil {
				return
			}
			delete(a.gone, addr)
			a.pending[addr] = time.Now()
			if _, _, p := a.doc.PaneOfTerminal(addr); p == nil {
				place(addr)
			}
			a.changed()
		})
	}()
}

// focusedFolder is where new terminals start: the focused pane's folder,
// else the space's.
func (a *App) focusedFolder() string {
	if cwd := a.cwdOf(a.pane()); cwd != "" {
		return cwd
	}
	if s := a.space(); s != nil && s.Folder != "" {
		return s.Folder
	}
	return homeDir()
}

// argvOf reads the argv argument: a list, a JSON array in a string
// ('["claude","--x"]'), or words split on whitespace.
func argvOf(v args) ([]string, error) {
	switch x := v["argv"].(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("argv holds strings only")
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		return x, nil
	case string:
		x = strings.TrimSpace(x)
		if strings.HasPrefix(x, "[") {
			var out []string
			if err := json.Unmarshal([]byte(x), &out); err != nil {
				return nil, fmt.Errorf("argv is a JSON array of strings: %w", err)
			}
			return out, nil
		}
		return strings.Fields(x), nil
	}
	return nil, nil
}

// launchArgv is what a new terminal runs: the argv given, with the task
// as its first message, in a shell that stays when the program exits, as
// herdr-app does. Without an argv it is nil: the default shell.
func launchArgv(v args) ([]string, error) {
	argv, err := argvOf(v)
	if err != nil || len(argv) == 0 {
		return nil, err
	}
	cmd := slices.Clone(argv)
	if task := v.str("task"); task != "" {
		cmd = append(cmd, task)
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	if runtime.GOOS == "windows" {
		quoted := make([]string, len(cmd))
		for i, w := range cmd {
			quoted[i] = "'" + strings.ReplaceAll(w, "'", "''") + "'"
		}
		return []string{"powershell.exe", "-NoExit", "-Command", "& " + strings.Join(quoted, " ")}, nil
	}
	// macOS terminal sessions load login profiles.
	shellFlag, rcFlag := "-ic", "-i"
	if runtime.GOOS == "darwin" {
		shellFlag, rcFlag = "-lic", "-li"
	}
	script := `"$@"; exec "$0" ` + rcFlag
	if filepath.Base(sh) == "fish" {
		script = "command $argv[2..]; exec $argv[1] " + rcFlag
	}
	return append([]string{sh, shellFlag, script, sh}, cmd...), nil
}

// noMods rejects the mod and props arguments: this app has no mods.
func noMods(v args) error {
	if v.str("mod") != "" || v["props"] != nil {
		return fmt.Errorf("mods are not supported by this app")
	}
	return nil
}

// localMachine reports whether name is this machine.
func localMachine(name string) bool {
	if name == "" || name == "local" || name == "localhost" {
		return true
	}
	h, _ := os.Hostname()
	return h != "" && name == h
}

func (a *App) cmdSpaceNew(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if m := v.str("machine"); !localMachine(m) {
		return nil, fmt.Errorf("machine %q is not supported by this app: only this machine", m)
	}
	argv, err := launchArgv(v)
	if err != nil {
		return nil, err
	}
	folder := v.str("folder")
	if folder == "" {
		if folder = a.cwdOf(a.pane()); folder == "" {
			folder = homeDir()
		}
	}
	// The space appears with its first terminal: an empty one would be
	// pruned.
	name, tabName, spaceID, tabID := v.str("name"), v.str("tab"), layout.NewID("sp"), layout.NewID("tb")
	a.spawn(spaceID, tabID, folder, argv, func(addr string) {
		s := a.doc.Space(spaceID)
		if s == nil {
			s = a.doc.AddSpace(name, folder)
			s.ID = spaceID
		}
		t, p := s.AddTab(tabName, addr)
		retab(t, tabID)
		a.activeSpace, a.activeTab[s.ID] = s.ID, t.ID
		a.setFocus(t, p.ID)
		a.focusReq = p.ID
	})
	return map[string]string{"space": spaceID, "tab": tabID}, nil
}

// retab gives a new tab the id its terminal was labelled with.
func retab(t *layout.Tab, id string) {
	t.ID = id
	if t.Root != nil {
		t.Root.ID = id
	}
}

func (a *App) spaceArg(v args) (*layout.Space, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if id := v.str("id"); id != "" {
		if s := a.doc.Space(id); s != nil {
			return s, nil
		}
		return nil, fmt.Errorf("no space %q", id)
	}
	if s := a.space(); s != nil {
		return s, nil
	}
	return nil, fmt.Errorf("no space")
}

func (a *App) cmdSpaceClose(v args) (any, error) {
	s, err := a.spaceArg(v)
	if err != nil {
		return nil, err
	}
	if v.str("confirmed") != "true" && a.liveTerminals(s) > 0 {
		id := s.ID
		a.askConfirm(fmt.Sprintf("Close space %q?", a.spaceLabel(s)), "Its terminals end.", func() {
			a.do("space.close", args{"id": id, "confirmed": true})
		})
		return nil, nil
	}
	for _, t := range s.Tabs {
		a.killPanes(t.Panes())
	}
	a.doc.RemoveSpace(s.ID)
	a.changed()
	return nil, nil
}

func (a *App) liveTerminals(s *layout.Space) int {
	n := 0
	for _, t := range s.Tabs {
		n += len(t.Panes())
	}
	return n
}

// killPanes ends the terminals of panes being closed.
func (a *App) killPanes(panes []*layout.Node) {
	be := a.core
	for _, p := range panes {
		if p.Content == nil || p.Content.Terminal == "" {
			continue
		}
		addr := p.Content.Terminal
		a.gone[addr] = true
		if pt := a.terms[addr]; pt != nil {
			pt.closing = true
		}
		if be == nil {
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			be.Kill(ctx, addr)
		}()
	}
}

func (a *App) cmdSpaceRename(v args) (any, error) {
	s, err := a.spaceArg(v)
	if err != nil {
		return nil, err
	}
	if _, given := v["name"]; !given {
		id := s.ID
		a.askText("Rename space", s.Name, a.spaceLabel(s), func(name string) {
			a.do("space.rename", args{"id": id, "name": name})
		})
		return nil, nil
	}
	s.Name = v.str("name")
	a.changed()
	return nil, nil
}

func (a *App) cmdSpaceSelect(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if id := v.str("id"); id != "" {
		if a.doc.Space(id) == nil {
			return nil, fmt.Errorf("no space %q", id)
		}
		a.activeSpace = id
	} else if n, ok := v.int("index"); ok {
		if n < 1 || n > len(a.doc.Spaces) {
			return nil, nil
		}
		a.activeSpace = a.doc.Spaces[n-1].ID
	}
	a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	return nil, nil
}

func (a *App) cycleSpace(d int) error {
	if err := a.ready(); err != nil {
		return err
	}
	n := len(a.doc.Spaces)
	if n == 0 {
		return nil
	}
	i := 0
	for j, s := range a.doc.Spaces {
		if s.ID == a.activeSpace {
			i = j
		}
	}
	a.activeSpace = a.doc.Spaces[((i+d)%n+n)%n].ID
	a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	return nil
}

func (a *App) cmdSpaceMove(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	n, ok := v.int("index")
	if !ok || n < 1 {
		return nil, fmt.Errorf("space.move index is a position, 1 = first")
	}
	if !a.doc.MoveSpace(v.str("id"), n-1) {
		return nil, fmt.Errorf("no space %q", v.str("id"))
	}
	a.changed()
	return nil, nil
}

func (a *App) cmdTabNew(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if err := noMods(v); err != nil {
		return nil, err
	}
	s := a.space()
	if id := v.str("space"); id != "" {
		if s = a.doc.Space(id); s == nil {
			return nil, fmt.Errorf("no space %q", id)
		}
	}
	if s == nil {
		return a.cmdSpaceNew(args{"folder": v.str("folder"), "argv": v["argv"], "task": v.str("task")})
	}
	argv, err := launchArgv(v)
	if err != nil {
		return nil, err
	}
	folder := v.str("folder")
	if folder == "" {
		folder = a.focusedFolder()
	}
	spaceID, tabID, name := s.ID, layout.NewID("tb"), v.str("name")
	a.spawn(spaceID, tabID, folder, argv, func(addr string) {
		s := a.doc.Space(spaceID)
		if s == nil {
			return
		}
		if _, _, p := a.doc.PaneOfTerminal(addr); p != nil {
			return // adopted already
		}
		t, p := s.AddTab(name, addr)
		retab(t, tabID)
		a.activeSpace, a.activeTab[s.ID] = s.ID, t.ID
		a.setFocus(t, p.ID)
		a.focusReq = p.ID
	})
	return map[string]string{"tab": tabID}, nil
}

func (a *App) tabArg(v args) (*layout.Space, *layout.Tab, error) {
	if err := a.ready(); err != nil {
		return nil, nil, err
	}
	if id := v.str("id"); id != "" {
		s, t := a.doc.Tab(id)
		if t == nil {
			return nil, nil, fmt.Errorf("no tab %q", id)
		}
		return s, t, nil
	}
	if t := a.tab(); t != nil {
		return a.space(), t, nil
	}
	return nil, nil, fmt.Errorf("no tab")
}

func (a *App) cmdTabClose(v args) (any, error) {
	s, t, err := a.tabArg(v)
	if err != nil {
		return nil, err
	}
	if v.str("confirmed") != "true" && len(t.Panes()) > 1 {
		id := t.ID
		a.askConfirm(fmt.Sprintf("Close tab %q?", a.tabLabel(t)), fmt.Sprintf("Its %d terminals end.", len(t.Panes())), func() {
			a.do("tab.close", args{"id": id, "confirmed": true})
		})
		return nil, nil
	}
	a.killPanes(t.Panes())
	s.RemoveTab(t.ID)
	if len(s.Tabs) == 0 {
		a.doc.RemoveSpace(s.ID)
	}
	a.changed()
	a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	return nil, nil
}

func (a *App) cmdTabRename(v args) (any, error) {
	_, t, err := a.tabArg(v)
	if err != nil {
		return nil, err
	}
	if _, given := v["name"]; !given {
		id := t.ID
		a.askText("Rename tab", t.Name, a.tabLabel(t), func(name string) {
			a.do("tab.rename", args{"id": id, "name": name})
		})
		return nil, nil
	}
	t.Name = v.str("name")
	a.changed()
	return nil, nil
}

func (a *App) cmdTabSelect(v args) (any, error) {
	s := a.space()
	if err := a.ready(); err != nil || s == nil {
		return nil, err
	}
	if id := v.str("id"); id != "" {
		ts, t := a.doc.Tab(id)
		if t == nil {
			return nil, fmt.Errorf("no tab %q", id)
		}
		a.activeSpace, a.activeTab[ts.ID] = ts.ID, t.ID
	} else if n, ok := v.int("index"); ok {
		if n < 1 || n > len(s.Tabs) {
			return nil, nil
		}
		a.activeTab[s.ID] = s.Tabs[n-1].ID
	}
	a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	return nil, nil
}

func (a *App) cycleTab(d int) error {
	s := a.space()
	if err := a.ready(); err != nil || s == nil || len(s.Tabs) == 0 {
		return err
	}
	i := 0
	for j, t := range s.Tabs {
		if t.ID == a.activeTab[s.ID] {
			i = j
		}
	}
	n := len(s.Tabs)
	a.activeTab[s.ID] = s.Tabs[((i+d)%n+n)%n].ID
	a.focusReq = a.focus[a.activeTab[s.ID]]
	return nil
}

func (a *App) cmdTabMove(v args) (any, error) {
	s, t, err := a.tabArg(v)
	if err != nil {
		return nil, err
	}
	n, ok := v.int("index")
	if !ok || n < 1 {
		return nil, fmt.Errorf("tab.move index is a position, 1 = first")
	}
	s.MoveTab(t.ID, n-1)
	a.changed()
	return nil, nil
}

func direction(s string) (layout.Direction, bool) {
	switch s {
	case "right":
		return layout.Right, true
	case "down":
		return layout.Down, true
	case "left":
		return layout.Left, true
	case "up":
		return layout.Up, true
	}
	return 0, false
}

func (a *App) paneArg(v args) (*layout.Space, *layout.Tab, *layout.Node, error) {
	if err := a.ready(); err != nil {
		return nil, nil, nil, err
	}
	if id := v.str("pane"); id != "" {
		s, t, p := a.doc.Pane(id)
		if p == nil {
			return nil, nil, nil, fmt.Errorf("no pane %q", id)
		}
		return s, t, p, nil
	}
	if p := a.pane(); p != nil {
		return a.space(), a.tab(), p, nil
	}
	return nil, nil, nil, fmt.Errorf("no pane")
}

func (a *App) cmdPaneSplit(v args) (any, error) {
	dir, ok := direction(v.str("direction"))
	if !ok || (dir != layout.Right && dir != layout.Down) {
		return nil, fmt.Errorf("direction is right or down")
	}
	if err := noMods(v); err != nil {
		return nil, err
	}
	if err := a.ready(); err != nil {
		return nil, err
	}
	argv, err := launchArgv(v)
	if err != nil {
		return nil, err
	}
	// Without a pane to split a new tab is the next best thing; a pane that
	// was named but is not there is an error.
	if v.str("pane") == "" && a.pane() == nil {
		return a.cmdTabNew(args{"argv": v["argv"], "task": v.str("task"), "folder": v.str("folder")})
	}
	s, t, p, err := a.paneArg(v)
	if err != nil {
		return nil, err
	}
	cwd := v.str("folder")
	if cwd == "" {
		cwd = a.cwdOf(p)
	}
	if cwd == "" {
		cwd = a.focusedFolder()
	}
	paneID, tabID := p.ID, t.ID
	a.spawn(s.ID, t.ID, cwd, argv, func(addr string) {
		_, t := a.doc.Tab(tabID)
		if t == nil {
			return
		}
		if _, _, q := a.doc.PaneOfTerminal(addr); q != nil {
			return
		}
		n, err := t.Split(paneID, dir, addr)
		if err != nil {
			a.notify(err.Error())
			return
		}
		delete(a.zoomed, t.ID)
		a.setFocus(t, n.ID)
		a.focusReq = n.ID
	})
	return nil, nil
}

// dropPane removes a pane from its tab, and the tab and space when that
// leaves them empty.
func (a *App) dropPane(s *layout.Space, t *layout.Tab, paneID string) {
	t.RemovePane(paneID)
	if len(t.Panes()) == 0 {
		s.RemoveTab(t.ID)
		if len(s.Tabs) == 0 {
			a.doc.RemoveSpace(s.ID)
		}
	}
}

func (a *App) cmdPaneClose(v args) (any, error) {
	s, t, p, err := a.paneArg(v)
	if err != nil {
		return nil, err
	}
	a.killPanes([]*layout.Node{p})
	a.dropPane(s, t, p.ID)
	a.changed()
	a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	return nil, nil
}

func (a *App) cmdPaneZoom(v args) (any, error) {
	_, t, p, err := a.paneArg(v)
	if err != nil {
		return nil, err
	}
	if a.zoomed[t.ID] == p.ID {
		delete(a.zoomed, t.ID)
	} else {
		a.zoomed[t.ID] = p.ID
		a.setFocus(t, p.ID)
	}
	a.focusReq = p.ID
	return nil, nil
}

func (a *App) cmdPaneFocus(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	if addr := v.str("terminal"); addr != "" {
		_, _, p := a.doc.PaneOfTerminal(addr)
		if p == nil {
			return nil, fmt.Errorf("no pane shows %s", addr)
		}
		a.show(p.ID)
		return nil, nil
	}
	_, _, p, err := a.paneArg(v)
	if err != nil {
		return nil, err
	}
	a.show(p.ID)
	return nil, nil
}

func (a *App) cmdPaneGo(v args) (any, error) {
	dir, ok := direction(v.str("direction"))
	if !ok {
		return nil, fmt.Errorf("direction is left, right, up or down")
	}
	_, t, p, err := a.paneArg(args{})
	if err != nil {
		return nil, err
	}
	if a.zoomed[t.ID] != "" {
		return nil, nil
	}
	if next := t.Neighbor(p.ID, dir); next != "" {
		a.setFocus(t, next)
		a.focusReq = next
	}
	return nil, nil
}

func (a *App) cmdPaneSwap(v args) (any, error) {
	dir, ok := direction(v.str("direction"))
	if !ok {
		return nil, fmt.Errorf("direction is left, right, up or down")
	}
	_, t, p, err := a.paneArg(args{})
	if err != nil {
		return nil, err
	}
	if t.Swap(p.ID, dir) {
		a.focusReq = p.ID
		a.changed()
	}
	return nil, nil
}

func (a *App) cmdAgentFocus(v args) (any, error) {
	return a.cmdPaneFocus(args{"terminal": v.str("terminal")})
}

// cycleAgent goes to the next or previous agent of the agents list.
func (a *App) cycleAgent(d int) error {
	if err := a.ready(); err != nil {
		return err
	}
	list := a.agents()
	if len(list) == 0 {
		return nil
	}
	cur := -1
	if rec, ok := a.record(a.pane()); ok {
		for i, e := range list {
			if e.rec.Address == rec.Address {
				cur = i
			}
		}
	}
	n := len(list)
	next := 0
	if cur >= 0 {
		next = ((cur+d)%n + n) % n
	} else if d < 0 {
		next = n - 1
	}
	a.show(list[next].pane)
	return nil
}

// cmdTerminalOpen starts a terminal as a new tab, or beside the focused pane.
func (a *App) cmdTerminalOpen(v args) (any, error) {
	switch place := v.str("place"); place {
	case "right", "down":
		return a.cmdPaneSplit(args{"direction": place, "argv": v["argv"], "folder": v.str("folder")})
	case "", "tab":
		name := v.str("name")
		if argv, err := argvOf(v); err != nil {
			return nil, err
		} else if name == "" && len(argv) > 0 {
			name = filepath.Base(argv[0])
		} else if name == "" {
			name = "shell"
		}
		return a.cmdTabNew(args{"name": name, "argv": v["argv"], "folder": v.str("folder")})
	default:
		return nil, fmt.Errorf("place is tab, right or down")
	}
}

// localTerminal checks that addr names a terminal of this machine.
func localTerminal(addr string) error {
	if strings.Contains(addr, "/") {
		return fmt.Errorf("terminal %q is on another machine: only this machine is supported by this app", addr)
	}
	return nil
}

// cmdTerminalKeep gives a running terminal that has no pane one: a new tab
// of the shown space, or right/down of the focused pane.
func (a *App) cmdTerminalKeep(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	addr := v.str("terminal")
	if err := localTerminal(addr); err != nil {
		return nil, err
	}
	if _, _, p := a.doc.PaneOfTerminal(addr); p != nil {
		a.show(p.ID)
		return map[string]string{"pane": p.ID}, nil
	}
	if rec, ok := a.terminals[addr]; !ok || rec.State != "live" {
		return nil, fmt.Errorf("no running terminal %q", addr)
	}
	place := v.str("place")
	if place == "" {
		place = "tab"
	}
	dir, split := direction(place)
	if place != "tab" && (!split || (dir != layout.Right && dir != layout.Down)) {
		return nil, fmt.Errorf("place is tab, right or down")
	}
	s := a.space()
	if s == nil {
		return nil, fmt.Errorf("no space to keep it in")
	}
	var targetID string
	tabID := layout.NewID("tb")
	if split {
		if p := a.pane(); p != nil {
			targetID, tabID = p.ID, a.tab().ID
		}
	}
	spaceID, be := s.ID, a.core
	labels := map[string]string{"herdr.space": spaceID, "herdr.tab": tabID}
	a.obtain(tabID, func(ctx context.Context) (string, error) {
		if err := be.Label(ctx, addr, labels); err != nil {
			return "", err
		}
		return addr, be.Keep(ctx, addr, true)
	}, func(addr string) {
		if targetID != "" {
			if _, t, p := a.doc.Pane(targetID); p != nil {
				if n, err := t.Split(targetID, dir, addr); err == nil {
					delete(a.zoomed, t.ID)
					a.setFocus(t, n.ID)
					a.focusReq = n.ID
					return
				}
			}
		}
		s := a.doc.Space(spaceID)
		if s == nil {
			return
		}
		t, p := s.AddTab("", addr)
		if _, other := a.doc.Tab(tabID); other == nil {
			retab(t, tabID)
		}
		a.activeSpace, a.activeTab[s.ID] = s.ID, t.ID
		a.setFocus(t, p.ID)
		a.focusReq = p.ID
	})
	return map[string]any{"pending": true, "terminal": addr, "tab": tabID}, nil
}

// cmdTerminalPark takes a terminal's pane away and stops the core keeping
// it; it keeps running.
func (a *App) cmdTerminalPark(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	addr := v.str("terminal")
	if err := localTerminal(addr); err != nil {
		return nil, err
	}
	if _, ok := a.terminals[addr]; !ok {
		return nil, fmt.Errorf("no terminal %q", addr)
	}
	be := a.core
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := be.Label(ctx, addr, map[string]string{"herdr.space": "", "herdr.tab": ""})
		if err == nil {
			err = be.Keep(ctx, addr, false)
		}
		a.post(func() {
			if err != nil {
				a.notify("cannot park " + addr + ": " + err.Error())
				return
			}
			// The core's record may still carry the old labels for a moment.
			a.gone[addr] = true
			if s, t, p := a.doc.PaneOfTerminal(addr); p != nil {
				a.dropPane(s, t, p.ID)
			}
			a.changed()
			a.focusReq = a.focus[a.activeTab[a.activeSpace]]
		})
	}()
	return map[string]any{"pending": true, "terminal": addr}, nil
}

func (a *App) cmdTerminalKill(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	be, addr := a.core, v.str("terminal")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := be.Kill(ctx, addr); err != nil {
			a.post(func() { a.notify(err.Error()) })
		}
	}()
	return nil, nil
}

func (a *App) cmdTerminalPin(v args) (any, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	value := ""
	if p := v.str("pinned"); p == "true" || p == "1" {
		value = "1"
	}
	be, addr := a.core, v.str("terminal")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := be.Label(ctx, addr, map[string]string{"herdr.pinned": value}); err != nil {
			a.post(func() { a.notify(err.Error()) })
		}
	}()
	return nil, nil
}

func (a *App) cmdLayoutToggle(v args) (any, error) {
	if id := v.str("id"); id != "" && id != "sidebar" {
		return nil, fmt.Errorf("only the sidebar toggles in this layout")
	}
	shown := !a.cfg.sidebarShown()
	a.cfg.Sidebar = &shown
	a.saveSettings()
	return nil, nil
}

func (a *App) cmdModOpen(v args) (any, error) {
	switch v.str("name") {
	case "picker":
		a.openPalette("")
		a.palette.switcher = true
	case "settings":
		a.settings = true
	default:
		return nil, fmt.Errorf("no mod %q in this app", v.str("name"))
	}
	return nil, nil
}

func (a *App) cmdAppTheme(v args) (any, error) {
	name := v.str("name")
	if !slices.Contains(theme.PresetNames, name) {
		return nil, fmt.Errorf("no theme %q; themes: %s", name, strings.Join(theme.PresetNames, ", "))
	}
	a.cfg.Theme = name
	a.applyTheme()
	a.saveSettings()
	return nil, nil
}

// zoomBy changes the text size of terminals a step, or back to 13.
func (a *App) zoomBy(steps int) {
	if steps == 0 {
		a.cfg.FontSize = 0
	} else {
		a.cfg.FontSize = a.cfg.fontSize() + float32(steps)
	}
	for _, pt := range a.terms {
		pt.term.SetFont(a.termFont())
	}
	a.saveSettingsSoon()
}

// statusWord names an agent status for people.
func statusWord(w agent.Word) string {
	switch w {
	case agent.Blocked:
		return "Needs you"
	case agent.Done:
		return "Done"
	case agent.Working:
		return "Working"
	case agent.Idle:
		return "Idle"
	}
	return ""
}
