package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
	"github.com/duskoide/shiori/internal/recovery"
)

// restartedCore is a fake core that starts terminals, as a new core does:
// at addresses the old one may have used.
type restartedCore struct {
	*fakeCore
	next []string // the addresses it gives out, in order
	runs []core.RunParams
}

func (f *restartedCore) Run(_ context.Context, p core.RunParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, p)
	addr := f.next[0]
	f.next = f.next[1:]
	f.terms[addr] = core.Terminal{Address: addr, State: "live", CreatedMs: 99, Labels: p.Labels}
	return addr, nil
}

func (f *restartedCore) ran() []core.RunParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.RunParams(nil), f.runs...)
}

var (
	oldCore = recovery.Core{Pid: 100, StartMs: time.Now().Add(-48 * time.Hour).UnixMilli()}
	// newCore has run long enough to have settled.
	newCore = recovery.Core{Pid: 200, StartMs: time.Now().Add(-time.Minute).UnixMilli()}
)

// restartApp is an app that connects to a new core, whose document is
// coreDoc (nil: none) and whose terminals are terms, with a snapshot of
// the panes seen under the old core.
func restartApp(t *testing.T, coreDoc *layout.Document, proc recovery.Core, panes map[string]recovery.Pane, terms ...core.Terminal) (*App, *restartedCore) {
	t.Helper()
	var value json.RawMessage
	var version uint64
	if coreDoc != nil {
		var err error
		if value, err = coreDoc.Encode(); err != nil {
			t.Fatal(err)
		}
		version = 1
	}
	f := &restartedCore{fakeCore: &fakeCore{doc: value, version: version, terms: map[string]core.Terminal{}}, next: []string{"t1", "t2", "t3"}}
	for _, tm := range terms {
		f.terms[tm.Address] = tm
	}
	a := newApp(t.TempDir(), "test")
	a.rc.snap.Core, a.rc.snap.Panes = oldCore, panes
	if coreDoc == nil {
		// What the window showed before.
		before := oneSpace()
		before.Spaces[0].Tabs[0].Panes()[0].ID = "p1"
		a.rc.snap.Layout, _ = before.Encode()
	}
	a.core, a.connected = f, true
	a.sy.gen = 1
	a.connectedCore(proc)
	a.setTerminals(terms)
	a.remoteDoc("", value, version)
	return a, f
}

// paneDoc is oneSpace with its pane's id known.
func paneDoc() *layout.Document {
	d := oneSpace()
	d.Spaces[0].Tabs[0].Panes()[0].ID = "p1"
	return d
}

func TestLostTerminalGetsAFreshShellInItsPane(t *testing.T) {
	dir := t.TempDir()
	panes := map[string]recovery.Pane{"p1": {Terminal: "t1", CreatedMs: 5, Cwd: dir, Core: oldCore}}
	a, f := restartApp(t, paneDoc(), newCore, panes)
	if !a.isLost("p1") {
		t.Fatal("the pane's terminal is not seen as lost")
	}
	// The new core gives the new shell the old one's address.
	until(t, a, "the restore", func() bool { return len(f.ran()) == 1 && !a.rc.busy })
	_, _, p := a.doc.Pane("p1")
	if p == nil || p.Content.Terminal != "t1" || a.isLost("p1") {
		t.Fatalf("pane %+v lost %v", p, a.isLost("p1"))
	}
	run := f.ran()[0]
	if run.Cwd != dir || !run.Keep || len(run.Argv) != 0 || run.Labels["herdr.space"] != "s1" || run.Labels["herdr.tab"] != "tab1" {
		t.Fatalf("run %+v", run)
	}
	// It stays restored: the reused address is the new terminal's.
	a.setTerminals([]core.Terminal{{Address: "t1", State: "live", CreatedMs: 99, Labels: run.Labels}})
	for i := 0; i < 5; i++ {
		a.runPosted()
		a.flush()
	}
	if len(f.ran()) != 1 || a.isLost("p1") {
		t.Fatalf("restored again: %d runs", len(f.ran()))
	}
	if _, _, p := a.doc.Pane("p1"); p == nil {
		t.Fatal("the pane closed")
	}
}

func TestTerminalBackFromAHolderStays(t *testing.T) {
	panes := map[string]recovery.Pane{"p1": {Terminal: "t1", CreatedMs: 5, Cwd: "/", Core: oldCore}}
	a, f := restartApp(t, paneDoc(), newCore, panes, core.Terminal{Address: "t1", State: "live", CreatedMs: 5})
	a.reconcile()
	if a.isLost("p1") || len(f.ran()) != 0 {
		t.Fatal("a terminal that outlived its core is restarted")
	}
	if rec, ok := a.record(a.doc.Spaces[0].Tabs[0].Panes()[0]); !ok || rec.Address != "t1" {
		t.Fatal("the pane does not show its terminal")
	}
}

func TestAnotherTerminalAtTheAddressIsNotShown(t *testing.T) {
	panes := map[string]recovery.Pane{"p1": {Terminal: "t1", CreatedMs: 5, Cwd: "/", Core: oldCore}}
	a, f := restartApp(t, paneDoc(), newCore, panes, live("t1", "elsewhere", "x"))
	if !a.isLost("p1") {
		t.Fatal("another terminal at the lost one's address is taken for it")
	}
	if _, ok := a.record(a.doc.Spaces[0].Tabs[0].Panes()[0]); ok {
		t.Fatal("the pane shows the other terminal's record")
	}
	until(t, a, "the restore", func() bool { return len(f.ran()) == 1 && !a.rc.busy })
}

