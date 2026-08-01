package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/hitl"
	"github.com/UnicoLab/slmcode/pkg/hooks"
	"github.com/UnicoLab/slmcode/pkg/permissions"
	"github.com/piotrlaczkowski/GoLangGraph/pkg/tools"
)

// FileChangeFunc is invoked after a successful (or staged) write/edit/patch.
// kind is write|edit|patch|dry-run|review; detail is a short human summary / snippet.
type FileChangeFunc func(path, kind, detail string)

// ShellAskNotify is called when a shell command needs interactive approval.
type ShellAskNotify func(ask ShellAsk)

// ToolOpts configures workspace tool safety (Claude Code–style permissions).
type ToolOpts struct {
	DryRun          bool
	Permission      string // auto | dry-run | review (file writes)
	ShellPermission string // allow | ask | deny (ws_shell)
	SlmDir          string
	OnFileChange    FileChangeFunc
	Focus           *FocusGuard // optional anti-wander write allowlist
	Hooks           *hooks.Runner
	ShellAskTimeout time.Duration
	OnShellAsk      ShellAskNotify
	AutoApprove     bool // when true, shell ask acts as allow
}

// RegisterCodingTools adds workspace-aware file, shell, and git tools that
// work with real source trees (including .go and other code extensions).
func RegisterCodingTools(reg *tools.ToolRegistry, root string, dryRun bool) error {
	return RegisterCodingToolsOpts(reg, root, ToolOpts{DryRun: dryRun, Permission: permissions.ModeAuto})
}

// RegisterCodingToolsOpts is the full registration entrypoint.
func RegisterCodingToolsOpts(reg *tools.ToolRegistry, root string, opts ToolOpts) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	perm := permissions.Normalize(opts.Permission)
	if opts.DryRun {
		perm = permissions.ModeDryRun
	}
	ws := &Workspace{
		Root: root, DryRun: perm == permissions.ModeDryRun, Permission: perm,
		ShellPermission: permissions.NormalizeShell(opts.ShellPermission),
		SlmDir:          opts.SlmDir, OnFileChange: opts.OnFileChange, Focus: opts.Focus,
		ShellAskTimeout: opts.ShellAskTimeout, OnShellAsk: opts.OnShellAsk,
		AutoApprove:     opts.AutoApprove,
	}
	wrap := func(name string, fn tools.ToolExecutor) tools.ToolExecutor {
		return hooks.WrapHandler(opts.Hooks, name, fn)
	}

	defs := []tools.Tool{
		tools.NewGenericTool(
			"ws_read",
			"Read a file from the project workspace. Path is relative to project root.",
			wrap("ws_read", ws.readFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "Relative file path"},
				},
				"required": []string{"path"},
			},
		),
		tools.NewGenericTool(
			"ws_write",
			"Write/overwrite a file in the project workspace.",
			wrap("ws_write", ws.writeFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":    map[string]interface{}{"type": "string"},
					"content": map[string]interface{}{"type": "string"},
				},
				"required": []string{"path", "content"},
			},
		),
		tools.NewGenericTool(
			"ws_edit",
			"Replace old_str with new_str in a workspace file (exact match). Prefer small patches.",
			wrap("ws_edit", ws.editFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":        map[string]interface{}{"type": "string"},
					"old_str":     map[string]interface{}{"type": "string"},
					"new_str":     map[string]interface{}{"type": "string"},
					"replace_all": map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"path", "old_str", "new_str"},
			},
		),
		tools.NewGenericTool(
			"ws_patch",
			"Apply a small unified-diff style patch (---/+++/@@ hunks) or a SEARCH/REPLACE block to a file. Prefer over full rewrites for SLMs.",
			wrap("ws_patch", ws.patchFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":  map[string]interface{}{"type": "string", "description": "Target file (relative)"},
					"patch": map[string]interface{}{"type": "string", "description": "Unified diff or <<<<<<< SEARCH / ======= / >>>>>>> REPLACE block"},
				},
				"required": []string{"path", "patch"},
			},
		),
		tools.NewGenericTool(
			"ws_list",
			"List files/directories under a workspace path.",
			wrap("ws_list", ws.listDir),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "Relative directory (default .)"},
				},
			},
		),
		tools.NewGenericTool(
			"ws_glob",
			"Glob files under the workspace (e.g. **/*.go).",
			wrap("ws_glob", ws.glob),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{"type": "string"},
				},
				"required": []string{"pattern"},
			},
		),
		tools.NewGenericTool(
			"ws_grep",
			"Search file contents in the workspace with a substring or simple pattern.",
			wrap("ws_grep", ws.grep),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{"type": "string"},
					"glob":    map[string]interface{}{"type": "string", "description": "Optional file glob, e.g. *.go"},
					"path":    map[string]interface{}{"type": "string", "description": "Subdirectory to search"},
				},
				"required": []string{"pattern"},
			},
		),
		tools.NewGenericTool(
			"ws_mv",
			"Rename/move a file in the workspace (git mv when .git present, else os.Rename). Prefer for file renames over rewrite+leave-old.",
			wrap("ws_mv", ws.moveFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from": map[string]interface{}{"type": "string", "description": "Existing relative path"},
					"to":   map[string]interface{}{"type": "string", "description": "New relative path"},
				},
				"required": []string{"from", "to"},
			},
		),
		tools.NewGenericTool(
			"ws_delete",
			"Delete a file in the workspace after a successful rename/move. Focus-guarded; prefer ws_mv for renames.",
			wrap("ws_delete", ws.deleteFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
				"required": []string{"path"},
			},
		),
		tools.NewGenericTool(
			"ws_shell",
			"Run a shell command in the project root. Prefer tests/build over destructive ops.",
			wrap("ws_shell", ws.shell),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{"type": "string"},
				},
				"required": []string{"command"},
			},
		),
		tools.NewGenericTool(
			"git_status",
			"Show git status --short in the project.",
			wrap("git_status", ws.gitStatus),
			map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		),
		tools.NewGenericTool(
			"git_diff",
			"Show git diff (optionally for a path).",
			wrap("git_diff", ws.gitDiff),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
			},
		),
	}

	for _, t := range defs {
		if err := reg.RegisterTool(t); err != nil {
			// Ignore duplicates if registry already has the name
			if !strings.Contains(err.Error(), "already") {
				return err
			}
		}
	}
	return nil
}

