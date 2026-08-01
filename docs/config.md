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
compact_mode: true        # quieter TUI/Studio live stream (default)
```

| Mode | Effect |
|------|--------|
| `permission: review` | Stage under `.slmcode/pending/` → `slmcode apply` 👀 |
| `dry_run: true` | Never write code files 🎭 |
| `shell_permission` | Independent of file writes |

---

## QA gate (on by default) ✅

```yaml
clarify_mode: auto        # auto | ask | off  (Claude Code AskUserQuestion style)
clarify_timeout: 2m       # ask mode: wait then apply recommended
scope_judge: true         # post-split PRD completeness gate
plan_approve: auto        # off | auto | ask  (Plan Mode gate before execute)
auto_approve: false       # skip plan/shell/clarify HITL waits
shell_permission: allow   # allow | ask | deny (ask = interactive approve)
context_compact: true     # mid-run CONTEXT.md summarization
wave_snapshots: true      # per-wave rewind under .slmcode/waves/
hooks_enabled: true       # load .slmcode/hooks.json Pre/PostToolUse
mcp_servers: []           # thin read-only MCP (stdio or HTTP)
qa_gate: true
qa_gate_command: ""       # empty = auto-detect (go/pytest/uv/npm/compileall)
qa_gate_max_rounds: 3
post_worker_smoke: true   # py_compile / go test after each worker before review
```

### Planning / scope

Vague queries get an **interviewer** pass (options + recommended defaults).
- `auto` — lock recommended decisions into a PRD (no pause)
- `ask` — emit SSE `kind=ask`, write `.slmcode/clarify/ask.json`, wait for
  Studio modal or `POST /api/clarify/answer` (timeout → recommended)
- `off` — skip interview

`scope_judge` then checks every task has concrete acceptance/files before
execute. `plan_approve: ask` pauses with a Studio modal / `POST /api/plan/approve`.

### Hooks / MCP / rewind

Copy `.slmcode-hooks.example.json` → `.slmcode/hooks.json`. PreToolUse non-zero
exit blocks the tool. PostToolUse can run `compileall` after writes.

`mcp_servers` registers a read-only `mcp_call` tool. Wave snapshots: TUI
`/rewind list` / `/rewind <id>`, API `GET/POST /api/rewind`. Real context
compact: `/compact context` or `POST /api/compact`.

### QA / smoke

After workers, `post_worker_smoke` runs a fast deterministic check (`python -m
py_compile` / `go test -short`) and blocks approve-on-disk-only when it fails.

After the finalize tester, `qa_gate` runs a real project command (and bootstraps
deps when needed: `pip install -r requirements.txt`, `uv sync`, `go mod tidy`).
Auto-detect covers `go test`, `python -m pytest` / `uv run pytest`,
`python -m compileall`, and `npm test` / `cargo test` / `make test` — never
`--help` as a quality proof. On failure, tester diagnoses → corrector patches →
re-run.

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
