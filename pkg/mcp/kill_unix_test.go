//go:build !windows

package mcp

import "syscall"

// syscallKill0 probes whether a pid is still alive.
func syscallKill0(pid int) error { return syscall.Kill(pid, 0) }
