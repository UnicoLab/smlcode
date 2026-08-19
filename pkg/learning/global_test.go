package learning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendReadGlobalMemoryUsesUserLevelPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := AppendGlobalMemory("Wave lessons", "- Avoid repeating timeout after context deadline exceeded"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".slmcode", "MEMORY.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "SLMCode Global Memory") {
		t.Fatalf("missing global memory header:\n%s", data)
	}
	read := ReadGlobalMemory()
	if !strings.Contains(read, "context deadline exceeded") {
		t.Fatalf("global memory not readable:\n%s", read)
	}
}

func TestRecentAdaptiveMemoryMergesGlobalAndProjectSignals(t *testing.T) {
	project := "- project memory: qa_gate failed, fix smoke first\n"
	global := "- global memory: timeout after context deadline exceeded; lower max_parallel\n"

	got := RecentAdaptiveMemory(project, global, 1000)
	if !strings.Contains(got, "global memory") {
		t.Fatalf("missing global signal:\n%s", got)
	}
	if !strings.Contains(got, "project memory") {
		t.Fatalf("missing project signal:\n%s", got)
	}
	if !strings.Contains(got, "timeout") || !strings.Contains(got, "qa_gate") {
		t.Fatalf("missing adaptive keywords:\n%s", got)
	}
}