// ShellAsk is the pending interactive shell approval payload.
type ShellAsk struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	CreatedAt string `json:"created_at"`
}

// ShellAnswer is the user decision for a pending shell command.
type ShellAnswer struct {
	Decision   string `json:"decision"` // approve | deny
	AnsweredAt string `json:"answered_at,omitempty"`
}

// Workspace is the root-jailed project filesystem.
type Workspace struct {
	Root            string
	DryRun          bool
	Permission      string
	ShellPermission string
	SlmDir          string
	OnFileChange    FileChangeFunc
	Focus           *FocusGuard
	ShellAskTimeout time.Duration
	OnShellAsk      ShellAskNotify
	AutoApprove     bool
}

func (w *Workspace) checkFocus(path string) error {
	if w == nil || w.Focus == nil {
		return nil
	}
	return w.Focus.Check(path)
}

func (w *Workspace) notify(path, kind, detail string) {
	if w != nil && w.OnFileChange != nil {
		w.OnFileChange(path, kind, detail)
	}
}

func (w *Workspace) guardWrite(path, kind, content string) (string, bool, error) {
	switch permissions.Normalize(w.Permission) {
	case permissions.ModeDryRun:
		return fmt.Sprintf("dry-run: would %s %s (%d bytes)", kind, path, len(content)), true, nil
	case permissions.ModeReview:
		if w.SlmDir == "" {
			w.SlmDir = filepath.Join(w.Root, ".slmcode")
		}
		p, err := permissions.RecordPending(w.SlmDir, path, kind, content)
		if err != nil {
			return "", true, err
		}
		return fmt.Sprintf("review: staged %s → %s (run `slmcode apply`)", path, filepath.Base(p)), true, nil
	default:
		return "", false, nil
	}
}

func (w *Workspace) resolve(rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	rel = filepath.Clean(rel)
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes workspace: %s", rel)
	}
	abs := filepath.Join(w.Root, rel)
	abs = filepath.Clean(abs)
	if abs != w.Root && !strings.HasPrefix(abs, w.Root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes workspace: %s", rel)
	}
	return abs, nil
}

func (w *Workspace) readFile(_ context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	const max = 100_000
	if len(data) > max {
		return string(data[:max]) + "\n...[truncated]", nil
	}
	return string(data), nil
}

