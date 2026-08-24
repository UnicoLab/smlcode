package quality

import (
	"strings"
	"testing"

	"github.com/UnicoLab/slmcode/pkg/memory"
)

// goTestFact is the stand-in for "this project's test command", recorded with
// enough support to be worth believing. Confidence is set explicitly rather
// than via a store so the floor can be probed exactly.
func goTestFact(conf float64) memory.Fact {
	return memory.Fact{
		Kind:       memory.FactCommand,
		Subject:    "go test ./...",
		Text:       "`go test ./...` works here (4/4 runs succeeded)",
		Support:    4,
		Confidence: conf,
	}
}

// ── extraction ──────────────────────────────────────────────────────────────

func TestExtractClaimsFindsFencedAndPromptCommandsButNotProse(t *testing.T) {
	output := "I finished the change.\n\n" +
		"Running the suite is easy: the npm test script is documented in the README,\n" +
		"and you should run pytest before pushing.\n\n" +
		"```bash\n" +
		"go build ./...\n" +
		"```\n\n" +
		"Then:\n\n" +
		"$ golangci-lint run\n\n" +
		"I ran `yarn lint` afterwards.\n"

	claims := ExtractClaims(output)
	got := map[string]bool{}
	for _, c := range claims {
		if c.Kind != ClaimCommand {
			t.Fatalf("unexpected claim kind %q", c.Kind)
		}
		if strings.TrimSpace(c.Line) == "" {
			t.Fatalf("claim %q carries no source line", c.Text)
		}
		got[c.Text] = true
	}

	for _, want := range []string{"go build ./...", "golangci-lint run", "yarn lint"} {
		if !got[want] {
			t.Fatalf("claim %q not extracted; got %v", want, got)
		}
	}
	// Prose that merely NAMES a command is advice, not a claim. Admitting these
	// is how the reviewer ends up rejecting correct work.
	for _, never := range []string{"npm test", "pytest"} {
		if got[never] {
			t.Fatalf("prose mention %q was extracted as a claim; got %v", never, got)
		}
	}
}

// A shell transcript is mostly OUTPUT. None of it may become a claim.
func TestExtractClaimsIgnoresCommandOutputInsideShellBlocks(t *testing.T) {
	output := "```console\n" +
		"$ go test ./...\n" +
		"--- FAIL: TestX (0.00s)\n" +
		"FAIL\tgithub.com/x/y\t0.5s\n" +
		"ok  \tgithub.com/x/z\t0.1s\n" +
		"make[1]: Entering directory '/tmp'\n" +
		"PASS  src/a.test.js\n" +
		"Tests: 3 passed\n" +
		"```\n"

	claims := ExtractClaims(output)
	if len(claims) != 1 || claims[0].Text != "go test ./..." {
		t.Fatalf("expected exactly the prompted command, got %+v", claims)
	}
}

// An untagged fence can hold anything — JSON, a diff, a stack trace. Reading it
// as shell is the cheapest possible way to manufacture a false claim.
func TestExtractClaimsSkipsUntaggedAndNonShellFences(t *testing.T) {
	output := "```\n" +
		"go build ./...\n" +
		"```\n" +
		"```json\n" +
		"npm test\n" +
		"```\n"
	if claims := ExtractClaims(output); len(claims) != 0 {
		t.Fatalf("untagged/non-shell fences must yield no claims, got %+v", claims)
	}
}

// The verb list is closed on purpose: a claim is something the answer says it
// DID. Advice ("you should run…", "try running…") is not a claim, and reading
// it as one would let a helpful sentence get the work rejected.
func TestExtractClaimsOnlyAcceptsPastTenseClaimVerbs(t *testing.T) {
	cases := []struct {
		line string
		want string // "" means: not a claim
	}{
		{"ran `npm test`", "npm test"},
		{"I ran npm test", "npm test"},
		{"executed: pytest -q", "pytest -q"},
		{"invoked `golangci-lint run` to be sure", "golangci-lint run"},
		{"Ran into an error while wiring it up.", ""},
		{"You should run pytest before pushing.", ""},
		{"Try running `npm test` next time.", ""},
		{"The npm test script is defined in package.json.", ""},
		{"ran the tests and everything passed", ""},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			claims := ExtractClaims(tc.line)
			switch {
			case tc.want == "":
				if len(claims) != 0 {
					t.Fatalf("%q must not be a claim, got %+v", tc.line, claims)
				}
			case len(claims) != 1 || claims[0].Text != tc.want:
				t.Fatalf("%q → want claim %q, got %+v", tc.line, tc.want, claims)
			}
		})
	}
}

