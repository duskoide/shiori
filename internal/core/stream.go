package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const (
	lazyAttachDelay = 300 * time.Millisecond
	defaultCols     = 80
	defaultRows     = 24
	attachHistory   = 2000
	writeChunk      = 32 << 10
)

// Stream is a lazily attached terminal byte stream: reading yields terminal
// output, writing sends input. It implements io.ReadWriteCloser and is safe
// for concurrent use.
type Stream struct {
	c    *Client
	addr string

	mu   sync.Mutex
	cond *sync.Cond

	display Display // desired display (colors and cell size)

	cols, rows int // desired size (0 = not set yet)
	focus      bool

	started  bool // attach requested
	attached bool
	channel  uint32
	timer    *time.Timer

	sentCols, sentRows int
	sentCell           [2]int
	sentFocus          bool

	queue   [][]byte
	cur     []byte
	curSize int

	coreClosed bool // core sent CLOSE
	reason     *CloseReason
	lostErr    error
	attachErr  error
	userClosed bool
}

var _ io.ReadWriteCloser = (*Stream)(nil)

// Stream returns a stream for a terminal. It attaches on the first
// Resize/ResizePixels, or after 300ms at 80x24. d may be nil.
func (c *Client) Stream(address string, d *Display) *Stream {
	s := &Stream{c: c, addr: address}
	s.cond = sync.NewCond(&s.mu)
	if d != nil {
		s.display = *d
	}
	s.timer = time.AfterFunc(lazyAttachDelay, func() {
		s.mu.Lock()
		s.startLocked()
		s.mu.Unlock()
	})
	return s
}

// startLocked begins attaching (once) with the desired size.
func (s *Stream) startLocked() {
	if s.started || s.userClosed || s.lostErr != nil {
		return
	}
	s.started = true
	s.timer.Stop()
	if s.cols == 0 || s.rows == 0 {
		s.cols, s.rows = defaultCols, defaultRows
	}
	go s.attach(s.cols, s.rows, s.display)
}

func (s *Stream) attach(cols, rows int, d Display) {
	params := map[string]any{"terminal": s.addr, "cols": cols, "rows": rows, "history": attachHistory}
	if d != (Display{}) {
		params["display"] = d
	}
	var res struct {
		Channel *uint32 `json:"channel"`
	}
	err := s.c.Call(context.Background(), "terminal.attach", params, &res)
	if err == nil && res.Channel == nil {
		err = errors.New("herdr-core: terminal.attach returned no channel (framed mode required)")
	}
	s.mu.Lock()
	if err != nil {
		s.attachErr = err
		s.cond.Broadcast()
		s.mu.Unlock()
		return
	}
	ch := *res.Channel
	s.channel = ch
	s.sentCols, s.sentRows, s.sentCell = cols, rows, d.Cell
	closed := s.userClosed
	s.mu.Unlock()

	if closed {
		s.c.closeChannel(ch)
		return
	}
	s.c.register(ch, s) // replays early frames into deliver

	s.mu.Lock()
	if s.userClosed {
		s.mu.Unlock()
		s.c.closeChannel(ch)
		return
	}
	s.attached = true
	s.syncLocked()
	s.cond.Broadcast()
	s.mu.Unlock()
}

// syncLocked sends whatever of size, facts and focus the core does not know.
func (s *Stream) syncLocked() {
	if !s.attached || s.coreClosed || s.userClosed {
		return
	}
	if s.cols > 0 && s.rows > 0 && (s.cols != s.sentCols || s.rows != s.sentRows) {
		s.sentCols, s.sentRows = s.cols, s.rows
		_ = s.c.send(s.channel, kindResize, resizePayload(s.cols, s.rows))
	}
	if s.display.Cell != s.sentCell && s.display.Cell[0] > 0 && s.display.Cell[1] > 0 {
		s.sentCell = s.display.Cell
		if b, err := json.Marshal(s.display); err == nil {
			_ = s.c.send(s.channel, kindFacts, b)
		}
	}
	if s.focus != s.sentFocus {
		s.sentFocus = s.focus
		b := []byte{0}
		if s.focus {
			b[0] = 1
		}
		_ = s.c.send(s.channel, kindFocus, b)
	}
}

func (s *Stream) usableLocked() error {
	switch {
	case s.userClosed:
		return net.ErrClosed
	case s.lostErr != nil:
		return s.lostErr
	case s.coreClosed:
		return io.ErrClosedPipe
	}
	return nil
}

