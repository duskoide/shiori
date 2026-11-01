package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
)

// fakeCore is a backend whose document and terminals the test controls.
type fakeCore struct {
	mu      sync.Mutex
	doc     json.RawMessage
	version uint64
	terms   map[string]core.Terminal
	setErr  error // the next DocSet fails with it
	sets    int
	kept    []string
}

func (f *fakeCore) Run(context.Context, core.RunParams) (string, error) { return "", errors.New("no") }
func (f *fakeCore) Kill(context.Context, string) error                  { return nil }
func (f *fakeCore) Label(context.Context, string, map[string]string) error {
	return nil
}

func (f *fakeCore) Keep(_ context.Context, addr string, keep bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kept = append(f.kept, addr)
	t := f.terms[addr]
	t.Keep = keep
	f.terms[addr] = t
	return nil
}

func (f *fakeCore) Get(_ context.Context, addr string) (core.Terminal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.terms[addr]
	if !ok {
		return t, &core.RPCError{Code: -32000, Message: "no such terminal", Kind: "not_found"}
	}
	return t, nil
}

func (f *fakeCore) DocSet(_ context.Context, _, _ string, version uint64, value json.RawMessage) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	if err := f.setErr; err != nil {
		f.setErr = nil
		return 0, err
	}
	if version != f.version {
		return 0, &core.RPCError{Code: -32000, Message: "version moved", Kind: "conflict"}
	}
	f.version++
	f.doc = append(json.RawMessage(nil), value...)
	return f.version, nil
}

func (f *fakeCore) DocGet(context.Context, string, string) (json.RawMessage, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.doc, f.version, nil
}

func (f *fakeCore) stored(t *testing.T) *layout.Document {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	d, err := layout.Decode(f.doc)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// syncApp is an app connected to a fake core holding doc.
func syncApp(t *testing.T, doc *layout.Document, terms ...core.Terminal) (*App, *fakeCore) {
	t.Helper()
	value, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCore{doc: value, version: 1, terms: map[string]core.Terminal{}}
	for _, tm := range terms {
		f.terms[tm.Address] = tm
	}
	a := newApp(t.TempDir(), "test")
	a.core, a.connected = f, true
	a.sy.gen = 1
	a.setTerminals(terms) // the order connectedTo and session1 keep
	a.remoteDoc(value, 1)
	return a, f
}

// until runs the posted work and saves until cond holds.
func until(t *testing.T, a *App, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		a.runPosted()
		a.flush()
		time.Sleep(5 * time.Millisecond)
	}
}

func live(addr, space, tab string) core.Terminal {
	return core.Terminal{Address: addr, State: "live", Labels: map[string]string{"herdr.space": space, "herdr.tab": tab}}
}

func oneSpace() *layout.Document {
	d := &layout.Document{}
	s := d.AddSpace("work", "/tmp")
	s.ID = "s1"
	t, _ := s.AddTab("", "t1")
	t.ID = "tab1"
	return d
}

func TestRemoteTerminalsAreNotAdopted(t *testing.T) {
	a, _ := syncApp(t, oneSpace(), live("t1", "s1", "tab1"), live("mac/t7", "s1", "tab1"))
	a.reconcile()
	if len(a.sy.orphanSince) != 0 {
		t.Fatalf("a remote terminal counts as an orphan: %v", a.sy.orphanSince)
	}
}

func TestAdoptionWaitsAndConfirms(t *testing.T) {
	a, f := syncApp(t, oneSpace(), live("t1", "s1", "tab1"), live("t2", "s1", "tab1"))
	if _, _, p := a.doc.PaneOfTerminal("t2"); p != nil {
		t.Fatal("adopted at once")
	}
	if _, ok := a.sy.orphanSince["t2"]; !ok {
		t.Fatal("the orphan is not tracked")
	}
	a.sy.orphanSince["t2"] = time.Now().Add(-adoptGrace) // the grace is over
	a.reconcile()
	until(t, a, "the adoption", func() bool { _, _, p := a.doc.PaneOfTerminal("t2"); return p != nil })
	until(t, a, "the save", func() bool { _, _, p := f.stored(t).PaneOfTerminal("t2"); return p != nil })
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.kept) != 1 || f.kept[0] != "t2" {
		t.Fatalf("kept = %v", f.kept)
	}
}

