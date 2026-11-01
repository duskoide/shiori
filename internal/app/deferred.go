package app

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// A deferred is the reply of a command that answers later: a dialog that
// waits for the person, a mod's program that runs. The command returns it
// at once; whoever ran the command waits for it, the control socket by
// the deferred's limit, the window's own callers in the background.
type deferred struct {
	ch    chan reply
	once  sync.Once
	limit time.Duration
	// cancel undoes what still waits, as closing a dialog, when nobody
	// waits for the reply any more. It runs on the main thread.
	cancel func()
}

type reply struct {
	v   any
	err error
}

// maxWait is the longest a command may make its caller wait: a dialog
// waiting for the person.
const maxWait = 10 * time.Minute

// errGone is the reply of a command whose caller went away.
var errGone = errors.New("cancelled: the caller went away")

// newDeferred makes a deferred reply that may take up to limit.
func (a *App) newDeferred(limit time.Duration, cancel func()) *deferred {
	d := &deferred{ch: make(chan reply, 1), limit: limit, cancel: cancel}
	if cancel != nil {
		a.waiting[d] = true
	}
	return d
}

// resolve gives the reply; only the first counts. Any goroutine may call it.
func (d *deferred) resolve(v any, err error) {
	d.once.Do(func() { d.ch <- reply{v, err} })
}

// settle forgets a deferred that was resolved (main thread).
func (a *App) settle(d *deferred) { delete(a.waiting, d) }

// await waits for d, at most its limit, or until gone closes; one not
// answered by then is cancelled. It runs off the main thread.
func (a *App) await(d *deferred, gone <-chan struct{}) (any, error) {
	var timeout <-chan time.Time
	if d.limit > 0 {
		t := time.NewTimer(d.limit)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case r := <-d.ch:
		return r.v, r.err
	case <-timeout:
		a.cancelDeferred(d, fmt.Errorf("no answer within %s", d.limit))
	case <-gone:
		a.cancelDeferred(d, errGone)
	}
	r := <-d.ch
	return r.v, r.err
}

// cancelDeferred resolves d with err, unless it was answered, and undoes
// what still waits for it.
func (a *App) cancelDeferred(d *deferred, err error) {
	d.resolve(nil, err)
	a.post(func() {
		if a.waiting[d] {
			a.settle(d)
			if d.cancel != nil {
				d.cancel()
			}
		}
	})
}

// cancelWaiting cancels every reply still waiting, as the window closes
// (main thread).
func (a *App) cancelWaiting() {
	for d := range a.waiting {
		a.settle(d)
		if d.cancel != nil {
			d.cancel()
		}
		d.resolve(nil, errGone)
	}
}

// later reports what a command that answers later says, for the window's
// own callers (keys, the palette, menus): an error is a toast.
func (a *App) reportLater(name string, r any) {
	d, ok := r.(*deferred)
	if !ok {
		return
	}
	go func() {
		if _, err := a.await(d, nil); err != nil && !errors.Is(err, errGone) {
			a.post(func() { a.notifyLevel(name+": "+err.Error(), levelError) })
		}
	}()
}
