package workspace

import "testing"

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
