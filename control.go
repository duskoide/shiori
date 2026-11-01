package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// The control socket lets "herdr-mygo ls|status|do" and agents talk to
// the running window: one JSON request line, one JSON reply line.

// request is a line sent to the control socket.
type request struct {
	Ls     bool           `json:"ls,omitempty"`
	Status bool           `json:"status,omitempty"`
	Do     string         `json:"do,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	Raise  bool           `json:"raise,omitempty"`
}

func socketPath(home string) string { return filepath.Join(home, "herdr-mygo.sock") }

// listen serves the control socket until the app quits.
func (a *App) listen() (net.Listener, error) {
	if err := os.MkdirAll(a.home, 0o755); err != nil {
		return nil, err
	}
	path := socketPath(a.home)
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		return nil, errors.New("another herdr-mygo window uses " + a.home)
	}
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
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
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req request
	var reply any
	if err := json.Unmarshal(line, &req); err != nil {
		reply = map[string]string{"error": "bad request: " + err.Error()}
	} else {
		reply = a.answer(req)
	}
	b, _ := json.Marshal(reply)
	conn.Write(append(b, '\n'))
}

// answer runs a request on the main thread and waits for its reply.
func (a *App) answer(req request) any {
	out := make(chan any, 1)
	a.post(func() { out <- a.handle(req) })
	select {
	case r := <-out:
		return r
	case <-time.After(5 * time.Second):
		return map[string]string{"error": "the window did not answer"}
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
	case req.Do != "":
		r, err := a.do(req.Do, req.Args)
		if err != nil {
			return map[string]string{"error": err.Error()}
		}
		if r == nil {
			r = map[string]bool{"ok": true}
		}
		return r
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
		return nil, fmt.Errorf("no herdr-mygo window is running (%s)", home)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second))
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
