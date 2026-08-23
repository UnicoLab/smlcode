# ⚙️ Config reference

Configuration lives in `.slmcode/config.yaml`. Every key below exists in `config.Config`.

```bash
slmcode config show              # effective config
slmcode config show --origin     # …and which layer supplied each value
slmcode config show --json
slmcode config get max_parallel
slmcode config set max_parallel 6
slmcode config unset fast_model
slmcode config set --user model qwen2.5-coder:14b   # write the user-level layer
slmcode config schema            # machine-readable field schema
slmcode config path
```

## Layering

Lowest precedence first:

1. built-in defaults (`config.Default`)
2. user file — most specific first: `$SLMCODE_USER_CONFIG`,
   `$XDG_CONFIG_HOME/slmcode/config.yaml`, `~/.slmcode/config.yaml`,
   `~/.config/slmcode/config.yaml`. Write to it with `slmcode config set --user <key> <value>`.
3. project file (`.slmcode/config.yaml`)
4. `SLMCODE_*` environment — every key has one, mechanically (`SLMCODE_MAX_PARALLEL`,
   `SLMCODE_QA_BOOTSTRAP`, …); `slmcode config schema` lists them
5. command-line flags

The layer is discovered by `pkg/config`, so it applies to Studio, the TUI and any embedder, not
just the CLI.

A saved `config.yaml` records **intent**: only the keys that differ from what the project would
otherwise inherit, plus a `config_version` stamp. Three consequences: `config show --origin` can
tell a deliberate choice from an inherited default, a new release's improved default reaches
existing projects, and no absolute path is embedded in a file that may be committed. `root` is
never persisted for exactly that last reason. Older files are migrated forward on load, and
`config show` reports when that happened.

---

## Provider & model

| Key | Default | Meaning |
|---|---|---|
| `provider` | `omlx` | `omlx` `ollama` `openai` `lmstudio` `openrouter` `vllm` `litellm` `together` `groq` `deepseek` `mistral` `google` `fireworks` `anthropic` … Any other name is treated as an OpenAI-compatible gateway. |
| `endpoint` | `http://127.0.0.1:8000/v1` | Base URL. Provider presets supply a default. |
| `model` | `Qwen3-Coder-30B-A3B-Instruct-MLX-4bit` | Any id your provider serves |
| `api_key` | — | Prefer `.slmcode/auth.json` or env |
| `fast_model` | — | Smaller/faster model for light agents (reviewer, coordinator, splitter, planner, context, architect, clarifier). Empty = use `model` everywhere. |
| `backend` | `slmcode` | `slmcode` or `claude-code` |
| `claude_code_bin` | `claude` | Binary for the `claude-code` backend |
| `enabled_models` | — | Scope the selectable catalog (empty = all) |
| `active_stack` | — | Last applied stack id |

## Run shape

| Key | Default | Meaning |
|---|---|---|
| `mode` | `full` | `full` (pipeline) or `specialist` (single role) |
| `specialist` | — | Role id when `mode: specialist` |
| `dynamic_pipeline` | `true` | Run the composer to assemble a task-specific pipeline first |
| `pinned_skills` | — | Always loaded, in addition to `@skill:` refs and matching |
| `max_parallel` | `4` | Concurrent tasks per wave |
| `max_retries` | `4` | Review/correct retries before escalate |
| `think_passes` | `1` | 2+ enables speculative digs |
| `task_timeout` | `12m` | Per-task timeout |
| `temperature` | `0.2` | Default sampling temperature (roles override) |
| `max_tokens` | `4096` | Default completion cap (roles override) |
| `dry_run` | `false` | Do not write code files |
| `verbose` | `false` | |
| `active_pack`, `active_pipeline` | — | Last applied block ids |

## Context & memory budget

See [Context engineering](context.md) for what these actually do.