func (w *Workspace) writeFile(_ context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := w.checkFocus(path); err != nil {
		return nil, err
	}
	if msg, stop, err := w.guardWrite(path, "write", content); stop {
		kind := "review"
		if strings.HasPrefix(msg, "dry-run:") {
			kind = "dry-run"
		}
		w.notify(path, kind, truncateSnippet(content, 400))
		return msg, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("wrote %s (%d bytes)", path, len(content))
	w.notify(path, "write", truncateSnippet(content, 400))
	return msg, nil
}

func (w *Workspace) editFile(_ context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	oldStr, _ := args["old_str"].(string)
	newStr, _ := args["new_str"].(string)
	replaceAll, _ := args["replace_all"].(bool)
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := w.checkFocus(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	text := string(data)
	if !strings.Contains(text, oldStr) {
		return nil, fmt.Errorf("old_str not found in %s", path)
	}
	var next string
	count := 1
	if replaceAll {
		count = strings.Count(text, oldStr)
		next = strings.ReplaceAll(text, oldStr, newStr)
	} else {
		next = strings.Replace(text, oldStr, newStr, 1)
	}
	snippet := diffSnippet(oldStr, newStr)
	if msg, stop, err := w.guardWrite(path, "edit", next); stop {
		if msg != "" && strings.HasPrefix(msg, "dry-run:") {
			out := fmt.Sprintf("dry-run: would edit %s (%d replacement(s))", path, count)
			w.notify(path, "dry-run", snippet)
			return out, err
		}
		w.notify(path, "review", snippet)
		return msg, err
	}
	if err := os.WriteFile(abs, []byte(next), 0o644); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("edited %s (%d replacement(s))", path, count)
	w.notify(path, "edit", snippet)
	return msg, nil
}

func (w *Workspace) patchFile(_ context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	patch, _ := args["patch"].(string)
	if strings.TrimSpace(patch) == "" {
		return nil, fmt.Errorf("patch required")
	}
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := w.checkFocus(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		// Allow creating new files via patch when missing and patch is SEARCH empty.
		if !os.IsNotExist(err) {
			return nil, err
		}
		data = nil
	}
	next, summary, err := ApplyPatch(string(data), patch)
	if err != nil {
		return nil, err
	}
	if msg, stop, err := w.guardWrite(path, "patch", next); stop {
		kind := "review"
		if strings.HasPrefix(msg, "dry-run:") {
			kind = "dry-run"
		}
		w.notify(path, kind, summary)
		return msg, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(abs, []byte(next), 0o644); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("patched %s (%s)", path, summary)
	w.notify(path, "patch", summary)
	return msg, nil
}

func (w *Workspace) moveFile(_ context.Context, args map[string]interface{}) (interface{}, error) {
	from, _ := args["from"].(string)
	to, _ := args["to"].(string)
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return nil, fmt.Errorf("from and to required")
	}
	fromAbs, err := w.resolve(from)
	if err != nil {
		return nil, err
	}
	toAbs, err := w.resolve(to)
	if err != nil {
		return nil, err
	}
	if err := w.checkFocus(from); err != nil {
		return nil, err
	}
	if err := w.checkFocus(to); err != nil {
		return nil, err
	}
	if _, err := os.Stat(fromAbs); err != nil {
		return nil, fmt.Errorf("source missing: %s", from)
	}
	if _, err := os.Stat(toAbs); err == nil {
		return nil, fmt.Errorf("destination already exists: %s", to)
	}
	content, _ := os.ReadFile(fromAbs)
	if msg, stop, err := w.guardWrite(to, "mv", string(content)); stop {
		kind := "review"
		if strings.HasPrefix(msg, "dry-run:") {
			kind = "dry-run"
			msg = fmt.Sprintf("dry-run: would mv %s → %s", from, to)
		}
		w.notify(to, kind, fmt.Sprintf("mv %s → %s", from, to))
		return msg, err
	}
	if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
		return nil, err
	}
	moved := false
	if w.hasGit() {
		cmd := exec.Command("git", "-C", w.Root, "mv", from, to)
		if out, err := cmd.CombinedOutput(); err == nil {
			moved = true
			_ = out
		}
	}
	if !moved {
		if err := os.Rename(fromAbs, toAbs); err != nil {
			// Cross-device fallback: write+delete
			if err := os.WriteFile(toAbs, content, 0o644); err != nil {
				return nil, err
			}
			if err := os.Remove(fromAbs); err != nil {
				return nil, fmt.Errorf("wrote %s but failed to remove %s: %w", to, from, err)
			}
		}
	}
	msg := fmt.Sprintf("moved %s → %s", from, to)
	w.notify(to, "mv", msg)
	w.notify(from, "delete", "renamed away")
	return msg, nil
}

func (w *Workspace) deleteFile(_ context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("path required")
	}
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := w.checkFocus(path); err != nil {
		return nil, err
	}
	if msg, stop, err := w.guardWrite(path, "delete", ""); stop {
		kind := "review"
		if strings.HasPrefix(msg, "dry-run:") {
			kind = "dry-run"
			msg = fmt.Sprintf("dry-run: would delete %s", path)
		}
		w.notify(path, kind, "delete "+path)
		return msg, err
	}
	if err := os.Remove(abs); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("deleted %s", path)
	w.notify(path, "delete", msg)
	return msg, nil
}

func (w *Workspace) listDir(_ context.Context, args map[string]interface{}) (interface{}, error) {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}
	return strings.Join(lines, "\n"), nil
}

