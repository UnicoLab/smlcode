package plan

import (
	"strings"
	"testing"
)

func rosterHas(id string) bool {
	switch id {
	case "go-worker", "go-corrector", "worker", "corrector":
		return true
	}
	return false
}

func TestParseTriageToleratesModelWrapping(t *testing.T) {
	body := `{"assignee":"GO-Corrector","reason":" compile error ","guidance":" encode the slice ","priority":"HIGH"}`
	for i, raw := range []string{
		body,
		"Here is my call:\n```json\n" + body + "\n```",
		"```\n" + body + "\n```\nHope that helps.",
	} {
		d, err := ParseTriage(raw)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if d.Assignee != "go-corrector" {
			t.Errorf("case %d: assignee = %q, want it lowercased", i, d.Assignee)
		}
		if d.Reason != "compile error" || d.Guidance != "encode the slice" {
			t.Errorf("case %d: not trimmed: %+v", i, d)
		}
		if d.Priority != "high" {
			t.Errorf("case %d: priority = %q", i, d.Priority)
		}
	}
}

func TestParseTriageDefaultsPriorityAndRejectsGarbage(t *testing.T) {
	d, err := ParseTriage(`{"assignee":"worker","reason":"x","priority":"urgent-ish"}`)
	if err != nil {
		t.Fatal(err)
	}
	// An unrecognized priority is normal, not an error: the field is advisory
	// and refusing the whole verdict over it would throw away the assignment.
	if d.Priority != "normal" {
		t.Errorf("priority = %q, want normal", d.Priority)
	}
	for _, raw := range []string{"", "no json", "{unclosed", "[1,2]"} {
		if _, err := ParseTriage(raw); err == nil {
			t.Errorf("expected an error for %q", raw)
		}
	}
}

// Guidance containing a brace must not truncate the object — a manager
// explaining a JSON fix is the likeliest case there is.
func TestParseTriageHandlesBracesInGuidance(t *testing.T) {
	d, err := ParseTriage(`{"assignee":"go-worker","reason":"r","guidance":"return {\"id\":1} not a bare slice"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Guidance, `{"id":1}`) {
		t.Errorf("guidance = %q", d.Guidance)
	}
}

// Two manager answers are worse than no manager at all.
func TestUsableRejectsTheTwoWaysAManagerStallsATask(t *testing.T) {
	cases := []struct {
		name     string
		d        TriageDecision
		failed   string
		usable   bool
		contains string
	}{
		{"a good pick", TriageDecision{Assignee: "go-corrector"}, "go-worker", true, ""},
		// The task would sit in ready_to_dev with nobody able to move it.
		{"unregistered agent", TriageDecision{Assignee: "cobol-worker"}, "go-worker", false, "not a registered agent"},
		// The loop triage exists to end.
		{"re-picks the failed agent", TriageDecision{Assignee: "go-worker"}, "go-worker", false, "just exhausted its retries"},
		{"case-insensitively re-picks it", TriageDecision{Assignee: "GO-WORKER"}, "go-worker", false, "just exhausted"},
		{"names nobody", TriageDecision{}, "go-worker", false, "no assignee"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.d
			d.Assignee = strings.ToLower(d.Assignee)
			ok, why := d.Usable(tc.failed, rosterHas)
			if ok != tc.usable {
				t.Fatalf("Usable = %v (%s), want %v", ok, why, tc.usable)
			}
			if tc.contains != "" && !strings.Contains(why, tc.contains) {
				t.Errorf("reason = %q, want it to mention %q", why, tc.contains)
			}
		})
	}
}

func TestUsableRefusesWithNoRegistry(t *testing.T) {
	d := TriageDecision{Assignee: "go-corrector"}
	if ok, _ := d.Usable("go-worker", nil); ok {
		t.Error("with no registry nothing can be confirmed dispatchable")
	}
}
