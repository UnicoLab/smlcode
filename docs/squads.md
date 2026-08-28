# 👥 Squads — parallel virtual dev teams

Build a Go backend and a React frontend **at the same time**, behind an
interface frozen before either half starts.

```bash
slmcode run "build a todo app: Go API serving a React frontend"
```

```text
charter  assembling parallel squads
charter  2 squads · 2 interfaces · backend(cmd/**,internal/**) | frontend(web/**)
charter  squad backend owns cmd/**, internal/**, go.mod
charter  squad frontend owns web/**
charter  squad assignment: backend=4 frontend=3 · 1 cross-squad
execute  squads: backend 2/4 working · frontend 1/3 working
```

---

## Why this exists 🤔

"A Go backend serving a React frontend" is not one stream of work. It is two,
and running it as one fails in two different ways:

- **Sequentially** — the wall clock is the sum of both halves, and the second
  half re-derives context the first already established.
- **Concurrently, with nothing frozen between them** — the frontend invents
  `GET /todos` returning `{items:[…]}` while the backend builds `GET /api/todos`
  returning a bare array. *Both halves pass their own tests. The app is broken.*

The fix is not more parallelism. It is **an interface frozen before either half
starts**, plus ownership boundaries that keep the halves out of each other's
files while they work.

---

## What a squad is 🧩

Three things, and it is useless without all three:

| | Why |
|---|---|
| **A domain it owns** (path globs) | Nobody else may write there — so two teams can run at once with no lock between them |
| **Its own acceptance command** | `go test ./...` and `npm run build` are not interchangeable, and one global QA command cannot express both |
| **A contract it provides or consumes** | Written to disk *before any worker runs*, so both halves build against the same text instead of their own recollection of the prompt |

---

## The flow 🔁

```text
explore ─▶ charter ─▶ plan ─▶ split ─▶ execute (both squads in parallel) ─▶ integration
             │                  │            │
             │                  │            └─ per-task squad brief + ownership deny list
             │                  └─ every task stamped with its owning squad
             └─ manager assembles squads, freezes the contract, writes CONTRACT.md
```

### 1. The manager assembles the teams

The `manager` specialist reads the query and returns an org chart: squads, what
each owns, the interfaces between them, and how the halves are joined.

It returns **one** squad for a single-domain query, and the run proceeds as a
normal single stream — squads are an accelerator, never a prerequisite.

### 2. The contract is frozen to disk

`.slmcode/CONTRACT.md`:

```markdown
# Interface contract

FROZEN. Both squads build against this text.

### GET /api/todos
- Provided by: `backend`
- Consumed by: `frontend`

    200 -> [{"id":string,"title":string,"done":bool}]
```

Two squads running concurrently cannot ask each other what the seam looks like,
so the seam has to be a file they both read. It is also the cheapest place for a
human to intervene: fixing one line here is worth more than reviewing either
half afterwards.

### 3. Tasks are routed to owners — and to specialists

Each task is stamped with the squad that owns its files, **and staffed with the
specialist its own files call for**. The composer picks one language specialist
per run; that is right for a single-language repo and wrong for every task on
the other side of a mixed one.

```text
split  routed 5 task(s) to language specialists: go-worker=3 react-worker=2
split  assigned react-worker — files are react
```

Precedence: a registered specialist the task already names → a non-implementer
role left alone → **the language of its own files** → its squad's preferred
worker → the run default → the generic worker. Files outrank the squad label for
the same reason the repository outranks a word in the query: an extension is a
fact, a label can be stale. The reviewer and tester are matched the same way — a
reviewer judging TypeScript with a Go reviewer's prompt reads the diff for the
wrong hazards.

### 3b. The contract becomes acceptance criteria

The interfaces are attached to the tasks that owe or consume them, as blocking
criteria:

```text
charter  contract attached as acceptance criteria on 5 task(s)
```

- a **provider** must *match* the frozen spec — another squad is building
  against it right now;
- a **consumer** must *call it exactly as stated, whether or not it exists on
  disk yet* — failing it because the provider has not finished would penalize it
  for being on time.

Without this the contract is in the prompt and absent from the gates: a worker
that drifts from the spec produces a task the reviewer approves — it did what
its description said — and an integration failure much later with no owner.

The task's own conditions keep their place at the head of the list, so a worker
is never handed a criteria list that is all seam and no job.

### 3c. Which tasks stay unassigned

Two kinds of task stay **deliberately unassigned**:

- **Cross-squad tasks** (the seam itself) — handing `cmd/server/main.go` +
  `web/vite.config.ts` to "frontend" is exactly how a frontend task acquires
  permission to rewrite the API.
- **Unowned files** — a hole in the squad plan, reported rather than guessed at.

### 4. Both squads execute in parallel

Their files are disjoint by construction, so the wave scheduler admits tasks
from both teams into the same wave. Each worker gets:

- **its own brief** — its charter, its boundary, and the interfaces it owes or
  consumes. Never the whole contract: a worker handed both halves spends its
  attention on the team it is not on.
- **an enforced boundary** — the other squads' paths go on the workspace deny
  list, so a write outside its lane is refused at the tool layer. A prompt
  saying "do not edit `web/`" is a suggestion a stuck model talks itself out
  of; a deny list is not.

### 5. The manager watches for cross-team stalls

Between waves:

```text
execute  squads: backend 1/4 working · frontend 3/3 done
execute  frontend is waiting on backend to deliver "GET /api/todos"
         — this is a contract dependency, not a task defect
```

