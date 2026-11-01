package core

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// newTestEvents builds an Events detached from any connection.
func newTestEvents(t *testing.T, terms []Terminal, seq uint64) *Events {
	t.Helper()
	c := &Client{done: make(chan struct{}), sinks: map[uint32]sink{}, early: map[uint32][]frame{}, wsig: make(chan struct{}, 1)}
	e := &Events{
		c: c, terms: terms, seq: seq,
		sig: make(chan struct{}, 1), out: make(chan Change, 1), stop: make(chan struct{}),
	}
	go e.pump()
	t.Cleanup(func() { e.Close() })
	return e
}

func waitChange(t *testing.T, e *Events) Change {
	t.Helper()
	select {
	case ch, ok := <-e.Changes():
		if !ok {
			t.Fatal("Changes closed")
		}
		return ch
	case <-time.After(2 * time.Second):
		t.Fatal("no change")
	}
	return Change{}
}

func noChange(t *testing.T, e *Events) {
	t.Helper()
	select {
	case ch, ok := <-e.Changes():
		t.Fatalf("unexpected change %+v (open=%v)", ch, ok)
	case <-time.After(60 * time.Millisecond):
	}
}

func snapshot() []Terminal {
	name := "alpha"
	return []Terminal{
		{Address: "t1", State: "live", Title: "one", Labels: map[string]string{"k": "v"}, Argv: []string{"zsh"}, Pid: ptr(10),
			Foreground: &Foreground{Pid: 11, Name: "vim"}, Cols: 80, Rows: 24, Name: &name},
		{Address: "t2", State: "live"},
		{Address: "t3", State: "finished"},
	}
}

func ptr[T any](v T) *T { return &v }

