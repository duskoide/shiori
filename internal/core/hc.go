package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HC runs herdr-core's command line. Machines are added, renamed, removed,
// updated and logged in to with it, and programs run on them, as herdr-app
// does: the core's RPC has these too, but the command is what herdr-app
// uses, and what knows how to ask a person for a password.
type HC struct {
	// Bin is the binary; "" finds it with FindBinary when first needed.
	Bin string
	// Env is added to the command's environment (HC_SESSION=…).
	Env []string
	// Timeout bounds a command that has no bound of its own (0 = HCTimeout).
	Timeout time.Duration

	once sync.Once
	bin  string
	err  error
}

const (
	// HCTimeout bounds an hc command by default.
	HCTimeout = 30 * time.Second
	// HCSlowTimeout bounds adding and updating a machine, which may copy
	// the core to it.
	HCSlowTimeout = 3 * time.Minute
)

// HCError is an error hc reported: {"error":{"kind","message"}}.
type HCError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (e *HCError) Error() string {
	if e.Message == "" {
		return "hc: " + e.Kind
	}
	return e.Message
}

// Machine states (machine.list).
const (
	MachineConnected   = "connected"
	MachineNeedsUpdate = "needs_update"
	MachineNeedsLogin  = "needs_login"
)

// Usable reports whether the machine is connected, so its terminals and
// document can be used.
func (m Machine) Usable() bool { return m.State == MachineConnected }

// Path is the hc binary the commands run.
func (h *HC) Path() (string, error) {
	h.once.Do(func() {
		h.bin, h.err = FindBinary(h.Bin)
	})
	return h.bin, h.err
}

// Argv is the command line of an hc command, for a terminal to run.
func (h *HC) Argv(args ...string) ([]string, error) {
	bin, err := h.Path()
	if err != nil {
		return nil, err
	}
	return append([]string{bin}, args...), nil
}

// Run runs an hc command and decodes its standard output into out (when
// not nil and there is output). A failure is an *HCError when hc said why.
func (h *HC) Run(ctx context.Context, timeout time.Duration, out any, args ...string) error {
	stdout, stderr, code, err := h.run(ctx, timeout, args)
	if err != nil {
		return err
	}
	if code != 0 {
		return hcFailure(args, stdout, stderr, code)
	}
	if out == nil || len(bytes.TrimSpace(stdout)) == 0 {
		return nil
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("hc %s: unexpected answer: %s", args[0], firstLine(stdout))
	}
	return nil
}

func (h *HC) run(ctx context.Context, timeout time.Duration, args []string) (stdout, stderr []byte, code int, err error) {
	bin, err := h.Path()
	if err != nil {
		return nil, nil, 0, err
	}
	if timeout <= 0 {
		timeout = h.Timeout
	}
	if timeout <= 0 {
		timeout = HCTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), h.Env...)
	cmd.WaitDelay = time.Second // a child that keeps the pipes open
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, nil, 0, fmt.Errorf("hc %s took longer than %s and was stopped", strings.Join(args[:min(2, len(args))], " "), timeout)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return o.Bytes(), e.Bytes(), exit.ExitCode(), nil
	}
	if err != nil {
		return nil, nil, 0, fmt.Errorf("cannot run hc: %w", err)
	}
	return o.Bytes(), e.Bytes(), 0, nil
}

// hcFailure is why a command failed: hc's error, on standard output or
// standard error, else what it printed.
func hcFailure(args []string, stdout, stderr []byte, code int) error {
	for _, b := range [][]byte{stdout, stderr} {
		var v struct {
			Error *HCError `json:"error"`
		}
		if json.Unmarshal(bytes.TrimSpace(b), &v) == nil && v.Error != nil {
			return v.Error
		}
	}
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = strings.TrimSpace(string(stdout))
	}
	if msg == "" {
		msg = fmt.Sprintf("hc %s exited with %d", args[0], code)
	}
	return &HCError{Message: msg}
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// MachineList lists the machines the core knows, temporary ones too.
func (h *HC) MachineList(ctx context.Context) ([]Machine, error) {
	var res struct {
		Machines []Machine `json:"machines"`
	}
	if err := h.Run(ctx, 0, &res, "machine", "ls", "--json"); err != nil {
		return nil, err
	}
	return res.Machines, nil
}

// MachineAdd adds the machine SSH reaches as target (user@host, or a host
// of ~/.ssh/config). When it fails for want of a person (AddNeedsPerson),
// run "hc machine add <target>" in a terminal instead.
func (h *HC) MachineAdd(ctx context.Context, target string) error {
	return h.Run(ctx, HCSlowTimeout, nil, "machine", "add", target, "--json")
}

