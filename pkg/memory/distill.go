package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Distillation tuning.
const (
	// distillMinRuns is how many observations a command needs before it is
	// promoted to a semantic fact. One lucky `go test` is not a project fact.
	distillMinRuns = 2
	// distillTopDirs and distillTopFiles bound the layout facts.
	distillTopDirs  = 5
	distillTopFiles = 8
	// distillWindow is how many recent episodes a distillation reads.
	distillWindow = 120
)

// Summarizer is the optional LLM hook. It takes a prompt and returns prose.
// Any error, empty result or timeout is ignored: distillation is complete and
// correct without it.
type Summarizer func(ctx context.Context, prompt string) (string, error)

// Distill folds episodic memory into semantic memory.
//
// The deterministic pass is the product. It aggregates commands by success
// rate, directories and files by change frequency, resolved failures into
// gotchas, and edit-format outcomes into conventions — all by counting, with
// no model involved. summarize may be nil; when supplied it is used only to
// add ONE extra fact (a project brief) that is treated like any other
// low-support observation and can be contradicted away.
func (s *Store) Distill(ctx context.Context, summarize Summarizer) error {
	if s.readOnly {
		return nil
	}
	episodes := s.episodes.Recent(distillWindow)
	if len(episodes) == 0 {
		return nil
	}
	for _, f := range DistillFacts(episodes, s.now()) {
		s.facts.Observe(f)
	}
	if summarize != nil {
		s.distillWithLLM(ctx, episodes, summarize)
	}
	return s.facts.Flush()
}

// DistillFacts is the pure, deterministic aggregation. Exported so callers can
// test or preview a distillation without touching a store.
func DistillFacts(episodes []Episode, now time.Time) []Fact {
	if len(episodes) == 0 {
		return nil
	}
	var out []Fact

	type tally struct {
		ok, fail int
		last     time.Time
		src      []string
	}
	commands := map[string]*tally{}
	dirs := map[string]int{}
	files := map[string]*tally{}
	gotchas := map[string]*struct {
		fix   string
		count int
		src   []string
	}{}
	editFmt := map[string]*tally{}
	langs := map[string]int{}

	for _, e := range episodes {
		if e.Language != "" {
			langs[e.Language]++
		}
		for _, c := range e.Commands {
			cmd := normalizeCommand(c.Cmd)
			if cmd == "" {
				continue
			}
			t := commands[cmd]
			if t == nil {
				t = &tally{}
				commands[cmd] = t
			}
			if c.OK {
				t.ok++
			} else {
				t.fail++
			}
			t.last = maxTime(t.last, e.At)
			t.src = dedupe(append(t.src, e.ID), MaxFactSources)
		}
		for _, p := range e.FilesChanged {
			if d := dirOf(p); d != "" {
				dirs[d]++
			}
			t := files[p]
			if t == nil {
				t = &tally{}
				files[p] = t
			}
			t.ok++
			t.last = maxTime(t.last, e.At)
			t.src = dedupe(append(t.src, e.ID), MaxFactSources)
		}
		for _, f := range e.Failures {
			if !f.Resolved() {
				continue
			}
			key := f.Fingerprint
			if key == "" {
				key = firstLine(f.Message, 90)
			}
			if key == "" {
				continue
			}
			g := gotchas[key]
			if g == nil {
				g = &struct {
					fix   string
					count int
					src   []string
				}{}
				gotchas[key] = g
			}
			g.count++
			if fix := orElse(f.Resolution, f.ResolvedBy); fix != "unknown" {
				g.fix = fix
			}
			g.src = dedupe(append(g.src, e.ID), MaxFactSources)
			if g.fix != "" {
				out = append(out, Fact{
					Kind:    FactGotcha,
					Subject: key,
					Text: fmt.Sprintf("%s — fix: %s (seen %d×)",
						firstLine(f.Message, 100), firstLine(g.fix, 90), g.count),
					Sources: g.src,
				})
			}
		}
		if e.EditFormat != "" && e.EditsAttempted > 0 {
			t := editFmt[e.EditFormat]
			if t == nil {
				t = &tally{}
				editFmt[e.EditFormat] = t
			}
			t.ok += e.EditsApplied
			t.fail += e.EditsAttempted - e.EditsApplied
			t.src = dedupe(append(t.src, e.ID), MaxFactSources)
		}
	}

	for _, cmd := range sortedKeys(commands) {
		t := commands[cmd]
		runs := t.ok + t.fail
		if runs < distillMinRuns || t.ok == 0 {
			continue
		}
		out = append(out, Fact{
			Kind:    FactCommand,
			Subject: cmd,
			Text: fmt.Sprintf("`%s` works here (%d/%d runs succeeded)",
				cmd, t.ok, runs),
			Sources: t.src,
		})
	}

	if len(dirs) > 0 {
		type kv struct {
			k string
			n int
		}
		var list []kv
		for _, k := range sortedKeys(dirs) {
			list = append(list, kv{k, dirs[k]})
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].n > list[j].n })
		if len(list) > distillTopDirs {
			list = list[:distillTopDirs]
		}
		names := make([]string, 0, len(list))
		for _, e := range list {
			names = append(names, fmt.Sprintf("`%s` (%d)", e.k, e.n))
		}
		out = append(out, Fact{
			Kind:    FactLayout,
			Subject: "hot-directories",
			Text:    "Most work happens in: " + strings.Join(names, ", "),
		})
	}

	fileNames := sortedKeys(files)
	sort.SliceStable(fileNames, func(i, j int) bool {
		return files[fileNames[i]].ok > files[fileNames[j]].ok
	})
	for i, p := range fileNames {
		if i >= distillTopFiles {
			break
		}
		t := files[p]
		if t.ok < distillMinRuns {
			continue
		}
		out = append(out, Fact{
			Kind:    FactFile,
			Subject: p,
			Text:    fmt.Sprintf("`%s` changed in %d recent runs", p, t.ok),
			Sources: t.src,
		})
	}

	for _, format := range sortedKeys(editFmt) {
		t := editFmt[format]
		total := t.ok + t.fail
		if total < distillMinRuns {
			continue
		}
		out = append(out, Fact{
			Kind:    FactConvention,
			Subject: "edit-format:" + format,
			Text: fmt.Sprintf("Edit format `%s` applies %d%% of the time here (%d/%d hunks)",
				format, pct(t.ok, total), t.ok, total),
			Sources: t.src,
		})
	}

	if len(langs) > 0 {
		best, bestN := "", 0
		for _, l := range sortedKeys(langs) {
			if langs[l] > bestN {
				best, bestN = l, langs[l]
			}
		}
		if best != "" {
			out = append(out, Fact{
				Kind:    FactDependency,
				Subject: "primary-language",
				Text:    fmt.Sprintf("Primary language is %s (%d/%d recent runs)", best, bestN, len(episodes)),
			})
		}
	}
	return out
}

