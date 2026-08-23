//go:build windows

package workspace

import (
	"os/exec"
	"syscall"
)

// setProcessGroup asks Windows to create a new process group for the child so
// that killing it does not take down the harness. Windows has no Setpgid; the
// closest equivalent is CREATE_NEW_PROCESS_GROUP.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// killProcessGroup terminates the child. Windows does not reliably propagate a
// kill to grandchildren, so this is best-effort — cmd.WaitDelay still bounds
// the wait so the harness cannot hang on an orphan holding the pipes open.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
