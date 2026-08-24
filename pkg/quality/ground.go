package quality

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/UnicoLab/slmcode/pkg/memory"
)

// Grounded claim reconciliation — checking an answer against a RECORD instead
// of deciding whether it looks right.
//
// Every other gate in this package interrogates the DISK: CheckClaimedFiles
// stats each claimed path, the smoke gates re-run the commands, and pkg/loop's
// hasRealWriteEvidence hashes the focus files against a pre-wave baseline. All
// of them are stronger than asking a model. None of them can check a claim
// about KNOWLEDGE — "I ran the suite with `npm test`" — because the answer to
// that one is not on disk. It is in pkg/memory, which has been counting which
// commands actually work in this project all along, with confidence scores and
// contradiction tracking, and which nothing read back at review time.
//
// This file closes that loop, deterministically and with zero model calls:
// extract the claims, reconcile them against the recorded facts, and hand the
// reviewer the disagreements in an explicit decision / claim / reason /
// required_evidence shape so it has something to VERIFY rather than a vibe to
// judge.
//
// ── Why precision beats recall here, by a wide margin ───────────────────────
//
// The two error directions are not symmetric, and it is not close.
//
// A missed contradiction costs nothing. The reviewer is exactly as informed as
// it was before this file existed, and every disk-backed gate still runs; the
// worst case is the status quo.
//
// A FALSE contradiction costs a correct answer. The reviewer reads "recorded
// project knowledge says this is wrong", rejects work that was fine, and the
// corrector burns a round trip un-fixing code that already worked — on a small
// model, quite possibly making it worse. One false positive is worth more than
// a dozen misses, so every judgment call below resolves toward silence:
//
//   - only fenced SHELL blocks and three explicit command-claim shapes are read.
//     Prose that merely names a command is not a claim;
//   - only commands whose tool this file can CLASSIFY become claims at all,
//     which is what keeps program output inside a shell transcript
//     ("PASS src/a.test.js", "FAIL github.com/x/y") from ever looking like one;
//   - a claim is reconciled only against a fact of the same kind carrying real
//     confidence behind it (minGroundingConfidence);
//   - and the moment the project shows ANY recorded sign of using the claimed
//     tool, the claim is dropped. A repo that has run both `go test` and
//     `npm test` contradicts neither, whatever the task was about.
//
// Everything here is pure: no I/O, no clock, no map-ordered output.

// Claim kinds. Only ClaimCommand is extracted today — see ExtractClaims for why
// the other two are declared but not yet produced.
const (
	// ClaimCommand is a command the answer says it ran.
	ClaimCommand = "command"
	// ClaimConvention is a claim about how this project does something.
	ClaimConvention = "convention"
	// ClaimPath is a claim about where something lives.
	ClaimPath = "path"
)

// DecisionRevise is the only decision a Contradiction ever carries.
//
// This layer never approves and never blocks. It cannot: it compares a claim to
// a probabilistic record, not to the filesystem, so its strongest honest
// statement is "this needs to be shown, not asserted". Approval stays with the
// reviewer and rejection stays with the disk-backed gates.
const DecisionRevise = "revise"

// KnowledgeSectionHeader heads the harness-authored reconciliation section.
const KnowledgeSectionHeader = "## Knowledge conflicts"

// minGroundingConfidence is the floor a recorded fact must clear before it is
// allowed to contradict anything.
//
// memory.Fact.Confidence is the posterior mean of a Beta(1,1)-prior Bernoulli:
// one clean sighting is 0.67, and a fact confirmed once and contradicted once
// is 0.50. 0.60 sits between them on purpose — a single confirmed observation
// is enough to speak up, anything at coin-flip odds or worse is not. Facts at
// or above the floor are admitted.
const minGroundingConfidence = 0.6

