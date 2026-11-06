package app

import (
	"time"

	"github.com/duskoide/shiori/internal/core"
	"github.com/duskoide/shiori/internal/layout"
	"github.com/duskoide/shiori/internal/programs"
	"github.com/duskoide/shiori/internal/recovery"
	"github.com/duskoide/shiori/internal/resume"
)

// Agents resumed: a pane whose terminal was lost with its core, and that
// ran a coding agent, starts the agent again on the conversation it was
// in. Which conversation is looked for now and then while the agents run
// (see package resume), off the main thread, and kept in the snapshot; a
// crash leaves nothing to ask.

// agentScanEvery is how often the agents' conversations are looked for.
const agentScanEvery = 15 * time.Second

// agentSession is the conversation an agent's process was found in.
type agentSession struct {
	pid int
	key string
	s   resume.Session
}

// resumeAgentsOn reports whether restored panes start their agents again.
func (a *App) resumeAgentsOn() bool {
	return a.restoreOn() && (a.cfg.ResumeAgents == nil || *a.cfg.ResumeAgents)
}

func (a *App) setResumeAgents(on bool) {
	a.cfg.ResumeAgents = &on
	if on {
		a.cfg.ResumeAgents = nil // the default
	}
	a.saveSettings()
}

// resumable is the agent a terminal of this machine runs, if it can start
// again: its program, and its process.
func resumable(rec core.Terminal) (programs.Program, *core.Foreground, bool) {
	if rec.State != "live" || layout.MachineOf(rec.Address) != "" {
		return programs.Program{}, nil, false
	}
	prog := programOf(rec)
	if !prog.Agent || (len(prog.Resume.ID) == 0 && len(prog.Resume.Last) == 0) {
		return programs.Program{}, nil, false
	}
	return prog, rec.Foreground, true
}

// scanAgents looks for the agents' conversations, at most every
// agentScanEvery, and once more after the last call within it.
func (a *App) scanAgents() {
	if !a.resumeAgentsOn() || a.rc.scanning {
		return
	}
	if wait := agentScanEvery - time.Since(a.rc.scannedAt); wait > 0 {
		if !a.rc.scanDue {
			a.rc.scanDue = true
			a.later(wait, func() {
				a.rc.scanDue = false
				a.scanAgents()
			})
		}
		return
	}
	var procs []resume.Proc
	var addrs []string
	for addr, rec := range a.terminals {
		prog, fg, ok := resumable(rec)
		if !ok || fg == nil || fg.Pid <= 0 || !resume.Supported(prog.Key) {
			continue
		}
		cwd := fg.Cwd
		if cwd == "" {
			cwd = rec.Cwd
		}
		procs = append(procs, resume.Proc{Key: prog.Key, Pid: fg.Pid, Argv: fg.Argv, Cwd: cwd})
		addrs = append(addrs, addr)
	}
	a.rc.scannedAt = time.Now()
	if len(procs) == 0 {
		a.rc.sessions = nil
		return
	}
	a.rc.scanning = true
	go func() {
		env := resume.System()
		for i := range procs {
			procs[i].StartMs = env.StartMs(procs[i].Pid)
		}
		found := resume.Find(env, procs)
		a.post(func() {
			a.rc.scanning = false
			a.rc.scannedAt = time.Now()
			sessions := map[string]agentSession{}
			for i, s := range found {
				if s.Known() {
					sessions[addrs[i]] = agentSession{pid: procs[i].Pid, key: procs[i].Key, s: s}
				}
			}
			a.rc.sessions = sessions
			a.snapshotSoon()
		})
	}()
}

// agentOf is what the snapshot keeps of the agent a live terminal runs:
// its key, and the conversation it was found in.
func (a *App) agentOf(rec core.Terminal, pane *recovery.Pane) {
	prog, fg, ok := resumable(rec)
	if !ok {
		return
	}
	pane.Agent = prog.Key
	if s, ok := a.rc.sessions[rec.Address]; ok && s.key == prog.Key && fg != nil && s.pid == fg.Pid {
		pane.SessionID, pane.SessionPath = s.s.ID, s.s.Path
	}
}

// resumeArgv is what starts a lost pane's agent again (off the main
// thread: it looks at files), nil for a fresh shell.
func resumeArgv(spec resume.Spec, was recovery.Pane) []string {
	if was.Agent == "" {
		return nil
	}
	cmd := spec.Argv(resume.Session{ID: was.SessionID, Path: was.SessionPath})
	if len(cmd) == 0 {
		return nil
	}
	argv, err := launchArgv(args{"argv": cmd}, "")
	if err != nil {
		return nil
	}
	return argv
}

// resumeSpec is how an agent of programs.json starts again.
func resumeSpec(key string) resume.Spec {
	if key == "" {
		return resume.Spec{}
	}
	return programs.Resolve(key, nil).Resume
}
