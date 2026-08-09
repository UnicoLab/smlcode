package blocks

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveQAGateCommand returns the QA gate from the active pack or auto-detect.
// Falls back to empty when no quality block matches (caller uses legacy detect).
func ResolveQAGateCommand(projectRoot, workspaceRoot, activePack string) string {
	reg, err := Load(projectRoot)
	if err != nil || reg == nil {
		return ""
	}
	if activePack != "" {
		if pack, ok := reg.GetPack(activePack); ok && pack.Spec.Quality != "" {
			if q, ok := reg.GetQuality(pack.Spec.Quality); ok {
				if gate := q.PrimaryQAGate(); gate != "" {
					return adaptPythonQAGate(workspaceRoot, gate)
				}
			}
		}
		if q, ok := reg.GetQuality(activePack); ok {
			if gate := q.PrimaryQAGate(); gate != "" {
				return adaptPythonQAGate(workspaceRoot, gate)
			}
		}
	}
	if q := reg.DetectQuality(workspaceRoot); q != nil {
		return adaptPythonQAGate(workspaceRoot, q.PrimaryQAGate())
	}
	return ""
}

// ResolveSmokeCommand returns the post-worker / project smoke from quality packs.
func ResolveSmokeCommand(projectRoot, workspaceRoot, activePack string) string {
	reg, err := Load(projectRoot)
	if err != nil || reg == nil {
		return ""
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
		q = reg.DetectQuality(workspaceRoot)
	}
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
