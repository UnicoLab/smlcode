# 🧪 Recipes

Copy-paste workflows that survive contact with reality.

---

## Recipe: first green run (playground)

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

## Recipe: safe edits on a real repo

```bash
cd ~/code/my-app
slmcode init
slmcode config set permission review
slmcode run -v "Add input validation to the login handler"
slmcode apply          # when the staged patches look sane
slmcode diff
slmcode commit -m "slmcode: validate login input"
```

---

## Recipe: “where is X?” without writing code

```bash
slmcode run --agent explorer --dry-run "Where is authentication handled?"
# or
slmcode run --agent docs "Summarize the public HTTP API surface"
```

---

## Recipe: slow SLM survival kit

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

## Recipe: hybrid brains (local worker, sharper reviewer)

```bash
slmcode tui
# then:
/agent edit worker provider=ollama model=qwen2.5-coder:14b endpoint=http://127.0.0.1:11434
/agent edit reviewer provider=openai model=gpt-4o-mini endpoint=https://api.openai.com/v1
```

Or edit YAML under `.slmcode/agents/`.

---

## Recipe: resume after lunch (or panic Ctrl+C)

```bash
# during a run: Ctrl+C or /stop
slmcode session list
slmcode session resume run-…
# TUI: /resume
```

---

## Recipe: OpenRouter weekend

```bash
export SLMCODE_PROVIDER=openrouter
export SLMCODE_ENDPOINT=https://openrouter.ai/api/v1
export SLMCODE_API_KEY=sk-or-…
export SLMCODE_MODEL=anthropic/claude-3.5-sonnet
slmcode doctor
slmcode run -v "Refactor the retry helper and add tests"
```

---

## Recipe: Studio + TUI dual wield

```bash
# terminal A
slmcode studio

# terminal B
cd the-same-project
slmcode watch
```

Edit CONTEXT in Studio; watch the board in the terminal. Feel powerful. Use responsibly.

---

## Recipe: teach house style once

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
