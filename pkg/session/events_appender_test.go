package session

import (
	"os"
	"strings"
	"testing"
)

// The event log is written through one buffered appender per run: token
// deltas are not persisted, chatty kinds are buffered until a structural kind
// or the timer flushes them, and run_end closes the file.
func TestEventAppenderBuffersAndSkipsTokens(t *testing.T) {
	slm := t.TempDir()
	const run = "run-buf"
	for _, rec := range []EventRecord{
		{Phase: "execute", Kind: "output", Message: "chatty one"},
		{Phase: "execute", Kind: "token", Message: "d"},
		{Phase: "execute", Kind: "token", Message: "e"},
		{Phase: "execute", Kind: "output", Message: "chatty two"},
	} {
		if err := AppendEvent(slm, run, rec); err != nil {
			t.Fatal(err)
		}
	}
	// Chatty kinds sit in the buffer; a reader through ReadEvents sees them
	// because it flushes first, and no token delta was persisted.
	recs, err := ReadEvents(slm, run, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Message != "chatty one" || recs[1].Message != "chatty two" {
		t.Fatalf("records after flush: %+v", recs)
	}
	raw, _ := os.ReadFile(EventsPath(slm, run))
	if strings.Contains(string(raw), `"token"`) {
		t.Fatalf("token deltas were persisted: %s", raw)
	}

	// A structural kind flushes on write: the file grows without ReadEvents.
	before := len(raw)
	if err := AppendEvent(slm, run, EventRecord{Phase: "execute", Kind: "phase", Message: "wave 1"}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(EventsPath(slm, run))
	if len(raw) <= before || !strings.Contains(string(raw), "wave 1") {
		t.Fatalf("structural kind not flushed immediately: %s", raw)
	}
	appendersMu.Lock()
	_, open := appenders[appenderKey(slm, run)]
	appendersMu.Unlock()
	if !open {
		t.Fatal("appender should stay open between structural events")
	}

	// run_end flushes, syncs and closes the appender.
	if err := AppendEvent(slm, run, EventRecord{Phase: "done", Kind: "run_end", Message: "finished"}); err != nil {
		t.Fatal(err)
	}
	appendersMu.Lock()
	_, open = appenders[appenderKey(slm, run)]
	appendersMu.Unlock()
	if open {
		t.Fatal("run_end did not close the appender")
	}
	recs, _ = ReadEvents(slm, run, 0)
	if len(recs) != 4 || recs[3].Kind != "run_end" {
		t.Fatalf("records after run_end: %+v", recs)
	}
	// A later append for the same run reopens transparently.
	if err := AppendEvent(slm, run, EventRecord{Phase: "done", Kind: "note", Message: "late"}); err != nil {
		t.Fatal(err)
	}
	CloseEventLog(slm, run)
	recs, _ = ReadEvents(slm, run, 0)
	if len(recs) != 5 || recs[4].Message != "late" {
		t.Fatalf("records after reopen: %+v", recs)
	}
}

func BenchmarkAppendEventChatty(b *testing.B) {
	slm := b.TempDir()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = AppendEvent(slm, "bench", EventRecord{Phase: "execute", Kind: "output", Message: "tool output line"})
	}
	CloseEventLog(slm, "bench")
}
