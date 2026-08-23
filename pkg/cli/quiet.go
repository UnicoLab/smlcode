package cli

import (
	"bufio"
	"io"
	"os"
	"strings"
	"sync"
)

// QuietStderr suppresses dependency log noise during a noisy construction step.
//
// GoLangGraph creates *private* logrus loggers (logrus.New() inside its own
// packages) that write to os.Stderr at Info level, so logrus.SetLevel on the
// standard logger is a no-op and there is no handle to reach them. The only
// portable lever is os.Stderr itself: swap it for a pipe, run fn, then replay
// only the lines that actually matter (warn/error/fatal/panic) through emit.
//
// Safety rules this implements:
//   - os.Stderr is restored on every path, including panic (which is re-raised
//     after the captured output has been flushed, so a crash is never hidden).
//   - When emit is nil, warn/error lines still reach the real stderr.
//   - Set SLMCODE_NO_QUIET=1 (or pass enabled=false) to opt out entirely.
func QuietStderr(fn func(), emit func(level, line string)) {
	QuietStderrIf(quietEnabled(), fn, emit)
}

// quietEnabled reports whether stderr capture is active.
func quietEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SLMCODE_NO_QUIET")))
	switch v {
	case "1", "true", "yes", "on":
		return false
	}
	return true
}

// QuietStderrIf is QuietStderr with an explicit on/off switch.
func QuietStderrIf(enabled bool, fn func(), emit func(level, line string)) {
	if fn == nil {
		return
	}
	if !enabled {
		fn()
		return
	}
	r, w, err := os.Pipe()
	if err != nil {
		fn()
		return
	}
	real := os.Stderr
	os.Stderr = w

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		drainStderr(r, real, emit)
	}()

	restore := func() {
		os.Stderr = real
		_ = w.Close()
		wg.Wait()
		_ = r.Close()
	}

	defer func() {
		if rec := recover(); rec != nil {
			restore()
			panic(rec)
		}
		restore()
	}()
	fn()
}

// drainStderr reads captured output and forwards only significant lines.
func drainStderr(r io.Reader, real io.Writer, emit func(level, line string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		level := LogLineLevel(line)
		if level == "" {
			continue // routine info/debug chatter from the dependency
		}
		if emit != nil {
			emit(level, line)
			continue
		}
		_, _ = io.WriteString(real, line+"\n")
	}
}

// LogLineLevel classifies a captured stderr line. It returns "" for lines that
// are safe to drop (info/debug/trace and unlabelled chatter) and "warning" or
// "error" for lines a user must see.
func LogLineLevel(line string) string {
	l := strings.ToLower(line)
	switch {
	case strings.Contains(l, "level=fatal"), strings.Contains(l, "level=panic"),
		strings.Contains(l, "level=error"):
		return "error"
	case strings.Contains(l, "level=warn"):
		return "warning"
	case strings.Contains(l, "level=info"), strings.Contains(l, "level=debug"),
		strings.Contains(l, "level=trace"):
		return ""
	}
	// Unlabelled output: keep anything that looks like a crash, drop the rest.
	if strings.Contains(l, "panic:") || strings.Contains(l, "goroutine ") ||
		strings.Contains(l, "fatal error:") {
		return "error"
	}
	return ""
}
