package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UnicoLab/slmcode/pkg/augment"
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

// DefaultReadWindow is the ws_read line window. SWE-agent's ablation put the
// sweet spot near 100 lines: 30-line windows cost 3.7 points and whole-file
// reads cost 5.3 points on SWE-bench relative to a ~100-line window.
const DefaultReadWindow = 120

// DefaultMaxToolChars caps EVERY tool result (~2k tokens). A single oversized
// result can evict the task description from a 7B model's context.
const DefaultMaxToolChars = 8000

// MaxGrepHits / MaxGlobHits bound search results; both announce truncation.
const (
	MaxGrepHits = 50
	MaxGlobHits = 200
)

// lineNumberPrefixRe detects text pasted straight out of a ws_read result
// ("   42|func foo() {"). Such an old_str can NEVER match the file.
var lineNumberPrefixRe = regexp.MustCompile(`(?m)^\s*\d+\|`)

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

	// SLM invariants (little-coder ports). Zero value = enabled (default ON).
	// Set the *Disable flags to opt out.
	DisableWriteGuard      bool         // allow ws_write to overwrite existing files
	DisableReadBeforeEdit  bool         // allow edit/patch without prior ws_read
	DisableShellWriteGuard bool         // allow cat>/tee redirects that clobber files
	DisableOverEditGuard   bool         // allow whole-file-style edits
	DisableSyntaxCheck     bool         // skip post-edit syntax check + revert
	Reads                  *ReadTracker // optional shared tracker; created if nil
	ReadHeadLines          int          // auto-trim head lines (default 80)
	MaxContextKB           int          // for read-guard budget (default 32)
	// ReadWindowLines is the default ws_read window (0 = DefaultReadWindow).
	ReadWindowLines int
	// MaxToolChars caps every tool result (0 = DefaultMaxToolChars).
	MaxToolChars int
	// ShellTimeout bounds ws_shell (0 = DefaultShellTimeout).
	ShellTimeout time.Duration
	// QualityMonitor enables mid-ReAct repeated-tool refusal (loopguard).
	QualityMonitor bool
	// ShellWhitelist enforces SAFE_PREFIXES (little-coder permission-gate).
	ShellWhitelist bool
	ShellAllow     []string
	// Checkpoints enables first-write-wins file backups.
	Checkpoints       bool
	CheckpointSession string
	// OnIntervention reports harness gate refusals to TUI/Studio.
	OnIntervention func(reason, message string)
}

// RegisterCodingTools adds workspace-aware file, shell, and git tools that
// work with real source trees (including .go and other code extensions).
func RegisterCodingTools(reg *tools.ToolRegistry, root string, dryRun bool) error {
	return RegisterCodingToolsOpts(reg, root, ToolOpts{DryRun: dryRun, Permission: permissions.ModeAuto})
}

// RegisterCodingToolsOpts is the full registration entrypoint.
func RegisterCodingToolsOpts(reg *tools.ToolRegistry, root string, opts ToolOpts) error {
	_, _, err := RegisterCodingToolsWithWorkspace(reg, root, opts)
	return err
}

// RegisterCodingToolsWithWorkspace is RegisterCodingToolsOpts plus a handle on
// the constructed Workspace and loop tracker, so the orchestrator can call
// CallTracker.ResetTask at task start and pass the task id into the context
// with WithTaskID.
func RegisterCodingToolsWithWorkspace(reg *tools.ToolRegistry, root string, opts ToolOpts) (*Workspace, *CallTracker, error) {
	ws, loop, err := NewWorkspace(root, opts)
	if err != nil {
		return nil, nil, err
	}
	wrap := func(name string, fn tools.ToolExecutor) tools.ToolExecutor {
		fn = ws.capped(fn)
		fn = hooks.WrapHandler(opts.Hooks, name, fn)
		if loop != nil {
			fn = loop.Wrap(name, fn)
		}
		return fn
	}
	for _, t := range ws.toolDefs(wrap) {
		if err := reg.RegisterTool(t); err != nil {
			if !strings.Contains(err.Error(), "already") {
				return nil, nil, err
			}
		}
	}
	return ws, loop, nil
}

// NewWorkspace builds the jailed workspace and (optionally) its loop tracker.
// Exposed so a caller can hold the tracker and call ResetTask per task.
func NewWorkspace(root string, opts ToolOpts) (*Workspace, *CallTracker, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	perm := permissions.Normalize(opts.Permission)
	if opts.DryRun {
		perm = permissions.ModeDryRun
	}
	reads := opts.Reads
	if reads == nil {
		reads = NewReadTracker()
	}
	ws := &Workspace{
		Root: root, DryRun: perm == permissions.ModeDryRun, Permission: perm,
		ShellPermission: permissions.NormalizeShell(opts.ShellPermission),
		SlmDir:          opts.SlmDir, OnFileChange: opts.OnFileChange, Focus: opts.Focus,
		ShellAskTimeout: opts.ShellAskTimeout, OnShellAsk: opts.OnShellAsk,
		AutoApprove:     opts.AutoApprove,
		WriteGuard:      !opts.DisableWriteGuard,
		ReadBeforeEdit:  !opts.DisableReadBeforeEdit,
		ShellWriteGuard: !opts.DisableShellWriteGuard,
		OverEditGuard:   !opts.DisableOverEditGuard,
		SyntaxCheck:     !opts.DisableSyntaxCheck,
		Reads:           reads,
		ReadHeadLines:   opts.ReadHeadLines,
		ReadWindow:      opts.ReadWindowLines,
		MaxContextKB:    opts.MaxContextKB,
		MaxToolChars:    opts.MaxToolChars,
		ShellTimeout:    opts.ShellTimeout,
		ShellWhitelist:  opts.ShellWhitelist,
		ShellAllow:      opts.ShellAllow,
		OnIntervention:  opts.OnIntervention,
	}
	if opts.Checkpoints && opts.SlmDir != "" {
		ws.Checkpointer = NewFileCheckpointer(opts.SlmDir, root, opts.CheckpointSession)
	}
	var loop *CallTracker
	if opts.QualityMonitor {
		loop = NewCallTracker()
		loop.OnIntervention = opts.OnIntervention
	}
	ws.Loop = loop
	return ws, loop, nil
}

