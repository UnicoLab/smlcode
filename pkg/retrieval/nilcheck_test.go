package retrieval

import (
	"context"
	"testing"
)

// TestNilRetrieverSearchDoesNotPanic pins the nil-receiver contract Search has
// always had. Splitting the ranking out into SearchAll nearly dropped it:
// SearchAll rebinds its own local r, which does nothing for Search's frame, so
// Search has to guard separately before it reads r.TopK.
func TestNilRetrieverSearchDoesNotPanic(t *testing.T) {
	var r *Retriever
	got, err := r.Search(context.Background(), "beta",
		[]Chunk{{Text: "alpha beta"}, {Text: "gamma delta"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("a nil retriever returned no hits; it is documented to fall back to lexical")
	}
}
