package autoconfig

import "testing"

// The rule that carries nearly all the value: an embedding model will never
// write Go however large it is, and picking one produces a failure that is
// baffling rather than obvious — the harness runs, the model answers, and
// nothing it says is JSON.
func TestModelsThatCannotWriteCodeAreRuledOut(t *testing.T) {
	for _, name := range []string{
		"text-embedding-3-large",
		"nomic-embed-text",
		"bge-m3",
		"jina-reranker-v2",
		"whisper-large-v3",
		"kokoro-tts",
		"Qwen2.5-VL-7B-Instruct",
		"llama-guard-3-8b",
		"stable-diffusion-xl",
	} {
		got := score(name)
		if got.Usable {
			t.Errorf("%s was offered as a coding model (%s)", name, got.Why)
		}
		if _, ok := Best([]string{name}); ok {
			t.Errorf("Best picked %s, which cannot write code", name)
		}
	}
}

// The bug a naive substring match ships with: "codestral" contains "tts",
// "instruct" contains "stt", "sdxl" is not "sd". Matching whole segments is
// what stops the disqualifier list eating the models it exists to protect.
func TestARealCodingModelIsNotRuledOutByASubstring(t *testing.T) {
	for _, name := range []string{
		"codestral-22b-v0.1",
		"Qwen2.5-Coder-32B-Instruct",
		"deepseek-coder-v2-lite-instruct",
		"granite-code-20b-instruct",
	} {
		got := score(name)
		if !got.Usable {
			t.Errorf("%s was ruled out: %s", name, got.Why)
		}
	}
}

func TestACoderModelBeatsAGeneralOne(t *testing.T) {
	best, ok := Best([]string{
		"Llama-3.3-70B-Instruct",
		"Qwen2.5-Coder-32B-Instruct",
		"Mistral-7B-Instruct-v0.3",
	})
	if !ok || best.Name != "Qwen2.5-Coder-32B-Instruct" {
		t.Errorf("Best = %+v, want the coder-tuned model", best)
	}
	if best.Why == "" {
		t.Error("a pick with no reasoning cannot be argued with")
	}
}

// A base model completes text; it never answers a JSON contract.
func TestAnInstructModelBeatsABaseModelOfTheSameSize(t *testing.T) {
	best, ok := Best([]string{"Qwen2.5-14B-base", "Qwen2.5-14B-Instruct"})
	if !ok || best.Name != "Qwen2.5-14B-Instruct" {
		t.Errorf("Best = %+v, want the instruction-tuned model", best)
	}
}

func TestBiggerWinsAmongEquals(t *testing.T) {
	best, ok := Best([]string{
		"Qwen2.5-Coder-1.5B-Instruct",
		"Qwen2.5-Coder-32B-Instruct",
		"Qwen2.5-Coder-7B-Instruct",
	})
	if !ok || best.Name != "Qwen2.5-Coder-32B-Instruct" {
		t.Errorf("Best = %+v, want the largest coder", best)
	}
}

// A mixture-of-experts name carries two numbers and the ACTIVE one is not the
// model's size. Reading "30B-A3B" as a 3B model would rank a capable local
// model below a 7B dense one.
func TestMoEActiveParametersAreNotMistakenForTheTotal(t *testing.T) {
	got, ok := paramsB("Qwen3-Coder-30B-A3B-Instruct-MLX-4bit")
	if !ok || got != 30 {
		t.Errorf("paramsB = %v/%v, want 30", got, ok)
	}
	active, ok := activeB("Qwen3-Coder-30B-A3B-Instruct-MLX-4bit")
	if !ok || active != 3 {
		t.Errorf("activeB = %v/%v, want 3", active, ok)
	}
	best, _ := Best([]string{"Mistral-7B-Instruct", "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"})
	if best.Name != "Qwen3-Coder-30B-A3B-Instruct-MLX-4bit" {
		t.Errorf("Best = %q, want the 30B MoE coder", best.Name)
	}
	// And it says so, because "why this one" is the question a user has.
	if got := score("Qwen3-Coder-30B-A3B-Instruct-MLX-4bit"); got.Why == "" ||
		!contains(got.Why, "30B") || !contains(got.Why, "3B active") {
		t.Errorf("Why = %q, want both counts", got.Why)
	}
}

// Under 3B a model rarely holds this harness's contracts, but "worse" is not
// "unusable" — with nothing else on the server it is still the answer.
func TestATinyModelIsAPoorChoiceRatherThanNoChoice(t *testing.T) {
	best, ok := Best([]string{"Qwen2.5-0.5B-Instruct"})
	if !ok || best.Name != "Qwen2.5-0.5B-Instruct" {
		t.Errorf("Best = %+v, want the only model on the server", best)
	}
	bigger, _ := Best([]string{"Qwen2.5-0.5B-Instruct", "Qwen2.5-7B-Instruct"})
	if bigger.Name != "Qwen2.5-7B-Instruct" {
		t.Errorf("Best = %q, want the larger model", bigger.Name)
	}
}

// Re-running against an unchanged server must produce the same answer, or a
// user is told their configuration drifted when nothing did.
func TestEqualCandidatesKeepTheServersOrder(t *testing.T) {
	names := []string{"alpha-7b-instruct", "beta-7b-instruct", "gamma-7b-instruct"}
	for i := 0; i < 5; i++ {
		best, ok := Best(names)
		if !ok || best.Name != "alpha-7b-instruct" {
			t.Fatalf("Best = %+v, want a stable first pick", best)
		}
	}
}

func TestAnEmptyServerHasNoAnswer(t *testing.T) {
	if _, ok := Best(nil); ok {
		t.Error("an empty model list cannot yield a pick")
	}
	if _, ok := Best([]string{"nomic-embed-text"}); ok {
		t.Error("a server with only embedding models cannot yield a pick")
	}
	if got := Rank(nil); len(got) != 0 {
		t.Errorf("Rank(nil) = %v", got)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (hay == needle ||
		len(hay) > 0 && (indexOf(hay, needle) >= 0))
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
