package workspace

import (
	"strings"
	"testing"
)

func TestAdversaryShellBypassBattery(t *testing.T) {
	cases := []string{
		`env pwnbin --steal`,
		`env FOO=1 bash -c 'pwnbin'`,
		`env -i /bin/sh -c pwnbin`,
		`printenv PATH; env sh -c pwnbin`,
		`find . -name '*.go' -exec pwnbin {} ;`,
		`find . -execdir pwnbin ;`,
		`find . -ok pwnbin ;`,
		`find . -name x -delete`,
		`find . -fprintf /etc/cron.d/x "* * * * * root pwnbin"`,
		`go test -exec 'sh -c pwnbin' ./...`,
		`go test -exec=pwnbin ./...`,
		`go build -toolexec=pwnbin ./...`,
		`go vet -vettool=pwnbin ./...`,
		`go test -ldflags=-extldflags=-fuse-ld=pwnbin ./...`,
		`cmake -P evil.cmake`,
		`cmake -E copy /etc/passwd out`,
		`ctest --build-and-test . . --build-generator x`,
		`touch /etc/x`,
		`touch ../../outside.txt`,
		`mkdir -p /tmp/pwned`,
		`mkdir ../../outside`,
		`ls\
&& pwnbin`,
	}
	for _, c := range cases {
		if _, blocked := GuardShellWhitelist(c, nil); !blocked {
			t.Errorf("NOT BLOCKED: %q", c)
		}
	}
}

// Commands that MUST stay allowed (regression against over-blocking).
func TestAdversaryShellStillAllowed(t *testing.T) {
	ok := []string{
		`go test ./pkg/foo -short`,
		`go build ./...`,
		`go vet ./...`,
		`python -m pytest -q`,
		`node --check foo.js`,
		`ls -la pkg`,
		`cat pkg/foo/bar.go`,
		`grep -rn "func" pkg`,
		`find . -name '*.go'`,
		`env`,
		`printenv`,
		`mkdir -p pkg/newdir`,
		`touch pkg/newfile.go`,
		`git status`,
		`go test ./... 2>&1 | tail -n 40`,
	}
	for _, c := range ok {
		if refuse, blocked := GuardShellWhitelist(c, nil); blocked {
			t.Errorf("OVER-BLOCKED: %q -> %s", c, refuse)
		}
	}
}

// `cmake --build <dir>` is the single canonical way to build a CMake project —
// no more dangerous than `go build` or `mvn test`, which run the project's own
// build too. Its path operands must stay inside the workspace, and cmake's
// script / file-mutator / installer modes stay refused.
func TestCmakeBuildAllowedOperandsPoliced(t *testing.T) {
	allowed := []string{
		`cmake --build build`,
		`cmake --build build -j 4`,
		`cmake --build build --target test`,
		`cmake --build=build`,
		`cmake -S . -B build`,
		`cmake -S . -B build -DCMAKE_BUILD_TYPE=Release`,
		`cmake -B build -S .`,
		`cmake .`,
		`cmake --version`,
		`cmake -S . -B build -G Ninja`,
		`cmake --build build && ctest --test-dir build`,
	}
	for _, c := range allowed {
		if refuse, blocked := GuardShellWhitelist(c, nil); blocked {
			t.Errorf("OVER-BLOCKED: %q -> %s", c, refuse)
		}
		if _, blocked := DangerousInvocation(c); blocked {
			t.Errorf("OVER-BLOCKED by DangerousInvocation: %q", c)
		}
	}

	refused := []string{
		// script / command-mode / installer: arbitrary code or writes.
		`cmake -P evil.cmake`,
		`cmake -E copy /etc/passwd out`,
		`cmake -E rm -rf build`,
		`cmake -C /tmp/initial-cache.cmake -S . -B build`,
		`cmake --install build --prefix /usr/local`,
		// build modes whose operand leaves the workspace.
		`cmake --build /tmp/pwned`,
		`cmake --build ../outside`,
		`cmake --build=/tmp/pwned`,
		`cmake -S . -B /var/tmp/out`,
		`cmake -B ../out -S .`,
		`cmake -S /etc -B build`,
		`cmake ..`,
		`cmake /tmp/othertree`,
		`cmake --build ~/elsewhere`,
	}
	for _, c := range refused {
		reason, blocked := GuardShellWhitelist(c, nil)
		if !blocked {
			t.Errorf("NOT BLOCKED: %q", c)
			continue
		}
		if reason == "" {
			t.Errorf("refusal without an explanation: %q", c)
		}
	}
}

// The refusal text must name the actual reason, not the generic
// "names another program to execute" line, which is false for -E/--install and
// teaches the model to retry the same class of command.
func TestCmakeRefusalsExplainThemselves(t *testing.T) {
	cases := map[string]string{
		`cmake -E copy a b`:                 "command mode",
		`cmake --install build`:             "--prefix",
		`cmake -P x.cmake`:                  "execute_process",
		`cmake -C init.cmake -S . -B build`: "initial-cache",
		`cmake --build /tmp/x`:              "outside the project root",
	}
	for cmd, want := range cases {
		reason, blocked := DangerousInvocation(cmd)
		if !blocked {
			t.Errorf("NOT BLOCKED: %q", cmd)
			continue
		}
		if !strings.Contains(reason, want) {
			t.Errorf("refusal for %q does not mention %q: %s", cmd, want, reason)
		}
	}
}