| Key | Default | Meaning |
|---|---|---|
| `model_profiles` | built-in | Per-model-family `context_limit`, `max_tokens`, `max_turns`, `temperature`, `thinking_budget_tokens`, `skill_token_budget`, `knowledge_token_budget` |
| `max_context_kb` | `16` | **Legacy** byte budget, used only when no model profile supplies a real context window |
| `context_reserve_system_tokens` | `500` | Subtracted from the window before the pack gets its share |
| `context_reserve_tool_tokens` | `900` | ” |
| `context_reserve_response_tokens` | `2048` | ” |
| `context_slack_percent` | `10` | Tokenizer disagreement + chat scaffolding |
| `context_role_budget` | built-in | Per-role share of the available window, e.g. `{worker: 100, reviewer: 85}` |
| `repo_map_tokens` | `900` | Ranked repo-symbol map's share of a pack |
| `excerpt_window_lines` | `25` | ± lines around each relevance match |
| `memory_tokens` | `300` | Budget for the injected memory block |
| `skill_disclosure` | `auto` | `auto` (cards + earned bodies) · `cards` · `full` |
| `skill_max_expanded` | `2` | How many skill bodies may be inlined at once |
| `skills_dirs` | — | Extra skill search roots |

## Compaction

| Key | Default | Meaning |
|---|---|---|
| `compact_mode` | `true` | Compact the run's markdown memory |
| `context_compact` | `true` | Document compaction (gated — see [Context](context.md#7-compaction-pkgcompact)) |
| `context_compact_engine` | `heuristic` | Compaction engine |
| `react_compact` | `true` | Mid-run conversation compaction, tool-pair safe |
| `react_compact_at_percent` | `80` | Trigger point, with a 5-point hysteresis band |

## Constrained decoding & tools

| Key | Default | Meaning |
|---|---|---|
| `structured_decoding` | `auto` | `auto` negotiates the strongest confirmed mechanism; `off` forces prompt-only JSON |
| `read_window_lines` | `0` → 120 | `ws_read` window |
| `max_tool_chars` | `0` → 8000 | Hard cap on every tool result |
| `shell_timeout` | `0` → 2m | `ws_shell` per-command timeout (per-call override ceiling: 15m) |
| `read_head_lines` | `80` | Auto-trim read head |
| `auto_text_tools` | — | Enable text-manipulation helpers |
| `llm_retry_count` | `3` | Provider HTTP retries (≠ `max_retries`, which is the board loop) |
| `llm_retry_delay_ms` | `1000` | ” |

## Quality gates

| Key | Default | Meaning |
|---|---|---|
| `qa_gate` | `true` | Iterate a test command until green after the board finishes |
| `qa_gate_command` | — | Empty = auto-detect from the quality block |
| `qa_gate_max_rounds` | `3` | Rounds before escalate |
| `post_worker_smoke` | `true` | Deterministic `py_compile` / `go test` after each worker, **before** review can approve — prevents broken-on-disk auto-approve |
| `qa_bootstrap` | `ask` | May the QA gate run dependency installers (`pip install`, `npm install`, `go mod tidy`) against agent-authored manifests? `off` · `ask` · `auto`. `ask` is the default because an agent that invented a `requirements.txt` should not get an unattended network install. |
| `regression_checks` | `true` | Replay stored regression checks around the QA gate |
| `disable_syntax_check` | `false` | Turn off post-edit syntax verification |
| `scope_judge` | `true` | Post-split PRD completeness check |
| `placeholder_pass` | `true` | Post-execute stub scan + fill/flag specialist |
| `auto_refine` | `false` | Auto-refinement loop |
| `auto_refine_max_rounds` | `2` | |

## Guardrails

| Key | Default | Guards against |
|---|---|---|
| `permission` | `auto` | `auto` \| `dry-run` \| `review` — file write policy |
| `shell_permission` | `allow` | `allow` \| `ask` \| `deny` |
| `shell_whitelist` | `true` | Non-allowlisted shell commands |
| `shell_allow` | — | Extra allowlist prefixes (merged with `SLMCODE_BASH_ALLOW`) |
| `shell_ask_timeout` | `2m` | |
| `write_guard` | `true` | Writes outside focus files |
| `read_before_edit` | `true` | Editing a file not read this session |
| `shell_write_guard` | `true` | `cat >file` / `tee` clobber |
| `over_edit_guard` | `true` | Whole-file rewrites through `ws_edit` |
| `claims_gate` | `true` | Hallucinated `files_changed` |
| `static_quality` | `true` | Stub / placeholder code |
| `require_smoke` | `true` | Coding tasks approved without a smoke check |
| `quality_monitor` | `true` | Empty output, tool loops, hallucinated tools |
| `finalize_warn` | `true` | Silent `MaxIter` exhaustion |
| `worker_critique` | `true` | Weak worker output (auto self-fix pass) |
| `thinking_budget` | `true` | Endless deliberation (commit-to-implementation nudge) |
| `thinking_budget_tokens` | `4096` | |
| `tool_guidance` | `true` | Per-turn tool skill cards |
| `knowledge_inject` | `true` | Keyword knowledge cards |
| `hooks_enabled` | `true` | Loads `.slmcode/hooks.json` |
| `file_checkpoints` | `true` | Per-file snapshots before each write |
| `wave_snapshots` | `true` | Per-wave snapshots |
| `max_task_calls` | `6` | Per-task LLM call budget in the inner loop |

Details → [Permissions & safety](permissions.md).

## Human-in-the-loop

| Key | Default | Meaning |
|---|---|---|
| `clarify_mode` | `ask` | `off` \| `auto` \| `ask` |
| `clarify_timeout` | `2m` | |
| `plan_approve` | `ask` | `off` \| `auto` \| `ask` |
| `plan_approve_timeout` | `2m` | |
| `plan_approve_on_timeout` | `auto` | `approve` \| `reject` \| `auto` — `auto` approves only when no event subscriber was attached |
| `continue_ask` | `ask` | Another wave, or stop, when retries are exhausted |
| `continue_ask_timeout` | `2m` | |
| `escalate_ask` | `ask` | Retry / re-scope / abort at max retries |
| `escalate_ask_timeout` | `5m` | |
| `escalate_timeout_agent` | — | Empty = auto-pick `@escalate` |
| `auto_approve` | `false` | `true` bypasses every gate |

With a human attached, gates block rather than expiring. Headless resolution is controlled by
`--on-gate-timeout` (default `stop`).

## Self-improvement

| Key | Default | Meaning |
|---|---|---|
| `evolve` | `true` | Memory, repair rules, bandit policy, regression checks |
| `deterministic` | `false` | Greedy policy, no exploration — for CI and reproducible runs. `dry_run` implies it. |
| `architect_editor` | `false` | The `describer` → `editor` role pair. Off by default: it doubles the LLM calls per task and only pays off when the two halves point at different models. |

Details → [Self-improvement & memory](self-improvement.md).

## Retrieval & embeddings

| Key | Default | Meaning |
|---|---|---|
| `embedding_enabled` | `false` | |
| `embedding_endpoint` | — | OpenAI-compatible `/v1/embeddings` |
| `embedding_model` | — | |
| `embedding_api_key` | — | |
| `embedding_top_k` | `5` | |
| `retrieval_min_score` | `0` | Overrides the calibrated similarity floor |
| `retrieval_cache_dir` | — | Embedding cache location |

## Cost tracking

| Key | Meaning |
|---|---|
| `price_preset` | Named pricing preset |
| `price_prompt_per_mtok` | $ per million prompt tokens |
| `price_completion_per_mtok` | $ per million completion tokens |

## Studio & integrations

| Key | Default | Meaning |
|---|---|---|
| `listen` | `127.0.0.1:7420` | Studio listen address |
| `session_event_log` | `true` | Persist per-run event logs under `.slmcode/queries/` |
| `mcp_servers` | — | MCP servers: `{name, command, args, env, url, read_only}` |

---

## A worked local-SLM config

```yaml
# .slmcode/config.yaml
provider: ollama
endpoint: http://127.0.0.1:11434
model: qwen2.5-coder:14b
fast_model: qwen2.5-coder:7b

model_profiles:
  qwen2.5-coder:
    context_limit: 32768     # the number that actually decides the pack budget
    max_tokens: 4096
    max_turns: 24

max_parallel: 2              # a local server serialises inference anyway
think_passes: 1
task_timeout: 20m

structured_decoding: auto
skill_disclosure: auto
repo_map_tokens: 900

permission: review           # nothing lands without you
shell_permission: ask
shell_allow:
  - "make "

plan_approve: ask
evolve: true
```

`slmcode readiness --fix` will suggest and apply most of the safe local-model defaults for you.

!!! note "Structured fields"
    `model_profiles`, `mcp_servers` and `context_role_budget` are structured values —
    `slmcode config set` takes a whole YAML/JSON document for them, not a dotted path. For
    anything non-trivial, edit `.slmcode/config.yaml` directly and check the result with
    `slmcode config show --origin`.
