package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFactObserveConfidence(t *testing.T) {
	f := openFacts("", 0, nil, nil)
	got := f.Observe(Fact{Kind: FactCommand, Subject: "go test ./...", Text: "`go test ./...` works"})
	if got.Support != 1 || got.Confidence <= 0.6 || got.Confidence >= 0.7 {
		t.Fatalf("first observation = %+v, want support 1 and confidence ~0.67", got)
	}
	for i := 0; i < 8; i++ {
		got = f.Observe(Fact{Kind: FactCommand, Subject: "go test ./...", Text: "`go test ./...` works"})
	}
	if got.Support != 9 || got.Confidence < 0.9 {
		t.Fatalf("repeated observation = support %d conf %.2f, want rising confidence", got.Support, got.Confidence)
	}
}

func TestFactContradictionReplacesText(t *testing.T) {
	f := openFacts("", 0, nil, nil)
	f.Observe(Fact{Kind: FactCommand, Subject: "test", Text: "run `make test`"})
	// Two contradicting observations outweigh one supporting one.
	f.Observe(Fact{Kind: FactCommand, Subject: "test", Text: "run `make ui && make test`"})
	got, _ := f.Get(FactCommand, "test")
	if got.Text != "run `make test`" {
		t.Fatalf("one contradiction should not flip the fact yet, got %q", got.Text)
	}
	f.Observe(Fact{Kind: FactCommand, Subject: "test", Text: "run `make ui && make test`"})
	got, _ = f.Get(FactCommand, "test")
	if got.Text != "run `make ui && make test`" {
		t.Fatalf("fact did not decay to the newer claim: %q", got.Text)
	}
	if got.Support != 1 || got.Contradict != 0 {
		t.Fatalf("counters not reset after a flip: %+v", got)
	}
}

func TestFactRefuteLowersConfidence(t *testing.T) {
	f := openFacts("", 0, nil, nil)
	f.Observe(Fact{Kind: FactCommand, Subject: "build", Text: "`go build ./...` works"})
	before, _ := f.Get(FactCommand, "build")
	if !f.Refute(FactCommand, "build") {
		t.Fatal("Refute on a known fact returned false")
	}
	after, _ := f.Get(FactCommand, "build")
	if after.Confidence >= before.Confidence {
		t.Fatalf("confidence %.2f did not drop from %.2f", after.Confidence, before.Confidence)
	}
	if f.Refute(FactCommand, "does-not-exist") {
		t.Error("Refute on an unknown fact should return false")
	}
}

func TestFactPinnedNeverFlipsOrPrunes(t *testing.T) {
	f := openFacts("", 0, nil, nil)
	f.Observe(Fact{Kind: FactGotcha, Subject: "ui", Text: "make test needs make ui first", Pinned: true})
	for i := 0; i < 10; i++ {
		f.Observe(Fact{Kind: FactGotcha, Subject: "ui", Text: "something else entirely"})
	}
	got, _ := f.Get(FactGotcha, "ui")
	if got.Text != "make test needs make ui first" {
		t.Fatalf("pinned fact was overwritten: %q", got.Text)
	}
	if n := f.Prune(PrunePolicy{MaxFacts: 0, MinFactConfidence: 0.99}); n != 0 {
		t.Fatalf("pinned fact was pruned (%d removed)", n)
	}
	if f.Refute(FactGotcha, "ui") {
		t.Error("pinned facts must not be refutable")
	}
}

func TestFactsRenderIsBudgetedAndOrdered(t *testing.T) {
	f := openFacts("", 0, nil, nil)
	if f.Render(400) != "" {
		t.Fatal("empty fact store must render nothing")
	}
	for i := 0; i < 50; i++ {
		f.Observe(Fact{Kind: FactFile, Subject: "pkg/x/f" + itoa(i) + ".go", Text: "`pkg/x/f" + itoa(i) + ".go` changed a lot"})
		f.Observe(Fact{Kind: FactFile, Subject: "pkg/x/f" + itoa(i) + ".go", Text: "`pkg/x/f" + itoa(i) + ".go` changed a lot"})
	}
	f.Observe(Fact{Kind: FactCommand, Subject: "go test", Text: "`go test ./...` works here"})
	f.Observe(Fact{Kind: FactCommand, Subject: "go test", Text: "`go test ./...` works here"})

	out := f.Render(200)
	if out == "" {
		t.Fatal("populated store rendered nothing")
	}
	if n := countTokens(nil, out); n > 200 {
		t.Errorf("render used %d tokens, budget 200", n)
	}
	iCmd := strings.Index(out, "Commands that work")
	iFile := strings.Index(out, "Files")
	if iCmd < 0 {
		t.Fatalf("commands section missing: %s", out)
	}
	if iFile >= 0 && iFile < iCmd {
		t.Errorf("files rendered before commands; action-first order violated:\n%s", out)
	}
}

func TestFactsLowConfidenceNotRendered(t *testing.T) {
	f := openFacts("", 0, nil, nil)
	f.Observe(Fact{Kind: FactGotcha, Subject: "s", Text: "first claim"})
	for i := 0; i < 5; i++ {
		f.Refute(FactGotcha, "s")
	}
	if strings.Contains(f.Render(400), "first claim") {
		t.Error("a heavily contradicted fact must not be injected into a prompt")
	}
}

func TestFactsSurviveCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "facts.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := openFacts(dir, 0, nil, nil)
	if f.Count() != 0 {
		t.Fatalf("count = %d, want 0", f.Count())
	}
	if len(f.Warnings()) == 0 {
		t.Error("corrupt facts file should be reported")
	}
	if _, err := os.Stat(filepath.Join(dir, "facts.json.corrupt")); err != nil {
		t.Error("corrupt file should be preserved, not deleted")
	}
	f.Observe(Fact{Kind: FactCommand, Subject: "a", Text: "b"})
	if err := f.Flush(); err != nil {
		t.Fatalf("Flush after corruption: %v", err)
	}
}

func TestFactsPersistAndReload(t *testing.T) {
	dir := t.TempDir()
	f := openFacts(dir, 0, nil, nil)
	f.Observe(Fact{Kind: FactCommand, Subject: "go build", Text: "`go build ./...` works"})
	f.Observe(Fact{Kind: FactCommand, Subject: "go build", Text: "`go build ./...` works"})
	if err := f.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "SEMANTIC.md")); err != nil {
		t.Errorf("SEMANTIC.md mirror missing: %v", err)
	}

	f2 := openFacts(dir, 0, nil, nil)
	got, ok := f2.Get(FactCommand, "go build")
	if !ok || got.Support != 2 {
		t.Fatalf("reloaded fact = %+v (ok=%v)", got, ok)
	}
}

func TestFactsPruneBoundsStore(t *testing.T) {
	now := time.Now()
	f := openFacts("", 500, func() time.Time { return now }, nil)
	for i := 0; i < 400; i++ {
		f.Observe(Fact{Kind: FactFile, Subject: "f" + itoa(i), Text: "text " + itoa(i)})
	}
	removed := f.Prune(PrunePolicy{MaxFacts: 50, MinFactConfidence: 0})
	if f.Count() != 50 {
		t.Fatalf("count after prune = %d, want 50 (removed %d)", f.Count(), removed)
	}
}
