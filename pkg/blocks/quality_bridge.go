package blocks

import (
	"os"
	"path/filepath"
	"strings"
)

// resolveQualityBlock returns the quality block that should drive this
// workspace. It prefers the active pack's quality block ONLY when that block
// still detects in the workspace — otherwise it falls back to auto-detection so
// a stale active_pack (e.g. "python" left over from a prior run) never forces a
// mismatched gate (pytest on a Go/JS/HTML project) onto the current task.
func resolveQualityBlock(projectRoot, workspaceRoot, activePack string) *QualityBlock {
	reg, err := Load(projectRoot)
	if err != nil || reg == nil {
		return nil
	}
	if activePack != "" {
		if pack, ok := reg.GetPack(activePack); ok && pack.Spec.Quality != "" {
			if q, ok := reg.GetQuality(pack.Spec.Quality); ok && q.DetectsIn(workspaceRoot) {
				return q
			}
		}
		if q, ok := reg.GetQuality(activePack); ok && q.DetectsIn(workspaceRoot) {
			return q
		}
	}
	return reg.DetectQuality(workspaceRoot)
}

// ResolveQAGateCommand returns the QA gate from the active pack or auto-detect.
// Falls back to empty when no quality block matches (caller uses legacy detect).
func ResolveQAGateCommand(projectRoot, workspaceRoot, activePack string) string {
	q := resolveQualityBlock(projectRoot, workspaceRoot, activePack)
	if q == nil {
		return ""
	}
	return adaptPythonQAGate(workspaceRoot, q.PrimaryQAGate())
}

// ResolveSmokeCommand returns the post-worker / project smoke from quality packs.
func ResolveSmokeCommand(projectRoot, workspaceRoot, activePack string) string {
	q := resolveQualityBlock(projectRoot, workspaceRoot, activePack)
	if q == nil {
		return ""
	}
	smoke := strings.TrimSpace(q.Spec.Smoke)
	if smoke == "" {
		smoke = q.PrimaryQAGate()
	}
	return adaptPythonQAGate(workspaceRoot, smoke)
}

// SafePrefixesFromPack returns extra acceptance command prefixes from quality packs.
func SafePrefixesFromPack(projectRoot, activePack string) []string {
	reg, err := Load(projectRoot)
	if err != nil || reg == nil {
		return nil
	}
	var q *QualityBlock
	if activePack != "" {
		if pack, ok := reg.GetPack(activePack); ok && pack.Spec.Quality != "" {
			q, _ = reg.GetQuality(pack.Spec.Quality)
		}
		if q == nil {
			q, _ = reg.GetQuality(activePack)
		}
	}
	if q == nil {
		return nil
	}
	return append([]string{}, q.Spec.SafePrefixes...)
}

func adaptPythonQAGate(root, gate string) string {
	gate = strings.TrimSpace(gate)
	if gate == "" {
		return ""
	}
	// Prefer uv run when the quality pack says pytest and uv.lock exists.
	if strings.Contains(gate, "pytest") && !strings.Contains(gate, "uv run") {
		if fileExists(filepath.Join(root, "uv.lock")) {
			if gate == "python -m pytest -q" || gate == "python -m pytest" {
				return "uv run pytest -q"
			}
		}
	}
	return gate
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
