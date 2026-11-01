package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// A session is a connection to the control socket that began with hello:
// it stays open, and both sides speak JSON-RPC 2.0, one message a line.
// The client calls the window (do, ls, status, subscribe); the window
// sends it events and, when it is a mod's service, calls it (command.run).
// See examples/mods/README.md.

// apiVersion is the version of the protocol this window speaks.
const apiVersion = 1

// eventBuffer is how many events a session holds for a slow client.
const eventBuffer = 256

// JSON-RPC error codes.
const (
	rpcInvalidRequest = -32600
	rpcNoMethod       = -32601
	rpcBadParams      = -32602
	rpcFailed         = 1 // a command that failed
)

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

type rpcSession struct {
	a    *App
	conn net.Conn
	wmu  sync.Mutex
	w    *bufio.Writer
	done chan struct{} // closed when the connection ends
	once sync.Once

	mod  string          // the mod whose service this is, after hello
	subs map[string]bool // events subscribed to (main thread)

	events chan []byte
	lost   atomic.Int64

	callMu sync.Mutex
	calls  map[string]chan rpcMsg
	nextID atomic.Uint64
}

// isRPC reports whether a first line opens a session.
func isRPC(line []byte) bool {
	var probe struct {
		JSONRPC string `json:"jsonrpc"`
	}
	return json.Unmarshal(line, &probe) == nil && probe.JSONRPC != ""
}

// runSession serves a session whose first line has been read.
func (a *App) runSession(conn net.Conn, r *bufio.Reader, first []byte) {
	s := &rpcSession{a: a, conn: conn, w: bufio.NewWriter(conn), done: make(chan struct{}),
		subs: map[string]bool{}, events: make(chan []byte, eventBuffer), calls: map[string]chan rpcMsg{}}
	defer s.close()
	conn.SetDeadline(time.Time{})

	var hello rpcMsg
	if err := json.Unmarshal(first, &hello); err != nil || hello.Method != "hello" || len(hello.ID) == 0 {
		s.respond(hello.ID, nil, &rpcError{rpcInvalidRequest, "a session begins with hello"})
		return
	}
	result, rerr := s.hello(hello.Params)
	s.respond(hello.ID, result, rerr)
	if rerr != nil {
		return
	}
	go s.writeEvents()
	for {
		line, err := readLine(r)
		if len(line) > maxRequest {
			s.respond(nil, nil, &rpcError{rpcInvalidRequest, "message too large"})
			return
		}
		if len(line) > 0 {
			s.receive(line)
		}
		if err != nil {
			return
		}
	}
}

func (s *rpcSession) hello(params json.RawMessage) (any, *rpcError) {
	var p struct {
		API   int    `json:"api"`
		Mod   string `json:"mod"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{rpcBadParams, "hello: " + err.Error()}
	}
	if p.API != apiVersion {
		return nil, &rpcError{rpcBadParams, fmt.Sprintf("api %d is not supported; this app speaks %d", p.API, apiVersion)}
	}
	a := s.a
	r := a.onMain(func() any {
		if p.Mod != "" {
			if err := a.attachService(s, p.Mod, p.Token); err != nil {
				return &rpcError{rpcBadParams, err.Error()}
			}
		}
		a.sessions[s] = true
		return map[string]any{"api": apiVersion, "app": "shiori", "session": a.session, "home": a.home}
	})
	if e, ok := r.(*rpcError); ok {
		return nil, e
	}
	if m, ok := r.(map[string]string); ok && m["error"] != "" {
		return nil, &rpcError{rpcFailed, m["error"]}
	}
	return r, nil
}

func (s *rpcSession) close() {
	s.once.Do(func() {
		close(s.done)
		s.conn.Close()
		s.callMu.Lock()
		for id, ch := range s.calls {
			ch <- rpcMsg{Error: &rpcError{rpcFailed, "the connection closed"}}
			delete(s.calls, id)
		}
		s.callMu.Unlock()
		s.a.post(func() {
			delete(s.a.sessions, s)
			s.a.detachService(s)
		})
	})
}

// receive handles a message from the client.
func (s *rpcSession) receive(line []byte) {
	var m rpcMsg
	if err := json.Unmarshal(line, &m); err != nil {
		s.respond(nil, nil, &rpcError{-32700, "parse error: " + err.Error()})
		return
	}
	switch {
	case m.Method != "" && len(m.ID) > 0:
		go s.request(m)
	case m.Method == "" && len(m.ID) > 0:
		// The reply to a call of the window's.
		s.callMu.Lock()
		ch := s.calls[string(m.ID)]
		delete(s.calls, string(m.ID))
		s.callMu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
	// Notifications from the client mean nothing yet.
}

// request answers a call of the client's, off the main thread.
func (s *rpcSession) request(m rpcMsg) {
	a := s.a
	switch m.Method {
	case "do":
		var p struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil || p.Command == "" {
			s.respond(m.ID, nil, &rpcError{rpcBadParams, "do takes {command, args}"})
			return
		}
		type out struct {
			v   any
			err error
		}
		r := a.onMain(func() any {
			v, err := a.do(p.Command, p.Args)
			return out{v, err}
		})
		o, ok := r.(out)
		if !ok { // the window did not answer
			s.respond(m.ID, nil, &rpcError{rpcFailed, "the window did not answer"})
			return
		}
		v, err := o.v, o.err
		if d, ok := v.(*deferred); ok && err == nil {
			v, err = a.await(d, s.done)
		}
		if err != nil {
			s.respond(m.ID, nil, &rpcError{rpcFailed, err.Error()})
			return
		}
		s.respond(m.ID, replyOf(v, nil), nil)
	case "ls":
		s.respond(m.ID, a.onMain(a.ls), nil)
	case "status":
		s.respond(m.ID, a.onMain(a.statusReport), nil)
	case "subscribe", "unsubscribe":
		var p struct {
			Events []string `json:"events"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			s.respond(m.ID, nil, &rpcError{rpcBadParams, m.Method + " takes {events}"})
			return
		}
		for _, e := range p.Events {
			if !knownEvent(e) {
				s.respond(m.ID, nil, &rpcError{rpcBadParams, fmt.Sprintf("no event %q; events: %v", e, eventNames)})
				return
			}
		}
		on := m.Method == "subscribe"
		s.respond(m.ID, a.onMain(func() any { return map[string]any{"events": s.subscribe(p.Events, on)} }), nil)
	default:
		s.respond(m.ID, nil, &rpcError{rpcNoMethod, "no method " + strconv.Quote(m.Method)})
	}
}

