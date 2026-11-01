package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/duskoide/herdr-mygo/internal/core"
	"github.com/duskoide/herdr-mygo/internal/layout"
)

// start connects to the core in the background, and again whenever the
// connection drops.
func (a *App) start() {
	ctx, cancel := context.WithCancel(context.Background())
	a.stop = cancel
	go a.connectLoop(ctx)
}

// shutdown detaches every terminal and closes the connection; the
// terminals keep running in the core.
func (a *App) shutdown() {
	if a.stop != nil {
		a.stop()
	}
	for addr, pt := range a.terms {
		pt.term.Close()
		delete(a.terms, addr)
	}
	if a.client != nil {
		a.client.Close()
	}
}

func (a *App) connectLoop(ctx context.Context) {
	for ctx.Err() == nil {
		err := a.session1(ctx)
		if ctx.Err() != nil {
			return
		}
		msg := "lost the core"
		if err != nil {
			msg = err.Error()
		}
		a.post(func() { a.disconnected(msg) })
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// session1 runs one connection to the core until it drops.
func (a *App) session1(ctx context.Context) error {
	cl, err := core.Connect(ctx, core.Options{Session: a.session, AutoStart: true})
	if err != nil {
		return err
	}
	defer cl.Close()
	// Subscribe before reading the document, so no change falls between.
	ev, err := cl.Subscribe(ctx)
	if err != nil {
		return err
	}
	value, version, err := cl.DocGet(ctx, docName, "")
	if err != nil {
		return err
	}
	terms := ev.Terminals()
	a.post(func() {
		a.connectedTo(cl, terms)
		a.remoteDoc(value, version)
	})
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-cl.Done():
			return errors.New("lost the core")
		case ch, ok := <-ev.Changes():
			if !ok {
				return errors.New("the core's event stream ended")
			}
			if ch.Terminals {
				terms := ev.Terminals()
				a.post(func() { a.setTerminals(terms) })
			}
			if ch.Docs {
				value, version, err := cl.DocGet(ctx, docName, "")
				if err != nil {
					return err
				}
				a.post(func() { a.remoteDoc(value, version) })
			}
		}
	}
}

func (a *App) connectedTo(cl *core.Client, terms []core.Terminal) {
	a.client, a.core = cl, cl
	a.connected, a.connErr = true, ""
	a.setTerminals(terms)
}

func (a *App) disconnected(why string) {
	a.connected, a.connErr = false, why
	a.client, a.core = nil, nil
	a.saving = false
	// The streams end with the connection; terminals attach again once
	// it is back, each to a fresh screen.
	for addr, pt := range a.terms {
		pt.term.Close()
		delete(a.terms, addr)
	}
}

// setTerminals takes the core's terminal table.
func (a *App) setTerminals(list []core.Terminal) {
	a.terminals = make(map[string]core.Terminal, len(list))
	for _, t := range list {
		a.terminals[t.Address] = t
	}
	a.reconcile()
}

// remoteDoc takes a version of the document the core has.
func (a *App) remoteDoc(value json.RawMessage, version uint64) {
	if a.loaded && version <= a.version {
		return
	}
	if a.saving {
		// The reply to our save tells which version is ours: look again
		// after it.
		a.refetch = true
		return
	}
	d, err := layout.Decode(value)
	if err != nil {
		a.connErr = "layout unavailable: " + err.Error()
		return
	}
	a.doc, a.base, a.version, a.loaded = d, d.Clone(), version, true
	a.fixView()
	a.reconcile()
}

// changed records an edit of the document, saved once no divider is
// being dragged.
func (a *App) changed() {
	a.dirty = true
	a.fixView()
}

