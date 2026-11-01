package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeHC is a shell script standing in for hc: it records its arguments
// and environment, one call per line, and answers by its first arguments.
const fakeHC = `#!/bin/sh
printf '%s|%s\n' "$FAKE_HC_SESSION" "$*" >> "$FAKE_HC_LOG"
case "$*" in
"--version") echo "hc 0.1.16 (abc)" ;;
"machine ls --json") echo '{"machines":[{"name":"mac","target":"me@mac","host":"mac.local","version":"0.1.16","state":"connected","since_ms":5},{"name":"tmp","state":"connected","temporary":true},{"name":"old","state":"needs_update"}]}' ;;
"machine add me@box --json") ;;
"machine add new@box --json") echo '{"error":{"kind":"host_key","message":"the host key of box is not known"}}'; exit 1 ;;
"machine add bad@box --json") echo '{"error":{"kind":"unreachable","message":"cannot reach box"}}' >&2; exit 1 ;;
"machine rename mac laptop") echo "renamed" ;;
"machine rm gone") echo "no machine gone" >&2; exit 2 ;;
"machine update old") echo '{"version":"0.1.16"}' ;;
"machine ls --json --slow") exec sleep 5 ;;
exec*"-- git status") echo '{"code":0,"stdout":"IE0gYS5nbwA=","stderr":"","timed_out":false}' ;;
exec*"-- false") echo '{"code":1,"stdout":"","stderr":"bm9wZQo=","timed_out":false}' ;;
exec*"-- sleep 99") echo '{"code":null,"stdout":"","stderr":"","timed_out":true}' ;;
exec*"--on nowhere"*) echo '{"error":{"kind":"machine_unknown","message":"no machine nowhere"}}'; exit 1 ;;
*) echo "unknown: $*" >&2; exit 3 ;;
esac
`

func newFakeHC(t *testing.T) (*HC, func() []string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "hc")
	if err := os.WriteFile(bin, []byte(fakeHC), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	h := &HC{Bin: bin, Env: []string{"FAKE_HC_LOG=" + log, "FAKE_HC_SESSION=work"}}
	calls := func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	return h, calls
}

func TestHCMachines(t *testing.T) {
	h, calls := newFakeHC(t)
	ctx := context.Background()

	ms, err := h.MachineList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3 || ms[0].Name != "mac" || ms[0].Target != "me@mac" || ms[0].SinceMs != 5 || !ms[0].Usable() ||
		!ms[1].Temporary || ms[2].Usable() || ms[2].State != MachineNeedsUpdate {
		t.Fatalf("%+v", ms)
	}
	if v, err := h.Version(ctx); err != nil || v != "0.1.16" {
		t.Fatal(v, err)
	}

	if err := h.MachineAdd(ctx, "me@box"); err != nil {
		t.Fatal(err)
	}
	// Errors come on standard output or standard error, with their kind.
	err = h.MachineAdd(ctx, "new@box")
	var he *HCError
	if !errors.As(err, &he) || he.Kind != "host_key" || err.Error() != "the host key of box is not known" || !AddNeedsPerson(err) {
		t.Fatalf("%#v", err)
	}
	err = h.MachineAdd(ctx, "bad@box")
	if !errors.As(err, &he) || he.Kind != "unreachable" || AddNeedsPerson(err) {
		t.Fatalf("%#v", err)
	}

	if err := h.MachineRename(ctx, "mac", "laptop"); err != nil {
		t.Fatal(err)
	}
	// A failure without JSON is what hc printed.
	if err := h.MachineRemove(ctx, "gone"); err == nil || err.Error() != "no machine gone" {
		t.Fatalf("%v", err)
	}
	if v, err := h.MachineUpdate(ctx, "old"); err != nil || v != "0.1.16" {
		t.Fatal(v, err)
	}

	want := []string{
		"work|machine ls --json", "work|--version", "work|machine add me@box --json", "work|machine add new@box --json",
		"work|machine add bad@box --json", "work|machine rename mac laptop", "work|machine rm gone", "work|machine update old",
	}
	if got := calls(); !slices.Equal(got, want) {
		t.Fatalf("calls\n%q\nwant\n%q", got, want)
	}
	if argv, err := h.Argv("machine", "add", "new@box"); err != nil || len(argv) != 4 || argv[0] != h.Bin {
		t.Fatal(argv, err)
	}
}

func TestHCExec(t *testing.T) {
	h, calls := newFakeHC(t)
	ctx := context.Background()

	r, err := h.Exec(ctx, "mac", "/src/x", 20*time.Second, "git", "status")
	if err != nil || r.Code != 0 || string(r.Stdout) != " M a.go\x00" || len(r.Stderr) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if got := calls()[0]; got != "work|exec --json --on mac --timeout 20 --cwd /src/x -- git status" {
		t.Fatal(got)
	}
	// The program's exit code is no error.
	r, err = h.Exec(ctx, "mac", "", 0, "false")
	if err != nil || r.Code != 1 || string(r.Stderr) != "nope\n" {
		t.Fatalf("%+v %v", r, err)
	}
	if got := calls()[1]; got != "work|exec --json --on mac --timeout 30 -- false" {
		t.Fatal(got)
	}
	if _, err = h.Exec(ctx, "mac", "", time.Second, "sleep", "99"); !errors.Is(err, ErrExecTimedOut) {
		t.Fatalf("%v", err)
	}
	var he *HCError
	if _, err = h.Exec(ctx, "nowhere", "", 0, "true"); !errors.As(err, &he) || he.Kind != "machine_unknown" {
		t.Fatalf("%#v", err)
	}
	if _, err = h.Exec(ctx, "mac", "", 0); err == nil {
		t.Fatal("no program ran")
	}
}

func TestHCTimeout(t *testing.T) {
	h, _ := newFakeHC(t)
	start := time.Now()
	err := h.Run(context.Background(), 200*time.Millisecond, nil, "machine", "ls", "--json", "--slow")
	if err == nil || !strings.Contains(err.Error(), "took longer than") {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("waited for the stopped command", time.Since(start))
	}
}

func TestHCMissing(t *testing.T) {
	h := &HC{Bin: filepath.Join(t.TempDir(), "nope")}
	if _, err := h.MachineList(context.Background()); err == nil || !strings.Contains(err.Error(), "cannot run hc") {
		t.Fatalf("%v", err)
	}
}
