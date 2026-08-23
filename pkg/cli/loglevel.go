package cli

import (
	"strings"
	"sync/atomic"

	"github.com/UnicoLab/slmcode/pkg/stream"
)

// Render verbosity. The CLI is the single renderer: the engine emits events and
// pkg/cli decides what reaches the terminal. `--verbose` used to print from
// inside the engine as well, so every line appeared twice.

// LogLevel orders render verbosity.
type LogLevel int32

const (
	LogError LogLevel = iota
	LogWarn
	LogInfo
	LogDebug
)

var logLevel atomic.Int32

func init() { logLevel.Store(int32(LogInfo)) }

// ParseLogLevel maps a --log-level value onto a LogLevel.
func ParseLogLevel(s string) (LogLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error", "err", "quiet":
		return LogError, true
	case "warn", "warning":
		return LogWarn, true
	case "", "info", "normal":
		return LogInfo, true
	case "debug", "trace", "verbose":
		return LogDebug, true
	}
	return LogInfo, false
}

// SetLogLevel sets the global render verbosity.
func SetLogLevel(l LogLevel) { logLevel.Store(int32(l)) }

// CurrentLogLevel returns the active verbosity.
func CurrentLogLevel() LogLevel { return LogLevel(logLevel.Load()) }

// String renders the level name.
func (l LogLevel) String() string {
	switch l {
	case LogError:
		return "error"
	case LogWarn:
		return "warn"
	case LogDebug:
		return "debug"
	default:
		return "info"
	}
}

// ShouldRender decides whether an event reaches the terminal at the current
// verbosity.
func ShouldRender(e stream.Event) bool {
	lvl := CurrentLogLevel()
	switch e.Level {
	case stream.LevelError, stream.LevelProblem:
		return true // errors always surface
	case stream.LevelWarn:
		return lvl >= LogWarn
	}
	switch e.Kind {
	case stream.KindDebug, stream.KindTool:
		return lvl >= LogDebug
	case stream.KindOutput, stream.KindLatency, stream.KindUsage, stream.KindToken:
		return lvl >= LogInfo
	default:
		return lvl >= LogWarn
	}
}
