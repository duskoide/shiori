package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ch      uint32
		kind    byte
		payload []byte
	}{
		{"empty", 0, kindClose, nil},
		{"data", 7, kindData, []byte("hello")},
		{"big channel", 0xdeadbeef, kindFacts, []byte(`{"cell":[1,2]}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := encodeFrame(tc.ch, tc.kind, tc.payload)
			if len(b) != headerLen+len(tc.payload) {
				t.Fatalf("len %d", len(b))
			}
			f, err := readFrame(bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			if f.channel != tc.ch || f.kind != tc.kind || !bytes.Equal(f.payload, tc.payload) {
				t.Fatalf("got %+v", f)
			}
		})
	}
}

func TestFrameWireLayout(t *testing.T) {
	got := encodeFrame(1, kindResize, resizePayload(120, 40))
	want := []byte{1, 0, 0, 0, 3, 4, 0, 0, 0, 120, 0, 40, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if !bytes.Equal(creditPayload(0x01020304), []byte{4, 3, 2, 1}) {
		t.Fatal("credit payload")
	}
	if !bytes.Equal(resizePayload(0, 70000), []byte{1, 0, 0xff, 0xff}) {
		t.Fatal("resize clamp")
	}
}

func TestReadFrameErrors(t *testing.T) {
	big := encodeFrame(1, kindData, nil)
	big[5], big[6], big[7], big[8] = 0, 0, 0, 0x02 // 32 MiB
	for _, tc := range []struct {
		name string
		in   []byte
		want error
	}{
		{"eof", nil, io.EOF},
		{"short header", []byte{1, 2, 3}, io.ErrUnexpectedEOF},
		{"truncated payload", encodeFrame(1, kindData, []byte("abcd"))[:11], io.ErrUnexpectedEOF},
		{"too large", big, errFrameTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readFrame(bytes.NewReader(tc.in)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	ok := func() Hello {
		h := Hello{Version: "0.1.16", Generation: 1, Methods: append([]string(nil), requiredMethods...)}
		return h
	}
	for _, tc := range []struct {
		name string
		mut  func(*Hello)
		want error
	}{
		{"ok", func(*Hello) {}, nil},
		{"exact min", func(h *Hello) { h.Version = "0.1.11" }, nil},
		{"numeric compare", func(h *Hello) { h.Version = "0.10.0" }, nil},
		{"prerelease suffix", func(h *Hello) { h.Version = "0.2.0-rc1" }, nil},
		{"old patch", func(h *Hello) { h.Version = "0.1.10" }, ErrCoreTooOld},
		{"old minor", func(h *Hello) { h.Version = "0.0.99" }, ErrCoreTooOld},
		{"garbage version", func(h *Hello) { h.Version = "x" }, ErrCoreTooOld},
		{"generation newer", func(h *Hello) { h.Generation = 2 }, ErrCoreNewer},
		{"generation older", func(h *Hello) { h.Generation = 0 }, ErrCoreTooOld},
		{"missing method", func(h *Hello) { h.Methods = h.Methods[1:] }, ErrCoreMissing},
		{"no methods", func(h *Hello) { h.Methods = nil }, ErrCoreMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := ok()
			tc.mut(&h)
			err := Check(h)
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string][3]int{"0.1.16": {0, 1, 16}, "hc 0.1.16": {0, 1, 16}, "v1.2.3+b": {1, 2, 3}} {
		if got, ok := parseVersion(in); !ok || got != want {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "1.2", "a.b.c", "1.x.3"} {
		if _, ok := parseVersion(in); ok {
			t.Errorf("%q parsed", in)
		}
	}
	a, _ := parseVersion("0.10.0")
	b, _ := parseVersion("0.9.3")
	if compareVersions(a, b) != 1 || compareVersions(b, a) != -1 || compareVersions(a, a) != 0 {
		t.Error("compare")
	}
}

func TestHelloFailures(t *testing.T) {
	leakCheck(t)
	for _, tc := range []struct {
		name string
		fo   fakeOpts
		want func(error) bool
	}{
		{"old core", fakeOpts{hello: func(h map[string]any) { h["version"] = "0.1.2" }}, func(e error) bool { return errors.Is(e, ErrCoreTooOld) }},
		{"newer generation", fakeOpts{hello: func(h map[string]any) { h["generation"] = 2 }}, func(e error) bool { return errors.Is(e, ErrCoreNewer) }},
		{"missing method", fakeOpts{hello: func(h map[string]any) { h["methods"] = []string{"hello"} }}, func(e error) bool { return errors.Is(e, ErrCoreMissing) }},
		{"not framed", fakeOpts{hello: func(h map[string]any) { h["framed"] = false }}, func(e error) bool { return strings.Contains(e.Error(), "framed") }},
		{"error reply", fakeOpts{rawHello: `{"jsonrpc":"2.0","id":0,"error":{"code":-32000,"message":"nope","data":{"kind":"unsupported_generation","hint":"update"}}}`}, func(e error) bool { return IsKind(e, "unsupported_generation") }},
		{"garbage reply", fakeOpts{rawHello: "not json"}, func(e error) bool { return e != nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c1, c2 := net.Pipe()
			defer c2.Close()
			startFake(t, c2, tc.fo)
			c, err := newClient(context.Background(), c1, time.Second)
			if err == nil {
				c.Close()
				t.Fatal("expected error")
			}
			if !tc.want(err) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestHelloContext(t *testing.T) {
	leakCheck(t)
	c1, c2 := net.Pipe()
	defer c2.Close()
	startFake(t, c2, fakeOpts{silent: true})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := newClient(ctx, c1, 5*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestHelloAccessors(t *testing.T) {
	c, _ := newPair(t, fakeOpts{})
	h := c.Hello()
	if h.Version != "0.1.16" || h.Generation != 1 || h.Core.Name != "box" || h.Core.ID != "abc" || len(h.Features) != 3 {
		t.Fatalf("%+v", h)
	}
}

func TestCallOutOfOrder(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	const n = 5
	results := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct {
				Echo string `json:"echo"`
			}
			if err := c.Call(context.Background(), "echo", map[string]any{"i": i}, &out); err != nil {
				t.Error(err)
			}
			results[i] = out.Echo
		}()
	}
	calls := make([]call, n)
	for i := range calls {
		calls[i] = f.nextCall()
	}
	ids := map[uint64]bool{}
	for _, cl := range calls {
		if cl.ID == 0 || ids[cl.ID] || cl.Method != "echo" {
			t.Fatalf("bad call %+v", cl)
		}
		ids[cl.ID] = true
	}
	for i := n - 1; i >= 0; i-- { // reverse order
		var p struct {
			I int `json:"i"`
		}
		_ = json.Unmarshal(calls[i].Params, &p)
		f.reply(calls[i].ID, map[string]any{"echo": string(rune('a' + p.I))})
	}
	wg.Wait()
	for i, r := range results {
		if r != string(rune('a'+i)) {
			t.Fatalf("results %v", results)
		}
	}
}

func TestCallNilParamsAndResult(t *testing.T) {
	c, f := newPair(t, fakeOpts{})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "x.y", nil, nil) }()
	cl := f.nextCall()
	if string(cl.Params) != "{}" {
		t.Fatalf("params %s", cl.Params)
	}
	f.reply(cl.ID, map[string]any{"ignored": true})
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestRPCError(t *testing.T) {
	c, f := newPair(t, fakeOpts{})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "terminal.get", map[string]any{"terminal": "t9"}, nil) }()
	cl := f.nextCall()
	f.replyErr(cl.ID, -32000, "no such terminal", "not_found", "try terminal.list")
	err := <-errc
	var re *RPCError
	if !errors.As(err, &re) {
		t.Fatalf("got %T %v", err, err)
	}
	if re.Code != -32000 || re.Message != "no such terminal" || re.Kind != "not_found" || re.Hint != "try terminal.list" {
		t.Fatalf("%+v", re)
	}
	if !IsKind(err, "not_found") || IsKind(err, "conflict") || IsKind(io.EOF, "x") {
		t.Fatal("IsKind")
	}
	if s := re.Error(); !strings.Contains(s, "no such terminal") || !strings.Contains(s, "not_found") || !strings.Contains(s, "try terminal.list") {
		t.Fatal(s)
	}
}

func TestCallContextAndTimeout(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- c.Call(ctx, "slow", nil, nil) }()
	f.nextCall()
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}

	c.timeout = 50 * time.Millisecond
	go func() { errc <- c.Call(context.Background(), "slow", nil, nil) }()
	f.nextCall()
	if err := <-errc; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending leaked: %d", n)
	}
	// a late reply for a forgotten call must be ignored
	f.reply(1, "late")
	f.reply(2, "late")
}

func TestConnectionLost(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "slow", nil, nil) }()
	f.nextCall()
	f.conn.Close()
	if err := <-errc; !errors.Is(err, ErrLost) {
		t.Fatalf("got %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed")
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrLost) {
		t.Fatalf("got %v", err)
	}
}

func TestClose(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "slow", nil, nil) }()
	f.nextCall()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("not done")
	}
	if err := c.send(0, kindData, nil); err == nil {
		t.Fatal("send after close")
	}
}

func TestHelpers(t *testing.T) {
	c, f := newPair(t, fakeOpts{})
	ctx := context.Background()

	run := func(fn func() error, handle func(call)) {
		t.Helper()
		errc := make(chan error, 1)
		go func() { errc <- fn() }()
		handle(f.nextCall())
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	params := func(cl call) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(cl.Params, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	var addr string
	run(func() (err error) {
		addr, err = c.Run(ctx, RunParams{Argv: []string{"zsh"}, Cwd: "/tmp", Labels: map[string]string{"a": "b"}, Keep: true,
			Env: map[string]string{"TERM": "screen", "X": "1"}})
		return
	}, func(cl call) {
		if cl.Method != "terminal.run" {
			t.Fatal(cl.Method)
		}
		p := params(cl)
		var env map[string]string
		_ = json.Unmarshal(p["env"], &env)
		if env["TERM"] != "screen" || env["COLORTERM"] != "truecolor" || env["TERM_PROGRAM"] != "herdr" || env["X"] != "1" {
			t.Fatalf("env %v", env)
		}
		if string(p["argv"]) != `["zsh"]` || string(p["cwd"]) != `"/tmp"` || string(p["keep"]) != "true" || string(p["labels"]) != `{"a":"b"}` {
			t.Fatalf("params %s", cl.Params)
		}
		if _, ok := p["on"]; ok {
			t.Fatal("on should be omitted")
		}
		f.reply(cl.ID, map[string]any{"address": "t5", "state": "live"})
	})
	if addr != "t5" {
		t.Fatal(addr)
	}

	run(func() error { _, err := c.Run(ctx, RunParams{}); return err }, func(cl call) {
		p := params(cl)
		if string(p["argv"]) != "[]" {
			t.Fatalf("argv %s", p["argv"])
		}
		f.reply(cl.ID, map[string]any{"address": "m/t1"})
	})

	errc := make(chan error, 1)
	go func() { _, err := c.Run(ctx, RunParams{}); errc <- err }()
	cl := f.nextCall()
	f.reply(cl.ID, map[string]any{})
	if err := <-errc; err == nil {
		t.Fatal("empty address accepted")
	}

	run(func() error { return c.Kill(ctx, "t5") }, func(cl call) {
		if cl.Method != "terminal.kill" || string(params(cl)["terminal"]) != `"t5"` {
			t.Fatalf("%+v", cl)
		}
		f.reply(cl.ID, map[string]any{"address": "t5"})
	})
	run(func() error { return c.Label(ctx, "t5", map[string]string{"herdr.seen": ""}) }, func(cl call) {
		if cl.Method != "terminal.label" || string(params(cl)["labels"]) != `{"herdr.seen":""}` {
			t.Fatalf("%+v", cl)
		}
		f.reply(cl.ID, map[string]any{})
	})
	run(func() error { return c.Keep(ctx, "t5", true) }, func(cl call) {
		if cl.Method != "terminal.keep" || string(params(cl)["keep"]) != "true" {
			t.Fatalf("%+v", cl)
		}
		f.reply(cl.ID, map[string]any{})
	})
	var term Terminal
	run(func() (err error) { term, err = c.Get(ctx, "t5"); return }, func(cl call) {
		f.reply(cl.ID, map[string]any{"address": "t5", "state": "live", "cols": 80, "rows": 24, "turn": 3,
			"status": map[string]any{"state": "done", "app": "pi"}, "name": "n", "exit_code": nil,
			"foreground": map[string]any{"pid": 4, "name": "pi", "argv": []string{"pi"}, "cwd": "/"}})
	})
	if term.Address != "t5" || term.Cols != 80 || term.Turn != 3 || term.Status == nil || term.Status.App != "pi" ||
		term.Name == nil || *term.Name != "n" || term.ExitCode != nil || term.Foreground.Pid != 4 || !term.Live() {
		t.Fatalf("%+v", term)
	}

	var val json.RawMessage
	var ver uint64
	run(func() (err error) { val, ver, err = c.DocGet(ctx, "herdr-app", "mac"); return }, func(cl call) {
		if cl.Method != "doc.get" || string(params(cl)["on"]) != `"mac"` {
			t.Fatalf("%+v", cl)
		}
		f.reply(cl.ID, map[string]any{"value": map[string]any{"spaces": []any{}}, "version": 7})
	})
	if string(val) != `{"spaces":[]}` || ver != 7 {
		t.Fatal(string(val), ver)
	}
	run(func() (err error) { val, ver, err = c.DocGet(ctx, "herdr-app", ""); return }, func(cl call) {
		if _, ok := params(cl)["on"]; ok {
			t.Fatal("on should be omitted")
		}
		f.reply(cl.ID, map[string]any{"value": nil, "version": 0})
	})
	if string(val) != "null" || ver != 0 {
		t.Fatal(string(val), ver)
	}

	var nv uint64
	run(func() (err error) { nv, err = c.DocSet(ctx, "herdr-app", "", 7, json.RawMessage(`{"a":1}`)); return }, func(cl call) {
		p := params(cl)
		if cl.Method != "doc.set" || string(p["version"]) != "7" || string(p["value"]) != `{"a":1}` {
			t.Fatalf("%s", cl.Params)
		}
		f.reply(cl.ID, map[string]any{"version": 8})
	})
	if nv != 8 {
		t.Fatal(nv)
	}

	go func() { _, err := c.DocSet(ctx, "herdr-app", "", 1, nil); errc <- err }()
	cl = f.nextCall()
	if string(params(cl)["value"]) != "null" {
		t.Fatalf("%s", cl.Params)
	}
	f.replyErr(cl.ID, -32000, "version is 9", "conflict", "re-read")
	if err := <-errc; !IsKind(err, "conflict") {
		t.Fatal(err)
	}

	var ms []Machine
	run(func() (err error) { ms, err = c.Machines(ctx); return }, func(cl call) {
		f.reply(cl.ID, map[string]any{"machines": []any{map[string]any{"name": "mac", "state": "connected", "since_ms": 5, "via": true, "temporary": false}}})
	})
	if len(ms) != 1 || ms[0].Name != "mac" || ms[0].State != "connected" || ms[0].SinceMs != 5 || !ms[0].Via {
		t.Fatalf("%+v", ms)
	}

	var st ServerStatus
	run(func() (err error) { st, err = c.Status(ctx); return }, func(cl call) {
		if cl.Method != "server.status" {
			t.Fatal(cl.Method)
		}
		f.reply(cl.ID, map[string]any{"core": "abc", "version": "0.1.16", "pid": 4242, "uptime_ms": 90000, "live": 3, "finished": 1})
	})
	if st.Pid != 4242 || st.UptimeMs != 90000 || st.Live != 3 || st.Version != "0.1.16" {
		t.Fatalf("%+v", st)
	}
}

func TestDisplayJSON(t *testing.T) {
	for _, tc := range []struct {
		d    Display
		want string
	}{
		{Display{}, `{}`},
		{Display{FG: "#fff", BG: "#000", Cursor: "#f00", Cell: [2]int{9, 18}}, `{"fg":"#fff","bg":"#000","cursor":"#f00","cell":[9,18]}`},
		{Display{Cell: [2]int{9, 0}}, `{}`},
	} {
		b, _ := json.Marshal(tc.d)
		if string(b) != tc.want {
			t.Errorf("got %s want %s", b, tc.want)
		}
	}
}

func TestEarlyFrames(t *testing.T) {
	c, _ := newPair(t, fakeOpts{})
	rec := &recSink{}
	c.dispatch(frame{channel: 9, kind: kindData, payload: []byte("a")})
	c.dispatch(frame{channel: 9, kind: kindData, payload: []byte("b")})
	c.dispatch(frame{channel: 8, kind: kindData, payload: []byte("other")})
	c.register(9, rec)
	c.dispatch(frame{channel: 9, kind: kindData, payload: []byte("c")})
	if got := rec.joined(); got != "abc" {
		t.Fatalf("got %q", got)
	}
	c.smu.Lock()
	_, leftover := c.early[8]
	c.smu.Unlock()
	if !leftover {
		t.Fatal("unrelated channel buffer lost")
	}

	// early buffer is bounded
	for i := 0; i < maxEarlyFrames+50; i++ {
		c.dispatch(frame{channel: 5, kind: kindData, payload: []byte("x")})
	}
	c.smu.Lock()
	n := len(c.early[5])
	c.smu.Unlock()
	if n != maxEarlyFrames {
		t.Fatalf("early len %d", n)
	}

	// registering on a dead client reports loss
	c.Close()
	rec2 := &recSink{}
	c.register(3, rec2)
	if !rec2.wasLost() {
		t.Fatal("not told about loss")
	}
}

type recSink struct {
	mu   sync.Mutex
	data []byte
	lerr error
}

func (r *recSink) deliver(f frame) {
	r.mu.Lock()
	r.data = append(r.data, f.payload...)
	r.mu.Unlock()
}
func (r *recSink) lost(err error) { r.mu.Lock(); r.lerr = err; r.mu.Unlock() }
func (r *recSink) joined() string { r.mu.Lock(); defer r.mu.Unlock(); return string(r.data) }
func (r *recSink) wasLost() bool  { r.mu.Lock(); defer r.mu.Unlock(); return r.lerr != nil }

func TestSingleWriterNoInterleave(t *testing.T) {
	c, f := newPair(t, fakeOpts{})
	const writers, per = 8, 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte('a' + w)}, 1000)
			for i := 0; i < per; i++ {
				_ = c.send(uint32(w+1), kindData, payload)
			}
		}()
	}
	wg.Wait()
	counts := map[uint32]int{}
	for i := 0; i < writers*per; i++ {
		fr := f.next()
		if len(fr.payload) != 1000 || fr.payload[0] != byte('a'+fr.channel-1) || fr.payload[999] != fr.payload[0] {
			t.Fatalf("corrupt frame on channel %d", fr.channel)
		}
		counts[fr.channel]++
	}
	for w := uint32(1); w <= writers; w++ {
		if counts[w] != per {
			t.Fatalf("channel %d: %d frames", w, counts[w])
		}
	}
}

func TestConnectUnixSocket(t *testing.T) {
	leakCheck(t)
	sock := filepath.Join(t.TempDir(), "c.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			startFake(t, conn, fakeOpts{})
		}
	}()
	c, err := Connect(context.Background(), Options{Socket: sock})
	if err != nil {
		t.Fatal(err)
	}
	if c.Hello().Version != "0.1.16" {
		t.Fatal(c.Hello())
	}
	c.Close()

	_, err = Connect(context.Background(), Options{Socket: filepath.Join(t.TempDir(), "none.sock")})
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestSocketCandidates(t *testing.T) {
	if got := SocketCandidates(Options{Socket: "/x/y.sock"}); len(got) != 1 || got[0] != "/x/y.sock" {
		t.Fatal(got)
	}
	state := t.TempDir()
	t.Setenv("HC_SOCKET", "")
	t.Setenv("HC_SESSION", "")
	t.Setenv("XDG_RUNTIME_DIR", "/run/xdgtest")
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(state, "herdr-core", "work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.lock"), []byte("/elsewhere/core.sock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := SocketCandidates(Options{Session: "work"})
	want := []string{"/run/xdgtest/herdr-core/work.sock", "/elsewhere/core.sock", filepath.Join(dir, "work.sock")}
	if got[0] != want[0] || got[len(got)-2] != want[1] || got[len(got)-1] != want[2] {
		t.Fatalf("got %v want %v", got, want)
	}
	t.Setenv("HC_SESSION", "envsess")
	if got := SocketCandidates(Options{}); got[0] != "/run/xdgtest/herdr-core/envsess.sock" {
		t.Fatal(got)
	}
	if sessionName(Options{Session: "x"}) != "x" {
		t.Fatal("session option")
	}
	t.Setenv("HC_SESSION", "")
	if sessionName(Options{}) != "default" {
		t.Fatal("default session")
	}
	t.Setenv("HC_SOCKET", "/env/s.sock")
	if got := SocketCandidates(Options{}); len(got) != 1 || got[0] != "/env/s.sock" {
		t.Fatal(got)
	}
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestSearchBinary(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeScript(t, filepath.Join(a, "herdr-core"), `echo "not hc 1"`)
	writeScript(t, filepath.Join(a, "hc"), `echo "hc 0.1.16"`)
	writeScript(t, filepath.Join(b, "herdr-core"), `echo "hc 0.1.16"`)
	if err := os.WriteFile(filepath.Join(b, "hc"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	// first dir: herdr-core rejected (wrong banner), hc accepted
	if got := searchBinary([]string{"", a, b, a}, binaryNames, isHC); got != filepath.Join(a, "hc") {
		t.Fatal(got)
	}
	if got := searchBinary([]string{b}, binaryNames, isHC); got != filepath.Join(b, "herdr-core") {
		t.Fatal(got) // non-executable hc skipped, herdr-core ok
	}
	if got := searchBinary([]string{t.TempDir()}, binaryNames, isHC); got != "" {
		t.Fatal(got)
	}
	if got, err := FindBinary("/some/explicit/hc"); err != nil || got != "/some/explicit/hc" {
		t.Fatal(got, err)
	}
	if !isHC(filepath.Join(a, "hc")) || isHC(filepath.Join(a, "herdr-core")) || isHC(filepath.Join(a, "missing")) {
		t.Fatal("isHC")
	}
	t.Setenv("PATH", b)
	if got, err := FindBinary(""); err != nil || got == "" {
		t.Skipf("no binary found through PATH: %v", err) // extra dirs may or may not exist
	}
}

func TestAutoStartPump(t *testing.T) {
	leakCheck(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "hc")
	hello := `{"jsonrpc":"2.0","id":0,"result":{"version":"0.1.16","generation":1,"framed":true,"methods":["terminal.run","terminal.list","terminal.kill","terminal.label","terminal.attach","terminal.keep","doc.get","doc.set","events.subscribe"],"features":[],"core":{"id":"1","name":"pump"}}}`
	writeScript(t, bin, "read line\nprintf '%s\\n' '"+hello+"'\nexec sleep 30")
	sock := filepath.Join(dir, "absent.sock")

	if _, err := Connect(context.Background(), Options{Socket: sock, Binary: bin}); err == nil {
		t.Fatal("without AutoStart a dial failure must fail")
	}
	c, err := Connect(context.Background(), Options{Socket: sock, Binary: bin, AutoStart: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.Hello().Core.Name != "pump" {
		t.Fatal(c.Hello())
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}

	_, err = Connect(context.Background(), Options{Socket: sock, Binary: filepath.Join(dir, "nope"), AutoStart: true})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestBundledBinaryDirs(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	want := filepath.Join(filepath.Dir(filepath.Dir(exe)), "libexec", "shiori")
	if got := bundledBinaryDirs(); len(got) != 1 || got[0] != want {
		t.Fatalf("bundledBinaryDirs() = %v, want [%s]", got, want)
	}
}