func TestApplyEvents(t *testing.T) {
	for _, tc := range []struct {
		name      string
		typ, term string
		data      string
		check     func(t *testing.T, ts []Terminal)
		wantTerms bool
		wantDocs  bool
		wantSeen  map[string]uint64
	}{
		{name: "created", typ: "created", term: "t9", data: `{"address":"t9","state":"live","title":"new"}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if len(ts) != 4 || ts[3].Address != "t9" || ts[3].Title != "new" {
					t.Fatalf("%+v", ts)
				}
			}},
		{name: "restarted replaces", typ: "restarted", term: "t2", data: `{"address":"t2","state":"live","title":"again","cols":10,"rows":5}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if len(ts) != 3 || ts[1].Title != "again" || ts[1].Cols != 10 {
					t.Fatalf("%+v", ts)
				}
			}},
		{name: "lost", typ: "lost", term: "t1", data: `{"reason":"signaled","exit_code":null,"signal":9,"finished_ms":null}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				x := ts[0]
				if x.State != "live" || x.Reason == nil || *x.Reason != "signaled" || x.Signal == nil || x.Pid != nil || x.Foreground != nil {
					t.Fatalf("%+v", x)
				}
			}},
		{name: "finished", typ: "finished", term: "t1", data: `{"reason":"exited","exit_code":3,"signal":null,"finished_ms":99}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				x := ts[0]
				if x.State != "finished" || x.ExitCode == nil || *x.ExitCode != 3 || x.FinishedMs == nil || *x.FinishedMs != 99 || x.Foreground != nil || x.Pid != nil {
					t.Fatalf("%+v", x)
				}
			}},
		{name: "removed", typ: "removed", term: "t2", data: `{}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if len(ts) != 2 || ts[0].Address != "t1" || ts[1].Address != "t3" {
					t.Fatalf("%+v", ts)
				}
			}},
		{name: "keep", typ: "keep", term: "t1", data: `{"keep":true}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if !ts[0].Keep {
					t.Fatal("keep")
				}
			}},
		{name: "status", typ: "status", term: "t1", data: `{"status":{"state":"working","app":"pi","kind":"k","msg":"m"},"records":{"a":1},"reported":true}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				x := ts[0]
				if x.Status == nil || x.Status.State != "working" || x.Status.Msg != "m" || !x.Reported || string(x.Records) != `{"a":1}` {
					t.Fatalf("%+v", x)
				}
			}},
		{name: "status cleared", typ: "status", term: "t1", data: `{"status":null,"records":{},"reported":false}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if ts[0].Status != nil {
					t.Fatal("status not cleared")
				}
			}},
		{name: "turn", typ: "turn", term: "t1", data: `{"turn":7,"state":"done"}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if ts[0].Turn != 7 {
					t.Fatal(ts[0].Turn)
				}
			}},
		{name: "title", typ: "title", term: "t1", data: `{"title":"new title"}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if ts[0].Title != "new title" {
					t.Fatal(ts[0].Title)
				}
			}},
		{name: "size", typ: "size", term: "t1", data: `{"cols":132,"rows":43}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if ts[0].Cols != 132 || ts[0].Rows != 43 {
					t.Fatal(ts[0])
				}
			}},
		{name: "foreground set", typ: "foreground", term: "t2", data: `{"pid":5,"name":"pi","argv":["pi"],"cwd":"/w"}`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if f := ts[1].Foreground; f == nil || f.Pid != 5 || f.Cwd != "/w" {
					t.Fatal(f)
				}
			}},
		{name: "foreground cleared", typ: "foreground", term: "t1", data: `null`, wantTerms: true,
			check: func(t *testing.T, ts []Terminal) {
				if ts[0].Foreground != nil {
					t.Fatal("not cleared")
				}
			}},
		{name: "labels replace", typ: "labels", term: "t1", data: `{"labels":{"herdr.seen":"4","x":"y"}}`, wantTerms: true, wantSeen: map[string]uint64{"t1": 4},
			check: func(t *testing.T, ts []Terminal) {
				if !reflect.DeepEqual(ts[0].Labels, map[string]string{"herdr.seen": "4", "x": "y"}) {
					t.Fatal(ts[0].Labels)
				}
			}},
		{name: "labels with record", typ: "labels", term: "t7", data: `{"labels":{"herdr.seen":"2"},"record":{"address":"t7","state":"live","labels":{"herdr.seen":"2"}}}`, wantTerms: true, wantSeen: map[string]uint64{"t7": 2},
			check: func(t *testing.T, ts []Terminal) {
				if len(ts) != 4 || ts[3].Address != "t7" {
					t.Fatalf("%+v", ts)
				}
			}},
		{name: "labels bad seen ignored", typ: "labels", term: "t1", data: `{"labels":{"herdr.seen":"nope"}}`, wantTerms: true},
		{name: "doc", typ: "doc", term: "", data: `{"name":"herdr-app","version":3}`, wantDocs: true,
			check: func(t *testing.T, ts []Terminal) {
				if len(ts) != 3 {
					t.Fatal(ts)
				}
			}},
		{name: "unknown terminal ignored", typ: "title", term: "zz", data: `{"title":"x"}`},
		{name: "unknown type ignored", typ: "wibble", term: "t1", data: `{}`},
		{name: "bad data ignored", typ: "size", term: "t1", data: `"oops"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEvents(t, snapshot(), 10)
			e.mu.Lock()
			e.feedLocked(eventLine(11, tc.typ, tc.term, json.RawMessage(tc.data)))
			e.mu.Unlock()
			if tc.check != nil {
				tc.check(t, e.Terminals())
			}
			if !tc.wantTerms && !tc.wantDocs && tc.wantSeen == nil {
				noChange(t, e)
				return
			}
			ch := waitChange(t, e)
			if ch.Terminals != tc.wantTerms || ch.Docs != tc.wantDocs {
				t.Fatalf("change %+v", ch)
			}
			if tc.wantSeen != nil && !reflect.DeepEqual(ch.Seen, tc.wantSeen) {
				t.Fatalf("seen %v", ch.Seen)
			}
		})
	}
}

