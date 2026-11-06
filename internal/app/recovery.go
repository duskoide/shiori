package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
	"github.com/duskoide/shiori/internal/recovery"
	"github.com/duskoide/shiori/internal/resume"
)

// Session recovery, as herdr's: when herdr-core stops without its
// terminals (a crash, a kill, a reboot), the panes keep their places and
// get fresh shells in the folders they worked in, instead of closing. The
// window keeps a snapshot (session.json in its home, see package recovery)
// of the layout and, for each pane, of its terminal and the core process
// that ran it.

// recoveryState is the bookkeeping of session recovery (main thread).
type recoveryState struct {
	snap     *recovery.Snapshot
	written  []byte      // the snapshot as last written, saved_ms aside
	writeErr string      // why the last write failed, told once
	timer    *time.Timer // the delayed write, if one is due

	core  recovery.Core // the core process connected to, if known
	fresh bool          // it is not the one the snapshot last saw

	lost      map[string]recovery.Pane // panes whose terminal was lost with its core, by pane id
	restoring map[string]bool          // of those, the ones whose terminal is being started again
	busy      bool                     // a restore is in flight
	settling  bool                     // a look once the core settled is due

	// The conversations the agents were last found in, by terminal
	// address (see resume.go).
	sessions  map[string]agentSession
	scanning  bool      // a look for them is in flight
	scannedAt time.Time // when the last one began or ended
	scanDue   bool      // a look is due after agentScanEvery
}

const (
	// snapshotDelay is how long a write of the snapshot waits for more
	// changes: a terminal's folder changes as often as twice a second.
	snapshotDelay = time.Second
	// restoreSettle is how long a new core runs before its terminal list
	// is taken as whole: terminals that outlived the old core in holder
	// processes come back to it.
	restoreSettle = 3 * time.Second
)

func (a *App) snapshotPath() string {
	return filepath.Join(a.home, recovery.FileName(a.session))
}

// loadSnapshot reads the snapshot from home; one that does not read is
// set aside, and replaced.
func (a *App) loadSnapshot() {
	s, err := recovery.Load(a.snapshotPath())
	if err != nil {
		a.notify("session recovery: " + err.Error())
		_ = os.Rename(a.snapshotPath(), a.snapshotPath()+".bad")
	}
	a.rc.snap = s
}

// restoreOn reports whether lost terminals are started again.
func (a *App) restoreOn() bool { return a.cfg.Restore == nil || *a.cfg.Restore }

func (a *App) setRestore(on bool) {
	a.cfg.Restore = &on
	if on {
		a.cfg.Restore = nil // the default
	}
	a.saveSettings()
	a.reconcile()
}

// coreStatus asks the core which process it is.
func coreStatus(ctx context.Context, cl *core.Client) recovery.Core {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	st, err := cl.Status(ctx)
	if err != nil {
		return recovery.Core{}
	}
	return recovery.At(st.Pid, st.UptimeMs, time.Now())
}

// connectedCore notes the core process of a new connection.
func (a *App) connectedCore(c recovery.Core) {
	last := a.rc.snap.Core
	a.rc.core = c
	a.rc.fresh = c.Known() && last.Known() && !last.Same(c)
	clear(a.rc.lost)
}

// lostPane reports whether the terminal a pane shows was lost with the
// core that ran it, and is to be started again.
func (a *App) lostPane(paneID, addr string) (recovery.Pane, bool) {
	// Only this machine's: another machine's terminals run in its own core.
	if !a.restoreOn() || a.rc.snap == nil || layout.MachineOf(addr) != "" {
		return recovery.Pane{}, false
	}
	rec, listed := a.terminals[addr]
	return a.rc.snap.Lost(paneID, addr, a.rc.core, listed, rec.CreatedMs)
}

// stoppedWithCore reports whether a terminal finished because its core
// stopped, not because its program ended or was killed.
func stoppedWithCore(rec core.Terminal) bool {
	if rec.Reason == nil {
		return false
	}
	switch *rec.Reason {
	case "server_stop", "host_shutdown", "holder_lost", "stopped_by_restart":
		return true
	}
	return false
}

// isLost reports whether a pane's terminal was lost with its core: its
// address means nothing to this core, or names another terminal.
func (a *App) isLost(paneID string) bool {
	_, ok := a.rc.lost[paneID]
	return ok
}

func (a *App) markLost(paneID string, was recovery.Pane) {
	if a.rc.lost == nil {
		a.rc.lost = map[string]recovery.Pane{}
	}
	a.rc.lost[paneID] = was
}

// restoreJob is a lost terminal to start again in its pane.
type restoreJob struct {
	pane, addr, space, tab string
	was                    recovery.Pane
	dirs                   []string // the folders to start in, the first that is one
	dir                    string   // the one it started in
	spec                   resume.Spec
	argv                   []string // what it started: nil a shell
	got                    string   // the new terminal's address
	err                    error
	back                   bool // it was not lost after all
}

