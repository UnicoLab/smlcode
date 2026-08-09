# 🧪 Recipes

Copy-paste workflows that survive contact with reality.
Less theory. More “paste this and win”. 🏆

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🍳</span>
<p class="slm-banner__text" markdown>
<strong>Chef’s note:</strong> if a recipe asks for a tiny change, keep it tiny.
The turkey rule applies in the kitchen too. 🦃
</p>
</div>

---

## Recipe: first green run 🟢

```bash
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
omlx start   # or configure Ollama — see Providers

mkdir -p /tmp/slm-demo && cd /tmp/slm-demo
printf 'package main\n\nfunc Hello() string { return "hi" }\n' > hello.go
slmcode init
slmcode run -v "Add a short Go doc comment to Hello(). Tiny change only."
cat hello.go
```

---

## Recipe: safe edits on a real repo 🛡️

```bash
cd ~/code/my-app
slmcode init
slmcode blocks apply python  # apply Python pipeline + quality pack
slmcode config set permission review
slmcode run -v "Add input validation to the login handler"
slmcode apply          # when the staged patches look sane
slmcode diff
slmcode commit -m "slmcode: validate login input"
```

---

## Recipe: “where is X?” without writing code 🔎

```bash
slmcode run --agent explorer --dry-run "Where is authentication handled?"
# or
slmcode run --agent docs "Summarize the public HTTP API surface"
```

Maps without mayhem. Excellent for onboarding days and panic Mondays.

---

## Recipe: slow SLM survival kit 🐢➡️🐇

```bash
slmcode config set think_passes 2
slmcode config set retries 2
slmcode config set max_context_kb 16
slmcode config set parallel 1
slmcode run -v "…"
```

If it wanders: tighten `AGENTS.md`, pin `atomic-coding`, force explore once.

```bash
SLMCODE_FORCE_EXPLORE=1 slmcode run -v "…"
```

---

## Recipe: language pack switch 🧱

Quickly switch your project's entire pipeline, agents, and quality checks to match the language:

```bash
# Switch a Go project to use Go pipeline + go-worker + go-tester + QA gate
slmcode blocks apply go

# Switch a Python project
slmcode blocks apply python

# Browse available packs
slmcode blocks list

# Validate custom blocks
slmcode blocks validate
```

Language packs set QA gates, pin skills, and override worker/tester agents automatically.
The PipelineEditor in Studio also has a one-click preset selector.

---

## Recipe: hybrid brains 🧬

Local worker, sharper reviewer — budget diplomacy at its finest.

```bash
slmcode tui
# then:
/agent edit worker provider=ollama model=qwen2.5-coder:14b endpoint=http://127.0.0.1:11434
/agent edit reviewer provider=openai model=gpt-4o-mini endpoint=https://api.openai.com/v1
```

Or edit YAML under `.slmcode/agents/`.

---

## Recipe: resume after lunch (or panic Ctrl+C) 🥪

```bash
# during a run: Ctrl+C or /stop
slmcode session list
slmcode session resume run-…
# TUI: /resume
```

Your checkpoint remembers. Your sandwich does not. Prioritize accordingly.

---

## Recipe: OpenRouter weekend 🌈

```bash
export SLMCODE_PROVIDER=openrouter
export SLMCODE_ENDPOINT=https://openrouter.ai/api/v1
export SLMCODE_API_KEY=sk-or-…
export SLMCODE_MODEL=anthropic/claude-3.5-sonnet
slmcode doctor
slmcode run -v "Refactor the retry helper and add tests"
```

---

## Recipe: Studio + TUI dual wield 🥊

```bash
# terminal A
slmcode studio

# terminal B
cd the-same-project
slmcode watch
```

Edit CONTEXT in Studio; watch the board in the terminal. Feel powerful. Use responsibly.

---

## Recipe: teach house style once 🏠

```markdown
# AGENTS.md

- Prefer table-driven tests
- No drive-by refactors
- Match existing naming; don't invent parallel worlds
```

```bash
slmcode skills new house-style --agents worker,reviewer,corrector
slmcode run --skill house-style "…"
```

<div class="slm-joke" markdown>
<span class="slm-joke__emoji">📜</span>
<p markdown>
<strong>Bonus tip:</strong> vague skills make vague agents.
“Be careful” is not a skill. “Never rewrite unrelated files” is a skill.
</p>
</div>

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
