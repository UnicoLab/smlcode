package compact

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func realisticContext() string {
	var b strings.Builder
	b.WriteString("# Working Context\n\n## Locked PRD\n\nShip the flag.\n\n")
	for i := 0; i < 60; i++ {
		b.WriteString("## Wave update " + string(rune('a'+i%26)) + "\n\n")
		b.WriteString("- touched `pkg/cli/flags.go` and `pkg/config/config.go`\n")
		b.WriteString("- ran `go test ./...`\n\n")
	}
	return b.String()
}

func TestStripPreamble(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"sure", "Sure! Here is the compressed context:\n## A\nbody", "## A\nbody"},
		{"okay", "Okay, compressing now.\n## A\nbody", "## A\nbody"},
		{"certainly", "Certainly:\n## A\n", "## A"},
		{"none", "## A\nbody", "## A\nbody"},
		{"fence", "```markdown\n## A\nbody\n```", "## A\nbody"},
		{"double", "Sure!\nHere is the result:\n## A\nb", "## A\nb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripPreamble(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestAcceptCompactionGates(t *testing.T) {
	input := "## A\n\nsee `pkg/a/a.go` and `pkg/b/b.go` and `cmd/x/main.go`\n" + strings.Repeat("filler line\n", 200)
	tests := []struct {
		name   string
		output string
		want   GateFailure
	}{
		{
			name:   "good",
			output: "## A\n\nkeeps `pkg/a/a.go`, `pkg/b/b.go`, `cmd/x/main.go`\n" + strings.Repeat("kept\n", 60),
			want:   GateOK,
		},
		{"empty", "", GateEmpty},
		{"whitespace", "   \n ", GateEmpty},
		{"chatty stub", "## A\nSure, done.", GateTooShort},
		{
			name:   "no heading",
			output: "just prose about `pkg/a/a.go` `pkg/b/b.go` `cmd/x/main.go`\n" + strings.Repeat("kept\n", 60),
			want:   GateNoHeading,
		},
		{
			name:   "dropped paths",
			output: "## A\n\nonly `pkg/a/a.go` survived\n" + strings.Repeat("kept\n", 60),
			want:   GateLostPaths,
		},
		{"not smaller", input + "more", GateNotSmaller},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AcceptCompaction(input, tc.output); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSummarizeRejectsBadLLMOutput(t *testing.T) {
	body := realisticContext()
	tests := []struct {
		name    string
		engine  string
		out     string
		err     error
		wantLLM bool
	}{
		{"chatty stub llm", "llm", "Sure! Here is the compressed context:", nil, false},
		{"chatty stub auto", "auto", "Sure! Here is the compressed context:", nil, false},
		{"tiny output", "llm", "## X\nok", nil, false},
		{"llm error", "auto", "", errors.New("down"), false},
		{
			name:    "good output",
			engine:  "llm",
			out:     "## Locked PRD\n\nShip the flag.\n\n## History\n\ntouched `pkg/cli/flags.go` and `pkg/config/config.go`\n" + strings.Repeat("- kept detail line\n", 80),
			wantLLM: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := Summarize(context.Background(), tc.engine, body, 4096,
				func(ctx context.Context, b string, max int) (string, error) {
					return tc.out, tc.err
				})
			if res.Original != strings.TrimSpace(body) {
				t.Fatal("Original must carry the pre-compaction body")
			}
			acceptedLLM := res.Engine != ""
			if acceptedLLM != tc.wantLLM {
				t.Fatalf("engine=%q rejected=%q summary=%q", res.Engine, res.Rejected, res.Summary[:min(120, len(res.Summary))])
			}
			if !tc.wantLLM && res.Summary == tc.out {
				t.Fatal("bad LLM output must not become the summary")
			}
			if !res.Compacted {
				t.Fatal("expected compaction")
			}
		})
	}
}

func TestSummarizeNoSummarizer(t *testing.T) {
	body := realisticContext()
	res := Summarize(context.Background(), "llm", body, 2048, nil)
	if res.Rejected != GateNoSummarize || !res.Compacted {
		t.Fatalf("%+v", res.Rejected)
	}
}

func TestNeedsCompactAndHysteresis(t *testing.T) {
	tests := []struct {
		name       string
		size       int
		soft, hard int
		want       bool
	}{
		{"under soft", 4000, 8, 32, false},
		{"over soft", 9000, 8, 32, true},
		{"exactly soft", 8 * 1024, 8, 32, false},
		{"soft above hard is normalized", 40 * 1024, 64, 32, false},
		{"defaults", 40 * 1024, 0, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Repeat("x", tc.size)
			if got := NeedsCompact(body, tc.soft, tc.hard); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}

	// The compaction target must sit clearly BELOW the trigger.
	for _, soft := range []int{8, 16, 32, 64} {
		target := CompactTargetBytes(soft, soft*2)
		if target >= soft*1024 {
			t.Fatalf("soft=%d target=%d does not open headroom", soft, target)
		}
		if NeedsCompact(strings.Repeat("x", target), soft, soft*2) {
			t.Fatalf("post-compaction size %d re-triggers at soft=%d", target, soft)
		}
	}
}

func TestForceHeuristicAndEngineFor(t *testing.T) {
	small := strings.Repeat("x", 10*1024)
	huge := strings.Repeat("x", 100*1024)
	if ForceHeuristic(small, 8, 32) {
		t.Fatal("small body should allow llm")
	}
	if !ForceHeuristic(huge, 8, 32) {
		t.Fatal("huge body should force heuristic")
	}
	if EngineFor("auto", small, 8, 32) != "auto" {
		t.Fatal("auto should survive under the ceiling")
	}
	if EngineFor("auto", huge, 8, 32) != "heuristic" {
		t.Fatal("auto must downgrade past the ceiling")
	}
	if EngineFor("", small, 8, 32) != "heuristic" {
		t.Fatal("empty engine defaults heuristic")
	}
}

func TestWindowTokensFor(t *testing.T) {
	tests := []struct {
		name      string
		limit, kb int
		want      int
	}{
		{"model limit wins", 32768, 16, 32768},
		{"fallback to kb", 0, 16, 4096},
		{"both zero", 0, 0, 4096},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := WindowTokensFor(tc.limit, tc.kb); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}
