package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
)

// start connects to the core in the background, and again whenever the
// connection drops.
func (a *App) start() {
	ctx, cancel := context.WithCancel(context.Background())
	a.stop = cancel
	go a.connectLoop(ctx)
	go a.watchConfig(ctx)
}

// shutdown detaches every terminal and closes the connection; the
// terminals keep running in the core.
func (a *App) shutdown() {
	a.cancelWaiting()
	a.flushSettings()
	a.drain()
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

// drain writes the last edits of the layout before the window goes, as
// herdr-app does: it waits for the save in flight, then saves what is left.
func (a *App) drain() {
	if done := a.sy.saveDone; done != nil {
		select {
		case <-done:
		case <-time.After(drainTimeout):
		}
	}
	// The save's reply is among the posted work.
	a.runPosted()
	if a.core == nil || !a.loaded || a.saving || layout.Equal(a.doc, a.base) {
		return
	}
	value, err := a.doc.Encode()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if next, err := a.core.DocSet(ctx, docName, "", a.version, value); err == nil {
		a.base, a.version, a.dirty = a.doc.Clone(), next, false
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
	value, version, err := docGet(ctx, cl)
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
				value, version, err := docGet(ctx, cl)
				if err != nil {
					return err
				}
				a.post(func() { a.remoteDoc(value, version) })
			}
		}
	}
}

// docGet reads the layout document, giving up on a core that hangs.
func docGet(ctx context.Context, be backend) (json.RawMessage, uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return be.DocGet(ctx, docName, "")
}

func (a *App) connectedTo(cl *core.Client, terms []core.Terminal) {
	a.sy.gen++
	a.client, a.core = cl, cl
	a.connected, a.connErr = true, ""
	// Edits a lost connection kept from the core go out again.
	if a.loaded && !layout.Equal(a.doc, a.base) {
		a.dirty = true
	}
	a.sy.saveAfter = time.Time{}
	a.setTerminals(terms)
}

func (a *App) disconnected(why string) {
	a.sy.gen++
	a.connected, a.connErr = false, why
	a.client, a.core = nil, nil
	a.saving = false
	clear(a.sy.checking)
	clear(a.sy.adopting)
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
	if a.saving || a.dragging {
		// The reply to our save tells which version is ours, and a drag
		// is not cut short: look again after either.
		a.refetch = true
		return
	}
	d, err := layout.Decode(value)
	if err != nil {
		a.connErr = "layout unavailable: " + err.Error()
		return
	}
	if a.loaded && !layout.Equal(a.doc, a.base) {
		// Someone else saved first: their copy wins, as in herdr-app.
		a.notify("Layout changed elsewhere")
	}
	a.doc, a.base, a.version, a.loaded = d, d.Clone(), version, true
	a.dirty = false
	a.fixView()
	a.dropUnshown()
	a.reconcile()
}

// dropUnshown detaches the terminals no pane shows any more; they keep
// running in the core.
func (a *App) dropUnshown() {
	for addr, pt := range a.terms {
		if _, _, p := a.doc.PaneOfTerminal(addr); p == nil {
			pt.closing = true
			pt.term.Close()
			delete(a.terms, addr)
		}
	}
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
	if a.saving || a.dragging || a.core == nil || !a.loaded {
		return
	}
	if a.refetch {
		a.refetch = false
		a.refetchDoc()
	}
	if !a.dirty || time.Now().Before(a.sy.saveAfter) {
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
	be, version, gen := a.core, a.version, a.sy.gen
	done := make(chan struct{})
	a.sy.saveDone = done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		next, err := be.DocSet(ctx, docName, "", version, value)
		conflict := core.IsKind(err, "conflict")
		var latest json.RawMessage
		var latestVersion uint64
		var getErr error
		if conflict {
			latest, latestVersion, getErr = be.DocGet(ctx, docName, "")
		}
		a.post(func() {
			if gen != a.sy.gen {
				// A reply from a connection since lost: connectedTo
				// decides what to save on the new one.
				return
			}
			a.saving = false
			switch {
			case err == nil:
				a.base, a.version = snapshot, next
				a.sy.saveFails = 0
				if !layout.Equal(a.doc, a.base) {
					a.dirty = true
				}
			case conflict:
				a.notify("Layout changed elsewhere")
				a.refetch = false
				if getErr == nil {
					// Take the other copy; this edit is lost, as in
					// herdr-app.
					a.base = a.doc.Clone()
					a.remoteDoc(latest, latestVersion)
				} else {
					a.refetch = true
				}
			default:
				// The edit stays and goes out again shortly.
				if a.sy.saveFails == 0 {
					a.notify("cannot save the layout: " + err.Error())
				}
				a.sy.saveFails++
				a.dirty = true
				wait := saveRetry * time.Duration(min(a.sy.saveFails, 5))
				a.sy.saveAfter = time.Now().Add(wait)
				a.later(wait, func() {})
			}
		})
		close(done)
	}()
}

