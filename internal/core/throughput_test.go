package core

import (
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"
)

// TestIntegrationThroughput measures how fast a Stream drains a flood of
// output from a real, isolated core (session herdr-mygo-tp), with no
// rendering. Skipped unless HERDR_MYGO_IT=1.
func TestIntegrationThroughput(t *testing.T) {
	if os.Getenv("HERDR_MYGO_IT") != "1" {
		t.Skip("set HERDR_MYGO_IT=1 to run against a real core")
	}
	bin, err := FindBinary("")
	if err != nil {
		t.Skip(err)
	}
	for _, k := range []string{"HC_SOCKET", "HC_TERMINAL_SOCKET", "HC_TERMINAL_ID", "HC_SCOPE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	const session = "herdr-mygo-tp"
	t.Setenv("HC_SESSION", session)
	hc := func(args ...string) string {
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("hc %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	hc("server", "start")
	t.Cleanup(func() {
		exec.Command(bin, "server", "stop").Run()
		if dir, err := os.UserHomeDir(); err == nil {
			os.RemoveAll(dir + "/.local/state/herdr-core/" + session)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := Connect(ctx, Options{Session: session, Binary: bin})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	addr := regexp.MustCompile(`\bt[0-9a-z]+\b`).FindString(hc("run", "--bg", "sh -c 'sleep 1; seq 1 2000000; sleep 2'"))
	if addr == "" {
		t.Fatal("no terminal address")
	}
	st := c.Stream(addr, nil)
	defer st.Close()
	st.Resize(80, 24)

	buf := make([]byte, 64<<10)
	var reads, total int
	var first, last time.Time
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := st.Read(buf)
			if n > 0 {
				if total > 4096 && first.IsZero() {
					first = time.Now()
				}
				reads++
				total += n
				last = time.Now()
			}
			if err == io.EOF || err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
	}
	if total < 10<<20 {
		t.Fatalf("read only %d bytes", total)
	}
	el := last.Sub(first)
	t.Logf("%d bytes in %d reads (%.0f B/read) over %v: %.1f MB/s", total, reads, float64(total)/float64(reads), el, float64(total)/el.Seconds()/1e6)
}