func TestEventsSeqFilterAndSplit(t *testing.T) {
	e := newTestEvents(t, snapshot(), 10)
	line := func(seq uint64, title string) []byte {
		return eventLine(seq, "title", "t1", map[string]string{"title": title})
	}
	feed := func(p []byte) {
		e.mu.Lock()
		e.feedLocked(p)
		e.mu.Unlock()
	}
	title := func() string { return e.Terminals()[0].Title }

	feed(line(10, "stale")) // seq <= last: ignored
	feed(line(9, "older"))
	if title() != "one" {
		t.Fatal(title())
	}
	noChange(t, e)

	// frame boundaries mid-line, and several lines in one frame
	all := append(append(line(11, "a"), line(12, "b")...), line(13, "c")...)
	for _, cut := range []int{1, 7, len(line(11, "a")) - 1, len(line(11, "a")), len(line(11, "a")) + 5} {
		e.mu.Lock()
		e.seq = 10
		e.terms[0].Title = "one"
		e.mu.Unlock()
		feed(all[:cut])
		feed(all[cut:])
		if title() != "c" {
			t.Fatalf("cut %d: title %q", cut, title())
		}
		waitChange(t, e)
	}
	e.mu.Lock()
	if e.seq != 13 || len(e.buf) != 0 {
		t.Fatalf("seq %d buf %q", e.seq, e.buf)
	}
	e.mu.Unlock()

	// a partial line is kept until its newline arrives
	l := line(14, "d")
	feed(l[:len(l)-1])
	if title() != "c" {
		t.Fatal("applied early")
	}
	feed(l[len(l)-1:])
	if title() != "d" {
		t.Fatal(title())
	}
	waitChange(t, e)

	// blank lines, garbage and heartbeats are harmless
	feed([]byte("\n  \nnot json\n{\"jsonrpc\":\"2.0\",\"method\":\"heartbeat\",\"params\":{\"seq\":99}}\n"))
	feed(line(15, "e"))
	if title() != "e" {
		t.Fatal(title()) // heartbeat must not advance seq
	}
	waitChange(t, e)
}

func TestEventsMissed(t *testing.T) {
	e := newTestEvents(t, snapshot(), 10)
	missed := `{"jsonrpc":"2.0","method":"missed","params":{"seq":57,"terminals":[{"address":"t5","state":"live","labels":{"herdr.seen":"6"}}]}}` + "\n"
	e.mu.Lock()
	e.feedLocked([]byte(missed))
	e.mu.Unlock()
	ts := e.Terminals()
	if len(ts) != 1 || ts[0].Address != "t5" {
		t.Fatalf("%+v", ts)
	}
	ch := waitChange(t, e)
	if !ch.Docs || !ch.Terminals || ch.Seen["t5"] != 6 {
		t.Fatalf("%+v", ch)
	}
	// events at or below the missed seq are stale now
	e.mu.Lock()
	e.feedLocked(eventLine(40, "created", "t8", map[string]any{"address": "t8", "state": "live"}))
	e.feedLocked(eventLine(58, "created", "t9", map[string]any{"address": "t9", "state": "live"}))
	e.mu.Unlock()
	if ts := e.Terminals(); len(ts) != 2 || ts[1].Address != "t9" {
		t.Fatalf("%+v", ts)
	}
}

func TestEventsCoalesce(t *testing.T) {
	e := newTestEvents(t, snapshot(), 0)
	// the consumer is not reading: the feeder must never block
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.mu.Lock()
		defer e.mu.Unlock()
		seq := uint64(0)
		for i := 0; i < 500; i++ {
			seq++
			e.feedLocked(eventLine(seq, "title", "t1", map[string]string{"title": "x"}))
		}
		for _, v := range []string{"3", "9", "5"} {
			seq++
			e.feedLocked(eventLine(seq, "labels", "t1", map[string]any{"labels": map[string]string{"herdr.seen": v}}))
		}
		seq++
		e.feedLocked(eventLine(seq, "doc", "", map[string]any{"name": "herdr-app", "version": 1}))
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("feeding blocked on a slow consumer")
	}
	var got Change
	n := 0
	for !got.Docs {
		ch := waitChange(t, e)
		got.merge(ch)
		n++
	}
	if !got.Terminals || got.Seen["t1"] != 9 {
		t.Fatalf("%+v", got)
	}
	if n > 3 {
		t.Fatalf("not coalesced: %d notifications", n)
	}
}

