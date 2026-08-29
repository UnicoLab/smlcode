# 🎨 Frontend: assemble or write

> Two ways to build React UI. SLMCode picks one from evidence, tells you which, and lets you override it in the request.

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🧩</span>
<p class="slm-banner__text" markdown>
<strong>Hand-writing a dialog is where a small model spends its runway.</strong> Focus traps, keyboard handling, ARIA roles — a 7–32B model burns its whole budget there and still ships something inaccessible. Installing a reviewed component and wiring it up is a different task shape: imports, props and layout, which is exactly what these models are good at.
</p>
</div>

---

## The three methods

| Method | Agent | What it does |
|---|---|---|
| **shadcn/ui assembler** | `shadcn-worker` | `npx shadcn@latest add …`, then wires and styles |
| **Untitled UI assembler** | `untitledui-worker` | `npx untitledui@latest add …`, then wires and styles |
| **From scratch** | `react-worker` / `worker` | Writes components by hand, matching the project's patterns |

Nothing to install and nothing to enable. Both assemblers ship bundled, are
registered at startup, and their install commands are on the shell allow-list by
default.

---

## How the method is chosen

Highest precedence first:

1. **Your request.** Name a library and you get it. Say *from scratch* (or
   "no component library", "hand-write", "vanilla react") and you get neither.
2. **The project.** A `components.json` beside a `components/ui/` tree means
   this project already chose shadcn; `@untitledui/icons` in `package.json`
   means it chose Untitled UI. Adding a hand-written Button next to twenty
   installed ones is a duplicate, not a style.
3. **Greenfield defaults to assembling.** New frontend work has nothing to match
   and nothing to override, and this is where reuse buys the most.
4. **Everything else writes by hand.** An existing React app with no library
   markers has house patterns to match; introducing a component library into it
   is a migration nobody asked for.

The run says which it took, and why:

```
· init frontend: shadcn-worker — this project already uses shadcn/ui
· init frontend: shadcn-worker — new frontend — assembling from shadcn/ui
                 instead of writing components by hand (say "from scratch" to opt out)
```

### Choosing explicitly

```bash
slmcode run "build the settings page with shadcn"
slmcode run "build the settings page with Untitled UI"
slmcode run "build the settings page from scratch"
```

To pin a project permanently — pipeline, quality gates, reviewer and all:

```bash
slmcode blocks apply shadcn
slmcode blocks apply untitledui
slmcode blocks apply react
```

---

## What the assemblers know

Both agents carry the library's real CLI contract, because getting it wrong
costs a whole turn on a local model:

- **shadcn's flag defaults are inverted between commands.** `add` needs `-y`;
  `init` already has it. And `init` needs an explicit `-b` — without it the CLI
  stops on an interactive *Select a component library* menu even with `-y`, and
  the turn hangs until it times out.
- **Untitled UI matches names fuzzily and wrongly.** `buttons` installs
  `app-store-buttons`; `badge` installs `badge-groups`. Neither errors — you get
  a different working component. Exact names only.
- **`data-table`, `date-picker` and `typography` are shadcn documentation
  guides, not installable names.** `toast` is deprecated in favour of `sonner`.
- **Untitled UI's page templates are a paid tier.** The free CLI covers 122
  components; the assembler never depends on `untitledui example`.

Whole pages often install as one block — `login-01`, `sidebar-01`,
`dashboard-01`, `calendar-01` — which is the single biggest saving available.

---

## The reviewers

Each assembler pack ships a reviewer whose job is the one failure this feature
exists to prevent: **a component written by hand that the registry already
ships.** They reject a local Button, Dialog, Card, Table or Select built from
divs, and they reject edits to `components/ui/*` made to restyle one page —
per-page styling belongs at the call site, via `className` or the component's
own variant props.

They deliberately do **not** review the installed component's own code. It is
upstream's, already reviewed, and not the task's work.

---

## Security

`npx` as a whole remains an executor that needs explicit operator approval. What
is allowed by default is five exact subcommands of two named packages:

```
npx shadcn@latest add · init · info
npx untitledui@latest add · init
```

Both CLIs accept a URL or an `@registry` reference in the same argument position
as a component name, which resolves to a registry that is not the official one
and writes whatever files it serves into your repository. SLMCode refuses those:

```
npx shadcn@latest add @acme/button          → refused
npx shadcn@latest add https://…/payload.json → refused
```

Destructive subcommands stay behind explicit approval, because they rewrite
config and existing files rather than adding new ones: shadcn's `eject` and
`migrate`, Untitled UI's `upgrade` and `example`.

---

## Why typecheck is the QA gate

Both assembler packs gate on `npx tsc --noEmit`, not `npm test`. A freshly
scaffolded UI project ships `dev`, `build`, `lint` and `typecheck` scripts and
**no test script** — measured on a real `shadcn init -t vite`. There, `npm test
--silent` exits 1 with empty output, which is indistinguishable from a real
failure, and a gate that can never go green spends the entire run in correction
loops instead of building anything.

Typecheck is also the honest question for assembly work: the components were
tested upstream, and what a run can actually get wrong is whether they resolve,
whether the props match, and whether the page compiles. A task that *asks* for
tests still gets them — the tester runs `npm test` when a script exists, and
per-task acceptance criteria verify them by name.

---

## Requirements

Node ≥ 20.18.1, and network access to the npm registry plus each library's
component registry — both CLIs fetch on every invocation. Both libraries target
React 19 and Tailwind CSS v4, and both require path aliases (`@/components/…`)
in `tsconfig.json`.
