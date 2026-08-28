# Changelog

## Unreleased

Two virtual dev teams, per-task specialist routing, greenfield scaffolding that
works, and defects that arrive as tickets instead of alarms.

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
  type is now labelled a **guess** — it is assembled from the query text alone,
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
