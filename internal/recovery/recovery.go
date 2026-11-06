// Package recovery keeps what the window needs to bring its terminals back
// after herdr-core stopped without them: a crash, a kill, a reboot.
//
// The layout survives in the core's document, but the terminals do not:
// the panes would point at addresses the new core never had (or that it
// gave to other terminals since). So, as herdr does with its
// session.json, the window keeps a snapshot on disk of the layout and, for
// each pane, the terminal it shows, the folder that worked in, when it was
// created and which core process ran it. Panes are told apart by id, which
// is unique, not by terminal address, which a new core gives out again. A
// pane's terminal recorded under another core process that the current
// core does not have (or has under the same address but created at
// another time) was lost with its core, and the pane gets a fresh shell in
// that folder.
package recovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Version is the snapshot format this package writes.
const Version = 1

// Core is one process of herdr-core: its pid and when it started. The
// start is worked out from server.status's uptime, so it is only as exact
// as the call was quick.
type Core struct {
	Pid     int   `json:"pid,omitempty"`
	StartMs int64 `json:"start_ms,omitempty"`
}

// slack is how far apart two starts worked out for the same process may
// be: the latency of the calls, and clocks that moved.
const slack = 5 * time.Second

// At is the core process of pid, which has run for uptime at now.
func At(pid int, uptimeMs int64, now time.Time) Core {
	if pid <= 0 || uptimeMs < 0 {
		return Core{}
	}
	return Core{Pid: pid, StartMs: now.UnixMilli() - uptimeMs}
}

// Known reports whether c names a process.
func (c Core) Known() bool { return c.Pid > 0 && c.StartMs > 0 }

// Same reports whether c and o are the same process. A pid alone is not
// enough: after a reboot the new core may get the old one's pid.
func (c Core) Same(o Core) bool {
	d := c.StartMs - o.StartMs
	if d < 0 {
		d = -d
	}
	return c.Pid == o.Pid && time.Duration(d)*time.Millisecond <= slack
}

// Pane is what a snapshot keeps of a pane: the terminal it shows.
type Pane struct {
	// Terminal is the terminal's address.
	Terminal string `json:"terminal"`
	// CreatedMs is the core's created_ms of it: an address the new core
	// gave to another terminal has another.
	CreatedMs int64 `json:"created_ms,omitempty"`
	// Cwd is the folder its program last worked in.
	Cwd string `json:"cwd,omitempty"`
	// Core is the core process it was last seen running in.
	Core Core `json:"core"`
	// Agent is the key of the coding agent it ran (programs.json's), to
	// start again; SessionID and SessionPath the conversation it was in,
	// when it could be told.
	Agent       string `json:"agent,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	SessionPath string `json:"session_path,omitempty"`
}

// Snapshot is the file: the layout as the window last showed it, and its
// panes' terminals, by pane id.
type Snapshot struct {
	Version int             `json:"version"`
	SavedMs int64           `json:"saved_ms,omitempty"`
	Core    Core            `json:"core"` // the core last connected to
	Layout  json.RawMessage `json:"layout,omitempty"`
	Panes   map[string]Pane `json:"panes,omitempty"`
}

// FileName is the snapshot's file for a herdr-core session: session.json
// for the default one, session-<name>.json for another.
func FileName(session string) string {
	if session == "" || session == "default" {
		return "session.json"
	}
	return "session-" + session + ".json"
}

// Load reads a snapshot. A missing file is an empty snapshot.
func Load(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Snapshot{Version: Version}, nil
	}
	if err != nil {
		return &Snapshot{Version: Version}, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return &Snapshot{Version: Version}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if s.Version > Version {
		return &Snapshot{Version: Version}, fmt.Errorf("%s: version %d is newer than this app's (%d)", filepath.Base(path), s.Version, Version)
	}
	s.Version = Version
	return &s, nil
}

// Encode is the file's contents.
func (s *Snapshot) Encode() ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// WriteFile writes b to path whole or not at all: to a file beside it,
// synced, then renamed over it, so a crash or a power cut cannot leave half
// a snapshot.
func WriteFile(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// Lost reports whether the terminal at addr that a pane shows was lost
// with the core it ran in, and what the snapshot knows of it: the pane
// showed it under a core process other than cur, and cur does not list it
// (listed false), or lists a terminal created at another time under its
// address.
func (s *Snapshot) Lost(pane, addr string, cur Core, listed bool, createdMs int64) (Pane, bool) {
	p, ok := s.Panes[pane]
	if !ok || p.Terminal != addr || !cur.Known() || !p.Core.Known() || p.Core.Same(cur) {
		return Pane{}, false
	}
	if listed && (p.CreatedMs == 0 || p.CreatedMs == createdMs) {
		// It outlived its core, in a holder process.
		return Pane{}, false
	}
	return p, true
}