func TestAdoptionSkipsATerminalThatEnded(t *testing.T) {
	a, f := syncApp(t, oneSpace(), live("t1", "s1", "tab1"), live("t2", "s1", "tab1"))
	f.mu.Lock()
	f.terms["t2"] = core.Terminal{Address: "t2", State: "finished"}
	f.mu.Unlock()
	a.sy.orphanSince["t2"] = time.Now().Add(-adoptGrace)
	a.reconcile()
	until(t, a, "the check", func() bool { return !a.sy.adopting["t2"] })
	if _, _, p := a.doc.PaneOfTerminal("t2"); p != nil {
		t.Fatal("adopted a finished terminal")
	}
}

func TestFailedSaveIsRetried(t *testing.T) {
	a, f := syncApp(t, oneSpace(), live("t1", "s1", "tab1"))
	f.mu.Lock()
	f.setErr = errors.New("timeout")
	f.mu.Unlock()
	a.doc.Spaces[0].Name = "renamed"
	a.changed()
	a.flush()
	until(t, a, "the failure", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return !a.saving && f.sets == 1 })
	if a.doc.Spaces[0].Name != "renamed" || !a.dirty {
		t.Fatal("the edit was dropped")
	}
	a.sy.saveAfter = time.Time{} // skip the wait
	until(t, a, "the retry", func() bool { return f.stored(t).Spaces[0].Name == "renamed" })
}

func TestConflictTakesTheOtherCopy(t *testing.T) {
	a, f := syncApp(t, oneSpace(), live("t1", "s1", "tab1"))
	other := oneSpace()
	other.Spaces[0].Name = "theirs"
	value, _ := other.Encode()
	f.mu.Lock()
	f.doc, f.version = value, 2
	f.mu.Unlock()
	a.doc.Spaces[0].Name = "mine"
	a.changed()
	a.flush()
	until(t, a, "the conflict", func() bool { return a.version == 2 })
	if a.doc.Spaces[0].Name != "theirs" || a.dirty {
		t.Fatalf("doc %q dirty %v", a.doc.Spaces[0].Name, a.dirty)
	}
}

func TestStaleSaveReplyIsIgnored(t *testing.T) {
	a, _ := syncApp(t, oneSpace(), live("t1", "s1", "tab1"))
	a.doc.Spaces[0].Name = "x"
	a.changed()
	a.flush()
	a.sy.gen++ // the connection was replaced meanwhile
	a.saving = false
	<-a.sy.saveDone
	a.runPosted()
	if a.version != 1 {
		t.Fatalf("a stale reply moved the version to %d", a.version)
	}
}

func TestDrainSavesTheLastEdit(t *testing.T) {
	a, f := syncApp(t, oneSpace(), live("t1", "s1", "tab1"))
	a.doc.Spaces[0].Name = "last"
	a.changed() // not flushed yet
	a.drain()
	if f.stored(t).Spaces[0].Name != "last" {
		t.Fatal("the last edit was lost")
	}
}

func TestPaneOfAVanishedTerminalCloses(t *testing.T) {
	d := oneSpace()
	d.Spaces[0].AddTab("", "t9")
	a, _ := syncApp(t, d, live("t1", "s1", "tab1"))
	until(t, a, "the stale pane to close", func() bool { _, _, p := a.doc.PaneOfTerminal("t9"); return p == nil })
	if _, _, p := a.doc.PaneOfTerminal("t1"); p == nil {
		t.Fatal("closed the live pane too")
	}
}

func TestReconcileKeepsEmptyTabsItDidNotEmpty(t *testing.T) {
	d := oneSpace()
	d.Spaces[0].Tabs = append(d.Spaces[0].Tabs, &layout.Tab{ID: "empty"})
	a, _ := syncApp(t, d, live("t1", "s1", "tab1"))
	a.reconcile()
	if _, tab := a.doc.Tab("empty"); tab == nil {
		t.Fatal("pruned another app's empty tab")
	}
}