func (w *Workspace) toolDefs(wrap func(string, tools.ToolExecutor) tools.ToolExecutor) []tools.Tool {
	return []tools.Tool{
		tools.NewGenericTool(
			"ws_read",
			fmt.Sprintf(
				"Read a file as numbered lines (default window: %d lines from offset). "+
					"Required before ws_edit/ws_patch. Use offset/limit to page through a big file; "+
					"the result tells you the total line count. "+
					"IMPORTANT: the leading `   42|` line numbers are display only — "+
					"NEVER include them in ws_edit old_str or a ws_patch body.",
				w.readWindow()),
			wrap("ws_read", w.readFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":   map[string]interface{}{"type": "string", "description": "Relative file path"},
					"offset": map[string]interface{}{"type": "integer", "description": "1-based start line (default 1)"},
					"limit":  map[string]interface{}{"type": "integer", "description": "Max lines to return (default window)"},
				},
				"required": []string{"path"},
			},
		),
		tools.NewGenericTool(
			"ws_write",
			"Create a NEW file with the given content. Overwriting an existing file is only "+
				"allowed after you have ws_read it in this session — prefer ws_edit/ws_patch for changes.",
			wrap("ws_write", w.writeFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":         map[string]interface{}{"type": "string"},
					"content":      map[string]interface{}{"type": "string"},
					"allow_shrink": map[string]interface{}{"type": "boolean", "description": "Confirm an intentional large truncation of an existing file"},
				},
				"required": []string{"path", "content"},
			},
		),
		tools.NewGenericTool(
			"ws_edit",
			"Replace old_str with new_str in an existing file. File must be ws_read first this session. "+
				"old_str must be the exact current text WITHOUT ws_read's line-number prefix, and must be "+
				"unique — include 2–3 surrounding lines. Minor whitespace/indent drift is tolerated and reported. "+
				"old_str may not be empty: to create a file use ws_write, to append anchor on the last 2–3 lines.",
			wrap("ws_edit", w.editFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":        map[string]interface{}{"type": "string"},
					"old_str":     map[string]interface{}{"type": "string", "description": "Exact existing text (no line numbers, non-empty)"},
					"new_str":     map[string]interface{}{"type": "string"},
					"replace_all": map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"path", "old_str", "new_str"},
			},
		),
		tools.NewGenericTool(
			"ws_patch",
			"Apply a unified diff or a SEARCH/REPLACE block. Multi-hunk diffs are applied hunk by hunk, "+
				"anchored on the @@ line numbers, and are all-or-nothing: if one hunk misses, nothing is "+
				"written and you get a per-hunk report. Existing files must be ws_read first.",
			wrap("ws_patch", w.patchFile),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":  map[string]interface{}{"type": "string", "description": "Target file (relative)"},
					"patch": map[string]interface{}{"type": "string", "description": "Unified diff (@@ hunks) or <<<<<<< SEARCH / ======= / >>>>>>> REPLACE block"},
				},
				"required": []string{"path", "patch"},
			},
		),
		tools.NewGenericTool(
			"ws_list",
			"List files/directories under a workspace path. Returns an explicit message when the "+
				"directory is empty or missing.",
			wrap("ws_list", w.listDir),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "Relative directory (default .)"},
				},
			},
		),
		tools.NewGenericTool(
			"ws_glob",
			"Find files by path pattern. Supports ** for any number of directories: "+
				"`**/*.go`, `pkg/**/*_test.go`, `cmd/*/main.go`.",
			wrap("ws_glob", w.glob),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{"type": "string", "description": "Glob, e.g. pkg/**/*.go"},
				},
				"required": []string{"pattern"},
			},
		),
		tools.NewGenericTool(
			"ws_grep",
			"Search file contents with a REGULAR EXPRESSION (Go/RE2 syntax). If the pattern is not a "+
				"valid regex it is used as a literal substring and the result says so. "+
				"Narrow with glob= (e.g. *.go) and path= (subdirectory).",
			wrap("ws_grep", w.grep),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{"type": "string", "description": "Regex, e.g. func\\s+New\\w+"},
					"glob":    map[string]interface{}{"type": "string", "description": "Optional file glob, e.g. *.go"},
					"path":    map[string]interface{}{"type": "string", "description": "Subdirectory to search"},
				},
				"required": []string{"pattern"},
			},
		),
		tools.NewGenericTool(
			"ws_mv",
			"Rename/move a file in the workspace (git mv when .git present, else rename). "+
				"Prefer this over rewrite+leave-old.",
			wrap("ws_mv", w.moveFile),
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
			"Delete a file in the workspace. Irreversible except via checkpoint — prefer ws_mv for renames.",
			wrap("ws_delete", w.deleteFile),
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
			fmt.Sprintf(
				"Run ONE shell command in the project root (no command substitution, no backgrounding). "+
					"Killed after %s. Use it for tests/build/lint — never to edit files.",
				w.shellTimeout().Round(time.Second)),
			wrap("ws_shell", w.shell),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command":     map[string]interface{}{"type": "string"},
					"timeout_sec": map[string]interface{}{"type": "integer", "description": "Optional override, capped by the harness"},
				},
				"required": []string{"command"},
			},
		),
		tools.NewGenericTool(
			"ws_todo",
			"Write or replace your short task checklist. The list is echoed back so your plan stays "+
				"in recent context. Call it after planning and after finishing each item.",
			wrap("ws_todo", w.todoTool),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"todos": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": `Checklist, e.g. ["read pkg/x/y.go", "[x] add nil check", "run go test ./pkg/x"]`,
					},
				},
			},
		),
		tools.NewGenericTool(
			"git_status",
			"Show git status --short in the project.",
			wrap("git_status", w.gitStatus),
			map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		),
		tools.NewGenericTool(
			"git_diff",
			"Show git diff (optionally for a path).",
			wrap("git_diff", w.gitDiff),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
			},
		),
	}
}

// ShellAsk is the pending interactive shell approval payload.
type ShellAsk struct {
	ID        string `json:"id"`
	Kind      string `json:"kind,omitempty"`
	Command   string `json:"command"`
	TimeoutS  int    `json:"timeout_sec,omitempty"`
	OnTimeout string `json:"on_timeout,omitempty"` // "deny"
	CreatedAt string `json:"created_at"`
}

