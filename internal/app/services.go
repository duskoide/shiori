package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"syscall"
	"time"

	"github.com/duskoide/shiori/internal/mods"
)

// A mod's service is its program that stays running: it connects back to
// the control socket with the token it was started with, handles the
// commands that have no program of their own (command.run) and gets the
// events its manifest names. It starts with the window or when first
// needed, starts again after it exits, and stops when the window closes,
// the mod is turned off or its manifest changes.

// Service states, as Settings → Mods shows them.
const (
	svcStarting   = "starting"
	svcRunning    = "running"
	svcRestarting = "restarting"
	svcCrashed    = "crashed"
	svcStopped    = "stopped"
)

var (
	// serviceBackoff is how long a service that exited waits to start
	// again, by the exits in a row.
	serviceBackoff = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	// crashExits exits within crashWindow mark a service crashed.
	crashExits  = 5
	crashWindow = time.Minute
	// connectWait is how long a call waits for a service to connect.
	connectWait = 10 * time.Second
	// callWait is how long a service may take to answer command.run.
	callWait = 30 * time.Second
	// stopWait is how long a stopping service has before each harder push:
	// after shutdown, SIGTERM; after SIGTERM, SIGKILL.
	stopWait = 2 * time.Second
)

// service is a mod's service; it belongs to the main thread.
type service struct {
	st      *modState
	state   string
	err     string // why it last exited or failed
	cmd     *exec.Cmd
	exited  chan struct{} // closed when the process of cmd ended
	token   string
	sess    *rpcSession
	gen     int // the process's number: news of an older one is no news
	waiters []func(*rpcSession, error)
	queued  [][]byte // events for it while it was not connected
	exits   []time.Time
	timer   *time.Timer
}

// startServices starts the services that start with the window.
func (a *App) startServices() {
	if !a.started {
		return
	}
	for _, id := range a.modOrder {
		st := a.mods[id]
		if st.on && len(st.Manifest.Run) > 0 && st.Manifest.StartsAtStartup() && (st.svc == nil || st.svc.state == svcStopped) {
			a.startService(st)
		}
	}
}

// startService starts a mod's service, unless it runs or is starting.
func (a *App) startService(st *modState) {
	svc := st.svc
	if svc == nil {
		svc = &service{st: st}
		st.svc = svc
	}
	switch svc.state {
	case svcStarting, svcRunning:
		return
	}
	if svc.timer != nil {
		svc.timer.Stop()
		svc.timer = nil
	}
	b := make([]byte, 16)
	rand.Read(b)
	svc.token = hex.EncodeToString(b)
	svc.gen++
	gen := svc.gen
	argv := mods.Resolve(st.Dir, st.Manifest.Run)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = st.Dir
	cmd.Env = append(a.modEnv(st), "SHIORI_MOD_TOKEN="+svc.token)
	cmd.Stderr = a.modLog(st)
	setpgid(cmd)
	if err := cmd.Start(); err != nil {
		svc.state, svc.err = svcCrashed, "cannot start: "+err.Error()
		a.notifyLevel(fmt.Sprintf("Mod %q: its service cannot start: %v", a.modName(st.ID), err), levelError)
		svc.fail(errors.New(svc.err))
		return
	}
	svc.cmd, svc.state, svc.err = cmd, svcStarting, ""
	exited := make(chan struct{})
	svc.exited = exited
	go func() {
		err := cmd.Wait()
		close(exited)
		a.post(func() { a.serviceExited(svc, gen, err) })
	}()
	time.AfterFunc(connectWait, func() {
		a.post(func() {
			if svc.gen == gen && svc.sess == nil && len(svc.waiters) > 0 {
				svc.fail(fmt.Errorf("its service did not connect within %s", connectWait))
			}
		})
	})
}

// fail answers what waits for the service with err.
func (svc *service) fail(err error) {
	w := svc.waiters
	svc.waiters = nil
	for _, fn := range w {
		fn(nil, err)
	}
}

// serviceExited handles the end of a service's process.
func (a *App) serviceExited(svc *service, gen int, err error) {
	if svc.gen != gen {
		return
	}
	svc.cmd = nil
	why := "it exited"
	if err != nil {
		why = err.Error()
	}
	if tail := lastLine([]byte(modLogTail(svc.st, 1))); tail != "" {
		why += ": " + tail
	}
	if svc.state == svcStopped {
		return
	}
	now := time.Now()
	svc.exits = append(slices.DeleteFunc(svc.exits, func(t time.Time) bool { return now.Sub(t) > crashWindow }), now)
	svc.err = why
	if len(svc.exits) >= crashExits {
		svc.state = svcCrashed
		a.notifyLevel(fmt.Sprintf("Mod %q: its service keeps exiting and is stopped (%s). Its log is in Settings → Mods.", a.modName(svc.st.ID), why), levelError)
		svc.fail(errors.New("its service crashed; restart it in Settings → Mods"))
		return
	}
	svc.state = svcRestarting
	wait := serviceBackoff[min(len(svc.exits)-1, len(serviceBackoff)-1)]
	svc.timer = time.AfterFunc(wait, func() {
		a.post(func() {
			if svc.gen == gen && svc.state == svcRestarting && svc.st.on && a.mods[svc.st.ID] == svc.st {
				a.startService(svc.st)
			}
		})
	})
}

