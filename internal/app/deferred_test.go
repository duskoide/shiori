package app

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// pump runs what goroutines post to the main thread until the test ends;
// the test then touches the app only through the main thread (onMain).
func pump(t *testing.T, a *App) {
	t.Helper()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				a.runPosted()
			}
		}
	}()
	t.Cleanup(func() { close(stop); wg.Wait() })
}

// serveApp listens on a's control socket for the test.
func serveApp(t *testing.T, a *App) {
	t.Helper()
	l, err := a.listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
}

// laterCommand adds a command answering with its deferred, which the test
// gets on d.
func laterCommand(a *App, name string, d chan<- *deferred, cancelled chan<- bool) {
	a.extra = append(a.extra, &command{Name: name, Title: name, Mod: "test", run: func(a *App, _ args) (any, error) {
		var def *deferred
		def = a.newDeferred(time.Minute, func() { cancelled <- true })
		d <- def
		return def, nil
	}})
}

func TestSocketWaitsForADeferredReply(t *testing.T) {
	a, _ := testApp(t)
	defs, cancelled := make(chan *deferred, 1), make(chan bool, 1)
	laterCommand(a, "test.later", defs, cancelled)
	serveApp(t, a)
	pump(t, a)
	got := make(chan json.RawMessage, 1)
	go func() {
		b, err := Do(a.home, "test.later", nil)
		if err != nil {
			b = json.RawMessage(`"` + err.Error() + `"`)
		}
		got <- b
	}()
	d := <-defs
	select {
	case b := <-got:
		t.Fatalf("answered before the reply: %s", b)
	case <-time.After(50 * time.Millisecond):
	}
	d.resolve(map[string]string{"value": "hi"}, nil)
	if b := <-got; !strings.Contains(string(b), `"value":"hi"`) {
		t.Fatalf("reply %s", b)
	}
}

func TestHangingUpCancelsTheReply(t *testing.T) {
	a, _ := testApp(t)
	defs, cancelled := make(chan *deferred, 1), make(chan bool, 1)
	laterCommand(a, "test.later", defs, cancelled)
	serveApp(t, a)
	pump(t, a)
	conn, err := net.Dial("unix", socketPath(a.home))
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte(`{"do":"test.later"}` + "\n"))
	<-defs
	conn.Close()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("the reply was not cancelled")
	}
}

func TestDeferredErrorsAreToasts(t *testing.T) {
	a, _ := testApp(t)
	a.extra = append(a.extra, &command{Name: "test.fails", Title: "Fails", run: func(a *App, _ args) (any, error) {
		d := a.newDeferred(time.Minute, nil)
		go d.resolve(nil, errors.New("it broke"))
		return d, nil
	}})
	a.doAction("test.fails", "")
	drain(t, a, func() bool { return len(a.toasts) == 1 })
	if tt := a.toasts[0]; tt.level != levelError || tt.text != "test.fails: it broke" {
		t.Fatalf("toast %+v", tt)
	}
}

func TestClosingTheWindowCancelsWhatWaits(t *testing.T) {
	a, _ := testApp(t)
	cancelled := false
	d := a.newDeferred(time.Minute, func() { cancelled = true })
	a.cancelWaiting()
	if _, err := a.await(d, nil); !errors.Is(err, errGone) || !cancelled || len(a.waiting) != 0 {
		t.Fatalf("err %v cancelled %v waiting %d", err, cancelled, len(a.waiting))
	}
}

func TestModCommandsAreFound(t *testing.T) {
	a, _ := testApp(t)
	a.extra = append(a.extra, &command{Name: "git.branch", Title: "Git: branch", Mod: "git", run: func(*App, args) (any, error) { return "ok", nil }})
	if a.findCommand("git.branch") == nil || a.findCommand("tab.new") == nil {
		t.Fatal("not found")
	}
	if r, err := a.do("git.branch", nil); err != nil || r != "ok" {
		t.Fatalf("%v %v", r, err)
	}
	found := false
	for _, it := range a.paletteItems() {
		found = found || it.title == "Git: branch"
	}
	if !found {
		t.Fatal("not in the palette")
	}
	b, _ := json.Marshal(a.allCommands())
	if !strings.Contains(string(b), `"mod":"git"`) {
		t.Fatalf("commands %s", b)
	}
}
