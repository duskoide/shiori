package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
	"time"
)

type attachReq struct {
	Terminal string   `json:"terminal"`
	Cols     int      `json:"cols"`
	Rows     int      `json:"rows"`
	History  int      `json:"history"`
	Display  *Display `json:"display"`
}

// answerAttach consumes the terminal.attach call and replies with channel ch.
func answerAttach(t *testing.T, f *fakeCore, ch uint32) attachReq {
	t.Helper()
	cl := f.nextCall()
	if cl.Method != "terminal.attach" {
		t.Fatalf("method %s", cl.Method)
	}
	var req attachReq
	if err := json.Unmarshal(cl.Params, &req); err != nil {
		t.Fatal(err)
	}
	f.reply(cl.ID, map[string]any{"channel": ch, "terminal": map[string]any{"address": req.Terminal}})
	return req
}

// attachedStream returns a stream that has completed its attach on channel 7.
func attachedStream(t *testing.T, d *Display) (*Client, *fakeCore, *Stream) {
	t.Helper()
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t1", d)
	if err := s.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	answerAttach(t, f, 7)
	// Write blocks until the attach completed; it also proves ordering.
	if _, err := s.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if got := f.expect(7, kindData); string(got) != "x" {
		t.Fatalf("%q", got)
	}
	return c, f, s
}

func TestStreamLazyAttachOnResize(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t1", &Display{FG: "#fff", BG: "#000"})
	defer s.Close()
	f.expectNone() // nothing happens before the first Resize
	if err := s.ResizePixels(100, 30, 9, 18); err != nil {
		t.Fatal(err)
	}
	req := answerAttach(t, f, 7)
	want := attachReq{Terminal: "t1", Cols: 100, Rows: 30, History: attachHistory, Display: &Display{FG: "#fff", BG: "#000", Cell: [2]int{9, 18}}}
	if !reflect.DeepEqual(req, want) {
		t.Fatalf("got %+v want %+v", req, want)
	}
	// the display passed by the caller is not modified
	f.raw(7, kindData, []byte("hi"))
	buf := make([]byte, 10)
	if n, err := s.Read(buf); err != nil || string(buf[:n]) != "hi" {
		t.Fatal(n, err)
	}
	f.expect(7, kindCredit)
	f.expectNone() // no FACTS: the cell size was part of the attach
}

