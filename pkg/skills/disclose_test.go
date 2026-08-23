package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fatBody(marker string, n int) string {
	return "# " + marker + "\n\n" + strings.Repeat(marker+" directive line\n", n)
}

func seedSkills(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := WriteSkill(dir, Skill{
		Name: "specialist-worker", Description: "Implement a scoped change",
		Triggers: []string{"implement"}, Agents: []string{"worker"},
		Body: fatBody("WORKER", 60), UserInvocable: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSkill(dir, Skill{
		Name: "atomic-coding", Description: "Tiny reviewable edits",
		Triggers: []string{"atomic"}, Body: fatBody("ATOMIC", 60), UserInvocable: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSkill(dir, Skill{
		Name: "multipass-quality", Description: "Multi-pass review",
		Triggers: []string{"quality"}, Body: fatBody("MULTIPASS", 60), UserInvocable: true,
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProgressiveDisclosure(t *testing.T) {
	dir := seedSkills(t)
	l := NewLoader(dir)

	tests := []struct {
		name         string
		agent        string
		query        string
		wantExpanded []string
		wantCards    []string
		wantNotBody  []string
	}{
		{
			name:         "explicit ref expands",
			agent:        "worker",
			query:        "use @skill:atomic-coding and implement it",
			wantExpanded: []string{"ATOMIC directive line"},
			wantCards:    []string{"**skill:atomic-coding**", "**skill:specialist-worker**"},
		},
		{
			name:        "role default stays a card",
			agent:       "worker",
			query:       "do the thing",
			wantCards:   []string{"**skill:specialist-worker**"},
			wantNotBody: []string{"WORKER directive line"},
		},
		{
			name:         "trigger hit outranks default tier",
			agent:        "worker",
			query:        "implement the atomic change",
			wantExpanded: []string{"WORKER directive line"},
			wantCards:    []string{"**skill:specialist-worker**"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := l.PackForAgentTiered(tc.agent, tc.query, 8000)
			for _, want := range tc.wantCards {
				if !strings.Contains(out, want) {
					t.Errorf("missing card %q in:\n%s", want, out)
				}
			}
			for _, want := range tc.wantExpanded {
				if !strings.Contains(out, want) {
					t.Errorf("expected expanded body %q in:\n%s", want, out)
				}
			}
			for _, notWant := range tc.wantNotBody {
				if strings.Contains(out, notWant) {
					t.Errorf("body %q should have stayed a card:\n%s", notWant, out)
				}
			}
		})
	}
}

func TestCardsAreCheap(t *testing.T) {
	dir := seedSkills(t)
	l := NewLoader(dir)
	list, _ := l.List()
	cards := RenderCards(list, 8000)
	full := RenderPack(list, 100000)
	if len(cards) >= len(full)/3 {
		t.Fatalf("cards (%d bytes) should be far cheaper than full bodies (%d bytes)", len(cards), len(full))
	}
	for _, s := range list {
		if !strings.Contains(cards, "**skill:"+s.Name+"**") {
			t.Fatalf("card for %s missing:\n%s", s.Name, cards)
		}
		if strings.Contains(cards, strings.ToUpper(s.Name)+" directive line") {
			t.Fatalf("cards must not include bodies:\n%s", cards)
		}
	}
}

func TestOneFatSkillDoesNotDropTheRest(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteSkill(dir, Skill{
		Name: "aaa-fat", Description: "enormous", Body: fatBody("FAT", 2000), UserInvocable: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"bbb-small", "ccc-small", "ddd-small"} {
		if _, err := WriteSkill(dir, Skill{
			Name: n, Description: "small one", Body: "# " + n + "\n\nshort", UserInvocable: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	l := NewLoader(dir)
	list, _ := l.List()

	// RenderPack (full bodies) must skip the fat one and keep going.
	pack := RenderPack(list, 4000)
	for _, n := range []string{"bbb-small", "ccc-small", "ddd-small"} {
		if !strings.Contains(pack, "skill:"+n) {
			t.Fatalf("RenderPack dropped %s after the oversized skill:\n%s", n, pack)
		}
	}
	if len(pack) > 4000 {
		t.Fatalf("RenderPack over budget: %d", len(pack))
	}

	// The tiered renderer must card every skill regardless.
	tiered := RenderMatches(matchesOf(list), PackOptions{MaxChars: 4000})
	for _, n := range []string{"aaa-fat", "bbb-small", "ccc-small", "ddd-small"} {
		if !strings.Contains(tiered, "**skill:"+n+"**") {
			t.Fatalf("tiered pack dropped card for %s:\n%s", n, tiered)
		}
	}
	if len(tiered) > 4000 {
		t.Fatalf("tiered pack over budget: %d", len(tiered))
	}
}

func matchesOf(list []Skill) []Match {
	out := make([]Match, 0, len(list))
	for _, s := range list {
		out = append(out, Match{Skill: s, Score: SpecialistDefaultScore})
	}
	return out
}

func TestRenderMatchesOptions(t *testing.T) {
	dir := seedSkills(t)
	l := NewLoader(dir)
	list, _ := l.List()
	ms := matchesOf(list)

	tests := []struct {
		name     string
		opts     PackOptions
		wantBody string
		noBody   string
	}{
		{"cards only", PackOptions{MaxChars: 20000, CardsOnly: true}, "", "WORKER directive line"},
		{"expand all", PackOptions{MaxChars: 200000, ExpandAll: true}, "WORKER directive line", ""},
		{"expand named", PackOptions{MaxChars: 200000, Expand: []string{"atomic-coding"}}, "ATOMIC directive line", "MULTIPASS directive line"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := RenderMatches(ms, tc.opts)
			if tc.wantBody != "" && !strings.Contains(out, tc.wantBody) {
				t.Errorf("missing %q:\n%s", tc.wantBody, out)
			}
			if tc.noBody != "" && strings.Contains(out, tc.noBody) {
				t.Errorf("unexpected %q:\n%s", tc.noBody, out)
			}
		})
	}
	if RenderMatches(nil, PackOptions{}) != "" {
		t.Fatal("empty match list should render empty")
	}
}

func TestExpandBody(t *testing.T) {
	dir := seedSkills(t)
	l := NewLoader(dir)
	body, ok := l.ExpandBody("atomic-coding")
	if !ok {
		t.Fatal("expected to find atomic-coding")
	}
	if !strings.Contains(body, "ATOMIC directive line") || !strings.Contains(body, "### skill:atomic-coding") {
		t.Fatalf("body=%s", body)
	}
	if _, ok := l.ExpandBody("nope"); ok {
		t.Fatal("unknown skill should not resolve")
	}
	// Case-insensitive.
	if _, ok := l.ExpandBody("ATOMIC-CODING"); !ok {
		t.Fatal("lookup should be case-insensitive")
	}
}

func TestResolveMatchesScores(t *testing.T) {
	dir := seedSkills(t)
	l := NewLoader(dir)
	ms := l.ResolveMatches("use @skill:atomic-coding here", "worker", nil, 6)
	if len(ms) == 0 {
		t.Fatal("no matches")
	}
	var atomic, worker *Match
	for i := range ms {
		switch ms[i].Skill.Name {
		case "atomic-coding":
			atomic = &ms[i]
		case "specialist-worker":
			worker = &ms[i]
		}
	}
	if atomic == nil || !atomic.Explicit || atomic.Score != ExplicitRefScore {
		t.Fatalf("explicit ref not scored: %+v", atomic)
	}
	if !atomic.ShouldExpand() {
		t.Fatal("explicit refs must expand")
	}
	if worker == nil || worker.Explicit {
		t.Fatalf("worker default should not be explicit: %+v", worker)
	}
	if worker.ShouldExpand() {
		t.Fatalf("specialist default tier (score %d) should stay a card", worker.Score)
	}
}

func TestLoaderCacheAvoidsReparsingAndInvalidatesOnMtime(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteSkill(dir, Skill{Name: "alpha", Description: "v1", Body: "# A", UserInvocable: true})
	if err != nil {
		t.Fatal(err)
	}
	l := NewLoader(dir)

	first, _ := l.List()
	if len(first) != 1 || first[0].Description != "v1" {
		t.Fatalf("first=%v", first)
	}
	// Cached: repeated calls stay consistent.
	for i := 0; i < 30; i++ {
		got, _ := l.List()
		if len(got) != 1 {
			t.Fatalf("iteration %d: %v", i, got)
		}
	}
	l.cache.mu.RLock()
	entry := l.cache.entry
	l.cache.mu.RUnlock()
	if entry == nil {
		t.Fatal("cache never populated")
	}

	// A content change must invalidate.
	newBody := "---\nname: alpha\ndescription: v2\nuser-invocable: true\n---\n\n# A\n\nmore body here\n"
	if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}
	after, _ := l.List()
	if len(after) != 1 || after[0].Description != "v2" {
		t.Fatalf("stale cache: %+v", after)
	}

	// A new skill file must invalidate.
	if _, err := WriteSkill(dir, Skill{Name: "beta", Description: "b", Body: "# B"}); err != nil {
		t.Fatal(err)
	}
	grown, _ := l.List()
	if len(grown) != 2 {
		t.Fatalf("new skill not picked up: %v", grown)
	}

	// Deleting must invalidate.
	if err := os.RemoveAll(filepath.Join(dir, "beta")); err != nil {
		t.Fatal(err)
	}
	shrunk, _ := l.List()
	if len(shrunk) != 1 {
		t.Fatalf("deleted skill still listed: %v", shrunk)
	}

	l.InvalidateCache()
	l.cache.mu.RLock()
	defer l.cache.mu.RUnlock()
	if l.cache.entry != nil {
		t.Fatal("InvalidateCache did not clear")
	}
}

func TestListMutationDoesNotPoisonCache(t *testing.T) {
	dir := seedSkills(t)
	l := NewLoader(dir)
	a, _ := l.List()
	a[0].Name = "poisoned"
	b, _ := l.List()
	if b[0].Name == "poisoned" {
		t.Fatal("List returned a shared backing array")
	}
}

func TestNilLoaderList(t *testing.T) {
	var l *Loader
	got, err := l.List()
	if err != nil || got != nil {
		t.Fatalf("nil loader: %v %v", got, err)
	}
}
