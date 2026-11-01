package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
	"github.com/duskoide/shiori/internal/recovery"
)

// start connects to the core in the background, and again whenever the
// connection drops.
func (a *App) start() {
	ctx, cancel := context.WithCancel(context.Background())
	a.stop = cancel
	a.started = true
	a.startServices()
	a.scheduleUpdateCheck()
	go a.connectLoop(ctx)
	go a.watchConfig(ctx)
}

// shutdown detaches every terminal and closes the connection; the
// terminals keep running in the core.
func (a *App) shutdown() {
	a.cancelWaiting()
	a.stopServices()
	a.stopChanges()
	if a.updTimer != nil {
		a.updTimer.Stop()
	}
	a.flushSettings()
	a.drain()
	a.writeSnapshot()
	if a.rc.timer != nil {
		a.rc.timer.Stop()
		a.rc.timer = nil
	}
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
// herdr-app does: it waits for the saves in flight, then saves what is
// left, each machine's document in its turn.
func (a *App) drain() {
	deadline := time.After(drainTimeout)
	for _, ds := range a.docs {
		if done := ds.saveDone; done != nil {
			select {
			case <-done:
			case <-deadline:
			}
		}
	}
	// The saves' replies are among the posted work.
	a.runPosted()
	if a.core == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	for m, ds := range a.docs {
		if !ds.loaded || ds.saving || !a.usable(m) {
			continue
		}
		part := a.part(m)
		if layout.Equal(part, ds.base) {
			continue
		}
		value, err := part.Encode()
		if err != nil {
			continue
		}
		if next, err := a.core.DocSet(ctx, docName, m, ds.version, value); err == nil {
			ds.base, ds.version, ds.dirty = part, next, false
		}
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
	// Which core process this is tells a restarted core from the one before.
	proc := coreStatus(ctx, cl)
	// Subscribe before reading the document, so no change falls between.
	ev, err := cl.Subscribe(ctx)
	if err != nil {
		return err
	}
	value, version, err := docGet(ctx, cl, "")
	if err != nil {
		return err
	}
	terms := ev.Terminals()
	machines, _ := machineList(ctx, cl)
	a.post(func() {
		a.connectedTo(cl, terms, proc)
		a.remoteDoc("", value, version)
		a.setMachines(machines)
	})
	// The machines are polled, as herdr-app does: often, and when the
	// terminals change, which they do when a machine comes or goes.
	poll := time.NewTicker(machinePoll)
	defer poll.Stop()
	var polled time.Time
	pollMachines := func() {
		polled = time.Now()
		if ms, err := machineList(ctx, cl); err == nil {
			a.post(func() { a.setMachines(ms) })
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-cl.Done():
			return errors.New("lost the core")
		case <-poll.C:
			pollMachines()
		case ch, ok := <-ev.Changes():
			if !ok {
				return errors.New("the core's event stream ended")
			}
			if ch.Terminals {
				terms := ev.Terminals()
				a.post(func() { a.setTerminals(terms) })
				if time.Since(polled) > machinePollMin {
					pollMachines()
				}
			}
			if ch.DocChanged("") {
				value, version, err := docGet(ctx, cl, "")
				if err != nil {
					return err
				}
				a.post(func() { a.remoteDoc("", value, version) })
			}
			if ch.Docs {
				// Other machines' documents are read off this loop: a
				// machine far away does not hold up this one's events.
				a.post(func() {
					for m := range a.docs {
						if m != "" && ch.DocChanged(m) {
							a.fetchDoc(m)
						}
					}
				})
			}
		}
	}
}

// docGet reads a machine's layout document ("" this one's), giving up on
// a core that hangs.
func docGet(ctx context.Context, be backend, machine string) (json.RawMessage, uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return be.DocGet(ctx, docName, machine)
}

// machineList lists the machines the core knows.
func machineList(ctx context.Context, be backend) ([]core.Machine, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return be.Machines(ctx)
}

func (a *App) connectedTo(cl *core.Client, terms []core.Terminal, proc recovery.Core) {
	a.sy.gen++
	a.client, a.core = cl, cl
	a.connectedCore(proc)
	a.connected, a.connErr = true, ""
	// Edits a lost connection kept from the core go out again.
	for m, ds := range a.docs {
		if ds.loaded && !layout.Equal(a.part(m), ds.base) {
			ds.dirty = true
		}
		ds.saveAfter = time.Time{}
	}
	a.setTerminals(terms)
}

func (a *App) disconnected(why string) {
	a.sy.gen++
	a.connected, a.connErr = false, why
	a.client, a.core = nil, nil
	for _, ds := range a.docs {
		ds.saving, ds.fetching = false, false
	}
	clear(a.sy.checking)
	clear(a.sy.adopting)
	clear(a.rc.lost)
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

// setMachines takes the core's list of machines. Temporary ones are left
// out, as herdr-app does: they are agents' short-lived sandboxes. A
// connected machine's document is read when it first connects and when
// it connects again (its since_ms changes); a machine that leaves the
// list takes its spaces away; one that is listed but not connected keeps
// them shown as they were.
func (a *App) setMachines(list []core.Machine) {
	var ms []core.Machine
	for _, m := range list {
		if !m.Temporary && m.Name != "" {
			ms = append(ms, m)
		}
	}
	slices.SortFunc(ms, func(x, y core.Machine) int { return strings.Compare(x.Name, y.Name) })
	a.machines = ms
	for _, m := range ms {
		if !m.Usable() {
			continue
		}
		if ds := a.ds(m.Name); !ds.loaded || ds.since != m.SinceMs {
			a.fetchDoc(m.Name)
		}
	}
	for name := range a.docs {
		if _, ok := a.machine(name); name != "" && !ok {
			a.dropMachine(name)
		}
	}
}

// machine is the machine of that name the core lists.
func (a *App) machine(name string) (core.Machine, bool) {
	for _, m := range a.machines {
		if m.Name == name {
			return m, true
		}
	}
	return core.Machine{}, false
}

// usable reports whether a machine's terminals and document can be used:
// this one's always, another's while it is connected.
func (a *App) usable(machine string) bool {
	if machine == "" {
		return true
	}
	m, ok := a.machine(machine)
	return ok && m.Usable()
}

// dropMachine takes the spaces of a machine that left the list away; its
// terminals keep running there.
func (a *App) dropMachine(name string) {
	delete(a.docs, name)
	if a.doc == nil {
		return
	}
	a.doc.Drop(name)
	a.fixView()
	a.dropUnshown()
}

// ds is the sync state of a machine's document ("" this one's).
func (a *App) ds(machine string) *docSync {
	ds := a.docs[machine]
	if ds == nil {
		ds = &docSync{}
		a.docs[machine] = ds
	}
	return ds
}

// local is the sync state of this machine's document.
func (a *App) local() *docSync { return a.ds("") }

// part is a machine's document as the window would save it: its spaces in
// that machine's ids, and the fields of its last copy that the window does
// not know.
func (a *App) part(machine string) *layout.Document {
	if a.doc == nil {
		return &layout.Document{}
	}
	p := a.doc.Part(machine)
	if ds := a.docs[machine]; machine != "" && ds != nil && ds.base != nil {
		p.Extra = ds.base.Extra
	}
	return p
}

// fetchDoc reads a machine's document in the background, one read in
// flight per machine.
func (a *App) fetchDoc(machine string) {
	ds := a.ds(machine)
	if ds.fetching || a.core == nil {
		return
	}
	ds.fetching = true
	be, gen := a.core, a.sy.gen
	m, _ := a.machine(machine)
	go func() {
		value, version, err := docGet(context.Background(), be, machine)
		a.post(func() {
			if gen != a.sy.gen {
				return
			}
			ds := a.docs[machine]
			if ds == nil {
				return // the machine left
			}
			ds.fetching = false
			if err != nil {
				ds.err = err.Error()
				return
			}
			if a.remoteDoc(machine, value, version) {
				ds.since = m.SinceMs
			}
		})
	}()
}

// remoteDoc takes a version of a machine's document its core has. It
// reports whether the window has that version now, or will read it again.
func (a *App) remoteDoc(machine string, value json.RawMessage, version uint64) bool {
	ds := a.ds(machine)
	// This machine's first document on a connection may be older than
	// ours: a new core that lost its documents counts from 0 again.
	first := machine == "" && ds.gen != a.sy.gen
	if ds.loaded && version <= ds.version && !(first && version < ds.version) {
		if first && version == ds.version {
			ds.gen = a.sy.gen
		}
		return true
	}
	if ds.saving || a.dragging {
		// The reply to our save tells which version is ours, and a drag
		// is not cut short: look again after either.
		ds.refetch = true
		return true
	}
	if machine == "" {
		ds.gen = a.sy.gen
	}
	d, err := layout.Decode(value)
	if err != nil {
		if machine == "" {
			a.connErr = "layout unavailable: " + err.Error()
		}
		ds.err = "layout unavailable: " + err.Error()
		return false
	}
	if a.doc == nil {
		if machine != "" {
			return false // this machine's comes first
		}
		a.doc = &layout.Document{}
	}
	if machine == "" && version == 0 && len(d.Spaces) == 0 {
		if r := a.restoreLayout(); r != nil {
			// A new core without the layout: put back the one kept here.
			a.doc.Replace(r, "")
			ds.base, ds.version, ds.loaded, ds.err = d, 0, true, ""
			ds.dirty = true
			a.notify("herdr-core lost the layout: Shiori put back the one it kept")
			a.fixView()
			a.dropUnshown()
			a.reconcile()
			return true
		}
	}
	if ds.loaded && !layout.Equal(a.part(machine), ds.base) {
		// Someone else saved first: their copy wins, as in herdr-app.
		a.notify(changedElsewhere(machine))
	}
	a.doc.Replace(d, machine)
	ds.base, ds.version, ds.loaded, ds.err = d, version, true, ""
	ds.dirty = false
	a.fixView()
	a.dropUnshown()
	a.reconcile()
	return true
}

func changedElsewhere(machine string) string {
	if machine == "" {
		return "Layout changed elsewhere"
	}
	return "Layout of " + machine + " changed elsewhere"
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
	for _, ds := range a.docs {
		ds.dirty = true
	}
	a.fixView()
	a.snapshotSoon()
}

// flush saves each machine's document that differs from its core's: one
// save in flight per machine, compare-and-set on the version.
func (a *App) flush() {
	if a.dragging || a.core == nil {
		return
	}
	for m, ds := range a.docs {
		a.flush1(m, ds)
	}
}

func (a *App) flush1(machine string, ds *docSync) {
	if ds.saving || !ds.loaded || !a.usable(machine) {
		return
	}
	if ds.refetch {
		ds.refetch = false
		a.fetchDoc(machine)
	}
	if !ds.dirty || time.Now().Before(ds.saveAfter) {
		return
	}
	ds.dirty = false
	snapshot := a.part(machine)
	if layout.Equal(snapshot, ds.base) {
		return
	}
	value, err := snapshot.Encode()
	if err != nil {
		a.notify("cannot save the layout: " + err.Error())
		return
	}
	ds.saving = true
	be, version, gen := a.core, ds.version, a.sy.gen
	done := make(chan struct{})
	ds.saveDone = done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		next, err := be.DocSet(ctx, docName, machine, version, value)
		conflict := core.IsKind(err, "conflict")
		var latest json.RawMessage
		var latestVersion uint64
		var getErr error
		if conflict {
			latest, latestVersion, getErr = be.DocGet(ctx, docName, machine)
		}
		a.post(func() {
			if gen != a.sy.gen || a.docs[machine] != ds {
				// A reply from a connection since lost, or about a
				// machine that left: connectedTo decides what to save.
				return
			}
			ds.saving = false
			switch {
			case err == nil:
				ds.base, ds.version = snapshot, next
				ds.saveFails = 0
				if !layout.Equal(a.part(machine), ds.base) {
					ds.dirty = true
				}
			case conflict:
				a.notify(changedElsewhere(machine))
				ds.refetch = false
				if getErr == nil {
					// Take the other copy; this edit is lost, as in
					// herdr-app.
					ds.base = a.part(machine)
					a.remoteDoc(machine, latest, latestVersion)
				} else {
					ds.refetch = true
				}
			default:
				// The edit stays and goes out again shortly.
				if ds.saveFails == 0 {
					what := "the layout"
					if machine != "" {
						what = "the layout of " + machine
					}
					a.notify("cannot save " + what + ": " + err.Error())
				}
				ds.saveFails++
				ds.dirty = true
				wait := saveRetry * time.Duration(min(ds.saveFails, 5))
				ds.saveAfter = time.Now().Add(wait)
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

// reconcile matches panes with terminals: a pane whose terminal finished
// or is gone closes, and a live terminal labelled with one of our spaces
// that no pane shows gets one (herdr-app's adoption, docs/labels.md).
// Another machine's terminals count once it is connected and its document
// read: until then nothing is known of them.
func (a *App) reconcile() {
	if !a.local().loaded || !a.connected {
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
				if p.Content == nil || p.Content.Terminal == "" || !a.terminalsKnown(layout.MachineOf(p.Content.Terminal)) {
					continue
				}
				addr := p.Content.Terminal
				if a.rc.restoring[p.ID] {
					continue
				}
				if was, lost := a.lostPane(p.ID, addr); lost {
					// Its core stopped without it: it starts again below,
					// and the pane stays.
					a.markLost(p.ID, was)
					continue
				}
				delete(a.rc.lost, p.ID)
				rec, ok := a.terminals[addr]
				switch {
				case ok && rec.State == "finished" && a.restoreOn() && stoppedWithCore(rec) && layout.MachineOf(addr) == "":
					// The core is going, and took it along: the pane waits
					// for the next core, which starts it again.
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
	a.startRestore()
	if edited {
		a.changed()
	}
	a.snapshotSoon()
}

// terminalsKnown reports whether the window can tell which of a
// machine's terminals its panes should show: this machine's always,
// another's while it is connected and its document is read.
func (a *App) terminalsKnown(machine string) bool {
	if machine == "" {
		return true
	}
	ds := a.docs[machine]
	return ds != nil && ds.loaded && a.usable(machine)
}

// orphan reports whether a terminal is one of ours that no pane shows.
// Its labels name a space and tab as its machine's document does.
func (a *App) orphan(rec core.Terminal) bool {
	m := layout.MachineOf(rec.Address)
	if rec.State != "live" || !a.terminalsKnown(m) || a.gone[rec.Address] {
		return false
	}
	if _, waiting := a.pending[rec.Address]; waiting {
		return false
	}
	if rec.Labels["herdr.space"] == "" || a.spawning[layout.Qualify(rec.Labels["herdr.tab"], m)] > 0 {
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
	m := layout.MachineOf(addr)
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
			value, version, err = be.DocGet(ctx, docName, m)
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
			a.remoteDoc(m, value, version)
			if ds := a.ds(m); ds.saving || a.dragging || version > ds.version {
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

// adopt gives a labelled terminal a pane in its space and tab, on its
// machine.
func (a *App) adopt(rec core.Terminal) {
	m := layout.MachineOf(rec.Address)
	spaceID, tabID := layout.Qualify(rec.Labels["herdr.space"], m), layout.Qualify(rec.Labels["herdr.tab"], m)
	s := a.doc.Space(spaceID)
	if s == nil {
		s = a.doc.AddSpaceOn(m, "", rec.Cwd)
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

// docSync is the bookkeeping of one machine's layout document. It
// belongs to the main thread.
type docSync struct {
	base      *layout.Document // the last version its core has, in that machine's ids
	version   uint64           // base's number
	loaded    bool
	saving    bool
	dirty     bool
	refetch   bool          // a newer version arrived while a save was in flight
	fetching  bool          // a read of it is in flight
	since     int64         // the machine's since_ms when it was read
	err       string        // why it could not be read, if it could not
	saveDone  chan struct{} // closed when the latest save's call returned
	saveFails int           // saves failed in a row, not for a conflict
	saveAfter time.Time     // no save before then
	gen       int           // the connection its last version came on (this machine's)
}

// syncState is the bookkeeping of keeping the document and the core's
// terminals in step. It belongs to the main thread.
type syncState struct {
	gen         int                  // counts connections, to drop replies from lost ones
	orphanSince map[string]time.Time // live terminals of ours without a pane, since
	adopting    map[string]bool      // adoptions being confirmed with the core
	checking    map[string]time.Time // pane terminals the core was last asked about
	attachFails map[string]int       // streams that broke in a row, by terminal
}

const (
	machinePoll    = 2500 * time.Millisecond // herdr-app's: every 5th 500 ms tick
	machinePollMin = 500 * time.Millisecond  // and when terminals change, not more often
	callTimeout    = 10 * time.Second
	drainTimeout   = 4 * time.Second
	saveRetry      = 2 * time.Second
	adoptGrace     = 2 * time.Second
	pendingTimeout = 10 * time.Second
	missingRecheck = 10 * time.Second
)