// later runs fn on the main thread after d.
func (a *App) later(d time.Duration, fn func()) {
	time.AfterFunc(d, func() { a.post(fn) })
}

func (a *App) refetchDoc() {
	be, gen := a.core, a.sy.gen
	if be == nil {
		return
	}
	go func() {
		value, version, err := docGet(context.Background(), be)
		if err != nil {
			return
		}
		a.post(func() {
			if gen == a.sy.gen {
				a.remoteDoc(value, version)
			}
		})
	}()
}

// reconcile matches panes with terminals: a pane whose terminal finished
// or is gone closes, and a live terminal labelled with one of our spaces
// that no pane shows gets one (herdr-app's adoption, docs/labels.md).
// Only this machine's terminals count; other machines' have documents of
// their own.
func (a *App) reconcile() {
	if !a.loaded || !a.connected {
		return
	}
	now := time.Now()
	for addr, at := range a.pending {
		if _, ok := a.terminals[addr]; ok || now.Sub(at) > pendingTimeout {
			if _, _, p := a.doc.PaneOfTerminal(addr); p != nil || now.Sub(at) > pendingTimeout {
				delete(a.pending, addr)
			}
		}
	}
	edited := false
	emptied := map[*layout.Tab]*layout.Space{}
	for _, s := range a.doc.Spaces {
		for _, t := range s.Tabs {
			for _, p := range t.Panes() {
				if p.Content == nil || p.Content.Terminal == "" || remote(p.Content.Terminal) {
					continue
				}
				addr := p.Content.Terminal
				rec, ok := a.terminals[addr]
				switch {
				case ok && rec.State == "finished":
					t.RemovePane(p.ID)
					edited = true
					emptied[t] = s
				case !ok:
					a.checkMissing(addr)
				}
			}
		}
	}
	// Prune only what this pass emptied: empty tabs and spaces other
	// apps wrote stay theirs.
	for t, s := range emptied {
		if len(t.Panes()) == 0 {
			s.RemoveTab(t.ID)
			if len(s.Tabs) == 0 {
				a.doc.RemoveSpace(s.ID)
			}
		}
	}
	for addr := range a.sy.orphanSince {
		if rec, ok := a.terminals[addr]; !ok || !a.orphan(rec) {
			delete(a.sy.orphanSince, addr)
		}
	}
	for _, rec := range a.terminals {
		if !a.orphan(rec) {
			continue
		}
		// Wait a little: the app that started it may not have saved its
		// pane yet, and a terminal ending soon needs none.
		since, seen := a.sy.orphanSince[rec.Address]
		if !seen {
			if a.sy.orphanSince == nil {
				a.sy.orphanSince = map[string]time.Time{}
			}
			a.sy.orphanSince[rec.Address] = now
			a.later(adoptGrace, a.reconcile)
			continue
		}
		if now.Sub(since) >= adoptGrace {
			a.startAdoption(rec)
		}
	}
	if edited {
		a.changed()
	}
}

// remote reports whether a terminal address is another machine's
// ("machine/t7").
func remote(addr string) bool { return strings.Contains(addr, "/") }

