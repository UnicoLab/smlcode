//go:build !windows

package workspace

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so the whole tree
// (bash -c → go test → compiled test binary) can be signalled at once.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup kills the child's entire process group. Falls back to
// killing just the child when the group id is unavailable.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 0 && pgid != 1 {
		// Negative pid ⇒ signal the whole group.
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			return nil
		}
	}
	return cmd.Process.Kill()
}
