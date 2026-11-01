package app

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/duskoide/shiori/internal/agent"
	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
	"github.com/duskoide/shiori/internal/programs"
)

// fixView keeps what the window looks at pointing at things that exist.
func (a *App) fixView() {
	if a.doc == nil {
		return
	}
	if a.doc.Space(a.activeSpace) == nil {
		a.activeSpace = ""
		if len(a.doc.Spaces) > 0 {
			a.activeSpace = a.doc.Spaces[0].ID
		}
	}
	for _, s := range a.doc.Spaces {
		if _, t := a.doc.Tab(a.activeTab[s.ID]); t == nil || !slices.Contains(s.Tabs, t) {
			if len(s.Tabs) > 0 {
				a.activeTab[s.ID] = s.Tabs[0].ID
			} else {
				delete(a.activeTab, s.ID)
			}
		}
		for _, t := range s.Tabs {
			panes := t.Panes()
			if !hasPane(panes, a.focus[t.ID]) {
				prev := a.history[t.ID]
				a.focus[t.ID] = ""
				for i := len(prev) - 1; i >= 0; i-- {
					if hasPane(panes, prev[i]) {
						a.focus[t.ID] = prev[i]
						break
					}
				}
				if a.focus[t.ID] == "" && len(panes) > 0 {
					a.focus[t.ID] = panes[0].ID
				}
			}
			if z := a.zoomed[t.ID]; z != "" && !hasPane(panes, z) {
				delete(a.zoomed, t.ID)
			}
		}
	}
}

func hasPane(panes []*layout.Node, id string) bool {
	if id == "" {
		return false
	}
	for _, p := range panes {
		if p.ID == id {
			return true
		}
	}
	return false
}

// space is the active space, or nil.
func (a *App) space() *layout.Space {
	if a.doc == nil {
		return nil
	}
	return a.doc.Space(a.activeSpace)
}

// tab is the active space's active tab, or nil.
func (a *App) tab() *layout.Tab {
	s := a.space()
	if s == nil {
		return nil
	}
	for _, t := range s.Tabs {
		if t.ID == a.activeTab[s.ID] {
			return t
		}
	}
	return nil
}

// pane is the active tab's focused pane, or nil.
func (a *App) pane() *layout.Node {
	t := a.tab()
	if t == nil {
		return nil
	}
	for _, p := range t.Panes() {
		if p.ID == a.focus[t.ID] {
			return p
		}
	}
	return nil
}

// setFocus focuses a pane of a tab, remembering the one before.
func (a *App) setFocus(t *layout.Tab, paneID string) {
	if old := a.focus[t.ID]; old != "" && old != paneID {
		h := slices.DeleteFunc(a.history[t.ID], func(id string) bool { return id == old || id == paneID })
		h = append(h, old)
		if len(h) > 32 {
			h = h[len(h)-32:]
		}
		a.history[t.ID] = h
	}
	a.focus[t.ID] = paneID
}

// show makes a pane the one the window looks at.
func (a *App) show(paneID string) bool {
	s, t, p := a.doc.Pane(paneID)
	if p == nil {
		return false
	}
	a.activeSpace = s.ID
	a.activeTab[s.ID] = t.ID
	if z := a.zoomed[t.ID]; z != "" && z != paneID {
		delete(a.zoomed, t.ID)
	}
	a.setFocus(t, paneID)
	a.focusReq = paneID
	return true
}

// record is the core's record of a pane's terminal.
func (a *App) record(p *layout.Node) (core.Terminal, bool) {
	if p == nil || p.Content == nil || p.Content.Terminal == "" {
		return core.Terminal{}, false
	}
	t, ok := a.terminals[p.Content.Terminal]
	return t, ok
}

// cwdOf is the folder a pane's program works in.
func (a *App) cwdOf(p *layout.Node) string {
	rec, ok := a.record(p)
	if !ok {
		return ""
	}
	if rec.Foreground != nil && rec.Foreground.Cwd != "" {
		return rec.Foreground.Cwd
	}
	return rec.Cwd
}

// programOf is what runs in a terminal, for its icon and name.
func programOf(rec core.Terminal) programs.Program {
	app := ""
	if rec.Status != nil {
		app = rec.Status.App
	}
	argv := rec.Argv
	if rec.Foreground != nil && len(rec.Foreground.Argv) > 0 {
		argv = rec.Foreground.Argv
	}
	return programs.Resolve(app, argv)
}

// paneName is the name a pane's header shows: an agent's or shell's
// name, else the terminal's title, else its program.
func (a *App) paneName(p *layout.Node) string {
	rec, ok := a.record(p)
	if !ok {
		if p != nil && p.Content != nil && p.Content.Mod != "" {
			return p.Content.Mod
		}
		return "Terminal"
	}
	prog := programOf(rec)
	if prog.Agent || prog.Shell {
		return prog.Name
	}
	if rec.Title != "" {
		return rec.Title
	}
	if prog.Name != "" {
		return prog.Name
	}
	return "shell"
}

