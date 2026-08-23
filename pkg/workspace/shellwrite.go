package workspace

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// ShellWrite is a path a shell command would write via redirect/tee/dd.
type ShellWrite struct {
	Path string
	Kind string // redirect | append | tee | dd
}

var (
	heredocStartRe = regexp.MustCompile(`<<-?[ \t]*(?:'([^']*)'|"([^"]*)"|([A-Za-z_][A-Za-z0-9_]*))`)
	devFdRe        = regexp.MustCompile(`^/dev/fd/\d+$`)
)

var nonDestructiveTargets = map[string]bool{
	"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true,
	"/dev/stdin": true, "/dev/tty": true, "/dev/zero": true,
	"/dev/full": true, "/dev/random": true, "/dev/urandom": true,
}

// IsNonDestructiveTarget reports redirects that cannot clobber project files.
func IsNonDestructiveTarget(path string) bool {
	return nonDestructiveTargets[path] || devFdRe.MatchString(path)
}

// StripHeredocBodies removes heredoc payloads so `>` inside content is ignored.
func StripHeredocBodies(cmd string) string {
	out := cmd
	searchFrom := 0
	for guard := 0; guard < 32; guard++ {
		rest := out[searchFrom:]
		loc := heredocStartRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			break
		}
		at := searchFrom + loc[0]
		if at+2 < len(out) && out[at+2] == '<' {
			searchFrom = at + 3
			continue
		}
		var delim string
		for i := 1; i <= 3; i++ {
			if loc[2*i] >= 0 {
				delim = rest[loc[2*i]:loc[2*i+1]]
				break
			}
		}
		bodyStart := strings.IndexByte(out[at+loc[1]-loc[0]:], '\n')
		if bodyStart < 0 || delim == "" {
			searchFrom = at + (loc[1] - loc[0])
			continue
		}
		bodyStart += at + (loc[1] - loc[0])
		lines := strings.Split(out[bodyStart+1:], "\n")
		consumed := 0
		closed := false
		for _, line := range lines {
			consumed += len(line) + 1
			if strings.TrimSpace(line) == delim {
				closed = true
				break
			}
		}
		bodyEnd := len(out)
		if closed {
			bodyEnd = bodyStart + consumed
			if bodyEnd > len(out) {
				bodyEnd = len(out)
			}
		}
		out = out[:at] + out[bodyEnd:]
		searchFrom = at
	}
	return out
}

// DetectWriteTargets finds filesystem write paths in a shell command.
func DetectWriteTargets(raw string) []ShellWrite {
	cmd := StripHeredocBodies(raw)
	var writes []ShellWrite

	scanRedirects(cmd, func(at int, kind string) {
		rest := cmd[at:]
		trimmed := strings.TrimLeftFunc(rest, unicode.IsSpace)
		if strings.HasPrefix(trimmed, "(") {
			return
		}
		target := firstWord(rest)
		if target == "" || strings.HasPrefix(target, "&") {
			return
		}
		writes = append(writes, ShellWrite{Path: unquote(target), Kind: kind})
	})

	for _, segment := range splitCommandChain(cmd) {
		writes = append(writes, mutatingCommandTargets(segment)...)
	}

	seen := map[string]bool{}
	var out []ShellWrite
	for _, w := range writes {
		if w.Path == "" || seen[w.Path] || IsNonDestructiveTarget(w.Path) {
			continue
		}
		seen[w.Path] = true
		out = append(out, w)
	}
	return out
}

// HasWriteRedirection reports whether cmd writes via shell redirection.
func HasWriteRedirection(cmd string) bool {
	return len(DetectWriteTargets(cmd)) > 0
}

