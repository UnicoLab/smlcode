# 🎨 Studio

Studio is the local web cockpit: a live run feed, the kanban board, a pending-change review UI, a
run trace, and editors for the pipeline, agents, skills and markdown memory. It ships as a
Vite + React + TypeScript SPA embedded in the binary — no CDN, no network at runtime.

```bash
slmcode studio                      # → http://127.0.0.1:7420/?t=<token>, opens a browser
slmcode studio --listen :9000       # custom address
slmcode studio --kill               # terminate an existing slmcode holding the port
slmcode studio --no-port-auto       # fail instead of moving to a free port
slmcode studio --dev-cors           # allow the Vite dev server (npm run dev in web/)
slmcode studio --no-auth            # drop the session token (loopback enforcement stays)
```

The printed URL carries the session token. Open **that** URL, not a bare
`http://127.0.0.1:7420` — the HTML shell is authenticated too, so an untokenised navigation gets a
401 page telling you to go back to the terminal, and every `/api/*` call gets a bare 401. The CLI
states which mode it is in (`auth: session token required (the URL above carries it)`, or a
warning that auth is disabled). See [Security model](#security-model) for the cookie the token
mints and for an honest account of what it does and does not protect against.

`Ctrl+C` shuts down gracefully: an in-flight run unwinds and every SSE stream closes, rather than
responses being truncated mid-write.

If the configured port is busy, Studio moves to the next free one and says so. Killing whatever
holds the port is never automatic: `--kill` only ever signals a process whose executable is
exactly `slmcode`.

The listen address comes from `listen` in config (`127.0.0.1:7420` by default) unless
`--listen` overrides it.

---

## Building the UI

Studio's front end is a React 18 + Vite + TypeScript SPA in `web/`. `make ui-react` builds it and
copies `web/dist/*` into `cmd/slmcode/ui/`, which the binary embeds with `//go:embed all:ui`.
Released binaries ship it already built; a binary you built yourself does not have it until you
run:

```bash
make bootstrap      # installs web/'s npm dependencies (Node 22+), then builds the UI
make build
```

Without that, everything still works except the web page: `slmcode studio` starts, serves the API,
prints its tokenised URL — and warns on startup that the UI is not built. The page you get says
the same and gives the command. That placeholder is compiled into the server (`pkg/server`), not
checked into `cmd/slmcode/ui/`: the only tracked file there is `.gitkeep`, so building the UI never
dirties a tracked file, and `go:embed` still has something to embed on a fresh clone.

!!! warning "`web/package-lock.json` is out of date"
    `web/package.json` gained `vitest`, `@testing-library/*` and `eslint`; the lock predates them,
    so `npm ci` refuses to run. `make bootstrap` reports this and falls back to `npm install`,
    which **regenerates `web/package-lock.json`** — commit the regenerated lock.
    See [Troubleshooting](troubleshooting.md#studio-ui-wont-build).

For UI work: `cd web && npm run dev` (Vite dev server), then `make ui-react` to fold it back into
the binary. `make web-check` runs the SPA's lint, typecheck, tests and build.

---

## Pages

| Route | Page | What it does |
|---|---|---|
| `/` | **Live** | The run as a place: the **team floor** (each team at its table with its manager, members, monitors and tickets; contract conduits between tables; a spark when a manager moves a ticket), the **phase journey** above it, a ticker of what is happening right now, and one **activity** column beside it — the log by default, with tasks, fixes, files and the result as filters |
| `/board` | **Board** | Kanban — add, edit, delete, move, delegate, drag mid-run |
| `/review` | **Review** | Pending changes from `permission: review`, as diffs, with per-file apply/reject |
| `/runs` | **Runs** | Run history, and a per-run **trace** with per-phase wall time and token/cost attribution |
| `/pipeline` | **Pipeline** | Edit the phase graph, bind agents to phases, insert slots, configure the execute loop |
| `/agents` | **Agents** | Create/edit/delete custom specialists with a full prompt editor |
| `/teams` | **Teams** | The [team library](squads.md#the-team-library) — build teams from existing agents and give each a project manager, send a request to teams and see who would work on it and how, edit the org chart and its frozen contract, and read how the managers and teams collaborated on a run |
| `/blocks` | **Blocks** | Browse and apply pipeline / agent / quality / pack / team blocks |
| `/files` | **Files** | Workspace tree browser, read-only, with diff against the last checkpoint |
| `/skills` | **Skills** | Manage `SKILL.md` packs |
| `/docs/:id` | **Docs** | Split-pane markdown editor for CONTEXT / PLAN / TASKS / SCRATCH / MEMORY |
| `/settings` | **Settings** | Provider, model, stacks, packs, HITL modes, parallelism, MCP, API keys |

A global **HITL modal** surfaces clarify / plan-approve / continue / escalate / shell gates from
any page — you no longer have to be on the Live view to answer one. It traps Tab and answers Esc
by returning focus to the decision (a gate cannot be dismissed — the harness is waiting). A
**connection badge** shows stream health, and an error boundary keeps one broken panel from
blanking the app.

### When a task gets stuck

A blocked or failed card tells its story instead of showing one error line: the **Attempts**
timeline (`attempt_log` — "attempt 2 failed because …"), the gate-retry count, the **review
verdict**, and the structured **criteria** checklist. Under it, a next-step row:

| Action | What it does |
|---|---|
| **Send back to Ready** | `PATCH /api/tasks/{id}` → column `ready_to_dev`; the next wave picks it up |
| **Retry** | `POST /api/tasks/{id}/retry` — same, keeping the attempt log so the next try knows what failed (falls back to the Ready move on a server without the endpoint; `409` means no board is loaded) |
| **Reassign team** | opens the edit form on the team control |
| **Steer** | prefills the live-feedback composer with `@task:ID ` — from the board it navigates to Live first |

The same row appears in the Live task panel, whose **Blocked / Failed** counters are filters over
the list. The panel accepts a `focusTaskId` prop (and Live reads `/?task=ID`) to expand, scroll
to and flash one task.

### When a run ends

The result panel's **What next** row offers whichever of these apply: **Resume interrupted run**,
**Open blocked on Board** (`/board?column=blocked`), **Review N pending**, and **Run again with
this prompt**. The app also fires a toast, flashes the tab title while the tab is hidden, sends a
browser notification when the tab is hidden *and* permission was already granted (Studio never
asks for it on its own), and throws a two-second confetti burst on a green run — not under
`prefers-reduced-motion`.

### Keyboard

| Keys | Does |
|---|---|
| `?` | shortcut sheet |
| `⌘K` / `Ctrl+K` | command palette — pages, tasks by id or title, agents, teams, recent runs, theme |
| `/` | focus the run prompt |
| `⌘↵` / `Ctrl+Enter` | run the prompt, while it is focused |
| `⌘.` / `Ctrl+.` | stop the active run, while the prompt is focused |
| `g` then `l` `b` `r` `p` `a` `t` `k` `f` `s` `h` `,` | go to Live, Board, Review, Pipeline, Agents, Teams, Blocks, Files, Skills, Runs, Settings |
| `Esc` | close a dialog, the sheet or the palette |

Plain-key shortcuts are inert while typing in a field; the modifier ones are not.

---

## The Live floor

The centre of the Live view is a 3D floor, drawn with three.js: one round table
per team on its own rug, the manager at the head with the team's board on the
wall behind them (a column per state, a tile per ticket, the counts above), the
members around the table, each with a monitor that lights up when the log says
that agent is working and a pop-out over their head naming the ticket. Tickets
lie on the table, colored by state, and a thread runs from each one to the
monitor of whoever holds it, with beads travelling along it while they type.
The frozen contract runs between tables as conduits with packets flowing from
the provider to the consumer; a consumer waiting on a clause it has not been
given turns its conduit amber. A gate paints the table's rim green or red.

Whoever is working is unmistakable: their monitor lights up, lines of code
rise off the screen, a pool of light and a breathing ring mark the seat, and
the pop-out over their head says which ticket and what they just said. Several
people can be at it at once — one per ticket in flight — and idle people glance
at whoever is. The pipeline's own people — the planner, splitter, architect,
explorer and the rest, who sit at no table — stand on a **stage** at the left
under a screen naming the phase the run is in and what is being said; the one
speaking is lit, the rest wait in the wings until their phase.

The floor moves when the run does. A manager sending someone onto a ticket is
an arc from the head seat with the ticket's name; a ticket moving from one
person to another is a spark across the table with the reason; a ticket that
appears drops onto the table, one that finishes or fails bursts; idle people
glance at whoever is working. Every change also slides in as a card in the
**feed** at the right edge — *T4 appeared on Backend*, *go-worker started on
T2*, *T2 done ✓*, *Backend: gate green* — for twenty seconds, and each card is a
link.

Everything is clickable. A person opens their **dossier**: seat and team, who
manages them (or, for a manager, who they manage and what they do), whether they
are working right now and on what, the last thing they said, the tickets they
hold and have touched, and their recent lines from the log. A ticket opens its
own: state, holder, everyone who has touched it, any handoff, and *open in
Tasks*. Every name in a dossier is a link to the next one, so the floor can be
explored by clicking around; the camera glides to whatever is selected. Drag to
orbit, wheel to zoom, right-drag to pan, click a table to frame it, **follow**
to keep whoever is working in the middle, **spin** for a slow tour, Esc or a
click on empty floor to clear.

A run with no org chart shows the **pipeline crew** — the planner, splitter,
worker, reviewer and tester the composition chose — at one table, and a single
library team staffing a run shows as that team, with its manager, board and
gate. A seat the team left empty and the pipeline fills (a tester on a team that
names none) sits at the table too, drawn as borrowed and labelled *from the
pipeline*.

Where WebGL is unavailable, or on request (the **3D / map** toggle), the same
floor is drawn as a flat map with the same dossier and feed. Both honor
`prefers-reduced-motion`. On a touch device with no stored preference the map
is the default; below the `sm` breakpoint the toggle sits in a strip under the
floor and the feed opens from it as a bottom sheet. Every person and ticket in
the 3D scene is also a visually hidden button, so the floor can be walked with
a keyboard: a dossier takes focus when it opens and gives it back when it
closes, and `Esc` closes it unless it was typed into a field.

The scene keeps its state between visits. The **selection** lives in the URL —
`/?task=T4` opens a ticket's dossier, `/?agent=go-worker` a person's, with an
optional `&team=` — so a dossier survives navigating away, a reload, and being
pasted to someone. The camera, **follow**, **spin** and the pulse feed live in
a module-level store (mirrored to `sessionStorage`), so coming back from the
Board finds the floor where it was left; **reset view** restores the camera
through OrbitControls rather than remounting the scene. The render loop runs
continuously only while something moves (a run, live tickets, fresh pulses,
follow, spin, a camera glide), drops to on-demand when the floor is still, and
stops while the tab is hidden. If the browser loses the WebGL context the page
falls back to the flat map and says so.

## One board, one stream

Every page that shows tasks reads the same **board store** (`web/src/hooks/
useBoardStore.ts`, provided from `App`): the Board, the Live floor, the ticker
and the Teams page. It is seeded once from `GET /api/tasks` and
`GET /api/squads`, then kept current by the live stream, and re-read every
30 s as a safety net, on both edges of a run, and when the stream reconnects.
Before the server's first push, a structural log line (a task starting or
finishing, a run starting or ending) also triggers one debounced re-read, so a
server without the events below still feels live. Pages call `refresh()`
after a write and `upsertTask()` for the optimistic paint. Nothing polls on
its own timer any more; the Sidebar's review badge comes from the stream too.

Two SSE kinds are **board events**: they are folded into the store and never
become rows in the log, since one row per column move would bury the story
the log tells with `task_start` / `task_done`:

| Kind | Payload | Effect |
|---|---|---|
| `task_update` | `task_id`, `data.task` — the task exactly as `GET /api/tasks` returns it | replaced by id in the store (appended if new); marks the store *live* |
| `review_pending` | `data.pending` — the review queue's length | updates the Review badge at once |

Their sequence ids still advance the reconnect cursor, and a replayed one is
dropped like any other duplicate.

The stream also keeps a **running derivation** of the log (`web/src/hooks/
runDerived.ts`): phases seen, the active phase and agent, task ids, files
written, token and cost totals, the newest composition — folded once per event
as it arrives rather than re-scanned by each panel on every flush. The
ticker's clock keys on the floor model's working set (an agent between its
`agent_start` and `agent_end`), so it stops when nobody is working.

### Links between pages

Identifiers are chips that go somewhere (`web/src/components/shared/
EntityLink.tsx`):

| Kind | Goes to |
|---|---|
| task | `/?task=ID` — the ticket's dossier on the floor, and the Tasks rail |
| agent | `/?agent=ID` — the person's dossier |
| team | `/teams?team=ID` |
| run | `/runs?run=ID` |
| file | `/files?file=path` |

The Board keeps its own filters in the URL as well: `?team=ID` (or the
unassigned lane) and `?column=ID`.

## The review workflow

Set `permission: review` and agent writes stop being writes — they become proposals.

```bash
slmcode config set permission review
slmcode run "add JWT validation"
```

Each proposal is a `.slmcode/pending/<nano>_<kind>_<mangled-path>.patch.json` holding
`{path, kind, content}` plus, when the workspace knew them at record time, `from` (the source of a
`ws_mv`), `task_id`, `agent` and `query_id`. Studio's **Review** page lists them with both sides of
the diff and per-file apply/reject; the same queue is available from the terminal with
`slmcode apply` and `slmcode reject`.

**Apply honors the kind.** `write`/`edit`/`patch` write the content; `delete` removes the file;
`mv` moves the source to the destination (falling back to writing the recorded content if the
source is gone). Stale `shell` entries from older builds are never files: they are hidden from the
listing, skipped by `{all: true}` and refused by id.

| Endpoint | Purpose |
|---|---|
| `GET /api/review/pending?hunks=1&context=3` | list pending changes, optionally with hunks. Items carry `kind`, `from`, `task_id`, `agent`, `query_id` (empty when unknown) |
| `GET /api/review/pending/{id}` | one change with its diff (a `delete` diffs to empty; an `mv` diffs source → content at the destination path) |
| `POST /api/review/apply` | `{ids}` / `{id}` / `{all: true}` — emits `review_pending` |
| `POST /api/review/reject` | same shape — emits `review_pending` |

The queue id is a bare file name and is validated as one — a traversal attempt is rejected rather
than resolved.

### Board, tasks and history

| Endpoint | Purpose |
|---|---|
| `GET /api/tasks` | the live board; each task includes `attempt_log`, `gate_retries` and `criteria` when set |
| `POST /api/tasks/{id}/retry` | move a task back to `ready_to_dev`, reset `retries`, clear `error`, append `"retried from Studio"` to `attempt_log`, persist, emit `task_update`. Returns the task. `409` when no board is loaded, `404` for an unknown id |
| `GET /api/queries` | run history; every item also carries `duration_ms`, `tokens`, `cost_usd`, `tasks_total`, `tasks_done`, `failed_tasks` and `teams` (string list), computed from the stored turn, board and event log (usage totals are cached per log size/mtime) |
| `GET /api/shell/pending` | `{pending, ask, asks: [...], count}` — `ask` is the oldest open shell ask (the shape the UI was built on); `asks` lists every open one, since a parallel wave can raise several |
| `POST /api/shell/approve` | `{ask_id, decision: "approve" \| "deny"}` — `ask_id` picks which pending ask; it may be omitted only when exactly one is pending |
| `POST /api/runs/stop` | asks the run to unwind. `running` stays `true` (with `stopping: true` in `/api/runs/latest` and `/api/status`) until the run goroutine has exited, so a new run cannot start on top of the old one's teardown |

---

## Live events (SSE)

`GET /api/events` is a long-lived Server-Sent Events stream.

- Every event carries a monotonic **id**. A reconnecting `EventSource` sends `Last-Event-ID`
  automatically (or you can pass `?last_event_id=`), and only receives what it missed.
- The replay ring buffers 1500 events. Token-delta events are evicted first, so a long streaming
  response cannot push the structural timeline out of the buffer.
- When events genuinely could not be replayed, an explicit `event: gap` frame is emitted with
  `{from, to}`, so the UI can say *"events N–M were dropped"* instead of quietly showing an
  incomplete run. A slow consumer is flagged rather than silently dropped.
- Kind `run_end` (phase `done` or `error`, message = the summary) is what the run-end toast,
  title flash and notification key on. Kind `review_pending` (`{pending: N}`) tells the Review
  page to refresh while a run is still writing; without it the page polls every 15 s during a run.
- The ring is **not** cleared when a run starts. `GET /api/runs/latest` scopes its snapshot to the
  current run by sequence number, while a reconnecting stream can still replay across a
  stop → start.

Two kinds are synthesized by the server itself rather than emitted by the engine, and they sit in
the ring and replay like every other event:

| Kind | When | Shape |
|---|---|---|
| `task_update` | after any board task change (worker moves, a `PATCH /api/tasks/{id}`, a retry) | `phase` = the run's current phase, or `"board"` when no run is active; `task_id`; `message` = `"<id> -> <column>"` (`"<id> -> removed"` for a deletion); `data` = `{"task": {…task exactly as GET /api/tasks renders it…}}` |
| `review_pending` | after a proposal is recorded by a running agent, and after every apply/reject | `phase` = `"review"`; `data` = `{"pending": N}` — the number of applicable entries in the queue |

`GET /api/queries/{id}/events` replays a recorded run's log, and `GET /api/queries/{id}/trace`
groups it into contiguous phase segments with totals — the numbers that matter when tuning a
small local model.

---

## Security model

Studio is a **local agent** with file read, config write, API-key write and run-start capability.
Three independent layers protect it, and none of them is optional-by-accident.

### Loopback only

A request whose `Host` is not `127.0.0.1`, `::1` or `localhost` is rejected with 403. This is what
blocks DNS rebinding, where a hostile page resolves its own domain to 127.0.0.1 and then talks to
your agent. `AllowNonLoopback` exists only for deliberate exposure behind an external
authenticating proxy.

### Same-origin only

No `Access-Control-Allow-Origin` header is emitted at all for ordinary use — the previous
`Access-Control-Allow-Origin: *` let any page you happened to visit read Studio's responses.

- A cross-origin `Origin`, or a `Sec-Fetch-Site: cross-site` request, is refused.
- When an origin *is* allowed, only that exact origin is echoed — never `*`.
- `--dev-cors` allows exactly the Vite dev origins (`http://127.0.0.1:5173`,
  `http://localhost:5173`, `http://[::1]:5173`) and nothing else. Studio warns on startup when it
  is on.

Every response also carries `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff` and
`Referrer-Policy: no-referrer` — the last because the URL can carry a token.

### Session token

`slmcode studio` mints a random 256-bit hex session token per launch and prints it in the URL:

```
✔ Studio listening
  url    http://127.0.0.1:7420/?t=8f3c…
  auth   session token required (the URL above carries it)
```

Open **that** URL. Everything is behind the token — **including the HTML shell**. A bare
`http://127.0.0.1:7420/` does not load Studio; it gets **401** and a static page that says to open
the URL the CLI printed. `/api/*` without a token gets a 401 JSON-less body and
`WWW-Authenticate: Bearer realm="slmcode-studio"`.

#### How a browser gets authenticated

1. You open the CLI's `?t=<token>` URL.
2. The server validates the parameter and replies with a session cookie:

   ```
   Set-Cookie: slmcode_studio=<token>; Path=/; HttpOnly; SameSite=Strict
   ```

3. From then on the cookie authenticates every request — page loads, `fetch`
   (`credentials: 'same-origin'`) and `EventSource` alike. The SPA strips `?t=` from the address
   bar on first read, so the token stops appearing in history, screenshots and shoulder-surfing
   range.

The cookie is deliberately shaped:

| Attribute | Why |
|---|---|
| `HttpOnly` | keeps it out of `document.cookie`, so an XSS in a rendered diff cannot exfiltrate it |
| `SameSite=Strict` | it is never attached to a request originated by another site |
| `Path=/` | one cookie covers the SPA and `/api/` alike |
| no `Secure` | Studio is plain HTTP on loopback; a `Secure` cookie would simply be dropped |
| session cookie (no `Max-Age`) | closing the browser drops it; re-open the CLI's URL to re-issue |

A non-browser client (curl, a script, another agent) can present the token directly instead:

| Transport | Form |
|---|---|
| Header | `X-SLMCode-Token: <tok>` |
| Header | `Authorization: Bearer <tok>` |
| Query | `?t=<tok>` — for `EventSource`, which cannot set headers |

Any of the three also mints the cookie, so a browser only ever needs it once.

!!! warning "The `<meta name="slmcode-token">` tag is gone"
    Studio used to serve `GET /` unauthenticated and inject the token into the HTML for the SPA to
    read. That made the shell an **unauthenticated token dispenser**: any other process on the
    machine could `curl http://127.0.0.1:7420/`, scrape the token out of the page and then drive
    the agent. There is no meta tag and no meta fallback any more, by design. If you built a
    client against it, read the token from the CLI output or set `SLMCODE_STUDIO_TOKEN` yourself.

#### Turning it off

`--no-auth` (or `SLMCODE_STUDIO_NO_AUTH=1`) drops the token requirement entirely: every request is
treated as already authenticated and no cookie is minted. Loopback and same-origin enforcement stay
on. The CLI prints `⚠ auth disabled — any local process can drive this agent`, which is exactly
what it means. Use it for a throwaway container, not on a machine you share.

`--dev-cors` (or `SLMCODE_STUDIO_DEV_CORS=1`) allows the Vite dev origins so `npm run dev` on
:5173 can talk to the API. It does not weaken the token: the dev server still has to present one,
which it does by proxying `/api` through the same origin.

Environment overrides, for embedders and tests:

| Variable | Effect |
|---|---|
| `SLMCODE_STUDIO_TOKEN` | use this token instead of a random one (handy for scripts) |
| `SLMCODE_STUDIO_NO_AUTH` | disable the token requirement — same as `--no-auth` |
| `SLMCODE_STUDIO_DEV_CORS` | allow the Vite dev origins — same as `--dev-cors` |

#### What the token actually buys you — and what it does not

Be precise about this, because the previous version of this page overstated it.

**It does bound:**

- any other **origin** — a page you visit cannot read Studio's responses, and `SameSite=Strict`
  means the cookie is never attached to its requests;
- an unprivileged process that can reach the port but **cannot read your terminal or your
  process's memory** — for example something in another container, another user's account on a
  shared box, or a service that only got a socket;
- accidental exposure through a proxy or a port-forward, since the URL alone is not enough without
  the `?t=` parameter.

**It does not bound another process running as you.** The token is printed to your terminal's
stdout and lives in the server process's memory. Anything with your uid can read your scrollback,
your shell history if you pasted the URL, `/proc/<pid>` on Linux, or simply the terminal
multiplexer buffer. On a single-user laptop the token is a good hygiene measure and a genuine
anti-CSRF/anti-rebinding control — it is **not** a sandbox against malware already running as you.

Loopback and same-origin are what stop a **remote** page. The token is what stops a **local
listener that is not you**. Neither stops **you**, or anything running with your privileges.

### Transport hardening

Plain `http.ListenAndServe` has no timeouts (gosec G114 / Slowloris). Studio sets
`ReadHeaderTimeout: 10s`, `IdleTimeout: 120s` and `MaxHeaderBytes: 1MB`. Read and write timeouts
stay zero **deliberately**: `/api/events` is a long-lived SSE stream, and any `WriteTimeout` would
cut it off mid-run. `ReadHeaderTimeout` is what actually bounds a header dribble. Shutdown is
graceful — in-flight requests drain instead of being severed.

### Path safety

`GET /api/workspace/file` and `/api/workspace/tree` resolve every path against the real workspace
root with symlinks evaluated, so neither `..` nor a symlink inside the tree escapes it.

---

## Frontend development

```bash
cd web
npm install
npm run dev          # Vite on :5173, proxying /api → :7420
```

The dev server is a **different origin** from the API, so start the backend with
`slmcode studio --dev-cors` (or `SLMCODE_STUDIO_DEV_CORS=1`). Studio ships no CORS headers
otherwise.

| Script | Does |
|---|---|
| `npm run build` | `tsc -b` + production build into `dist/` |
| `npm run typecheck` | `tsc --noEmit` |
| `npm run lint` | typecheck **and** ESLint |
| `npm test` | Vitest + Testing Library |
| `npm run test:coverage` | Vitest with v8 coverage |

`react-hooks/exhaustive-deps` is an **error**, not a warning: a stale closure in the SSE handler
once reduced the live event log to a single row, and that rule is what catches it.

### Cross-component contracts

A few interactions cross component boundaries without a shared parent. They go through
`CustomEvent`s on `window`, all named in `web/src/components/ui/events.ts`. Every event is
dispatched `cancelable`; a listener that handles it calls `preventDefault()`, and the dispatcher
falls back to doing the work itself when nobody does.

| Event | Fired by | Listened to by |
|---|---|---|
| `slmcode:focus-prompt` | `/` | Live view — focuses the prompt |
| `slmcode:run-prompt` (`detail.query?`) | `⌘↵` in the prompt; the result panel's *Run again* | Live view — starts the prompt (or `detail.query`) with the chosen teams/specialist; the result panel calls `POST /api/runs` directly when unclaimed |
| `slmcode:stop-run` | `⌘.` in the prompt | Live view — `POST /api/runs/stop` |
| `slmcode:steer-task` (`detail.taskId`) | a task's *Steer* | `LiveFeedback` — prefills `@task:ID `; parked in `sessionStorage` when no composer is mounted |
| `slmcode:command-palette` | anything | `Layout` — toggles the palette |

The prompt input is recognised by its accessible name `Run prompt` (or a `data-run-prompt`
attribute). URL parameters that pages read: `/board?column=blocked` (Board), `/files?path=a/b.go`
(Files), `/runs?run=ID` (Runs), `/?task=ID` (Live → task panel `focusTaskId`),
`/?run=ID&replay=1` (Runs → *Replay on the floor*; harmless when Live ignores it).

Errors on every editor page go through `useToast().reportError`; list pages render
`ui/ErrorState` with a Retry in place of the list when their load fails. Tab strips use
`ui/useRovingTabs` (one Tab stop, arrows between tabs, `aria-controls` to the panel). Dialogs
share `useFocusTrap` from `ui/Modal.tsx`.

`make ui-react` builds and syncs `web/dist/` into `cmd/slmcode/ui/`, which is embedded with
`go:embed all:ui`. `make bootstrap` does the same but only when the assets are missing.

Studio downloads no webfonts. Typography uses the platform UI stack; drop
`inter-variable.woff2` / `jetbrains-mono-variable.woff2` into `web/public/fonts/` to opt into
Inter and JetBrains Mono locally.

---

## API surface

Roughly 60 endpoints under `/api/`, grouped: `health` · `readiness` · `config` (+ `config/schema`)
· `docs` · `tasks` · `board` · `columns` · `skills` · `runs` (start / stop / resume / latest /
interrupted) · `clarify` · `plan` · `continue` · `escalate` · `shell` (the five HITL gates, each
`GET …/pending` + `POST …/answer|approve`) · `rewind` · `compact` · `events` · `status` · `models`
· `auth` · `mcp` · `stacks` · `agents` · `pipeline` · `composition` · `blocks` · `packs` ·
`squads` (the current run's org chart) · `teams` (the library — CRUD, plus `teams/preselect`,
`teams/activate`, `teams/activity` and `teams/{id}/manager`) · `archives` · `queries` (+ `/events`, `/trace`) · `review` ·
`workspace/file` · `workspace/tree`.

`slmcode config schema` and `GET /api/config/schema` both emit the machine-readable config schema
the Settings page renders from — that is how Settings stays in sync with `config.Config`.
