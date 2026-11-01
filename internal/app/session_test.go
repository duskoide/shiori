package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
)

// rpcClient is a client of a session, as a mod's would be.
type rpcClient struct {
	t      *testing.T
	conn   net.Conn
	r      *bufio.Reader
	id     int
	events []map[string]any
	calls  []rpcMsg // the window's calls, as they came
}

func dialRPC(t *testing.T, a *App) *rpcClient {
	t.Helper()
	conn, err := net.Dial("unix", socketPath(a.home))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	return &rpcClient{t: t, conn: conn, r: bufio.NewReader(conn)}
}

func (c *rpcClient) write(m any) {
	b, _ := json.Marshal(m)
	c.conn.Write(append(b, '\n'))
}

// read reads the next message.
func (c *rpcClient) read() rpcMsg {
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		panic(fmt.Sprintf("read: %v", err))
	}
	var m rpcMsg
	if err := json.Unmarshal(line, &m); err != nil {
		panic(err)
	}
	return m
}

// call sends a request and returns its reply, keeping the events and the
// window's calls that come before it.
func (c *rpcClient) call(method string, params any) rpcMsg {
	c.id++
	c.write(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	for {
		m := c.read()
		switch {
		case m.Method == "event":
			var p map[string]any
			json.Unmarshal(m.Params, &p)
			c.events = append(c.events, p)
		case m.Method != "":
			c.calls = append(c.calls, m)
		case string(m.ID) == fmt.Sprint(c.id):
			return m
		}
	}
}

// nextEvent reads until an event comes.
func (c *rpcClient) nextEvent() map[string]any {
	if len(c.events) > 0 {
		e := c.events[0]
		c.events = c.events[1:]
		return e
	}
	for {
		m := c.read()
		if m.Method == "event" {
			var p map[string]any
			json.Unmarshal(m.Params, &p)
			return p
		}
	}
}

// async runs fn off the main thread while the test draws frames, which
// run what the session posts, until fn returns.
func async(t *testing.T, tt *ui.Tester, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		fn()
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case r := <-done:
			if r != nil {
				t.Fatal(r)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(2 * time.Millisecond)
		tt.Frame()
	}
}

func sessionApp(t *testing.T) (*App, *ui.Tester) {
	a, _ := uxApp(t)
	serveApp(t, a)
	return a, uxTester(a)
}

func hello(c *rpcClient) rpcMsg { return c.call("hello", map[string]any{"api": 1}) }

func TestSessionHelloAndCalls(t *testing.T) {
	a, tt := sessionApp(t)
	c := dialRPC(t, a)
	var h, ls, renamed, bad, unknown rpcMsg
	async(t, tt, func() {
		h = hello(c)
		ls = c.call("ls", nil)
		renamed = c.call("do", map[string]any{"command": "tab.rename", "args": map[string]any{"name": "build"}})
		bad = c.call("do", map[string]any{"command": "nope.nope"})
		unknown = c.call("dance", nil)
	})
	if h.Error != nil || !strings.Contains(string(h.Result), `"api":1`) {
		t.Fatalf("hello %s %v", h.Result, h.Error)
	}
	if !strings.Contains(string(ls.Result), `"spaces"`) {
		t.Fatalf("ls %s", ls.Result)
	}
	if renamed.Error != nil || a.tab().Name != "build" {
		t.Fatalf("rename %v %q", renamed.Error, a.tab().Name)
	}
	if bad.Error == nil || bad.Error.Code != rpcFailed || !strings.Contains(bad.Error.Message, "unknown command") {
		t.Fatalf("bad %+v", bad.Error)
	}
	if unknown.Error == nil || unknown.Error.Code != rpcNoMethod {
		t.Fatalf("unknown %+v", unknown.Error)
	}
	// The one-line protocol is as it was.
	var b json.RawMessage
	async(t, tt, func() { b, _ = Ls(a.home) })
	if !strings.Contains(string(b), `"spaces"`) {
		t.Fatalf("ls %s", b)
	}
}

func TestSessionRefusals(t *testing.T) {
	a, tt := sessionApp(t)
	for _, tc := range []struct {
		first any
		want  string
	}{
		{map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ls"}, "begins with hello"},
		{map[string]any{"jsonrpc": "2.0", "id": 1, "method": "hello", "params": map[string]any{"api": 2}}, "api 2 is not supported"},
		{map[string]any{"jsonrpc": "2.0", "id": 1, "method": "hello", "params": map[string]any{"api": 1, "mod": "ghost", "token": "x"}}, `no mod "ghost"`},
	} {
		c := dialRPC(t, a)
		var m rpcMsg
		async(t, tt, func() {
			c.write(tc.first)
			m = c.read()
		})
		if m.Error == nil || !strings.Contains(m.Error.Message, tc.want) {
			t.Errorf("%v: %+v", tc.first, m.Error)
		}
	}
	c := dialRPC(t, a)
	var m rpcMsg
	async(t, tt, func() {
		hello(c)
		m = c.call("subscribe", map[string]any{"events": []string{"tab.created", "tab.exploded"}})
	})
	if m.Error == nil || m.Error.Code != rpcBadParams || !strings.Contains(m.Error.Message, "tab.exploded") {
		t.Fatalf("subscribe %+v", m.Error)
	}
}

func TestSessionEvents(t *testing.T) {
	a, tt := sessionApp(t)
	c := dialRPC(t, a)
	var sub rpcMsg
	async(t, tt, func() {
		hello(c)
		sub = c.call("subscribe", map[string]any{"events": []string{"tab.renamed", "focus.changed", "agent.status"}})
	})
	if !strings.Contains(string(sub.Result), `"agent.status","focus.changed"`) && !strings.Contains(string(sub.Result), `"focus.changed","agent.status"`) {
		t.Fatalf("subscribed %s", sub.Result)
	}
	settle(tt) // the first frame after subscribing takes the summary
	a.do("tab.rename", args{"name": "build"})
	a.do("pane.go", args{"direction": "right"})
	// The agent of the logs tab, which is not on screen, starts working.
	a.terminals["a3"] = core.Terminal{Address: "a3", State: "live", Argv: []string{"codex"}, Status: &core.Status{State: "working", App: "codex"}}
	settle(tt)
	var got []map[string]any
	async(t, tt, func() {
		for range 3 {
			got = append(got, c.nextEvent())
		}
	})
	names := []string{}
	for _, e := range got {
		names = append(names, e["name"].(string))
	}
	if strings.Join(names, ",") != "tab.renamed,focus.changed,agent.status" {
		t.Fatalf("events %v", got)
	}
	if d := got[0]["data"].(map[string]any); d["name"] != "build" {
		t.Fatalf("renamed %v", d)
	}
	if d := got[2]["data"].(map[string]any); d["status"] != "working" || d["previous"] != "blocked" || d["app"] != "codex" {
		t.Fatalf("agent %v", d)
	}
	if got[0]["seq"].(float64) >= got[1]["seq"].(float64) {
		t.Fatalf("seq %v", got)
	}
}

func TestDiffEvents(t *testing.T) {
	a, _ := uxApp(t)
	old := a.snapshot()
	s := a.space()
	s.AddTab("new", "a9")
	tab := a.tab()
	tab.RemovePane(tab.Panes()[1].ID)
	a.doc.RemoveSpace(a.doc.Spaces[2].ID)
	a.focused = false
	a.cfg.Theme = "ink"
	a.connected, a.connErr = false, "lost the core"
	var names []string
	for _, e := range diffEvents(old, a.snapshot()) {
		names = append(names, e.name)
	}
	want := "tab.created,pane.created,pane.closed,pane.closed,tab.closed,space.closed,window.focus,settings.changed,connection"
	if strings.Join(names, ",") != want {
		t.Fatalf("events\n %s\nwant\n %s", strings.Join(names, ","), want)
	}
	if len(diffEvents(a.snapshot(), a.snapshot())) != 0 {
		t.Fatal("no change made events")
	}
	_ = layout.Right
}

func TestSlowClientsLoseTheOldestEvents(t *testing.T) {
	a, _ := testApp(t)
	server, client := net.Pipe()
	defer client.Close()
	s := &rpcSession{a: a, conn: server, w: bufio.NewWriter(server), done: make(chan struct{}), events: make(chan []byte, eventBuffer)}
	for i := range eventBuffer + 44 {
		s.push([]byte(fmt.Sprintf(`{"n":%d}`+"\n", i)))
	}
	go s.writeEvents()
	r := bufio.NewReader(client)
	first, _ := r.ReadString('\n')
	second, _ := r.ReadString('\n')
	if !strings.Contains(first, `"events.lost"`) || !strings.Contains(first, `"count":44`) || second != `{"n":44}`+"\n" {
		t.Fatalf("got %q then %q", first, second)
	}
	s.close()
}

func TestSessionCallsTheClient(t *testing.T) {
	a, tt := sessionApp(t)
	c := dialRPC(t, a)
	async(t, tt, func() { hello(c) })
	var s *rpcSession
	for x := range a.sessions {
		s = x
	}
	type res struct {
		v   json.RawMessage
		err error
	}
	out := make(chan res, 1)
	go func() {
		v, err := s.call("command.run", map[string]any{"command": "x.y"}, 5*time.Second)
		out <- res{v, err}
	}()
	m := c.read()
	if m.Method != "command.run" {
		t.Fatalf("call %+v", m)
	}
	c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID), "result": map[string]string{"ok": "yes"}})
	if r := <-out; r.err != nil || string(r.v) != `{"ok":"yes"}` {
		t.Fatalf("%s %v", r.v, r.err)
	}
	// A client that goes away fails what the window waits for.
	go func() {
		_, err := s.call("command.run", nil, 5*time.Second)
		out <- res{nil, err}
	}()
	c.read()
	c.conn.Close()
	if r := <-out; r.err == nil {
		t.Fatal("no error")
	}
	settle(tt)
	if len(a.sessions) != 0 {
		t.Fatal("the session stays")
	}
}

func TestSessionDialogEndsWithTheClient(t *testing.T) {
	a, tt := sessionApp(t)
	c := dialRPC(t, a)
	async(t, tt, func() {
		hello(c)
		c.id++
		c.write(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": "do", "params": map[string]any{"command": "app.ask", "args": map[string]any{"title": "Name?"}}})
	})
	for i := 0; i < 100 && a.prompt == nil; i++ {
		time.Sleep(2 * time.Millisecond)
		tt.Frame()
	}
	if a.prompt == nil {
		t.Fatal("no prompt")
	}
	c.conn.Close()
	for i := 0; i < 500 && a.prompt != nil; i++ {
		time.Sleep(2 * time.Millisecond)
		tt.Frame()
	}
	if a.prompt != nil {
		t.Fatal("the prompt outlived its caller")
	}
}
