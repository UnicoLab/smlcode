package cli

import (
	"bufio"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
// are safe to drop (info/debug/trace and unlabeled chatter) and "warning" or
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
	// Unlabeled output: keep anything that looks like a crash, drop the rest.
	if strings.Contains(l, "panic:") || strings.Contains(l, "goroutine ") ||
		strings.Contains(l, "fatal error:") {
		return "error"
	}
	return ""
}

// FilterStderr installs a process-wide stderr filter for the lifetime of fn.
//
// QuietStderr solves the *construction* burst; this solves the rest of the run.
// GoLangGraph builds a fresh private logrus logger for every agent it creates,
// and logrus.New() captures the current os.Stderr, so an agent constructed
// mid-run writes Info records straight into the user's transcript — a `slmcode
// run` used to interleave ~40 `time=… level=info msg="Executing node"` lines
// with the rendered phase output, and the TUI's boxes were shredded by them.
//
// The filter differs from QuietStderr in one important way: it is a
// PASS-THROUGH. Only lines that are recognizably a dependency info/debug/trace
// log record are dropped; everything else — the CLI's own "✖ …", a panic
// trace, an unlabeled write from any library — reaches the real stderr
// untouched. That is what makes it safe to wrap a whole command in.
//
// It is a no-op when the filter would hide something the user asked for:
// SLMCODE_NO_QUIET=1 skips the pipe entirely, and --log-level=debug passes
// every line through — checked per line, because FilterStderr wraps flag
// parsing and so runs before --log-level is known.
func FilterStderr(fn func()) {
	if fn == nil {
		return
	}
	if !quietEnabled() {
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
	realStderr.Store(real)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		passThroughStderr(r, real)
	}()

	restore := func() {
		os.Stderr = real
		realStderr.Store((*os.File)(nil))
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

// realStderr holds the process's true stderr while FilterStderr has os.Stderr
// pointed at its pipe.
var realStderr atomic.Pointer[os.File]

// Stderr is the writer to use for anything that must reach the user even when
// the process is about to exit without unwinding — a signal handler's "force
// quit", the final error line. Writing those through the filter pipe risks
// losing them: os.Exit never runs the deferred drain.
func Stderr() *os.File {
	if f := realStderr.Load(); f != nil {
		return f
	}
	return os.Stderr
}

// passThroughStderr copies r to real, dropping dependency chatter.
func passThroughStderr(r io.Reader, real io.Writer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Per line, not once up front: this filter wraps command dispatch, so
		// --log-level is not parsed yet when the goroutine starts.
		if CurrentLogLevel() < LogDebug && IsDependencyChatter(line) {
			continue
		}
		_, _ = io.WriteString(real, line+"\n")
	}
}

// IsDependencyChatter reports whether a stderr line is a routine info/debug
// record from a logging library rather than something the user needs.
//
// Both logrus text layouts are matched, because the dependency emits each in a
// different place: the logfmt form (`time="…" level=info msg="…"`) when stderr
// is a pipe, and the colored form (`INFO[0012] …`) when it is a terminal.
// Anything else — including warnings, errors and every unlabeled line — is
// NOT chatter and must survive.
func IsDependencyChatter(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	for _, prefix := range []string{"INFO[", "DEBU[", "TRAC["} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	l := strings.ToLower(line)
	if !strings.Contains(l, "level=") {
		return false
	}
	return strings.Contains(l, "level=info") ||
		strings.Contains(l, "level=debug") ||
		strings.Contains(l, "level=trace")
}
