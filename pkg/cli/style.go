package cli

import (
	"fmt"
	"os"
	"strings"
)

var (
	enabled = true
)

func init() {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("SLMCODE_NO_COLOR") != "" {
		enabled = false
	}
}

func c(code, s string) string {
	if !enabled {
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
	return Accent(logo) + "\n" + Dim("  SLM-first coding harness · oMLX · atomic tasks · live kanban") + "\n" +
		Dim("  ") + Accent("https://unicolab.ai") + Dim(" · AI & Innovation") + "\n"
}

func Header(title string) {
	fmt.Println()
	fmt.Println(Bold(Accent("▸ " + title)))
	fmt.Println(Dim(strings.Repeat("─", min(60, len(title)+4))))
}

func KeyVal(k, v string) {
	fmt.Printf("  %s  %s\n", Dim(fmt.Sprintf("%-14s", k)), v)
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

// Clip truncates s for compact TUI/CLI lines.
func Clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if n <= 0 || len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
