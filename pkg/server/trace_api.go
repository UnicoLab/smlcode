package server

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/session"
)

// ── Run trace (capability C) ──
//
// .slmcode/queries/<id>/ already holds a full event log, but Studio could only
// show it as a flat list. TracePhase turns it into a replayable timeline with
// per-phase wall time and token/cost attribution — the numbers that matter when
// tuning a small local model.

// TracePhase is one contiguous phase segment of a recorded run.
type TracePhase struct {
	Phase      string   `json:"phase"`
	StartedAt  string   `json:"started_at,omitempty"`
	EndedAt    string   `json:"ended_at,omitempty"`
	DurationMS int64    `json:"duration_ms"`
	Events     int      `json:"events"`
	Tokens     int      `json:"tokens,omitempty"`
	CostUSD    float64  `json:"cost_usd,omitempty"`
	Agents     []string `json:"agents,omitempty"`
	Models     []string `json:"models,omitempty"`
	Tools      int      `json:"tools,omitempty"`
	Errors     int      `json:"errors,omitempty"`
	Warnings   int      `json:"warnings,omitempty"`
	Message    string   `json:"message,omitempty"`
}

// TraceTotals aggregates a whole run.
type TraceTotals struct {
	DurationMS int64   `json:"duration_ms"`
	Events     int     `json:"events"`
	Tokens     int     `json:"tokens"`
	CostUSD    float64 `json:"cost_usd"`
	Phases     int     `json:"phases"`
	Errors     int     `json:"errors"`
	Warnings   int     `json:"warnings"`
}

// handleQueryTrace — GET /api/queries/{id}/trace
func (s *Server) handleQueryTrace(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "invalid query id", 400)
		return
	}
	limit := intParamUnbounded(r, "limit", 20000)
	events, err := session.ReadEvents(s.slmDir(), id, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	phases, totals := BuildTrace(events)
	out := map[string]any{
		"id":      id,
		"phases":  phases,
		"totals":  totals,
		"summary": session.AnalyzeEvents(events),
	}
	if t, err := session.LoadTurn(s.slmDir(), id); err == nil {
		out["query"] = t.Query
		out["success"] = t.Success
		out["updated_at"] = t.UpdatedAt
		out["interrupted"] = t.Interrupted
	}
	writeJSON(w, out)
}

// BuildTrace groups an event log into contiguous phase segments.
func BuildTrace(events []session.EventRecord) ([]TracePhase, TraceTotals) {
	phases := make([]TracePhase, 0, 16)
	totals := TraceTotals{}
	if len(events) == 0 {
		return phases, totals
	}

	var cur *TracePhase
	var curAgents, curModels map[string]struct{}
	var firstTime, lastTime time.Time

	flush := func() {
		if cur == nil {
			return
		}
		cur.Agents = sortedKeys(curAgents)
		cur.Models = sortedKeys(curModels)
		phases = append(phases, *cur)
		cur = nil
	}

	for _, ev := range events {
		ts, hasTS := parseEventTime(ev.Time)
		if hasTS {
			if firstTime.IsZero() || ts.Before(firstTime) {
				firstTime = ts
			}
			if ts.After(lastTime) {
				lastTime = ts
			}
		}
		name := ev.Phase
		if name == "" {
			name = "unknown"
		}
		if cur == nil || cur.Phase != name {
			flush()
			cur = &TracePhase{Phase: name}
			curAgents = map[string]struct{}{}
			curModels = map[string]struct{}{}
			if hasTS {
				cur.StartedAt = ts.UTC().Format(time.RFC3339Nano)
			}
		}
		cur.Events++
		totals.Events++
		if hasTS {
			cur.EndedAt = ts.UTC().Format(time.RFC3339Nano)
			if start, ok := parseEventTime(cur.StartedAt); ok {
				cur.DurationMS = ts.Sub(start).Milliseconds()
			}
		}
		if ev.Agent != "" {
			curAgents[ev.Agent] = struct{}{}
		}
		if ev.Model != "" {
			curModels[ev.Model] = struct{}{}
		}
		if ev.Tokens > 0 {
			cur.Tokens += ev.Tokens
			totals.Tokens += ev.Tokens
		}
		if ev.CostUSD > 0 {
			cur.CostUSD += ev.CostUSD
			totals.CostUSD += ev.CostUSD
		}
		switch ev.Kind {
		case "tool":
			cur.Tools++
		}
		switch strings.ToLower(kindLevel(ev)) {
		case "error", "problem":
			cur.Errors++
			totals.Errors++
		case "warning":
			cur.Warnings++
			totals.Warnings++
		}
		if ev.Message != "" {
			cur.Message = ev.Message
		}
	}
	flush()

	totals.Phases = len(phases)
	if !firstTime.IsZero() && lastTime.After(firstTime) {
		totals.DurationMS = lastTime.Sub(firstTime).Milliseconds()
	}
	return phases, totals
}

// kindLevel extracts a level from an event record. session.EventRecord has no
// Level field today; the level travels inside Data for engine-emitted events,
// so this reads it defensively and degrades to "" when absent.
func kindLevel(ev session.EventRecord) string {
	if m, ok := ev.Data.(map[string]any); ok {
		if lv, ok := m["level"].(string); ok {
			return lv
		}
	}
	switch ev.Kind {
	case "error":
		return "error"
	}
	if strings.HasPrefix(strings.ToLower(ev.Message), "error") {
		return "error"
	}
	return ""
}

func parseEventTime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func intParamUnbounded(r *http.Request, key string, def int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 1_000_000
		}
	}
	if n <= 0 {
		return def
	}
	return n
}
