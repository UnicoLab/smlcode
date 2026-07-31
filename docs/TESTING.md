# SLMCode — Testing Guide (local / offline)

**Version:** 0.5.0
**Binary:** `/opt/homebrew/bin/slmcode` (or `~/.local/bin/slmcode`)
**Studio:** http://127.0.0.1:7420

No cloud required. Uses local oMLX.

Docs index: [README.md](README.md)

---

## 0. Prerequisites (once)

```bash
# Terminal A
omlx start

# Terminal B
cd ~/Desktop/PROJECT/slmcode
make install-system   # system-wide on PATH (like Claude Code)
# or: make install    # ~/.local/bin only
slmcode version          # expect 0.5.0 + binary/source paths
slmcode doctor
# later, after code changes from anywhere:
slmcode update --check && slmcode update
```

API key comes from `~/.omlx/settings.json` (`auth.api_key`).

---

## 1. Five-minute smoke test

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

**Pass:** `hello.go` has `// Hello returns…`, board done, session saved, skills evolved.

---

## 2. Studio GUI

```bash
cd /tmp/slm-demo
slmcode studio
# open http://127.0.0.1:7420
```

Checklist:

1. Query → **Run**
2. Pipeline strip advances
3. **Live** tab shows `@agent` + scope + output
4. Drag a kanban card / edit CONTEXT
5. Settings → model list loads from oMLX

API smoke:

```bash
curl -s http://127.0.0.1:7420/api/health | jq .
curl -s http://127.0.0.1:7420/api/agents | jq 'length'   # 14
```

---

## 3. Interactive REPL

```bash
slmcode chat
```

```
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
| `auto` | Writes files | `cat` changed file |
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

## 6. Automated tests

```bash
cd ~/Desktop/PROJECT/slmcode
go test ./... -count=1

# Live oMLX (≈1–2 min)
RUN_E2E=1 go test ./test/e2e/ -run 'TestLiveOMLX|TestStudioAPI' -timeout 45m -v
```

**Pass:** `ok` on all packages; live test `success=true`.

---

## 7. Feature checklist

| Feature | How to verify |
|---------|----------------|
| Plan → split → parallel | `run -v` stream |
| Coordinator | `[coord]` / `@coordinator` |
| Self-critic | `approved → done` or corrector |
| Skip deep explore | 2nd run: “reusing CONTEXT/MEMORY” |
| Auto skills | `.slmcode/SKILLS.md` after run |
| AGENTS.md load | “loaded project instructions” |
| Live CLI | `run -v` or `chat` |
| Live GUI | Studio → Live |
| Diff / commit | `slmcode diff` / `commit -m` |
| Review mode | `permission review` → `apply` |
| 14 agents | `/api/agents` |

---

## Troubleshooting

```bash
slmcode doctor
# LLM must show status=200

SLMCODE_FORCE_EXPLORE=1 slmcode run -v "…"
slmcode config set think_passes 2
slmcode config set retries 2
slmcode config set model Qwen3-Coder-30B-A3B-Instruct-MLX-4bit
```

If Studio port busy: `slmcode studio --listen 127.0.0.1:7421`.