// Bounds. Each is a hard stop, not a target: this section shares the reviewer's
// context window with the disk evidence and the smoke output, and it is the
// least authoritative of the three.
const (
	maxScanLines       = 400 // lines of worker output read for claims
	maxClaims          = 24  // claims extracted from one answer
	maxContradictions  = 5   // conflicts reported for one answer
	maxCommandTokens   = 6   // tokens kept per command (matches pkg/memory)
	maxKnowledgeBytes  = 1400
	maxClaimTextLen    = 120
	maxReasonTextLen   = 220
	maxEvidenceTextLen = 160
	maxSourceLineLen   = 140
)

// Claim is one thing the answer asserts about the world.
type Claim struct {
	Kind string // "command" | "convention" | "path" | ...
	Text string // the raw claimed thing, e.g. "npm test"
	Line string // the source line, for the report
}

// Contradiction is one claim that disagrees with a recorded fact.
type Contradiction struct {
	Decision         string   // always DecisionRevise
	Claim            string   // the claim as extracted
	Reason           string   // what the record says instead
	RequiredEvidence []string // what would settle it
	Confidence       float64  // confidence of the fact that contradicts the claim
}

// init registers this file's header with the shared strip list.
//
// The list in sections.go is deliberately the ONE place strip logic iterates —
// re-listing headers by hand is exactly how two of them silently drifted out of
// sync with the formatters that emit them. A section defined in another file of
// this package therefore registers itself rather than forcing a second literal
// into existence. Package-level vars are fully initialized before any init runs,
// so this appends to a complete slice, exactly once, before anything reads it.
func init() {
	HarnessSectionHeaders = append(HarnessSectionHeaders, KnowledgeSectionHeader)
}

// ── extraction ──────────────────────────────────────────────────────────────

var (
	// shellFenceTags are the fence languages that mean "this is a shell
	// transcript". A BARE ``` fence is deliberately absent: it is just as
	// likely to hold JSON, a diff, a stack trace or program output, and
	// treating it as commands is the single easiest way to manufacture a false
	// claim.
	shellFenceTags = map[string]bool{
		"bash": true, "sh": true, "shell": true, "zsh": true, "console": true,
		"terminal": true, "shell-session": true, "shellsession": true, "console-session": true,
	}

	// promptLineRe matches an interactive prompt line: "$ npm test", "% pytest".
	// The mandatory space is what keeps "$HOME/bin" and "100% done" out.
	promptLineRe = regexp.MustCompile(`^[ \t]*[$%][ \t]+(\S.*)$`)

	// verbClaimRe matches the three explicit "I did this" shapes. The verb list
	// is closed on purpose: "should run", "you can run" and "try running" are
	// advice, not claims, and admitting them would turn every helpful sentence
	// into an accusation.
	verbClaimRe = regexp.MustCompile(`(?i)^[ \t]*(?:i[ \t]+|then[ \t]+i[ \t]+)?(?:ran|executed|invoked)[ \t]*:?[ \t]+(\S.*)$`)

	// wsRunRe collapses runs of whitespace for normalization.
	wsRunRe = regexp.MustCompile(`[ \t\v\f\r\n]+`)
)

