package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
)

const (
	initialEventCredit = 1 << 20
	maxEventLine       = 64 << 20
	seenLabel          = "herdr.seen"
)

// Events is a live, event-maintained view of the core's terminals.
type Events struct {
	c       *Client
	channel uint32

	mu      sync.Mutex
	terms   []Terminal
	seq     uint64
	buf     []byte // incomplete trailing line
	pending Change
	has     bool
	ended   bool

	sig      chan struct{}
	out      chan Change
	stop     chan struct{}
	stopOnce sync.Once
}

// Subscribe subscribes to terminal events. It returns once the initial
// snapshot is in; Changes then reports updates until the stream ends.
func (c *Client) Subscribe(ctx context.Context) (*Events, error) {
	var res struct {
		Seq       uint64     `json:"seq"`
		Channel   *uint32    `json:"channel"`
		Terminals []Terminal `json:"terminals"`
	}
	if err := c.Call(ctx, "events.subscribe", nil, &res); err != nil {
		return nil, err
	}
	if res.Channel == nil {
		return nil, errors.New("herdr-core: events.subscribe returned no channel (framed mode required)")
	}
	e := &Events{
		c:       c,
		channel: *res.Channel,
		terms:   res.Terminals,
		seq:     res.Seq,
		sig:     make(chan struct{}, 1),
		out:     make(chan Change, 1),
		stop:    make(chan struct{}),
	}
	go e.pump()
	if err := c.send(e.channel, kindCredit, creditPayload(initialEventCredit)); err != nil {
		e.Close()
		return nil, err
	}
	c.register(e.channel, e)
	return e, nil
}

// Terminals returns a copy of the snapshot (live and finished, core order).
func (e *Events) Terminals() []Terminal {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Terminal, len(e.terms))
	for i, t := range e.terms {
		out[i] = cloneTerminal(t)
	}
	return out
}

// Live returns a copy of the live terminals.
func (e *Events) Live() []Terminal {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Terminal, 0, len(e.terms))
	for _, t := range e.terms {
		if t.Live() {
			out = append(out, cloneTerminal(t))
		}
	}
	return out
}

// Terminal returns a copy of one terminal from the snapshot.
func (e *Events) Terminal(address string) (Terminal, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i := e.find(address); i >= 0 {
		return cloneTerminal(e.terms[i]), true
	}
	return Terminal{}, false
}

// Changes returns coalesced change notifications. It is closed when the
// stream ends (CLOSE, connection loss or Close): resubscribe then.
func (e *Events) Changes() <-chan Change { return e.out }

// Close unsubscribes. The Changes channel is closed.
func (e *Events) Close() error {
	e.stopOnce.Do(func() {
		close(e.stop)
		e.mu.Lock()
		ended := e.ended
		e.ended = true
		e.mu.Unlock()
		if !ended && e.channel != 0 {
			e.c.closeChannel(e.channel)
		}
	})
	return nil
}

// pump forwards the pending change to the consumer; the reader never waits
// for the consumer.
func (e *Events) pump() {
	defer close(e.out)
	for {
		select {
		case <-e.sig:
		case <-e.stop:
			return
		}
		e.mu.Lock()
		ch, has, ended := e.pending, e.has, e.ended
		e.pending, e.has = Change{}, false
		e.mu.Unlock()
		if has {
			select {
			case e.out <- ch:
			case <-e.stop:
				return
			case <-e.c.done:
				return
			}
		}
		if ended {
			return
		}
	}
}

// notifyLocked merges a change into the pending one and wakes the pump.
func (e *Events) notifyLocked(ch Change) {
	if ch.empty() {
		return
	}
	e.pending.merge(ch)
	e.has = true
	e.wake()
}

func (e *Events) wake() {
	select {
	case e.sig <- struct{}{}:
	default:
	}
}

func (e *Events) endLocked() {
	e.ended = true
	e.wake()
}

// deliver implements sink.
func (e *Events) deliver(f frame) {
	switch f.kind {
	case kindData:
		e.mu.Lock()
		e.feedLocked(f.payload)
		e.mu.Unlock()
		if len(f.payload) > 0 {
			_ = e.c.send(e.channel, kindCredit, creditPayload(len(f.payload)))
		}
	case kindClose:
		e.mu.Lock()
		e.endLocked()
		e.mu.Unlock()
	}
}

// lost implements sink.
func (e *Events) lost(error) {
	e.mu.Lock()
	e.endLocked()
	e.mu.Unlock()
}