func (w *Workspace) glob(_ context.Context, args map[string]interface{}) (interface{}, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("pattern required")
	}
	// Walk from root and match
	var matches []string
	_ = filepath.WalkDir(w.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(w.Root, path)
		ok, _ := filepath.Match(pattern, rel)
		if !ok {
			ok, _ = filepath.Match(pattern, filepath.Base(rel))
		}
		// Support **/*.ext style loosely
		if !ok && strings.HasPrefix(pattern, "**/") {
			ok, _ = filepath.Match(strings.TrimPrefix(pattern, "**/"), filepath.Base(rel))
			if !ok {
				ok, _ = filepath.Match(strings.TrimPrefix(pattern, "**/"), rel)
			}
		}
		if ok {
			matches = append(matches, rel)
			if len(matches) >= 200 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return strings.Join(matches, "\n"), nil
}

func (w *Workspace) grep(_ context.Context, args map[string]interface{}) (interface{}, error) {
	pattern, _ := args["pattern"].(string)
	globFilter, _ := args["glob"].(string)
	sub, _ := args["path"].(string)
	base := w.Root
	if sub != "" {
		var err error
		base, err = w.resolve(sub)
		if err != nil {
			return nil, err
		}
	}
	var hits []string
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if globFilter != "" {
			ok, _ := filepath.Match(globFilter, d.Name())
			if !ok {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 500_000 {
			return nil
		}
		rel, _ := filepath.Rel(w.Root, path)
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, pattern) {
				hits = append(hits, fmt.Sprintf("%s:%d:%s", rel, i+1, strings.TrimSpace(line)))
				if len(hits) >= 50 {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if len(hits) == 0 {
		return "no matches", nil
	}
	return strings.Join(hits, "\n"), nil
}

func (w *Workspace) shell(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	command, _ := args["command"].(string)
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("command required")
	}
	if w.DryRun {
		return "dry-run: " + command, nil
	}
	switch permissions.NormalizeShell(w.ShellPermission) {
	case permissions.ShellDeny:
		return nil, fmt.Errorf("shell denied by permission mode (shell=deny)")
	case permissions.ShellAsk:
		if w.AutoApprove {
			break // treat as allow
		}
		ok, err := w.waitShellApproval(ctx, command)
		if err != nil {
			return nil, err
		}
		if !ok {
			return "shell denied by user", nil
		}
		// approved → fall through to execute
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = w.Root
	out, err := cmd.CombinedOutput()
	text := string(out)
	if len(text) > 80_000 {
		text = text[:80_000] + "\n...[truncated]"
	}
	if err != nil {
		return fmt.Sprintf("exit error: %v\n%s", err, text), nil
	}
	return text, nil
}

func (w *Workspace) waitShellApproval(ctx context.Context, command string) (bool, error) {
	if w.SlmDir == "" {
		return false, fmt.Errorf("shell ask: no slm dir")
	}
	ask := ShellAsk{
		ID:        fmt.Sprintf("shell-%d", time.Now().UnixNano()),
		Command:   command,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := hitl.WriteAsk(w.SlmDir, "shell", ask); err != nil {
		return false, err
	}
	// Also record classic pending for apply CLI visibility.
	_, _ = permissions.RecordPending(w.SlmDir, "shell.sh", "shell", command)
	if w.OnShellAsk != nil {
		w.OnShellAsk(ask)
	}
	timeout := w.ShellAskTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	var ans ShellAnswer
	ok, err := hitl.WaitAnswers(ctx, w.SlmDir, "shell", timeout, &ans)
	hitl.Clear(w.SlmDir, "shell")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("shell ask timeout — command not executed: %s", command)
	}
	d := strings.ToLower(strings.TrimSpace(ans.Decision))
	return d == "" || d == "approve" || d == "allow" || d == "yes" || d == "ok", nil
}

func (w *Workspace) gitStatus(ctx context.Context, _ map[string]interface{}) (interface{}, error) {
	if !w.hasGit() {
		return "not a git repository", nil
	}
	return w.shell(ctx, map[string]interface{}{"command": "git status --short"})
}

func (w *Workspace) gitDiff(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if !w.hasGit() {
		return "not a git repository — no diff", nil
	}
	path, _ := args["path"].(string)
	cmd := "git diff --"
	if path != "" {
		cmd += " " + path
	}
	return w.shell(ctx, map[string]interface{}{"command": cmd})
}

func (w *Workspace) hasGit() bool {
	_, err := os.Stat(filepath.Join(w.Root, ".git"))
	return err == nil
}

// ToolNames returns the coding tool names for agent config.
func ToolNames() []string {
	return []string{
		"ws_read", "ws_write", "ws_edit", "ws_patch", "ws_mv", "ws_delete",
		"ws_list", "ws_glob", "ws_grep", "ws_shell", "git_status", "git_diff",
	}
}