// flush saves the document when it differs from the core's: one save in
// flight at a time, compare-and-set on the version.
func (a *App) flush() {
	if !a.dirty || a.saving || a.dragging || a.core == nil || !a.loaded {
		return
	}
	a.dirty = false
	if layout.Equal(a.doc, a.base) {
		return
	}
	snapshot := a.doc.Clone()
	value, err := snapshot.Encode()
	if err != nil {
		a.notify("cannot save the layout: " + err.Error())
		return
	}
	a.saving = true
	be, version := a.core, a.version
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		next, err := be.DocSet(ctx, docName, "", version, value)
		var latest json.RawMessage
		var latestVersion uint64
		var getErr error
		if err != nil {
			latest, latestVersion, getErr = be.DocGet(ctx, docName, "")
		}
		a.post(func() {
			a.saving = false
			if err == nil {
				a.base, a.version = snapshot, next
				if !layout.Equal(a.doc, a.base) {
					a.dirty = true
				}
				if a.refetch {
					a.refetch = false
					a.refetchDoc()
				}
				return
			}
			var rpc *core.RPCError
			if errors.As(err, &rpc) && rpc.Kind == "conflict" {
				a.notify("Layout changed elsewhere")
			} else {
				a.notify("cannot save the layout: " + err.Error())
			}
			a.refetch = false
			if getErr == nil {
				a.loaded = false
				a.remoteDoc(latest, latestVersion)
			}
		})
	}()
}

func (a *App) refetchDoc() {
	be := a.core
	if be == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		value, version, err := be.DocGet(ctx, docName, "")
		if err != nil {
			return
		}
		a.post(func() { a.remoteDoc(value, version) })
	}()
}

// reconcile matches panes with terminals: a pane whose terminal finished
// closes, and a live terminal labelled with one of our spaces that no
// pane shows gets one (herdr-app's adoption, docs/labels.md).
func (a *App) reconcile() {
	if !a.loaded || !a.connected {
		return
	}
	now := time.Now()
	for addr, at := range a.pending {
		if _, ok := a.terminals[addr]; ok || now.Sub(at) > 10*time.Second {
			if _, _, p := a.doc.PaneOfTerminal(addr); p != nil || now.Sub(at) > 10*time.Second {
				delete(a.pending, addr)
			}
		}
	}
	edited := false
	for _, s := range a.doc.Spaces {
		for _, t := range s.Tabs {
			for _, p := range t.Panes() {
				if p.Content == nil || p.Content.Terminal == "" {
					continue
				}
				addr := p.Content.Terminal
				if rec, ok := a.terminals[addr]; ok && rec.State == "finished" {
					t.RemovePane(p.ID)
					edited = true
				}
			}
		}
	}
	for _, s := range append([]*layout.Space(nil), a.doc.Spaces...) {
		for _, t := range append([]*layout.Tab(nil), s.Tabs...) {
			if len(t.Panes()) == 0 {
				s.RemoveTab(t.ID)
				edited = true
			}
		}
		if len(s.Tabs) == 0 {
			a.doc.RemoveSpace(s.ID)
			edited = true
		}
	}
	for _, rec := range a.terminals {
		if rec.State != "live" || a.gone[rec.Address] {
			continue
		}
		if _, waiting := a.pending[rec.Address]; waiting {
			continue
		}
		spaceID := rec.Labels["herdr.space"]
		if spaceID == "" || a.spawning[rec.Labels["herdr.tab"]] > 0 {
			continue
		}
		if _, _, p := a.doc.PaneOfTerminal(rec.Address); p != nil {
			continue
		}
		a.adopt(rec)
		edited = true
	}
	if edited {
		a.changed()
	}
}

// adopt gives a labelled terminal a pane in its space and tab.
func (a *App) adopt(rec core.Terminal) {
	spaceID, tabID := rec.Labels["herdr.space"], rec.Labels["herdr.tab"]
	s := a.doc.Space(spaceID)
	if s == nil {
		s = a.doc.AddSpace("", rec.Cwd)
		s.ID = spaceID
	}
	if tabID != "" {
		if _, t := a.doc.Tab(tabID); t != nil {
			panes := t.Panes()
			if len(panes) > 0 {
				t.Split(panes[len(panes)-1].ID, layout.Right, rec.Address)
				return
			}
		}
	}
	t, _ := s.AddTab("", rec.Address)
	if tabID != "" && a.doc.Space(spaceID) != nil {
		if _, other := a.doc.Tab(tabID); other == nil {
			t.ID = tabID
			if t.Root != nil {
				t.Root.ID = tabID
			}
		}
	}
}