// ShellAnswer is the user decision for a pending shell command.
type ShellAnswer struct {
	AskID      string `json:"ask_id,omitempty"`
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

	WriteGuard      bool
	ReadBeforeEdit  bool
	ShellWriteGuard bool
	OverEditGuard   bool
	// SyntaxCheck runs a cheap parse check after every write/edit/patch and
	// REVERTS an edit that introduces a new syntax error. Default ON via
	// RegisterCodingToolsOpts; the zero value is off so a bare &Workspace{}
	// (tests, embedders) keeps the old behaviour.
	SyntaxCheck        bool
	SyntaxCheckTimeout time.Duration
	ShellWhitelist     bool
	ShellAllow         []string
	Reads              *ReadTracker
	Checkpointer       *FileCheckpointer
	Loop               *CallTracker
	OnIntervention     func(reason, message string)
	// ReadHeadLines caps auto-trimmed full-file reads (0 = default 80).
	ReadHeadLines int
	// ReadWindow is the default ws_read line window (0 = DefaultReadWindow).
	ReadWindow int
	// MaxContextKB informs read-guard trim decisions (0 = 32).
	MaxContextKB int
	// MaxToolChars caps every tool result (0 = DefaultMaxToolChars).
	MaxToolChars int
	// ShellTimeout bounds ws_shell (0 = DefaultShellTimeout).
	ShellTimeout time.Duration

	todoMu sync.Mutex
	todos  []TodoItem

	rootOnce sync.Once
	realRoot string
}

func (w *Workspace) readWindow() int {
	if w != nil && w.ReadWindow > 0 {
		return w.ReadWindow
	}
	return DefaultReadWindow
}

func (w *Workspace) maxToolChars() int {
	if w != nil && w.MaxToolChars > 0 {
		return w.MaxToolChars
	}
	return DefaultMaxToolChars
}

func (w *Workspace) shellTimeout() time.Duration {
	if w != nil && w.ShellTimeout > 0 {
		return w.ShellTimeout
	}
	return DefaultShellTimeout
}

func (w *Workspace) intervene(reason, message string) {
	if w != nil && w.OnIntervention != nil && message != "" {
		w.OnIntervention(reason, message)
	}
}

func (w *Workspace) backup(path string) {
	if w != nil && w.Checkpointer != nil {
		w.Checkpointer.BackupIfNeeded(path)
	}
}

// checkFocus enforces both the harness-state boundary and the focus allowlist.
func (w *Workspace) checkFocus(path string) error {
	if err := CheckHarnessStateWrite(path); err != nil {
		return err
	}
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

// capped truncates every string tool result and appends steering text.
func (w *Workspace) capped(fn tools.ToolExecutor) tools.ToolExecutor {
	if fn == nil {
		return fn
	}
	return func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		out, err := fn(ctx, args)
		if err != nil {
			return out, err
		}
		s, ok := out.(string)
		if !ok {
			return out, nil
		}
		return w.capResult(s), nil
	}
}

// capResult applies the head+tail cap with an explicit narrowing instruction.
func (w *Workspace) capResult(s string) string {
	max := w.maxToolChars()
	if len(s) <= max {
		return s
	}
	return truncateToolOutput(s, max) +
		fmt.Sprintf(
			"\n\n[result truncated: %d chars total, %d shown]\n"+
				"NARROW THE QUERY — do not re-run this call unchanged:\n"+
				"  • ws_read: pass offset= and limit= for the span you need\n"+
				"  • ws_grep: add path= / glob=, or use a longer, more specific pattern\n"+
				"  • ws_glob: use a deeper pattern (pkg/foo/**/*.go instead of **/*.go)\n"+
				"  • ws_shell: target one package or test, or pipe through `tail -n 40`",
			len(s), max)
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

// resolve maps a relative path to an absolute one inside the jail.
//
// The lexical check alone was escapable: a symlink committed inside the repo
// (or created by a previous shell command) pointed anywhere on the host and
// both reads and writes followed it. We additionally resolve the deepest
// EXISTING ancestor with EvalSymlinks and require it to stay under the root.
func (w *Workspace) resolve(rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf(
			"path escapes workspace: %s — use a path relative to the project root (e.g. pkg/foo/bar.go)", rel)
	}
	abs := filepath.Clean(filepath.Join(w.Root, rel))
	if abs != w.Root && !strings.HasPrefix(abs, w.Root+string(os.PathSeparator)) {
		return "", fmt.Errorf(
			"path escapes workspace: %s — use a path relative to the project root (e.g. pkg/foo/bar.go)", rel)
	}
	if err := w.checkSymlinkEscape(abs, rel); err != nil {
		return "", err
	}
	return abs, nil
}

// resolvedRoot is the root with symlinks evaluated (macOS /tmp → /private/tmp).
func (w *Workspace) resolvedRoot() string {
	w.rootOnce.Do(func() {
		if r, err := filepath.EvalSymlinks(w.Root); err == nil {
			w.realRoot = r
		} else {
			w.realRoot = w.Root
		}
	})
	return w.realRoot
}

func (w *Workspace) checkSymlinkEscape(abs, rel string) error {
	real := w.resolvedRoot()
	// Walk up to the deepest component that actually exists; components that
	// do not exist yet cannot be symlinks.
	p := abs
	for {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			return nil
		}
		p = parent
	}
	evaluated, err := filepath.EvalSymlinks(p)
	if err != nil {
		// Cannot evaluate (permissions, race) → fall back to the lexical check.
		return nil
	}
	if evaluated == real || strings.HasPrefix(evaluated, real+string(os.PathSeparator)) {
		return nil
	}
	return fmt.Errorf(
		"path escapes workspace via symlink: %s → %s. "+
			"The harness only operates on real files inside the project root; "+
			"pick a path that stays in the project", rel, evaluated)
}

func (w *Workspace) readFile(_ context.Context, args map[string]interface{}) (interface{}, error) {
	path := w.normalizeRelPath(strArg(args, "path"))
	if strings.TrimSpace(path) == "" {
		return "ws_read: path is required. Pass a project-relative file path, e.g. {\"path\":\"pkg/foo/bar.go\"}.", nil
	}
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return readErrorHint(path, err), nil
	}
	if st.IsDir() {
		return fmt.Sprintf(
			"%s is a directory, not a file. Use ws_list {\"path\":\"%s\"} to see what is inside.",
			path, path), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return readErrorHint(path, err), nil
	}
	w.markRead(path)
	text := string(data)
	lines := strings.Split(text, "\n")
	total := len(lines)

	offset := intArg(args, "offset", 1)
	limit := intArg(args, "limit", 0)
	if offset < 1 {
		offset = 1
	}
	if offset > total {
		return fmt.Sprintf(
			"%s has %d lines; offset %d is past the end. Re-read with offset between 1 and %d.",
			path, total, offset, total), nil
	}

	// Default to a fixed window rather than the whole file.
	if limit <= 0 {
		limit = w.readWindow()
	}
	// Hard ceiling: never let one read eat more than ~15% of the context window.
	if capped := w.readBudgetLines(text, total); capped > 0 && limit > capped {
		limit = capped
	}

	end := offset - 1 + limit
	if end > total {
		end = total
	}
	start := offset - 1
	slice := lines[start:end]
	var b strings.Builder
	for i, ln := range slice {
		fmt.Fprintf(&b, "%6d|%s\n", start+i+1, ln)
	}
	out := strings.TrimRight(b.String(), "\n")
	if start > 0 || end < total {
		out += fmt.Sprintf(
			"\n\n[showing lines %d–%d of %d in %s; use offset= to see more]",
			start+1, end, total, path)
		if end < total {
			out += fmt.Sprintf(
				"\nNext page: ws_read {\"path\":\"%s\",\"offset\":%d,\"limit\":%d}. "+
					"To jump straight to a symbol use ws_grep first.",
				path, end+1, limit)
		}
	}
	return out, nil
}

