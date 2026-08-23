//go:build !windows

package cli

import (
	"os"
	"os/signal"
	"syscall"
)

// NotifyResize delivers a token on the returned channel whenever the terminal
// is resized. The returned stop function deregisters the handler.
func NotifyResize() (<-chan struct{}, func()) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGWINCH)
	out := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigc:
				select {
				case out <- struct{}{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()
	return out, func() {
		signal.Stop(sigc)
		close(done)
	}
}
