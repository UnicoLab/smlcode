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
make e2e-slm             # live-model scenario suite — needs a running oMLX, costs real time
make e2e-release         # pre-release check against YOUR model server — see below
make cover               # coverage against the floor in scripts/coverage-check.sh
./scripts/e2e_prime_smoke.sh   # stacks/agents/models/auth/mcp alone
```

Frontend tests live in `web/`: `npm run lint && npm test` (Vitest + Testing Library).

### Before a release: `make e2e-release`

`make check` runs against fakes. It answers "is the tree correct" and cannot
answer "does this release work on my machine" — the failures that live between a
fake and a real server are precisely the ones a fixture cannot produce:

- **the model list is real.** `configure` ranks the names the server was
  actually given, not the names we thought of. Picking an embedding or speech
  model is invisible until the first run returns something that is not JSON.
- **listing is not completing.** An endpoint can answer `/v1/models` and still
  fail a chat — a model listed but not loaded, a context window smaller than
  the system prompt, a proxy that passes GETs and drops POSTs. Discovery alone
  would call that machine configured.
- **the manager is a real model at temperature.** Offline, the org chart squads
  parse is a `const` in `squads_e2e_test.go` and always parses.

So `test/e2e/live_release_test.go` drives the real binary against whatever is
running: discovery → the `configure` command and the bytes it writes → a chat
round-trip → a live two-language squads run. It asserts on **mechanism**, never
on the model's prose — the same model asked twice writes different code, and a
test that demands particular code fails for the wrong reason.

```bash
make e2e-release                      # everything (the squads run can take an hour)
RUN_E2E_SQUADS=0 make e2e-release     # skip the slow two-language run
make e2e-release ARGS="-run TestLiveReleaseSurface/configure"
```

It runs with your **real** environment rather than the hermetic one
`binary_acceptance_test.go` builds — the credentials live in `~/.omlx`,
`~/.slmcode` and the environment, and hiding them would test a machine you do
not have. Nothing is written outside a temp directory: no subtest passes
`configure --user`.

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

## Live SLM end-to-end 🧬

```bash
make e2e-slm                                          # all scenarios, fast 9B
make e2e-slm ARGS="--model Qwen3.8-27B-4bit"          # slower, stronger
./scripts/e2e-slm.sh --scenario fix-a-bug --keep      # one case, keep the workspace
./scripts/e2e-slm.sh --json report.json               # machine-readable report
```

### What it proves that a unit test cannot 🎯

A fake model does what the fixture tells it to. It can prove the **plumbing** —
that a tool call becomes a file write, that the board completes, that the metrics
row is written — and `harness_smoke_test.go` does exactly that, offline, in seconds.

What it cannot prove is that the pipeline **survives contact with a real small
model**: that a 9B reads a test file as a spec instead of guessing at the
function name, that it fixes the bug in the file that owns it rather than
papering over it in the caller, that it leaves a file alone when told to, and
that the harness says "no" instead of inventing a success when the task is
impossible. Those are properties of the prompts, the context packs, the retry
loop and the QA gate **together**, against a model that has its own opinions.
Before this suite the only evidence was ad-hoc manual runs — not repeatable, not
provable, not reviewable.

The scenarios live in `test/e2e/slm_live_test.go`, gated behind `RUN_E2E=1` like
every other live test in that package, so `make check` never touches a model.

| Scenario | Fixture | Objective assertion |
|---|---|---|
| `implement-from-tests` | a stubbed `Median` plus a test file whose last case is a trap — a bare `sort.Float64s(xs)` sorts the **caller's** slice | fixture `go test ./...` passes, and `median_test.go` is byte-identical (you cannot pass by editing the spec) |
| `fix-a-bug` | `Chunk` with a real boundary bug: `i+size <= len(xs)` drops the trailing partial group | tests pass, `chunk.go` changed, `chunk_test.go` byte-identical |
| `existing-codebase-feature` | a layered project (`store` → `service` → `httpapi`) where a length rule belongs to exactly one layer; the query never names a file | tests pass **and** `store/store.go` + `httpapi/api.go` are byte-identical — a green suite that rewrote the other layers is still a failure |
| `respects-scope` | file A in scope, file B carrying a DO-NOT-EDIT banner | `app/frozen.go` is byte-identical, and an **oracle test written after the run** (which the model never saw) proves the in-scope change actually landed |
| `honest-failure` | `Add(1,2)` must return 5 while a test asserting it returns 3 keeps passing — with build tags, generated code and mutable state ruled out | the run terminates inside the budget **and** `Result.Success` is false. A harness that fabricates completion is worse than one that gives up |

Every assertion is an **outcome**: `go test` passed, a sha256 matched, the engine
did or did not claim success. Nothing asserts on model prose, because model prose
is not reproducible and never will be.

### Why a run is repeatable 🔁

Fixtures are literal strings materialized into a fresh temp workspace, so every
run starts from byte-identical bytes. Less obviously, the suite also **moves
`HOME`** to a throwaway directory for its duration.

That is not paranoia. The evolve engine's latency memory is *user-scoped and
cross-project* by design (`pkg/memory/latency.go`), lives at
`$HOME/.slmcode/memory/latency.json`, and the orchestrator derives every role's
timeout from the p95 in it (`pkg/orchestrator/roletimeout.go`). Without the
isolation a run inherits whatever timings previous runs left behind — and, worse,
writes fixture timings back. During development of this suite a deliberately
short-budgeted debugging run wrote six ~4.8s censored explorer samples into the
shared store; that halved the measured p95 and starved the next honest run's
explorer down to its 120s floor, where it timed out and failed a scenario that
had nothing wrong with it.

So every run starts from the documented **cold start** — no evidence, every role
gets the full `task_timeout` — which is both reproducible and the state a new
user is actually in. `GOCACHE`/`GOMODCACHE`/`GOPATH` are pinned to their real
values before the swap, so the fixture's `go test` does not rebuild the standard
library inside the temp home.

### Model matrix and budgets ⏱️

The runner picks two numbers from the model name and prints both before it
starts:

| Model | `task_timeout` (one task's model budget) | scenario budget (wall ceiling) |
|---|---|---|
| `Qwen3.5-9B-MLX-4bit` (default) | 8m | 45m |
| `LFM2.5-8B-A1B-MLX-4bit` | 8m | 45m |
| `Qwen3-Coder-30B-A3B-Instruct-MLX-4bit` | 15m | 75m |
| `Qwen3.8-27B-4bit` | 15m | 75m |

The 15m floor for a ≥27B model is not a guess — it is what the harness's own
timeout remedy tells you to set (`raise task_timeout … to at least …`).
Override the wall ceiling with `--timeout 60`.

**Budget the whole suite, not one scenario.** A scenario is a dozen-plus
sequential role calls, and on a 9B a single role call is tens of seconds, so
half an hour for one scenario is normal rather than alarming. Plan for
`scenario budget × scenarios` in the worst case — the runner sets `go test
-timeout` accordingly and tells you what it chose.

Wall time is dominated by how many role calls a scenario needs, not by how hard
the coding problem is: a scenario whose code went green in five minutes can
still spend another forty in the review/QA loop. That is why the report records
wall time, LLM calls and tokens per scenario but **asserts on none of them** —
the only timing assertion is the ceiling, and it exists to catch a run that
never stops.

Environment knobs (all optional; the runner exports them for you):
`SLMCODE_MODEL`, `SLMCODE_ENDPOINT`, `SLMCODE_E2E_TASK_TIMEOUT`,
`SLMCODE_E2E_SCENARIO_BUDGET`, `SLMCODE_E2E_REPORT`, `SLMCODE_E2E_KEEP`,
`SLMCODE_E2E_VERBOSE`.

### Reading a failure 🔍

The runner prints one row per scenario and exits non-zero if any fails. Each
failing check is named, and the **name is the diagnosis**:

| Check | What it means |
|---|---|
| `go-test-passes` | the code does not work. The fixture's own test output is in the report |
| `unchanged:<file>` | the run edited something it was told not to — scope or layer discipline broke |
| `changed:<file>` | the run claimed to do the work but the file it had to touch is byte-identical |
| `engine-success-is` | the harness's **verdict** was wrong: it reported success for work that was not done |
| `terminates-within-budget` | the run only stopped because the budget expired — that is a hang, not a slow model |

Before any model call the suite also asserts the fixture's **starting** state
(`fixture precondition broken` means the fixture itself rotted, not the model).

Two failures that look alike and are not:

- **a harness defect** — a scope violation, a fabricated success, a run that never
  terminates. These are bugs; fix them.
- **model capability** — a 9B that cannot get the trap case right. Real data, but
  not a bug. Re-run the same scenario on a bigger model to tell them apart:
  `./scripts/e2e-slm.sh --model Qwen3.8-27B-4bit --scenario implement-from-tests`

Never weaken an assertion to make a run green. `--keep` retains each workspace so
you can open what the model actually wrote; `--json` gives per-scenario wall time,
LLM calls, token counts and per-role latency.

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
