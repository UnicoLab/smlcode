# ✅ Testing

Prove it on your machine — offline if you want. Green checks taste better than vibes. 🥒

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🔬</span>
<p class="slm-banner__text" markdown>
<strong>Definition of done:</strong> a tiny file changed for real, the board shows completed work,
and you can explain what happened without inventing lore.
</p>
</div>

---

## Prerequisites 🧰

```bash
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
omlx start   # or ollama + provider config
slmcode doctor
```

---

## Five-minute smoke 🔥

```bash
mkdir -p /tmp/slm-demo && cd /tmp/slm-demo
printf 'package main\n\nfunc Hello() string { return "hi" }\n' > hello.go
printf '# Agents\n\nPrefer tiny Go edits and godoc comments.\n' > AGENTS.md
slmcode init
# Apply the Go language pack: tuned pipeline, go-worker/go-tester agents,
# and quality gate (go vet + go test). Always run this after init so the
# pipeline is configured for your language before the first run.
slmcode blocks apply go
slmcode run -v "Add a Go doc comment to Hello() explaining it returns a greeting. Keep it tiny."
cat hello.go && slmcode board && slmcode session list
```

**Pass:** doc comment present · board done · session saved · skills touched. 🎉

---

## Studio / API 🎨

```bash
slmcode studio
curl -s http://127.0.0.1:7420/api/health | jq .
curl -s http://127.0.0.1:7420/api/agents | jq 'length'   # 14
```

Checklist: Run → pipeline moves → Live shows `@agent` → drag a card → Settings loads models.

---

## Chat + permissions 🛡️

```bash
slmcode chat
# /permission review → run a prompt → /diff → /quit
slmcode apply
```

| Mode | Verify |
|------|--------|
| `auto` | file changed ✍️ |
| `dry-run` | log only 🎭 |
| `review` | `.slmcode/pending/` 👀 |

---

## Automated (devs) 🤖

```bash
make lint && make test && make docs-build
make e2e                 # offline e2e + prime CLI/API smoke
RUN_E2E=1 make e2e       # also live oMLX / multi-agent
./scripts/e2e_prime_smoke.sh   # stacks/agents/models/auth/mcp alone
```

Offline prime-port coverage: `TestPrimePortsEndToEnd` (stacks apply, auth.json,
find_models allowlist, compact, events, Studio APIs).

---

## Feature matrix 🧪

| Feature | Verify |
|---------|--------|
| Plan/split/parallel | `run -v` |
| Spec clarifier | vague query → assumptions in CONTEXT/PLAN |
| Coordinator | `@coordinator` |
| Self-critic | approve / corrector |
| Real tester | `commands[]` + ws_shell / pytest smoke |
| QA gate | SCRATCH.md “QA gate” GREEN (default on) |
| Explore reuse | 2nd run skips deep dive ♻️ |
| Skills flywheel | `.slmcode/SKILLS.md` 🦋 |
| Resume | `/stop` → `/resume` 🛟 |
| Agent detail | Studio Agents → click row shows system prompt |
| 14 agents | `/api/agents` |

Stuck? → [❓ FAQ](faq.md)

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
