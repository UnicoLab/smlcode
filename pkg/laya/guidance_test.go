package laya

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	contextstore "github.com/UnicoLab/slmcode/pkg/context"
)

func TestGuidanceBoundedAdvisoryAndAbstention(t *testing.T) {
	var calls atomic.Int32
	var low atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct{ State string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if contextstore.DefaultTokenCounter(req.State) > 640 || !strings.Contains(req.State, "partial context") {
			t.Error("unbounded or unlabeled excerpt")
		}
		score := 0.95
		if low.Load() {
			score = 0.5
		}
		_, _ = fmt.Fprintf(w, `{"answers":{"relevant":{"type":"noul","noul":%f}}}`, score)
	}))
	defer srv.Close()
	client, err := New(Options{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"go-worker", "explorer", "context", "planner", "architect", "splitter", "coordinator", "strict-reviewer", "tester", "critic", "corrector"} {
		hint, err := client.Guidance(context.Background(), role, strings.Repeat("large input ", 2000))
		if err != nil || !strings.Contains(hint, "acceptance gates still apply") {
			t.Fatalf("%s: %q %v", role, hint, err)
		}
	}
	before := calls.Load()
	if hint, err := client.Guidance(context.Background(), "unknown", "input"); hint != "" || err != nil || calls.Load() != before {
		t.Fatal("unknown role caused inference")
	}
	low.Store(true)
	if hint, err := client.Guidance(context.Background(), "worker", strings.Repeat("long ", 2000)); hint != "" || err != nil {
		t.Fatalf("low score must abstain: %q %v", hint, err)
	}
	for _, input := range []string{"", "short", strings.Repeat("語🙂", 2000)} {
		if got := excerpt(input, 320); contextstore.DefaultTokenCounter(got) > 320 {
			t.Fatal("token bound exceeded")
		}
	}
}