// startRestore starts the lost terminals again, a fresh shell each in the
// folder it worked in, once the core has settled.
func (a *App) startRestore() {
	if a.rc.busy || len(a.rc.lost) == 0 || a.core == nil {
		return
	}
	if wait := restoreSettle - time.Since(time.UnixMilli(a.rc.core.StartMs)); wait > 0 {
		if !a.rc.settling {
			a.rc.settling = true
			a.later(wait, func() {
				a.rc.settling = false
				a.reconcile()
			})
		}
		return
	}
	var jobs []*restoreJob
	for paneID, was := range a.rc.lost {
		s, t, p := a.doc.Pane(paneID)
		if p == nil || p.Content == nil || p.Content.Terminal != was.Terminal {
			delete(a.rc.lost, paneID)
			continue
		}
		j := &restoreJob{pane: paneID, addr: was.Terminal, space: s.ID, tab: t.ID, was: was,
			dirs: []string{was.Cwd, s.Folder, homeDir()}}
		if a.resumeAgentsOn() {
			j.spec = resumeSpec(was.Agent)
		}
		jobs = append(jobs, j)
	}
	if len(jobs) == 0 {
		return
	}
	if a.rc.restoring == nil {
		a.rc.restoring = map[string]bool{}
	}
	if a.rc.snap.Panes == nil {
		a.rc.snap.Panes = map[string]recovery.Pane{}
	}
	for _, j := range jobs {
		a.rc.restoring[j.pane] = true
		// Until the pane shows it, the new terminal is not an orphan.
		a.spawning[j.tab]++
	}
	a.rc.busy = true
	be, gen := a.core, a.sy.gen
	go func() {
		for _, j := range jobs {
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			// Ask once more: a terminal back from a holder is not started
			// twice.
			if rec, err := be.Get(ctx, j.addr); err == nil && rec.State == "live" && j.was.CreatedMs != 0 && rec.CreatedMs == j.was.CreatedMs {
				j.back = true
				cancel()
				continue
			}
			labels := map[string]string{"herdr.space": j.space, "herdr.tab": j.tab, "herdr.seen": "0"}
			j.dir = firstDir(j.dirs)
			j.argv = resumeArgv(j.spec, j.was)
			j.got, j.err = be.Run(ctx, core.RunParams{Argv: j.argv, Cwd: j.dir, Labels: labels, Keep: true})
			cancel()
		}
		a.post(func() { a.restored(be, gen, jobs) })
	}()
}

// firstDir is the first of dirs that is a folder.
func firstDir(dirs []string) string {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	return ""
}

// restored puts the terminals a restore started in their panes.
func (a *App) restored(be backend, gen int, jobs []*restoreJob) {
	a.rc.busy = false
	for _, j := range jobs {
		delete(a.rc.restoring, j.pane)
		if a.spawning[j.tab]--; a.spawning[j.tab] <= 0 {
			delete(a.spawning, j.tab)
		}
	}
	if gen != a.sy.gen {
		// The connection they were started on is gone: they would be
		// orphans of a core nobody shows.
		killAll(be, jobs)
		return
	}
	restored, resumed, failed := 0, 0, 0
	var unused []*restoreJob
	for _, j := range jobs {
		delete(a.rc.lost, j.pane)
		if j.back {
			continue
		}
		s, t, p := a.doc.Pane(j.pane)
		if p == nil || p.Content == nil || p.Content.Terminal != j.addr {
			// The pane went, or shows another terminal now.
			unused = append(unused, j)
			continue
		}
		if j.err != nil {
			failed++
			a.dropPane(s, t, p.ID)
			continue
		}
		p.Content.Terminal = j.got
		// The pane's terminal is this core's now, even when the core gave
		// it the lost one's address.
		pane := recovery.Pane{Terminal: j.got, Cwd: j.dir, Core: a.rc.core}
		if j.argv != nil {
			// Until the agent is looked at again, it is still in that
			// conversation.
			pane.Agent, pane.SessionID, pane.SessionPath = j.was.Agent, j.was.SessionID, j.was.SessionPath
			resumed++
		}
		a.rc.snap.Panes[j.pane] = pane
		a.freshAddress(j.got)
		a.pending[j.got] = time.Now()
		restored++
	}
	killAll(be, unused)
	if restored+failed > 0 {
		a.changed()
		a.focusReq = a.focus[a.activeTab[a.activeSpace]]
	}
	switch {
	case resumed > 0 && failed == 0:
		msg := "herdr-core stopped: an agent started again on its conversation"
		if resumed > 1 {
			msg = "herdr-core stopped: " + plural(resumed, "agent", "agents") + " started again on their conversations"
		}
		if shells := restored - resumed; shells > 0 {
			msg += ", " + plural(shells, "pane gets a fresh shell", "panes get fresh shells")
		}
		a.notify(msg)
	case restored > 0 && failed > 0:
		a.notifyLevel("herdr-core stopped: "+plural(restored, "terminal", "terminals")+" started again, "+plural(failed, "terminal", "terminals")+" could not start", levelError)
	case restored == 1:
		a.notify("herdr-core stopped: a pane gets a fresh shell in its last folder")
	case restored > 1:
		a.notify("herdr-core stopped: " + plural(restored, "pane", "panes") + " get fresh shells in their last folders")
	case failed > 0:
		a.notifyLevel("herdr-core stopped, and "+plural(failed, "terminal", "terminals")+" could not start again", levelError)
	}
	a.reconcile()
}