// ExtractClaims pulls the commands an answer says it ran out of that answer.
//
// Sources, and only these:
//
//	fenced shell blocks   ```bash … ```      (tagged fences only)
//	prompt lines          $ npm test        (anywhere)
//	explicit verbs        ran `npm test`    (ran / executed / invoked)
//
// Every candidate is then filtered through classifyCommand, so a line only
// survives if its leading token is a build/test tool this file recognizes. That
// second filter is what makes the first one safe: a shell transcript is mostly
// OUTPUT, and output lines ("PASS src/a.test.js", "--- FAIL: TestX",
// "make[1]: Entering directory") do not start with a tool name. The cost is
// real and accepted — a project whose test command is `./scripts/ci.sh` yields
// no claims at all. That is a miss, and misses are free.
//
// Only command claims are produced. ClaimConvention and ClaimPath exist because
// Reconcile switches on Kind and the extension point should be visible, but a
// convention claim has no comparable low-false-positive extraction: "we use
// table-driven tests here" is unbounded natural language, and guessing at it
// would trade away the guarantee this whole file is built on.
func ExtractClaims(output string) []Claim {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	lines := strings.Split(output, "\n")
	if len(lines) > maxScanLines {
		lines = lines[:maxScanLines]
	}

	var claims []Claim
	seen := map[string]bool{}
	add := func(raw, source string) {
		if len(claims) >= maxClaims {
			return
		}
		cmd := cleanCommandText(raw)
		if cmd == "" {
			return
		}
		if runner, _ := classifyCommand(cmd); runner == "" {
			return
		}
		key := strings.ToLower(cmd)
		if seen[key] {
			return
		}
		seen[key] = true
		claims = append(claims, Claim{
			Kind: ClaimCommand,
			Text: cmd,
			Line: truncateRunes(collapseSpaces(source), maxSourceLineLen),
		})
	}

	inShell, inOther := false, false
	for _, ln := range lines {
		if tag, isFence := fenceTag(ln); isFence {
			switch {
			case inShell || inOther:
				inShell, inOther = false, false
			case shellFenceTags[tag]:
				inShell = true
			default:
				// An untagged or non-shell fence: skip its whole body rather
				// than read it. Diffs and JSON live in these.
				inOther = true
			}
			continue
		}
		if inOther {
			continue
		}
		if m := promptLineRe.FindStringSubmatch(ln); m != nil {
			add(m[1], ln)
			continue
		}
		if inShell {
			add(ln, ln)
			continue
		}
		if m := verbClaimRe.FindStringSubmatch(ln); m != nil {
			add(m[1], ln)
		}
	}
	return claims
}

// fenceTag reports whether a line opens or closes a fenced block, and its
// lowercased language tag when it opens one.
func fenceTag(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "```") && !strings.HasPrefix(t, "~~~") {
		return "", false
	}
	rest := strings.TrimLeft(t, "`~")
	fields := strings.Fields(strings.ToLower(rest))
	if len(fields) == 0 {
		return "", true
	}
	return fields[0], true
}

// cleanCommandText turns a candidate line into the bare command text: trimmed,
// unwrapped from backticks or quotes, whitespace collapsed, trailing shell
// comments and sentence punctuation removed.
func cleanCommandText(raw string) string {
	s := collapseSpaces(raw)
	if s == "" || strings.HasPrefix(s, "#") {
		return ""
	}
	// A prompt can survive inside a quoted or verb-introduced claim.
	if m := promptLineRe.FindStringSubmatch(s); m != nil {
		s = collapseSpaces(m[1])
	}
	s = unwrapDelimited(s)
	if i := strings.Index(s, " #"); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.TrimRight(s, ",;:")
	s = trimSentencePeriod(s)
	return truncateRunes(strings.TrimSpace(s), maxClaimTextLen)
}

// unwrapDelimited takes the contents of a leading backtick/quote pair, because
// a delimited claim ENDS at its closing delimiter: "ran `yarn lint` afterwards"
// is a claim about `yarn lint`, not about the rest of the sentence.
func unwrapDelimited(s string) string {
	if len(s) < 2 {
		return strings.TrimSpace(s)
	}
	q := s[0]
	if q != '`' && q != '"' && q != '\'' {
		return strings.TrimSpace(s)
	}
	if j := strings.IndexByte(s[1:], q); j > 0 {
		return strings.TrimSpace(s[1 : 1+j])
	}
	return strings.TrimSpace(strings.TrimLeft(s, "`\"'"))
}

// trimSentencePeriod drops a full stop that ends a SENTENCE and never one that
// belongs to the command. "I ran pytest." loses its stop; `go test ./...` and
// `python main.py` keep every character they have.
func trimSentencePeriod(s string) string {
	r := []rune(s)
	if len(r) < 2 || r[len(r)-1] != '.' {
		return s
	}
	if unicode.IsLetter(r[len(r)-2]) || unicode.IsDigit(r[len(r)-2]) {
		return string(r[:len(r)-1])
	}
	return s
}

func collapseSpaces(s string) string {
	return strings.TrimSpace(wsRunRe.ReplaceAllString(s, " "))
}

// ── command classification ──────────────────────────────────────────────────

