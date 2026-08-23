# 🗂️ SLMCode layout

Dedicated project — **not** inside the GoLangGraph repo.

Made with ♥ by [UnicoLab](https://unicolab.ai)

```text
slmcode/
├── cmd/slmcode/                 CLI (cobra) + embedded Studio UI (ui/, go:embed all:ui)
├── pkg/                         Engine packages — see the ownership map in CONTRIBUTING.md
│   ├── harness/                 top-level New / Init / Run façade
│   ├── orchestrator/            phase graph, HITL gates, project instructions, scope
│   ├── loop/                    inner loop: worker → review → correct → test
│   ├── agents/ plan/            specialist prompts + role specs; task/board model
│   ├── workspace/               the tool layer (ACI): ws_* tools, guards, edit ladder
│   ├── context/ repomap/        token budget, task packs, excerpts; ranked symbol map
│   ├── compact/ skills/         compaction; SKILL.md + progressive disclosure
│   ├── instructions/ retrieval/ AGENTS.md loading with path gating; embeddings
│   ├── schema/ backends/ repair/ JSON+GBNF contracts, capability probe, repair ladder
│   ├── memory/ evolve/ eval/    four memory layers; repair rules + bandit; metrics
│   ├── blocks/ pipeline/ stacks/ YAML building blocks, phase graph, provider presets
│   ├── permissions/ hitl/ hooks/ write & shell policy, human gates, lifecycle hooks
│   ├── server/                  Studio HTTP/SSE API + security policy
│   └── cli/                     terminal rendering: diffs, gates, REPL input, width
├── web/                         Vite + React + TS Studio SPA → cmd/slmcode/ui/
├── skills/default/              Default skill packs (source, embedded)
├── stacks/                      Provider/model presets (YAML)
├── test/e2e/                    Board, Studio API, prime ports, live oMLX
├── docs/                        MkDocs Material pages (→ GitHub Pages)
│   ├── index.md · install.md · quickstart.md · concepts.md · providers.md
│   ├── guide.md · tui.md · studio.md · skills.md · agents.md · blocks.md
│   ├── pipeline.md · customization.md · recipes.md
│   ├── cli.md · config.md · tools.md · decoding.md · context.md
│   ├── permissions.md · testing.md · troubleshooting.md · faq.md
│   ├── architecture.md · conventions.md · self-improvement.md
│   ├── migration.md · changelog.md · contributing.md
│   └── assets/ · stylesheets/ · overrides/ · javascripts/
├── Formula/slmcode.rb           Homebrew formula
├── scripts/
│   ├── install-remote.sh        curl one-liner (prebuilt)
│   ├── install.ps1 / install.cmd
│   ├── install.sh               build-from-source installer
│   ├── lint.sh · coverage-check.sh · e2e_prime_smoke.sh
│   └── prepare-release.sh · update-formula.sh · check-repo-refs.sh
├── AGENTS.md                    ≤2 KB always-on agent core (loaded into prompts)
├── CONTRIBUTING.md              build, gate, lint ratchet, ownership map
├── mkdocs.yml · requirements-docs.txt
└── go.mod                       → github.com/UnicoLab/slmcode
```

## Runtime state (gitignored)

```text
<project>/.slmcode/
├── config.yaml · pipeline.yaml · board.json · hooks.json
├── CONTEXT.md · PLAN.md · TASKS.md · SCRATCH.md · MEMORY.md
├── skills/ · agents/ · blocks/       project overrides
├── pending/                          permission=review proposals
├── checkpoints/ · sessions/ · queries/
├── scratch/                          the ONLY tool-writable path under .slmcode/
├── memory/ · evolve/ · metrics/      self-improvement state
└── auth.json                         provider keys (never commit)

~/.slmcode/
├── config.yaml                       user-level config layer
├── memory/ · evolve/                 cross-project procedures + bandit policy
└── blocks/ · agents/ · skills/       user-level overrides
```

## Build

```bash
make bootstrap     # build the Studio UI (required once per clone)
make check         # the one gate — same as CI
make docs-serve    # http://127.0.0.1:8000
make docs-build    # strict build → site/
```

Published: <https://unicolab.github.io/smlcode/>

## Install

```bash
# Users
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash

# Developers
make bootstrap && make check && make install-system
RUN_E2E=1 make e2e
```
