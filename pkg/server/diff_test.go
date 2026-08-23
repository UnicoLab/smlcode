package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/session"
)

func TestComputeDiffBasics(t *testing.T) {
	d := ComputeDiff("a\nb\nc\n", "a\nB\nc\n", 1)
	if d.Stat.Added != 1 || d.Stat.Removed != 1 {
		t.Fatalf("stat=%+v", d.Stat)
	}
	if len(d.Hunks) != 1 {
		t.Fatalf("hunks=%d", len(d.Hunks))
	}
	var kinds []string
	for _, op := range d.Hunks[0].Ops {
		kinds = append(kinds, op.Type)
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "delete") || !strings.Contains(joined, "insert") {
		t.Fatalf("ops=%s", joined)
	}
	// Line numbers must be usable for a side-by-side render.
	for _, op := range d.Hunks[0].Ops {
		switch op.Type {
		case "delete":
			if op.OldLine == 0 || op.NewLine != 0 {
				t.Fatalf("delete line numbers wrong: %+v", op)
			}
		case "insert":
			if op.NewLine == 0 || op.OldLine != 0 {
				t.Fatalf("insert line numbers wrong: %+v", op)
			}
		}
	}
}

func TestComputeDiffNewAndUnchangedFiles(t *testing.T) {
	d := ComputeDiff("", "one\ntwo\n", 3)
	if d.Stat.Added != 2 || d.Stat.Removed != 0 {
		t.Fatalf("new-file stat=%+v", d.Stat)
	}
	d = ComputeDiff("same\n", "same\n", 3)
	if d.Stat.Added != 0 || d.Stat.Removed != 0 || len(d.Hunks) != 0 {
		t.Fatalf("identical files produced a diff: %+v", d)
	}
	d = ComputeDiff("x\x00y", "z", 3)
	if !d.Stat.Binary {
		t.Fatal("binary content not detected")
	}
}

func TestComputeDiffCRLFNormalised(t *testing.T) {
	d := ComputeDiff("a\r\nb\r\n", "a\nb\n", 3)
	if d.Stat.Added != 0 || d.Stat.Removed != 0 {
		t.Fatalf("line-ending-only change reported as a diff: %+v", d.Stat)
	}
}

func TestBuildTracePhases(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(sec int) string { return base.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano) }
	events := []session.EventRecord{
		{Time: at(0), Phase: "plan", Kind: "phase", Agent: "planner"},
		{Time: at(4), Phase: "plan", Kind: "usage", Tokens: 120, CostUSD: 0.002, Model: "qwen"},
		{Time: at(6), Phase: "execute", Kind: "phase", Agent: "worker"},
		{Time: at(9), Phase: "execute", Kind: "tool", Agent: "worker"},
		{Time: at(20), Phase: "execute", Kind: "usage", Tokens: 400, CostUSD: 0.01},
	}
	phases, totals := BuildTrace(events)
	if len(phases) != 2 {
		t.Fatalf("phases=%d (%+v)", len(phases), phases)
	}
	if phases[0].Phase != "plan" || phases[0].DurationMS != 4000 || phases[0].Tokens != 120 {
		t.Fatalf("plan phase=%+v", phases[0])
	}
	if phases[1].Phase != "execute" || phases[1].DurationMS != 14000 || phases[1].Tools != 1 {
		t.Fatalf("execute phase=%+v", phases[1])
	}
	if totals.Tokens != 520 || totals.DurationMS != 20000 || totals.Phases != 2 {
		t.Fatalf("totals=%+v", totals)
	}
	if len(phases[1].Agents) != 1 || phases[1].Agents[0] != "worker" {
		t.Fatalf("agents=%v", phases[1].Agents)
	}
}

func TestBuildTraceEmpty(t *testing.T) {
	phases, totals := BuildTrace(nil)
	if len(phases) != 0 || totals.Events != 0 {
		t.Fatalf("phases=%v totals=%+v", phases, totals)
	}
}

func TestQueryTraceEndpoint(t *testing.T) {
	h := newHarness(t)
	s := New(h, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newAPIRequest(http.MethodGet, "/api/queries/does-not-exist/trace", nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["phases"]; !ok {
		t.Fatalf("no phases key: %s", rec.Body.String())
	}
	if _, ok := out["totals"]; !ok {
		t.Fatalf("no totals key: %s", rec.Body.String())
	}
}
