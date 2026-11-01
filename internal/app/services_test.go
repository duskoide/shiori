package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain runs the test binary as a mod's service when FAKE_MOD_SERVICE
// names how it behaves; else it runs the tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_MOD_SERVICE"); mode != "" {
		fakeService(mode)
		return
	}
	os.Exit(m.Run())
}

// fakeService is a mod's service:
//   - echo: answers command.run with what it was asked, fails "boom"
//     commands, and shows each event it gets as a toast;
//   - exit: exits at once, failing;
//   - stubborn: ignores shutdown and SIGTERM.
func fakeService(mode string) {
	switch mode {
	case "exit":
		fmt.Fprintln(os.Stderr, "no config found")
		os.Exit(2)
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)
	}
	conn, err := net.Dial("unix", os.Getenv("SHIORI_SOCKET"))
	if err != nil {
		os.Exit(3)
	}
	r := bufio.NewReader(conn)
	send := func(m map[string]any) {
		m["jsonrpc"] = "2.0"
		b, _ := json.Marshal(m)
		conn.Write(append(b, '\n'))
	}
	send(map[string]any{"id": "h", "method": "hello", "params": map[string]any{"api": 1, "mod": os.Getenv("SHIORI_MOD"), "token": os.Getenv("SHIORI_MOD_TOKEN")}})
	n := 0
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if mode == "stubborn" {
				select {} // waits for SIGKILL
			}
			os.Exit(0)
		}
		var m map[string]any
		json.Unmarshal(line, &m)
		params, _ := m["params"].(map[string]any)
		switch m["method"] {
		case "command.run":
			if strings.HasSuffix(params["command"].(string), ".boom") {
				send(map[string]any{"id": m["id"], "error": map[string]any{"code": 1, "message": "it went boom"}})
				continue
			}
			send(map[string]any{"id": m["id"], "result": map[string]any{"ran": params["command"], "args": params["args"], "cwd": params["context"].(map[string]any)["cwd"]}})
		case "event":
			n++
			send(map[string]any{"id": fmt.Sprint("n", n), "method": "do", "params": map[string]any{"command": "app.notify", "args": map[string]any{"text": "event " + params["name"].(string)}}})
		case "shutdown":
			if mode != "stubborn" {
				os.Exit(0)
			}
		}
	}
}

const svcManifest = `{
  "api": 1, "name": "Helper", "run": [%q], "start": %q,
  "commands": [
    {"name": "hello", "title": "Helper: hello", "args": [{"name": "who"}]},
    {"name": "boom", "title": "Helper: boom"}
  ],
  "events": ["tab.renamed"]
}`

// svcApp is an app serving its socket, with the mod "helper" on, whose
// service is this test binary in mode.
func svcApp(t *testing.T, mode, start string) *App {
	t.Helper()
	t.Setenv("FAKE_MOD_SERVICE", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := uxApp(t)
	serveApp(t, a)
	writeMod(t, a.home, "helper", fmt.Sprintf(svcManifest, exe, start), nil)
	a.reloadMods()
	if err := a.setModEnabled("helper", true); err != nil {
		t.Fatal(err)
	}
	a.started = true
	t.Cleanup(func() { a.stopServices() })
	return a
}

// pumpUntilTrue runs the main thread's posted work until cond holds.
func pumpUntilTrue(t *testing.T, a *App, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		a.runPosted()
		time.Sleep(2 * time.Millisecond)
	}
}

// doWait runs a command and waits for its reply, running the main
// thread meanwhile.
func doWait(t *testing.T, a *App, name string, v args) (any, error) {
	t.Helper()
	r, err := a.do(name, v)
	if err != nil {
		return nil, err
	}
	d, ok := r.(*deferred)
	if !ok {
		return r, nil
	}
	var rv any
	var rerr error
	pumpUntilTrue(t, a, name, func() bool {
		var ok bool
		rv, rerr, ok = replyNow(d)
		return ok
	})
	return rv, rerr
}

