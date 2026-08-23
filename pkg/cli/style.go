package cli

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"golang.org/x/term"
)

// ColorMode is the tri-state resolution policy for ANSI output.
type ColorMode string

const (
	ColorAuto   ColorMode = "auto"
	ColorAlways ColorMode = "always"
	ColorNever  ColorMode = "never"
)

// enabled is read on every styling call, so it is an atomic to stay safe when
// a background goroutine renders while the main goroutine toggles it.
var enabled atomic.Bool

func init() {
	SetColorMode(ColorAuto)
}

// ParseColorMode maps a --color flag value onto a ColorMode.
func ParseColorMode(s string) (ColorMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto", "tty":
		return ColorAuto, nil
	case "always", "force", "yes", "on", "1", "true":
		return ColorAlways, nil
	case "never", "none", "no", "off", "0", "false":
		return ColorNever, nil
	default:
		return ColorAuto, fmt.Errorf("invalid --color %q (want auto|always|never)", s)
	}
}

// SetColorMode applies a color policy. In auto mode color is emitted only when
// stdout is a terminal, TERM is usable, and NO_COLOR is unset — so
// `slmcode status | cat` and redirects to files stay clean.
func SetColorMode(mode ColorMode) {
	switch mode {
	case ColorAlways:
		enabled.Store(true)
	case ColorNever:
		enabled.Store(false)
	default:
		enabled.Store(autoColor())
	}
}

// ColorEnabled reports the resolved state.
func ColorEnabled() bool { return enabled.Load() }

func autoColor() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("SLMCODE_NO_COLOR") != "" {
		return false
	}
	if v := os.Getenv("FORCE_COLOR"); v != "" && v != "0" && v != "false" {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func c(code, s string) string {
	if !enabled.Load() {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func Bold(s string) string   { return c("1", s) }
func Dim(s string) string    { return c("2", s) }
func Cyan(s string) string   { return c("36", s) }
func Green(s string) string  { return c("32", s) }
func Yellow(s string) string { return c("33", s) }
func Red(s string) string    { return c("31", s) }
func Magenta(s string) string {
	return c("35", s)
}
func Blue(s string) string { return c("34", s) }
func White(s string) string {
	return c("97", s)
}
func Accent(s string) string { return c("38;5;214", s) } // amber

// Reverse renders reverse-video, used for intra-line diff highlighting.
func Reverse(s string) string { return c("7", s) }

func Success(s string) string { return Green("✔ " + s) }
func Warn(s string) string    { return Yellow("⚠ " + s) }
func Error(s string) string   { return Red("✖ " + s) }
func Info(s string) string    { return Cyan("→ " + s) }

func Banner() string {
	logo := `
   ███████╗██╗     ███╗   ███╗ ██████╗ ██████╗ ██████╗ ███████╗
   ██╔════╝██║     ████╗ ████║██╔════╝██╔═══██╗██╔══██╗██╔════╝
   ███████╗██║     ██╔████╔██║██║     ██║   ██║██║  ██║█████╗
   ╚════██║██║     ██║╚██╔╝██║██║     ██║   ██║██║  ██║██╔══╝
   ███████║███████╗██║ ╚═╝ ██║╚██████╗╚██████╔╝██████╔╝███████╗
   ╚══════╝╚══════╝╚═╝     ╚═╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝`
	tagline := Dim("  SLM-first coding harness · oMLX · atomic tasks · live kanban") + "\n"
	brand := "  " +
		Magenta("♥") + " " +
		Blue("m") + Cyan("a") + Blue("d") + Cyan("e") + " " +
		Blue("w") + Cyan("i") + Blue("t") + Cyan("h") + " " +
		Magenta("♥") + " " + Blue("b") + Cyan("y") + " " +
		Bold(Cyan("UnicoLab")) +
		Dim("  —  ") +
		Bold(Magenta("AI")) + " " + Dim("&") + " " + Bold(Blue("Innovation")) +
		"\n"
	return Accent(logo) + "\n" + tagline + brand
}

func Header(title string) {
	fmt.Println()
	fmt.Println(Bold(Accent("▸ " + title)))
	fmt.Println(Dim(strings.Repeat("─", min(60, StringWidth(title)+4))))
}

func KeyVal(k, v string) {
	fmt.Printf("  %s  %s\n", Dim(PadMinWidth(k, 14)), v)
}

func ColumnColor(col string) string {
	switch col {
	case "to_scope":
		return Dim(col)
	case "scoped":
		return Blue(col)
	case "ready_to_dev":
		return Cyan(col)
	case "in_progress":
		return Yellow(col)
	case "in_review":
		return Magenta(col)
	case "done":
		return Green(col)
	case "blocked":
		return Red(col)
	default:
		return col
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Clip collapses whitespace and truncates s to n display cells for compact
// one-line CLI output. Width is rune-aware and ANSI-aware.
func Clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if n <= 0 {
		return s
	}
	return ClipWidth(s, n)
}
