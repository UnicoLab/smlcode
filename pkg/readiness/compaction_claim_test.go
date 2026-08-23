package readiness

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/loop"
)

// TestCompactionCheckDoesNotOverclaim pins defect 9.
//
// react_compact is on by default and the readiness report told the operator
// "Document and ReAct compaction are enabled". Only the resume path compacts:
// loop.CompactLiveMessages has no caller, so a live 16-iteration worker
// appending a whole file per tool result is never compacted mid-call — which is
// precisely the case an operator reads this check to rule out.
func TestCompactionCheckDoesNotOverclaim(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.ContextCompact = true
	cfg.ReactCompact = true

	got := compactionCheck(cfg)
	if !got.OK {
		t.Fatalf("check should pass with both settings on: %+v", got)
	}
	if loop.LiveReactCompactionWired {
		if !strings.Contains(got.Message, "live") {
			t.Fatalf("live compaction is wired but the message does not say so: %q", got.Message)
		}
		return
	}
	if !strings.Contains(got.Message, "checkpoint/resume only") {
		t.Fatalf("the message must say what is NOT covered, got %q", got.Message)
	}
	if strings.Contains(got.Message, "ReAct compaction are enabled") {
		t.Fatalf("the old over-claiming message is back: %q", got.Message)
	}

	cfg.ReactCompact = false
	off := compactionCheck(cfg)
	if off.OK {
		t.Fatalf("check must fail with react_compact off: %+v", off)
	}
}

// TestReactCompactionStatusTracksWiring keeps the claim and the wiring flag in
// step: flipping loop.LiveReactCompactionWired to true must change what the
// operator is told, in one place.
func TestReactCompactionStatusTracksWiring(t *testing.T) {
	if got := loop.ReactCompactionStatus(false); !strings.Contains(got, "off") {
		t.Fatalf("react_compact=false status = %q", got)
	}
	on := loop.ReactCompactionStatus(true)
	if loop.LiveReactCompactionWired != strings.Contains(on, "live iterations") {
		t.Fatalf("status %q disagrees with LiveReactCompactionWired=%v",
			on, loop.LiveReactCompactionWired)
	}
}
