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
slmcode init   # detects Go from go.mod + .go content and applies the go pack for you —
               # watch for "✓ auto-applied go pack" and "pack   go (detected)"
slmcode run -v "Add a Go doc comment to Hello() explaining it returns a greeting. Keep it tiny."
cat hello.go && slmcode board && slmcode session list
```

**Pass:** doc comment present · board done · session saved · skills touched. 🎉

---

## Studio / API 🎨

```bash
slmcode studio            # open the URL it prints — it carries ?t=<token>
T=<the token from that URL>
curl -s -H "X-SLMCode-Token: $T" http://127.0.0.1:7420/api/health | jq .
curl -s -H "X-SLMCode-Token: $T" http://127.0.0.1:7420/api/agents | jq 'length'   # 20 built-ins + registry blocks

# Auth is on by default and covers the HTML shell too, so both of these are 401:
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7420/api/health   # 401
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7420/            # 401 + "open the URL the CLI printed"
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
make bootstrap           # build the Studio UI first (e2e checks the embedded assets)
make check               # the one gate: tidy-check, fmt, vet, lint, tests+coverage, race, web
make e2e                 # offline e2e + prime CLI/API smoke
RUN_E2E=1 make e2e       # also live oMLX / multi-agent
make cover               # coverage against the floor in scripts/coverage-check.sh
./scripts/e2e_prime_smoke.sh   # stacks/agents/models/auth/mcp alone
```

Frontend tests live in `web/`: `npm run lint && npm test` (Vitest + Testing Library).

### The two suites that stand in for a real run

Both need no model, no network and no API key, and both run under plain `make test`:

| Suite | What it proves |
|---|---|
| `test/e2e/harness_smoke_test.go` | the harness **in-process** — harness → orchestrator → loop → workspace against a fake OpenAI server: the file lands on disk, the board completes, an episode and a metrics row are written with real edit accounting |
| `test/e2e/binary_acceptance_test.go` | the **shipped binary** — builds `./cmd/slmcode` and `./test/fakemodel`, then drives `init → doctor → run → task show → diff → apply` against a Go fixture (`permission: auto`) and a TypeScript fixture (`permission: review`), asserting the bytes on disk, the pack `init` detected, the `.gitignore` it wrote (via real `git check-ignore`), and that the run summary's claims match the tree |

`test/fakemodel` is also usable by hand — it follows the tool contract (reads a file before
writing it), so a full pipeline against it lands real edits:

```bash
go run ./test/fakemodel -addr 127.0.0.1:0        # prints the port it got
go run ./test/fakemodel -mode=401                # reproduce the failures doctor explains
```

Offline prime-port coverage: `TestPrimePortsEndToEnd` (stacks apply, auth.json,
find_models allowlist, compact, events, Studio APIs). `scripts/e2e_prime_smoke.sh` drives the
same surface over HTTP against a live Studio **with** its session token, and asserts that an
untokenised request is refused.

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
| 20 built-in agents | `/api/agents` |

Stuck? → [❓ FAQ](faq.md)

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