// The floor is load-bearing arithmetic, not a magic number: it has to sit
// between "confirmed once" (0.67) and "confirmed once, contradicted once" (0.5).
func TestGroundingFloorSitsBetweenOneSightingAndACoinFlip(t *testing.T) {
	oneSighting := memory.Fact{Support: 1}
	oneEach := memory.Fact{Support: 1, Contradict: 1}
	// Mirror memory's own posterior so the floor is checked against the real
	// arithmetic rather than a copied constant.
	conf := func(f memory.Fact) float64 {
		return float64(f.Support+1) / float64(f.Support+f.Contradict+2)
	}
	if conf(oneSighting) < minGroundingConfidence {
		t.Fatalf("floor %v rejects a single clean sighting (%v)", minGroundingConfidence, conf(oneSighting))
	}
	if conf(oneEach) >= minGroundingConfidence {
		t.Fatalf("floor %v admits a coin flip (%v)", minGroundingConfidence, conf(oneEach))
	}
}

func TestExtractClaimsIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("```sh\n")
	for i := 0; i < 200; i++ {
		b.WriteString("go build ./pkg/p")
		b.WriteByte(byte('a' + i%26))
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	if n := len(ExtractClaims(b.String())); n > maxClaims {
		t.Fatalf("extraction is unbounded: %d claims", n)
	}
}

// ── reconciliation: the false-positive guards ───────────────────────────────

// TestReconcileNoFalsePositiveWhenClaimMatchesKnownFact is the guard that
// matters most: a correct answer must never be accused. A claim that IS the
// recorded command, and a claim that merely uses the same recorded TOOL, both
// have to pass silently.
func TestReconcileNoFalsePositiveWhenClaimMatchesKnownFact(t *testing.T) {
	facts := []memory.Fact{goTestFact(0.83)}
	for _, claim := range []string{
		"go test ./...",           // exactly the recorded command
		"go test ./pkg/quality/…", // same tool, narrower scope
		"go test ./... -race",     // same command plus a flag
		"go build ./...",          // same tool, different job
		"go vet ./...",            // same tool, different job again
	} {
		t.Run(claim, func(t *testing.T) {
			cs := Reconcile([]Claim{{Kind: ClaimCommand, Text: claim}}, facts)
			if len(cs) != 0 {
				t.Fatalf("claim %q was falsely contradicted: %+v", claim, cs)
			}
		})
	}
}

// A tool the project is known to use gets permanent amnesty, at ANY confidence
// and for any job — polyglot repos are normal, and a Go repo with a web/ tree
// must not have its npm runs called hallucinations.
func TestReconcileNoFalsePositiveForAToolTheProjectIsKnownToUse(t *testing.T) {
	facts := []memory.Fact{
		goTestFact(0.83),
		{Kind: memory.FactCommand, Subject: "npm install", Confidence: 0.5, Support: 1},
	}
	if cs := Reconcile([]Claim{{Kind: ClaimCommand, Text: "npm test"}}, facts); len(cs) != 0 {
		t.Fatalf("a known tool must never be contradicted: %+v", cs)
	}
}

func TestReconcileWhitespaceAndQuotingDifferencesAreNotConflicts(t *testing.T) {
	facts := []memory.Fact{{
		Kind: memory.FactCommand, Subject: "npm run lint", Confidence: 0.9, Support: 8,
	}}
	for _, claim := range []string{
		"npm  run   lint",
		"npm run \"lint\"",
		"\tnpm run lint  ",
		"npm run lint --fix",
	} {
		t.Run(claim, func(t *testing.T) {
			if cs := Reconcile([]Claim{{Kind: ClaimCommand, Text: claim}}, facts); len(cs) != 0 {
				t.Fatalf("formatting difference produced a contradiction: %+v", cs)
			}
		})
	}
}