// orphan reports whether a terminal is one of ours that no pane shows.
func (a *App) orphan(rec core.Terminal) bool {
	if rec.State != "live" || remote(rec.Address) || a.gone[rec.Address] {
		return false
	}
	if _, waiting := a.pending[rec.Address]; waiting {
		return false
	}
	if rec.Labels["herdr.space"] == "" || a.spawning[rec.Labels["herdr.tab"]] > 0 {
		return false
	}
	_, _, p := a.doc.PaneOfTerminal(rec.Address)
	return p == nil
}

// startAdoption confirms with the core that an orphan is still live and
// still labelled the same, keeps it, reads the latest document, and then
// gives it a pane if it still has none.
func (a *App) startAdoption(rec core.Terminal) {
	if a.sy.adopting[rec.Address] || a.core == nil {
		return
	}
	if a.sy.adopting == nil {
		a.sy.adopting = map[string]bool{}
	}
	a.sy.adopting[rec.Address] = true
	be, gen, addr := a.core, a.sy.gen, rec.Address
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		fresh, err := be.Get(ctx, addr)
		ok := err == nil && fresh.State == "live" &&
			fresh.Labels["herdr.space"] == rec.Labels["herdr.space"] &&
			fresh.Labels["herdr.tab"] == rec.Labels["herdr.tab"]
		if ok && !fresh.Keep {
			ok = be.Keep(ctx, addr, true) == nil
		}
		var value json.RawMessage
		var version uint64
		if ok {
			value, version, err = be.DocGet(ctx, docName, "")
			ok = err == nil
		}
		a.post(func() {
			if gen != a.sy.gen {
				return
			}
			delete(a.sy.adopting, addr)
			if !ok {
				return
			}
			a.remoteDoc(value, version)
			if a.saving || a.dragging || version > a.version {
				// The document is moving: decide on a later pass.
				a.later(adoptGrace, a.reconcile)
				return
			}
			if cur, known := a.terminals[addr]; known {
				fresh.Labels = cur.Labels
			}
			if a.orphan(fresh) {
				delete(a.sy.orphanSince, addr)
				a.adopt(fresh)
				a.changed()
			}
		})
	}()
}

// checkMissing asks the core about a terminal a pane shows that its event
// stream does not list, closing the pane when the core no longer has it
// or it finished.
func (a *App) checkMissing(addr string) {
	if _, waiting := a.pending[addr]; waiting || a.core == nil {
		return
	}
	if at, ok := a.sy.checking[addr]; ok && time.Since(at) < missingRecheck {
		return
	}
	if a.sy.checking == nil {
		a.sy.checking = map[string]time.Time{}
	}
	a.sy.checking[addr] = time.Now()
	be, gen := a.core, a.sy.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		rec, err := be.Get(ctx, addr)
		gone := core.IsKind(err, "not_found") || (err == nil && rec.State == "finished")
		if !gone {
			return
		}
		a.post(func() {
			if gen != a.sy.gen {
				return
			}
			if _, listed := a.terminals[addr]; !listed {
				a.closeTerminalPanes(addr)
			}
		})
	}()
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

// syncState is the bookkeeping of keeping the document and the core's
// terminals in step. It belongs to the main thread.
type syncState struct {
	gen         int                  // counts connections, to drop replies from lost ones
	saveDone    chan struct{}        // closed when the latest save's call returned
	saveFails   int                  // saves failed in a row, not for a conflict
	saveAfter   time.Time            // no save before then
	orphanSince map[string]time.Time // live terminals of ours without a pane, since
	adopting    map[string]bool      // adoptions being confirmed with the core
	checking    map[string]time.Time // pane terminals the core was last asked about
	attachFails map[string]int       // streams that broke in a row, by terminal
}

const (
	callTimeout    = 10 * time.Second
	drainTimeout   = 4 * time.Second
	saveRetry      = 2 * time.Second
	adoptGrace     = 2 * time.Second
	pendingTimeout = 10 * time.Second
	missingRecheck = 10 * time.Second
)
