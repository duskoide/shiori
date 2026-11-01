//go:build unix

package app

import (
	"os/exec"
	"syscall"
	"time"
)

// ownGroup runs cmd in a process group of its own, so that ending it ends
// what it started too.
// It is for a command made with exec.CommandContext.
func ownGroup(cmd *exec.Cmd) {
	setpgid(cmd)
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

// setpgid runs cmd in a process group of its own, which signalGroup
// reaches.
func setpgid(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A child that keeps the output open does not hold the wait up.
	cmd.WaitDelay = 2 * time.Second
}

// signalGroup sends sig to cmd's process group.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, sig)
	}
}
