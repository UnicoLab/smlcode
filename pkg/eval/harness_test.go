package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultCasesShape(t *testing.T) {
	cases := DefaultCases()
	if len(cases) < 2 {
		t.Fatal("expected cases")
	}
	for _, c := range cases {
		if c.ID == "" || c.Query == "" || len(c.ExpectFiles) == 0 {
			t.Fatalf("incomplete case %+v", c)
		}
	}
}

func TestWriteReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	rep := Report{
		Started: time.Now().UTC().Format(time.RFC3339),
		Model:   "test", Results: []Result{{ID: "x", OK: true}},
		Passed: 1,
	}
	if err := WriteReport(path, rep); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if json.Unmarshal(data, &got) != nil || got.Passed != 1 {
		t.Fatalf("%s", data)
	}
}