// Job roles. Two commands conflict only when they claim the same job.
const (
	roleTest      = "test"
	roleBuild     = "build"
	roleLint      = "lint"
	roleFormat    = "format"
	roleInstall   = "install"
	roleTypecheck = "typecheck"
)

// fixedRoleRunners are tools whose name alone names the job.
var fixedRoleRunners = map[string]string{
	"pytest": roleTest, "tox": roleTest, "jest": roleTest, "vitest": roleTest,
	"rspec": roleTest, "phpunit": roleTest, "gotestsum": roleTest, "nose2": roleTest,
	"golangci-lint": roleLint, "eslint": roleLint, "flake8": roleLint,
	"pylint": roleLint, "staticcheck": roleLint, "shellcheck": roleLint,
	"gofmt": roleFormat, "goimports": roleFormat, "prettier": roleFormat,
	"black": roleFormat, "isort": roleFormat,
	"mypy": roleTypecheck, "pyright": roleTypecheck,
	"pip": roleInstall, "pip3": roleInstall,
}

// subcommandRunners are tools that need their subcommand read before the job is
// known. A recognized runner with an unrecognized subcommand yields no role,
// and therefore never contradicts anything.
var subcommandRunners = map[string]bool{
	"go": true, "npm": true, "yarn": true, "pnpm": true, "bun": true,
	"cargo": true, "make": true, "just": true, "task": true,
	"python": true, "python3": true, "poetry": true, "uv": true, "pipenv": true,
	"mvn": true, "gradle": true, "dotnet": true, "swift": true,
	"bundle": true, "rake": true, "composer": true, "deno": true, "ruff": true,
}

// jobWords map a subcommand (or npm script name) to the job it does. They are
// matched against the token's LEADING ALPHABETIC RUN, so "test:unit",
// "build:prod" and "lint-fix" all land where they should.
//
// Genuinely ambiguous words are absent by design: "check" is tests under make,
// a compile under cargo and lint under ruff, and a word that means three things
// cannot be evidence of anything.
var jobWords = map[string]string{
	"test": roleTest, "tests": roleTest, "spec": roleTest,
	"build": roleBuild, "compile": roleBuild, "package": roleBuild, "assemble": roleBuild,
	"lint": roleLint, "vet": roleLint, "clippy": roleLint,
	"fmt": roleFormat, "format": roleFormat,
	"install": roleInstall, "ci": roleInstall, "sync": roleInstall, "restore": roleInstall,
	"typecheck": roleTypecheck,
}

// wrapperWords are pass-through subcommands that introduce the real tool.
var wrapperWords = map[string]bool{
	"run": true, "exec": true, "dlx": true, "x": true, "tool": true,
}

// commandFields normalizes a command into comparable tokens.
//
// It mirrors pkg/memory's own normalizeCommand exactly — flags dropped, at most
// maxCommandTokens kept — because the two sides of every comparison in
// Reconcile are a claim normalized here and a memory.Fact.Subject normalized
// there. If the two normalizations disagreed, `go test ./... -race` and
// `go test ./...` would read as rival claims and this file would invent
// conflicts out of a flag.
func commandFields(cmd string) []string {
	c := strings.TrimSpace(cmd)
	if i := strings.IndexAny(c, "\n\r"); i >= 0 {
		c = c[:i]
	}
	c = strings.ToLower(c)
	kept := make([]string, 0, maxCommandTokens)
	for _, f := range strings.Fields(c) {
		if strings.HasPrefix(f, "-") {
			continue // flags are run-specific noise, not part of the habit
		}
		f = strings.Trim(f, "`\"'")
		if f == "" {
			continue
		}
		kept = append(kept, f)
		if len(kept) >= maxCommandTokens {
			break
		}
	}
	return kept
}

// commandKey is the comparison identity of a command: normalized tokens joined.
// Whitespace, quoting and flag differences all collapse into the same key,
// which is precisely why they never produce a contradiction.
func commandKey(cmd string) string {
	return strings.Join(commandFields(cmd), " ")
}

