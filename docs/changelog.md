# Changelog

## Unreleased

Three defects found by running v0.23.0 against a local 30B, twice. All three
share a shape: the run reported success, or reported red, and the number it
showed did not mean what it looked like.

### Fixed

- **A task spanning two teams is now cut along the boundary instead of running
  outside both lanes.** Measured twice, on two different local models: *every*
  task in the plan named a file from each half, so not one belonged to anybody
  — `routing: 0 task(s) in a lane, 2 straddling`. The org chart was right, the
  contract was frozen, both halves were edited, and the run reported success
  while the teams did nothing: no parallel waves, no ownership fence, no
  per-team acceptance. Such a task is now split into one task per team
  (`T1-BACKEND`, `T1-FRONTEND`), each carrying only its own team's files and
  told where its boundary is. The run after the change put 6 of its 7 tasks in
  a lane, where before every task straddled — though the two runs planned
  different work (7 tasks against 2), so read that as the mechanism working,
  not as a measured speed-up. The cut is refused where it would destroy
  something: a tester on the seam, or a file no team owns — and each refusal now says which, since a task sitting outside
  every lane looks identical whether the harness chose that or failed to notice.
  The cut is also written through to the live store, which it was not at first:
  every other routing decision mutates the task list in place and so reaches
  the store through the shared backing array, while adding tasks replaces the
  slice, and a new array is invisible to it. That failed in the most misleading
  way available — the cut was reported, both teams were assigned, and then the
  snapshot taken before execute restored the uncut task, so the wave that
  actually ran held the original straddler with no team at all.
  See [squads](squads.md#3c-tasks-on-the-seam-are-cut-along-it).
- **A team no longer goes red because the project defines no such check.**
  `npm --prefix web run build` against a package.json with no `build` script
  exits non-zero, and the user was shown *"team frontend-react is RED — its own
  half does not pass"* over code nothing had found fault with. npm launched
  fine, so the existing absent-tooling rule correctly said no; the epistemics
  are identical all the same. A check that never ran is UNVERIFIED, and the
  board now says which of the two it was. Covers npm, pnpm, yarn, make, and
  `go test` over a tree with nothing in it.
- **The two halves stop waiting on each other once the seam is frozen.**
  Letting them build at once is the whole point of freezing the interface
  contract — the frontend needs the API's shape, not its code, and the shape is
  what was frozen and attached to both sides as acceptance criteria. Planners
  wrote the dependency anyway: measured live, `T2(frontend-react) after=T1`,
  two tasks with disjoint files running in consecutive single-task waves. Every
  mechanism was working and the teams still took twice the wall clock they
  needed, on a model where wall clock is the budget that runs out. Such an edge
  is now dropped, and each drop is reported by name. Only where the contract
  actually replaced it — the awaited task's team PROVIDES an interface the
  waiting task's team CONSUMES. A wait inside one team, a wait involving the
  seam itself, a wait between two teams with no interface between them, and
  every wait in a run with no frozen contract are all left alone.
- **One task can no longer take a whole run to itself.** Measured live: a seam
  task took 17 of a run's 23 agent starts — corrector, reviewer, corrector,
  reviewer, across three consecutive waves — while four tasks sitting in lanes
  were attempted once or not at all, and the run ended with everything
  unexecuted and nothing failed. No single ceiling was wrong: review retries,
  gate retries and the corrective-wave continuation each grant attempts for
  their own good reason, and none can see that another task has had no turn.
  The wave was filled in board order, and a retry usually collides on files
  with the work it would otherwise share a wave with — which is exactly when it
  excludes it. Ready tasks are now ordered least-tried-first, so a first
  attempt at untried work goes before a fourth at work that keeps failing.
  It only reorders: nothing is dropped, a retry runs as soon as no fresher task
  is available, and the attempt ceiling still parks it.
- **A contract clause that names no consumer gets one, when there is only one
  it can be.** The shape a local 30B actually emits is a provider and an empty
  consumer list, which half-freezes the seam: the spec is agreed and nothing
  records who agreed to it. With exactly two teams that is arithmetic rather
  than a guess — the only team that can consume what one provides is the other
  — and left empty it costs the consumer its reason to stop waiting, so the fix
  above never fired. At three teams the consumer is a real question and none is
  invented. The inference is reported, never silent.
- **Duplicate-task merging works again when a language pack is active.** The
  merge grouped tasks by role family using the bare ids `worker` and `tester`,
  so `go-worker` and `react-worker` matched neither and nothing was ever folded
  — and per-task routing puts a language specialist on nearly every task, which
  is most runs. Now uses the suffix-aware `IsImplementerRole` / `IsTesterRole`,
  the predicates that exist for exactly this bug.

- **A task everything waits on is cut too, and its dependents follow.** The
  cut at first refused these, reasoning that rewriting a dependent onto "all of
  the pieces" was a guess. It is not: the parent's work is exactly the union of
  its pieces, so waiting for all of them is precisely as strong as waiting for
  the parent and can never permit anything the original ordering forbade.
  Refusing was the costly choice, and measured live it became the dominant
  shape — the first task straddles, everything waits on it, so it runs alone
  with no team while both lanes sit idle.
- **A correction that made the work worse no longer gets another round.**
  Measured on a live 30B, one task's reviews ran 40, 40, 40, 40, 20, 40, 0
  across its attempts — never improving, and finally destroying what was there.
  Each round cost 300-560s of a 45-minute run while other tasks had not been
  attempted once. A score that merely fails to improve may still be one round
  from passing, so only an actual DROP stops the task: the corrector changed
  the work, the reviewer liked it less, and the next round would start from the
  worse version. Only judged verdicts count — a reviewer that never replied
  scores 0 as an absence, and reading that as "much worse" would end a task on
  a transport error.
- **A team is told that its acceptance command must be able to run.** The brief
  said "your half is done when this passes: `npm --prefix web run build`", which
  a small model reads as a description rather than an obligation — so the
  scaffold got no build script and the half came back UNVERIFIED in every run,
  for want of two lines only that team could add. It now says the command must
  be runnable and that adding the missing script is part of the work.
- **A team is proved by a check its project actually has.** The shipped
  `frontend-react` team declares `npm --prefix web run build`, which is right
  for Vite or CRA and wrong for whatever a 30B just scaffolded — so that half
  reported UNVERIFIED in every run and "both teams green" could never be said.
  The script is now resolved against the project's own package.json: a repo
  whose build is called `compile`, or that has only `typecheck`, proves its
  half instead of reporting nothing. Never substitutes into npm's default
  failing `test` placeholder, which would turn an unproved half into a red one,
  and leaves the command alone when nothing usable exists — an honest grey
  beats proving something nobody asked for.

### Added

- **A run names each frozen contract clause** — `frozen: GET /todos — provided
  by backend-go, consumed by frontend-react`. It used to report a count, which
  makes a contract that froze the wrong seam, or the right seam between the
  wrong two teams, indistinguishable from one that got it right.
- **The live team suite prints the team decisions on every run, green ones
  included** — who was cut, who was assigned, who stopped waiting, which teams
  were live in each wave, how each half was judged. A dozen lines; the full
  event stream stays failure-only. A run that succeeds while its teams do
  nothing is the failure that suite exists to catch, and it is invisible in the
  result: board, elapsed time and success flag all look the same either way.
- **Every wave now says who is in it and which teams are live** —
  `wave 3: T1(backend-go), T2(frontend-react) — teams live: backend-go,
  frontend-react`. A run recorded that a wave happened and never what was in
  it, which is the difference between diagnosing a throughput problem and
  guessing at one: a run put two tasks in disjoint lanes into consecutive
  single-task waves — the teams ran in series, the one thing the design exists
  to avoid — and nothing in 2,700 lines of output said whether a dependency,
  the fence or the scheduler put them there. The helper that formats it had
  been written and tested and never called.

### Known limitation

Plan size is the strongest predictor of a run finishing, and nothing yet acts
on it. Across nine runs of the identical prompt against the same model and
budget — a controlled comparison, since only the decomposition varied — every
run of 1-2 tasks succeeded (3 of 3), 4 tasks succeeded twice in three, and
every run of 5 or more failed (0 of 3). The cap is 8. The measured costs behind
that: planning takes 74-95s, a first attempt 54-150s, and a correction round
306-561s — so more tasks mean more review gates, more corrections, and rounds
that are 4-6x the price of the work itself.

A local 30B still runs out of runway before it runs out of plan on a bad draw,
and run-to-run variance remains larger than any effect measured here — the same
model on the same prompt produced 4 of 4 tasks done in 12m and 3 of 4 in 35m
with the same code. Read the event lines, not the clock: the wave line naming
both teams, and the drop line naming the interface, are what these changes can
be held to.

## v0.23.0 — 2026-08-31

Teams stop being something a model invents once per run and start being
something you own — and the board, the live view and the gates all learned about
them.

### Breaking behaviour changes

No flag, config key or API shape is removed. What changes is what a run DOES.

- **Teams are preselected from a library instead of assembled by a model.**
  `team_library` defaults to `true`, so a repository the shipped teams cover now
  runs with `backend-go` / `frontend-react` rather than whatever the manager
  specialist invented. The manager is still the fallback when the library has
  nothing to say. Set `team_library: false` for the old behaviour.
- **Team ids on the board have changed shape** as a consequence: a task's
  `squad` is now a library id (`backend-go`), not a model-authored one
  (`backend`). Anything grepping event lines or `board.json` for a squad name
  should stop assuming it.
- **A run executes more shell commands.** Each team's acceptance command now
  runs, once, after its lane finishes. A command that cannot start is reported
  UNVERIFIED and fails nothing; a command that runs and fails raises a ticket
  in that team's lane and skips integration.
- **The plan-approval card carries up to 60 structured tasks, was 20.** A larger
  SSE payload and a longer card in the terminal.
- **A correction ticket that straddles two teams now stays unassigned** instead
  of landing on whichever half appeared first on the board. It is worked in the
  normal unassigned lane rather than by one team.

### Added — the team library

v0.22.0 shipped squads: two teams, assembled by the `manager` specialist, alive
for exactly one run. The Teams page reported on them, which meant it was empty
whenever nothing was running — it was reporting on something with the lifetime
of a single run.

A team is not a per-run fact. *"The backend is Go, it lives under `cmd/` and
`internal/`, `go-worker` writes it, `go test ./...` proves it"* is true of the
repository, forever. So teams now live in a library you manage.

- **A team is a block** (`kind: team`), with the same discovery every other
  block has: builtin → user → project, project wins. Six ship by default —
  `backend-go`, `backend-python`, `backend-node`, `frontend-react`, `docs`,
  `infra`. Editing a builtin writes a project override; deleting the override
  reveals the builtin again.
- **Preselection needs no model call.** Which teams a request involves is scored
  from the query's words, the marker files on disk and the extensions present.
  This was the single most expensive gamble in a small-model run: the org chart
  is assembled before the split and ownership, routing, per-team acceptance and
  the write deny list are all derived from it, so a 30B that returned
  overlapping globs or an unregistered worker id silently downgraded the whole
  run to one stream. The manager is still the fallback when the library has
  nothing to say, and is still asked for the CONTRACT either way — the one part
  that is genuinely about this request.
- **A contract reference that misses by a suffix now resolves.** The teams are
  `backend-go` and `frontend-react`; a small model handed those ids still writes
  `backend`. That clause used to name a provider no team matched and cost the
  run its frozen seam. Ambiguity is still refused rather than guessed at.
- **The Teams page is a library manager.** Create, edit, duplicate, delete and
  pin teams; *Try a request* shows which teams a query would get **and why**,
  from the same code the run uses; **Activate** writes the org chart and pins it
  so the next run keeps it.
- **Teams attach to a pipeline** (`teams: [...]` in `pipeline.yaml`) and to one
  run (`slmcode run … --team backend-go`). An id the library no longer has is
  dropped with a warning rather than failing the pipeline.
- **A team is however many people you put on it.** Four seats is a shape the
  harness dispatches, not a shape a team has to be: `agents` is an open roster
  and `skills` an open pack list, both editable from the Teams page and from the
  approval card, with pickers over what is actually installed. The roster is
  what the team's project manager sees first when it decides who takes a
  rejected delivery, and what the approval card offers first for that team's
  tasks — an ordering, never a fence.
- **The splitter is told where the team boundaries are.** The org chart exists
  before the split and the splitter had never been shown it. Measured against a
  live 30B: it emitted two tasks, each naming both halves' files, so every one
  correctly stayed unassigned and the run did no parallel work at all — on a
  plan whose org chart and frozen contract were both right. A model cannot
  respect a boundary it was not shown, and the failure is invisible in the
  output.
- **A team's reviewer and tester now reach the loop.** Both were editable and
  neither was consulted by task routing: the user set a seat, nothing changed,
  and nothing said why. They sit one rung below the language of a task's own
  files, the same place the team's worker already sat.
- **New config:** `team_library` (default `true`) and `teams` (pinned ids).
- **New endpoints:** `GET/POST /api/teams`, `GET/PUT/DELETE /api/teams/{id}`,
  `POST /api/teams/preselect`, `POST /api/teams/activate`.

### Added — the plan-approval card edits everything

A field that is visible and not editable is worse than one that is hidden: it
shows the user the mistake and gives them no way to fix it. The approval editor
covered task title, agent, team and files; it now covers everything the harness
can apply.

- **Tasks** — description, acceptance and priority join the existing fields, and
  **waits for** makes ordering expressible: the board dispatches waves, so "do
  this later" only ever meant "after these have finished". A task you add can be
  depended on by an existing one before the board has named it.
- **Teams** — charter is editable, a team can be **added from the library**, and
  a team can be removed outright. Both were previously one-way.
- **The frozen contract** — rename a clause (keeping its spec), change its
  provider and consumers, edit the shape, add and remove clauses. This is the
  one artifact a two-team run cannot recover from getting wrong, and it was
  read-only.
- The card now carries up to 60 structured tasks instead of 20, and says how
  many it is showing when the board is larger.
- The same editor is on the Teams page, so the org chart and its contract can be
  corrected without waiting for a gate to open.

### Added — the board and the live view learned about teams

- **The board shows the teams.** A strip above the columns names each team, its
  **project manager**, the command that proves its half, its progress, and any
  cross-team wait — a team blocked on another's interface is a contract
  dependency, and it now reads as one instead of as a red task. Clicking a team
  filters the board to its lane; tasks on no team get their own lane, because
  the seam is a real thing to look at.
- **Every task card carries its team**, as a badge and a coloured left edge. The
  colour is derived from the team's id, so it is the same on the board, the card
  and the live rail, and adding a team never recolours the others.
- **The live view says what is happening RIGHT NOW.** One line above the stream:
  the agent, its task, its team, the model, the run's token usage, and a clock
  that keeps ticking. On a local 30B the next log line can be four minutes out,
  and a wall of finished lines under a blinking cursor reads as a hang — this is
  the difference between "thinking" and "stuck", and past two minutes it says so
  in a colour rather than leaving the reader to do the arithmetic.

### Added — each half is proved, not assumed

- **A team's acceptance command now runs.** It was written into the contract,
  shown on the approval card, editable on the Teams page — and never executed.
  "Green" meant every task in the lane reached done, which is a statement about
  the board rather than about the code. Each half is now proved before the
  halves are joined, so a green team is a team whose own command passed.
- **A missing toolchain leaves a half UNVERIFIED, never red.** `npm run build`
  where `node_modules` was never installed exits non-zero and says nothing at
  all about the code; scoring it red would send a corrector to rewrite source
  that was never at fault and show a red team for something no model can fix.
- A half that genuinely fails raises a ticket **in its own lane**, and
  integration is skipped — joining a known-broken half proves nothing and
  reports the seam as the fault.

### Added — a local run stops wasting its budget on duplicate work

Measured on a live 30B (`Qwen3-Coder-30B-A3B-Instruct-MLX-4bit`, oMLX, one
laptop): for one small full-stack change the splitter produced eight tasks — two
workers on the same `App.tsx`, and **four** tester tasks over the same two
files. Each duplicate is a full worker → review → test round, they cannot even
run in the same wave because their files collide, and the run spent its entire
50-minute budget finishing **one** task of the eight and reporting failure.

- **Duplicate testers now fold**, the way duplicate workers already did.
  Verification is idempotent; running the same check three times costs three
  model rounds for one answer. Merging is keyed by role FAMILY, so an
  implementer and a tester over one file never fold into each other — they
  answer different contracts.
- **The dedupe runs again after file reconciliation.** Identity is the file set,
  and reconciliation rewrites file sets — it prunes paths that do not resolve
  and adds what discovery found. Two tasks the sanitizer legitimately declined
  to merge were identical by the time they reached the board, and nothing looked
  again.
- **A merged task keeps the absorbed task's files.** The merge used to narrow
  scope: folding a tester over two files into one over one file silently stopped
  verifying the second, and folding a worker the same way denied it write access
  to a file its own merged description told it to change.

**On measured effect: run-to-run variance dominates.** Three runs of the same
model against the same query produced 8 tasks / 1 done / failed in 42m, then 5
tasks / 4 done / succeeded in 29m, then 8 tasks / 0 done / failed in 42m. The
plan a small model writes differs every time, and how long the run takes is
mostly a function of how much redundant work is in that plan. Do not read a
speed-up into these changes — what they do is remove specific, reproducible ways
the harness wasted the budget or corrupted the routing, each of which is pinned
by a unit test. Reproduce with:

```bash
RUN_E2E=1 SLMCODE_MODEL=<your-model> go test ./test/e2e/ -run TestTeamsLive -timeout 60m -v
```

The suite records the harness event stream and dumps it when the run does not
succeed, so a board that ends with work undone says *which round it stopped on*
rather than leaving it to be guessed at — which is how the three defects above
were found in the first place.

### Fixed

- **Every page except `/` 404'd on a browser refresh.** A client-side route has
  no file behind it, so `/board`, `/teams` and `/settings` all answered the bare
  404 page on a reload, a bookmark, or a link shared with a colleague — while
  in-app navigation to the identical URL worked. A navigation now falls back to
  the SPA shell; a missing *asset* still 404s, because answering a request for a
  hashed `.js` with HTML makes the browser report a syntax error in a script
  that was never there.
- **Pages no longer strand themselves in the middle of a large screen.** Agents,
  Pipeline, Skills, Blocks and Settings were capped at 896–1024px, so a 27"
  display showed a narrow column with a third of the screen blank on each side
  — while the content that wanted the room (a task list, an agent grid, a log of
  tool output) was the content being squeezed. Widths are now chosen from what a
  page holds: collections fill the viewport and turn extra width into more
  columns, forms stay near a readable measure, and the live log's reading column
  grows past `2xl`.
- **A stale team stamp could deny a task write access to its own file.** A
  task's files can change after it is stamped — reconciliation prunes a path, a
  rewrite narrows a reopened task — and the wave's write deny list is derived
  from the stamp. A task stamped `backend-go` whose only remaining file was
  `web/package.json` had every frontend path denied at the tool layer, so its
  single write was refused on its own declared target. The stamp is now
  re-derived from the task's current files immediately before the fence is
  computed. Found by the live 30B end-to-end run.
- **A correction ticket on the seam no longer lands on one half and stalls.**
  Tickets were routed by scanning the board for the first task sharing a
  filename, so one naming `web/src/App.tsx` and `cmd/server/main.go` was stamped
  with whichever half appeared first — and then could not be worked at all,
  because the wave's write deny list is derived from the task's squad and
  refused it at the tool layer the moment it touched the other team's files. The
  ticket sat in `ready_to_dev` for the rest of the run, which reads as a stalled
  harness rather than a misrouted ticket. Ownership decides now, and a straddling
  ticket stays unassigned — the same answer a straddling task already got.
  Found by the live 30B end-to-end run.

---

## v0.22.0 — 2026-08-30

Two virtual dev teams, per-task specialist routing, greenfield scaffolding that
works, and defects that arrive as tickets instead of alarms.

### Breaking behaviour changes

Nothing here changes a flag, a config key or an API shape. What changes is what
a run DOES, and the wall clock is where you will notice it first.

- **Runs stop early less often.** The early-stop guard now also refuses when the
  request names code that is missing from disk and that no task owns. A run that
  used to finish the moment the objective command went green will keep going
  until everything the request named exists — which is the point, because the
  alternative was `✔` over a half-built request. Expect longer runs on requests
  that name several deliverables.
- **More verification actually executes.** Acceptance criteria, self-critique
  and the continue-run reopen filter had been silently skipped for every
  language-routed task, which on a squad run is every task. They now run. That
  is more model calls per task, and more tasks that correctly fail a gate they
  were previously waved through.
- **A verification command that cannot start no longer fails the task.** A
  missing `pytest` or `vitest` reads as UNVERIFIED rather than as broken code,
  so tasks that used to burn their retry budget rewriting correct source now
  finish and say the check could not run. Install the runner to get the check.
- **Frontend work routes to the assembler when one is chosen.** A `.tsx` task
  that previously ran as `react-worker` now runs as `shadcn-worker` or
  `untitledui-worker` where the request or the project selects one. To keep
  hand-written components, say so in the request — see
  [Frontend: assemble or write](frontend.md).

### Added — Squads: parallel virtual dev teams

"Build a Go backend serving a React frontend" is not one stream of work. It is
two, and running it as one fails in two different ways: sequentially the wall
clock is the sum of both halves, and concurrently — with nothing frozen between
them — the frontend invents `GET /todos` returning `{items:[…]}` while the
backend builds `GET /api/todos` returning a bare array. Both halves pass their
own tests and the application is broken.

A **squad** is a domain it owns, its own acceptance command, and a contract it
owes the other teams. It is useless without all three.

- **`manager` specialist** assembles the org chart from the query: who exists,
  what each owns, the interfaces between them, and how the halves are joined. It
  returns one squad for a single-domain query and the run proceeds as a normal
  single stream — squads are an accelerator, never a prerequisite.
- **The contract is frozen to `.slmcode/CONTRACT.md` before any worker starts.**
  Two squads running concurrently cannot ask each other what the seam looks
  like, so the seam has to be a file they both read — and one a human can
  correct between phases.
- **Ownership is enforced, not requested.** Every other squad's paths go on the
  workspace deny list for the wave, so a write outside a worker's lane is
  refused at the tool layer. A prompt saying "do not edit `web/`" is a
  suggestion a stuck model talks itself out of; a deny list is not.
- **Disjoint ownership is validated before anything runs.** Two squads claiming
  one path means one team's edit is silently lost, so an overlap is an error and
  the run falls back to a single stream rather than starting teams that can
  corrupt each other. The check is deliberately conservative: a wrong "these
  overlap" costs one more specific glob, a wrong "these are disjoint" costs an
  edit.
- **Per-task briefs**: each worker gets its own charter, its boundary and the
  interfaces it owes — never the whole contract, which would spend a 30B model's
  attention on the team it is not on.
- **Cross-team management**: per-squad progress between waves, and a named
  cross-team stall when a consumer is blocked on an interface its provider has
  not delivered. That is a contract dependency, not a task defect, and retrying
  the consumer's tasks forever is the wrong response.
- **Integration gate**: once every squad is green, the join command runs. Both
  halves green with the assembled application broken is a failed run — that is
  the whole reason the step exists.
- On by default (`squads: true`). Every failure mode falls back to one stream;
  the only thing it will never do is activate a plan it could not validate.
  Full guide: [Squads](squads.md).

### Added — the frozen contract is checked, not just stated

Each interface is attached to the tasks that owe or consume it as a **blocking
acceptance criterion**, so the seam is judged per task by the reviewer at the
moment the work is done. Without it the contract lived in the prompt and nothing
checked it: a worker that drifted from the spec produced a task the reviewer
approved — it did what its description said — and an integration failure much
later with no obvious owner.

The wording differs by side because the obligations do. A provider must *match*
the spec; a consumer must *call it exactly as stated, whether or not it exists on
disk yet* — failing a consumer because its provider has not finished would
penalize it for being on time. The task's own conditions keep their place at the
head of the criteria list, so no worker is handed a list that is all seam and no
job.

### Added — per-task specialist routing

The composer picks ONE language specialist per run. That is correct for a
single-language repository and wrong for every task on the other side of a mixed
one: in a Go API with a React SPA, a run-level `go-worker` is wrong for
everything under `web/`.

Every task is now staffed from **its own files** after the split, with the
reviewer and tester matched to the same language — a reviewer judging TypeScript
with a Go reviewer's prompt reads the diff for the wrong hazards.

Precedence, each rung earning its place over the one below: a registered
specialist the task already names (somebody chose it on purpose) → a
non-implementer role left alone (a tester task is not a worker task) → **the
language of its own files** → its squad's preferred worker → the run-level
default → the generic worker. Files outrank the squad label for the same reason
`langpick.go` prefers the repository over a word in the query: a file extension
is a fact, a label can be stale.

An agent that is not registered is never named — that fails to dispatch, which is
worse than a slightly less apt specialist doing the work. Every reroute is
reported with its reason, so a surprising choice is auditable rather than
mysterious.

### Added — the proposed plan is editable before you approve it

The gate offered two answers: approve, or replan. Replan throws the whole board
away and pays for another planning pass to fix one wrong file path or one task
on the wrong specialist — so in practice people approved a plan they could see
was slightly wrong and let the run discover it the expensive way.

Edits are the third answer, applied by the harness so what you saw is what runs:

- **tasks** — title, description, role, squad, acceptance, priority, files,
  dependencies; add tasks, remove tasks;
- **teams** — a squad's charter, owned paths, acceptance command and staffing;
  add or remove a squad.

Guarded where a wrong edit would be silent:

- a **role the harness cannot staff** is refused with a reason, because the only
  symptom of one is a task that never starts. The approval card carries the list
  of agents this run can actually dispatch, so the UI offers real choices
  instead of a hardcoded list;
- **removing a task repairs the dependencies that named it** — a dangling
  `depends_on` parks every dependent forever waiting on an id that no longer
  exists, which looks exactly like the harness hanging;
- a **squad edit that breaks disjoint ownership is refused whole and loudly**,
  and leaves the live plan untouched. A half-applied org chart is worse than the
  model's, because the user believes they fixed it. Unrelated task edits made in
  the same pass still apply;
- **omitted fields are untouched, cleared fields are cleared.** A UI sending
  back only what it edited must not blank the rest, and both are expressible.

### Changed — an exhausted task changes hands before it asks a human

When the review ladder ran out of retries the task went to `to_scope` with
"needs human input or smaller scope" and a red intervention event. On a long run
that is the notification you see over and over, and it asks for the one thing
that is hardest to do from a parked task: work out what the agent should have
done differently.

The agent has already had every retry the ladder allows, so re-running it is the
loop that produced those notifications. The task is now handed **once** to a
different specialist — the language corrector, whose entire prompt is "somebody
else's code is failing, fix it" — carrying the reviewer's findings as context
and told not to repeat the attempts already made. It keeps its id, files,
acceptance and squad: this is a change of hands, not new work.

A human is still the last resort, and reassignment declines rather than naming
an agent that cannot be dispatched — a task nobody can staff would sit in
`ready_to_dev` forever. A second handoff is not attempted: a third agent
guessing at work two others could not do is a scoping problem, which is exactly
what the human is being asked to look at.

### Added — a project manager decides who takes a rejected delivery

Routing a correction ticket by language is the right *first* answer and the
wrong *second* one: when the same defect comes back, language routing hands it
to the agent that just failed at it, carrying a ticket whose only new content is
that it failed again.

- **A repeat ticket goes past a project manager** before it goes back to work —
  on the reviewer path when the ladder is spent, and now on the tester path too,
  which is where most gate failures come from. The manager does the two things
  the router cannot: pick somebody else, and say what to do differently. Its
  direction lands *above* the evidence in the ticket, since it is the only thing
  there the last attempt did not already have.
- **A first ticket is never sent to it.** The obvious choice has not been tried
  yet, and a model call to confirm it is pure latency.
- **The roster is ranked and labeled, not alphabetical.** Sorted by name the
  generics come first, so the model took one — even though its own prompt said
  to prefer a specialist. The task's own language now leads the list, each
  entry marked `(Go specialist)` or `(generic)`, and the preference is enforced
  afterwards: a generic pick is upgraded to the language corrector, or its
  worker when no corrector is registered. A specialist pick is never
  overridden — a manager that deliberately reached for another language's
  expert has a reason the file extensions cannot see.
- **The verdict is validated before it is applied.** Naming an unregistered
  agent (the ticket then sits unassigned) and re-picking the agent that just
  failed (the loop this exists to end) both fall through to the deterministic
  route rather than being applied.
- **Each team can carry its own manager.** A run-wide manager answering for a
  specific team picks from a roster it has no reason to understand — the agents
  who can fix a failing Go handler are not the ones staffing the React half. The
  request now names the team and its staff, and lists the team's own people
  first. First, not exclusively: the reason a delivery was rejected may be that
  the team lacks the skill the fix needs.
- **Only agents that answer the triage contract may be nominated.** An agent's
  decoding grammar comes from its own system prompt, so one that answers a
  different contract replies with something the reassignment step cannot read —
  after a full model call has been spent. Both the approval card and the Teams
  page offer only the eligible list.

### Fixed — the run stopped without acting on the manager's decision

The tester path's last act was to re-staff a ticket: raise it, notice the defect
had come back, hand it to a different specialist with guidance the last attempt
did not have — and then finish. The user saw "run complete" over a defect still
on disk and a correctly-assigned ticket nobody had touched, which is worse than
never having triaged at all.

A reassignment is new information nobody has acted on, so it now gets a wave.
Bounded twice: a ticket is re-staffed at most once (a second manager verdict is
a third agent guessing at work two others could not do — a scoping problem, and
what a human is being asked to see), and the corrective-wave budget still
applies. The stream says `resolved after the project manager reassigned it`, or
says once that it is still failing.

### Fixed — one team's defect reopened the other team's finished work

The reopen heuristics are text matches, and text matches leak across teams. The
frozen contract made it worse rather than better: it is attached as acceptance
criteria to *both* halves, so a single clause of shared text was enough for a
backend compile error to reopen the frontend's completed tasks. The frontend
then re-ran, failed at a defect it did not own and could not see, exhausted its
retries and the run ended reporting `frontend 0/1 working` over a half that was
correct and finished.

Ownership now scopes the reopen, the same property the write deny list enforces
one layer down. When every path the tester named is inside one team's territory,
only that team's work — and unassigned work, which has no team to be outside of
— may be reopened. A defect on the seam still reaches both halves, and a run
with no org chart is filtered by nothing.

The lane is computed from the tester's own words rather than the resolved target
list, because that list is widened by the very task matches the check exists to
catch: computing from it would let the contamination declare the defect a
straddle and disable the check exactly when it is needed.

### Fixed — a manifest routed to whatever the run defaulted to

`requirements.txt` is a `.txt` and `Gemfile` has no extension at all, so neither
carried a language signal and both landed on the run's default specialist — in a
mixed repo, a Go worker editing a Python dependency list. Manifests come up
constantly in real builds ("add the dependency", every greenfield scaffold), so
this was not an edge case.

Manifests now route by name: `go.mod`/`go.sum`/`go.work`, `requirements.txt`/
`pyproject.toml`/`setup.py`/`Pipfile`/`conftest.py`/`tox.ini`, `Cargo.toml`,
`Gemfile`, `pom.xml`/`build.gradle`, `composer.json`.

`package.json` and `tsconfig.json` are deliberately excluded. Both are genuinely
ambiguous between a TypeScript and a React lane, and the file-language rung
outranks the squad rung — so claiming them would override the frontend team's
own choice of worker with a guess. Leaving them unmapped lets the better signal
win.

### Fixed — one unassigned task unfenced both teams

`ForeignPatterns` stood down entirely as soon as a wave contained any task with
no squad, so neither team was fenced from the other. That dropped ownership
enforcement far more often than it looks: a task is unassigned whenever it
straddles two teams *and* whenever nothing owns its files at all — a README, a
Makefile, a top-level config. One of those in a wave and the deny list went
away for everybody in it.

An unassigned task's declared files now say which lanes it actually needs. A
seam task naming `web/src/api.ts` opens the frontend's lane and nothing else; a
task naming only `README.md` opens nothing and both teams stay fenced; a third
team nobody named stays fenced regardless. A task that declared *no* files still
stands the fence down completely — declared files are a task's scope, and one
with no scope could write anywhere.

### Fixed — an integration failure raised the worst ticket in the harness

Every squad green and the assembled application broken is the defect the whole
squads design exists to catch. It arrived as the *least* useful ticket the
harness can produce: the failure was routed through the QA path with a synthetic
verdict reading `qa_gate command still failing` / `qa_gate red`, so the
integration command, the output that names the seam, the contract clauses at
stake and the team that owes them were all discarded. What landed was a generic
`worker` task with no files, owned by nobody, whose entire context was that a
gate was red.

It is also on the repair ledger now, so the run summary says `1 defect found,
none resolved` instead of `0 failed` over a broken application, and the Fixes
tab shows the seam rather than nothing at all — that silence is what makes a
user stop trusting both.

It now raises a real correction ticket: the provider owes the clause (a consumer
built against text it was handed), the evidence rides along, and the implicated
files are kept to the owing team's lane — if the output named nothing there, the
ticket ships unscoped rather than pointing at the other half's files. With
several providers the failure text decides; when it names neither a team nor a
path in one's lane, the ticket stays unassigned, because an unassigned ticket
with real evidence beats one parked on the wrong team.

A seam that fails again bumps the existing ticket instead of stacking a second,
and the QA path no longer re-enters the tester rewrite for it — its reopen pass
would have reopened halves that are green by definition, which is exactly what
"every squad passed and the seam is wrong" means.

### Added — the run summary says what it repaired

`Todo app: Go API + React SPA — 2/2 tasks done, 0 failed` read exactly the same
whether the run sailed through or hit two defects and fixed both. The failures
are already loud in the stream, so a summary mentioning none of them reads
either as a swallowed failure or as a summary not worth trusting.

It now ends `· 1 defect found and fixed`, or `· 1 of 2 defects fixed, 1 still
open` when something is still owed to a person. The count rides on the run
result as `repairs` (found / resolved / restaffed / needs_human) and the Studio's
result panel says it in words: *Fixed the 1 defect without you · 1 reassigned by
the project manager*.

A defect that comes back is one defect with another attempt, folded by the same
rule the Fixes tab uses — the summary and the panel must never disagree about
how many things went wrong.

### Fixed — `PUT /api/config` could set keys the schema marks read-only

A config patch carries an untyped half for keys without a dedicated field, and
that half went straight to the low-level setter with no patchability check. So
three surfaces disagreed about the same key: `slmcode config set` refused it,
the Studio's settings form never offered it, and a `PUT /api/config` carrying it
set it anyway.

Three keys were reachable that way. `mcp_servers` is the one that matters: MCP
servers are external processes whose tools agents can call, so a request able to
register one is tool-execution surface — and the Studio panel listing them says
*"configured in config.yaml"*, because file-only is exactly what the read-only
flag was declaring. `skills_dirs` loads prompt content into agents;
`context_role_budget` is benign.

The check goes on the patch path rather than on `Config.Set`, which must keep
accepting these — it is the setter env-var and config-file loading both use, and
a read-only key still has to be loadable from the file that declares it. The
flag is a statement about *remote* editing, so that is where it now holds.

Found by walking every schema key through `Set`/`Get` and `ApplyPatch`. Nothing
had checked that the schema and the code agreed about which keys are writable.

### Fixed — a language-specialised tester was given the wrong finish contract

Per-task routing puts `go-tester` / `python-tester` on a verification task
whenever a language pack is active, which is most runs. Everything recognizing a
tester by exact id then stopped recognizing it, across eight call sites — and
the finish contract was the one that hurt: a tester handed the **worker**
contract answers `{"status":"done","files_changed":[]}` while the gate parses
for `{"passed":…,"failures":[]}`, so a passing verification read as a malformed
one and the run rewrote a plan that was fine.

The other seven mattered too. The tester task was never reopened after a failure
(so the run never re-verified), tester budget caps did not apply, and the
shared-task brief ranked its output as an ordinary worker's.

This is the third time the same bug has appeared — `isImplementerRole` and
`looksImplementer` were the first two — so `plan.IsTesterRole` joins
`plan.IsImplementerRole` as the shared, suffix-aware predicate, and no exact-id
tester check remains.

### Changed — the worker prompt stops presenting bookkeeping as human instruction

`BuildWorkerPrompt` rendered a task's Notes under the heading **Human notes**.
On a correction ticket that block reads:

```text
correction ticket from the tester gate; assigned to go-worker
correction-key: tester|handler returns 500|internal/http/todo.go
correction-attempt: 2
query scope run-1787…
```

A human wrote none of it. A 30B model told these are *human* notes treats them
as the highest-authority text in its pack and spends attention parsing a dedupe
key — while what those markers stand for (this is a repeat; somebody else had
it) is already stated properly, in prose, in the ticket body.

The bookkeeping lines are dropped, harness prose that genuinely tells an agent
something is kept, and the heading is now just **Notes** — because most of what
lands there is written by the harness, and claiming otherwise hands it an
authority it should not have.

### Fixed — invalid UTF-8 in a model answer poisoned the next prompt

Five parsers echo raw model output into their result whenever JSON parsing
fails. That fallback is deliberate — it is what keeps a malformed answer useful
— but it means the model's own bytes reach the *next* prompt, and a stray
invalid sequence there is not cosmetic: providers reject invalid UTF-8 outright,
turning one bad answer into a failed request the user cannot explain, and where
it is accepted it tokenizes into replacement-character byte fallbacks, which is
exactly the waste `pkg/context/textutil` exists to prevent.

`textutil.Sanitize` drops invalid sequences (rather than replacing them with
U+FFFD, which is a visible artifact a small model will try to reason about), and
the five parsers sanitize their input once, which covers every path out of them.

The repair layer sanitizes too, and it is the more consequential of the two:
`RepairToolArgs` output becomes a **tool call's arguments**, so an invalid byte
reaches a tool that writes it to disk rather than merely a prompt. Repair is the
last thing between a bad answer and a usable one, which makes it the right place
to clean the bytes rather than the tenth.

Found by a hostile-input sweep over every model-output parser: truncated JSON,
prose-wrapped JSON, right-shape-wrong-types, another contract's answer, 200-deep
nesting, 100KB strings and invalid encodings. None of them panicked, which is
the property that most needed proving — the input is the one thing the harness
does not control, and a parser panic takes the run down.

### Fixed — a data race in the event path, with a nil dereference inside it

`emitFullDataL` read `o.currentTurn` without the lock while `Run` wrote it under
one. The race detector found it on the e2e suite (which `make check` does not
cover — it races `pkg/...` only), and it is worse than a reported race: `Run`
nils that field in a `defer`, so the write could land between the `!= nil` check
and the `.ID` dereference on the very next line. That is a panic in the one code
path that must never take the process down, reachable whenever a background
probe emits while a run is finishing.

Every read now goes through one locked accessor — including the ones that are
provably single-goroutine today, because the field having exactly one obvious
way to read it is what stops the next one being written unlocked.

`make race-e2e` closes the gap that let it survive: a full run has parallel
workers and background probes emitting while the run goroutine rewrites session
state, and no `pkg/...` test starts one. CI runs it alongside `make race`.

### Fixed — a scheme-less endpoint broke everything that built a URL from it

`endpoint: 127.0.0.1:1234/v1` is a spelling config files genuinely carry, and
`pkg/config` already tolerated it when deciding whether an endpoint is local.
Everything that BUILT a URL from it did not: `net/url` refuses
`127.0.0.1:1234/v1/models` with *first path segment in URL cannot contain
colon*.

That reached further than it sounds. The provider registration is the path
**every model call** goes through, so a config that reads as perfectly correct
produced a harness where nothing worked — and the endpoint probe, the model
catalog and auto-configuration each reported a perfectly reachable server as
broken, which sent auto-configuration walking past it to something else.

One `config.NormalizeEndpoint` now serves all of them.

### Fixed — an Ollama endpoint ending `/v1/` kept its suffix

`TrimSuffix(ep, "/v1")` does not match a string ending in `/v1/`, and the
trailing slash was trimmed *after* it — so `http://127.0.0.1:11434/v1/` shaped
to `…/v1` and every Ollama call went to `/v1/api/tags`. The two trims are the
other way round now, and the shaping for both providers is one tested function
each rather than two similar-looking inline blocks that had drifted apart.

### Added — `slmcode configure`, on both surfaces

Every piece of this existed and none of it was joined up. The harness could list
an endpoint's models, measure what a (model, endpoint) pair can do, probe
decoding support and check whether the configured endpoint was answering — but
nothing could answer the question a new user actually has, which is *what do I
put in the config*. They got a default endpoint that may not be running, a
default model that may not be served, and a refusal at their first run.

`slmcode configure` probes the configured endpoint first, then the addresses
local model servers listen on (oMLX, Ollama, LM Studio, vLLM), then any hosted
provider whose API key is already set. Candidates are probed concurrently, so a
machine with nothing running answers in a couple of seconds rather than waiting
out each address in turn. In the Studio the same thing is the **Find my model
server** panel in Settings, split the same way: *Look around* changes nothing,
*Configure for me* writes.

The model is chosen by ruling out what cannot do the job before preferring what
can. A server serves whatever it was given and the list is rarely all chat
models; picking an embedding or speech model by accident produces a failure that
is baffling rather than obvious — the harness runs, the model answers, and
nothing it says is JSON. Among what survives, coder-tuned beats
instruction-tuned beats bigger, `30B-A3B` is read as a 30B model with 3B active,
and matching is on whole name segments so `codestral` is not ruled out for
containing `tts`.

Three things it will not do: replace a working configuration (yours is probed
first and kept if it answers), send your API key to a local port that merely
might be a model server, or second-guess an explicit `--endpoint`.

A failed pass distinguishes the three real problems — nothing listening, a
server with no models loaded, and a server whose models cannot write code —
because they have different fixes. And it is now the remedy named when a run
refuses to start because the endpoint is down or the configured model is not
served, which is where somebody actually needs it.

`slmcode init` runs it for you. It used to end a first run with "no model server
answered" and a pointer at `slmcode doctor` — two more commands for somebody who
has just scaffolded a workspace, when a server is often running on a port that
is simply not the default. It now looks around and adopts what it finds. Only
when the configured endpoint did not answer, so it can never move a working
setup, and never when you pinned `--endpoint` or `SLMCODE_ENDPOINT`.

### Added — correction tickets are legible on the board

A correction ticket looked exactly like planned work: same card, same badges, a
role and a title. It is not the same thing — it is a defect a gate found, with a
reproduction and an owner, possibly on its second attempt and possibly moved
there by the project manager because the first specialist could not fix it.
Reading the whole description to work that out is what makes a board feel like a
log rather than a plan.

Cards now carry a `fix` badge (with `attempt N` on a repeat) and, when a manager
moved it, the specialist it went to.

### Added — a Fixes tab: what the harness repaired by itself

The harness recovers from most of what goes wrong, and none of it was visible.
The failures are red and loud — `tester found 1 failure`, `T2 reassigned after
its retries were spent` — and the recovery was four plain lines somewhere in a
log of fifty. A user watching that sees a run going wrong with no evidence
anything is handling it, which is the worst possible reading of a system that is
in fact fixing itself.

The Live rail's new **Fixes** tab is one row per defect: what the gate found,
which specialist has it now, the steps taken, and whether it closed. A defect
that comes back is the *same* row with another attempt, not a second problem —
splitting them would make a run look twice as broken as it is. The tab carries a
count, so a user who never opens it still sees that something was found and
something was done about it, and the one state that stands out is the one where
the harness has run out of moves and a person has to look.

### Added — a Teams page

The rail's Teams tab answers "how are the teams doing right now". The new
**Teams** page answers "are the teams right at all" — a different question, at a
different time, with a consequence the rail does not have: a team structure
outlives the run that proposed it, so the boundaries and staffing edited here
are what the next run inherits.

It edits names, owned globs, acceptance commands and each team's project
manager, sends only the fields you touched, and — when an edit would make two
teams share a path — shows *which* teams collide instead of a bare refusal.
Nothing is saved in that case: a half-applied org chart is worse than the one it
replaced, because you believe you fixed it.

### Fixed — reopened tasks on a squad run were never made into tickets

`looksImplementer` matched only the bare role ids `worker`, `corrector` and
`deep`. Per-task specialist routing puts `go-worker` on the task, so on any run
with language packs — which is every squad run — a reopened task failed that
check and was never enriched into a correction ticket. It came back with
`Review: "tester feedback: <one sentence>"` and nothing else: no command to
reproduce the failure, no output, no implicated files. That is exactly what
turns corrections into retries.

It is the third copy of the same predicate and the second time it has had this
bug, so there is now one exported `plan.IsImplementerRole` and the copies are
gone.

### Fixed — a correction attempt counted the board, not the defect

A ticket's "correction attempt N" came from a count of every correction on the
board, so two unrelated failures made a first attempt at a third defect announce
itself as a third — telling its worker that approaches it had never tried were
already ruled out. It now counts tickets for *that* defect, finished ones
included.

Reading a ticket's dedupe key back is also trim-safe now. The marker carries a
leading newline so it cannot match mid-line, but a stamp that lands at the start
of an empty notes field loses that newline to any caller that trims — after
which dedupe stops seeing the ticket and the board grows a second one for the
same failure.

### Fixed — greenfield scaffolding could not scope its own tasks

Found by running the squad path end to end against a fake model, and it is the
bug that made the headline query produce nothing.

`ReconcileFiles` refuses a claimed target that does not exist on disk, falling
back to discovered files — the guard that stops a hallucinated path from
becoming a twelve-file write allowlist. New files were allowed only under a
fixed prefix list: `src/`, `tests/`, `test/`, `lib/`, `app/`, plus a handful of
root manifests.

"Build a Go backend serving a React frontend" targets `cmd/server/main.go` and
`web/src/App.tsx` — the conventional layouts for exactly that request, and
neither is under any of those prefixes. **Every task the splitter wrote was
parked as `to_scope` with "no resolvable target files".** Nothing was built, no
squad could be assigned (assignment is by file ownership), and the failure read
as the splitter being wrong rather than the guard being too narrow.

The discriminator is now the repository's state rather than the path's prefix: a
workspace with no source code in it has nothing to reconcile against, so a
claimed path that looks like a real file — a real source extension, no
placeholder marker, no traversal, sane depth — is the scope. Once a repository
has code, the conservative behavior is unchanged, because there a claimed path
that does not exist really is more likely invented. Manifests and docs do not
make a repository established: `go.mod` and a README describe a project about to
be written, not a layout to reconcile against.

### Changed — defects arrive as correction tickets, not alarms

A failing tester used to produce a red notification and a generic
"Fix tester failures" task assigned to `worker`, with the failure lines pasted
into its description. Three things were wrong with that, and all three are why
the failures felt like noise rather than progress:

- **The generic role.** A TypeScript compile error handed to the plain worker
  gets a plain worker's guess. Tickets now route to the specialist whose
  language actually broke — `go-worker`, `react-worker`, `python-worker` — by
  majority of the implicated files, falling back to the generic worker when that
  specialist is not registered (naming an agent that does not exist fails to
  dispatch, which is worse). A task already held by a specialist is never
  re-routed: that choice was made deliberately by the composer, the manager or a
  human.
- **The missing evidence.** "test failed" is not a bug report. A ticket now
  carries what broke, the command that found it, the tail of that command's
  output (a runner prints the failing assertion last), the implicated files, and
  an acceptance that names the command rather than saying "the tester passes".
- **The noise.** A reopened task IS the correction ticket, so it gets the same
  treatment instead of a one-line verdict; repeat corrections say which attempt
  they are and not to repeat the last one; and the same unresolved defect
  reopens its existing ticket instead of stacking a new one every gate run,
  which is what made the board look like it was losing ground.
### Fixed — found by running v0.21.0 on a live 30B

Everything in this subsection was found by driving the harness against a real
local stack — oMLX serving Qwen3-Coder-30B — rather than by reading the code.
Each item names the measurement that produced it.

### Fixed — the harness

- **Executable acceptance criteria were discarded before they could run.**
  `collapseToWorker` rebuilt the surviving task without `Criteria`, so whenever
  the splitter's tasks collapsed — the common shape on a small model — every
  criterion the model authored was dropped. Measured: the 30B emitted correct
  criteria with `go test ./...` as the verify command, and the run reached the
  reviewer with nothing to check, indistinguishable from a task that passed.
- **Harness state followed the sandbox under `--isolate worktree`.** Memory,
  the derived graph and the metrics row take a project root and join
  `<root>/.slmcode/…` themselves, so they never saw `StateDir`. The isolated
  run wrote them into the throwaway worktree: an orphan directory survived every
  run (a late write re-created the directory git had just removed), and
  `git add -A` swept nine `.slmcode/**` files into the commit merged onto the
  operator's branch. New `Config.StateRoot()`; the isolated commit now carries
  only source.
- **A green test command that ran no tests counted as verification.**
  `classifySmoke` checked `sr.OK` before the no-tests check, and `go test ./...`
  on a tree with no `_test.go` files exits **zero** — so the gate's own
  documented contract ("a 'nothing to run' exit is NoTests, never Green") held
  only for runners that fail on empty. Measured: a run told "both parts must
  have tests" wrote none and finished ✔ with `qa_gate green`.
- **The board could deadlock on an abandoned task.** `runWave` is synchronous,
  so nothing is in flight at the top of the scheduler loop — yet a task left in
  `in_progress`/`in_review` still made `AgentWorkRemaining` report work in
  progress, and no path re-dispatches those columns. Measured: one stranded
  task, four dependents frozen behind it, ~9 minutes of correct work discarded
  and reported as a failure with the edit already on disk and compiling.
  Orphans are now re-queued, bounded by the existing attempt ceiling.
- **The composer could drop the coordinator.** `applyBudgetClass` is applied to
  the heuristic composition only, so the class could budget `coord` and the
  composer silently omit it — 3 live runs out of 3. Coordination is restored
  because @coordinator acts on the *board*, which does not exist yet when the
  composer decides.
- **Per-file specialist routing and the model ladder were both dead by
  default.** `runner.HasRole` was only wired inside the escalation branch, so
  on any install without a `model_escalation` ladder — nearly all of them —
  every feature that asks "is this agent registered?" answered no.
- **A task's role never reached its language specialist.** `normalizeExecRole`
  applied `execute.default_role` only to an *empty* role, but `Task.Normalize`
  fills empty roles with `worker` first, so the chosen specialist was displayed
  in the composition and never used. Tasks now route to the specialist that owns
  their files, which is also what makes a mixed Go + React board run on two
  specialists at once.
- **Duplicate workers on one file.** Two tasks scoped to the same file cannot
  share a wave (concurrent workers share one tree), so each cost a full wave and
  the second opened a file the first had rewritten. Same-file worker tasks now
  merge, with dependencies rewritten onto the survivor.
- **Greenfield Go and web paths were dropped.** `isGreenfieldCreatePath`
  accepted `main.py` but not `main.go`, and knew no `cmd/`, `pkg/` or `web/`.
  Measured: a full-stack request finished **0 of 7 tasks with nothing on disk**,
  because every planned path was discarded and a worker forbidden to invent
  paths had nothing it was allowed to write. After the fix the same request
  produced a compiling Go backend and a React frontend.
- **Two prompt few-shot examples were being copied as real work.** The splitter
  planned "add Sum to calc.go" — verbatim from its own example — on a greenfield
  request, and the composer's handoff carried `verify with go test ./...` into a
  React run for the same reason. Both examples are now shapes, not content.
- **A refusal never said what it refused.** `"npx" can execute arbitrary code`
  is unanswerable when npx runs a different program every invocation; refusals
  now quote the command.
- **`slmcode agent list` showed 20 of 59 agents.** Block-defined agents — every
  language specialist and both frontend assemblers — were registered by the
  orchestrator and invisible to the command that lists the roster.

- **`npx tsc --noEmit` was refused.** The bare `tsc` and `eslint` were already
  builtin-safe, but their npx forms were not — and in a JS/TS project those
  tools live in `node_modules`, never on PATH, so the npx spelling is the only
  one that runs. It is the react and shadcn packs' own typecheck and `qa_gate`.
  Measured: a live assembler run was refused for its own smoke command.
- **A whitelisted prefix matched past a word boundary.** `npx tsc` also admitted
  `npx tsc-evil` — any package whose name merely starts the same way — because
  matching is a plain prefix test. The builtin list already carries trailing
  spaces (`tsc `, `eslint `, `find `) for exactly this; the npx entries now do
  too, and the same hole is closed in the `react` and `typescript` quality
  packs.
- **A forged criteria header could switch the criteria gate off.** A worker that
  merely echoed `## Acceptance criteria` — plausible, since the reviewer
  contract in its own prompt names that heading — suppressed the review-time
  gate, and with nothing run `CriteriaUnverifiedInOutput` stayed false, which is
  the value that ALLOWS the reviewer fast path. The section's provenance stamp
  is now required, so a genuine section is still recognized (and its commands
  still not re-run) while a typed one is not.
- **`model_roles` and `model_escalation` were invisible to `doctor`.** It
  printed "agents inherit stack/global LLM" unconditionally — false as soon as a
  role is pinned — and validated nothing, so a typo'd model passed at 100/100
  and failed mid-run at the reviewer. Both are now shown, every model they name
  is checked against what the endpoint serves, and an unserved one is a
  readiness finding (measured: 100 → 86) rather than a surprise.
- **Criteria that could never run.** Told only in the abstract to write a
  runnable verify command, a live 30B wrote
  `go test -v ./... | grep -E '(TestX|PASS|FAIL)'`; the sanitizer refuses shell
  metacharacters, so every criterion on that board came back UNVERIFIED. The
  splitter now gets the concrete ✓/✗ example, and its own prompt budget — it is
  paid once per split, like the composer is paid once per run.

- **Correction rounds aimed at the wrong files.** When the QA gate stayed red,
  the synthesized tester verdict carried the literal string `"qa_gate red"` —
  and the corrective rewrite scopes its fix by mining file paths out of those
  failure strings, so a verdict naming no file fell back to "whatever finished
  most recently". Measured on a greenfield Go build: three correction rounds
  scoped to `pkg/tasks` while every compiler error was in `cmd/server/main.go`,
  which none of them was allowed to touch. The gate's own output is now carried
  through, so the fix is scoped at the file that is actually broken.
- **Workers wrote against types they had never read.** Rule 1 said "ws_read a
  file before editing it" — nothing about *using* one. Measured: a worker
  scoped to `cmd/server/main.go` used `task.Title`, `task.CreatedAt` and
  `task.UpdatedAt` on a `Task` that a sibling task had defined with three other
  fields, and the package would not compile. Reads were never scope-limited;
  the worker simply had no instruction to use them for cross-file APIs.

### Added — frontend assemblers

- **`shadcn` and `untitledui` packs: build React UI by INSTALLING components.**
  Hand-writing a dialog with focus traps is where a 7–32B model spends its
  runway; installing a reviewed one and wiring it up is imports, props and
  layout. Both ship enabled — nothing to install, nothing to apply — with a
  worker, an assembly reviewer that rejects a component you hand-rolled when the
  registry already had it, a pipeline and quality gates.
- **The method is chosen from evidence and announced.** Your request wins first
  (name a library, or say *from scratch*), then the project's own markers, then
  greenfield defaults to assembling; an existing app with no markers keeps
  writing by hand. The run prints which and why.
- **Scoped shell access.** `npx` stays an executor; five subcommands of two
  named packages are allowed, matched structurally so `npx shadcn add`,
  `npx --yes shadcn@latest add` and the legacy `shadcn-ui` name all work. An
  `add` naming a URL, an `@registry`, or a local path is refused — both CLIs
  accept those in the same position as a component name, and they resolve to a
  registry nobody reviewed.
- The assembler agents carry each CLI's real contract, verified by running them:
  shadcn's `init` needs an explicit `-b` or it stops on an interactive menu even
  with `-y`; Untitled UI matches names fuzzily and wrongly (`buttons` installs
  `app-store-buttons`, and it does not error).
- See [Frontend: assemble or write](frontend.md).

### Fixed — a run that was not doing the work still looked like one that was

A second live series against the same stack, after the fixes above were in. The
theme is narrower than "bugs": in every case the harness's own report was the
part that was wrong, and every objective signal it held was true.

- **Per-task routing switched verification off.** Once a task is staffed from
  its own files, a Go task arrives as `go-worker` — and four private copies of
  "is this an implementer" compared the id for EQUALITY, so each answered no and
  quietly did not run. On a squad run that is every task. It took out the
  acceptance-criteria gate, worker self-critique, the continue-run reopen filter
  and the placeholder-gap annotator. Measured: a board whose criteria were
  emitted correctly, with runnable bare verify commands, and never verified
  once. All four now call `plan.IsImplementerRole`, whose own doc comment had
  already warned that every private copy has had this bug.
- **The claims gate read the harness's own text as the model's.** `runGates`
  appends stamped evidence to `Task.Output`, and `CheckClaimedFiles` re-parsed
  that same string — whose loose parser matches any quoted `*.go` token
  anywhere in it. Measured, on a task that had done nothing wrong: the worker
  wrote `cmd/server/main.go`, `go vet ./cmd/server` passed on it, the worker
  truthfully reported `files_changed ["cmd/server/main.go"]`, and the gate
  flagged a hallucinated path — the reviewer rejected on that evidence and the
  task escalated to `to_scope` twice before being reported as needing a human.
  The gate now reads `StripHarnessSections`, which six other callers already
  used for this; that helper also learned to cut at the genuine provenance
  stamp rather than only at a registered header, so a section nobody remembered
  to register still ends the model's region. Never the stamp regex — a model
  able to end that region by writing its own stamp could hide the rest of its
  output from every gate that reads it.
- **A missing test runner was scored as broken code.** `RunSmoke` reports
  Ran/OK, so a non-zero exit is a non-zero exit: `python -m pytest -q` on a
  machine with no pytest scored exactly like a suite that ran and found a bug.
  Measured: the worker wrote the file it was asked for, the smoke failed with
  "No module named pytest", the reviewer rejected, the corrector rewrote correct
  code, the smoke failed identically, and the task escalated — a whole retry
  budget spent fixing a missing dependency by editing source. New
  `quality.ToolingMissing`, narrow on purpose: only the command's OWN entrypoint
  counts, so "No module named pytest" for `python -m pytest` is absent tooling
  while "No module named myapp" from that same command is the code under test
  failing to import, which is the exact fault the check exists to find. `npm run
  test` names a package.json SCRIPT and is not treated as a program either. The
  tool name must appear as a whole token, which its own test caught:
  `load.go:8: … no such file or directory` contains "go" beside a launcher
  error, and a genuine missing-fixture failure would have been excused as an
  absent Go toolchain.
- **An escalated role stopped matching the predicates that gate it.**
  `IsTesterRole` and `IsImplementerRole` compared roles that still carried their
  rung, so `go-worker@esc2` was not a worker and `go-tester@esc1` was not a
  tester — the harness was wrong for exactly the tasks that had had the most
  trouble. The strip moved INTO the predicates and `EscalationSuffix` moved down
  to `pkg/plan` with them (`agents.EscalationSuffix` aliases it). Four more
  exact `!= RoleTester` comparisons went through the predicate at the same
  time, each a gate testers are deliberately exempt from.
- **The chosen frontend assembler was announced and never dispatched to.**
  `specializeExecRole` upgraded a task to the shadcn/Untitled UI assembler only
  when the role was the GENERIC worker — but per-task routing staffs a `.tsx`
  task as `react-worker` first, so the guard skipped it. Measured on a live
  shadcn run: `· init frontend: shadcn-worker — the request named shadcn/ui`,
  then `▸ @react-worker …`. The run still installed the component, because the
  query said to and the CLI is allowed — so the failure was invisible from the
  outcome, and a request that did not spell out "install it" would have got
  hand-written components. The assembler now also supersedes the frontend
  specialist the board already carries.
- **`✔` over a half-built request when the board dropped the task.** The
  early-stop guard blocked only for a path an unfinished task OWNED, which
  misses the case that costs the most: the board never planning a named
  deliverable, or planning it and dropping it. Measured on a request naming a Go
  server, a Go store, Go tests AND `web/src/TaskList.tsx`:
  `✔ … 2/2 tasks done, 0 failed (objective met between waves — 1 task(s) not
  executed)`, three files changed, all Go. `go build` and `go test` were
  genuinely green over the Go half; the React half did not exist and its task
  was no longer on the board. The guard now also refuses when the request names
  CODE that is on no task's file list and not on disk — code specifically, so
  "the behavior is described in `docs/design.md`" still names a document as
  input and does not block.
- **Work queued behind a human was reported as a failed run**, and **the run
  headline could be raw planner JSON** — two more cases of a correct run
  reporting itself wrongly.
- **`.slmcode/scratch/TODO.md` rode into an isolated run's commit.**
  `persistTodos` derived its path from `Root`, so under `--isolate worktree` the
  checklist landed in the throwaway checkout where `git add -A` swept it into
  the commit merged back. Keyed on `SlmDir` now, which the workspace already
  carried — the same bug class as the memory, graph and metrics directories, in
  a subsystem that had not been taught it yet.

### Added — `test/live/sweep.sh`, the proof `go test` cannot give

The unit suite and the fakemodel e2e tests prove the harness's logic. Every
defect in the two subsections above was invisible to `make check` and obvious
within one live run, so the sweep that found them is now in the tree instead of
dying with a scratch directory.

Six scenarios — bug fix with scope respected and criteria verified, worktree
isolation, greenfield, multi-language squads, and both frontend assemblers —
each building its own fixture, so there is nothing to set up. **Every assertion
is an objective outcome**: a file on disk, a compiler's exit code, git state.
The harness's own success line is never the assertion, because in every defect
above the self-report was the thing that was wrong.

Deliberately not part of `make check`: it needs a configured provider, takes
tens of minutes on a local model, and on a loaded machine runs time out in ways
that look like defects and are not — measured, at load 42 a scenario that
passes in minutes stalled for 40 with seven timeouts. See
[CONTRIBUTING](https://github.com/UnicoLab/smlcode/blob/main/CONTRIBUTING.md).

## v0.21.0 — 2026-08-27

Adapts the structural ideas from [zeroshot](https://github.com/the-open-engine/zeroshot)'s
executor–verifier orchestration, rewritten for this harness's thesis: small
local models, a fixed runway, gates that fail closed. Its central claim — an
independent LLM verifier — is deliberately not adopted, because our gates ask
the filesystem, which is stronger evidence than a second opinion from the same
weak model. What was missing was everything around that verifier.

**Carries v0.20.0 as well.** That version was prepared but never tagged, so its
offline-install channel and Live-page work ship here for the first time; its
entry below still describes them.

### Added

- **Executable acceptance criteria.** `Task.Criteria` splits acceptance into
  individually checkable conditions, each with the exact command that proves it,
  run through the same whitelist every auto-run command already passes. The
  reviewer gets three verdicts, never two: `PASSED`, `FAILED`, and
  **`UNVERIFIED`** — nothing ran. That third state is the point. A prose
  acceptance blob is scanned by regex, and a condition it finds no command for
  is invisible, so "the harness did not check" silently becomes "the harness
  says it is fine". `UNVERIFIED` never blocks, but it denies the reviewer fast
  path: disk evidence proves the worker *changed* something, never that the
  stated condition is now true.
- **Budget classes.** The composer now decides what a request is *worth*, not
  just what shape it has: complexity (`trivial`/`simple`/`standard`/`critical`)
  × kind (`inquiry`/`task`/`debug`) picks the optional phase breadth, wave
  budget, think passes and gate depth. A deterministic classifier answers the
  easy majority for free, and `trivial`/`inquiry` skip the composer LLM
  entirely. Reviewer *count* is deliberately never scaled — our reviewers are
  LLM calls and our gates are not, so a higher class buys more determinism, not
  more voices. See `slmcode compose "…"`.
- **Per-role model routing and a failure ladder.** `model_roles` pins roles to
  models by name; `model_escalation` steps a repeatedly-failing task up to a
  bigger model, using the attempt ledger the board was already keeping and
  nothing read back. A rung is a separately registered agent, so a task that
  never escalates pays nothing.
- **Worktree isolation** (`slmcode run --isolate worktree`). Runs against a
  throwaway `git worktree`; your checkout is never written to mid-run, and an
  abandoned run is deleted in one operation rather than restored file by file.
  Harness state stays in your checkout's `.slmcode/`, so memory and learned
  policy keep accumulating. Opt-in; in-place remains the default.
- **Issue intake and pull-request delivery.** `--issue <url|owner/repo#N|N>`
  takes the query from a GitHub issue; `--deliver pr` opens a pull request when
  the run succeeds, always asking first and showing the exact file list. Issue
  bodies are untrusted text: framed as a report rather than pasted as
  instructions, with harness markers defused so they cannot forge gate evidence.

### Fixed

- **The composer could contradict itself about language.** A query mentioning
  Python in a Go repository assembled `python-worker` and `python-tester` while
  the same handoff contract said "Detected project language: Go" and "verify
  with `go test`". The repository now wins unless the workspace inventory
  actually corroborates the query — which preserves the legitimate case, the
  `web/` tree inside a Go project.
- **A start-then-configure race in the CLI hotkey test**, which failed under
  `-race` on loaded runners.

### Studio

- The Live page is rebuilt around the stream it exists to show: four fixed
  zones, a single phase rail replacing four header panels, and the run setup
  behind a disclosure that is closed while running. The pipeline shown while you
  type is now labeled a **guess** — it is assembled from the query text alone,
  and the composer decides the real one when the run starts.
- The navigation rail collapses to icons below `lg`, where it was taking 60% of
  a phone screen.

### Dependencies

- React 19, Vite 8, Vitest 4, and the GitHub Actions majors. **CI now needs
  Node 22** — Vitest 4 pulls a jsdom whose undici requires it.
- TypeScript 7 and ESLint 10 are *not* adopted: `typescript-eslint` caps
  TypeScript below 6.1, and `eslint-plugin-jsx-a11y` has no ESLint 10 support.
  Taking either would mean shipping a tree whose linting is silently broken.
- `eslint-plugin-react-hooks` v7's new React Compiler rules are off for now.
  They flag 42 existing patterns — not regressions — and adopting them is a
  refactor that deserves its own review.

## v0.20.0 — 2026-08-26

An install path for machines where every existing one is blocked, and a Live
page that fits the screen it is on.

On a locked-down corporate workstation all three installers fail, each with a
`403` that reads like a bug rather than a policy: `brew` has to update itself
first, `scripts/install.sh` needs a Go toolchain and the module proxy, and
`scripts/install-remote.sh` needs `api.github.com` plus a release-asset fetch
from `objects.githubusercontent.com`. `git clone` typically survives that
filtering, because the git endpoint is allowlisted for work that has to get
done. So the clone becomes the delivery mechanism.

### Added

- **`prebuilt/` — the macOS binaries live in the repository.** `darwin/arm64`
  and `darwin/amd64`, gzipped, one version at a time. `prebuilt/SHA256SUMS`
  holds digests of the *uncompressed* binaries under their release asset names,
  so its lines are byte-identical to the published release `SHA256SUMS` and can
  be diffed against them from any unfiltered machine.
- **`scripts/install-offline.sh`** — installs one of those binaries with **no
  network call of any kind**. It detects OS/arch, decompresses, verifies the
  SHA-256 (a mismatch aborts), clears `com.apple.quarantine` on its own staged
  copy so Gatekeeper does not block it, smoke-tests the binary *before*
  installing it, then does the same atomic install, `install.json` and
  completions the other installers do. `--system` calls `brew --prefix` only to
  locate a directory — it never runs `brew update`, which is the command that
  403s. Also `--list`, `--add-to-path`, `--arch amd64` for Rosetta, `--binary`
  for sneakernet, `--uninstall`.
- **[Offline install guide](install-offline.md)**, including why each of the
  other three paths fails and how to verify what you got.
- **`make prebuilt`** cross-compiles and rewrites `prebuilt/`. It refuses to run
  when the Studio SPA is not embedded, so a contributor without Node cannot
  commit binaries that serve the placeholder page — the one failure mode of this
  channel nobody would notice until they ran `slmcode studio`.

### Studio — the Live page

- **The layout adapts to the window instead of assuming one.** Every panel above
  the event log used to be `shrink-0` in a fixed flex column, so on a laptop
  viewport the header consumed the screen and the log — the thing the page
  exists to show — was clipped to a few pixels with no way to get the room back.
  Now:
    - **Pipeline, Current stage, Agents, Feedback and Composition are
      collapsible**, individually and all at once, and each choice is
      remembered across reloads. Collapsing the detail region roughly doubles
      the log (measured: 272px → 605px at 1440×900).
    - **The detail region is capped at ~38vh and scrolls itself**, so no
      combination of expanded panels can starve the log.
    - **Metrics, phase chips and stage facts use `auto-fit` grids**, so they
      reflow to the space actually available rather than stepping at fixed
      breakpoints. Verified with no horizontal overflow from 900px to 2560px.
    - **The side panel is draggable and can be hidden.** It was a hard
      `lg:w-[27rem]` — a third of a 1280px window, with no way to give it back.
      Width is persisted and clamped so a window narrowed later can never leave
      the log with no room.
    - The composition, active-agent and token-stream panels are height-capped
      and scroll internally instead of pushing the log off the bottom.

- **The page no longer flickers during a run.** Four independent causes, all
  fixed:
    - The `AppContext` value was rebuilt on every render, so every consumer in
      the app re-rendered whenever anything changed. It is memoised.
    - Events and token deltas published to React synchronously, once per
      message — tens of full-app renders a second. They are now coalesced into
      one flush per animation frame, through a single scheduler.
    - The log auto-scrolled with `scrollIntoView({ behavior: 'smooth' })`, which
      scrolls every ancestor — including the page's `<main>` — and restarts its
      animation on each call. Events arrived faster than the animation finished,
      so the scroll never settled. It now writes the log element's own
      `scrollTop`, and only while the user is already at the bottom, so
      scrolling up to read something is no longer undone by the next event.
    - The log was re-serialised to `sessionStorage` on every event. That write
      is debounced, and flushed on `pagehide`/`visibilitychange` so nothing is
      lost.

### Changed

- **The release workflow keeps `prebuilt/` current.** `scripts/update-prebuilt.sh`
  runs against the just-published `dist/` in the same commit that syncs the
  Homebrew formula, so the committed binaries *are* the release binaries, not a
  rebuild.
- **Release notes no longer republish v0.19.0's breaking changes as their own.**
  That block was hardcoded into `release.yml`, so every release after v0.19.0
  would have announced "five defaults changed in this release" about changes
  that were not in it. It is now a standing upgrade pointer; what is
  per-release is generated from the commits and written here.
- `make clean` removes `dist/` and no longer describes `prebuilt/` as something
  it would delete — that directory is tracked source, not build output.

### Notes

`slmcode update` downloads a release asset, which is exactly what a blocking
proxy refuses. On such a machine the supported refresh is
`git pull && ./scripts/install-offline.sh`; the installer says so on every run.

This channel costs ~20 MiB of permanent git history per release. It is bounded
on purpose — macOS only by default (Linux and Windows users are not the ones
being blocked), one version at a time, and every documented clone uses
`--depth 1` so the user's download stays ~20 MiB regardless of history depth.
`prebuilt/README.md` records the trade-off and the migration to an orphan branch
if it stops being worth it.

### Breaking behaviour changes

None. Existing installs, workspaces and config are untouched; this release only
adds a path for machines that could not install at all.

## v0.19.1 — 2026-08-26

Two things that were left open in v0.19.0, closed with measurement.

### Changed

- **`max_turns` no longer scales with the context window.** It grew by 4 per
  context doubling, on the reasoning that a wider window lets a model keep more
  of its own trail in view. That sounds right and the measurement refutes it.

  On `respects-scope` the growth took `max_turns` 20 → 36, and the run from 11
  LLM calls to 26 and 130,255 prompt tokens to 435,296. Decomposed: **2.36× from
  the extra calls, 1.41× from each call carrying more history — 2.36 × 1.41 =
  3.34, the whole of the observed cost.** It did not finish better for the extra
  turns; it timed out a task.

  Removing the growth was predicted to land near 184,000 tokens and **measured
  176,192 — a 4% error, and a 60% reduction**. The reading the data supports is
  the inverse of the original: a wider window means more held *per turn*, which
  argues for needing fewer turns on the same task. How many turns a task needs is
  a property of the task; a turn ceiling is a safety bound, and raising it
  because the model has more memory only lets a non-converging run go on longer.

  The control confirms it is not a trade: `implement-from-tests`, which *passed*
  under sizing before the change, improved from 164,504 to 153,349 tokens and kept
  5/5 checks and engine success. The gap between 60% and 7% is the finding — extra
  turns cost most where a run has least to do.

  Only the growth is gone — the `min_turns` floor stays, because too few turns
  fails work that would otherwise succeed.

### Fixed

- **`TestLookupIsCheap` measured the machine, not the code.** It asserted a
  500 µs-per-call budget, which fails on a loaded developer box while the code is
  unchanged and correct — and a test that flakes teaches people to re-run the
  suite until it passes, which is how a real regression gets waved through.

  It now bounds **allocations** per call: measured 75, 75, 75 on three
  consecutive runs, ceiling 96. It still skips under `-race`, but for a checked
  reason rather than an assumed one — the detector adds its own per-access
  allocations, measured at 114–117 and varying between runs, so a ceiling wide
  enough to cover it would catch nothing.

### Documented

- **The `respects-scope` cost is attributed, not merely observed.** The leading
  suspect was `ws_read`'s line budget; it was ruled out without a GPU run, since
  the scenario's whole fixture is 272 tokens across four files and both budgets
  return every file whole. [SLM learnings](slm-learnings.md) now carries the
  decomposition instead of an open hypothesis.

## v0.19.0 — 2026-08-25

Verification you can trust, and a written record of why.

The headline is not a feature: **2 of 7 runs on a deliberately impossible task
reported success**, and every false success was a SHORT run. Against a repository
whose suite was already green, the model edited a file, nothing turned red, and
every signal the harness owned said done. Three outcomes could not express
"nothing was measured either way".

[SLM learnings](slm-learnings.md) is new, and collects all of it: 59 recorded
runs across 4 local models, what each number showed, and the mechanism it
produced. Its tables regenerate with `make slm-learnings`.

### Fixed

- **A run cannot claim success on evidence that predates it.** New
  `unverified` outcome for "files changed, and the only acceptance evidence is a
  suite that was green at baseline". It sets `Outcome` and deliberately **not**
  `Success` — an earlier version set both, and the `respects-scope` control run
  caught it failing a run that passed six of six checks. Missing evidence is not
  missing achievement, and `Success` drives exit codes.
- **The harness now establishes its own baseline.** A run-start probe executes
  the acceptance command concurrently, so green means something. An earlier
  opportunistic version waited for the model to run tests first; it passed ten
  unit tests and never fired in production, because the real model made seven
  tool calls straight to editing.
- **Protected files are restored, not just reported.** A 30B run made 142 tool
  calls and rewrote the `_test.go` file its task text forbade it to touch. Only
  paths a task explicitly forbade, and only where exact prior bytes were
  snapshotted — without a snapshot, "restoring" would be deletion.
- **The snapshot hook was never called.** `Runner.OnProtect` was declared and
  assigned, and nothing invoked it, so no backup ever existed and the self-heal
  above could only ever report. Every unit test passed because they all call
  `SnapshotProtected` directly.

### Added

- **Budget sizing from the measured window** — opt-in via
  `slmcode config set calibrate_budgets true`, and **off by default because it
  was measured to cost**. Held to one model (Qwen3-Coder-30B) it was worse on
  both scenarios tried: `implement-from-tests` 119,340 → 164,504 prompt tokens,
  `respects-scope` 130,255 → 435,296 with a task timing out. It ships as a knob
  rather than a default so a 262K model is not stuck with 4K-era budgets when a
  user wants the room.
- **Calibration in Studio.** A panel showing the measured evidence, the
  concurrency ladder with the chosen knee marked, the budgets in force and the
  rendered report; plus a live progress banner, so a cold 42GB model no longer
  looks like a hang. `GET /api/calibration`, with `ensureCalibrated` before every
  run — a model switched in the UI is measured on its first run instead of
  inheriting the previous model's knee and timeouts.
- **`honest-verification` skill** — green is only evidence if it was red before;
  report an impossible task rather than editing the test; stop when the
  objective is already met.
- **`make slm-learnings`** regenerates the evidence tables from e2e reports, so
  the document cannot drift from the data.

### Changed

- **Capacity and demand are no longer the same number.** `MaxPackWindowTokens`
  bounds the window used to *size a pack* at 32,768 while the declared context
  limit stays the model's real window — overflow detection and compaction need
  the truth, and the packer fills whatever window it is given. Models at or below
  the bound are unaffected.

  Measured on `respects-scope`, the bound took the opt-in sizing path from
  631,160 prompt tokens to 435,296 — real, and not a fix: baseline is 121,337.
  The pack is one amplifier among several, which is the evidence behind keeping
  `calibrate_budgets` off by default rather than shipping it on with a bound.
- **Refused measurements are reported.** When an explicit config blocks a
  measured value, calibration now says so and names the edit that would let it
  through. "Nothing changed" had two causes and the report claimed the wrong one.

## v0.18.4 — 2026-08-25

Every limit follows the model.

Two defects here were found only by widening the test matrix — a DENSE 27B
alongside the sparse mixture-of-experts models, and a scenario whose test suite
is green before the run starts. Neither is reachable with a fast model on a
red-to-green fixture, which is all the previous releases measured.

**Measured across four models, 21 scenario runs, full five-scenario suite:**

| model | scenarios | checks |
|---|---|---|
| `Qwen3-Coder-30B-A3B` (two samples) | 9 of 10 | 57 of 58 |
| `Qwen3-Coder-Next` (262K context) | 4 of 5 | 28 of 29 |
| `Qwen3.8-27B` (dense) | 3 of 5 | 25 of 29 |

### Fixed

- **The QA gate no longer spends the finish reserve.** v0.18.3 stopped the BOARD
  while time remained, and the gate then consumed that reserve on its own
  rounds — each one a command run plus a model call, minutes apiece on a dense
  model. Measured: the 27B finished `honest-failure` at 2401s against a 2400s
  budget, and the Coder-30B at exactly 1200s of 1200s, both with `run_err=nil`.
  A run that did everything else right, missing by a second. The gate is the
  LAST thing a run does, so overrunning there costs the report itself — the
  summary, the board write and the verdict all come after it. Validated: two
  runs at **329s and 1143s** against the same 1200s budget.
- **A green that was green before the run no longer ends it.** On a project
  whose suite already passes, green afterwards is the SAME green — evidence
  about the suite, not about the run. Measured on `Qwen3-Coder-Next`: the model
  edited a file, ran `go test` itself, saw the pre-existing pass, and the
  harness read it as the objective being met. The baseline is now captured for
  free from the worker's own first pre-edit test run, and a green matching it
  cannot finish a run early. Red-then-green — `implement-from-tests`,
  `fix-a-bug` — is untouched, and an UNOBSERVED baseline keeps the old
  behaviour: not knowing must not become a refusal.

### Added — every limit follows the model

The same defect existed at three layers, each discarding more context the better
the model was:

- **`ws_read` sized from a 16KB PROMPT-BYTE budget**, capping reads near 80
  lines whatever the model could hold — the conflation
  `compact.WindowTokensFromKB` is already marked Deprecated for, and which the
  packer was migrated off with the note that it "silently capped a 32K model at
  ~3.2K tokens". The read guard was never migrated. Now: **80 → 546 → 4,369**
  lines for legacy / 32K / 262K.
- **Calibration measured the window and applied three knobs**, leaving every
  token budget static. A 262K model ran with a 260-token skill budget — 0.2% of
  its window, the same ABSOLUTE allowance a 4K model gets. Now derived:
  skills **260 → 1,024**, knowledge **180 → 768**, turns **20 → 36**.

  The first cut of this derived 4,096 and 2,730 — a pure share of the window,
  with no cap. MEASURED on a 262K model, that turned respects-scope from 121k
  prompt tokens into 631k and 164s into 1050s: the injected reference material
  is re-sent on EVERY call, so a share-of-window budget multiplies by turn
  count. The caps are what make the share affordable, and sizing is opt-in
  (`calibrate_budgets`, default off) for the same reason.

  *Corrected in v0.19.0:* this entry originally said sizing "helps a focused
  task and costs an exploratory one". That rested on comparing a Qwen3.5-9B run
  against a Coder-30B one. Held to one model it costs on both — see
  [SLM learnings](slm-learnings.md#8-context-capacity-is-not-demand).
- **The search surface had fixed DOMAINS**, so the optimiser proposed
  small-model values forever and actively undid a good calibration one
  experiment at a time. Ranges now scale: `memory_tokens` **800 → 1,600 →
  3,200**.

Output budgets scale WEAKLY and injection budgets STRONGLY, and the asymmetry is
the design: response length is a property of the task, while how much reference
material fits is a property of the window. Lifting `max_tokens` to window/8
turned a slow model's recommended `task_timeout` into eight hours — measured,
and the reason the shares differ.

- **Calibration narrates itself.** Four staged callbacks — warm-up, latency
  baseline, each concurrency level, the context-window read. A cold 42GB model
  is minutes of silence otherwise, at exactly the moment the harness is
  measuring the numbers that make its later timeouts correct.
- **`slmcode calibrate` prints the evidence**: what was measured, what changed
  because of it, and how to override any of it. Numbers that arrive without
  their evidence are numbers nobody can argue with.
- **Studio calibrates on the run path**, not just at startup — Studio is where
  models get switched, and a switch between launch and the first run was
  previously governed by the PREVIOUS model's profile. Progress reaches the
  event stream. `GET /api/calibration` serves the evidence, and is behind the
  session token like every other API route.

### Known

`honest-failure` reports success when the model gives up early rather than
engaging. Measured across two runs of the same code and model: one worked
through 168 tool calls, could not do it, and reported honestly; the other
asserted completion after 38, and the harness believed it because the suite was
green — as it had been from the start. When the objective command is green
before AND after, nothing has been verified about the actual requirement, and
the outcome model has only "success" and "failure" to say. The fix is a third
outcome, not a stricter rule: refusing success whenever the baseline was green
would fail most legitimate work, which runs against green repositories.

## v0.18.3 — 2026-08-25

The harness stops betting time it does not have.

v0.18.2 shipped with one measured defect open: `fix-a-bug` hit its 20-minute
ceiling in one run of three, unchanged across two releases. Chasing it properly
found that all three failures were the SAME event, and that the harness — not
the model — was the part getting it wrong.

**A worker burns its full task timeout producing nothing, and the harness then
starts ANOTHER full-length attempt with less wall-clock left than that attempt
is allowed to take.** It cannot finish. The deadline arrives mid-call and takes
the finish path with it: no QA gate, no board write, no summary — in runs that
had *already left a correct tree on disk*. The only failing assertion was the
wall budget; everything the run actually did was right and was thrown away.

**Measured, same fixtures, same 8m task timeout and 20m ceiling:**

| | v0.18.1 | v0.18.2 | v0.18.3 |
|---|---|---|---|
| `fix-a-bug` pass rate (9B) | 2 of 3 | 2 of 3 | **3 of 3** |
| `fix-a-bug` pass rate (Coder 30B) | — | — | **2 of 2** |
| `implement-from-tests` | 3 of 3 | 3 of 3 | **1 of 1** |
| individual checks | — | — | **30 of 30** |

The run that demonstrates the mechanism rather than luck is the one that still
reached the wall: it stopped ITSELF at 1199.1s with `run_err=<nil>` and all five
checks green, where the equivalent v0.18.2 run was killed at 1200s with
`context deadline exceeded`. It reports `engine_success=false` with six tasks
planned and not all executed — which is the honest reading, not a regression.

### Fixed

- **A stalled worker can no longer outlive its run.** Every loop-side agent
  dispatch — worker, reviewer, review retry, corrector, finalize recovery — ran
  on a flat `r.Timeout` that never looked at the clock. They are now clamped to
  the runway, with a fifth of the run's ORIGINAL budget held back for the finish
  path, and dispatch STOPS once only that reserve remains.
  Two things this took, both found by a deterministic reproduction and neither
  visible in live runs:
  - **The reserve must be absolute, not fractional.** A reserve taken as a
    fraction of what REMAINS is geometric and reserves nothing: measured on a 6s
    runway, successive 4/5 clamps handed out 4.8s, 0.96s, 0.19s … summing back to
    the whole 6s. Every call individually affordable; together they ate the run.
  - **Stopping new waves is not enough.** A worker that stalls to the reserve
    boundary is still followed, inside the same wave, by a review and a
    correction. The guard belongs in `execOne`, the one choke point every
    loop-side dispatch passes through.
  This cannot make a stalled model productive — nothing in the harness can. It
  stops the harness spending time it does not have, which is its own to get right.
- **The harness harvests the worker's own verification.** A worker checks its
  work by running the project's test command through `ws_shell`, and that output
  already flowed past the harness, which ignored it and later paid for a full
  test run to learn the same thing. A clean exit of the EXACT objective command
  is now free evidence for the next probe, and the model is told in the tool
  result it is already reading that the criterion is met and the next call must
  be its finish call. Matching is exact: a green `go test ./chunk` says nothing
  about `go test ./...`, and the evidence is discarded the moment anything is
  written after it, retracted by a later failure, and never accepted from a weak
  gate or a `[no test files]` run.

### Notes

Measured on two SLMs. `Qwen3-Coder-30B-A3B-Instruct-MLX-4bit` — the project's
configured default — does about 2.5x the work of the 9B (55 tool calls, ~254k
tokens) in half the wall time, and does it consistently (513s/466s against the
9B's 461s-1199s swing). The remaining ceiling risk is a 9B capability property,
not a harness one.

## v0.18.2 — 2026-08-25

Budgets that were spent by failure.

v0.18.1 left one measured defect unsolved: `fix-a-bug` hit its 20-minute ceiling
in one run of three with `failed_tasks == 0` — nothing wrong except that nobody
asked whether the work was done. Chasing it found the cause, and the cause
turned out to be a **pattern** rather than a bug. Six mechanisms in this
codebase bounded themselves with a fixed count, charged that count for negative
or failed attempts, and so switched themselves off precisely on the runs that
needed them. Every one of them failed silently.

**Measured, three runs per scenario, Qwen3.5-9B on oMLX, same fixtures and the
same 20-minute ceiling as the v0.18.1 measurement:**

| | v0.18.1 | v0.18.2 |
|---|---|---|
| `implement-from-tests` median prompt tokens | 123,652 | **75,535 (−39%)** |
| `implement-from-tests` pass rate | 3 of 3 | 3 of 3 |
| `fix-a-bug` median prompt tokens | ~178,000 | ~182,000 |
| `fix-a-bug` pass rate | 2 of 3 | 2 of 3 |
| runs terminating within budget | 5 of 6 | 5 of 6 |

**Read that table honestly: one scenario improved substantially and the other
did not move.** The probe-starvation defect was real, is fixed, and is confirmed
firing in a live run — the run metrics record
`"gates":[{"name":"qa_gate","passed":true},{"name":"objective_met_early","passed":true}]`,
which is the between-waves probe ending a run the moment the objective went
green, in 6 LLM calls and 375s. But it was **not** what `fix-a-bug` was failing
on, and that scenario's 1-in-3 ceiling miss is unchanged.

What that remaining miss actually is, measured: the same 9B needs 8 tool calls
and 69k tokens for this three-line boundary fix on a good run and **32 tool
calls and 192k tokens** on a bad one. In the run that hits the ceiling the
harness still returns a CORRECT result — `engine_success=true`, the fixture
tests green, the protected test file byte-identical, `failed_tasks=0`. The only
failing assertion is the suite's own wall-clock budget. So this is model
variance, not a harness that cannot tell it is done.

The identified next step, with evidence behind it and deliberately NOT taken
here: the probe fires only between waves, so when a worker fixes the bug at tool
call 10 of 32, nothing notices until the task ends. That worker runs the
objective command itself through `ws_shell` during those calls, and the harness
already sees the output — a green result there is free evidence the probe could
harvest without spending anything. It is a real design and an unproven one, and
this release does not ship unproven changes.

Wall-clock is deliberately absent from the table above. Across these runs the
same suite showed FEWER tokens taking MORE seconds (75k/792s here against
124k/608s for v0.18.1), i.e. throughput degraded over an hour of continuous
local GPU load. Wall times are not comparable between sessions on this hardware;
token counts do not depend on machine speed, so they are what is reported.

### Fixed

- **The objective probe no longer goes blind three waves into a run.**
  `maxObjectiveProbes` was 2, and the budget was charged for RED answers.
  Between-waves probing spends it on the earliest waves — the two moments in a
  run when "not yet" is most certain and least worth paying to learn — so from
  wave three nothing ever asked again. If the implementation landed at wave
  five, the run burned its whole ceiling with the work already complete. The
  budget is also **per run, not per board**: a run drives `RunBoard` once per
  corrective round, so two probes was two for the entire run, and the post-drain
  probe that corrective boards deliberately rely on (they run with early-stop
  off) was starved by the same exhaustion.
  A count also cannot be right for two projects at once — two probes is miserly
  for a 200ms unit suite and profligate for a 6-minute integration suite. The
  bound is now economic: the probe's cost is **measured** (`SmokeResult.Duration`,
  timed where the command actually runs) and asking continues while
  `runway >= 6 × cost`. A cheap gate is asked on every wave that wrote
  something; an expensive one only while there is runway for the answer to pay
  for itself. Neither number is guessed per project.
- **A slow endpoint no longer loses structured decoding for seven days.**
  One deadline covers up to six sequential capability probes. A cold local model
  — the exact case the budget was widened for — could spend most of it loading
  weights for the first, after which every later probe died on the shared
  deadline. `attempt` returns false for a transport error exactly as for a 400,
  so the negotiation could not tell "the server refused this field" from "we
  never got to ask", and stamped `Source: "probe"` on a wholesale-false record.
  `capCache` then persisted and honored it for `CapabilityTTL` — seven days in
  which every structured role on that endpoint silently degraded to prompt-only
  + repair, with no path back, because nothing re-probes a record that is still
  fresh. A cut-short negotiation now falls back to the family preset and leaves
  `Probed` zero, which keeps it out of the on-disk cache. Preferring the preset
  over all-false is the recoverable direction: an over-claimed mechanism costs
  one 400 and is then recorded by `demoteCapability`; an under-claimed one has
  nothing that can ever notice it.
- **Retrieval no longer goes blind when the corpus is most on-topic.**
  The relative noise floor is documented as "the corpus's own median", but it
  was measured over the value `Search` returns — already truncated to `TopK`.
  With the default `TopK=5` the "median" was the **third-best hit**, so the
  threshold became third-best + `NoiseMargin`: arithmetically at most two hits
  could ever survive, and a tightly clustered top five — a set of uniformly
  strong matches — cleared nothing at all and `RetrieveForQuery` returned `""`
  with no error and no warning. It also made `MinChunksForNoiseFloor`
  unreachable for its stated purpose, since it compared against `len(top-k)` and
  never the corpus size. `Retriever.SearchAll` now supplies the whole scored
  distribution for the floor, and the top-k truncation happens after.
- **A cancelled QA gate no longer reports a red verdict.** `runQAGate` returns
  "the gate failed", and every cancellation path returned **true**.
  `finalizeAfterExecute` reads that as `QAFailed`, sets `TesterRejected`, and
  feeds the board a synthesized tester verdict
  (`{"passed":false,...,"qa_gate red"}`) through `applyTesterFeedback` — a
  planner call. So Ctrl-C, or a scenario budget expiring, bought an extra LLM
  round-trip during shutdown and annotated a done task with "QA gate still
  failing" about a gate never allowed to finish. A cancelled gate now records
  nothing: no verdict, no annotation.
- **The QA gate stops re-running an unchanged tree.** `qaDiagnoseAndFix`
  discarded both role outputs and never reported whether anything was written,
  so a fix pass that produced nothing (budget exhausted, a refusal, a prose-only
  answer) was followed by a byte-identical command run against a byte-identical
  tree, at full price, for every remaining round. The objective probe has
  refused exactly this since it was written; the gate never learned it. Measured
  on a stalled gate: 4 command runs down to 1. Gate rounds also now price the
  command for the probe budget, since they run the same one.
- **A cold start no longer pins concurrency for a month.** The calibration probe
  runs inside a fixed wall-clock budget in which every unit of work is a model
  call — the thing being measured. On a slow server the warm-up and solo
  baseline can exhaust it before any concurrency level is measured, leaving only
  the synthetic single-level entry, from which `SelectKnee` returns 1.
  `Profile.Current` checked ID, `MaxParallel`, version and age but **not
  `Partial`**, so that degenerate verdict was served from cache for
  `DefaultTTL`; `Apply` only checks `MaxParallel > 0`, and the "partial" marker
  appears solely in `Summary()`, which the auto path never prints. So the
  slowest models — the ones with the most to gain from a real measurement — were
  silently capped at `max_parallel=1` for thirty days. Partial profiles now
  expire after `PartialTTL` (1 hour): a cold start is transient, so the retry
  should be too.
- **The thrash detector no longer blinds itself on thrashing runs.** The
  signature map **froze** at `MaxSignatures` — once full, a signature it had not
  already seen was never admitted, so its later repeats were never counted. The
  asymmetry is what makes it bite: the classic small-model edit failure is a
  near-miss `ws_edit` retried with a slightly different `old_str` each time, and
  every one of those is a *distinct* signature, so a thrashing run fills the map
  faster than a healthy one and goes blind sooner. Measured: 5 identical calls
  after 256 distinct ones counted **0** repeats instead of 4.
  `RunReport.RedundantCalls` and the redundant-call-rate KPI under-reported with
  nothing marking the count as capped, and evolve learned from the truncated
  signal. It now evicts oldest-first, like every other bound in that file.
- **A reviewer that timed out is no longer recorded as one that judged the work
  worthless.** The synthesized error attempt carries `NoVerdict`, and
  `Attempt.Score` documents that a 0 under an `error` verdict is an absence, not
  a judgement.
- **`autoresearch` no longer claims a surface was exhausted when it was not.**
  `StopExhausted` is reached on `ErrNoProposal` from the *deterministic*
  proposer, which cannot touch a text knob at all — yet its sentence read "every
  value of every knob was tried", the one message in that package built to be
  trusted, while every sibling carefully says when the surface was *not*
  exhausted.

### Known, reported rather than changed

Three further findings are real and measured but are **behaviour changes whose
benefit cannot be proven without a controlled study**, so they are documented
instead of shipped unmeasured:

- `reviewerStrictDelay` (20ms) means the default `max_parallel=4` issues **two
  reviewer LLM requests per review**, and `strictOut` is used only when the
  primary reply is empty — so the second is discarded almost always. The code is
  honest about the cost (`noteExtraRequests` reports it), but on a local server
  that runs inference serially this roughly doubles review latency. A delay
  derived from measured reviewer p50 would keep the insurance and drop the cost.
- `readBudgetLines` sizes `ws_read` from `MaxContextKB`, the legacy prompt-byte
  budget, rather than the model's real `ContextLimit` — the exact conflation
  `compact.WindowTokensFromKB` is already marked Deprecated for. On typical
  source that caps a read around 80–120 lines whatever the model's real window
  is. Fixing it would raise read sizes several-fold on a large-context model,
  which needs measuring before it ships.
- `agents.factory` charges loop-guard interventions against the ReAct iteration
  budget (both escalation paths return a successful tool result), and a model
  profile's `max_turns` can only *lower* the default 8, never raise it.

## v0.18.1 — 2026-08-25

The harness stops when the work is done.

Every fix in this release was found by running slmcode against real local models
(Qwen3.5-9B and Qwen3.8-27B on oMLX) rather than by reading code or watching a
green test suite. The pattern they share is the one that matters most for a
local SLM: **the model did the job correctly and the harness could not tell.**

In the measured baseline, a 9B implemented `Median` — including a deliberate
no-mutation trap a naive `sort.Float64s(xs)` fails — left the protected test file
byte-identical, and `go test ./...` went green after about five minutes. The
harness then ran for the remaining fifteen, reported `engine_success=false`, and
spent 201,875 prompt tokens doing it. Same shape in a second scenario. That is
not model incapability; every one of these is a harness defect, and they are
fixed here.

**Measured, same fixtures and model and 20-minute ceiling, three runs each:**

| | before | after (median of 3) |
|---|---|---|
| `implement-from-tests` wall | 1200s — hit the ceiling | **608s** |
| `implement-from-tests` prompt tokens | 201,875 | **123,652** |
| `fix-a-bug` wall | 1200s — hit the ceiling | **1075s** |
| runs terminating within budget | 0 of 4 | **5 of 6** |
| runs reporting a failed task | 4 of 4 | **0 of 6** |

`implement-from-tests` passes all five checks in all three runs, including the
protected-file check that previously failed. `failed_tasks` dropping to zero
everywhere is the reviewer fix below, measured from the other end: those tasks
were never failing, the harness was misreading its own reviewer.

Not everything is solved. `fix-a-bug` still hits the ceiling in one run of
three, and its token count did not improve. The harness adapts to the situation
far better than it did; it does not adapt perfectly.

### Added

- **Auto-calibration.** slmcode now measures the endpoint it is pointed at
  instead of guessing from a provider name: concurrency knee, p50/p95 latency,
  throughput, and the context window (read from `GET /v1/models`, not probed).
  Profiles are stored per **exact model id + endpoint** in
  `~/.slmcode/memory/calibration.json`, so a 27B and a 9B of the same family
  never share a number. It runs once per unseen pair, is hard-capped at 60s,
  never blocks a run, and falls back to static defaults with one warning on any
  failure. `slmcode calibrate --show` prints the measured table.
  Nothing hardcodes a concurrency: fed a perfectly-scaling server it answers 8;
  fed a serial one, 1. Measured here, a single local endpoint runs 4-way
  concurrency at ~40% efficiency while per-request latency nearly triples — and
  role timeouts are wall-clock, which is why this is a correctness issue and not
  a tuning preference.
  Calibration **seeds only what it measured** — throughput and role-latency
  floors. It deliberately does not touch the bandit's posteriors: a 16-token
  probe is evidence about none of `edit_format`, `think_passes` or review
  strictness, and those stay with the learner that earns them from outcomes.
- **`slmcode calibrate`** and **`make e2e-slm`** — a repeatable live-model
  end-to-end suite (`scripts/e2e-slm.sh`, five scenarios) that asserts objective
  outcomes: fixture tests green, protected files byte-identical, `Result.Success`.
  Gated behind `RUN_E2E=1`; `make check` never contacts a model.

### Fixed

- **The harness now stops when the objective is met.** The QA gate only ran in
  the finish path, so nothing evaluated the goal *during* the board — and because
  a rejected task kept the board from draining, the existing post-drain probe
  never fired. A `BetweenWaves` probe now asks the same gate (one definition of
  "green", shared with the finish path) after each wave, bounded to two paid
  command runs per run and skipped entirely when nothing has been written since
  the last answer. Weak gates (syntax-only `compileall`) can never end a run
  early, and the probe refuses while a tester rejected, while work is escalated,
  while a task is mid-flight, and when an operator has wired their own `test`
  phase. Abandoned tasks are reported, not swallowed: `Result.UnexecutedTasks`
  and a summary line name the count.
- **The reviewer no longer reports parse failures as rejections.** There was no
  representation for "the reviewer produced no verdict" — every unreadable reply
  became `{Approved:false, Score:0}`, byte-identical to a considered score-0
  rejection. Three paths fed it: an empty reply short-circuited to a zero value;
  the JSON repair ladder carved out the *first* balanced document, which is often
  the worker JSON the reviewer echoed back, turning an approval into a rejection;
  and the rescue that exists for exactly this was disabled by
  `looksLikeBrokenReview`, which returns false the moment the text contains
  `"approved"` — precisely what a reply looks like after its verdict was
  destroyed. The corrector was consequently being asked to fix *the reviewer's*
  JSON. `ReviewResult.NoVerdict` now carries the parser's own report and is
  authoritative.
- **Repeated-tool-call loops actually break.** Escalation was keyed on
  *consecutive* refusals and reset by any successful call, so an alternating
  A,B,A,B loop never escalated at all — and every rung, including the "HARD
  STOP", was just more text for the model to acknowledge and ignore. The second
  refusal of a call now withdraws that tool for the task, closing the
  "vary the arguments and keep going" escape. Editing tools are never withdrawn
  (varying `old_str` is a real attempt); they get a terminal finish directive.
  Any state-changing call hands the tools back.
- **`ws_shell` no longer bypasses the focus guard.** `ws_write`, `ws_edit`,
  `ws_patch`, `ws_mv` and `ws_delete` all called `checkFocus`; `ws_shell` called
  nothing, so `bash tool.sh` could rewrite a file the task was told not to touch.
  Writes are now detected by fingerprint comparison and surfaced to the reviewer
  as disk evidence — deliberately **not** reverted, because once a process has
  exited its own build output is indistinguishable from a stray write.
  A task's own "do not edit any `_test.go` file" is now derived into an enforced
  protection: `ws_edit` refuses it and a shell write to it is flagged. Derivation
  fails toward extracting nothing — a clause containing `unless`/`except` is
  discarded whole rather than half-understood.
- **Acceptance commands are no longer corrupted into failures.** Four bugs in one
  function turned working commands into failing ones, which the smoke gate then
  reported as `FAILED` and the reviewer rejected correct work over:
  `TrimRight(cmd, ".,;:")` ate package patterns (`go test ./...` → `go test ./`);
  trailing outcome prose was executed as argv (`go test ./... passes` made Go
  look for a package named `passes`); and a substring prefix match meant
  **`cargo test` ran `go test`** — a Go toolchain against a Rust repo, with the
  failure blamed on the model.
- **`make check` no longer dies as a phantom hang.** `coverage-check.sh` ran
  `go test ./...` with no `-timeout`, inheriting Go's 10-minute-per-package
  default. `test/e2e` takes ~520s clean; coverage instrumentation pushed it over
  and the gate failed with `panic: test timed out` — indistinguishable at a
  glance from a hang in the code under test.
- **`scripts/lint.sh` excludes `.claude/`**, which holds agent worktrees — full
  checkouts of the repo. `gofmt -l .` walked them, so another session's
  half-written file could fail the gate for a working tree that was itself clean.

## v0.18.0 — 2026-08-24

Memory that connects, and the defects that only appear when you run a real model.

This release does two things. It makes the harness's learning *relational* — records that
were already being written but never joined are now traversable, attempts keep their
lineage, and the reviewer can be grounded against recorded facts. And it fixes nine
defects, seven of which were invisible to a passing test suite and surfaced only by
running the harness end to end against local models (Qwen3.8-27B and Qwen3.5-9B on oMLX).

Nothing here needs a config migration. Two new subsystems are opt-in and default off.

### ⚠️ Behaviour changes

| What changed | Who it affects | What to do |
|---|---|---|
| **Headless gates proceed instead of stopping.** With no TTY, the plan / clarify / continue / escalate gates now announce a decision at run start and continue. Previously they resolved to "stop" *after* the planning work was already done. | Scripts and CI that relied on a headless run stopping at the plan gate. | Pass `--on-gate-timeout=stop` — an explicit choice still wins. The decision is printed at `init` either way. |
| **The shell-permission gate never auto-approves headless.** It refuses at run start with exit 6. | Headless runs that needed a shell command requiring approval. | Add the command to `shell_allow`, or set `shell_permission` explicitly. A safety gate is not a convenience gate. |
| **`IsInteractive` requires stdin *and* stdout to be TTYs.** | `slmcode run … \| tee` — which previously wrote a gate card into the pipe and blocked on a question nobody could see. | Nothing; this is the fix. |
| **A dependency is satisfied only by `done`.** A `blocked` (failed) upstream no longer licenses its dependents; they become blocked too, with the failed upstream named. | Boards that relied on work continuing past a failed prerequisite. | Nothing — the previous behaviour built work on a foundation that was never laid. |
| **Role timeouts are measured, not fractional.** Derived from p95 latency per model family instead of a fixed fraction of `task_timeout`. | Anyone on a slow model whose `context`/`composer`/`architect` roles were timing out. | Nothing. Cold start grants the full budget; `task_timeout` remains a hard ceiling. |

### Added

- **`pkg/graph` — a traversable index over records the harness already wrote.** `Fact.Sources`,
  `Rule.Evidence`, `Episode.FilesChanged`, `FailureNote.ResolvedBy` and `Episode.RunID` were
  stored as opaque strings that nothing followed. They are now typed edges, so a question
  spanning several stores — *"what failure classes has this file produced, and which rule
  resolved them"* — is one traversal instead of three lookups joined by hand.
  It is **not** entity extraction over your source: node identity is exact, no fuzzy matching,
  so the classic false-merge failure mode does not apply. Inspect with `slmcode graph`
  (`stats`, `file <path>`, `neighbors`, `walk`, `backfill`, `prune`, `forget`). Derived data —
  `rm -rf .slmcode/graph` costs a backfill and nothing else. See [Knowledge graph](graph.md).
- **`pkg/autoresearch` — a ratchet over this project's own prompts and knobs.** The mutable
  surface is already data (`.slmcode/agents/*.yaml` carry `system_prompt`, `temperature`,
  `max_tokens`); the evaluator is the existing `pkg/eval` harness. Reversibility is a file
  snapshot, never a git commit, so an experiment cannot touch your history. Guards check the
  primary metric against **both** the champion and the run's own baseline, so a sequence of
  individually-tolerable regressions cannot accumulate into a large one. Two-key opt-in
  (`--apply` **and** `autoresearch: true`); the default invocation calls the evaluator zero
  times. See [Autoresearch](autoresearch.md).
- **Attempt lineage.** Each attempt is persisted with a parent pointer, its hypothesis, diff
  stat, gate signals, reviewer verdict and failure class. The corrector now receives
  *approaches already tried and rejected, with the reason each was rejected*, deduplicated —
  which is what stops a small model re-proposing something the reviewer already refused twice.
- **Knowledge grounding for the reviewer.** A deterministic pass reconciles the worker's command
  claims against semantic memory and emits structured contradictions (`decision` / `claim` /
  `reason` / `required_evidence`). Precision over recall: a claim is contradicted only when the
  tool appears in *no* recorded command at any confidence, so a Go repo with a `web/` tree never
  has `npm test` flagged as hallucinated. Zero additional model calls.
- **Measured role latency**, persisted per model family in `~/.slmcode/memory/latency.json`.

### Fixed

- **The reviewer could not see the evidence it was told to judge on.** `runGates` appends
  `## Disk evidence` / `## Deterministic smoke` / `## Acceptance smoke` to the **end** of the
  worker's output, and the review prompt clipped that output **head-first** at 3500 chars. A
  verbose worker therefore deleted the evidence, and the reviewer — following its own *"reject
  if output is only claims"* rule — rejected correct code. Observed live: **seven consecutive
  score-0 rejections across 22 minutes** of an implementation whose tests all passed, with no
  correction round able to fix it. Prose and evidence are now budgeted separately.
- **Headless runs discarded completed work.** A 27B run spent 9m17s, produced a green scope
  judge and a valid four-task board, then wrote nothing. The gate mechanism existed; its default
  punished the headless case maximally.
- **Slow models were starved by fractional timeouts.** On `task_timeout: 5m`, `context` got 75s
  and failed every run on a 27B while `explorer` — comparable work, same workspace — got 300s
  and used 128s. Timeouts are now recorded as censored lower bounds, so an under-measured budget
  widens itself instead of failing forever.
- **A read-only role could burn its whole budget fighting the focus guard.** The refusal said
  what was blocked and never what to do instead, so an explorer retried the same impossible
  `ws_edit` six times, then wandered off-task. Refusals now name the role's contract and a
  terminating next action. Out-of-scope writes are also a classified failure with a seeded
  repair rule, so the self-improvement loop can finally learn from a failure mode it hit
  constantly.
- **A failed dependency silently unblocked everything downstream**, and `pkg/plan` had no cycle
  detection anywhere — a cycle meant those tasks were simply never executable, silently.
- **Concurrent workers could write the same file with no guard.** Verified load-bearing: without
  the per-path lock, **11 of 16** concurrent updates were lost. Wave admission additionally
  defers overlapping tasks to the next wave.
- **The keyword gate on learned lessons is gone.** The only path from a stored lesson to a future
  prompt dropped any line not containing one of eleven hardcoded substrings; on this repository's
  own `MEMORY.md` that discarded **4 of 7** lessons. Lessons now become confidence-scored,
  contradiction-tracked `Fact`s, ranked by the BM25F scorer that already existed. Relevance
  *orders* the block rather than filtering it — filtering on a token mismatch could empty it.
  `RenderMarkdown` also no longer discards `TaskID` and `At` at write time.

## v0.17.0 — 2026-08-23

The largest release since the project started: a rebuilt engine, 13 language packs, a
reworked Studio, and a security pass that changed several defaults. **Read the breaking
changes below before upgrading an existing workspace** — nothing needs a config migration
(`.slmcode/config.yaml` is migrated forward on load), but five defaults are now more
conservative and two of them will change what your scripts see.

→ Full detail and the opt-back-in for every item: **[Migration notes](migration.md)**

### ⚠️ Breaking behaviour changes

| What changed | Who it breaks | What to do |
|---|---|---|
| **Repository hooks fail closed.** `hooks_enabled` now defaults to `false`, and even with it on, `.slmcode/hooks.json` must be approved per user against a SHA-256 of its exact contents. | Anyone relying on a hooks file that used to run automatically after a clone. | `slmcode hooks list` prints every command that would run; `slmcode hooks trust` approves the current contents. Any edit revokes the approval. CI: `SLMCODE_TRUST_HOOKS=1`. |
| **`mcp_servers` is honoured only from the user config layer.** A project file can no longer add, replace or clear the list. | Repos that shipped their own MCP servers in `.slmcode/config.yaml`. | Move the entries to your user config, or set `SLMCODE_TRUST_PROJECT_MCP=1`. `status`, `doctor` and `config show` name whatever the project file tried to declare. |
| **The shell allowlist is tiered.** Interpreters (`python` `node` `bash` `make` `npx` `sudo` …) and file mutators (`sed` `rm` `cp` `chmod` `git reset` …) no longer auto-run. Command substitution (`$(…)`, backticks, `<(…)`) and a bare `&` are refused and are **not** allowlistable. | Runs that depended on `make`, `python -c`, or shelling out to a mutator. | Add prefixes to `shell_allow` (`- "make "`, `- "python -c"`), or `export SLMCODE_BASH_ALLOW="make ,python -c"`. Verification forms like `python -m pytest`, `node --check`, `npm test`, `go test`, `bash -n` stay auto-allowed. See [migration §1](migration.md#1-the-shell-whitelist-is-tiered-interpreters-and-file-mutators-are-refused). |
| **`slmcode apply` is interactive by default**, and **exits 2 without a TTY** rather than guessing. | Scripts and CI that ran `slmcode apply` and expected it to apply everything. | Pass `--all` for the old behaviour (`--list` / `--json` for read-only). See [migration §3](migration.md#3-slmcode-apply-is-interactive-by-default). |
| **HITL gates block instead of auto-approving when a human is attached.** | Interactive sessions that used to sail through plan/escalation gates. | Answer the gate, or set the gate to `auto`. Headless runs are unchanged and still follow `--on-gate-timeout`. See [migration §4](migration.md#4-hitl-gates-block-instead-of-auto-approving-when-a-human-is-attached). |
| **Studio requires a session token.** The `<meta name="slmcode-token">` shell injection is gone; `GET /` is no longer an unauthenticated token dispenser. CORS `*` is gone with it, and non-loopback `Host` values get a 403. | Bookmarks to `http://127.0.0.1:7420/`, and anything scripting the Studio API. | Open the URL `slmcode studio` prints (it carries `?t=…`); that mints an HttpOnly `SameSite=Strict` session cookie. API clients send `X-SLMCode-Token` or `Authorization: Bearer`. See [migration §2](migration.md#2-studio-cors-is-gone-and-there-is-a-session-token). |
| **New state directories** `.slmcode/memory/` and `.slmcode/evolve/` (plus `metrics/`, `summaries/`, `capabilities.json`). | Anyone whose `.slmcode/.gitignore` predates them — `slmcode commit` runs `git add -A` and would commit them. | Run `slmcode init` once. It rewrites `.slmcode/.gitignore` with all 26 rules. See [migration §5](migration.md#5-new-state-directories-under-slmcode-and-slmcode). |

### Security

- **Repository-supplied hooks fail closed.** `.slmcode/hooks.json` lives inside the project, so a
  clone could ship one and `slmcode run` would execute it. Now `hooks_enabled` defaults to
  **false**, and even with it on the file's exact contents must be approved per user via the new
  `slmcode hooks list | trust | untrust`. Approvals are keyed on a SHA-256 of the file and stored
  in the user's config directory, so a repo cannot ship its own approval and any edit revokes it.
  `slmcode hooks list` prints every command that would run before anything is approved.
  `SLMCODE_TRUST_HOOKS=1` remains the CI escape hatch.
- **`mcp_servers` is honoured only from the user config layer.** Each entry is spawned as a child
  process at startup; a project file can no longer add, replace or clear the list. Whatever it
  declared is named in a warning that `status`, `doctor` and `config show` all print.
  `SLMCODE_TRUST_PROJECT_MCP=1` opts back in.
- **Studio authenticates the HTML shell.** The `<meta name="slmcode-token">` injection is gone —
  it made `GET /` an unauthenticated token dispenser for any other local process. Presenting the
  token (`?t=`, `X-SLMCode-Token`, or `Authorization: Bearer`) once mints an HttpOnly,
  `SameSite=Strict` session cookie; an unauthenticated navigation now gets a 401 page telling the
  user to open the URL the CLI printed.

### Fixed

- **Exit code 130 no longer guesses.** Any error whose text contained the word "interrupted" —
  including a provider replying "upstream request interrupted" — exited 130 on a run nobody had
  touched. Cancellation is now decided by one definition shared with the engine
  (`loop.IsContextCancelErr`: `errors.Is(context.Canceled)` plus the exact phrase), and commands
  that know their own run context classify it themselves.
- **`slmcode init` writes the full ignore list.** The CLI kept its own six-entry copy of
  `.slmcode/.gitignore` while the workspace had grown to 26 paths, so `slmcode commit`
  (`git add -A`) staged `memory/`, `evolve/`, `metrics/`, `summaries/`, `capabilities.json` and
  more. The list now lives in `pkg/config` and is the same one `slmcode doctor` probes.
- **Every command group rejects a typo.** `slmcode blocks nosuchthing` printed a block listing and
  exited 0; the guard skipped any group with its own default action, which was most of them. All
  fourteen groups now exit 2.
- **`--no-banner` does something.** It was bound to a variable nothing read. It now suppresses the
  ASCII banner in help, `studio`, the TUI and `version`.
- **`scripts/e2e_prime_smoke.sh` runs again**, and drives Studio's real session token instead of
  opting out of auth. It had been aborting on a `SIGPIPE` from `head` under `set -o pipefail`
  before it reached a single Studio assertion.
- **`make check` is honest.** It no longer depends on `go mod tidy` (which rewrote `go.mod` as a
  side effect and needed the module proxy); `tidy-check` and `web-check` now skip with a named
  reason when the proxy or the npm registry is unreachable. `make build` no longer runs `tidy`
  either, so it works offline. `scripts/install.sh` no longer aborts when the Studio UI cannot be
  built — it installs with the placeholder page and says so.

### Added

- **`slmcode hooks list | trust | untrust`** — the supported path for an operator with legitimate
  repository hooks.
- **`test/e2e/binary_acceptance_test.go`** — the acceptance test for the product: it builds the
  real binary and `test/fakemodel`, then drives `init → doctor → run → task show → diff → apply`
  against a Go fixture and a TypeScript fixture, asserting the bytes on disk, the language pack
  `init` detected, and that the run summary's claims match the tree. No model, no network.
- **`test/fakemodel -addr 127.0.0.1:0`** picks a free port and prints it, so parallel CI jobs
  cannot collide.

### Changed

- `max_task_calls` now defaults to **10**, derived from `max_retries` (worker + self-critique +
  `max_retries` × (review + correct)) rather than picked. At the old 6 a task got two correction
  rounds no matter what `max_retries` said.
- Skills support `paths:` — a glob list that keeps a skill out of prompts whose scope it cannot
  apply to. An explicit `@skill:name` or a config pin still wins.
- `slmcode init` drives language detection from `blocks.DetectPack` across 13 packs, proving the
  language from file content rather than from a marker file alone.

## v0.16.0 — 2026-08-19

- Add self-evolving harness memory
- Sync Homebrew formula checksums for v0.15.0 [skip ci]

## v0.15.0 — Production SLM Harness, HITL UX & Studio Control Plane

### Highlights
- **Production readiness diagnostics** — richer status/readiness commands and Studio
  checks surface backend health, provider state, dynamic pipeline fit, and actionable
  warnings before a run starts.
- **User-validation UX** — plan approval, clarification, continuation, escalation, and
  shell asks now use structured pending-state handling with timeouts/default actions,
  making HITL decisions visible and resumable from Studio.
- **SLM information sharing** — shared briefs, session event summaries, and composer
  fit analysis help specialized agents pass compact task context without exhausting
  local-model context windows.
- **Dynamic pipeline visibility** — the Live page now shows selected agents, composed
  phases, SLM-fit hints, execute-loop roles, phase progress, current stage, recent
  agent activity, and readable long labels with hover/full-detail access.
- **Board task control** — the Kanban board supports adding tasks to any column,
  editing all primary task fields, moving tasks across custom columns, viewing long
  descriptions/outputs without clipping, and deleting tasks from the board UI.
- **Studio UX polish** — agent cards, Live task views, run history, readiness panels,
  and event logs are more readable for long local-model names, prompts, task titles,
  and diagnostics.

## v0.14.0 — Dynamic Pipeline, Broad Language Support & Live Log Severity

### Highlights
- **Dynamic pipeline is now the default** — the `composer` specialist assembles a
  task-specific pipeline (phases, team, tools, skills) before every run; disable via
  `dynamic_pipeline: false`, `slmcode run --no-dynamic`, or the Studio Settings toggle.
  The composer deterministically upgrades generic `worker`/`tester` to the matching
  language specialist, enforces that `execute` + `test` always run, and falls back to a
  safe generic pipeline for any unknown language.
- **Six new language packs** — `web` (static HTML/CSS/JS), `rust`, `java`, `cpp`
  (full pipeline + quality + worker/tester), plus `shell` worker/tester agents.
  `DetectProjectLanguage`/`langHint`/splitter guidance now cover Go, Python, JS/TS,
  Rust, Java, C/C++, HTML, and shell.
- **Static-web deliverables are guaranteed** — HTML/browser/game queries always get an
  `index.html` (or the splitter's chosen `.html`) entrypoint injected; a pile of
  disconnected `.js` files with no page can no longer happen.
- **QA gate is workspace-aware** — a stale `active_pack` (e.g. `python` from a prior
  run) no longer forces pytest onto a Go/JS/HTML workspace; virtualenv/cache dirs are
  skipped during quality detection; a lone `.go` file without `go.mod` uses a
  module-free `gofmt -e` smoke, and the `go test`→`go vet` fast-path no longer emits
  an invalid `-short` flag.
- **Live log severity** — events now carry a `level` (`info|warning|error|success|problem`);
  the Studio Event Log renders severity badges, colors, and a Problems filter with
  error/warning/success counts.
- **Tester finalize is more forgiving** — an explicit `passed:true` anywhere in the
  finalize (not just inside the parsed JSON object) is now honored, reducing false
  "missing passed:true" rejections.
- **Same-file task collapse** — many worker tasks editing one self-contained file are
  collapsed into a single worker (fixes the "7 tasks editing index.html" grind that
  caused review/correction loops).
- **Shell whitelist** extended for cargo/mvn/gradle/ctest/gcc/clang/shellcheck.

## v0.13.1 — 2026-08-10

- Automate Homebrew formula checksum sync in the release pipeline
- Make LiveStore onChange callback synchronous (fixes flaky TempDir cleanup race in CI)
- Normalize 'v' prefix on update-check latest tag (no double-v in notices)
- Sync Homebrew formula checksums with v0.13.0 release assets, fix install.ps1 typo in release body
## v0.13.0 — Block CRUD, Studio GUI Editing, Language Pinning & Live Feedback

### Highlights
- **Block CRUD API + Studio GUI** — Create / edit / delete building blocks (pipeline,
  agent, quality, pack) from the Blocks page with kind-aware visual editors; editing a
  builtin creates a project override; deleting a builtin is protected.
- **Pipeline Library + visual builder** — Browse, select, create, edit, and delete
  pipelines in the GUI; visual editors for groups, phases, execute loop, and slots with
  agent pickers; deleted phases archive as `when: never` (restorable) instead of
  resurrecting from defaults.
- **Agent blocks as runtime roles** — `go-tester` / `go-worker` / `python-tester` and
  every registry agent block are real registered roles; `execute.default_role` is
  honored; unknown roles fall back to generics with a warning.
- **Language pinning (no more pytest in Go runs)** — Project language is injected into
  tester / worker / reviewer / QA-gate / placeholder / interview prompts and knowledge
  cards are filtered by language; `when: never` / `enabled: false` is honored for all
  13 agent-driven phases.
- **Live feedback** — Send free-form steering from the Studio Live page or the TUI
  (`/feedback <text>`); it is injected into the next agent call as highest-priority
  instructions. New `GET/POST/DELETE /api/feedback`.
- **Skills ↔ Agents cross-linking** — Attach skills to agents and select agents in
  skills with visual multi-select chips in the GUI.
- **Update notifications** — TUI banner, `slmcode version`, `slmcode update --check`,
  and a Studio banner notify when a newer release is available
  (`GET /api/update`).
- **CLI parity** — `slmcode blocks new|edit|delete <kind> <id>`.
- **Hardening** — pipeline validation (duplicate groups, unknown steps), HTTP tests for
  the block API, skills edit modal, agent editor as a modal.

## v0.10.1 — LiveView Pipeline Progress, Task Management & Context Injection

### Highlights
- **Pipeline Progress Strip** — Visual tracker showing 5 groups (Prepare→Design→Build→Verify→Finish)
  with 16 colored phase dots, active pulse animations, and completed checkmarks.
- **Stats Dashboard** — Real-time phases completed, active agent, tasks in-flight, events count.
- **Active Agent Panel** — Current agent with description + recent events during runs.
- **LiveTaskPanel** — Tabbed right sidebar with full task CRUD (add/edit/delete),
  context injection (CONTEXT.md editor), and worker precision temperature slider.
- **Collapsible Event Log** — Toggle to show/hide the streaming event feed.
- **Tabbed Right Sidebar** — Tasks tab + Results tab for better information organization.
- All existing functionality preserved: SSE streaming, run/stop, specialist picker,
  config badges, event scrolling, result summary.

### SLM Optimizations

- **JSON repair improvements** — The repair engine now handles Python-style boolean
  literals (`True`/`False`) by normalizing them to JSON `true`/`false` before parsing.
  Trailing text after closing braces — a common SLM artifact where the model appends
  commentary after completing JSON output — is stripped automatically, reducing parse
  failures on partial or over-eager completions.
- **Tester gate robustness** — Regex-based pass detection scans tester output for 20+
  known shell/test-framework success markers (`PASS`, `ok`, `success`, `tests passed`,
  `All tests passed`, `0 failures`, green check variants). This makes the tester agent
  reliably recognize passing test runs regardless of framework (Go test, pytest, Jest,
  etc.) or output format quirks.
- **Worker status detection** — Smarter heuristics for detecting worker completion
  status from partial, malformed, or tool-chain-terminated output. Reduces false
  "incomplete" classifications when the worker produced valid changes but the final
  JSON was truncated or blocked by a tool call.
- **Improved tester prompt** — Tuned tester agent system prompt for better SLM
  adherence: explicit pass/fail criteria, shell command expectations, and a
  structured output schema that small models can follow more consistently.
- **LiveView enhancements** — Pipeline progress strip now reflects slot insertions
  (before/after/replace) with distinct styling. Group labels remain sticky during
  scroll in long runs. Event cards include tool-call metadata (tool name, args
  summary) inline.
- **HITL popup** — New modal overlay in Studio for escalate, clarify, continue, and
  shell-permission decisions. Replaces the inline prompt pattern with a focused,
  non-dismissible dialog that includes a visible countdown timer, action buttons,
  and contextual metadata (affected task, agent, retry count).
- **File inspector** — New Studio page for browsing workspace files with syntax
  highlighting, line numbers, and a diff view against the last checkpoint snapshot.
  Supports read-only inspection of any file in the project tree during or after
  runs — useful for verifying worker edits without leaving the Studio.

---

## v0.10.0 — Building Blocks, Language Packs & One-Click Pipeline Switching

### Highlights
- **Building Blocks system** — Marketplace-ready YAML presets: pipelines, agents, quality packs,
  language packs. Four block kinds with `api_version: blocks/v1` schema.
- **Predefined language packs** — 🐹 Go, 🐍 Python, ⚛️ React/TypeScript ready to use.
  Each pack includes tuned pipeline, language-specific worker/tester agents, and quality gates.
- **Blocks CLI** — `slmcode blocks list|show|apply|validate` — full block lifecycle management.
- **Chat REPL commands** — `/blocks` lists all blocks, `/pack <id>` applies a language pack.
- **BlockManager UI** — New Studio page: tabbed browser for all blocks, one-click apply,
  active indicators, source badges (builtin/custom).
- **PipelineEditor enhancement** — Preset selector for one-click switching between
  Go/Python/React pipelines directly from the pipeline editor.
- **PackSelector in Settings** — Switch language packs from the Settings page,
  alongside the existing Stack Selector.
- **Active config indicators** — LiveView and Sidebar now show active pack, pipeline,
  and stack badges during runs.
- **AGENTS.md** — Comprehensive 416-line contributor guide at project root.
- **18 blocks tests** — Full test coverage: registry loading, validation, catalog filtering,
  quality detection, QA gate resolution, meta validation, edge cases.

### Fixes
- Import cycle resolved: `blocks → agents → workspace → quality → blocks`.
  Quality smoke detection now delegates to `blocks.ResolveQAGateCommand` in orchestrator layer.
- `active_pack` and `active_pipeline` fields added to config schema and Studio Config type.

---

## v0.9.0 — Stacks, auth store & strict-provider ReAct

### Highlights
- **Stacks CLI + Studio** — `slmcode stack list|show|apply`, `GET/POST /api/stacks…`,
  Settings → Model Stack; hierarchy `stack → agent pin → runtime`
- **Per-agent LLM pins** — `slmcode agent …`, `effective_model` / `effective_provider` on APIs
- **Models catalog** — `GET /api/models`, `find_models` tool, costs + enabled_models allowlist
- **Auth store** — `.slmcode/auth.json` via `/auth`, `PUT /api/auth` (keys out of config.yaml)
- **Prime-agent ports** — LLM compaction engines, session `events.jsonl`, MCP status,
  auto-refine after waves, config schema (`GET /api/config/schema`)
- **DeepSeek** — default endpoint `https://api.deepseek.com` (client appends `/v1`)
- **GoLangGraph v0.2.2** — ReAct appends `role=tool` messages; no finalize race that
  skipped `act` (fixes DeepSeek 400 “insufficient tool messages”)
- **Success semantics** — historical `ESCALATED…` notes on **done** tasks no longer fail a green run

### Live smoke
- `RUN_E2E=1 go test ./test/e2e/ -run TestLiveStacksOMLXAndDeepSeek` (omlx-local + deepseek)

---

## v0.8.3 — Studio React style fix

### Fixes
- Studio Quick-start footer used HTML string `style` attrs → React error #62 crash
- `make lint` gofmt ignores `.slmcode/` workspace artifacts

---

## v0.7.3 — Incomplete finalize recovery

### Highlights
- Detect empty finalize and synthetic `model ended on a tool call` blocked JSON
- Up to two finish-steer corrector passes (demand status JSON, stop tool chains)
- Provisional done from disk/tool evidence when finalize still fails
- Knowledge cards: Python / Go project bars for language expectations

---

## v0.7.2 — Escalate timeout → SLM arbitrator

### Highlights
- On escalate HITL timeout, dedicated **@escalate** agent decides
  `retry` / `re_scope` / `abort` / `mark_done` (override via `escalate_timeout_agent`)
- Heuristic fallback when the LLM is unavailable (stubs → retry, vague → re_scope)
- Docs: Studio / TUI / Agents / Architecture / Config cover escalate + runnable QA bar
- CI: trailing-whitespace fix in `docs/pipeline.md`

---

## v0.7.1 — Runnable quality bar + escalate HITL

### Highlights
- Greenfield Python QA defaults to **pytest** (fail closed), not `compileall`
- **Acceptance smoke** — whitelisted commands from task acceptance run after each worker
- Worker critique loops until smoke/static/acceptance green (bounded by `max_retries`)
- Run success requires a **strong** QA gate — syntax-only cannot rubber-stamp incomplete boards
- **Escalate HITL** — Studio modal + TUI `/escalate`; options: re-scope / retry / mark done / abort

### API
- `GET` / `POST` `/api/escalate/pending|answer`

---

## v0.7.0 — Pipeline control + reference quality

### Highlights
- **Config-driven pipeline** (`.slmcode/pipeline.yaml`) — bind any agent to any phase,
  configure execute-loop reviewer/corrector, insert slots before/after/replace phases
- **Studio Pipeline tab** — edit phases/slots live; progress header follows config dynamically
- **Reference-quality bar** — project completeness gate blocks TestSLMs-style false success
- **Real-query eval suite** — offline harness + optional live LangGraph / FastAPI / CLI cases
- **Loop feedback** — SSE `kind=loop` with wave reasons + continue/abort/flag HITL
- **Placeholder polish** — `@placeholder` pass + precise gap flagging
- Custom agents accepted in specialist mode and board role dropdowns

### API
- `GET` / `PUT` `/api/pipeline`
- `POST` `/api/pipeline/reset`
- `GET` / `POST` `/api/continue/pending|answer`

### Docs
- New [Pipeline](pipeline.md) guide; updates to Studio, Agents, Config, Architecture

---

## v0.6.0 — SLM quality ports + eval

Harness gates, interventions, turn meter, `slmcode eval`, and Studio/TUI polish.
