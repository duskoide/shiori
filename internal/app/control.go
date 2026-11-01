package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// answerTimeout is how long a request waits for the main thread.
var answerTimeout = 5 * time.Second

// maxRequest is the longest request line the control socket reads.
const maxRequest = 1 << 20

// The control socket lets "shiori ls|status|do" and agents talk to
// the running window: one JSON request line, one JSON reply line.

// request is a line sent to the control socket.
type request struct {
	Ls       bool           `json:"ls,omitempty"`
	Status   bool           `json:"status,omitempty"`
	Commands bool           `json:"commands,omitempty"`
	Do       string         `json:"do,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
	Raise    bool           `json:"raise,omitempty"`
}

func socketPath(home string) string { return filepath.Join(home, "shiori.sock") }

// listen serves the control socket until the app quits.
func (a *App) listen() (net.Listener, error) {
	if err := os.MkdirAll(a.home, 0o700); err != nil {
		return nil, err
	}
	// The socket runs commands: only its owner may reach it.
	if err := os.Chmod(a.home, 0o700); err != nil {
		return nil, err
	}
	path := socketPath(a.home)
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		return nil, errors.New("another shiori window uses " + a.home)
	}
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go a.serve(conn)
		}
	}()
	return l, nil
}

func (a *App) serve(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(conn, maxRequest+1)).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req request
	var reply any
	if len(line) > maxRequest {
		reply = map[string]string{"error": "request too large"}
	} else if err := json.Unmarshal(line, &req); err != nil {
		reply = map[string]string{"error": "bad request: " + err.Error()}
	} else {
		// A client that hangs up cancels what waits for it, as a dialog.
		gone := make(chan struct{})
		go func() {
			io.Copy(io.Discard, conn)
			close(gone)
		}()
		conn.SetDeadline(time.Time{})
		reply = a.answer(req, gone)
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}
	b, _ := json.Marshal(reply)
	conn.Write(append(b, '\n'))
}

// answer runs a request on the main thread and waits for its reply, and
// for a command that answers later, for that, until gone closes.
func (a *App) answer(req request, gone <-chan struct{}) any {
	r := a.onMain(func() any { return a.handle(req) })
	d, ok := r.(*deferred)
	if !ok {
		return r
	}
	v, err := a.await(d, gone)
	return replyOf(v, err)
}

// replyOf is a command's result as the socket sends it.
func replyOf(v any, err error) any {
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	if v == nil {
		return map[string]bool{"ok": true}
	}
	return v
}

// onMain runs fn on the main thread and waits for what it returns, at
// most answerTimeout.
func (a *App) onMain(fn func() any) any {
	const (
		waiting = iota
		running
		cancelled
	)
	var state atomic.Int32
	out := make(chan any, 1)
	a.post(func() {
		// A request given up on must not run when the window wakes up.
		if !state.CompareAndSwap(waiting, running) {
			return
		}
		out <- fn()
	})
	select {
	case r := <-out:
		return r
	case <-time.After(answerTimeout):
		if state.CompareAndSwap(waiting, cancelled) {
			return map[string]string{"error": "the window did not answer"}
		}
		// It started just now: its answer is on its way.
		return <-out
	}
}

func (a *App) handle(req request) any {
	switch {
	case req.Raise:
		if a.win != nil {
			a.win.Show()
			a.win.Focus()
		}
		return map[string]bool{"ok": true}
	case req.Ls:
		return a.ls()
	case req.Status:
		return a.statusReport()
	case req.Commands:
		return a.allCommands()
	case req.Do != "":
		r, err := a.do(req.Do, req.Args)
		if d, ok := r.(*deferred); ok && err == nil {
			return d
		}
		return replyOf(r, err)
	}
	return map[string]string{"error": "empty request"}
}

type lsPane struct {
	ID       string `json:"id"`
	Terminal string `json:"terminal,omitempty"`
	Name     string `json:"name"`
	Cwd      string `json:"cwd,omitempty"`
	Status   string `json:"status,omitempty"`
	Focused  bool   `json:"focused,omitempty"`
}

type lsTab struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Active bool     `json:"active,omitempty"`
	Panes  []lsPane `json:"panes"`
}

type lsSpace struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Folder string  `json:"folder,omitempty"`
	Active bool    `json:"active,omitempty"`
	Tabs   []lsTab `json:"tabs"`
}

// ls describes spaces, tabs and panes, as herdr-app ls does.
func (a *App) ls() any {
	spaces := []lsSpace{}
	if a.doc != nil {
		for _, s := range a.doc.Spaces {
			ls := lsSpace{ID: s.ID, Name: a.spaceLabel(s), Folder: s.Folder, Active: s.ID == a.activeSpace, Tabs: []lsTab{}}
			for _, t := range s.Tabs {
				lt := lsTab{ID: t.ID, Name: a.tabLabel(t), Active: t.ID == a.activeTab[s.ID], Panes: []lsPane{}}
				for _, p := range t.Panes() {
					lp := lsPane{ID: p.ID, Name: a.paneName(p), Cwd: a.cwdOf(p), Focused: p.ID == a.focus[t.ID]}
					if p.Content != nil {
						lp.Terminal = p.Content.Terminal
					}
					if rec, ok := a.record(p); ok {
						lp.Status = a.word(rec).String()
						if lp.Status == "none" {
							lp.Status = ""
						}
					}
					lt.Panes = append(lt.Panes, lp)
				}
				ls.Tabs = append(ls.Tabs, lt)
			}
			spaces = append(spaces, ls)
		}
	}
	return map[string]any{"spaces": spaces}
}

func (a *App) statusReport() any {
	focus := map[string]string{"space": a.activeSpace}
	if t := a.tab(); t != nil {
		focus["tab"] = t.ID
		focus["pane"] = a.focus[t.ID]
		if z := a.zoomed[t.ID]; z != "" {
			focus["zoomed"] = z
		}
	}
	var overlays []string
	if a.palette.open {
		overlays = append(overlays, "palette")
	}
	if a.prompt != nil {
		overlays = append(overlays, "prompt")
	}
	if a.confirm != nil {
		overlays = append(overlays, "confirm")
	}
	if a.settings {
		overlays = append(overlays, "settings")
	}
	return map[string]any{
		"connected":     a.connected,
		"error":         a.connErr,
		"layout_loaded": a.loaded,
		"doc_version":   a.version,
		"focus":         focus,
		"overlays":      overlays,
		"session":       a.session,
		"home":          a.home,
	}
}

// ask sends a request to a running window.
func ask(home string, req request) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", socketPath(home), 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("no shiori window is running (%s)", home)
	}
	defer conn.Close()
	// A command may wait for the person, as app.ask does.
	limit := 15 * time.Second
	if req.Do != "" {
		limit = maxWait + time.Minute
	}
	conn.SetDeadline(time.Now().Add(limit))
	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(line, &e) == nil && e.Error != "" {
		return nil, errors.New(e.Error)
	}
	return json.RawMessage(line), nil
}
