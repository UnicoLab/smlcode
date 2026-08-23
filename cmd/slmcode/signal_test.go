package main

import (
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestSignalContextCancelsOnFirstSignal covers the first half of the fix: one
// SIGINT cancels the run (and prints the "press Ctrl-C again" hint). The
// force-quit half calls os.Exit(130) and so cannot be exercised in-process.
func TestSignalContextCancelsOnFirstSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no SIGINT delivery on Windows")
	}
	ctx, cancel := signalContext()
	defer cancel()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Skipf("cannot self-signal: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("SIGINT did not cancel the context")
	}
}

// TestSignalContextCancelFuncDeregisters proves the leak is gone: after cancel,
// the watcher goroutine exits, so repeated run setups do not accumulate one
// goroutine and one signal registration each.
func TestSignalContextCancelFuncDeregisters(t *testing.T) {
	settle := func() int {
		for i := 0; i < 50; i++ {
			runtime.Gosched()
			time.Sleep(5 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()

	for i := 0; i < 25; i++ {
		_, cancel := signalContext()
		cancel()
	}
	after := settle()

	if after > before+5 {
		t.Fatalf("goroutines leaked: before=%d after=%d", before, after)
	}
}

func TestSignalContextCancelIsIdempotent(t *testing.T) {
	_, cancel := signalContext()
	cancel()
	cancel() // must not panic on a double close
	cancel()
}
