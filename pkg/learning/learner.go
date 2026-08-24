package learning

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/plan"
)

// Lesson is a durable takeaway for MEMORY.md / future packs.
type Lesson struct {
	TaskID string `json:"task_id"`
	Kind   string `json:"kind"` // success | failure | convention
	Text   string `json:"text"`
	At     string `json:"at"`
}

// Extract pulls short lessons from a finished/blocked task for memory evolution.
func Extract(t plan.Task) []Lesson {
	var out []Lesson
	at := time.Now().Format(time.RFC3339)
	t.Normalize()

	switch t.Column {
	case plan.ColDone:
		if s := firstSentence(t.Output); s != "" {
			out = append(out, Lesson{
				TaskID: t.ID, Kind: "success", At: at,
				Text: fmt.Sprintf("%s (%s): %s", t.ID, t.Title, s),
			})
		}
		if t.Acceptance != "" {
			out = append(out, Lesson{
				TaskID: t.ID, Kind: "convention", At: at,
				Text: fmt.Sprintf("Acceptance pattern that worked for %s: %s", t.ID, firstSentence(t.Acceptance)),
			})
		}
	case plan.ColBlocked, plan.ColToScope, plan.ColScoped:
		msg := t.Error
		if msg == "" {
			msg = firstSentence(t.Review)
		}
		if msg != "" {
			out = append(out, Lesson{
				TaskID: t.ID, Kind: "failure", At: at,
				Text: fmt.Sprintf("Avoid repeating %s failure: %s", t.ID, msg),
			})
		}
	}

	if t.Notes != "" && t.Column == plan.ColDone {
		out = append(out, Lesson{
			TaskID: t.ID, Kind: "convention", At: at,
			Text: fmt.Sprintf("Human note honored on %s: %s", t.ID, firstSentence(t.Notes)),
		})
	}
	return out
}

// RenderMarkdown turns lessons into MEMORY.md bullets.
func RenderMarkdown(lessons []Lesson) string {
	if len(lessons) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lessons {
		prefix := "•"
		switch l.Kind {
		case "failure":
			prefix = "⚠"
		case "convention":
			prefix = "⚙"
		case "success":
			prefix = "✓"
		}
		fmt.Fprintf(&b, "- %s %s\n", prefix, l.Text)
	}
	return b.String()
}

// ContextDelta builds a short CONTEXT.md append after a wave.
func ContextDelta(wave []plan.Task) string {
	var done, blocked, needsScope, progress []string
	for _, t := range wave {
		t.Normalize()
		switch t.Column {
		case plan.ColDone:
			done = append(done, t.ID+": "+t.Title)
		case plan.ColBlocked:
			blocked = append(blocked, t.ID+": "+firstSentence(t.Error+t.Review))
		case plan.ColToScope, plan.ColScoped:
			if strings.TrimSpace(t.Error) != "" {
				needsScope = append(needsScope, t.ID+": "+firstSentence(t.Error+t.Review))
			}
		case plan.ColInProgress, plan.ColInReview:
			progress = append(progress, t.ID)
		}
	}
	var b strings.Builder
	b.WriteString("### Wave update\n\n")
	if len(done) > 0 {
		b.WriteString("**Completed:** " + strings.Join(done, "; ") + "\n\n")
	}
	if len(blocked) > 0 {
		b.WriteString("**Blocked:** " + strings.Join(blocked, "; ") + "\n\n")
	}
	if len(needsScope) > 0 {
		b.WriteString("**Needs scope / decision:** " + strings.Join(needsScope, "; ") + "\n\n")
	}
	if len(progress) > 0 {
		b.WriteString("**Still active:** " + strings.Join(progress, ", ") + "\n\n")
	}
	return b.String()
}

// JSONLessonsToMarkdown parses SLM learner JSON into MEMORY bullets.
// Accepts either {"lessons":[...]} or a raw array.
func JSONLessonsToMarkdown(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Extract JSON object/array if wrapped in prose/fences
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			raw = raw[i : j+1]
		}
	} else if i := strings.Index(raw, "["); i >= 0 {
		if j := strings.LastIndex(raw, "]"); j > i {
			raw = raw[i : j+1]
		}
	}

	type item struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	var wrap struct {
		Lessons []item `json:"lessons"`
	}
	var arr []item
	if err := json.Unmarshal([]byte(raw), &wrap); err == nil && len(wrap.Lessons) > 0 {
		arr = wrap.Lessons
	} else if err := json.Unmarshal([]byte(raw), &arr); err != nil || len(arr) == 0 {
		return ""
	}

	var lessons []Lesson
	at := time.Now().Format(time.RFC3339)
	for _, it := range arr {
		text := strings.TrimSpace(it.Text)
		if text == "" {
			continue
		}
		kind := strings.TrimSpace(it.Kind)
		if kind == "" {
			kind = "convention"
		}
		lessons = append(lessons, Lesson{Kind: kind, Text: text, At: at})
	}
	return RenderMarkdown(lessons)
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, ".!"); i > 0 && i < 180 {
		return strings.TrimSpace(s[:i+1])
	}
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