func TestStreamLazyAttachTimer(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t2", nil)
	defer s.Close()
	start := time.Now()
	errc := make(chan error, 1)
	go func() { _, err := s.Write([]byte("early input")); errc <- err }() // waits for the attach
	cl := f.nextCall()
	if d := time.Since(start); d < lazyAttachDelay-50*time.Millisecond {
		t.Fatalf("attached after %v", d)
	}
	var req attachReq
	_ = json.Unmarshal(cl.Params, &req)
	if req.Cols != 80 || req.Rows != 24 || req.Display != nil || req.Terminal != "t2" {
		t.Fatalf("%+v", req)
	}
	select {
	case err := <-errc:
		t.Fatalf("Write returned before the attach: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	f.reply(cl.ID, map[string]any{"channel": 2})
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if got := f.expect(2, kindData); string(got) != "early input" {
		t.Fatalf("%q", got)
	}
}

func TestStreamTimerCancelledByResize(t *testing.T) {
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t1", nil)
	defer s.Close()
	_ = s.Resize(50, 20)
	answerAttach(t, f, 3)
	f.expectNone()
	time.Sleep(lazyAttachDelay + 100*time.Millisecond)
	f.expectNone() // the timer must not attach a second time
}

func TestStreamReadCreditAndPartialReads(t *testing.T) {
	leakCheck(t)
	_, f, s := attachedStream(t, nil)
	defer s.Close()
	f.raw(7, kindData, []byte("hello world"))
	f.raw(7, kindData, []byte("!"))
	f.raw(7, kindData, nil) // empty payloads are ignored

	p := make([]byte, 3)
	var got []byte
	for i := 0; i < 3; i++ {
		n, err := s.Read(p)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p[:n]...)
	}
	if string(got) != "hello wor" {
		t.Fatalf("%q", got)
	}
	// credit is only sent once a payload is fully consumed
	f.expectNone()
	big := make([]byte, 64)
	n, _ := s.Read(big)
	if string(big[:n]) != "ld" {
		t.Fatalf("%q", big[:n])
	}
	if got := f.expect(7, kindCredit); !reflect.DeepEqual(got, creditPayload(11)) {
		t.Fatalf("credit %v", got)
	}
	n, _ = s.Read(big)
	if string(big[:n]) != "!" {
		t.Fatalf("%q", big[:n])
	}
	if got := f.expect(7, kindCredit); !reflect.DeepEqual(got, creditPayload(1)) {
		t.Fatalf("credit %v", got)
	}
	if n, err := s.Read(nil); n != 0 || err != nil {
		t.Fatal(n, err)
	}
}

func TestStreamReadBlocksUntilData(t *testing.T) {
	leakCheck(t)
	_, f, s := attachedStream(t, nil)
	defer s.Close()
	type rr struct {
		s   string
		err error
	}
	rc := make(chan rr, 1)
	go func() {
		b := make([]byte, 16)
		n, err := s.Read(b)
		rc <- rr{string(b[:n]), err}
	}()
	select {
	case <-rc:
		t.Fatal("Read returned without data")
	case <-time.After(50 * time.Millisecond):
	}
	f.raw(7, kindData, []byte("ping"))
	if r := <-rc; r.err != nil || r.s != "ping" {
		t.Fatal(r)
	}
}

func TestStreamResizeDedupeAndFacts(t *testing.T) {
	leakCheck(t)
	_, f, s := attachedStream(t, &Display{FG: "#111111", BG: "#222222", Cursor: "#333333"})
	defer s.Close()

	_ = s.Resize(100, 30) // unchanged since attach
	f.expectNone()
	_ = s.Resize(120, 40)
	if got := f.expect(7, kindResize); !reflect.DeepEqual(got, resizePayload(120, 40)) {
		t.Fatalf("%v", got)
	}
	_ = s.Resize(120, 40)
	f.expectNone()

	// pixel resize: RESIZE only if the grid changed, FACTS when the cell changed
	_ = s.ResizePixels(120, 40, 9, 18)
	var d Display
	if err := json.Unmarshal(f.expect(7, kindFacts), &d); err != nil {
		t.Fatal(err)
	}
	if d != (Display{FG: "#111111", BG: "#222222", Cursor: "#333333", Cell: [2]int{9, 18}}) {
		t.Fatalf("facts %+v", d)
	}
	f.expectNone()
	_ = s.ResizePixels(120, 40, 9, 18)
	f.expectNone()
	_ = s.ResizePixels(90, 20, 10, 20)
	if got := f.expect(7, kindResize); !reflect.DeepEqual(got, resizePayload(90, 20)) {
		t.Fatalf("%v", got)
	}
	if err := json.Unmarshal(f.expect(7, kindFacts), &d); err != nil || d.Cell != [2]int{10, 20} {
		t.Fatal(d, err)
	}
	_ = s.ResizePixels(90, 20, 0, 0) // unknown cell size does not resend
	f.expectNone()

	if s.Resize(0, 5) == nil || s.ResizePixels(5, 0, 1, 1) == nil {
		t.Fatal("invalid sizes accepted")
	}
}

func TestStreamResizeDuringAttach(t *testing.T) {
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t1", nil)
	defer s.Close()
	_ = s.Resize(80, 24)
	cl := f.nextCall() // attach in flight
	_ = s.ResizePixels(132, 50, 8, 16)
	s.Focus(true)
	f.expectNone()
	f.reply(cl.ID, map[string]any{"channel": 9})
	if got := f.expect(9, kindResize); !reflect.DeepEqual(got, resizePayload(132, 50)) {
		t.Fatalf("%v", got)
	}
	var d Display
	if err := json.Unmarshal(f.expect(9, kindFacts), &d); err != nil || d.Cell != [2]int{8, 16} {
		t.Fatal(d, err)
	}
	if got := f.expect(9, kindFocus); !reflect.DeepEqual(got, []byte{1}) {
		t.Fatalf("%v", got)
	}
}

func TestStreamFocus(t *testing.T) {
	_, f, s := attachedStream(t, nil)
	defer s.Close()
	s.Focus(true)
	if got := f.expect(7, kindFocus); !reflect.DeepEqual(got, []byte{1}) {
		t.Fatal(got)
	}
	s.Focus(true)
	f.expectNone()
	s.Focus(false)
	if got := f.expect(7, kindFocus); !reflect.DeepEqual(got, []byte{0}) {
		t.Fatal(got)
	}
}

func TestStreamWriteChunksAndEmpty(t *testing.T) {
	_, f, s := attachedStream(t, nil)
	defer s.Close()
	if n, err := s.Write(nil); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	f.expectNone()
	data := make([]byte, writeChunk*2+10)
	for i := range data {
		data[i] = byte(i)
	}
	if n, err := s.Write(data); n != len(data) || err != nil {
		t.Fatal(n, err)
	}
	var got []byte
	for len(got) < len(data) {
		got = append(got, f.expect(7, kindData)...)
	}
	if !reflect.DeepEqual(got, data) {
		t.Fatal("data mismatch")
	}
}

func TestStreamCoreCloseEOF(t *testing.T) {
	leakCheck(t)
	_, f, s := attachedStream(t, nil)
	defer s.Close()
	f.raw(7, kindData, []byte("bye"))
	f.raw(7, kindClose, []byte(`{"reason":"exited","exit_code":3,"signal":null}`))
	b := make([]byte, 16)
	n, err := s.Read(b)
	if err != nil || string(b[:n]) != "bye" {
		t.Fatal(n, err)
	}
	f.expect(7, kindCredit)
	if _, err := s.Read(b); err != io.EOF {
		t.Fatalf("got %v", err)
	}
	if _, err := s.Read(b); err != io.EOF {
		t.Fatalf("second: %v", err)
	}
	r := s.CloseReason()
	if r == nil || r.Reason != "exited" || r.ExitCode == nil || *r.ExitCode != 3 || r.Signal != nil {
		t.Fatalf("%+v", r)
	}
	if r.String() == "" {
		t.Fatal("String")
	}
	if _, err := s.Write([]byte("x")); err == nil {
		t.Fatal("write after CLOSE succeeded")
	}
	if s.Resize(10, 10) == nil {
		t.Fatal("resize after CLOSE succeeded")
	}
	s.Close()
	f.expectNone() // the core already ended the stream: no CLOSE from us
}

func TestStreamCloseReasonNilBeforeEnd(t *testing.T) {
	_, _, s := attachedStream(t, nil)
	defer s.Close()
	if s.CloseReason() != nil {
		t.Fatal("reason before end")
	}
}

func TestStreamCloseDetaches(t *testing.T) {
	leakCheck(t)
	c, f, s := attachedStream(t, nil)
	errc := make(chan error, 1)
	go func() { _, err := s.Read(make([]byte, 8)); errc <- err }()
	time.Sleep(30 * time.Millisecond)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("got %v", err)
	}
	if payload := f.expect(7, kindClose); len(payload) != 0 {
		t.Fatalf("%q", payload)
	}
	s.Close() // idempotent: no second CLOSE
	f.expectNone()
	if _, err := s.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if err := s.Resize(5, 5); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	// late output and the core's CLOSE ack are swallowed
	f.raw(7, kindData, []byte("late"))
	f.raw(7, kindClose, nil)
	f.expectNone()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.smu.Lock()
		_, has := c.sinks[7]
		_, early := c.early[7]
		c.smu.Unlock()
		if !has && !early {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("channel state not cleaned up (sink=%v early=%v)", has, early)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStreamCloseBeforeAttach(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t1", nil)
	s.Close()
	time.Sleep(lazyAttachDelay + 80*time.Millisecond)
	f.expectNone() // the lazy timer was cancelled
	if _, err := s.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}

func TestStreamCloseDuringAttach(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t1", nil)
	_ = s.Resize(80, 24)
	cl := f.nextCall()
	s.Close()
	f.reply(cl.ID, map[string]any{"channel": 4})
	f.expect(4, kindClose) // the late attach is detached again
}

func TestStreamAttachError(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t1", nil)
	defer s.Close()
	_ = s.Resize(80, 24)
	cl := f.nextCall()
	f.replyErr(cl.ID, -32000, "terminal is not running", "unavailable", "retry later")
	if _, err := s.Read(make([]byte, 4)); !IsKind(err, "unavailable") {
		t.Fatalf("got %v", err)
	}
	if _, err := s.Write([]byte("x")); !IsKind(err, "unavailable") {
		t.Fatalf("got %v", err)
	}
}

func TestStreamConnectionLost(t *testing.T) {
	leakCheck(t)
	_, f, s := attachedStream(t, nil)
	defer s.Close()
	f.raw(7, kindData, []byte("last"))
	time.Sleep(30 * time.Millisecond)
	errc := make(chan error, 1)
	go func() {
		b := make([]byte, 16)
		for {
			if _, err := s.Read(b); err != nil {
				errc <- err
				return
			}
		}
	}()
	f.conn.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrLost) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read not released")
	}
	if _, err := s.Write([]byte("x")); !errors.Is(err, ErrLost) {
		t.Fatalf("write: %v", err)
	}
}

