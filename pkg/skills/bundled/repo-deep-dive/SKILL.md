---
name: repo-deep-dive
description: Describe an existing codebase as it really is — structure, entry points, docs checked against code — and turn it into ranked, located recommendations.
triggers: deep dive, current state, architecture, audit, onboarding, overview, maintenance, tech debt, legacy, existing code, what's going on
agents: worker, reviewer, tester, architect, explorer, architect-worker, docs-audit-worker, challenger-reviewer, insight-tester
paths: "ARCHITECTURE.md, MAINTENANCE.md, reports/insight/**"
user-invocable: true
---

# Repository deep dive

The goal is an account someone can act on tomorrow, not a tour. Every
sentence either locates something (a path) or judges something (a risk, a
recommendation) — ideally both.

## Read in layers, stop when a layer goes quiet

1. **Tree and build files.** `go.mod`, `package.json`, `pyproject.toml`,
   `Dockerfile`, CI workflows. They say what the project IS better than the
   README does: languages, entry points, how it is really built and tested.
2. **Entry points.** `main` packages, CLI commands, server bootstrap, the
   exported API. Follow one request end to end.
3. **Docs against code.** For each command, flag, config key or path the
   README names, find it in the code. A mismatch is a finding.
4. **Tests.** What runs in CI, what is covered, what is not.

## ARCHITECTURE.md — the current state

```markdown
# Architecture

<One paragraph: what it does, for whom, in what shape (CLI / service / lib).>

## Components
| Component | Path | Responsibility | Depends on |
|-----------|------|----------------|------------|
| HTTP API  | `cmd/api/`, `internal/http/` | routes, auth | store, queue |

## How a request flows
1. `cmd/api/main.go` builds the router …

## Build, run, test
Exactly as the repository does it (copied from the Makefile / CI).
```

## MAINTENANCE.md — what to do about it

Ranked by value. Each item: **what**, **why**, **where**, **effort**.

```markdown
1. **Docs lie about the config file location** — README says `~/.app.yml`,
   `internal/config/load.go:42` reads `$XDG_CONFIG_HOME/app/config.yml`.
   Users following the README get defaults silently. Effort: S.
```

## Rules

- Cite a path for every load-bearing claim; never invent a file or function.
- "Not found" beats a plausible guess.
- Ten specific findings beat forty generic ones — "improve test coverage" is
  not a finding, "`internal/billing` has no tests and handles refunds" is.
- Describe, do not fix: the deep dive edits no source.