func TestServiceRunsCommandsOnDemand(t *testing.T) {
	a := svcApp(t, "echo", "demand")
	if a.mods["helper"].svc != nil {
		t.Fatal("a demand service started before it was needed")
	}
	r, err := doWait(t, a, "helper.hello", args{"who": "you"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(r.(json.RawMessage), &got)
	if got["ran"] != "helper.hello" || got["args"].(map[string]any)["who"] != "you" || got["cwd"] != "/src/api" {
		t.Fatalf("result %v", got)
	}
	if svc := a.mods["helper"].svc; svc.state != svcRunning || svc.sess == nil {
		t.Fatalf("state %q", svc.state)
	}
	if _, err := doWait(t, a, "helper.boom", nil); err == nil || err.Error() != "it went boom" {
		t.Fatalf("boom %v", err)
	}
}

func TestServiceGetsItsEvents(t *testing.T) {
	a := svcApp(t, "echo", "demand")
	a.emitEvents() // the first summary
	a.do("tab.rename", args{"name": "build"})
	a.emitEvents() // the event starts the service, and waits for it
	if svc := a.mods["helper"].svc; svc == nil || len(svc.queued) != 1 {
		t.Fatal("the event did not wait for the service")
	}
	pumpUntilTrue(t, a, "the event's toast", func() bool {
		for _, ts := range a.toasts {
			if ts.text == "event tab.renamed" {
				return true
			}
		}
		return false
	})
	// Connected, it gets them through its session.
	a.do("tab.rename", args{"name": "build 2"})
	a.toasts = nil
	a.emitEvents()
	pumpUntilTrue(t, a, "the second toast", func() bool { return len(a.toasts) == 1 })
}

func TestServiceStartsWithTheWindow(t *testing.T) {
	a := svcApp(t, "echo", "startup")
	a.startServices()
	pumpUntilTrue(t, a, "the service", func() bool { svc := a.mods["helper"].svc; return svc != nil && svc.state == svcRunning })
}

func TestAServiceThatKeepsExitingIsCrashed(t *testing.T) {
	old := serviceBackoff
	serviceBackoff = []time.Duration{time.Millisecond}
	defer func() { serviceBackoff = old }()
	a := svcApp(t, "exit", "startup")
	a.startServices()
	pumpUntilTrue(t, a, "the crash", func() bool { return a.mods["helper"].svc.state == svcCrashed })
	svc := a.mods["helper"].svc
	if !strings.Contains(svc.err, "no config found") {
		t.Fatalf("err %q", svc.err)
	}
	if _, err := doWait(t, a, "helper.hello", nil); err == nil || !strings.Contains(err.Error(), "crashed") {
		t.Fatalf("err %v", err)
	}
	found := false
	for _, ts := range a.toasts {
		found = found || strings.Contains(ts.text, "keeps exiting")
	}
	if !found {
		t.Fatalf("toasts %v", a.toasts)
	}
}

func TestStoppingAStubbornService(t *testing.T) {
	old := stopWait
	stopWait = 100 * time.Millisecond
	defer func() { stopWait = old }()
	a := svcApp(t, "stubborn", "startup")
	a.startServices()
	pumpUntilTrue(t, a, "the service", func() bool { return a.mods["helper"].svc.state == svcRunning })
	pid := a.mods["helper"].svc.cmd.Process.Pid
	start := time.Now()
	done := a.stopService(a.mods["helper"], "test")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("it did not stop")
	}
	if time.Since(start) < 2*stopWait {
		t.Fatalf("stopped after %s: SIGTERM should not have done it", time.Since(start))
	}
	if syscall.Kill(pid, 0) == nil {
		t.Fatal("the process lives")
	}
}

func TestTurningAModOffStopsItsService(t *testing.T) {
	a := svcApp(t, "echo", "startup")
	a.startServices()
	pumpUntilTrue(t, a, "the service", func() bool { return a.mods["helper"].svc.state == svcRunning })
	exited := a.mods["helper"].svc.exited
	a.setModEnabled("helper", false)
	pumpUntilTrue(t, a, "the exit", func() bool {
		select {
		case <-exited:
			return true
		default:
			return false
		}
	})
	if a.mods["helper"].svc != nil {
		t.Fatal("an off mod keeps a service")
	}
}

func TestOnlyTheServiceMayHelloAsTheMod(t *testing.T) {
	a := svcApp(t, "echo", "startup")
	a.startServices()
	pumpUntilTrue(t, a, "the service", func() bool { return a.mods["helper"].svc.state == svcRunning })
	if err := a.attachService(&rpcSession{subs: map[string]bool{}}, "helper", "guess"); err == nil {
		t.Fatal("a wrong token was taken")
	}
	if err := a.attachService(&rpcSession{subs: map[string]bool{}}, "helper", a.mods["helper"].svc.token); err == nil {
		t.Fatal("a second connection was taken")
	}
}