func TestStreamLostBeforeAttach(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t1", nil)
	defer s.Close()
	f.conn.Close()
	<-c.Done()
	if _, err := s.Read(make([]byte, 4)); !errors.Is(err, ErrLost) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.Write([]byte("x")); !errors.Is(err, ErrLost) {
		t.Fatalf("write: %v", err)
	}
	// a stream created on a dead client fails the same way
	s2 := c.Stream("t1", nil)
	defer s2.Close()
	_ = s2.Resize(80, 24)
	if _, err := s2.Read(make([]byte, 4)); !errors.Is(err, ErrLost) && !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
}

func TestStreamEarlyDataBeforeRegistration(t *testing.T) {
	leakCheck(t)
	c, f := newPair(t, fakeOpts{})
	s := c.Stream("t1", nil)
	defer s.Close()
	_ = s.Resize(80, 24)
	cl := f.nextCall()
	f.reply(cl.ID, map[string]any{"channel": 5})
	f.raw(5, kindData, []byte("first paint"))
	b := make([]byte, 32)
	n, err := s.Read(b)
	if err != nil || string(b[:n]) != "first paint" {
		t.Fatal(n, err)
	}
	_ = c
}

func TestStreamConcurrentUse(t *testing.T) {
	leakCheck(t)
	_, f, s := attachedStream(t, nil)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			f.raw(7, kindData, []byte("0123456789"))
		}
	}()
	go func() {
		defer close(done)
		got := 0
		b := make([]byte, 7)
		for got < 2000 {
			n, err := s.Read(b)
			if err != nil {
				return
			}
			got += n
		}
	}()
	for i := 0; i < 50; i++ {
		go s.Write([]byte("k"))
		go s.Resize(80+i%3, 24)
		go s.Focus(i%2 == 0)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reader stalled")
	}
	s.Close()
	// drain everything the fake received so its goroutine can finish
	for {
		select {
		case <-f.in:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}

func TestStreamViaClientAfterClientClose(t *testing.T) {
	leakCheck(t)
	c, _, s := attachedStream(t, nil)
	c.Close()
	if _, err := s.Read(make([]byte, 4)); !errors.Is(err, ErrLost) {
		t.Fatalf("got %v", err)
	}
	s.Close()
	_ = context.Background()
}
