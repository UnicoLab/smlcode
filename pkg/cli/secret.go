package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// Secrets are read, never echoed, and never printed back.
//
// An API key that reaches the terminal ends up in the scrollback, in a tmux
// capture and in whatever records the session. It also must not arrive as an
// argv word when it can be avoided: /proc/<pid>/cmdline is world-readable on
// Linux and the shell records the line in history.

// ReadSecret prompts on stderr and reads one line WITHOUT echo.
//
// It falls back to a plain (still un-echoed by the pipe) read when stdin is not
// a terminal, so `echo $KEY | slmcode auth set --stdin` works in CI.
func ReadSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return ReadSecretLine()
	}
	fmt.Fprint(os.Stderr, Bold(prompt))
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ReadSecretLine reads one line from stdin with no prompt and no echo control.
// Used for the piped/`--stdin` path.
func ReadSecretLine() (string, error) {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

// MaskSecret renders a secret as a non-reversible presence indicator: the
// length and, for keys long enough that the tail is not identifying, the last
// four characters. It never returns the leading characters, which is the half
// that identifies the provider account.
func MaskSecret(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return ""
	case len(s) <= 8:
		return strings.Repeat("•", len(s))
	default:
		return strings.Repeat("•", 8) + s[len(s)-4:]
	}
}
