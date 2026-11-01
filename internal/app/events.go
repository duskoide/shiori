package app

import (
	"encoding/json"
	"maps"
	"slices"
)

// Events tell sessions what changed in the window. Each frame, while
// anyone listens, the window takes a small summary of its state and
// compares it with the last one: that catches every change, those that
// come from the core (another window editing the layout) included,
// without each place that changes something having to say so.

// eventNames are the events, in the order a frame sends them.
var eventNames = []string{
	"space.created", "tab.created", "pane.created",
	"pane.closed", "tab.closed", "space.closed",
	"space.renamed", "tab.renamed",
	"focus.changed", "window.focus", "agent.status",
	"settings.changed", "keys.changed", "connection", "machine.changed",
}

func knownEvent(name string) bool { return slices.Contains(eventNames, name) }

type snapSpace struct{ name, label, folder, machine string }
type snapTab struct{ space, name, label string }
type snapPane struct{ space, tab, terminal string }
type snapAgent struct{ terminal, app, status string }
type snapFocus struct{ space, tab, pane, terminal, cwd string }
type snapMachine struct{ state, why string }

// stateSnap is what events are worked out from.
type stateSnap struct {
	spaces     map[string]snapSpace
	spaceOrder []string
	tabs       map[string]snapTab
	tabOrder   []string
	panes      map[string]snapPane
	paneOrder  []string
	agents     map[string]snapAgent // by pane
	focus      snapFocus
	focused    bool
	settings   map[string]any
	keys       string
	connected  bool
	connErr    string
	machines   map[string]snapMachine
}

func (a *App) snapshot() *stateSnap {
	s := &stateSnap{spaces: map[string]snapSpace{}, tabs: map[string]snapTab{}, panes: map[string]snapPane{}, agents: map[string]snapAgent{},
		focused: a.focused, keys: string(a.keysRaw), connected: a.connected, connErr: a.connErr, machines: map[string]snapMachine{}}
	for _, m := range a.machines {
		s.machines[m.Name] = snapMachine{state: m.State, why: m.Why}
	}
	if a.doc != nil {
		for _, sp := range a.doc.Spaces {
			s.spaces[sp.ID] = snapSpace{name: sp.Name, label: a.spaceLabel(sp), folder: sp.Folder, machine: sp.Machine}
			s.spaceOrder = append(s.spaceOrder, sp.ID)
			for _, t := range sp.Tabs {
				s.tabs[t.ID] = snapTab{space: sp.ID, name: t.Name, label: a.tabLabel(t)}
				s.tabOrder = append(s.tabOrder, t.ID)
				for _, p := range t.Panes() {
					term := ""
					if p.Content != nil {
						term = p.Content.Terminal
					}
					s.panes[p.ID] = snapPane{space: sp.ID, tab: t.ID, terminal: term}
					s.paneOrder = append(s.paneOrder, p.ID)
					if rec, ok := a.record(p); ok && rec.Status != nil {
						app := rec.Status.App
						if app == "" {
							app = programOf(rec).Name
						}
						s.agents[p.ID] = snapAgent{terminal: rec.Address, app: app, status: a.word(rec).String()}
					}
				}
			}
		}
	}
	s.focus.space = a.activeSpace
	if t := a.tab(); t != nil {
		s.focus.tab = t.ID
		if p := a.pane(); p != nil {
			s.focus.pane = p.ID
			if p.Content != nil {
				s.focus.terminal = p.Content.Terminal
			}
			s.focus.cwd = a.cwdOf(p)
		}
	}
	themeName := a.cfg.Theme
	if themeName == "" {
		themeName = "graphite"
	}
	s.settings = map[string]any{"theme": themeName, "font": a.cfg.Font, "font_size": a.cfg.fontSize(), "sidebar": a.cfg.sidebarShown(), "changes": a.cfg.changesShown()}
	return s
}

type event struct {
	name string
	data any
}