// subscribe adds or removes events, and returns those subscribed (main
// thread).
func (s *rpcSession) subscribe(names []string, on bool) []string {
	for _, n := range names {
		if on {
			s.subs[n] = true
		} else {
			delete(s.subs, n)
		}
	}
	out := []string{}
	for _, n := range eventNames {
		if s.subs[n] {
			out = append(out, n)
		}
	}
	return out
}

func (s *rpcSession) respond(id json.RawMessage, result any, rerr *rpcError) {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if id == nil {
		m["id"] = nil
	}
	if rerr != nil {
		m["error"] = rerr
	} else {
		m["result"] = result
	}
	s.send(m)
}

// send writes a message; one that cannot be written ends the session.
func (s *rpcSession) send(m any) bool {
	b, err := json.Marshal(m)
	if err != nil {
		return false
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := s.w.Write(append(b, '\n')); err == nil {
		err = s.w.Flush()
	}
	if err != nil {
		go s.close()
		return false
	}
	return true
}

// notify sends a notification of the window's.
func (s *rpcSession) notify(method string, params any) bool {
	return s.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// call calls the client and waits for its reply, at most limit.
func (s *rpcSession) call(method string, params any, limit time.Duration) (json.RawMessage, error) {
	id := "w" + strconv.FormatUint(s.nextID.Add(1), 10)
	key := strconv.Quote(id)
	ch := make(chan rpcMsg, 1)
	s.callMu.Lock()
	s.calls[key] = ch
	s.callMu.Unlock()
	defer func() {
		s.callMu.Lock()
		delete(s.calls, key)
		s.callMu.Unlock()
	}()
	if !s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}) {
		return nil, errors.New("the connection closed")
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-time.After(limit):
		return nil, fmt.Errorf("no answer within %s", limit)
	case <-s.done:
		return nil, errors.New("the connection closed")
	}
}

// push queues an event for the client; when the client is behind, the
// oldest gives way, and the client hears how many it lost.
func (s *rpcSession) push(ev []byte) {
	for {
		select {
		case s.events <- ev:
			return
		default:
		}
		select {
		case <-s.events:
			s.lost.Add(1)
		default:
		}
	}
}

func (s *rpcSession) writeEvents() {
	for {
		select {
		case <-s.done:
			return
		case ev := <-s.events:
			if n := s.lost.Swap(0); n > 0 {
				if !s.notify("event", map[string]any{"name": "events.lost", "data": map[string]int64{"count": n}}) {
					return
				}
			}
			s.wmu.Lock()
			s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_, err := s.w.Write(ev)
			if err == nil {
				err = s.w.Flush()
			}
			s.wmu.Unlock()
			if err != nil {
				go s.close()
				return
			}
		}
	}
}
