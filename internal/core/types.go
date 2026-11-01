// Package core is a client for the herdr-core framed JSON-RPC protocol: RPC
// calls, the live terminal event stream and per-terminal byte streams.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"
)

// Options configures Connect.
type Options struct {
	// Socket is an explicit socket path; otherwise it is discovered.
	Socket string
	// Session is the core session ("" = HC_SESSION or "default").
	Session string
	// Binary is the herdr-core/hc binary used to auto-start the core ("" = search).
	Binary string
	// AutoStart runs "<binary> connect" as a byte pump when dialing fails.
	AutoStart bool
	// CallTimeout bounds each Call (0 = 10s).
	CallTimeout time.Duration
}

// DefaultCallTimeout is the per-call timeout when Options.CallTimeout is zero.
const DefaultCallTimeout = 10 * time.Second

// Errors returned by the client.
var (
	// ErrLost reports that the connection to the core dropped.
	ErrLost = errors.New("herdr-core: connection lost")
	// ErrClosed reports use of a closed Client.
	ErrClosed = errors.New("herdr-core: client closed")
)

// Hello is the core's reply to the hello handshake.
type Hello struct {
	Version    string   `json:"version"`
	Generation int      `json:"generation"`
	Methods    []string `json:"methods"`
	Features   []string `json:"features"`
	Framed     bool     `json:"framed"`
	Core       struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"core"`
}

// RPCError is an error reply from the core.
type RPCError struct {
	Code    int
	Message string
	Kind    string
	Hint    string
}

// Error implements error.
func (e *RPCError) Error() string {
	s := "herdr-core: " + e.Message
	if e.Kind != "" {
		s += " [" + e.Kind + "]"
	}
	if e.Hint != "" {
		s += " (" + e.Hint + ")"
	}
	return s
}

// IsKind reports whether err is an *RPCError of the given kind.
func IsKind(err error, kind string) bool {
	var re *RPCError
	return errors.As(err, &re) && re.Kind == kind
}

// RunParams are the parameters of terminal.run (see Client.Run).
type RunParams struct {
	Argv   []string
	Cwd    string
	Env    map[string]string
	Labels map[string]string
	On     string
	Keep   bool
	Name   string
}

// Terminal is a terminal record of the core.
type Terminal struct {
	Address    string            `json:"address"`
	State      string            `json:"state"`
	Cwd        string            `json:"cwd"`
	Title      string            `json:"title"`
	Name       *string           `json:"name"`
	Parent     *string           `json:"parent"`
	Argv       []string          `json:"argv"`
	Labels     map[string]string `json:"labels"`
	Status     *Status           `json:"status"`
	Turn       uint64            `json:"turn"`
	Keep       bool              `json:"keep"`
	Cols       int               `json:"cols"`
	Rows       int               `json:"rows"`
	Foreground *Foreground       `json:"foreground"`
	CreatedMs  int64             `json:"created_ms"`
	StatusMs   int64             `json:"status_ms"`
	ExitCode   *int              `json:"exit_code"`
	Reason     *string           `json:"reason"`

	// Extras beyond the minimal record.
	Pid        *int            `json:"pid"`
	Signal     *int            `json:"signal"`
	FinishedMs *int64          `json:"finished_ms"`
	Reported   bool            `json:"reported"`
	Records    json.RawMessage `json:"records,omitempty"`
}

// Live reports whether the terminal is not finished.
func (t Terminal) Live() bool { return t.State == "live" }

// Status is what a program reported through OSC 7501.
type Status struct {
	State string `json:"state"`
	App   string `json:"app"`
	Kind  string `json:"kind"`
	Msg   string `json:"msg"`
}

// Foreground is the foreground process of a terminal.
type Foreground struct {
	Pid  int      `json:"pid"`
	Name string   `json:"name"`
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd"`
}

// Machine is a machine known to the core (machine.list).
type Machine struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	ID        string `json:"id"`
	Host      string `json:"host"`
	Version   string `json:"version"`
	State     string `json:"state"`
	Why       string `json:"why"`
	SinceMs   int64  `json:"since_ms"`
	Session   string `json:"session"`
	Temporary bool   `json:"temporary"`
	Via       bool   `json:"via"`
}

// Change is a coalesced notification from the event stream.
type Change struct {
	// Docs: a document changed (or events were missed): re-read documents.
	Docs bool
	// DocMachines names the machines whose document changed ("" is this
	// one). Nil with Docs set means every machine's: events were missed.
	DocMachines map[string]bool
	// Terminals: the terminal snapshot changed.
	Terminals bool
	// Seen is the highest herdr.seen label value written per terminal in the
	// coalesced batch.
	Seen map[string]uint64
}

func (c *Change) merge(o Change) {
	switch {
	case !o.Docs:
	case !c.Docs:
		c.DocMachines = maps.Clone(o.DocMachines)
	case c.DocMachines == nil || o.DocMachines == nil:
		c.DocMachines = nil // every machine's already, or now
	default:
		maps.Copy(c.DocMachines, o.DocMachines)
	}
	c.Docs = c.Docs || o.Docs
	c.Terminals = c.Terminals || o.Terminals
	for k, v := range o.Seen {
		if c.Seen == nil {
			c.Seen = map[string]uint64{}
		}
		if v > c.Seen[k] {
			c.Seen[k] = v
		}
	}
}

// DocChanged reports whether the machine's document may have changed.
func (c Change) DocChanged(machine string) bool {
	return c.Docs && (c.DocMachines == nil || c.DocMachines[machine])
}

func (c Change) empty() bool { return !c.Docs && !c.Terminals && len(c.Seen) == 0 }

// Display describes the viewer's colors and cell size (FACTS / attach display).
type Display struct {
	FG     string `json:"fg,omitempty"`
	BG     string `json:"bg,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Cell   [2]int `json:"cell"`
}

// MarshalJSON omits unset fields; "cell" is only written when non-zero.
func (d Display) MarshalJSON() ([]byte, error) {
	out := struct {
		FG     string  `json:"fg,omitempty"`
		BG     string  `json:"bg,omitempty"`
		Cursor string  `json:"cursor,omitempty"`
		Cell   *[2]int `json:"cell,omitempty"`
	}{FG: d.FG, BG: d.BG, Cursor: d.Cursor}
	if d.Cell[0] > 0 && d.Cell[1] > 0 {
		out.Cell = &d.Cell
	}
	return json.Marshal(out)
}

// CloseReason is why a terminal stream ended.
type CloseReason struct {
	Reason   string `json:"reason"`
	ExitCode *int   `json:"exit_code"`
	Signal   *int   `json:"signal"`
}

func (r CloseReason) String() string {
	s := r.Reason
	if r.ExitCode != nil {
		s += fmt.Sprintf(" (exit %d)", *r.ExitCode)
	}
	if r.Signal != nil {
		s += fmt.Sprintf(" (signal %d)", *r.Signal)
	}
	return s
}

func cloneTerminal(t Terminal) Terminal {
	if t.Labels != nil {
		m := make(map[string]string, len(t.Labels))
		for k, v := range t.Labels {
			m[k] = v
		}
		t.Labels = m
	}
	t.Argv = append([]string(nil), t.Argv...)
	if t.Argv != nil && len(t.Argv) == 0 {
		t.Argv = []string{}
	}
	if t.Status != nil {
		s := *t.Status
		t.Status = &s
	}
	if t.Foreground != nil {
		f := *t.Foreground
		f.Argv = append([]string(nil), f.Argv...)
		t.Foreground = &f
	}
	return t
}