func TestChangeMerge(t *testing.T) {
	a := Change{Seen: map[string]uint64{"t1": 3}}
	a.merge(Change{Terminals: true, Seen: map[string]uint64{"t1": 2, "t2": 1}})
	a.merge(Change{Docs: true})
	if !a.Docs || !a.Terminals || a.Seen["t1"] != 3 || a.Seen["t2"] != 1 {
		t.Fatalf("%+v", a)
	}
	if !(Change{}).empty() || a.empty() {
		t.Fatal("empty")
	}
}

func TestEventsAccessorsCopy(t *testing.T) {
	e := newTestEvents(t, snapshot(), 0)
	ts := e.Terminals()
	ts[0].Labels["k"] = "changed"
	ts[0].Argv[0] = "bash"
	ts[0].Foreground.Name = "x"
	ts[0].Status = &Status{}
	again := e.Terminals()
	if again[0].Labels["k"] != "v" || again[0].Argv[0] != "zsh" || again[0].Foreground.Name != "vim" || again[0].Status != nil {
		t.Fatal("snapshot aliased")
	}
	live := e.Live()
	if len(live) != 2 || live[0].Address != "t1" || live[1].Address != "t2" {
		t.Fatalf("%+v", live)
	}
	if tt, ok := e.Terminal("t3"); !ok || tt.State != "finished" {
		t.Fatal(tt)
	}
	if _, ok := e.Terminal("zz"); ok {
		t.Fatal("found zz")
	}
}

// subscribed performs Subscribe against the fake core on channel ch.
func subscribed(t *testing.T, ch uint32, early func(f *fakeCore)) (*Client, *fakeCore, *Events) {
	t.Helper()
	c, f := newPair(t, fakeOpts{})
	type res struct {
		e   *Events
		err error
	}
	rc := make(chan res, 1)
	go func() {
		e, err := c.Subscribe(context.Background())
		rc <- res{e, err}
	}()
	cl := f.nextCall()
	if cl.Method != "events.subscribe" {
		t.Fatal(cl.Method)
	}
	f.reply(cl.ID, map[string]any{"seq": 5, "channel": ch, "terminals": []Terminal{{Address: "t1", State: "live", Title: "one"}}})
	if early != nil {
		early(f)
	}
	r := <-rc
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := f.expect(ch, kindCredit); !reflect.DeepEqual(got, creditPayload(1<<20)) {
		t.Fatalf("initial credit %v", got)
	}
	return c, f, r.e
}