// MachineRename renames a machine; addresses with the old name stop
// working.
func (h *HC) MachineRename(ctx context.Context, name, to string) error {
	return h.Run(ctx, 0, nil, "machine", "rename", name, to)
}

// MachineRemove forgets a machine; its terminals keep running there.
func (h *HC) MachineRemove(ctx context.Context, name string) error {
	return h.Run(ctx, 0, nil, "machine", "rm", name)
}

// MachineUpdate puts this core's version on the machine, and answers the
// version it has now ("" when hc did not say).
func (h *HC) MachineUpdate(ctx context.Context, name string) (string, error) {
	var res struct {
		Version string `json:"version"`
	}
	stdout, stderr, code, err := h.run(ctx, HCSlowTimeout, []string{"machine", "update", name})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", hcFailure([]string{"machine"}, stdout, stderr, code)
	}
	json.Unmarshal(bytes.TrimSpace(stdout), &res) // the version is a nicety
	return res.Version, nil
}

// Version is this hc's version, without the "hc " ("0.1.16").
func (h *HC) Version(ctx context.Context) (string, error) {
	stdout, _, code, err := h.run(ctx, 5*time.Second, []string{"--version"})
	if err != nil {
		return "", err
	}
	v, ok := strings.CutPrefix(strings.TrimSpace(string(stdout)), "hc ")
	if code != 0 || !ok {
		return "", errors.New("hc --version: unexpected answer")
	}
	v, _, _ = strings.Cut(v, " ")
	return v, nil
}

// addNeedsPerson are the kinds of error with which adding a machine needs
// someone at a terminal: a password, a host key, a core to install.
var (
	addNeedsPerson   = []string{"hc_missing", "incompatible", "login_needed", "auth_failed", "host_key"}
	addNeedsPersonRe = regexp.MustCompile(`(?i)password|passphrase|host key|permission denied|login`)
)

// AddNeedsPerson reports whether MachineAdd failed in a way a person at a
// terminal can get past, as herdr-app decides it.
func AddNeedsPerson(err error) bool {
	var e *HCError
	if !errors.As(err, &e) {
		return false
	}
	return slices.Contains(addNeedsPerson, e.Kind) || addNeedsPersonRe.MatchString(e.Message)
}

// ExecResult is what a program run on a machine did.
type ExecResult struct {
	Code   int
	Stdout []byte
	Stderr []byte
}

// ErrExecTimedOut reports a program stopped for taking too long.
var ErrExecTimedOut = errors.New("took too long and was stopped")

// Exec runs a program on a machine, in cwd there ("" is its home), for at
// most timeout (0 = HCTimeout), and answers what it did. Exit codes other
// than 0 are the program's, not errors.
func (h *HC) Exec(ctx context.Context, machine, cwd string, timeout time.Duration, argv ...string) (ExecResult, error) {
	if len(argv) == 0 {
		return ExecResult{}, errors.New("hc exec: no program")
	}
	if timeout <= 0 {
		timeout = HCTimeout
	}
	secs := max(int((timeout+time.Second-1)/time.Second), 1)
	args := []string{"exec", "--json", "--on", machine, "--timeout", strconv.Itoa(secs)}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	args = append(append(args, "--"), argv...)
	// hc stops the program at its timeout; this one is for hc itself.
	stdout, stderr, code, err := h.run(ctx, timeout+10*time.Second, args)
	if err != nil {
		return ExecResult{}, err
	}
	var v struct {
		Error    *HCError `json:"error"`
		Code     *int     `json:"code"`
		Stdout   string   `json:"stdout"`
		Stderr   string   `json:"stderr"`
		TimedOut bool     `json:"timed_out"`
	}
	if json.Unmarshal(bytes.TrimSpace(stdout), &v) != nil && json.Unmarshal(bytes.TrimSpace(stderr), &v) != nil {
		return ExecResult{}, hcFailure(args, stdout, stderr, code)
	}
	if v.Error != nil {
		return ExecResult{}, v.Error
	}
	if v.TimedOut {
		return ExecResult{}, fmt.Errorf("%s on %s %w after %ds", argv[0], machine, ErrExecTimedOut, secs)
	}
	r := ExecResult{Code: -1}
	if v.Code != nil {
		r.Code = *v.Code
	}
	if r.Stdout, err = base64.StdEncoding.DecodeString(v.Stdout); err != nil {
		return ExecResult{}, fmt.Errorf("hc exec: its output: %w", err)
	}
	if r.Stderr, err = base64.StdEncoding.DecodeString(v.Stderr); err != nil {
		return ExecResult{}, fmt.Errorf("hc exec: its errors: %w", err)
	}
	return r, nil
}
