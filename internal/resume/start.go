package resume

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// clockTicks is Linux's USER_HZ, the unit of /proc/<pid>/stat's times: 100
// on every architecture Go runs on.
const clockTicks = 100

// StartMs is when process pid started, in ms, 0 if it cannot be told.
func (e Env) StartMs(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	if e.Proc != "" {
		return procStart(e.Proc, pid)
	}
	return psStart(pid)
}

// procStart reads Linux's /proc: the start in ticks since boot, and the
// boot.
func procStart(proc string, pid int) int64 {
	b, err := os.ReadFile(filepath.Join(proc, itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	// The command, in parentheses, may hold spaces and parentheses.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b[i+1:]))
	// Field 22 of the line, the state (3) being f[0].
	if len(f) < 20 {
		return 0
	}
	ticks, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil {
		return 0
	}
	st, err := os.ReadFile(filepath.Join(proc, "stat"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(st), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			boot, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return 0
			}
			return boot*1000 + ticks*1000/clockTicks
		}
	}
	return 0
}

// psStart asks ps, as on macOS: to the second.
func psStart(pid int) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	// Fields collapses the padding of a one-digit day.
	t, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(strings.Fields(string(out)), " "), time.Local)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}
