// Package refine turns post-wave lessons into durable knowledge (prime-agent refine loop).
package refine

import (
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/learning"
)

// Input is one refine cycle after a wave or run.
type Input struct {
	Query    string
	Lessons  []learning.Lesson
	WaveNote string
	Round    int
}

// Output is markdown to append to CONTEXT / knowledge.
type Output struct {
	Markdown string
	Skip     bool
	Reason   string
}

// Build synthesizes a short refine note from lessons (no LLM required).
func Build(in Input) Output {
	if len(in.Lessons) == 0 && strings.TrimSpace(in.WaveNote) == "" {
		return Output{Skip: true, Reason: "no lessons"}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Refine %s (round %d)\n\n", time.Now().Format("15:04:05"), in.Round)
	if q := strings.TrimSpace(in.Query); q != "" {
		b.WriteString("Query: " + firstLine(q, 160) + "\n\n")
	}
	if note := strings.TrimSpace(in.WaveNote); note != "" {
		b.WriteString(note + "\n\n")
	}
	if md := learning.RenderMarkdown(in.Lessons); strings.TrimSpace(md) != "" {
		b.WriteString(md)
		b.WriteString("\n")
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return Output{Skip: true, Reason: "empty"}
	}
	return Output{Markdown: out}
}

// ShouldRun gates auto-refine by config-like knobs.
func ShouldRun(enabled bool, maxRounds, round int, lessons int) bool {
	if !enabled {
		return false
	}
	if maxRounds <= 0 {
		maxRounds = 2
	}
	if round > maxRounds {
		return false
	}
	return lessons > 0
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
