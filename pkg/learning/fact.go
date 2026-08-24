package learning

import (
	"strings"

	"github.com/UnicoLab/slmcode/pkg/memory"
)

// The bridge from the prose learning stack to the typed one.
//
// Two learning stacks used to run side by side and never speak: pkg/memory
// stores Facts with a Beta-posterior confidence, contradiction handling,
// supersession and pruning, while pkg/learning appended flat Markdown bullets
// with no confidence, no dedup and no provenance — and it was the *prose* one
// that reached prompts. A lesson that turned out to be wrong could never be
// outvoted, and a lesson observed ten times counted exactly as much as one
// typed once.
//
// Routing lessons into memory.Facts fixes that for free: the same claim seen
// twice gains support, a rival claim about the same subject contradicts it and
// eventually supersedes it, low-confidence facts stop being rendered, and the
// store prunes itself. The Markdown mirror keeps being written — this is
// additive, and MEMORY.md stays the human's copy.
//
// Direction of the dependency: learning → memory, never the reverse. pkg/memory
// is the dependency-light store that pkg/evolve, pkg/loop, pkg/orchestrator and
// cmd/slmcode all sit on; pkg/learning is one producer of one kind of
// observation, and it already pulls in pkg/plan. Pointing the edge the other
// way would drag the task/kanban model into every consumer of memory to serve a
// single conversion, and would invert the layering. Neither package imported
// the other before this change, so the choice was free — and this is the way
// that stays free.

// lessonSubjectWords is how many significant words form a lesson's subject.
//
// The subject is a fact's identity, so this number decides when two lessons are
// talking about the same thing. Too many words and every rephrasing becomes a
// brand-new fact that can never be confirmed or refuted; too few and unrelated
// lessons collide and fight. Five is the leading noun phrase of a typical
// bullet — enough to separate "qa_gate smoke command…" from "acceptance
// pattern…", coarse enough that two claims about the same gate meet.
const lessonSubjectWords = 5

// lessonSubjectPrefix namespaces lesson-derived subjects so they can never
// collide with a distilled fact, whose subjects are commands and paths.
const lessonSubjectPrefix = "lesson: "

// FactKindFor maps a lesson kind onto the semantic fact kind it belongs in.
//
//	failure    → gotcha     (a trap that cost a run — exactly what a gotcha is)
//	convention → convention
//	success    → convention (a pattern observed to work here)
//
// Anything else lands in convention: an unrecognized kind is still an
// observation about how this project behaves, and inventing a new FactKind for
// it would put it in a heading no prompt template knows about.
func FactKindFor(lessonKind string) memory.FactKind {
	if strings.EqualFold(strings.TrimSpace(lessonKind), "failure") {
		return memory.FactGotcha
	}
	return memory.FactConvention
}

// FactFromLesson converts one prose lesson into a typed semantic fact.
//
// Sources carries the originating task and run id, so provenance is queryable
// from facts.json rather than only readable in MEMORY.md. Confidence is
// deliberately NOT set here: it is the store's Beta posterior, computed from
// support and contradictions as the fact is observed over time.
//
// Returns false for a lesson with no usable text.
func FactFromLesson(l Lesson, runID string) (memory.Fact, bool) {
	text := strings.TrimSpace(l.Text)
	if text == "" {
		return memory.Fact{}, false
	}
	subject := lessonSubject(text)
	if subject == "" {
		return memory.Fact{}, false
	}
	var sources []string
	for _, s := range []string{strings.TrimSpace(l.TaskID), strings.TrimSpace(runID)} {
		if s != "" {
			sources = append(sources, s)
		}
	}
	return memory.Fact{
		Kind:    FactKindFor(l.Kind),
		Subject: lessonSubjectPrefix + subject,
		Text:    text,
		Sources: sources,
	}, true
}

// RecordFacts folds lessons into semantic memory and reports how many were
// observed. A nil store is a no-op, so callers running without an evolve engine
// need no special case.
//
// Nothing here can fail loudly: a lesson that will not convert is skipped. This
// is a best-effort enrichment of a run that has already done its real work.
func RecordFacts(facts *memory.Facts, lessons []Lesson, runID string) int {
	if facts == nil || len(lessons) == 0 {
		return 0
	}
	n := 0
	for _, l := range lessons {
		f, ok := FactFromLesson(l, runID)
		if !ok {
			continue
		}
		if got := facts.Observe(f); got.ID != "" {
			n++
		}
	}
	return n
}

// LessonsFromMarkdown parses a MEMORY.md block and routes it into semantic
// memory in one step — the path for lessons that arrived as prose (an SLM
// distillation, a human edit) rather than as a []Lesson.
func LessonsFromMarkdown(facts *memory.Facts, md, runID string) int {
	return RecordFacts(facts, ParseMarkdown(md), runID)
}

// lessonSubject reduces a lesson's text to the coarse topic it is about:
// lowercase, task ids and bare numbers removed, stopwords dropped, first few
// significant words kept.
//
// Task ids in particular must go. They look stable ("T1") but are per-run, so
// keeping them would both split one recurring claim across every run that made
// it and collide two unrelated claims that happened to be task 1.
func lessonSubject(text string) string {
	words := significantWords(text, lessonSubjectWords)
	if len(words) == 0 {
		// Nothing survived normalization (all ids, digits or punctuation).
		// Fall back to the raw text so the lesson still gets an identity.
		return clipRunes(strings.ToLower(strings.Join(strings.Fields(text), " ")), 120)
	}
	return strings.Join(words, " ")
}

// lessonStopwords are dropped from a subject: they pad the word budget without
// saying what the lesson is about.
var lessonStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "into": true, "was": true, "were": true, "are": true, "is": true,
	"to": true, "of": true, "in": true, "on": true, "an": true, "at": true,
	"be": true, "it": true, "its": true, "as": true, "by": true, "or": true,
	"do": true, "not": true, "but": true, "all": true, "we": true, "you": true,
}

// significantWords lowercases text, splits it on anything that is not a letter,
// digit or underscore (so `qa_gate` and `max_parallel` survive whole), and
// returns at most max content words.
func significantWords(text string, max int) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		w := cur.String()
		cur.Reset()
		if len(out) >= max || len(w) < 2 || lessonStopwords[w] || isTaskID(w) || isAllDigits(w) {
			return
		}
		out = append(out, w)
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			cur.WriteRune(r)
		case r >= 128: // keep non-ASCII letters together rather than splitting words
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// isTaskID matches the board's per-run identifiers ("t1", "t12").
func isTaskID(w string) bool {
	return len(w) > 1 && w[0] == 't' && isAllDigits(w[1:])
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// clipRunes shortens s to at most n bytes without splitting a rune.
func clipRunes(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
