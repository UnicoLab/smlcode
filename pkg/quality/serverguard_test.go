package quality

import "testing"

// A dev server is not a proposition that can pass. `npm run` is whitelisted
// because `npm run lint` and `npm run typecheck` are exactly what this gate
// should run — and `npm run dev` shares that prefix while never returning, so
// auto-running it burns the task's whole timeout and learns nothing.
//
// Measured: a live splitter wrote `npm run dev passes` as a tester task's
// acceptance.
func TestServerCommandsAreNotAcceptance(t *testing.T) {
	for _, cmd := range []string{
		"npm run dev",
		"npm start",
		"npm run serve",
		"npm run preview",
		"npm run watch",
		"yarn dev",
		"pnpm dev",
		"bun run dev",
		"vite",
		"next dev",
		"astro dev",
	} {
		if got := SafeVerifyCommand(cmd); got != "" {
			t.Errorf("SafeVerifyCommand(%q) = %q — a server is not a verification", cmd, got)
		}
	}
}

// The verifications that share those prefixes must keep working, or the guard
// costs more than the bug.
func TestRealVerificationsStillRun(t *testing.T) {
	for _, cmd := range []string{
		"npm test",
		"npm test --silent",
		"npm run lint",
		"npm run typecheck",
		"npm run build",
		"pnpm run build",
		"go test ./...",
		"pytest -q",
	} {
		if got := SafeVerifyCommand(cmd); got == "" {
			t.Errorf("SafeVerifyCommand(%q) = \"\" — this is a real verification", cmd)
		}
	}
}

func TestIsLongRunningServer(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want bool
	}{
		{"npm run dev", true},
		{"npm run dev -- --host", true},
		{"NPM RUN DEV", true}, // case must not be an escape hatch
		{"npm run build", false},
		{"npm run test:unit", false},
		{"npm start", true},
		{"pnpm dev", true},
		{"pnpm build", false},
		{"next dev", true},
		{"next build", false},
		{"vite", true},
		{"go test ./...", false},
		{"", false},
	} {
		if got := isLongRunningServer(tc.cmd); got != tc.want {
			t.Errorf("isLongRunningServer(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}