// commandBase reduces a token to the tool it names: "./node_modules/.bin/jest"
// and "/usr/bin/make" become "jest" and "make".
func commandBase(tok string) string {
	if i := strings.LastIndex(tok, "/"); i >= 0 {
		tok = tok[i+1:]
	}
	return strings.TrimSuffix(tok, ".exe")
}

// leadingAlpha returns the leading run of letters, so "test:unit" → "test".
func leadingAlpha(tok string) string {
	for i, r := range tok {
		if !unicode.IsLetter(r) {
			return tok[:i]
		}
	}
	return tok
}

// classifyCommand names the tool and the job a command performs. An empty
// runner means "not a command this file understands", and nothing downstream
// ever looks at it again.
func classifyCommand(cmd string) (runner, role string) {
	fields := commandFields(cmd)
	if len(fields) == 0 {
		return "", ""
	}
	runner = commandBase(fields[0])
	if r, ok := fixedRoleRunners[runner]; ok {
		return runner, r
	}
	if subcommandRunners[runner] {
		return runner, subcommandRole(fields)
	}
	return "", ""
}

// subcommandRole reads the job out of the FIRST non-wrapper token after the
// runner, and stops there. Scanning further would find "test" inside
// "go run ./cmd/testdata" and call it a test run.
func subcommandRole(fields []string) string {
	skipped := 0
	for _, f := range fields[1:] {
		if wrapperWords[f] {
			skipped++
			if skipped > 2 {
				return ""
			}
			continue
		}
		if r, ok := fixedRoleRunners[commandBase(f)]; ok {
			return r // `python -m pytest`, `bundle exec rspec`, `yarn dlx eslint`
		}
		return jobWords[leadingAlpha(commandBase(f))]
	}
	return ""
}

// ── reconciliation ──────────────────────────────────────────────────────────

// knownCommand is one memory.Fact of kind command, pre-classified.
type knownCommand struct {
	runner string
	role   string
	fact   memory.Fact
}