// readBudgetLines returns a line cap derived from the context budget, or 0.
func (w *Workspace) readBudgetLines(text string, total int) int {
	if total <= 0 {
		return 0
	}
	ctxKB := w.MaxContextKB
	if ctxKB <= 0 {
		ctxKB = 32
	}
	windowTok := (ctxKB * 1024) / 4
	budgetTok := windowTok * 15 / 100
	if budgetTok < 256 {
		budgetTok = 256
	}
	avgLineBytes := (len(text) + total) / total
	if avgLineBytes <= 0 {
		avgLineBytes = 1
	}
	capLines := (budgetTok * 4) / avgLineBytes
	head := w.ReadHeadLines
	if head <= 0 {
		head = 80
	}
	if capLines < head {
		capLines = head
	}
	return capLines
}

func readErrorHint(path string, err error) string {
	if os.IsNotExist(err) {
		return fmt.Sprintf(
			"%s does not exist. Use ws_glob (e.g. {\"pattern\":\"**/%s\"}) or ws_list to find the real path "+
				"before reading. If you meant to CREATE it, use ws_write.",
			path, filepath.Base(path))
	}
	if os.IsPermission(err) {
		return fmt.Sprintf("%s cannot be read (permission denied). Pick a different file.", path)
	}
	return fmt.Sprintf("%s could not be read: %v. Check the path with ws_list.", path, err)
}

func (w *Workspace) writeFile(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path := w.normalizeRelPath(strArg(args, "path"))
	content := strArg(args, "content")
	allowShrink := boolArg(args, "allow_shrink", false)
	if strings.TrimSpace(path) == "" {
		return "ws_write: path is required, e.g. {\"path\":\"pkg/foo/bar.go\",\"content\":\"…\"}.", nil
	}
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := w.checkFocus(path); err != nil {
		return nil, err
	}
	prev, existed := readIfExists(abs)
	if w.WriteGuard {
		if refuse, reason := w.checkOverwrite(path, abs, existed, prev, content, allowShrink); refuse {
			return reason, nil
		}
	}
	if msg, stop, err := w.guardWrite(path, "write", content); stop {
		kind := "review"
		if strings.HasPrefix(msg, "dry-run:") {
			kind = "dry-run"
		}
		w.notify(path, kind, truncateSnippet(content, 400))
		return msg, err
	}
	w.backup(path)
	note, reverted, err := w.applyWithSyntaxGuard(ctx, path, abs, prev, content, existed)
	if err != nil {
		return nil, err
	}
	if reverted {
		return note, nil
	}
	w.markRead(path) // authored → known for follow-up edit
	verb := "wrote"
	if existed {
		verb = "overwrote"
	}
	msg := fmt.Sprintf("%s %s (%d bytes)", verb, path, len(content)) + note
	w.notify(path, "write", truncateSnippet(content, 400))
	return msg, nil
}

// checkOverwrite implements the write guard with an escape hatch: a file the
// model has actually READ this session may be rewritten (that is the only way
// out of a repeated-failed-edit loop), while an unread file is still refused.
func (w *Workspace) checkOverwrite(rel, abs string, existed bool, prev, next string, allowShrink bool) (bool, string) {
	if IsReservedDeviceName(abs) {
		return true, fmt.Sprintf(
			"Write refused — %s uses a reserved device name (nul/con/com/lpt). Pick a real filename.",
			filepath.Base(abs))
	}
	if st, err := os.Stat(abs); err == nil && st.IsDir() {
		return true, fmt.Sprintf(
			"Write refused — %s is a directory. Pass a file path such as %s/main.go.", rel, rel)
	}
	if !existed {
		return false, ""
	}
	if !w.Reads.Has(filepath.Clean(rel)) {
		return true, WriteRefuseReason(rel) + augment.FailureRecovery("ws_write", rel)
	}
	// Catastrophic-truncation guard: rewriting a 900-line file as 12 lines is
	// almost always a model that lost the file's contents, not an intent.
	if !allowShrink && len(prev) >= 400 && len(next)*4 < len(prev) {
		return true, fmt.Sprintf(
			"Write refused — this would shrink %s from %d to %d bytes (%.0f%% of the original).\n"+
				"That usually means the replacement content is incomplete.\n"+
				"If you only need to change part of the file, use ws_edit / ws_patch.\n"+
				"If the truncation really is intended, repeat the call with \"allow_shrink\": true.",
			rel, len(prev), len(next), float64(len(next))/float64(len(prev))*100)
	}
	return false, ""
}