// feedLocked splits newline-delimited JSON across frame boundaries.
func (e *Events) feedLocked(p []byte) {
	e.buf = append(e.buf, p...)
	for {
		i := bytes.IndexByte(e.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(e.buf[:i])
		if len(line) > 0 {
			e.lineLocked(line)
		}
		e.buf = e.buf[i+1:]
	}
	if len(e.buf) == 0 {
		e.buf = nil
	} else if len(e.buf) > maxEventLine {
		e.buf = nil
		e.endLocked()
	} else {
		e.buf = append([]byte(nil), e.buf...)
	}
}

type eventParams struct {
	Seq       uint64          `json:"seq"`
	Type      string          `json:"type"`
	Terminal  string          `json:"terminal"`
	Data      json.RawMessage `json:"data"`
	Terminals []Terminal      `json:"terminals"`
}

func (e *Events) lineLocked(line []byte) {
	var m struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(line, &m) != nil {
		return
	}
	switch m.Method {
	case "event":
		var p eventParams
		if json.Unmarshal(m.Params, &p) != nil || p.Seq <= e.seq {
			return
		}
		e.seq = p.Seq
		e.notifyLocked(e.applyLocked(p))
	case "missed":
		var p eventParams
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		e.seq = p.Seq
		e.terms = p.Terminals
		ch := Change{Docs: true, Terminals: true}
		for _, t := range p.Terminals {
			noteSeen(&ch, t.Address, t.Labels)
		}
		e.notifyLocked(ch)
	}
	// heartbeat and unknown methods need no action.
}

func (e *Events) find(addr string) int {
	for i := range e.terms {
		if e.terms[i].Address == addr {
			return i
		}
	}
	return -1
}

func noteSeen(ch *Change, addr string, labels map[string]string) {
	v, ok := labels[seenLabel]
	if !ok {
		return
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return
	}
	if ch.Seen == nil {
		ch.Seen = map[string]uint64{}
	}
	if n > ch.Seen[addr] {
		ch.Seen[addr] = n
	}
}

func (e *Events) upsertLocked(t Terminal) {
	if i := e.find(t.Address); i >= 0 {
		e.terms[i] = t
	} else {
		e.terms = append(e.terms, t)
	}
}

// applyLocked applies one event to the snapshot.
func (e *Events) applyLocked(p eventParams) Change {
	changed := Change{Terminals: true}
	switch p.Type {
	case "doc":
		return Change{Docs: true}
	case "created", "restarted":
		var t Terminal
		if json.Unmarshal(p.Data, &t) != nil || t.Address == "" {
			return Change{}
		}
		e.upsertLocked(t)
		noteSeen(&changed, t.Address, t.Labels)
		return changed
	case "removed":
		i := e.find(p.Terminal)
		if i < 0 {
			return Change{}
		}
		e.terms = append(e.terms[:i], e.terms[i+1:]...)
		return changed
	case "labels":
		var d struct {
			Labels map[string]string `json:"labels"`
			Record *Terminal         `json:"record"`
		}
		if json.Unmarshal(p.Data, &d) != nil {
			return Change{}
		}
		if d.Record != nil && d.Record.Address != "" {
			e.upsertLocked(*d.Record)
			noteSeen(&changed, d.Record.Address, d.Record.Labels)
		}
		if i := e.find(p.Terminal); i >= 0 && d.Labels != nil {
			e.terms[i].Labels = d.Labels
		}
		noteSeen(&changed, p.Terminal, d.Labels)
		return changed
	}

	i := e.find(p.Terminal)
	if i < 0 {
		return Change{}
	}
	t := &e.terms[i]
	switch p.Type {
	case "lost", "finished":
		var d struct {
			Reason     *string `json:"reason"`
			ExitCode   *int    `json:"exit_code"`
			Signal     *string `json:"signal"`
			FinishedMs *int64  `json:"finished_ms"`
		}
		if json.Unmarshal(p.Data, &d) != nil {
			return Change{}
		}
		t.Reason, t.ExitCode, t.Signal, t.FinishedMs = d.Reason, d.ExitCode, d.Signal, d.FinishedMs
		t.Pid, t.Foreground = nil, nil
		if p.Type == "finished" {
			t.State = "finished"
		}
	case "keep":
		var d struct {
			Keep bool `json:"keep"`
		}
		if json.Unmarshal(p.Data, &d) != nil {
			return Change{}
		}
		t.Keep = d.Keep
	case "status":
		var d struct {
			Status   *Status         `json:"status"`
			Records  json.RawMessage `json:"records"`
			Reported bool            `json:"reported"`
		}
		if json.Unmarshal(p.Data, &d) != nil {
			return Change{}
		}
		t.Status, t.Records, t.Reported = d.Status, d.Records, d.Reported
	case "turn":
		var d struct {
			Turn uint64 `json:"turn"`
		}
		if json.Unmarshal(p.Data, &d) != nil {
			return Change{}
		}
		t.Turn = d.Turn
	case "title":
		var d struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(p.Data, &d) != nil {
			return Change{}
		}
		t.Title = d.Title
	case "size":
		var d struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		}
		if json.Unmarshal(p.Data, &d) != nil {
			return Change{}
		}
		t.Cols, t.Rows = d.Cols, d.Rows
	case "foreground":
		var fg *Foreground
		if json.Unmarshal(p.Data, &fg) != nil {
			return Change{}
		}
		t.Foreground = fg
	default:
		return Change{}
	}
	return changed
}