// GuardShellWrites refuses shell commands that would clobber existing files
// (the write-guard bypass: cat > file <<EOF). Appends (>>) to existing files
// are allowed; reserved device names are always refused.
func GuardShellWrites(root, command string) error {
	// Command substitution defeats every static check below: we cannot know
	// what `$(...)` writes, so it is refused before we look at redirects.
	if reason, unsafe := UnsafeShellSyntax(command); unsafe {
		return fmt.Errorf("%s", reason)
	}
	for _, w := range DetectWriteTargets(command) {
		abs := w.Path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, abs)
		}
		abs = filepath.Clean(abs)
		if IsReservedDeviceName(abs) {
			return fmt.Errorf(
				"shell write refused — %s is a reserved device name. Use ws_write/ws_edit with a real path",
				filepath.Base(abs),
			)
		}
		truncates := w.Kind != "append"
		refuse, reason := CheckWriteDestination(abs, truncates)
		if refuse {
			rel := abs
			if r, err := filepath.Rel(root, abs); err == nil {
				rel = r
			}
			if strings.Contains(reason, "already exists") {
				return fmt.Errorf(
					"shell write refused — %s already exists and `%s` would overwrite it.\n"+
						"File mutations must go through the tool layer so they can be checkpointed, "+
						"reviewed and reverted.\n"+
						"Recipe: ws_read %s, then ws_edit with exact old_str/new_str (or ws_patch for a diff)",
					rel, w.Kind, rel,
				)
			}
			if strings.Contains(reason, "is a directory") {
				return fmt.Errorf(
					"shell write refused — `%s` targets the directory %s. "+
						"Use ws_write with an explicit file path, or ws_mv to relocate a file",
					w.Kind, rel,
				)
			}
			return fmt.Errorf("shell write refused — %s", reason)
		}
	}
	return nil
}

func scanRedirects(cmd string, visit func(at int, kind string)) {
	quoted := byte(0)
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if quoted == 0 && ch == '\\' {
			i++
			continue
		}
		if quoted == 0 && (ch == '"' || ch == '\'') {
			quoted = ch
			continue
		}
		if quoted != 0 && ch == quoted {
			quoted = 0
			continue
		}
		if quoted != 0 || ch != '>' {
			continue
		}
		if i > 0 && cmd[i-1] == '>' {
			continue
		}
		kind := "redirect"
		at := i + 1
		if i+1 < len(cmd) && cmd[i+1] == '>' {
			kind = "append"
			at = i + 2
		}
		visit(at, kind)
	}
}

func splitCommandChain(raw string) []string {
	cmd := StripHeredocBodies(raw)
	// "&&" must be tested before "&" so a logical AND is not split as a
	// background operator. A BARE "&" starts a second command; before this it
	// was invisible to every guard ("ls & rm -rf /tmp/x" read as one `ls`).
	ops := []string{"&&", "||", ";;", ";", "||", "|", "\n", "&"}
	type cut struct{ at, len int }
	var cuts []cut
	quoted := byte(0)
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if quoted == 0 && ch == '\\' {
			i++
			continue
		}
		if quoted == 0 && (ch == '"' || ch == '\'') {
			quoted = ch
			continue
		}
		if quoted != 0 && ch == quoted {
			quoted = 0
			continue
		}
		if quoted != 0 {
			continue
		}
		for _, op := range ops {
			if !strings.HasPrefix(cmd[i:], op) {
				continue
			}
			if n := len(cuts); n > 0 && i < cuts[n-1].at+cuts[n-1].len {
				break
			}
			if op == "&" && !isBareAmpersandAt(cmd, i) {
				break
			}
			if op == "|" && i+1 < len(cmd) && cmd[i+1] == '|' {
				break // handled by the "||" entry
			}
			cuts = append(cuts, cut{at: i, len: len(op)})
			break
		}
	}
	var segs []string
	start := 0
	for _, c := range cuts {
		segs = append(segs, strings.TrimSpace(cmd[start:c.at]))
		start = c.at + c.len
	}
	segs = append(segs, strings.TrimSpace(cmd[start:]))
	var out []string
	for _, s := range segs {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func unquote(word string) string {
	if len(word) >= 2 {
		q := word[0]
		if (q == '"' || q == '\'') && word[len(word)-1] == q {
			return word[1 : len(word)-1]
		}
	}
	var b strings.Builder
	for i := 0; i < len(word); i++ {
		if word[i] == '\\' && i+1 < len(word) {
			b.WriteByte(word[i+1])
			i++
			continue
		}
		b.WriteByte(word[i])
	}
	return b.String()
}

func firstWord(s string) string {
	words := splitWords(s)
	if len(words) == 0 {
		return ""
	}
	return words[0]
}

func splitWords(s string) []string {
	var words []string
	var cur strings.Builder
	quoted := byte(0)
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if quoted == 0 && ch == '\\' {
			cur.WriteByte(ch)
			if i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
			}
			continue
		}
		if quoted == 0 && (ch == '"' || ch == '\'') {
			quoted = ch
			cur.WriteByte(ch)
			continue
		}
		if quoted != 0 && ch == quoted {
			quoted = 0
			cur.WriteByte(ch)
			continue
		}
		if quoted == 0 && unicode.IsSpace(rune(ch)) {
			flush()
			continue
		}
		if quoted == 0 && (ch == '>' || ch == '<') {
			flush()
			continue
		}
		cur.WriteByte(ch)
	}
	flush()
	return words
}

