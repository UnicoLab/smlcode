# 👥 Teams — parallel virtual dev teams

Build a Go backend and a React frontend **at the same time**, behind an
interface frozen before either half starts.

```bash
slmcode run "build a todo app: Go API serving a React frontend"
```

```text
charter  team library: 2 teams selected: backend-go, frontend-react
charter  team backend-go — workspace has "go.mod"; workspace contains ".go" files
charter  team frontend-react — query mentions "react"; workspace has "web/package.json"
charter  freezing the interface contract
charter  2 squads · 2 interfaces · backend-go(cmd/**,internal/**) | frontend-react(web/**)
charter  squad assignment: backend-go=4 frontend-react=3 · 1 cross-squad
execute  squads: backend-go 2/4 working · frontend-react 1/3 working
```

Teams come from a **library you own** — create, edit and delete them on the
Teams page, attach them to a pipeline, pin them for one run. Which of them a
request involves is decided from the words in the query and the files on disk,
with **no model call**; see [The team library](#the-team-library-).

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
             └─ library preselects the teams, manager freezes the contract,
                CONTRACT.md written
```

### 1. The teams are chosen 🎯

Two entrances, in this order:

**The library, deterministically.** Every team in the library carries the
evidence that says when it applies — query keywords, marker files, file
extensions. Scoring that evidence answers "which teams does this request
involve" with no model call at all:

```text
charter  team library: 2 teams selected: backend-go, frontend-react
charter  team backend-go — query mentions "backend", "api"; workspace has "go.mod"
```

This is the part that mattered most to get off the model. The org chart is
assembled *before* the split, and ownership, routing, per-team acceptance and
the write deny list are all derived from it — so a 7–32B model that returns
overlapping globs, an unregistered worker id, or JSON that will not parse does
not produce a warning, it silently downgrades the whole run to a single stream.
Reading `go.mod` off disk cannot fail that way.

**The manager, when the library has nothing to say.** No library answer — a
repository the library does not cover — and the `manager` specialist assembles
the whole org chart itself, exactly as before.

Either way, **fewer than two teams means one stream**. That is the correct
answer for a single-domain query, which is most of them, and it is reported
rather than silent:

```text
charter  team library: only backend-go matched — one team is the single-stream
         pipeline wearing a hat, so it runs as one stream
```

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
half afterwards — the plan-approval card and the Teams page both edit it.

On the library path the manager is asked for **only** this. The teams, their
globs and their acceptance commands are handed to it as fixed, so the answer it
has to get right is a third of the size — and the two thirds it no longer owns
are the two thirds that could corrupt a run.

A reference that misses by a suffix is resolved rather than dropped. The teams
are called `backend-go` and `frontend-react`; a small model handed those ids
still writes `backend`. That clause would name a provider no team matches, fail
validation, and cost the run its frozen seam — so an **unambiguous** reference
resolves to the real team. Ambiguity is not guessed at: `backend` against a plan
holding both `backend-go` and `backend-node` is a question only the author can
answer.

### 2b. The splitter is told where the boundaries are

The org chart exists before the split, so the splitter is handed it:

```text
## Teams — every task's files must stay inside ONE team

- `backend-go` owns cmd/**, internal/**, pkg/**, go.mod
- `frontend-react` owns web/**, frontend/**, client/**

A task whose `files` span two teams belongs to neither…
```

A model cannot respect a boundary it was not shown. Measured against a live 30B
without this: it emitted two tasks, each naming `cmd/server/main.go` **and**
`web/src/App.tsx`. Both correctly stayed unassigned — a seam task handed to one
half is how a frontend task acquires permission to rewrite the API — and the run
finished having done no parallel work at all, on a plan whose org chart and
frozen contract were both right. The failure is invisible in the output, which
is what makes four lines of prompt worth their tokens.

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

### The reassignment is executed, not just recorded

A ticket the manager just moved is new information nobody has acted on, so it
gets its own wave — `re-staffed wave: 1 ticket(s) moved by the project manager`
— followed by a re-verify. Without it the run would finish having done the whole
analysis and thrown it away.

It is bounded twice: a ticket is re-staffed **at most once**, and the
corrective-wave budget still applies. A second verdict would be a third agent
guessing at work two others could not do, which is a scoping problem rather than
a staffing one, and that is what a human is being asked to look at.

### A defect stays in its own team's lane

Reopening work from a tester failure is a text match — file basenames,
acceptance snippets, task ids in the failure blob — and text matches leak across
teams. The frozen contract makes that worse, not better: it is attached as
acceptance criteria to *both* halves, so one clause of shared text used to be
enough for a backend compile error to reopen the frontend's finished tasks.

So ownership scopes it, the same property the write deny list enforces one layer
down. When every path the tester named lies inside one team's territory, only
that team's work may be reopened. Unassigned tasks are never excluded — they have
no team to be outside of — a defect on the seam reaches both halves, and a run
without an org chart is filtered by nothing.

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

## Each half proves itself 🟢

A squad's acceptance command is one of the three things a squad *is*: the
command that proves this half works alone. It runs once every task in the lane
is done, before the halves are joined:

```text
verify  team backend-go: proving its half alone — go build ./... && go test ./...
verify  team backend-go is green: go build ./... && go test ./...
verify  team frontend-react: acceptance could not run (npm is not installed here)
        — its half is UNVERIFIED, not broken
```

Three rules, and the second is the one that keeps a local run from going red for
nothing:

| | |
|---|---|
| **Green means proved** | Not "every task reached done". A half can finish its tasks and not build, and the first thing to notice used to be integration — which then reported the *seam* as wrong when the real defect was one team's own code. |
| **Absent tooling is UNVERIFIED, never red** | `npm run build` where `node_modules` was never installed exits non-zero and says nothing whatsoever about the code. Scoring it red sends a corrector to rewrite source that was never at fault, burns the retry budget, and shows a red team for something no model can fix. |
| **A red half is that team's ticket** | Scoped to its own lane, because the wave's write deny list is derived from the task's squad — a ticket carrying another team's paths is one the tool layer refuses on exactly the files it was told to fix. Integration is skipped: joining a known-broken half proves nothing. |

The result is on the board, per team: **proved green**, **half is red**, or
**unverified** with the reason.

---

## Watching it happen 👀

**The board** shows a strip above the columns: each team, its project manager,
the command that proves its half, its progress, and any cross-team wait. A team
blocked on another team's interface is a *contract dependency* and reads as one
rather than as a red task. Clicking a team filters the board to its lane; the
tasks on **no team** get their own lane, because the seam is a real thing to look
at. Every card carries its team as a badge and a coloured left edge, and the
colour comes from the team's id — so it is the same everywhere, and adding a
team never recolours the others.

**The live view** pins one line above the stream: the agent, its task, its team,
the model, the run's token usage, and a clock that keeps ticking. On a local 30B
the next log line can be four minutes out, and a wall of finished lines under a
blinking cursor reads as a hang. Past two minutes on one step it says so in a
colour, because a user who knows a call takes minutes is a user who waits
instead of hitting stop.

---

## When the halves do not fit 🔗

Every squad green and the assembled application broken is the defect this whole
design exists to catch: both halves passed their own tests and the seam between
them is wrong.

The integration command runs as soon as every squad is complete, and a failure
raises a **ticket somebody owns** rather than a warning at the end of a
"successful" run:

- the **provider owes the clause** — a consumer built against text it was
  handed, so the team that either implemented that text or drifted from it is
  the one that gets the ticket;
- the ticket carries the integration command, the output that names the seam,
  the contract clauses at stake and the implicated files;
- files are kept to the owing team's lane. If the output named nothing there,
  the ticket ships unscoped rather than pointing at the other half's files.

With several providers, the failure text decides — by naming a team or a path
inside its lane. When it names neither, the ticket stays unassigned: a guess is
worse than nothing, because an unassigned ticket with real evidence beats one
parked on the wrong team.

A seam that fails again is the same defect: the existing ticket's attempt count
goes up rather than a second ticket appearing.

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

The fence is per wave, and a task with no squad no longer drops it for
everybody. An unassigned task's **declared files** say which lanes it needs: a
seam task naming `web/src/api.ts` opens the frontend's lane and nothing else, a
task naming only `README.md` opens nothing, and every other team stays fenced.
That matters more than it sounds — a task is unassigned whenever it straddles
two teams *and* whenever nothing owns its files at all, so one `Makefile` task
in a wave used to unfence both halves.

The one case that still stands down completely is a task that declared **no
files**. Declared files are a task's scope; one with no scope could write
anywhere, and fencing it would block work the harness cannot show is out of
bounds.

Other findings are **warnings** (the run continues): a squad with no acceptance
command, an interface with no spec, a plan with no integration command.

---

## The team library 📚

A team is not a per-run fact. *"The backend is Go, it lives under `cmd/` and
`internal/`, `go-worker` writes it, `go test ./...` proves it"* is true of the
repository, on every run, forever. Re-deriving it from a model each time pays a
planning call to rediscover something you already know — and gets it subtly
wrong a meaningful fraction of the time.

So teams live in a library, as [building blocks](blocks.md) of kind `team`:

```text
pkg/blocks/bundled/teams/    shipped with SLMCode
~/.slmcode/blocks/teams/     yours, every project
.slmcode/blocks/teams/       this project — wins on an id clash
```

Six ship by default: `backend-go`, `backend-python`, `backend-node`,
`frontend-react`, `docs` and `infra`. Editing a builtin writes a **project
override** that shadows it; deleting the override reveals the builtin again.
There is nothing to delete until you have edited one.

### What a team carries

```yaml
# .slmcode/blocks/teams/payments.yaml
api_version: blocks/v1
kind: team
id: payments
name: Payments
spec:
  id: payments
  charter: Own billing and invoices. Never edit another team's files.
  owns: [billing/**, internal/money/**]     # no other team may claim these
  acceptance: go test ./billing/...          # proves THIS half works alone
  worker: go-worker                          # blank = the pipeline default
  reviewer: go-reviewer
  tester: go-tester
  manager: backend-triage                    # who triages its rejected work
  agents: [go-corrector, deep]               # the rest of the team — any number
  skills: [go-table-tests, go-concurrency]   # pinned for every task it takes
  match:                                     # when this team applies
    keywords: [billing, invoice, payment, refund]
    files: [billing/go.mod]
    extensions: [.go]
    priority: 0
```

Everything under `spec` is a field the harness actually reads. The two that
carry the most weight are the two people get wrong:

| | |
|---|---|
| **`owns`** | Teams may **never** share a path. Two agents writing one file in parallel loses one of the edits, silently. |
| **`match`** | This is what preselects the team without a model. Leave it empty and the team becomes **manual-only**: still pickable by hand, never automatic. |

### The team is however many people you put on it

Four seats — worker, reviewer, tester, manager — is a shape the harness
*dispatches*, not a shape a team has to *be*. `agents` is the rest of the team,
in any number and in the order you write them, and it is load-bearing in three
places:

- the project manager triaging this team's rejected work sees **its own people
  first** (that is the whole reason a per-team manager beats a run-wide one);
- the approval card offers them first for this team's tasks;
- an agent this harness cannot dispatch is **dropped with a reason** rather than
  becoming a name on a team that never does any work.

`skills` is open the same way: everything listed is pinned for every task the
team takes, on top of whatever the query matches.

Nothing here is filtered — the roster is an **ordering**, never a fence. The
reason a task needs reassigning is often that its team lacks the skill the fix
needs, and a picker that hid everybody else could only offer agents that have
already failed.

### How a team is scored

| Evidence | Weight | Why |
|---|---|---|
| a marker **file** exists | 4 | `go.mod` is a fact about the repository |
| a **keyword** appears in the query | 3 | matched on word boundaries — `api` must not fire on *rapids* |
| an **extension** is present | 2 | one `.go` script is the weakest of the three |
| its **territory already exists** | +1 each, capped at 3 | corroboration, not a reason — capped so breadth cannot win |

`priority` breaks ties, and a **negative** priority opts a team out of automatic
selection entirely while leaving it pinnable. That is why `docs` and `infra`
ship at `-1`: nearly every request touches them in some sense, and a team that
joins every run adds an acceptance command to every wave for nothing.

Selection is capped at four teams, and **overlap is resolved here** rather than
downstream. A library holding both `backend-go` and `backend-node` — as any real
one will — would otherwise make every mixed repository fall back to a single
stream. The weaker claim is dropped, and the page says which team took the
contested path.

### Managing them

On the **Teams** page: create, edit, duplicate, delete, and *Try a request* —
type a query and see which teams it would get **and why**, from the same code
the run uses, before starting anything. Pick two or more and **Activate** to
write the org chart the next run inherits.

```bash
slmcode blocks list                       # teams appear under TEAMS
slmcode blocks show team backend-go
slmcode blocks new team payments --file payments.yaml
slmcode blocks delete team payments
```

### Pinning a team for one run

An explicit choice is an instruction, not a hypothesis: a pinned team is used
regardless of what the query says, and it wins the contested paths.

```bash
slmcode run "add invoice totals" --team payments --team frontend-react
```

Studio's run setup sends the same thing per run; it is restored when the run
ends, so a one-off choice never quietly governs every later run.

### Attaching teams to a pipeline

A pipeline is a shape of work, and for most shapes the shape implies the org
chart — a *fullstack* pipeline always has a backend half and a frontend half,
whatever today's query says. Attaching them also reaches teams a query would
never hint at:

```yaml
# .slmcode/pipeline.yaml
teams: [backend-go, frontend-react, infra]
```

Ids the library no longer has are dropped with a warning rather than failing the
pipeline: a shared preset outlives the library it was written against, and
running with one fewer team is a better answer than refusing to start.

---

## Editing the plan before it runs ✏️

The approval gate used to offer two answers: approve, or replan — and replan
throws the whole board away to fix one wrong file path. So people approved plans
they could see were slightly wrong and let the run find out.

Editing is the third answer, and **everything the harness can apply is
editable**, because a field that is visible and not editable is worse than one
that is hidden:

| | |
|---|---|
| **Tasks** | title, description, agent, team, files, acceptance, priority, and **waits for** — add a task, remove one, or make an existing task wait on a task you just added |
| **Teams** | name, charter, owns, acceptance, worker, reviewer, project manager; **add** a team from the library or remove one outright |
| **Contract** | rename a clause (keeping its spec), change its provider and consumers, edit the shape, add and remove clauses |

Ordering is expressed as **dependencies**, not position. The board dispatches
waves, so "do this later" only means "after these have finished" — a task with
no dependency runs in the first wave wherever it sits in the list.

Only the fields you actually changed are sent, so the harness can tell *"I did
not touch this"* from *"set this to empty"*. Edits are applied by the harness,
not by the model, so what you saw is what runs — and an edit that makes two
teams share a path is refused **whole**, with the collision named, rather than
half-applied.

The same editor is on the Teams page, so the org chart and its contract can be
corrected without waiting for a gate to open.

---

## Configuration ⚙️

```yaml
# .slmcode/config.yaml
squads: true          # default — assemble teams at all
team_library: true    # default — preselect from the library before asking a model
teams: []             # pinned team ids (per run; `--team` writes this)
```

```bash
slmcode config set squads false         # always run a single stream
slmcode config set team_library false   # always let the manager assemble them
```

Both on by default. Assembly costs one extra planning call per run, which is
cheap next to the sequential build it removes, and returns "one stream" for
single-domain queries anyway.

**Everything about it is non-fatal.** A library that covers nothing, a manager
that fails or times out, or a plan that does not validate leaves the run exactly
as it was — one stream, one board. The only thing it will never do is activate a
plan it could not validate.

---

## Artifacts on disk 📁

```text
.slmcode/
├── CONTRACT.md            the frozen interface — what the agents read
├── squads.json            the org chart — what a resumed run reloads
└── blocks/teams/*.yaml    the library — teams you authored or overrode
```

`CONTRACT.md` and `squads.json` are written atomically, together. A plan without
its contract would mean agents building against text that describes a different
plan. The library outlives both: it is what the *next* run starts from.

---

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