func TestReconcileIgnoresFactsBelowTheConfidenceFloor(t *testing.T) {
	// 0.5 is "confirmed once, contradicted once" — a coin flip, and no basis
	// for telling a worker its answer is wrong.
	low := goTestFact(0.5)
	if low.Confidence >= minGroundingConfidence {
		t.Fatalf("fixture is not below the floor (%v)", minGroundingConfidence)
	}
	cs := Reconcile([]Claim{{Kind: ClaimCommand, Text: "npm test"}}, []memory.Fact{low})
	if len(cs) != 0 {
		t.Fatalf("a coin-flip fact must not contradict anything: %+v", cs)
	}
}

func TestReconcileEmptyInputs(t *testing.T) {
	if cs := Reconcile(nil, []memory.Fact{goTestFact(0.9)}); len(cs) != 0 {
		t.Fatalf("no claims must yield no contradictions: %+v", cs)
	}
	if cs := Reconcile([]Claim{{Kind: ClaimCommand, Text: "npm test"}}, nil); len(cs) != 0 {
		t.Fatalf("no facts must yield no contradictions: %+v", cs)
	}
	// Facts of a kind this file does not reconcile are not facts about commands.
	other := []memory.Fact{{Kind: memory.FactConvention, Subject: "tests", Confidence: 0.99}}
	if cs := Reconcile([]Claim{{Kind: ClaimCommand, Text: "npm test"}}, other); len(cs) != 0 {
		t.Fatalf("a non-command fact must not contradict a command claim: %+v", cs)
	}
}

// ── reconciliation: the true positive ───────────────────────────────────────

func TestReconcileFlagsAClaimAgainstAHighConfidenceFact(t *testing.T) {
	fact := goTestFact(0.83)
	cs := Reconcile([]Claim{{Kind: ClaimCommand, Text: "npm test", Line: "$ npm test"}},
		[]memory.Fact{fact})
	if len(cs) != 1 {
		t.Fatalf("want exactly one contradiction, got %d: %+v", len(cs), cs)
	}
	c := cs[0]
	if c.Decision != DecisionRevise {
		t.Fatalf("decision = %q, want %q", c.Decision, DecisionRevise)
	}
	if c.Claim != "npm test" {
		t.Fatalf("claim = %q", c.Claim)
	}
	if c.Confidence != fact.Confidence {
		t.Fatalf("confidence = %v, want the contradicting fact's %v", c.Confidence, fact.Confidence)
	}
	if !strings.Contains(c.Reason, "go test ./...") || !strings.Contains(c.Reason, "npm") {
		t.Fatalf("reason must name both sides: %q", c.Reason)
	}
	if len(c.RequiredEvidence) == 0 {
		t.Fatalf("a contradiction with no required evidence is just an opinion: %+v", c)
	}
	for _, e := range c.RequiredEvidence {
		if strings.TrimSpace(e) == "" {
			t.Fatalf("empty required evidence: %+v", c.RequiredEvidence)
		}
	}
}

func TestReconcileIsDeterministicAndBounded(t *testing.T) {
	facts := []memory.Fact{
		goTestFact(0.83),
		{Kind: memory.FactCommand, Subject: "go build ./...", Confidence: 0.75, Support: 2},
		{Kind: memory.FactCommand, Subject: "gofmt -l .", Confidence: 0.9, Support: 9},
	}
	claims := []Claim{
		{Kind: ClaimCommand, Text: "yarn test"},
		{Kind: ClaimCommand, Text: "cargo build"},
		{Kind: ClaimCommand, Text: "pytest"},
		{Kind: ClaimCommand, Text: "prettier ."},
		{Kind: ClaimCommand, Text: "jest"},
		{Kind: ClaimCommand, Text: "mvn package"},
		{Kind: ClaimCommand, Text: "black ."},
	}
	first := Reconcile(claims, facts)
	if len(first) == 0 {
		t.Fatal("fixture produced no contradictions at all")
	}
	if len(first) > maxContradictions {
		t.Fatalf("unbounded: %d contradictions", len(first))
	}
	// Sorted, so the store's own ordering cannot leak into the report.
	for i := 1; i < len(first); i++ {
		if first[i-1].Claim > first[i].Claim {
			t.Fatalf("contradictions are not sorted: %q before %q", first[i-1].Claim, first[i].Claim)
		}
	}
	reversed := make([]memory.Fact, 0, len(facts))
	for i := len(facts) - 1; i >= 0; i-- {
		reversed = append(reversed, facts[i])
	}
	second := Reconcile(claims, reversed)
	if RenderContradictions(first) != RenderContradictions(second) {
		t.Fatal("output depends on the order facts were supplied")
	}
}