// mutatingCommandTargets finds files a command rewrites WITHOUT using shell
// redirection. `sed -i`, `cp`, `mv`, `install`, `truncate`, `tee -a` and
// `rsync` all clobber files behind the tool layer's back; before this the
// write guard only understood `>` / `>>` / `tee` / `dd`, so the harness
// blocked `echo x >> f.py` while waving `sed -i s/a/b/ f.py` straight through.
func mutatingCommandTargets(segment string) []ShellWrite {
	words := splitWords(segment)
	// Skip leading VAR=value assignments so `FOO=1 cp a b` is still seen.
	for len(words) > 0 && isAssignmentWord(words[0]) {
		words = words[1:]
	}
	if len(words) == 0 {
		return nil
	}
	bin := words[0]
	if i := strings.LastIndexByte(bin, '/'); i >= 0 {
		bin = bin[i+1:]
	}
	args := words[1:]
	switch bin {
	case "tee":
		kind := "tee"
		if hasFlag(args, "-a", "--append") {
			kind = "append"
		}
		return targetsFor(operands(args), kind)
	case "dd":
		var out []ShellWrite
		for _, w := range args {
			if strings.HasPrefix(w, "of=") {
				out = append(out, ShellWrite{Path: unquote(w[3:]), Kind: "dd"})
			}
		}
		return out
	case "sed", "gsed", "perl":
		if !hasInPlaceFlag(args) {
			return nil
		}
		ops := operands(args, "-e", "--expression", "-f", "--file")
		// Without -e/-f the first operand is the script, not a file.
		if !hasFlag(args, "-e", "--expression", "-f", "--file") && len(ops) > 0 {
			ops = ops[1:]
		}
		return targetsFor(ops, "in-place")
	case "cp", "mv", "install", "rsync", "ln":
		ops := operands(args, "-t", "--target-directory", "--suffix", "-S")
		if len(ops) < 2 {
			return nil
		}
		// Last operand is the destination (file or directory).
		return targetsFor(ops[len(ops)-1:], bin)
	case "truncate":
		return targetsFor(operands(args, "-s", "--size", "-r", "--reference"), "truncate")
	case "patch":
		return targetsFor(operands(args, "-i", "--input", "-p", "--strip", "-d", "--directory"), "patch")
	}
	return nil
}

func isAssignmentWord(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 || strings.HasPrefix(w, "-") {
		return false
	}
	return !strings.ContainsAny(w[:i], "/. \t")
}

func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n {
				return true
			}
		}
	}
	return false
}

// hasInPlaceFlag matches -i, -i.bak, --in-place, and clustered forms like -ni.
func hasInPlaceFlag(args []string) bool {
	for _, a := range args {
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			return true
		}
		if len(a) < 2 || a[0] != '-' || a[1] == '-' {
			continue
		}
		if strings.ContainsRune(a[1:], 'i') {
			return true
		}
	}
	return false
}

// operands returns non-flag arguments, stopping flag parsing at "--".
// valueFlags names flags whose SEPARATE next argument is a value, not a target
// — the set is command-specific (`truncate -s 0 f` consumes the 0, while
// `ln -s a b` does not).
func operands(args []string, valueFlags ...string) []string {
	takesValue := map[string]bool{}
	for _, f := range valueFlags {
		takesValue[f] = true
	}
	var out []string
	endFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !endFlags && a == "--" {
			endFlags = true
			continue
		}
		if !endFlags && strings.HasPrefix(a, "-") && len(a) > 1 {
			if takesValue[a] {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func targetsFor(paths []string, kind string) []ShellWrite {
	var out []ShellWrite
	for _, p := range paths {
		p = unquote(p)
		if p == "" {
			continue
		}
		out = append(out, ShellWrite{Path: p, Kind: kind})
	}
	return out
}

// isBareAmpersandAt reports whether cmd[i] == '&' is a background operator
// rather than part of "&&" or an fd duplication such as 2>&1 / &>log.
func isBareAmpersandAt(cmd string, i int) bool {
	if i >= len(cmd) || cmd[i] != '&' {
		return false
	}
	if i+1 < len(cmd) && (cmd[i+1] == '&' || cmd[i+1] == '>') {
		return false
	}
	if i > 0 && (cmd[i-1] == '&' || cmd[i-1] == '>' || cmd[i-1] == '<') {
		return false
	}
	return true
}
