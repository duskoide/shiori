package core

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const itSession = "shiori-it"

// TestIntegrationRealCore runs against a real, isolated core (session
// shiori-it). It is skipped unless SHIORI_IT=1 and never touches the
// default session.
func TestIntegrationRealCore(t *testing.T) {
	if os.Getenv("SHIORI_IT") != "1" {
		t.Skip("set SHIORI_IT=1 to run against a real core")
	}
	bin, err := FindBinary("")
	if err != nil {
		t.Skip(err)
	}
	// Make sure nothing can point us at another core.
	for _, k := range []string{"HC_SOCKET", "HC_TERMINAL_SOCKET", "HC_TERMINAL_ID", "HC_SCOPE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("HC_SESSION", itSession)
	for _, p := range SocketCandidates(Options{Session: itSession}) {
		if !strings.Contains(p, itSession) {
			t.Fatalf("socket candidate %q is not session-specific", p)
		}
	}
	hc := func(args ...string) ([]byte, error) {
		cmd := exec.Command(bin, args...) // HC_SESSION selects the isolated core
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		return out.Bytes(), err
	}
	if out, err := hc("server", "start"); err != nil {
		t.Fatalf("server start: %v\n%s", err, out)
	}
	t.Cleanup(func() { hc("server", "stop") }) // only our session

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Connect(ctx, Options{Session: itSession, Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ev, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()

	addr, err := c.Run(ctx, RunParams{Argv: []string{"sh", "-c", "echo shiori-ready; exec cat"}, Labels: map[string]string{"herdr.seen": "0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill(context.Background(), addr)

	deadline := time.After(10 * time.Second)
	for {
		if _, ok := ev.Terminal(addr); ok {
			break
		}
		select {
		case <-ev.Changes():
		case <-deadline:
			t.Fatal("terminal never showed up in events")
		}
	}

	s := c.Stream(addr, &Display{FG: "#d4d4d4", BG: "#1e1e1e"})
	defer s.Close()
	if err := s.ResizePixels(100, 30, 9, 18); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("ping-from-it\n")); err != nil {
		t.Fatal(err)
	}
	var seen bytes.Buffer
	buf := make([]byte, 4096)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for !bytes.Contains(seen.Bytes(), []byte("shiori-ready")) || !bytes.Contains(seen.Bytes(), []byte("ping-from-it")) {
			n, err := s.Read(buf)
			seen.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-readDone:
	case <-time.After(10 * time.Second):
		s.Close()
		<-readDone
	}
	if !bytes.Contains(seen.Bytes(), []byte("ping-from-it")) {
		t.Fatalf("stream output: %q", seen.String())
	}

	if err := c.Label(ctx, addr, map[string]string{"herdr.seen": "3"}); err != nil {
		t.Fatal(err)
	}
	tm, err := c.Get(ctx, addr)
	if err != nil || tm.Labels["herdr.seen"] != "3" {
		t.Fatalf("%+v %v", tm, err)
	}
	if err := c.Kill(ctx, addr); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.DocGet(ctx, "shiori-it", ""); err != nil {
		t.Fatal(err)
	}
}
