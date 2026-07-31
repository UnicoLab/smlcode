# ⚙️ Config reference

Primary file: **`.slmcode/config.yaml`** (created by `slmcode init`).
Knobs. Dials. The cockpit without the fake airplane noises. ✈️

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🎛️</span>
<p class="slm-banner__text" markdown>
<strong>Override layers (roughly):</strong> CLI flags → env → project config → built-in defaults.
When two knobs disagree, the louder one closer to the command usually wins.
</p>
</div>

```bash
slmcode config
slmcode config set <key> <value>
```

---

## Provider & model 🔌

```yaml
provider: omlx          # or ollama, openai, lmstudio, openrouter, …
endpoint: http://127.0.0.1:8000/v1
model: Qwen3-Coder-30B-A3B-Instruct-MLX-4bit
api_key: ""             # prefer env vars
```

| Key | Notes |
|-----|-------|
| `provider` | Unknown names → OpenAI-compatible ✨ |
| `endpoint` | Auto-defaults per preset if empty |
| `model` | Whatever your gateway serves |
| `api_key` | Avoid committing; use env 🔑 |

Env: `SLMCODE_PROVIDER`, `SLMCODE_MODEL`, `SLMCODE_ENDPOINT`, `SLMCODE_API_KEY`, `OPENAI_API_KEY`, `OPENAI_BASE_URL`, …

---

## Execution shape 🏭

```yaml
backend: slmcode        # harness engine
mode: full              # full | specialist
specialist: worker      # when mode=specialist
pinned_skills:
  - atomic-coding
```

---

## Quality & throughput 📊

```yaml
temperature: 0.2
max_tokens: 4096
max_retries: 4
max_parallel: 2
max_context_kb: 32
think_passes: 1
task_timeout: 12m
```

| Key | SLM tip |
|-----|---------|
| `think_passes` | Try `2` on 7–14B 🐣 |
| `max_context_kb` | Lower if models wander 🥴 |
| `max_parallel` | `1` on slow local GPUs 🐢 |
| `max_retries` | Critic stubbornness 💪 |

---

## Safety 🛡️

```yaml
dry_run: false
permission: auto          # auto | dry-run | review
shell_permission: ask     # allow | ask | deny
auto_approve: false
verbose: false
compact_mode: false
```

| Mode | Effect |
|------|--------|
| `permission: review` | Stage under `.slmcode/pending/` → `slmcode apply` 👀 |
| `dry_run: true` | Never write code files 🎭 |
| `shell_permission` | Independent of file writes |

---

## QA gate (optional) ✅

```yaml
qa_gate: false
qa_gate_command: ""       # empty = auto-detect
qa_gate_max_rounds: 3
```

When enabled, runs a project check command between correction rounds.
Like a bouncer for bad patches.

---

## Embeddings (memory ranking) 🧲

```yaml
embedding_enabled: true
embedding_endpoint: ""    # defaults to chat endpoint
embedding_model: ""
embedding_api_key: ""
embedding_top_k: 8
```

Fallback order: provider embeddings → pure-Go local hashing → lexical TF-IDF.
`slmcode doctor` reports which mode is active.

---

## Pricing display (optional) 💸

```yaml
price_preset: ""          # off | local | omlx | openai | anthropic | openrouter | auto
price_prompt_per_mtok: 0
price_completion_per_mtok: 0
```

TUI `/stats` shows tokens; dollars only if you configure rates (no fake `$`). Honesty > theater.

---

## Studio & skills paths 🎨

```yaml
listen: 127.0.0.1:7420
skills_dirs: []           # extra skill roots
claude_code_bin: claude   # only if you use that backend
```

---

## Example: Ollama project 🦙

```yaml
provider: ollama
endpoint: http://127.0.0.1:11434
model: qwen2.5-coder:14b
think_passes: 2
max_context_kb: 16
max_parallel: 1
permission: review
pinned_skills:
  - atomic-coding
```

---

## Related 🔗

- [🔌 Providers](providers.md)
- [⌨️ CLI](cli.md)
- [❓ FAQ](faq.md)

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
