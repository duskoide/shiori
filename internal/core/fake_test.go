package core

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCore is an in-process herdr-core speaking the framed protocol.
type fakeCore struct {
	t    *testing.T
	conn net.Conn
	in   chan frame
	wmu  sync.Mutex
}

type fakeOpts struct {
	hello    func(map[string]any) // mutate the hello result
	rawHello string               // reply with this line instead
	silent   bool                 // never answer hello
}

func defaultHello() map[string]any {
	return map[string]any{
		"version": "0.1.16", "generation": 1, "framed": true,
		"features": []string{"framed", "attach", "events"},
		"methods":  append([]string{"hello"}, requiredMethods...),
		"core":     map[string]any{"id": "abc", "name": "box"},
	}
}

func startFake(t *testing.T, conn net.Conn, fo fakeOpts) *fakeCore {
	f := &fakeCore{t: t, conn: conn, in: make(chan frame, 4096)}
	go func() {
		defer close(f.in)
		br := bufio.NewReader(conn)
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if !strings.Contains(line, `"framed":true`) || !strings.Contains(line, `"method":"hello"`) {
			t.Errorf("bad hello line %q", line)
		}
		if fo.silent {
			_, _ = io.Copy(io.Discard, br)
			return
		}
		out := fo.rawHello
		if out == "" {
			h := defaultHello()
			if fo.hello != nil {
				fo.hello(h)
			}
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 0, "result": h})
			out = string(b)
		}
		if _, err := io.WriteString(conn, out+"\n"); err != nil {
			return
		}
		for {
			fr, err := readFrame(br)
			if err != nil {
				return
			}
			f.in <- fr
		}
	}()
	return f
}

// newPair connects a Client to a fake core over net.Pipe.
func newPair(t *testing.T, fo fakeOpts) (*Client, *fakeCore) {
	t.Helper()
	c1, c2 := net.Pipe()
	f := startFake(t, c2, fo)
	c, err := newClient(context.Background(), c1, 5*time.Second)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	t.Cleanup(func() { c.Close(); c2.Close() })
	return c, f
}

func (f *fakeCore) next() frame {
	f.t.Helper()
	select {
	case fr, ok := <-f.in:
		if !ok {
			f.t.Fatal("fake core: connection closed")
		}
		return fr
	case <-time.After(3 * time.Second):
		f.t.Fatal("fake core: timed out waiting for a frame")
	}
	return frame{}
}

// expectNone asserts that no frame arrives shortly.
func (f *fakeCore) expectNone() {
	f.t.Helper()
	select {
	case fr := <-f.in:
		f.t.Fatalf("unexpected frame %+v", fr)
	case <-time.After(60 * time.Millisecond):
	}
}

type call struct {
	ID     uint64
	Method string
	Params json.RawMessage
}

func (f *fakeCore) nextCall() call {
	f.t.Helper()
	fr := f.next()
	if fr.channel != 0 || fr.kind != kindData {
		f.t.Fatalf("expected a channel 0 call, got %+v", fr)
	}
	var m call
	if err := json.Unmarshal(fr.payload, &m); err != nil {
		f.t.Fatalf("bad call %q: %v", fr.payload, err)
	}
	return m
}

func (f *fakeCore) expect(ch uint32, kind byte) []byte {
	f.t.Helper()
	fr := f.next()
	if fr.channel != ch || fr.kind != kind {
		f.t.Fatalf("expected channel %d kind %d, got channel %d kind %d (%q)", ch, kind, fr.channel, fr.kind, fr.payload)
	}
	return fr.payload
}

func (f *fakeCore) raw(ch uint32, kind byte, payload []byte) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_, _ = f.conn.Write(encodeFrame(ch, kind, payload))
}

func (f *fakeCore) reply(id uint64, result any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	f.raw(0, kindData, b)
}

func (f *fakeCore) replyErr(id uint64, code int, msg, kind, hint string) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{
		"code": code, "message": msg, "data": map[string]any{"kind": kind, "hint": hint}}})
	f.raw(0, kindData, b)
}

// event builds one newline-terminated event notification.
func eventLine(seq uint64, typ, term string, data any) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "event", "params": map[string]any{
		"seq": seq, "type": typ, "terminal": term, "data": data}})
	return append(b, '\n')
}

// leakCheck fails the test if goroutines outlive it.
func leakCheck(t *testing.T) {
	t.Helper()
	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if n := runtime.NumGoroutine(); n > before {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			t.Errorf("goroutine leak: %d -> %d\n%s", before, n, buf)
		}
	})
}