// Reconcile compares extracted claims against recorded facts and returns only
// the genuine disagreements.
//
// A command claim becomes a contradiction when ALL of the following hold:
//
//  1. the claim classifies to a job (test/build/lint/…) — an unclassifiable
//     command is never accused of anything;
//  2. the exact command is NOT already a recorded fact, under the shared
//     normalization, so whitespace, quoting and flag differences are invisible
//     here by construction;
//  3. the claimed TOOL appears nowhere in the recorded commands, at any
//     confidence and for any job. This is the single most important guard: a
//     polyglot repo where both `go test ./...` and `npm test` have been seen
//     contradicts neither, and one low-confidence sighting of a tool is enough
//     to buy that tool permanent amnesty;
//  4. and a fact for the SAME job, at or above minGroundingConfidence, exists
//     to disagree with it.
//
// Read as a sentence: "you say you ran a <job> command with a tool this project
// has never been observed using, and this project has a known <job> command."
//
// Note the asymmetry in how facts are used. Facts that SUPPRESS a contradiction
// (2 and 3) are consulted at any confidence; only the fact that ACCUSES has to
// clear the floor. Doubt always resolves toward saying nothing.
func Reconcile(claims []Claim, facts []memory.Fact) []Contradiction {
	if len(claims) == 0 || len(facts) == 0 {
		return nil
	}

	var known []knownCommand
	knownKeys := map[string]bool{}
	knownRunners := map[string]bool{}
	for _, f := range facts {
		if f.Kind != memory.FactCommand {
			continue
		}
		key := commandKey(f.Subject)
		if key == "" {
			continue
		}
		knownKeys[key] = true
		runner, role := classifyCommand(f.Subject)
		if runner == "" {
			continue
		}
		knownRunners[runner] = true
		if role != "" {
			known = append(known, knownCommand{runner: runner, role: role, fact: f})
		}
	}
	if len(known) == 0 {
		return nil
	}

	var out []Contradiction
	emitted := map[string]bool{}
	for _, c := range claims {
		if len(out) >= maxContradictions {
			break
		}
		if c.Kind != ClaimCommand {
			continue
		}
		key := commandKey(c.Text)
		if key == "" || knownKeys[key] || emitted[key] {
			continue
		}
		runner, role := classifyCommand(c.Text)
		if runner == "" || role == "" || knownRunners[runner] {
			continue
		}
		best, ok := bestKnownForRole(known, role)
		if !ok {
			continue
		}
		emitted[key] = true
		out = append(out, Contradiction{
			Decision: DecisionRevise,
			Claim:    c.Text,
			Reason: fmt.Sprintf(
				"this project has no recorded use of `%s`; the recorded %s command is `%s` (confidence %.2f, %d confirmation(s))",
				runner, role, strings.TrimSpace(best.fact.Subject), best.fact.Confidence, best.fact.Support),
			RequiredEvidence: []string{
				"the ws_shell call that ran `" + c.Text + "`, with its exit status",
				"or the files_changed entry that introduced `" + runner + "` to this project",
			},
			Confidence: best.fact.Confidence,
		})
	}

	// Total order, independent of how the facts were stored or iterated.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Claim != out[j].Claim {
			return out[i].Claim < out[j].Claim
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// bestKnownForRole picks the fact that speaks for a job: highest confidence,
// ties broken by subject so the choice never depends on store order.
func bestKnownForRole(known []knownCommand, role string) (knownCommand, bool) {
	var best knownCommand
	found := false
	for _, k := range known {
		if k.role != role || k.fact.Confidence < minGroundingConfidence {
			continue
		}
		switch {
		case !found,
			k.fact.Confidence > best.fact.Confidence,
			k.fact.Confidence == best.fact.Confidence && k.fact.Subject < best.fact.Subject:
			best, found = k, true
		}
	}
	return best, found
}

// ── rendering ───────────────────────────────────────────────────────────────

// RenderContradictions formats the conflicts as a bounded harness section, in
// the same shape as the smoke/static/claims sections this package already
// emits. It returns "" when there is nothing to say, which is the common case.
//
// Deliberately absent: SmokeFailedMarker. This section reports UNVERIFIED
// claims, not proven failures, and stamping it FAILED would let a probabilistic
// record trip the same gates that today only fire on disk-backed evidence.
func RenderContradictions(cs []Contradiction) string {
	if len(cs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n" + KnowledgeSectionHeader + "\n")
	b.WriteString("Recorded project knowledge disagrees with claims in this answer. " +
		"These are UNVERIFIED claims, not proven failures: do not approve them on the " +
		"assertion alone, and do not reject on this section alone either — require the " +
		"evidence named under each item.\n")

	shown := 0
	for _, c := range cs {
		if shown >= maxContradictions {
			break
		}
		item := renderContradiction(c)
		if b.Len()+len(item) > maxKnowledgeBytes {
			break
		}
		b.WriteString(item)
		shown++
	}
	if omitted := len(cs) - shown; omitted > 0 {
		fmt.Fprintf(&b, "- (%d further conflict(s) omitted to stay within budget)\n", omitted)
	}
	b.WriteString("Reviewer: treat each claim above as unproven until its required_evidence " +
		"is present in the answer.\n")
	return b.String()
}

func renderContradiction(c Contradiction) string {
	decision := strings.TrimSpace(c.Decision)
	if decision == "" {
		decision = DecisionRevise
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- claim: `%s`\n", truncateRunes(collapseSpaces(c.Claim), maxClaimTextLen))
	fmt.Fprintf(&b, "  decision: %s\n", truncateRunes(collapseSpaces(decision), 32))
	fmt.Fprintf(&b, "  reason: %s\n", truncateRunes(collapseSpaces(c.Reason), maxReasonTextLen))
	fmt.Fprintf(&b, "  fact_confidence: %.2f\n", c.Confidence)
	if len(c.RequiredEvidence) > 0 {
		b.WriteString("  required_evidence:\n")
		for i, e := range c.RequiredEvidence {
			if i >= 3 {
				break
			}
			fmt.Fprintf(&b, "    - %s\n", truncateRunes(collapseSpaces(e), maxEvidenceTextLen))
		}
	}
	return b.String()
}
