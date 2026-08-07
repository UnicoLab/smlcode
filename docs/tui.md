# 🖥️ TUI & chat

Two terminal faces. One harness. Zero mystery meat. 🥩➡️🥗

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">⌨️</span>
<p class="slm-banner__text" markdown>
<strong>Pick your fighter:</strong> Premium TUI for day-to-day board + live events;
Chat REPL for quick prompts and slash-command speedruns.
</p>
</div>

| Face | Best for |
|------|----------|
| **Premium TUI** (`slmcode` / `slmcode tui`) | Day-to-day — board, live events, agents, stats 🕹️ |
| **Chat REPL** (`slmcode chat`) | Quick prompts + slash commands 💬 |

---

## Premium TUI ✨

```bash
slmcode
# or
slmcode tui
```

### What you see 👀

- Connection / provider health
- Live kanban (columns move as waves complete — ASMR optional)
- Agent stream: `@worker`, scope files, truncated outputs
- Errors without the doom-scroll
- Diffs and query history

### Slash commands 🪄

| Command | Effect |
|---------|--------|
| `/compact [heuristic\|llm\|auto]` | Shrink noisy context 🧹 |
| `/models` | Catalog / search enabled models 🔎 |
| `/mcp` | MCP server status 🔌 |
| `/auth` | Show / set provider keys (`.slmcode/auth.json`) 🔑 |
| `/schema` | Dump config JSON schema 📐 |
| `/sessions` | Browse saved runs 📼 |
| `/stats` | Phase latency + token estimates 📊 |
| `/permission` | Write / shell policy 🛡️ |
| `/plan [auto\|ask]` | Plan-approve gate before execute 📋 |
| `/escalate …` | Answer escalate HITL: `re_scope` \| `retry` \| `mark_done` \| `abort` ⚖️ |
| `/agents` | List / create / edit / delete agents 🧩 |
| `/stop` | Checkpoint mid-run 🛑 |
| `/resume` | Continue from checkpoint ▶️ |
| `/help` | Remind your future self 🛟 |

!!! tip "⚖ Escalate banner"
    When a task hits max review retries, the TUI shows an **ESCALATE** banner and the
    pipeline pauses that task. Answer with `/escalate retry` (etc.), or wait for the
    timeout — then **@escalate** (SLM) decides. Same modal exists in Studio.

!!! tip "💪 Keyboard muscle memory"
    **Ctrl+C** mid-run checkpoints board + ReAct history under
    `.slmcode/queries/<id>/react/`. Prefer `/resume` over “start over and pray”.

### Agent CRUD from the TUI 🧩

```bash
/agent new
/agent new id=night-auditor title=Night provider=ollama model=qwen2.5-coder:14b
/agent edit worker model=qwen2.5-coder:14b
/agent show night-auditor
/agent delete night-auditor
```

---

## Chat REPL 💬

```bash
slmcode chat
```

```text
slm › /help
slm › /permission review
slm › Add validation to the login handler
slm › /board
slm › /diff
slm › /quit
```

| Command | Effect |
|---------|--------|
| `/run <q>` | Explicit full pipeline |
| `/board` `/status` `/diff` `/skills` `/doctor` | Inspect |
| `/permission auto\|dry-run\|review` | Write policy |
| `/model <id>` | Switch model (rebuild as needed) |
| `/quit` | Exit (politely) |

Plain lines (no `/`) also run the full pipeline. Yes, even typos. Choose carefully. 😅

---

## Classic one-shot CLI 🛠️

Still the power tool:

```bash
slmcode run -v "…"
slmcode board
slmcode watch
slmcode session list
```

See [⌨️ CLI reference](cli.md) for every subcommand.

---

## When to use which 🎯

| Situation | Reach for |
|-----------|-----------|
| Exploring a new repo | TUI + Studio side-by-side 🥊 |
| Scripted / CI-ish | `slmcode run` + flags |
| “Just fix this” | `chat` or `run -v` |
| Teaching / demos | `permission dry-run` + TUI 🎭 |

<div class="slm-joke" markdown>
<span class="slm-joke__emoji">🍿</span>
<p markdown>
<strong>Pro move:</strong> run <code>slmcode studio</code> in one pane and <code>slmcode watch</code> in another.
Feel like mission control. Resist the urge to narrate aloud. Or don’t — we can’t hear you.
</p>
</div>

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