// ── rendering ───────────────────────────────────────────────────────────────

func TestRenderContradictionsIsEmptyWhenThereIsNothingToSay(t *testing.T) {
	if got := RenderContradictions(nil); got != "" {
		t.Fatalf("want empty section, got %q", got)
	}
}

func TestRenderContradictionsIsDeterministicAndBounded(t *testing.T) {
	var cs []Contradiction
	for i := 0; i < 40; i++ {
		cs = append(cs, Contradiction{
			Decision:         DecisionRevise,
			Claim:            strings.Repeat("npm run verylongscriptname", 6),
			Reason:           strings.Repeat("this project has no recorded use of npm. ", 20),
			RequiredEvidence: []string{strings.Repeat("show the ws_shell call. ", 20)},
			Confidence:       0.83,
		})
	}
	out := RenderContradictions(cs)
	if out != RenderContradictions(cs) {
		t.Fatal("rendering is not deterministic")
	}
	if len(out) > maxKnowledgeBytes+400 {
		t.Fatalf("section is unbounded: %d bytes", len(out))
	}
	if !strings.Contains(out, "omitted") {
		t.Fatalf("a truncated section must say so:\n%s", out)
	}
}

func TestRenderContradictionsShapeAndHeader(t *testing.T) {
	out := RenderContradictions(Reconcile(
		[]Claim{{Kind: ClaimCommand, Text: "npm test"}},
		[]memory.Fact{goTestFact(0.83)},
	))
	for _, want := range []string{
		"\n" + KnowledgeSectionHeader + "\n",
		"- claim: `npm test`",
		"decision: " + DecisionRevise,
		"required_evidence:",
		"fact_confidence: 0.83",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("section is missing %q:\n%s", want, out)
		}
	}
	// This section reports unverified claims, never a proven failure: minting
	// FAILED here would let a probabilistic record trip disk-backed gates.
	if strings.Contains(out, SmokeFailedMarker) {
		t.Fatalf("knowledge section must not mint a FAILED verdict:\n%s", out)
	}
	if StaticFailedInOutput(out) || ClaimsFailedInOutput(out) || SmokeFailedInOutput(out) {
		t.Fatalf("knowledge section trips an unrelated gate:\n%s", out)
	}
}

// The section is harness-authored, so it must be stripped before the model's
// own answer is judged for completeness — the same contract every other gate
// section has, via the same single list.
func TestKnowledgeSectionIsStrippedBeforeCompletenessIsJudged(t *testing.T) {
	if !containsHeader(HarnessSectionHeaders, KnowledgeSectionHeader) {
		t.Fatalf("%q is missing from HarnessSectionHeaders", KnowledgeSectionHeader)
	}
	core := `{"status":"done","summary":"ok","files_changed":["a.go"]}`
	sec := RenderContradictions(Reconcile(
		[]Claim{{Kind: ClaimCommand, Text: "npm test"}},
		[]memory.Fact{goTestFact(0.83)},
	))
	if got := StripHarnessSections(core + sec); got != core {
		t.Fatalf("knowledge section survived stripping:\n%q", got)
	}
}

// ── end to end, still with no store and no model ────────────────────────────

func TestGroundEndToEndOverRealisticWorkerOutput(t *testing.T) {
	output := "Implemented the parser.\n\n" +
		"```bash\n" +
		"$ npm test\n" +
		"PASS  src/parser.test.js\n" +
		"```\n\n" +
		`{"status":"done","summary":"parser","files_changed":["parser.go"]}` + "\n"

	cs := Reconcile(ExtractClaims(output), []memory.Fact{goTestFact(0.83)})
	if len(cs) != 1 || cs[0].Claim != "npm test" {
		t.Fatalf("want one contradiction about npm test, got %+v", cs)
	}
	if !strings.Contains(RenderContradictions(cs), KnowledgeSectionHeader) {
		t.Fatal("rendered section lost its header")
	}
}