// diffEvents lists what changed from old to now.
func diffEvents(old, now *stateSnap) []event {
	var out []event
	add := func(name string, data any) { out = append(out, event{name, data}) }
	for _, id := range now.spaceOrder {
		if _, ok := old.spaces[id]; !ok {
			sp := now.spaces[id]
			add("space.created", map[string]string{"space": id, "name": sp.name, "label": sp.label, "folder": sp.folder, "machine": sp.machine})
		}
	}
	for _, id := range now.tabOrder {
		if _, ok := old.tabs[id]; !ok {
			t := now.tabs[id]
			add("tab.created", map[string]string{"space": t.space, "tab": id, "name": t.name, "label": t.label})
		}
	}
	for _, id := range now.paneOrder {
		if _, ok := old.panes[id]; !ok {
			p := now.panes[id]
			add("pane.created", map[string]string{"space": p.space, "tab": p.tab, "pane": id, "terminal": p.terminal})
		}
	}
	for _, id := range old.paneOrder {
		if _, ok := now.panes[id]; !ok {
			p := old.panes[id]
			add("pane.closed", map[string]string{"space": p.space, "tab": p.tab, "pane": id, "terminal": p.terminal})
		}
	}
	for _, id := range old.tabOrder {
		if _, ok := now.tabs[id]; !ok {
			t := old.tabs[id]
			add("tab.closed", map[string]string{"space": t.space, "tab": id, "name": t.name, "label": t.label})
		}
	}
	for _, id := range old.spaceOrder {
		if _, ok := now.spaces[id]; !ok {
			sp := old.spaces[id]
			add("space.closed", map[string]string{"space": id, "name": sp.name, "label": sp.label, "folder": sp.folder})
		}
	}
	for _, id := range now.spaceOrder {
		if was, ok := old.spaces[id]; ok && was.name != now.spaces[id].name {
			add("space.renamed", map[string]string{"space": id, "name": now.spaces[id].name, "label": now.spaces[id].label})
		}
	}
	for _, id := range now.tabOrder {
		if was, ok := old.tabs[id]; ok && was.name != now.tabs[id].name {
			t := now.tabs[id]
			add("tab.renamed", map[string]string{"space": t.space, "tab": id, "name": t.name, "label": t.label})
		}
	}
	if old.focus != now.focus {
		f := now.focus
		add("focus.changed", map[string]string{"space": f.space, "tab": f.tab, "pane": f.pane, "terminal": f.terminal, "cwd": f.cwd})
	}
	if old.focused != now.focused {
		add("window.focus", map[string]bool{"focused": now.focused})
	}
	for _, id := range now.paneOrder {
		ag, ok := now.agents[id]
		if !ok || ag.status == "none" {
			continue
		}
		if was := old.agents[id]; was.status != ag.status {
			add("agent.status", map[string]string{"pane": id, "terminal": ag.terminal, "app": ag.app, "status": ag.status, "previous": was.status})
		}
	}
	for k, v := range now.settings {
		if old.settings[k] != v {
			add("settings.changed", now.settings)
			break
		}
	}
	if old.keys != now.keys {
		add("keys.changed", map[string]any{})
	}
	if old.connected != now.connected || old.connErr != now.connErr {
		add("connection", map[string]any{"connected": now.connected, "error": now.connErr})
	}
	// A machine added, changing state, or gone ("removed").
	names := slices.Sorted(maps.Keys(now.machines))
	for name := range old.machines {
		if _, ok := now.machines[name]; !ok {
			names = append(names, name)
		}
	}
	for _, name := range names {
		was, had := old.machines[name]
		is, has := now.machines[name]
		switch {
		case !has:
			add("machine.changed", map[string]any{"name": name, "state": "removed", "previous": was.state})
		case !had || was != is:
			add("machine.changed", map[string]any{"name": name, "state": is.state, "why": is.why, "previous": was.state, "connected": is.state == "connected"})
		}
	}
	return out
}

// listening reports whether any session wants an event.
func (a *App) listening() bool {
	for s := range a.sessions {
		if len(s.subs) > 0 {
			return true
		}
	}
	return len(a.wantEvents()) > 0
}

// emitEvents sends what changed since the last frame to those who listen
// (main thread, once a frame).
func (a *App) emitEvents() {
	if !a.listening() {
		a.snap = nil // compared afresh once someone listens
		return
	}
	now := a.snapshot()
	old := a.snap
	a.snap = now
	if old == nil {
		return
	}
	for _, ev := range diffEvents(old, now) {
		a.eventSeq++
		b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "event",
			"params": map[string]any{"name": ev.name, "seq": a.eventSeq, "data": ev.data}})
		if err != nil {
			continue
		}
		b = append(b, '\n')
		for s := range a.sessions {
			if s.subs[ev.name] {
				s.push(b)
			}
		}
		a.eventForServices(ev.name, b)
	}
}
