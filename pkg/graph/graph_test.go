package graph

import (
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func TestNodeConstructors(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"file", FileNode("pkg/http/client.go"), "file:pkg/http/client.go"},
		{"file strips ./", FileNode("./pkg/x.go"), "file:pkg/x.go"},
		{"file cleans segments", FileNode("pkg/./a/../x.go"), "file:pkg/x.go"},
		{"file normalizes separators", FileNode(`pkg\x.go`), "file:pkg/x.go"},
		{"file blank", FileNode("   "), ""},
		{"file dot", FileNode("."), ""},
		{"symbol", SymbolNode("pkg/x.go", "Run"), "symbol:pkg/x.go#Run"},
		{"symbol without name", SymbolNode("pkg/x.go", " "), ""},
		{"symbol without file", SymbolNode("", "Run"), ""},
		{"episode", EpisodeNode("ep_1a2b"), "episode:ep_1a2b"},
		{"episode blank", EpisodeNode(""), ""},
		{"run", RunNode("run-17"), "run:run-17"},
		{"task", TaskNode("run-17", "t3"), "task:run-17/t3"},
		{"task without run", TaskNode("", "t3"), ""},
		{"attempt", AttemptNode("run-17", "t3", 2), "attempt:run-17/t3/2"},
		{"rule", RuleNode("rule_ab12"), "rule:rule_ab12"},
		{"fact", FactNode("f_cd34"), "fact:f_cd34"},
		{"failure", FailureNode("fp_ef56"), "failure:fp_ef56"},
		{"commit", CommitNode("deadbeef"), "commit:deadbeef"},
		{"artifact", ArtifactNode("./dist/slmcode"), "artifact:dist/slmcode"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("= %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestNodeKindAndValue(t *testing.T) {
	tests := []struct {
		node      string
		wantKind  string
		wantValue string
	}{
		{FileNode("pkg/x.go"), NodeFile, "pkg/x.go"},
		{SymbolNode("pkg/x.go", "Run"), NodeSymbol, "pkg/x.go#Run"},
		{TaskNode("run-1", "t1"), NodeTask, "run-1/t1"},
		{"bare", "", "bare"},
		{"", "", ""},
		{":leading", "", ":leading"},
	}
	for _, tc := range tests {
		t.Run(tc.node, func(t *testing.T) {
			if got := NodeKind(tc.node); got != tc.wantKind {
				t.Errorf("NodeKind = %q, want %q", got, tc.wantKind)
			}
			if got := NodeValue(tc.node); got != tc.wantValue {
				t.Errorf("NodeValue = %q, want %q", got, tc.wantValue)
			}
		})
	}
	if !IsKind(FileNode("a.go"), NodeFile) {
		t.Error("IsKind(file) = false")
	}
	if IsKind(FileNode("a.go"), NodeRule) {
		t.Error("IsKind(rule) = true for a file node")
	}
}

func TestEdgeIDIsContentAddressOfEndsAndType(t *testing.T) {
	base := Edge{From: "file:a.go", To: "file:b.go", Type: Touched}
	same := Edge{
		From: "file:a.go", To: "file:b.go", Type: Touched,
		At: testNow, RunID: "run-1", Confidence: 0.5, Note: "different metadata",
	}
	if base.ID() != same.ID() {
		t.Errorf("metadata changed the content address: %s vs %s", base.ID(), same.ID())
	}

	differs := []struct {
		name string
		edge Edge
	}{
		{"type", Edge{From: "file:a.go", To: "file:b.go", Type: DependsOn}},
		{"from", Edge{From: "file:c.go", To: "file:b.go", Type: Touched}},
		{"to", Edge{From: "file:a.go", To: "file:c.go", Type: Touched}},
		{"reversed", Edge{From: "file:b.go", To: "file:a.go", Type: Touched}},
	}
	for _, tc := range differs {
		t.Run(tc.name, func(t *testing.T) {
			if tc.edge.ID() == base.ID() {
				t.Errorf("%s collides with the base edge", tc.name)
			}
		})
	}
	if !strings.HasPrefix(base.ID(), "e_") {
		t.Errorf("ID = %q, want an e_ prefix", base.ID())
	}
}

func TestEdgeNormalizeBoundsEverything(t *testing.T) {
	e := Edge{
		From:       "  " + FileNode("a.go") + "  ",
		To:         FileNode("b.go"),
		Type:       "  DerivedFrom  ",
		Note:       strings.Repeat("n", 5000),
		RunID:      " run-1 ",
		Confidence: 4.2,
	}
	e.Normalize(testNow)

	if e.From != "file:a.go" {
		t.Errorf("From = %q, not trimmed", e.From)
	}
	if e.Type != "derivedfrom" {
		t.Errorf("Type = %q, want it lowercased", e.Type)
	}
	if len(e.Note) > MaxNoteLen {
		t.Errorf("Note = %d bytes, exceeds cap %d", len(e.Note), MaxNoteLen)
	}
	if e.RunID != "run-1" {
		t.Errorf("RunID = %q, not trimmed", e.RunID)
	}
	if e.Confidence != 1 {
		t.Errorf("Confidence = %v, want it clamped to 1", e.Confidence)
	}
	if !e.At.Equal(testNow) {
		t.Errorf("At = %v, want it defaulted to now", e.At)
	}

	neg := Edge{From: "a", To: "b", Type: Touched, Confidence: -3}
	neg.Normalize(testNow)
	if neg.Confidence != 0 {
		t.Errorf("Confidence = %v, want it clamped to 0", neg.Confidence)
	}

	long := Edge{From: strings.Repeat("x", MaxNodeLen*2), To: "b", Type: Touched}
	long.Normalize(testNow)
	if len(long.From) > MaxNodeLen {
		t.Errorf("From = %d bytes, exceeds cap %d", len(long.From), MaxNodeLen)
	}
}

func TestEdgeValidate(t *testing.T) {
	tests := []struct {
		name    string
		edge    Edge
		wantErr bool
	}{
		{"ok", Edge{From: "file:a", To: "file:b", Type: Touched}, false},
		{"no source", Edge{To: "file:b", Type: Touched}, true},
		{"no target", Edge{From: "file:a", Type: Touched}, true},
		{"no type", Edge{From: "file:a", To: "file:b"}, true},
		{"self edge", Edge{From: "file:a", To: "file:a", Type: Touched}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.edge.Validate(); (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestEdgeOther(t *testing.T) {
	e := Edge{From: "file:a", To: "file:b", Type: Touched}
	if got, ok := e.Other("file:a"); !ok || got != "file:b" {
		t.Errorf("Other(from) = %q, %v", got, ok)
	}
	if got, ok := e.Other("file:b"); !ok || got != "file:a" {
		t.Errorf("Other(to) = %q, %v", got, ok)
	}
	if _, ok := e.Other("file:z"); ok {
		t.Error("Other(stranger) reported the node was on the edge")
	}
}

func TestEdgeTypesIsACopy(t *testing.T) {
	got := EdgeTypes()
	if len(got) != len(edgeTypes) {
		t.Fatalf("EdgeTypes() = %d entries, want %d", len(got), len(edgeTypes))
	}
	got[0] = "clobbered"
	if edgeTypes[0] == "clobbered" {
		t.Error("EdgeTypes() aliases the package vocabulary")
	}
}

func TestDirectionString(t *testing.T) {
	tests := []struct {
		dir  Direction
		want string
	}{
		{Outgoing, "outgoing"},
		{Incoming, "incoming"},
		{Either, "either"},
		{Direction(99), "outgoing"},
	}
	for _, tc := range tests {
		if got := tc.dir.String(); got != tc.want {
			t.Errorf("Direction(%d).String() = %q, want %q", tc.dir, got, tc.want)
		}
	}
}
