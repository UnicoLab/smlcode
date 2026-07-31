# 🖥️ TUI & chat

Two terminal faces. One harness.

| Face | Best for |
|------|----------|
| **Premium TUI** (`slmcode` / `slmcode tui`) | Day-to-day — board, live events, agents, stats |
| **Chat REPL** (`slmcode chat`) | Quick prompts + slash commands |

---

## Premium TUI

```bash
slmcode
# or
slmcode tui
```

### What you see

- Connection / provider health  
- Live kanban (columns move as waves complete)  
- Agent stream: `@worker`, scope files, truncated outputs  
- Errors without the doom-scroll  
- Diffs and query history  

### Slash commands

| Command | Effect |
|---------|--------|
| `/compact` | Shrink noisy context |
| `/sessions` | Browse saved runs |
| `/stats` | Phase latency + token estimates |
| `/permission` | Write / shell policy |
| `/agents` | List / create / edit / delete agents |
| `/stop` | Checkpoint mid-run |
| `/resume` | Continue from checkpoint |
| `/help` | Remind your future self |

!!! tip "Keyboard muscle memory"
    **Ctrl+C** mid-run checkpoints board + ReAct history under
    `.slmcode/queries/<id>/react/`. Prefer `/resume` over “start over and pray”.

### Agent CRUD from the TUI

```bash
/agent new
/agent new id=night-auditor title=Night provider=ollama model=qwen2.5-coder:14b
/agent edit worker model=qwen2.5-coder:14b
/agent show night-auditor
/agent delete night-auditor
```

---

## Chat REPL

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
| `/quit` | Exit |

Plain lines (no `/`) also run the full pipeline.

---

## Classic one-shot CLI

Still the power tool:

```bash
slmcode run -v "…"
slmcode board
slmcode watch
slmcode session list
```

See [CLI reference](cli.md) for every subcommand.

---

## When to use which

| Situation | Reach for |
|-----------|-----------|
| Exploring a new repo | TUI + Studio side-by-side |
| Scripted / CI-ish | `slmcode run` + flags |
| “Just fix this” | `chat` or `run -v` |
| Teaching / demos | `permission dry-run` + TUI |