func TestSubscribeEndToEnd(t *testing.T) {
	leakCheck(t)
	c, f, e := subscribed(t, 4, nil)
	if ts := e.Terminals(); len(ts) != 1 || ts[0].Title != "one" {
		t.Fatalf("%+v", ts)
	}

	l1 := eventLine(6, "title", "t1", map[string]string{"title": "two"})
	l2 := eventLine(7, "turn", "t1", map[string]any{"turn": 2, "state": "done"})
	payload := append(append([]byte(nil), l1...), l2...)
	f.raw(4, kindData, payload[:10])
	f.raw(4, kindData, payload[10:])
	for i, n := range []int{10, len(payload) - 10} {
		if got := f.expect(4, kindCredit); !reflect.DeepEqual(got, creditPayload(n)) {
			t.Fatalf("credit %d: %v", i, got)
		}
	}
	ch := waitChange(t, e)
	if !ch.Terminals {
		t.Fatal(ch)
	}
	for e.Terminals()[0].Turn != 2 { // second change may still be in flight
		waitChange(t, e)
	}
	if e.Terminals()[0].Title != "two" {
		t.Fatal(e.Terminals()[0])
	}

	// the stream ends with CLOSE: Changes is closed
	f.raw(4, kindClose, nil)
	for {
		select {
		case _, ok := <-e.Changes():
			if !ok {
				goto closed
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Changes not closed on CLOSE")
		}
	}
closed:
	e.Close()
	f.expectNone() // already ended: no CLOSE frame sent
	_ = c
}

func TestSubscribeEarlyFrames(t *testing.T) {
	// events arrive right behind the subscribe reply, before registration
	_, f, e := subscribed(t, 6, func(f *fakeCore) {
		f.raw(6, kindData, eventLine(5, "title", "t1", map[string]string{"title": "dup"}))   // seq == snapshot: dropped
		f.raw(6, kindData, eventLine(6, "title", "t1", map[string]string{"title": "early"})) // applied
	})
	deadline := time.Now().Add(2 * time.Second)
	for e.Terminals()[0].Title != "early" {
		if time.Now().After(deadline) {
			t.Fatalf("early frame lost: %q", e.Terminals()[0].Title)
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = f
}

func TestSubscribeNoChannel(t *testing.T) {
	c, f := newPair(t, fakeOpts{})
	errc := make(chan error, 1)
	go func() { _, err := c.Subscribe(context.Background()); errc <- err }()
	cl := f.nextCall()
	f.reply(cl.ID, map[string]any{"seq": 1, "channel": nil, "terminals": []any{}})
	if err := <-errc; err == nil {
		t.Fatal("expected error")
	}
}

func TestEventsConnectionLost(t *testing.T) {
	leakCheck(t)
	_, f, e := subscribed(t, 3, nil)
	f.conn.Close()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-e.Changes():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("Changes not closed on loss")
		}
	}
}

func TestEventsClose(t *testing.T) {
	leakCheck(t)
	_, f, e := subscribed(t, 3, nil)
	e.Close()
	e.Close()
	f.expect(3, kindClose)
	select {
	case _, ok := <-e.Changes():
		if ok {
			t.Fatal("unexpected change")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Changes not closed")
	}
}

func TestEventsDocMachines(t *testing.T) {
	e := newTestEvents(t, snapshot(), 10)
	feed := func(line string) {
		e.mu.Lock()
		e.feedLocked([]byte(line + "\n"))
		e.mu.Unlock()
	}
	// A doc event names its machine in its params, or in its data, or
	// none: this machine.
	feed(`{"method":"event","params":{"seq":11,"type":"doc","machine":"mac","data":{"name":"herdr-app"}}}`)
	feed(`{"method":"event","params":{"seq":12,"type":"doc","data":{"name":"herdr-app","machine":"box"}}}`)
	feed(`{"method":"event","params":{"seq":13,"type":"doc","data":{"name":"herdr-app"}}}`)
	var got Change
	for !(got.DocMachines["mac"] && got.DocMachines["box"] && got.DocMachines[""]) {
		got.merge(waitChange(t, e))
	}
	if !got.DocChanged("mac") || !got.DocChanged("") || got.DocChanged("other") {
		t.Fatalf("%+v", got)
	}

	// Missed events: every machine's document.
	feed(`{"method":"missed","params":{"seq":20,"terminals":[]}}`)
	ch := waitChange(t, e)
	if !ch.Docs || ch.DocMachines != nil || !ch.DocChanged("anything") {
		t.Fatalf("%+v", ch)
	}
}

func TestChangeMergeDocMachines(t *testing.T) {
	one := func(m string) Change { return Change{Docs: true, DocMachines: map[string]bool{m: true}} }
	var c Change
	c.merge(Change{Terminals: true})
	if c.Docs || c.DocChanged("") {
		t.Fatalf("%+v", c)
	}
	src := one("mac")
	c.merge(src)
	c.merge(one("box"))
	if len(src.DocMachines) != 1 {
		t.Fatal("merge changed its argument's map")
	}
	if !c.DocChanged("mac") || !c.DocChanged("box") || c.DocChanged("") {
		t.Fatalf("%+v", c)
	}
	c.merge(Change{Docs: true}) // every machine's
	if c.DocMachines != nil || !c.DocChanged("") {
		t.Fatalf("%+v", c)
	}
	c.merge(one("mac")) // still every machine's
	if c.DocMachines != nil {
		t.Fatalf("%+v", c)
	}
}
