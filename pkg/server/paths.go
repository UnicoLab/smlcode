package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/workspace"
)

// ErrPathEscape is returned when a requested path resolves outside the
// workspace root (prefix trickery, `..`, or a symlink pointing out of tree).
var ErrPathEscape = errors.New("path escapes workspace root")

// resolveWorkspacePath maps a client-supplied relative path onto an absolute
// path that is provably inside the workspace root.
//
// It fixes two classes of bug present in the naive
// `strings.HasPrefix(full, root)` check:
//
//   - prefix-without-separator: root "/home/u/proj" must NOT accept
//     "/home/u/proj-secrets/creds.env";
//   - symlink escape: "docs/link" -> "/etc" must not expose /etc/passwd.
//
// Both the root and the target are passed through filepath.EvalSymlinks. For
// targets that do not exist yet (review-queue writes) the nearest existing
// ancestor is resolved instead, and the remaining segments are appended.
func resolveWorkspacePath(root, rel string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", ErrPathEscape
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", ErrPathEscape
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		// Root itself missing/unreadable — fall back to the lexical form so we
		// still fail closed rather than open.
		realRoot = filepath.Clean(absRoot)
	}

	rel = strings.TrimSpace(rel)
	// Reject absolute inputs and NUL bytes outright.
	if strings.ContainsRune(rel, 0) {
		return "", ErrPathEscape
	}
	if filepath.IsAbs(rel) || (len(rel) > 1 && rel[1] == ':') {
		return "", ErrPathEscape
	}
	cleaned := filepath.Clean(filepath.FromSlash(rel))
	if cleaned == "." || cleaned == string(filepath.Separator) {
		cleaned = ""
	}
	candidate := filepath.Join(realRoot, cleaned)

	real, err := evalExisting(candidate)
	if err != nil {
		return "", ErrPathEscape
	}
	if !withinRoot(realRoot, real) {
		return "", ErrPathEscape
	}
	return real, nil
}

// evalExisting resolves symlinks for path, walking up to the nearest existing
// ancestor when path does not exist yet.
func evalExisting(path string) (string, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", os.ErrNotExist
	}
	realParent, err := evalExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(realParent, filepath.Base(path)), nil
}

// withinRoot reports whether target is root itself or lives under it, using a
// separator-terminated comparison so "/a/proj-secrets" is not "under" "/a/proj".
func withinRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if target == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(target, prefix)
}

// workspacePath resolves rel against the server's configured workspace root.
func (s *Server) workspacePath(rel string) (string, error) {
	return resolveWorkspacePath(s.rootDir(), rel)
}

// ErrSecretPath is returned for a workspace path that names harness credential
// state. The Studio file browser sits behind loopback + same-origin + a session
// token, but "authenticated" is not a reason to serve the operator's provider
// API keys over HTTP: any other local process holding the token, any --no-auth
// deployment, and the SPA's own file tree would all render them.
var ErrSecretPath = errors.New("path names harness credential state")

// workspaceReadPath is workspacePath plus the credential-file refusal. Use it
// for every endpoint that returns FILE CONTENT.
func (s *Server) workspaceReadPath(rel string) (string, error) {
	full, err := s.workspacePath(rel)
	if err != nil {
		return "", err
	}
	root := s.rootDir()
	if r, rerr := filepath.Rel(root, full); rerr == nil && workspace.IsHarnessSecretPath(r) {
		return "", ErrSecretPath
	}
	return full, nil
}
