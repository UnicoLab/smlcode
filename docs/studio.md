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
`http://127.0.0.1:7420` — without the token every `/api/*` call returns 401. The CLI states which
mode it is in (`auth: session token required (the URL above carries it)`, or a warning that auth
is disabled).

`Ctrl+C` shuts down gracefully: an in-flight run unwinds and every SSE stream closes, rather than
responses being truncated mid-write.

If the configured port is busy, Studio moves to the next free one and says so. Killing whatever
holds the port is never automatic: `--kill` only ever signals a process whose executable is
exactly `slmcode`.

The listen address comes from `listen` in config (`127.0.0.1:7420` by default) unless
`--listen` overrides it.

!!! note "Build it first"
    `cmd/slmcode/ui/index.html` is a checked-in **placeholder** so `go build` always succeeds.
    From a source checkout, run `make bootstrap` (or `make ui-react`) before `slmcode studio`, or
    you will get the placeholder page instead of Studio.

---

## Pages

| Route | Page | What it does |
|---|---|---|
| `/` | **Live** | SSE-streamed run: phases, `@agent` activity, token stream, event log, HITL modal |
| `/board` | **Board** | Kanban — add, edit, delete, move, delegate, drag mid-run |
| `/review` | **Review** | Pending changes from `permission: review`, as diffs, with per-file apply/reject |
| `/runs` | **Runs** | Run history, and a per-run **trace** with per-phase wall time and token/cost attribution |
| `/pipeline` | **Pipeline** | Edit the phase graph, bind agents to phases, insert slots, configure the execute loop |
| `/agents` | **Agents** | Create/edit/delete custom specialists with a full prompt editor |
| `/blocks` | **Blocks** | Browse and apply pipeline / agent / quality / pack blocks |
| `/files` | **Files** | Workspace tree browser, read-only, with diff against the last checkpoint |
| `/skills` | **Skills** | Manage `SKILL.md` packs |
| `/docs/:id` | **Docs** | Split-pane markdown editor for CONTEXT / PLAN / TASKS / SCRATCH / MEMORY |
| `/settings` | **Settings** | Provider, model, stacks, packs, HITL modes, parallelism, MCP, API keys |

A global **HITL modal** surfaces clarify / plan-approve / continue / escalate / shell gates from
any page — you no longer have to be on the Live view to answer one. A **connection badge** shows
stream health, and an error boundary keeps one broken panel from blanking the app.

---

## The review workflow

Set `permission: review` and agent writes stop being writes — they become proposals.

```bash
slmcode config set permission review
slmcode run "add JWT validation"
```

Each proposal is a `.slmcode/pending/<nano>_<kind>_<mangled-path>.patch.json` holding
`{path, kind, content}`. Studio's **Review** page lists them with both sides of the diff and
per-file apply/reject; the same queue is available from the terminal with `slmcode apply` and
`slmcode reject`.

| Endpoint | Purpose |
|---|---|
| `GET /api/review/pending?hunks=1&context=3` | list pending changes, optionally with hunks |
| `GET /api/review/pending/{id}` | one change with its diff |
| `POST /api/review/apply` | `{ids}` / `{id}` / `{all: true}` |
| `POST /api/review/reject` | same shape |

The queue id is a bare file name and is validated as one — a traversal attempt is rejected rather
than resolved.

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

`slmcode studio` mints a random 256-bit hex session token per run (`--no-auth` disables it).
Every `/api/*` request must then carry it, supplied as:

| Transport | Form |
|---|---|
| Header | `X-SLMCode-Token: <tok>` |
| Header | `Authorization: Bearer <tok>` |
| Query | `?t=<tok>` — for `EventSource`, which cannot set headers |

The SPA picks it up from the `?t=` parameter of the URL the CLI prints, or from the
`<meta name="slmcode-token">` tag the server injects into `index.html`, stores it in
`sessionStorage`, and strips the parameter from the address bar on first read
(`web/src/api/session.ts`).

Environment overrides, for embedders and tests:

| Variable | Effect |
|---|---|
| `SLMCODE_STUDIO_TOKEN` | use this token instead of a random one (handy for scripts) |
| `SLMCODE_STUDIO_NO_AUTH` | disable the token requirement — same as `--no-auth` |
| `SLMCODE_STUDIO_DEV_CORS` | allow the Vite dev origins — same as `--dev-cors` |

Loopback and origin enforcement stay on regardless of the token setting. The token is defense in
depth against **other local processes and users** on a shared machine; loopback and same-origin
are what stop a remote page.

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
`archives` · `queries` (+ `/events`, `/trace`) · `review` · `workspace/file` · `workspace/tree`.

`slmcode config schema` and `GET /api/config/schema` both emit the machine-readable config schema
the Settings page renders from — that is how Settings stays in sync with `config.Config`.