// freshAddress forgets what this window knew of an address the core gave
// to a new terminal: it may have meant another before.
func (a *App) freshAddress(addr string) {
	delete(a.gone, addr)
	delete(a.localSeen, addr)
	delete(a.seenTurns, addr)
	delete(a.seenBusy, addr)
	delete(a.retry, addr)
	delete(a.sy.attachFails, addr)
	delete(a.sy.checking, addr)
}

// killAll ends the terminals of jobs that no pane is to show.
func killAll(be backend, jobs []*restoreJob) {
	var addrs []string
	for _, j := range jobs {
		if j.err == nil && j.got != "" && !j.back {
			addrs = append(addrs, j.got)
		}
	}
	if len(addrs) == 0 || be == nil {
		return
	}
	go func() {
		for _, addr := range addrs {
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			_ = be.Kill(ctx, addr)
			cancel()
		}
	}()
}

// restoreLayout is the layout to put back in a new core whose document is
// gone: what this window shows of this machine, else the snapshot's.
func (a *App) restoreLayout() *layout.Document {
	if !a.restoreOn() || !a.rc.fresh {
		return nil
	}
	if a.local().loaded && a.doc != nil {
		if p := a.part(""); len(p.Spaces) > 0 {
			return p
		}
	}
	if a.rc.snap == nil || len(a.rc.snap.Layout) == 0 {
		return nil
	}
	d, err := layout.Decode(a.rc.snap.Layout)
	if err != nil || len(d.Spaces) == 0 {
		return nil
	}
	return d
}

// snapshotSoon writes the snapshot after a short delay, one write for the
// changes made until then.
func (a *App) snapshotSoon() {
	if a.rc.timer != nil || a.rc.snap == nil {
		return
	}
	a.rc.timer = time.AfterFunc(snapshotDelay, func() {
		a.post(func() {
			a.rc.timer = nil
			a.writeSnapshot()
		})
	})
}

// flushSnapshot writes the snapshot now if a write is due.
func (a *App) flushSnapshot() {
	if a.rc.timer == nil {
		return
	}
	a.rc.timer.Stop()
	a.rc.timer = nil
	a.writeSnapshot()
}

// writeSnapshot brings the snapshot up to date and writes it when it
// changed.
func (a *App) writeSnapshot() {
	s := a.rc.snap
	if s == nil || !a.local().loaded || a.doc == nil {
		return
	}
	// This machine's spaces: other machines' are theirs to bring back.
	value, err := a.part("").Encode()
	if err != nil {
		return
	}
	s.Layout = value
	live := a.connected && a.rc.core.Known()
	if live {
		s.Core = a.rc.core
	}
	panes := map[string]recovery.Pane{}
	for _, sp := range a.doc.Spaces {
		for _, t := range sp.Tabs {
			for _, p := range t.Panes() {
				if p.Content == nil || p.Content.Terminal == "" || layout.MachineOf(p.Content.Terminal) != "" {
					continue
				}
				addr := p.Content.Terminal
				rec, ok := a.terminals[addr]
				if live && ok && rec.State == "live" && !a.isLost(p.ID) {
					cwd := rec.Cwd
					if rec.Foreground != nil && rec.Foreground.Cwd != "" {
						cwd = rec.Foreground.Cwd
					}
					pane := recovery.Pane{Terminal: addr, CreatedMs: rec.CreatedMs, Cwd: cwd, Core: a.rc.core}
					a.agentOf(rec, &pane)
					panes[p.ID] = pane
				} else if old, kept := s.Panes[p.ID]; kept && old.Terminal == addr {
					// Not to be seen now: what was known stays.
					panes[p.ID] = old
				}
			}
		}
	}
	s.Panes = panes
	s.SavedMs = 0
	b, err := s.Encode()
	if err != nil || bytes.Equal(b, a.rc.written) {
		return
	}
	s.SavedMs = time.Now().UnixMilli()
	out, err := s.Encode()
	if err != nil {
		return
	}
	if err := recovery.WriteFile(a.snapshotPath(), out); err != nil {
		if err.Error() != a.rc.writeErr {
			a.notify("session recovery: " + err.Error())
		}
		a.rc.writeErr = err.Error()
		return
	}
	a.rc.written, a.rc.writeErr = b, ""
}
