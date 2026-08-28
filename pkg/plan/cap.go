package plan

import "strings"

// CapTasksPreserveHarness limits task count while keeping harness-critical
// tasks (tester, requirements, main.py, tests) that greenfield sanitization
// injects. Prevents the old "[:8] slice" bug that dropped verification.
func CapTasksPreserveHarness(tasks []Task, max int) []Task {
	if max <= 0 || len(tasks) <= max {
		return tasks
	}
	var keep, rest []Task
	for _, t := range tasks {
		if isHarnessCriticalTask(t) {
			keep = append(keep, t)
		} else {
			rest = append(rest, t)
		}
	}
	// Always keep harness tasks; fill remaining slots from the front of rest.
	room := max - len(keep)
	if room < 0 {
		// Too many harness tasks — keep first max harness (tester last).
		testers := []Task{}
		others := []Task{}
		for _, t := range keep {
			if IsTesterRole(t.Role) {
				testers = append(testers, t)
			} else {
				others = append(others, t)
			}
		}
		out := others
		if len(out) > max-len(testers) {
			out = out[:max-len(testers)]
		}
		out = append(out, testers...)
		if len(out) > max {
			out = out[:max]
		}
		return out
	}
	if len(rest) > room {
		rest = rest[:room]
	}
	return append(rest, keep...)
}

func isHarnessCriticalTask(t Task) bool {
	if IsTesterRole(t.Role) {
		return true
	}
	blob := strings.ToLower(t.Title + " " + t.Description + " " + strings.Join(t.Files, " "))
	return strings.Contains(blob, "requirements.txt") ||
		strings.Contains(blob, "main.py") ||
		strings.Contains(blob, "tests/") ||
		strings.Contains(blob, "test_smoke") ||
		strings.Contains(blob, "pytest")
}
