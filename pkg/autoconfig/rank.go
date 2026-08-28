package autoconfig

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ── Picking a model to write code with ───────────────────────────────────
//
// A model server serves whatever it was given, and the list is rarely all
// chat models: embedding models, rerankers, speech and vision models and safety
// classifiers sit next to the one you want. Picking the first entry — which is
// what "just use the default" amounts to — routinely picks one of those, and
// the failure it produces is baffling rather than obvious: the harness runs, the
// model answers, and nothing it says is JSON.
//
// So the ranking is two rules, in this order of importance:
//
//  1. RULE OUT what cannot do the job. An embedding model will never write Go
//     no matter how large it is, and this is where nearly all the value is.
//  2. Among what is left, prefer models tuned for CODE, then instruction-tuned
//     ones, then bigger over smaller.
//
// Deliberately not a curated allowlist of known model names. This has to work
// for models that did not exist when it was written — which is the whole point
// of reading the server's own list rather than shipping a table.

// Ranked is one model with the reasoning that placed it.
type Ranked struct {
	Name string
	// Score is higher-is-better; negative means ruled out.
	Score int
	// Why is a short human-readable justification.
	Why string
	// Usable is false for models that cannot do this job at all.
	Usable bool
}

// disqualifiers mark a model that cannot write code, whatever its size.
//
// Matched as whole words or hyphen/underscore-delimited segments so a model
// legitimately called "codestral" is not ruled out for containing "tts", which
// is the kind of bug a naive substring match ships with.
var disqualifiers = []string{
	"embed", "embedding", "bge", "gte", "e5",
	"rerank", "reranker",
	"whisper", "tts", "stt", "voice", "audio", "speech", "parler",
	"vision", "vl", "clip", "siglip", "ocr",
	"guard", "shield", "moderation", "safety", "prompt-guard",
	"diffusion", "sd", "flux", "image",
}

// codeSignals mark a model tuned for programming.
var codeSignals = []string{"coder", "code", "codestral", "starcoder", "codellama", "devstral", "codegemma"}

// instructSignals mark an instruction-following chat model, which is the
// minimum this harness needs — a base model completes text and never answers a
// contract.
var instructSignals = []string{"instruct", "-it", "chat", "-hf", "sft"}

// baseSignals mark a raw completion model, which cannot follow the prompts.
var baseSignals = []string{"base", "pt"}

// sizeRe finds a parameter count: 7b, 30b, 1.5b, 480b.
var sizeRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9.])(\d+(?:\.\d+)?)\s*b(?:[^a-z0-9]|$)`)

// activeRe finds a MoE active-parameter count: a3b in "30B-A3B".
var activeRe = regexp.MustCompile(`(?i)[-_]a(\d+(?:\.\d+)?)b(?:[^a-z0-9]|$)`)

// Rank scores every model name for writing code, best first.
//
// Stable: equal scores keep the server's own order, so re-running against an
// unchanged server produces the same answer and a user is never told their
// configuration drifted when nothing did.
func Rank(names []string) []Ranked {
	out := make([]Ranked, 0, len(names))
	for _, name := range names {
		out = append(out, score(name))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// Best returns the highest-ranked usable model, "" when none can do the job.
func Best(names []string) (Ranked, bool) {
	for _, r := range Rank(names) {
		if r.Usable {
			return r, true
		}
	}
	return Ranked{}, false
}

func score(name string) Ranked {
	segs := segments(name)
	if bad, ok := firstMatch(segs, disqualifiers); ok {
		return Ranked{Name: name, Score: -1, Usable: false, Why: "not a coding model (" + bad + ")"}
	}

	r := Ranked{Name: name, Usable: true, Score: 10}
	var why []string

	if hit, ok := firstMatch(segs, codeSignals); ok {
		r.Score += 100
		why = append(why, "tuned for code ("+hit+")")
	}
	switch {
	case hasAny(segs, instructSignals):
		r.Score += 40
		why = append(why, "instruction-tuned")
	case hasAny(segs, baseSignals):
		// A base model completes text; it never answers a JSON contract. Usable
		// only in the sense that nothing better may exist.
		r.Score -= 30
		why = append(why, "base model — completes text rather than following instructions")
	}

	// Size, when the name says. Bigger is better up to a point: the gap between
	// 1B and 7B is the difference between working and not, while the gap between
	// 32B and 70B is throughput a local box usually cannot spend.
	if b, ok := paramsB(name); ok {
		r.Score += sizeScore(b)
		why = append(why, trimFloat(b)+"B")
		if a, ok := activeB(name); ok {
			// A mixture-of-experts model runs at its ACTIVE parameter cost, which
			// is what makes a 30B usable on a laptop. Worth saying, not scoring
			// twice.
			why = append(why, trimFloat(a)+"B active")
		}
	}
	if len(why) == 0 {
		why = append(why, "a chat model")
	}
	r.Why = strings.Join(why, ", ")
	return r
}

// sizeScore maps parameters to a preference, flattening past the point where
// more parameters stop buying capability a local run can use.
func sizeScore(b float64) int {
	switch {
	case b >= 24:
		return 30
	case b >= 12:
		return 25
	case b >= 6:
		return 18
	case b >= 3:
		return 8
	default:
		// Under 3B a model is rarely able to hold this harness's contracts.
		return -10
	}
}

// segments splits a model id into comparable tokens, so a match is on a whole
// segment rather than any substring: "codestral" must not match "tts", and
// "qwen3-vl" must.
func segments(name string) []string {
	fields := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '-' || r == '_' || r == '/' || r == ':' || r == '.' || r == ' '
	})
	return fields
}

func firstMatch(segs, needles []string) (string, bool) {
	for _, s := range segs {
		for _, n := range needles {
			if s == strings.TrimPrefix(n, "-") {
				return n, true
			}
		}
	}
	return "", false
}

func hasAny(segs, needles []string) bool {
	_, ok := firstMatch(segs, needles)
	return ok
}

func paramsB(name string) (float64, bool) {
	// The ACTIVE count in a MoE name ("30B-A3B") must not be mistaken for the
	// total, so it is stripped before the total is read.
	cleaned := activeRe.ReplaceAllString(name, " ")
	m := sizeRe.FindStringSubmatch(cleaned)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

func activeB(name string) (float64, bool) {
	m := activeRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