func readIfExists(abs string) (string, bool) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (w *Workspace) editFile(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path := w.normalizeRelPath(strArg(args, "path"))
	oldStr := strArg(args, "old_str")
	newStr := strArg(args, "new_str")
	replaceAll := boolArg(args, "replace_all", false)
	if strings.TrimSpace(path) == "" {
		return "ws_edit: path is required, e.g. {\"path\":\"pkg/foo/bar.go\",\"old_str\":\"…\",\"new_str\":\"…\"}.", nil
	}
	// An empty old_str used to pass strings.Contains(text, "") and silently
	// PREPEND new_str (or, with replace_all, insert it between every single
	// character) while reporting success. It is never a valid edit.
	if strings.TrimSpace(oldStr) == "" {
		return EmptyOldStrReason(path), nil
	}
	if lineNumberPrefixRe.MatchString(oldStr) {
		return LineNumberedOldStrReason(path), nil
	}
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := w.checkFocus(path); err != nil {
		return nil, err
	}
	if err := w.requireRead(path); err != nil {
		return err.Error(), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return readErrorHint(path, err), nil
	}
	text := string(data)
	if oldStr == newStr {
		return "No-op edit refused — old_str and new_str are identical. Change something real, or finish with status JSON.", nil
	}
	if w.OverEditGuard {
		if msg := AssessOverEditFor("ws_edit", text, oldStr, newStr); msg != "" {
			return msg + augment.FailureRecovery("ws_edit", path), nil
		}
	}

	var next string
	count := 1
	strategy := MatchExact
	exact := strings.Count(text, oldStr)
	switch {
	case replaceAll && exact >= 1:
		count = exact
		next = strings.ReplaceAll(text, oldStr, newStr)
	case exact == 1:
		next = strings.Replace(text, oldStr, newStr, 1)
	case exact > 1:
		return fmt.Sprintf(
			"old_str found %d times in %s — pass replace_all:true, or include more surrounding "+
				"context (2–3 lines above and below) to make the match unique. Do NOT use ws_write.",
			exact, path,
		) + augment.FailureRecovery("ws_edit", path), nil
	default:
		// Exact match missed: run the tolerant fallback ladder.
		res := FindEditMatch(text, oldStr)
		if !res.Found {
			if res.Ambiguous {
				return AmbiguityMessage(path, res.AmbigN, res.AmbigWhat) +
					augment.FailureRecovery("ws_edit", path), nil
			}
			msg := EditNotFoundReason(path)
			if tip := fuzzyEditHint(text, oldStr); tip != "" {
				msg += "\n\n" + tip
			}
			return msg + augment.FailureRecovery("ws_edit", path), nil
		}
		strategy = res.Match.Strategy
		next = ApplyEditReplacement(text, res.Match, oldStr, newStr)
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
	w.backup(path)
	note, reverted, err := w.applyWithSyntaxGuard(ctx, path, abs, text, next, true)
	if err != nil {
		return nil, err
	}
	if reverted {
		return note, nil
	}
	w.markRead(path)
	msg := fmt.Sprintf("edited %s (%d replacement(s))%s%s",
		path, count, StrategyNote(strategy), note)
	w.notify(path, "edit", snippet)
	return msg, nil
}

// EmptyOldStrReason explains why an empty old_str is refused.
func EmptyOldStrReason(path string) string {
	return fmt.Sprintf(
		"Edit refused — old_str is empty (or only whitespace). An empty search matches nothing "+
			"meaningful and would corrupt %s.\n\n"+
			"WHAT YOU PROBABLY WANT:\n"+
			"  • Creating a new file → use ws_write with the full content.\n"+
			"  • Appending to the end of a file → set old_str to the LAST 2–3 lines currently in the "+
			"file and set new_str to those same lines followed by your new text.\n"+
			"  • Inserting before something → set old_str to the 2–3 lines you want to insert before "+
			"and repeat them at the end of new_str.\n"+
			"ws_read the file first if you do not know its exact current text.",
		path)
}

// LineNumberedOldStrReason catches text pasted from a ws_read result.
func LineNumberedOldStrReason(path string) string {
	return fmt.Sprintf(
		"Edit refused — old_str still contains ws_read's line-number prefix (like `   42|`).\n"+
			"Those numbers are display only; they are NOT in %s, so this can never match.\n\n"+
			"Strip everything up to and including the `|` on every line, keeping the original "+
			"indentation, then retry. Example:\n"+
			"  from ws_read:   42|    if err != nil {\n"+
			"  in old_str:         if err != nil {",
		path)
}

func (w *Workspace) patchFile(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	path := w.normalizeRelPath(strArg(args, "path"))
	patch := strArg(args, "patch")
	if strings.TrimSpace(path) == "" {
		return "ws_patch: path is required, e.g. {\"path\":\"pkg/foo/bar.go\",\"patch\":\"@@ …\"}.", nil
	}
	if strings.TrimSpace(patch) == "" {
		return "ws_patch: patch is required — supply a unified diff with @@ hunks, or a " +
			"<<<<<<< SEARCH / ======= / >>>>>>> REPLACE block.", nil
	}
	if lineNumberPrefixRe.MatchString(patch) {
		return LineNumberedOldStrReason(path), nil
	}
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := w.checkFocus(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	existed := err == nil
	if err != nil {
		// Allow creating new files via patch when missing and patch is SEARCH empty.
		if !os.IsNotExist(err) {
			return readErrorHint(path, err), nil
		}
		data = nil
	} else if err := w.requireRead(path); err != nil {
		return err.Error(), nil
	}
	prev := string(data)
	next, summary, err := ApplyPatch(prev, patch)
	if err != nil {
		return err.Error() + augment.FailureRecovery("ws_patch", path), nil
	}
	// ws_patch used to sail straight past the over-edit guard, which made
	// "emit the whole file as a diff" the cheapest way around it.
	if w.OverEditGuard && existed {
		if msg := AssessOverEditFor("ws_patch", prev, prev, next); msg != "" && len(next) > 0 {
			if float64(commonPrefixLen(prev, next))/float64(len(prev)) < 0.10 {
				return msg + augment.FailureRecovery("ws_patch", path), nil
			}
		}
	}
	if msg, stop, err := w.guardWrite(path, "patch", next); stop {
		kind := "review"
		if strings.HasPrefix(msg, "dry-run:") {
			kind = "dry-run"
		}
		w.notify(path, kind, summary)
		return msg, err
	}
	w.backup(path)
	note, reverted, err := w.applyWithSyntaxGuard(ctx, path, abs, prev, next, existed)
	if err != nil {
		return nil, err
	}
	if reverted {
		return note, nil
	}
	w.markRead(path)
	msg := fmt.Sprintf("patched %s (%s)%s", path, summary, note)
	w.notify(path, "patch", summary)
	return msg, nil
}

// commonPrefixLen measures how much of the file the patch left untouched — a
// cheap proxy for "was this a surgical change or a rewrite?".
func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func (w *Workspace) moveFile(_ context.Context, args map[string]interface{}) (interface{}, error) {
	from := w.normalizeRelPath(strArg(args, "from"))
	to := w.normalizeRelPath(strArg(args, "to"))
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return "ws_mv: both from and to are required, e.g. {\"from\":\"a/old.go\",\"to\":\"a/new.go\"}.", nil
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
	srcInfo, err := os.Stat(fromAbs)
	if err != nil {
		return fmt.Sprintf(
			"ws_mv: source %s does not exist. Use ws_list or ws_glob to find the real path first.", from), nil
	}
	if srcInfo.IsDir() {
		return fmt.Sprintf(
			"ws_mv: %s is a directory. This tool moves single files; move them one at a time.", from), nil
	}
	if _, err := os.Stat(toAbs); err == nil {
		return fmt.Sprintf(
			"ws_mv: destination %s already exists. Pick a different name, or ws_delete the destination "+
				"first if replacing it is really intended.", to), nil
	}
	// The only two irreversible operations were the only two that never
	// checkpointed. Back up BOTH endpoints before touching anything.
	content, readErr := os.ReadFile(fromAbs)
	if readErr != nil {
		return fmt.Sprintf("ws_mv: cannot read %s: %v. Fix the path or permissions, then retry.", from, readErr), nil
	}
	if msg, stop, err := w.guardWrite(to, "mv", string(content)); stop {
		kind := "review"
		if strings.HasPrefix(msg, "dry-run:") {
			kind = "dry-run"
			msg = fmt.Sprintf("dry-run: would mv %s → %s", from, to)
		}
		w.notify(to, kind, fmt.Sprintf("mv %s → %s", from, to))
		return msg, err
	}
	w.backup(from)
	w.backup(to)
	if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
		return nil, err
	}
	moved := false
	if w.hasGit() {
		cmd := exec.Command("git", "-C", w.Root, "mv", from, to)
		if _, err := cmd.CombinedOutput(); err == nil {
			moved = true
		}
	}
	if !moved {
		if err := os.Rename(fromAbs, toAbs); err != nil {
			// Cross-device (or otherwise un-renamable): copy, VERIFY, then remove.
			// The old fallback discarded the ReadFile error and could leave a
			// zero-byte destination while deleting the source.
			if werr := os.WriteFile(toAbs, content, srcInfo.Mode().Perm()); werr != nil {
				return fmt.Sprintf(
					"ws_mv failed: could not write %s (%v). %s is untouched — pick a writable destination.",
					to, werr, from), nil
			}
			st, serr := os.Stat(toAbs)
			if serr != nil || st.Size() != int64(len(content)) {
				_ = os.Remove(toAbs)
				return fmt.Sprintf(
					"ws_mv aborted: the copy of %s to %s was incomplete, so %s was NOT deleted. Retry, "+
						"or use ws_write + ws_delete explicitly.", from, to, from), nil
			}
			if err := os.Remove(fromAbs); err != nil {
				return fmt.Sprintf(
					"ws_mv partially done: %s was created but %s could not be removed (%v). "+
						"Delete it with ws_delete, or continue if both copies are acceptable.",
					to, from, err), nil
			}
		}
	}
	w.markRead(to)
	msg := fmt.Sprintf("moved %s → %s", from, to)
	w.notify(to, "mv", msg)
	w.notify(from, "delete", "renamed away")
	return msg, nil
}

func (w *Workspace) deleteFile(_ context.Context, args map[string]interface{}) (interface{}, error) {
	path := w.normalizeRelPath(strArg(args, "path"))
	if strings.TrimSpace(path) == "" {
		return "ws_delete: path is required, e.g. {\"path\":\"pkg/foo/old.go\"}.", nil
	}
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := w.checkFocus(path); err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf(
				"ws_delete: %s does not exist — nothing to do. Check the path with ws_list.", path), nil
		}
		return fmt.Sprintf("ws_delete: cannot stat %s: %v.", path, err), nil
	}
	if st.IsDir() {
		return fmt.Sprintf(
			"ws_delete: %s is a directory. This tool deletes single files only.", path), nil
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
	// Checkpoint BEFORE removing — delete is irreversible otherwise.
	w.backup(path)
	if err := os.Remove(abs); err != nil {
		return fmt.Sprintf("ws_delete failed for %s: %v. Check permissions or whether it is open.", path, err), nil
	}
	msg := fmt.Sprintf("deleted %s", path)
	w.notify(path, "delete", msg)
	return msg, nil
}

