package augment

import (
	"fmt"
	"strings"
)

// FailureRecovery returns a short mid-loop recovery card appended to failed
// tool results (little-coder skill-inject error-recovery path).
func FailureRecovery(tool, path string) string {
	tool = strings.TrimSpace(tool)
	path = strings.TrimSpace(path)
	if path == "" {
		path = "<path>"
	}
	switch tool {
	case "ws_edit", "ws_patch":
		return fmt.Sprintf(
			"\n\n## RECOVERY (do this next)\n"+
				"1. ws_read %s (copy exact numbered text)\n"+
				"2. Retry %s with exact old_str/SEARCH — include 2–3 context lines for uniqueness\n"+
				"3. Never escalate to ws_write on an existing file\n"+
				"4. After a successful edit: smoke with ws_shell, then status JSON\n",
			path, tool,
		)
	case "ws_write":
		return fmt.Sprintf(
			"\n\n## RECOVERY (do this next)\n"+
				"File already exists. Use ws_read %s then ws_edit/ws_patch. "+
				"Do not retry ws_write.\n",
			path,
		)
	default:
		return "\n\n## RECOVERY\nTry a different approach: re-read the file, adjust arguments, " +
			"or run a smoke test. Do not repeat the exact same failing call.\n"
	}
}
