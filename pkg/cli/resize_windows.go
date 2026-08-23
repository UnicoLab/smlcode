//go:build windows

package cli

// NotifyResize is a no-op on Windows, which has no SIGWINCH.
func NotifyResize() (<-chan struct{}, func()) {
	out := make(chan struct{})
	return out, func() {}
}