func (w *Workspace) listDir(_ context.Context, args map[string]interface{}) (interface{}, error) {
	path := strArg(args, "path")
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	path = w.normalizeRelPath(path)
	abs, err := w.resolve(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf(
				"ws_list: %s does not exist. Try ws_list {\"path\":\".\"} to see the project root, "+
					"or ws_glob to search by pattern.", path), nil
		}
		return fmt.Sprintf("ws_list: cannot read %s: %v.", path, err), nil
	}
	var dirs, files []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name()+"/")
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(dirs)
	sort.Strings(files)
	if len(dirs) == 0 && len(files) == 0 {
		// An empty tool result is a known SLM stall trigger — say something.
		return fmt.Sprintf(
			"ws_list: %s exists but is empty (no files, no subdirectories). "+
				"Look somewhere else with ws_list on the parent directory, or ws_glob for a pattern.",
			path), nil
	}
	out := strings.Join(append(dirs, files...), "\n")
	return out + fmt.Sprintf("\n\n[%d director(ies), %d file(s) in %s]", len(dirs), len(files), path), nil
}

// MatchGlob reports whether rel matches a glob pattern supporting `**`
// (any number of path segments, including zero).
//
// filepath.Match has no `**`; the old implementation special-cased a LEADING
// "**/" only, so the advertised `pkg/**/*.go` silently returned nothing.
func MatchGlob(pattern, rel string) bool {
	pattern = filepath.ToSlash(pattern)
	rel = filepath.ToSlash(rel)
	return globSegments(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

func globSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Collapse consecutive **.
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return true // trailing ** matches everything left
			}
			for i := 0; i <= len(name); i++ {
				if globSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := filepath.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat = pat[1:]
		name = name[1:]
	}
	return len(name) == 0
}

func skipDirName(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "venv", "__pycache__",
		"target", "dist", ".mypy_cache", ".pytest_cache", ".tox":
		return true
	}
	return false
}

