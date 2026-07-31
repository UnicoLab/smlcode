# ✅ Testing

Prove SLMCode works on *your* machine — offline, with oMLX or Ollama, without summoning a cloud bill.

!!! tip "Studio"
    Default UI: [http://127.0.0.1:7420](http://127.0.0.1:7420) — see [Studio](studio.md).

---

## 0. Prerequisites (once)

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
# or from a checkout: make install-system

# Terminal A — local model server
omlx start
# or: ollama serve  (+ configure provider — see Providers)

# Terminal B
slmcode version
slmcode doctor
```

---

## 1. Five-minute smoke test 🔥

```bash
mkdir -p /tmp/slm-demo && cd /tmp/slm-demo
printf 'package main\n\nfunc Hello() string { return "hi" }\n' > hello.go
printf '# Agents\n\nPrefer tiny Go edits and godoc comments.\n' > AGENTS.md
slmcode init

slmcode run -v "Add a Go doc comment to Hello() explaining it returns a greeting. Keep it tiny."

cat hello.go
slmcode board
slmcode session list
slmcode docs list
ls .slmcode/SKILLS.md .slmcode/skills/learned/ 2>/dev/null
```

**Pass:**

- [ ] `hello.go` has `// Hello returns…`
- [ ] Board shows completed work
- [ ] Session saved
- [ ] Skills flywheel touched disk

---

## 2. Studio GUI

```bash
cd /tmp/slm-demo
slmcode studio
```

Checklist:

1. Query → **Run**
2. Pipeline strip advances (not stuck forever at “thinking about thinking”)
3. **Live** tab shows `@agent` + scope + output
4. Drag a kanban card / edit CONTEXT
5. Settings → model list loads

API smoke:

```bash
curl -s http://127.0.0.1:7420/api/health | jq .
curl -s http://127.0.0.1:7420/api/agents | jq 'length'   # expect 14
```

---

## 3. Interactive REPL

```bash
slmcode chat
```

```text
slm › /help
slm › /permission review
slm › Add a unit test for Hello()
slm › /board
slm › /diff
slm › /quit
```

If permission is `review`:

```bash
slmcode apply
slmcode commit -m "slmcode: hello tests"
```

---

## 4. Permissions matrix

| Mode | Behavior | Verify |
|------|----------|--------|
| `auto` | Writes files | `cat` the changed file |
| `dry-run` | Never writes | log shows `dry-run: would…` |
| `review` | Stages patches | `.slmcode/pending/` → `slmcode apply` |

```bash
slmcode config set permission dry-run
slmcode config set permission auto
```

---

## 5. Sessions

```bash
slmcode session list
slmcode session show run-…      # id from list
slmcode session resume run-…
slmcode run "continue remaining work"
```

---

## 6. Automated tests (developers)

```bash
cd /path/to/smlcode
make lint && make test

# Live multi-agent / oMLX (optional, slower)
RUN_E2E=1 make e2e
```

**Pass:** `ok` on packages; live tests report success when enabled.

---

## 7. Feature checklist

| Feature | How to verify |
|---------|----------------|
| Plan → split → parallel | `run -v` stream |
| Coordinator | `[coord]` / `@coordinator` |
| Self-critic | `approved → done` or corrector loop |
| Skip deep explore | 2nd run mentions reusing CONTEXT/MEMORY |
| Auto skills | `.slmcode/SKILLS.md` after run |
| Instructions load | “loaded project instructions” |
| Live CLI | `run -v` or `chat` |
| Live GUI | Studio → Live |
| Diff / commit | `slmcode diff` / `commit -m` |
| Review mode | `permission review` → `apply` |
| 14 agents | `/api/agents` |

---

## 🆘 Troubleshooting

```bash
slmcode doctor
# LLM should show a happy status

SLMCODE_FORCE_EXPLORE=1 slmcode run -v "…"
slmcode config set think_passes 2
slmcode config set retries 2
```

| Symptom | Fix |
|---------|-----|
| Studio port busy | `slmcode studio --listen 127.0.0.1:7421` |
| Model unreachable | Start oMLX/Ollama; check [Providers](providers.md) |
| Wander / wrong files | Lower context KB; raise think passes; tighten `AGENTS.md` |

Made with ♥ by [UnicoLab](https://unicolab.ai)