func (s *Store) distillWithLLM(ctx context.Context, episodes []Episode, summarize Summarizer) {
	if summarize == nil || len(episodes) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("Summarize what a new engineer must know about this project in at most 3 bullet points. ")
	b.WriteString("Only state things supported by the log below. No preamble.\n\n")
	for i, e := range episodes {
		if i >= 20 {
			break
		}
		fmt.Fprintf(&b, "- %s | files: %s | verdict: %s\n",
			firstLine(e.Query, 100), strings.Join(dedupe(e.FilesChanged, 3), ", "), e.Verdict)
	}
	out, err := summarize(ctx, b.String())
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	// The LLM's contribution enters as an ordinary low-support fact: it can be
	// contradicted, decayed and pruned exactly like a counted one.
	s.facts.Observe(Fact{
		Kind:    FactConvention,
		Subject: "project-brief",
		Text:    clip(strings.TrimSpace(out), MaxFactTextLen),
	})
}

// normalizeCommand strips shell noise so `go test ./... -count=1` and
// `go test ./...` are recognized as the same habit.
func normalizeCommand(cmd string) string {
	c := strings.TrimSpace(cmd)
	if c == "" {
		return ""
	}
	if i := strings.IndexAny(c, "\n\r"); i >= 0 {
		c = c[:i]
	}
	c = strings.TrimPrefix(c, "$ ")
	fields := strings.Fields(c)
	kept := make([]string, 0, len(fields))
	for _, f := range fields {
		if strings.HasPrefix(f, "-") {
			continue // flags are run-specific noise, not part of the habit
		}
		kept = append(kept, f)
		if len(kept) >= 6 {
			break
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return clip(strings.Join(kept, " "), 120)
}

func dirOf(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return ""
	}
	return p[:i]
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func pct(n, total int) int {
	if total <= 0 {
		return 0
	}
	return n * 100 / total
}