func (w *Workspace) glob(_ context.Context, args map[string]interface{}) (interface{}, error) {
	pattern := strings.TrimSpace(strArg(args, "pattern"))
	if pattern == "" {
		return "ws_glob: pattern is required, e.g. {\"pattern\":\"pkg/**/*.go\"}.", nil
	}
	var matches []string
	found := 0
	_ = filepath.WalkDir(w.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != w.Root && skipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(w.Root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		ok := MatchGlob(pattern, rel)
		if !ok && !strings.Contains(pattern, "/") {
			// A bare "*.go" should also match nested files — that is what a
			// model means, and it is what the old loose base-name check did.
			ok = MatchGlob("**/"+pattern, rel)
		}
		if !ok {
			return nil
		}
		found++
		if len(matches) < MaxGlobHits {
			matches = append(matches, rel)
		}
		return nil
	})
	if found == 0 {
		return fmt.Sprintf(
			"ws_glob: no files match %q.\n"+
				"Try a broader pattern (**/*%s), check the extension, or ws_list the directory you expect it in.",
			pattern, filepath.Ext(pattern)), nil
	}
	sort.Strings(matches)
	out := strings.Join(matches, "\n")
	if found > len(matches) {
		out += fmt.Sprintf(
			"\n\n…[%d of %d matches shown; narrow the pattern, e.g. add a directory prefix]",
			len(matches), found)
	} else {
		out += fmt.Sprintf("\n\n[%d match(es)]", found)
	}
	return out, nil
}

