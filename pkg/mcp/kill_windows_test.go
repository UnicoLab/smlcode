//go:build windows

package mcp

import "fmt"

// syscallKill0 is unsupported on Windows; the leak check is skipped there.
func syscallKill0(pid int) error { return fmt.Errorf("unsupported") }