// stopService stops a mod's service: it hears shutdown, then SIGTERM,
// then SIGKILL, each after stopWait. It returns when the process ended,
// if wait, else at once.
func (a *App) stopService(st *modState, why string) <-chan struct{} {
	svc := st.svc
	done := make(chan struct{})
	if svc == nil {
		close(done)
		return done
	}
	svc.state = svcStopped
	svc.gen++ // an exit from here on is no news
	if svc.timer != nil {
		svc.timer.Stop()
		svc.timer = nil
	}
	svc.fail(errors.New("its service stopped: " + why))
	svc.queued = nil
	sess, cmd, exited := svc.sess, svc.cmd, svc.exited
	svc.sess, svc.cmd = nil, nil
	if sess != nil {
		sess.notify("shutdown", map[string]string{"reason": why})
	}
	if cmd == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		select {
		case <-exited:
			return
		case <-time.After(stopWait):
		}
		signalGroup(cmd, syscall.SIGTERM)
		select {
		case <-exited:
			return
		case <-time.After(stopWait):
		}
		signalGroup(cmd, syscall.SIGKILL)
		<-exited
	}()
	return done
}

// restartService stops a mod's service and starts it afresh, its crashes
// forgotten.
func (a *App) restartService(st *modState) {
	if st.svc != nil {
		a.stopService(st, "restarted")
		st.svc = &service{st: st}
	}
	a.startService(st)
}

// stopServices stops every service, waiting for them a few seconds at
// most, as the window closes.
func (a *App) stopServices() {
	var all []<-chan struct{}
	for _, id := range a.modOrder {
		if st := a.mods[id]; st.svc != nil {
			all = append(all, a.stopService(st, "the window closes"))
		}
	}
	deadline := time.After(2*stopWait + time.Second)
	for _, done := range all {
		select {
		case <-done:
		case <-deadline:
			return
		}
	}
}

// attachService ties a session to the mod whose service it is (main
// thread).
func (a *App) attachService(s *rpcSession, mod, token string) error {
	st := a.mods[mod]
	if st == nil || st.svc == nil || st.svc.token == "" || (st.svc.state != svcStarting && st.svc.state != svcRunning) {
		return fmt.Errorf("no mod %q is running", mod)
	}
	svc := st.svc
	if token != svc.token {
		return fmt.Errorf("wrong token for mod %q", mod)
	}
	if svc.sess != nil {
		return fmt.Errorf("mod %q is connected already", mod)
	}
	s.mod, svc.sess, svc.state = mod, s, svcRunning
	svc.exits = nil // it got this far: earlier exits are forgiven
	for _, e := range st.Manifest.Events {
		s.subs[e] = true
	}
	for _, ev := range svc.queued {
		s.push(ev)
	}
	svc.queued = nil
	w := svc.waiters
	svc.waiters = nil
	for _, fn := range w {
		fn(s, nil)
	}
	return nil
}

// detachService forgets a session that was a mod's service (main thread).
func (a *App) detachService(s *rpcSession) {
	if s.mod == "" {
		return
	}
	if st := a.mods[s.mod]; st != nil && st.svc != nil && st.svc.sess == s {
		st.svc.sess = nil
		if st.svc.state == svcRunning {
			st.svc.state = svcStarting // its process may connect again
		}
	}
}

// withService calls fn with the mod's service's session, starting it and
// waiting for it to connect when needed.
func (a *App) withService(st *modState, fn func(*rpcSession, error)) {
	svc := st.svc
	if svc != nil && svc.sess != nil {
		fn(svc.sess, nil)
		return
	}
	if svc != nil && svc.state == svcCrashed {
		fn(nil, errors.New("its service crashed; restart it in Settings → Mods"))
		return
	}
	a.startService(st)
	if svc = st.svc; svc.state == svcCrashed {
		fn(nil, errors.New(svc.err))
		return
	}
	svc.waiters = append(svc.waiters, fn)
}

// runOnService runs a command through the mod's service.
func (a *App) runOnService(st *modState, mc mods.Command, v args) (any, error) {
	f := a.snapshot().focus
	params := map[string]any{"command": st.ID + "." + mc.Name, "args": v,
		"context": map[string]string{"space": f.space, "tab": f.tab, "pane": f.pane, "terminal": f.terminal, "cwd": f.cwd, "machine": f.machine}}
	if params["args"] == nil {
		params["args"] = map[string]any{}
	}
	d := a.newDeferred(connectWait+callWait+5*time.Second, nil)
	a.withService(st, func(s *rpcSession, err error) {
		if err != nil {
			d.resolve(nil, err)
			return
		}
		go func() {
			res, err := s.call("command.run", params, callWait)
			if err != nil {
				d.resolve(nil, err)
				return
			}
			if len(res) == 0 || string(res) == "null" {
				d.resolve(nil, nil)
				return
			}
			d.resolve(json.RawMessage(res), nil)
		}()
	})
	return d, nil
}

// wantEvents are the events the services that are not connected wait for.
func (a *App) wantEvents() []string {
	var out []string
	for _, id := range a.modOrder {
		st := a.mods[id]
		if !st.on || len(st.Manifest.Events) == 0 || (st.svc != nil && (st.svc.sess != nil || st.svc.state == svcCrashed)) {
			continue
		}
		out = append(out, st.Manifest.Events...)
	}
	return out
}

// eventForServices keeps an event for the services that want it and are
// not connected, starting them.
func (a *App) eventForServices(name string, ev []byte) {
	for _, id := range a.modOrder {
		st := a.mods[id]
		if !st.on || !slices.Contains(st.Manifest.Events, name) {
			continue
		}
		if st.svc != nil && (st.svc.sess != nil || st.svc.state == svcCrashed) {
			continue
		}
		a.startService(st)
		if svc := st.svc; svc.state != svcCrashed {
			if len(svc.queued) >= eventBuffer {
				svc.queued = svc.queued[1:]
			}
			svc.queued = append(svc.queued, ev)
		}
	}
}