func (w *Workspace) grep(_ context.Context, args map[string]interface{}) (interface{}, error) {
	pattern := strArg(args, "pattern")
	globFilter := strArg(args, "glob")
	sub := strArg(args, "path")
	if strings.TrimSpace(pattern) == "" {
		return "ws_grep: pattern is required, e.g. {\"pattern\":\"func NewFoo\",\"glob\":\"*.go\"}.", nil
	}
	base := w.Root
	if strings.TrimSpace(sub) != "" {
		sub = w.normalizeRelPath(sub)
		var err error
		base, err = w.resolve(sub)
		if err != nil {
			return nil, err
		}
		if st, serr := os.Stat(base); serr != nil || !st.IsDir() {
			return fmt.Sprintf(
				"ws_grep: path %q is not a directory in this workspace. Drop path= to search everything.",
				sub), nil
		}
	}
	// Real regex, with a literal fallback so a pattern like `foo(` still works.
	re, rerr := regexp.Compile(pattern)
	mode := "regex"
	if rerr != nil {
		mode = "literal"
	}
	matcher := func(line string) bool { return strings.Contains(line, pattern) }
	if rerr == nil {
		matcher = re.MatchString
	}

	var hits []string
	total := 0
	scanned := 0
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != base && skipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if globFilter != "" {
			rel, _ := filepath.Rel(w.Root, path)
			if !MatchGlob(globFilter, filepath.ToSlash(rel)) &&
				!MatchGlob("**/"+globFilter, filepath.ToSlash(rel)) {
				return nil
			}
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil || len(data) > 500_000 || isProbablyBinary(data) {
			return nil
		}
		scanned++
		rel, _ := filepath.Rel(w.Root, path)
		rel = filepath.ToSlash(rel)
		for i, line := range strings.Split(string(data), "\n") {
			if !matcher(line) {
				continue
			}
			total++
			if len(hits) < MaxGrepHits {
				hits = append(hits, fmt.Sprintf("%s:%d:%s", rel, i+1, truncateSnippet(line, 240)))
			}
			if total > 5000 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if total == 0 {
		hint := ""
		if mode == "literal" {
			hint = fmt.Sprintf(" (pattern %q is not a valid regex, so it was matched literally: %v)", pattern, rerr)
		}
		return fmt.Sprintf(
			"ws_grep: no matches for %q in %d file(s)%s.\n"+
				"Try a shorter or case-insensitive pattern ((?i)foo), drop glob=/path= to widen the search, "+
				"or use ws_glob to confirm the files you expect actually exist.",
			pattern, scanned, hint), nil
	}
	out := strings.Join(hits, "\n")
	if total > len(hits) {
		out += fmt.Sprintf(
			"\n\n…[%d of %d matches shown; narrow with path= or glob=, or use a longer pattern]",
			len(hits), total)
	} else {
		out += fmt.Sprintf("\n\n[%d match(es) in %d file(s), %s match]", total, scanned, mode)
	}
	return out, nil
}

// isProbablyBinary keeps grep from dumping object files into the context.
func isProbablyBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func (w *Workspace) shell(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	command := strArg(args, "command")
	if strings.TrimSpace(command) == "" {
		return "ws_shell: command is required, e.g. {\"command\":\"go test ./pkg/foo -short\"}.", nil
	}
	if w.DryRun {
		return "dry-run: " + command, nil
	}
	if w.ShellWriteGuard {
		if err := GuardShellWrites(w.Root, command); err != nil {
			return err.Error(), nil
		}
	}
	// SAFE_PREFIXES gate (little-coder permission-gate). In ask mode, non-safe
	// commands still go through approval; in allow mode they are refused.
	if w.ShellWhitelist {
		if refuse, blocked := GuardShellWhitelist(command, w.ShellAllow); blocked {
			mode := permissions.NormalizeShell(w.ShellPermission)
			if mode == permissions.ShellAllow || mode == "" || mode == permissions.ShellDeny {
				w.intervene("shell_whitelist", refuse)
				return refuse, nil
			}
			// ask: still notify, then fall through for approval
			w.intervene("shell_whitelist", refuse)
		}
	}
	switch permissions.NormalizeShell(w.ShellPermission) {
	case permissions.ShellDeny:
		return "shell denied by permission mode (shell=deny). Make the change with ws_edit/ws_write " +
			"and report what still needs verifying in your status JSON.", nil
	case permissions.ShellAsk:
		if w.AutoApprove {
			break // treat as allow
		}
		ok, err := w.waitShellApproval(ctx, command)
		if err != nil {
			return "shell approval unavailable: " + err.Error() +
				". Continue without running the command and note it in your status JSON.", nil
		}
		if !ok {
			return "shell denied by user. Do not retry the same command; continue with the edit work.", nil
		}
		// approved → fall through to execute
	}
	timeout := w.shellTimeout()
	if n := intArg(args, "timeout_sec", 0); n > 0 {
		timeout = time.Duration(n) * time.Second
		if timeout > MaxShellTimeout {
			timeout = MaxShellTimeout
		}
	}
	res := RunBounded(ctx, w.Root, command, timeout, MaxCapturedOutput)
	if res.TimedOut {
		// A timeout is information for the model, not a harness error.
		return TimeoutMessage(command, timeout, res.Output), nil
	}
	if res.Err != nil {
		return fmt.Sprintf(
			"exit error: %v\n%s\n\nThe command failed — read the output above, fix the cause with "+
				"ws_edit/ws_patch, then re-run it.", res.Err, res.Output), nil
	}
	if strings.TrimSpace(res.Output) == "" {
		return fmt.Sprintf("(command succeeded with no output: %s)", truncateSnippet(command, 200)), nil
	}
	return res.Output, nil
}

func (w *Workspace) markRead(rel string) {
	if w == nil || w.Reads == nil {
		return
	}
	w.Reads.Mark(filepath.Clean(rel))
}

func (w *Workspace) requireRead(rel string) error {
	if w == nil || !w.ReadBeforeEdit {
		return nil
	}
	if w.Reads == nil {
		w.Reads = NewReadTracker()
	}
	rel = filepath.Clean(rel)
	if w.Reads.Has(rel) {
		return nil
	}
	return fmt.Errorf("%s", EditBeforeReadReason(rel))
}

// normalizeRelPath maps whatever the model typed onto a canonical, project-
// relative path. It MUST be applied by every tool: read/list/grep used to skip
// it, so `ws_read "/main.go"` recorded the key "/main.go" while
// `ws_edit "/main.go"` normalized to "main.go" and the read-before-edit guard
// then refused the edit forever.
func (w *Workspace) normalizeRelPath(path string) string {
	if w == nil || strings.TrimSpace(path) == "" {
		return path
	}
	path = strings.TrimSpace(path)
	resolved, from := NormalizeWritePath(path, w.Root)
	if from == "" && filepath.IsAbs(path) {
		if rel, err := filepath.Rel(w.Root, resolved); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
		return path
	}
	if rel, err := filepath.Rel(w.Root, resolved); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

// fuzzyEditHint suggests nearby lines when old_str misses (SLM whitespace drift).
//
// The snippet is emitted WITHOUT the `%6d|` prefix ws_read uses: the previous
// version told the model to "copy exact text into old_str" while handing it
// text that could never match, which is a guaranteed infinite loop.
func fuzzyEditHint(fileText, oldStr string) string {
	needle := strings.TrimSpace(oldStr)
	if needle == "" || len(needle) < 4 {
		return ""
	}
	// Use first non-empty line of old_str as search key.
	key := needle
	if i := strings.IndexByte(needle, '\n'); i > 0 {
		key = strings.TrimSpace(needle[:i])
	}
	if len(key) < 4 {
		return ""
	}
	keyNorm := squashWS(key)
	lines := strings.Split(fileText, "\n")
	type hit struct {
		from, to int
		text     string
	}
	var hits []hit
	for i, ln := range lines {
		trim := strings.TrimSpace(ln)
		if trim == "" {
			continue
		}
		lnNorm := squashWS(trim)
		matched := strings.Contains(ln, key) ||
			strings.Contains(key, trim) ||
			(len(keyNorm) >= 4 && (strings.Contains(lnNorm, keyNorm) || strings.Contains(keyNorm, lnNorm)))
		if !matched {
			continue
		}
		start := i - 1
		if start < 0 {
			start = 0
		}
		end := i + 2
		if end > len(lines) {
			end = len(lines)
		}
		hits = append(hits, hit{from: start + 1, to: end, text: strings.Join(lines[start:end], "\n")})
		if len(hits) >= 3 {
			break
		}
	}
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Closest text already in the file — copy one of these blocks VERBATIM into old_str " +
		"(no line numbers, keep the exact indentation):\n")
	for i, h := range hits {
		if i > 0 {
			b.WriteString("---\n")
		}
		fmt.Fprintf(&b, "(lines %d–%d)\n%s\n", h.from, h.to, h.text)
	}
	return strings.TrimRight(b.String(), "\n")
}

func squashWS(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// truncateToolOutput keeps head + tail (little-coder style) to preserve context.
func truncateToolOutput(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max * 2 / 3
	tail := max - head - 40
	if tail < 80 {
		tail = 80
		head = max - tail - 40
	}
	if head < 100 {
		return s[:max] + "\n...[truncated]"
	}
	return s[:head] + fmt.Sprintf("\n...[%d chars truncated]...\n", len(s)-head-tail) + s[len(s)-tail:]
}

func (w *Workspace) waitShellApproval(ctx context.Context, command string) (bool, error) {
	if w.SlmDir == "" {
		return false, fmt.Errorf("shell ask: no slm dir")
	}
	timeout := w.ShellAskTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ask := ShellAsk{
		ID:        fmt.Sprintf("shell-%d", time.Now().UnixNano()),
		Kind:      "shell",
		Command:   command,
		TimeoutS:  int(timeout.Seconds()),
		OnTimeout: "deny",
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
	var ans ShellAnswer
	ok, err := hitl.WaitAnswersForID(ctx, w.SlmDir, "shell", ask.ID, timeout, &ans)
	hitl.Clear(w.SlmDir, "shell")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("shell ask timed out after %s — command not executed", timeout)
	}
	d := strings.ToLower(strings.TrimSpace(ans.Decision))
	return d == "approve" || d == "allow" || d == "yes" || d == "ok", nil
}

func (w *Workspace) gitStatus(ctx context.Context, _ map[string]interface{}) (interface{}, error) {
	if !w.hasGit() {
		return "not a git repository — no status. Use ws_list / ws_glob to inspect the tree instead.", nil
	}
	return w.shell(ctx, map[string]interface{}{"command": "git status --short"})
}

func (w *Workspace) gitDiff(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if !w.hasGit() {
		return "not a git repository — no diff. Use ws_read to inspect the current file contents.", nil
	}
	path := w.normalizeRelPath(strArg(args, "path"))
	cmd := "git diff --"
	if strings.TrimSpace(path) != "" {
		if _, err := w.resolve(path); err != nil {
			return nil, err
		}
		cmd += " " + shellSingleQuote(path)
	}
	return w.shell(ctx, map[string]interface{}{"command": cmd})
}

// shellSingleQuote wraps s in POSIX single quotes, which suppress every form
// of expansion (unlike double quotes, which still honour $(…) and backticks).
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (w *Workspace) hasGit() bool {
	_, err := os.Stat(filepath.Join(w.Root, ".git"))
	return err == nil
}

// ToolNames returns the coding tool names registered by RegisterCodingTools.
func ToolNames() []string {
	return []string{
		"ws_read", "ws_write", "ws_edit", "ws_patch", "ws_mv", "ws_delete",
		"ws_list", "ws_glob", "ws_grep", "ws_shell", "ws_todo",
		"git_status", "git_diff",
	}
}

// SpecialistToolNames are meta-tools registered outside workspace
// (find_models, mcp_call) but allowlisted on coding agents.
func SpecialistToolNames() []string {
	return []string{"find_models", "mcp_call"}
}