A consumer blocked on an undelivered interface is not something a reviewer can
fix, and retrying its tasks forever is the wrong response.

---

## When a gate rejects the work 🎫

A failing tester or reviewer does not produce a red notification and a generic
"fix it" task. It produces a **correction ticket**:

- routed to the specialist whose language actually broke — a TypeScript compile
  error handed to the plain worker gets a plain worker's guess;
- carrying the evidence: what broke, the command that found it, the tail of that
  command's output, the implicated files, and an acceptance that names the
  command rather than saying "the tester passes";
- kept with the squad that owns the files, so a backend regression does not land
  on the frontend's board;
- **deduplicated** — the same unresolved defect reopens its existing ticket
  instead of stacking a new one on every gate run, which is what made the board
  look like it was losing ground.

A repeat correction says which attempt it is and not to repeat the last one.
The attempt count is per **defect**, not per board: two unrelated failures used
to make a first attempt at a third defect announce itself as a third, telling
its worker that approaches it never tried had already been ruled out.

### The project manager decides who takes it next

Routing a ticket by language is the right *first* answer and the wrong *second*
one. When the same defect comes back, language routing hands it to the agent
that just failed at it, carrying a ticket whose only new content is that it
failed again — which is the loop that made gate failures feel like noise rather
than progress.

So a **repeat** ticket goes past a project manager before it goes back to work.
The manager does the two things the router cannot: pick somebody else, and say
what to do differently. Its direction is written *above* the evidence in the
ticket, because it is the only thing there the last attempt did not already
have.

The roster it picks from is **ranked and labeled**, not alphabetical. Sorted by
name, `corrector` and `deep` come before `go-worker` and `python-corrector`, so
the generic agents sit at the top of the list the model reads first — and it
takes one, even though the prompt told it to prefer a specialist. The task's own
language leads:

```text
## ROSTER — pick exactly one of these, best fit first
- go-corrector (Go specialist)
- go-worker (Go specialist)
- react-worker (React specialist)
- corrector (generic)
- worker (generic)
```

And the preference is **enforced**, not merely requested: a generic pick is
upgraded to the language corrector — or its worker, when no corrector is
registered — whenever the roster offers one. A generic corrector handed a
failing Go handler brings nothing the Go worker that already failed did not
have. A pick that is already a specialist is never overridden: a manager that
deliberately reached for another language's expert has a reason the file
extensions cannot see.

Testers are deliberately absent from the roster. Triage decides who *writes* the
fix; offering a tester or a reviewer invites an answer the loop would then
refuse.

Its verdict is validated before it is applied. Two answers are worse than no
manager at all — naming an agent that cannot be dispatched (the ticket then sits
unassigned) and re-picking the agent that just failed (the loop triage exists to
end) — and either one falls through to the deterministic route rather than being
applied.

The same manager decides when the review ladder runs out of retries, one handoff
before a human is asked.

### Each team can have its own manager

A run-wide manager answering for a specific team picks from a roster it has no
reason to understand: the agents who can fix a failing Go handler are not the
ones staffing the React half. So a squad may carry its own `manager`, and the
triage request tells it which team it answers for and who staffs it.

The team's own people are listed **first** in the roster, not exclusively — the
whole reason a delivery was rejected may be that the team lacks the skill the
fix needs, and a manager forbidden from looking outside could only choose
between agents that have already failed.

Only an agent that answers the triage contract may be nominated. An agent's
decoding grammar is derived from its own system prompt, so one that answers a
different contract replies with something the reassignment step cannot read —
after a full model call has already been spent. Nominate a built-in `triage`, or
write your own agent with a `-triage` suffix in its id:

```yaml
# .slmcode/agents/backend-triage.yaml
id: backend-triage
title: Backend project manager
system_prompt: |
  You decide who takes a rejected delivery next...
```

Attach one on the plan-approval card or on the **Teams** page; leaving it empty
hands the team back to the run's default manager.

---

## Safety: disjoint ownership 🛡️

**Two squads may never own the same path.** They write concurrently; an overlap
means one team's edit is silently lost.

The plan is validated before anything runs, and an overlap is an **error** — the
run falls back to a single stream rather than starting two teams that can
corrupt each other:

```text
charter  squad plan rejected: squads "backend" and "frontend" both claim
         "web/**" / "web/src/**" — two teams writing one path in parallel
         loses one of the two edits; give each squad a disjoint subtree
```

The overlap check is deliberately conservative. A wrong "these overlap" costs
the manager one more specific glob; a wrong "these are disjoint" costs a lost
edit — the bias is not symmetric and neither is the rule.

Other findings are **warnings** (the run continues): a squad with no acceptance
command, an interface with no spec, a plan with no integration command.

---

## Configuration ⚙️

```yaml
# .slmcode/config.yaml
squads: true       # default
```

```bash
slmcode config set squads false   # always run a single stream
```

On by default: it costs one extra planning call per run, which is cheap next to
the sequential build it removes, and returns "one stream" for single-domain
queries anyway.

**Everything about it is non-fatal.** A manager that fails, times out, or returns
a plan that does not validate leaves the run exactly as it was — one stream, one
board. The only thing it will never do is activate a plan it could not validate.

---

## Artifacts on disk 📁

```text
.slmcode/
├── CONTRACT.md    the frozen interface — what the agents read
└── squads.json    the plan — what a resumed run reloads
```

Both are written atomically, together. A plan without its contract would mean
agents building against text that describes a different plan.

---

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