// Resize sets the terminal size in cells. The first call triggers the attach.
func (s *Stream) Resize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return errors.New("herdr-core: invalid size")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return err
	}
	s.cols, s.rows = cols, rows
	s.startLocked()
	s.syncLocked()
	return nil
}

// ResizePixels is Resize plus the cell size in pixels, which is also sent to
// the core (FACTS) when it changes.
func (s *Stream) ResizePixels(cols, rows, cellW, cellH int) error {
	if cols < 1 || rows < 1 {
		return errors.New("herdr-core: invalid size")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return err
	}
	s.cols, s.rows = cols, rows
	if cellW > 0 && cellH > 0 {
		s.display.Cell = [2]int{cellW, cellH}
	}
	s.startLocked()
	s.syncLocked()
	return nil
}

// Focus tells the core whether this viewer has focus.
func (s *Stream) Focus(focused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.focus = focused
	s.syncLocked()
}

// Read returns terminal output, blocking until some is available. After the
// core closes the stream (see CloseReason) it returns io.EOF; when the
// connection dropped, ErrLost.
func (s *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	for {
		if s.userClosed {
			s.mu.Unlock()
			return 0, net.ErrClosed
		}
		if len(s.cur) == 0 && len(s.queue) > 0 {
			s.cur, s.queue = s.queue[0], s.queue[1:]
			s.curSize = len(s.cur)
		}
		if len(s.cur) > 0 {
			n := copy(p, s.cur)
			s.cur = s.cur[n:]
			credit, ch := 0, s.channel
			if len(s.cur) == 0 {
				s.cur, credit = nil, s.curSize
			}
			s.mu.Unlock()
			if credit > 0 {
				_ = s.c.send(ch, kindCredit, creditPayload(credit))
			}
			return n, nil
		}
		switch {
		case s.attachErr != nil:
			err := s.attachErr
			s.mu.Unlock()
			return 0, err
		case s.coreClosed:
			s.mu.Unlock()
			return 0, io.EOF
		case s.lostErr != nil:
			err := s.lostErr
			s.mu.Unlock()
			return 0, err
		}
		s.cond.Wait()
	}
}

// Write sends input bytes to the terminal, waiting for the attach first.
func (s *Stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for !s.attached {
		if err := s.usableLocked(); err != nil {
			return 0, err
		}
		if s.attachErr != nil {
			return 0, s.attachErr
		}
		s.cond.Wait()
	}
	if err := s.usableLocked(); err != nil {
		return 0, err
	}
	for off := 0; off < len(p); off += writeChunk {
		end := min(off+writeChunk, len(p))
		if err := s.c.send(s.channel, kindData, p[off:end]); err != nil {
			return off, err
		}
	}
	return len(p), nil
}

// CloseReason returns why the core ended the stream, once Read returned io.EOF.
func (s *Stream) CloseReason() *CloseReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reason == nil {
		return nil
	}
	r := *s.reason
	return &r
}

// Close detaches (the terminal keeps running) and unblocks pending Reads.
func (s *Stream) Close() error {
	s.mu.Lock()
	if s.userClosed {
		s.mu.Unlock()
		return nil
	}
	s.userClosed = true
	s.timer.Stop()
	detach := s.attached && !s.coreClosed && s.lostErr == nil
	ch := s.channel
	s.queue, s.cur = nil, nil
	s.cond.Broadcast()
	s.mu.Unlock()
	if detach {
		s.c.closeChannel(ch)
	}
	return nil
}

// deliver implements sink.
func (s *Stream) deliver(f frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch f.kind {
	case kindData:
		if len(f.payload) > 0 && !s.userClosed {
			s.queue = append(s.queue, f.payload)
		}
	case kindClose:
		if !s.userClosed && !s.coreClosed {
			var r CloseReason
			if len(f.payload) > 0 && json.Unmarshal(f.payload, &r) == nil {
				s.reason = &r
			}
		}
		s.coreClosed = true
	}
	s.cond.Broadcast()
}

// lost implements sink.
func (s *Stream) lost(err error) {
	s.mu.Lock()
	if s.lostErr == nil {
		s.lostErr = err
	}
	s.timer.Stop()
	s.cond.Broadcast()
	s.mu.Unlock()
}
