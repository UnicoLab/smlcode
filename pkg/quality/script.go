package quality

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ── Proving a half with a check the project actually has ─────────────────
//
// # WHY THIS EXISTS
//
// A team's acceptance command is one of the three things a team IS: the command
// that proves this half works alone. The shipped `frontend-react` team declares
// `npm --prefix web run build`, which is right for a Vite or CRA project and
// wrong for whatever a local 30B just scaffolded.
//
// Measured live, twice:
//
//	verify  team frontend-react: proving its half alone — npm --prefix web run build
//	verify  team frontend-react: acceptance could not run (this project defines no
//	        such check: npm error Missing script: "build") — UNVERIFIED, not broken
//
// UNVERIFIED is the honest verdict and it is not a good outcome: that half is
// never proved in any run, so "both teams green" cannot be said, and the user
// gets a permanent grey where they wanted green. The team was not wrong about
// what it wants — it wants the half to compile — only about what this
// particular project calls that.
//
// So the script is resolved against the project's own package.json before the
// gate runs. A repo whose build script is named `compile`, or that has only
// `typecheck`, now proves its half instead of reporting nothing. When the
// project defines no usable script at all the command is left exactly as it
// was, and the run reports UNVERIFIED as before — an honest grey beats a
// substitution that proves something nobody asked for.

// scriptPreference is the order to fall back through, best proof first.
//
// A build is the strongest evidence a frontend half is sound: it type-checks,
// resolves every import and fails on a broken component. Tests come after the
// compile-only checks because a scaffold's test script is more often absent or
// a placeholder than its build is.
var scriptPreference = []string{
	"build", "compile", "typecheck", "type-check", "tsc", "check", "test", "lint",
}

// placeholderScript matches the body `npm init` writes for a project with no
// tests: `echo "Error: no test specified" && exit 1`.
//
// Substituting into it would turn a half that was merely unproved into a RED
// one, inventing exactly the failure this package exists to prevent.
func placeholderScript(body string) bool {
	low := strings.ToLower(body)
	return strings.Contains(low, "no test specified") ||
		strings.Contains(low, "not implemented") ||
		strings.TrimSpace(low) == ""
}

// ResolveScriptCommand rewrites a package-script command onto a script the
// project actually defines.
//
// Returns the command to run and a note naming the substitution, empty when
// nothing was changed. The command is returned UNCHANGED — deliberately, so it
// fails and is reported UNVERIFIED — when there is no package.json, when the
// asked-for script exists, or when no script in it is worth running.
func ResolveScriptCommand(root, command string) (string, string) {
	runner, dir, script, ok := parseScriptCommand(command)
	if !ok {
		return command, ""
	}
	scripts := readScripts(filepath.Join(root, dir))
	if len(scripts) == 0 {
		return command, ""
	}
	if body, defined := scripts[script]; defined && !placeholderScript(body) {
		return command, ""
	}
	for _, candidate := range scriptPreference {
		if candidate == script {
			continue
		}
		body, defined := scripts[candidate]
		if !defined || placeholderScript(body) {
			continue
		}
		resolved := strings.Replace(command, " "+script, " "+candidate, 1)
		return resolved, "this project defines no " + runner + " script called " +
			script + " — proving the half with " + candidate + " instead"
	}
	return command, ""
}

// parseScriptCommand pulls the runner, working directory and script name out of
// a package-script invocation, and reports whether it is one at all.
//
// Only the explicit `run <script>` form. `npm test` is a shorthand this does not
// touch: rewriting a bare subcommand risks turning something that is not a
// script reference into one.
func parseScriptCommand(command string) (runner, dir, script string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 3 {
		return "", "", "", false
	}
	switch strings.ToLower(fields[0]) {
	case "npm", "pnpm", "yarn", "bun":
		runner = strings.ToLower(fields[0])
	default:
		return "", "", "", false
	}
	// A command joined to another (`npm run build && npm test`) is not one this
	// can rewrite safely — the script name may appear in either half.
	for _, f := range fields {
		if f == "&&" || f == "||" || f == ";" || f == "|" {
			return "", "", "", false
		}
	}
	for i := 1; i < len(fields); i++ {
		switch fields[i] {
		case "--prefix", "--cwd", "-C", "--dir":
			if i+1 < len(fields) {
				dir = fields[i+1]
				i++
			}
		case "run":
			if i+1 < len(fields) {
				return runner, dir, fields[i+1], true
			}
		}
	}
	return "", "", "", false
}

// readScripts returns the "scripts" map from a package.json, or nil.
func readScripts(dir string) map[string]string {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		// A package.json that does not parse is a broken project, not an
		// invitation to guess at what it meant.
		return nil
	}
	return pkg.Scripts
}