func TestRestoreWaitsForTheCoreToSettle(t *testing.T) {
	panes := map[string]recovery.Pane{"p1": {Terminal: "t1", CreatedMs: 5, Cwd: "/", Core: oldCore}}
	young := recovery.Core{Pid: 300, StartMs: time.Now().Add(-restoreSettle + 300*time.Millisecond).UnixMilli()}
	a, f := restartApp(t, paneDoc(), young, panes)
	a.runPosted()
	if len(f.ran()) != 0 {
		t.Fatal("restored before the core settled")
	}
	if _, _, p := a.doc.Pane("p1"); p == nil {
		t.Fatal("the pane closed while waiting")
	}
	until(t, a, "the restore", func() bool { return len(f.ran()) == 1 && !a.rc.busy })
}

func TestEndedTerminalStillClosesItsPane(t *testing.T) {
	// Seen under the core still running: it ended, it was not lost.
	panes := map[string]recovery.Pane{"p1": {Terminal: "t1", CreatedMs: 5, Cwd: "/", Core: newCore}}
	a, f := restartApp(t, paneDoc(), newCore, panes)
	until(t, a, "the pane to close", func() bool { _, _, p := a.doc.Pane("p1"); return p == nil })
	if len(f.ran()) != 0 {
		t.Fatal("an ended terminal was restarted")
	}
}

func TestRestoreOff(t *testing.T) {
	panes := map[string]recovery.Pane{"p1": {Terminal: "t1", CreatedMs: 5, Cwd: "/", Core: oldCore}}
	value, _ := paneDoc().Encode()
	f := &restartedCore{fakeCore: &fakeCore{doc: value, version: 1, terms: map[string]core.Terminal{}}}
	a := newApp(t.TempDir(), "test")
	off := false
	a.cfg.Restore = &off
	a.rc.snap.Core, a.rc.snap.Panes = oldCore, panes
	a.core, a.connected, a.sy.gen = f, true, 1
	a.connectedCore(newCore)
	a.setTerminals(nil)
	a.remoteDoc("", value, 1)
	until(t, a, "the pane to close", func() bool { _, _, p := a.doc.Pane("p1"); return p == nil })
	if len(f.ran()) != 0 {
		t.Fatal("restored with restore off")
	}
}

func TestLayoutPutBackInACoreThatLostIt(t *testing.T) {
	panes := map[string]recovery.Pane{"p1": {Terminal: "t1", CreatedMs: 5, Cwd: "/", Core: oldCore}}
	a, f := restartApp(t, nil, newCore, panes)
	if len(a.doc.Spaces) != 1 || a.doc.Spaces[0].ID != "s1" {
		t.Fatalf("the layout was not put back: %d spaces", len(a.doc.Spaces))
	}
	until(t, a, "the restore", func() bool { return len(f.ran()) == 1 && !a.rc.busy })
	until(t, a, "the save", func() bool { _, _, p := f.stored(t).Pane("p1"); return p != nil })
}

func TestLayoutNotPutBackInTheSameCore(t *testing.T) {
	a, _ := restartApp(t, nil, oldCore, nil)
	if len(a.doc.Spaces) != 0 {
		t.Fatal("put back a layout in the core the snapshot saw")
	}
}

func TestSnapshotKeepsThePanesTerminals(t *testing.T) {
	a, _ := restartApp(t, paneDoc(), newCore, nil, core.Terminal{Address: "t1", State: "live", CreatedMs: 42, Cwd: "/home",
		Foreground: &core.Foreground{Cwd: "/home/src"}, Labels: map[string]string{"herdr.space": "s1", "herdr.tab": "tab1"}})
	a.writeSnapshot()
	s, err := recovery.Load(a.snapshotPath())
	if err != nil {
		t.Fatal(err)
	}
	got := s.Panes["p1"]
	if got.Terminal != "t1" || got.CreatedMs != 42 || got.Cwd != "/home/src" || !got.Core.Same(newCore) || !s.Core.Same(newCore) {
		t.Fatalf("%+v", s)
	}
	if d, err := layout.Decode(s.Layout); err != nil || d.Space("s1") == nil {
		t.Fatalf("layout %v", err)
	}
	// Nothing changed: no new write.
	before, _ := os.Stat(a.snapshotPath())
	time.Sleep(10 * time.Millisecond)
	a.writeSnapshot()
	if after, _ := os.Stat(a.snapshotPath()); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("rewrote an unchanged snapshot")
	}
}

func TestPaneWaitsWhenTheCoreStopsWithItsTerminal(t *testing.T) {
	stop, exit := "server_stop", "exited"
	d := paneDoc()
	d.Spaces[0].Tabs[0].Split("p1", layout.Right, "t2")
	a, _ := syncApp(t, d, live("t1", "s1", "tab1"), live("t2", "s1", "tab1"))
	a.setTerminals([]core.Terminal{
		{Address: "t1", State: "finished", Reason: &stop},
		{Address: "t2", State: "finished", Reason: &exit},
	})
	until(t, a, "the ended pane to close", func() bool { _, _, p := a.doc.PaneOfTerminal("t2"); return p == nil })
	if _, _, p := a.doc.Pane("p1"); p == nil {
		t.Fatal("closed the pane of a terminal its core took along")
	}
}

func TestOtherMachinesPanesAreNotRestored(t *testing.T) {
	a := newApp(t.TempDir(), "test")
	a.rc.core = newCore
	a.rc.snap.Panes = map[string]recovery.Pane{"mac/p1": {Terminal: "mac/t1", CreatedMs: 5, Cwd: "/", Core: oldCore}}
	if _, lost := a.lostPane("mac/p1", "mac/t1"); lost {
		t.Fatal("this core's restart says nothing of another machine's terminals")
	}
}