// word is an agent's status as this window sees it.
func (a *App) word(rec core.Terminal) agent.Word {
	if rec.Status == nil {
		return agent.None
	}
	return agent.Derive(agent.Input{State: rec.Status.State, Turn: rec.Turn, Labels: rec.Labels, LocalSeen: a.localSeen[rec.Address]})
}

// tabWord is the most urgent status of a tab's agents.
func (a *App) tabWord(t *layout.Tab) agent.Word {
	w := agent.None
	for _, p := range t.Panes() {
		if rec, ok := a.record(p); ok {
			w = agent.Rollup(w, a.word(rec))
		}
	}
	return w
}

func (a *App) spaceWord(s *layout.Space) agent.Word {
	w := agent.None
	for _, t := range s.Tabs {
		w = agent.Rollup(w, a.tabWord(t))
	}
	return w
}

// spaceLabel names a space: its name, else the folder of its first
// terminal, as an unnamed space follows it.
func (a *App) spaceLabel(s *layout.Space) string {
	if s.Name != "" {
		return s.Name
	}
	for _, t := range s.Tabs {
		for _, p := range t.Panes() {
			if cwd := a.cwdOf(p); cwd != "" {
				return folderName(cwd)
			}
		}
	}
	return s.Label(homeDir())
}

func folderName(dir string) string {
	if dir == homeDir() {
		return "~"
	}
	return filepath.Base(dir)
}

// tabLabel names a tab: its name, else its focused program.
func (a *App) tabLabel(t *layout.Tab) string {
	if t.Name != "" {
		return t.Name
	}
	panes := t.Panes()
	for _, p := range panes {
		if p.ID == a.focus[t.ID] {
			return a.paneName(p)
		}
	}
	if len(panes) > 0 {
		return a.paneName(panes[0])
	}
	return "Terminal"
}

// tildePath shortens a folder under home.
func tildePath(dir string) string {
	h := homeDir()
	if h != "" && (dir == h || strings.HasPrefix(dir, h+"/")) {
		return "~" + dir[len(h):]
	}
	return dir
}

// agentEntry is a row of the agents list.
type agentEntry struct {
	rec   core.Terminal
	pane  string
	space *layout.Space
	tab   *layout.Tab
	word  agent.Word
	prog  programs.Program
}

// agents lists the terminals in the window's panes that report a status,
// the most urgent first; pinned ones lead.
func (a *App) agents() []agentEntry {
	if a.doc == nil {
		return nil
	}
	var out []agentEntry
	for _, s := range a.doc.Spaces {
		for _, t := range s.Tabs {
			for _, p := range t.Panes() {
				rec, ok := a.record(p)
				if !ok || rec.Status == nil {
					continue
				}
				out = append(out, agentEntry{rec: rec, pane: p.ID, space: s, tab: t, word: a.word(rec), prog: programOf(rec)})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := agent.Pinned(out[i].rec.Labels), agent.Pinned(out[j].rec.Labels)
		if pi != pj {
			return pi
		}
		if out[i].word != out[j].word {
			return out[i].word > out[j].word
		}
		return out[i].rec.StatusMs > out[j].rec.StatusMs
	})
	return out
}

// lookedAt notes what the person looks at: the agents of the panes drawn
// on screen (every visible pane of the active tab, while the window has the
// focus) are seen, here and, once per turn, for every window (herdr.seen).
func (a *App) lookedAt() {
	t := a.tab()
	if t == nil || !a.focused {
		return
	}
	panes := t.Panes()
	if z := a.zoomed[t.ID]; z != "" {
		panes = slices.DeleteFunc(panes, func(p *layout.Node) bool { return p.ID != z })
	}
	for _, p := range panes {
		if rec, ok := a.record(p); ok {
			a.sawAgent(rec)
		}
	}
}

// sawAgent notes that the person looks at a terminal, and writes
// herdr.seen when it is behind: when the label is missing, even at turn 0,
// or the turn is past it. A write that fails is tried again.
func (a *App) sawAgent(rec core.Terminal) {
	a.localSeen[rec.Address] = true
	if rec.Status == nil || a.core == nil {
		return
	}
	switch rec.Status.State {
	case "working", "blocked", "error":
		return
	}
	addr := rec.Address
	seen, written := a.seenTurns[addr]
	label, labeled := rec.Labels["herdr.seen"]
	if labeled {
		var n uint64
		for _, r := range label {
			if r < '0' || r > '9' {
				n = 0
				break
			}
			n = n*10 + uint64(r-'0')
		}
		seen, written = max(seen, n), true
	}
	if written && rec.Turn <= seen {
		return
	}
	if until, ok := a.seenBusy[addr]; ok && time.Now().Before(until) {
		return
	}
	a.seenBusy[addr] = time.Now().Add(10 * time.Second)
	be, turn := a.core, rec.Turn
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := be.Label(ctx, addr, agent.SeenLabel(turn))
		a.post(func() {
			if err != nil {
				// Tried again in a few seconds.
				a.seenBusy[addr] = time.Now().Add(3 * time.Second)
				time.AfterFunc(3*time.Second, a.invalidate)
				return
			}
			delete(a.seenBusy, addr)
			a.seenTurns[addr] = max(a.seenTurns[addr], turn)
		})
	}()
}
