# Feature index

This index maps the implemented public surfaces to their detailed guides.
For exact installed-version flags and subcommands use `slmcode --help` and
`slmcode <command> --help`. For the complete configuration field catalog use
`slmcode config schema`; [configuration](config.md) explains precedence and
persistence. The `slmcode docs` command reads project documents, not this website.

## Work from a request to a verified change

| Capability | Entry points | Guide |
| --- | --- | --- |
| Install, upgrade and offline use | Installers, `init`, `update`, `version` | [Installation](install.md), [offline installation](install-offline.md), [quick start](quickstart.md) |
| Interactive coding | Default command, `tui`, `chat` | [TUI and chat](tui.md), [user guide](guide.md) |
| Run or watch a project | `run`, `watch` | [CLI](cli.md), [recipes](recipes.md) |
| Browser interface and live events | `studio`; run setup, Live floor, board, settings | [Studio](studio.md) |
| Plans, tasks and human steering | `plan`, `board`, `task`, `compose`, plan/clarify/escalate gates | [CLI](cli.md), [pipeline](pipeline.md), [teams](squads.md) |
| Review, stage or discard edits | `diff`, `apply`, `reject`, `commit` | [CLI review commands](cli.md#review-changes), [permissions](permissions.md) |
| Continue or inspect work | `session`, `status`, `task show` | [CLI](cli.md), [user guide](guide.md) |

## Configure the harness

| Capability | Entry points | Guide |
| --- | --- | --- |
| Discover and configure a served model | `configure`, `config`, `auth`, `doctor`, `readiness` | [Providers](providers.md), [configuration](config.md), [troubleshooting](troubleshooting.md) |
| Model context and runtime calibration | `calibrate`, model profiles | [Calibration](calibration.md), [decoding](decoding.md) |
| Agents, skills and reusable building blocks | `agent`, `skills`, `blocks`, `stack` | [Agents](agents.md), [skills](skills.md), [blocks](blocks.md), [customization](customization.md) |
| Team composition and specialist ownership | Dynamic/strict teams, team pins, `compose` | [Teams](squads.md), [pipeline](pipeline.md) |
| Frontend workflows | Frontend method, skills and specialist roles | [Frontend](frontend.md) |
| External tools and lifecycle integrations | MCP configuration, `hooks` trust/list commands | [Tools](tools.md), [customization](customization.md), [permissions](permissions.md) |
| Structured responses and tool discipline | Role schemas, constrained decoding, bounded tools | [Decoding](decoding.md), [tools](tools.md), [conventions](conventions.md) |
| External model predictions | `laya_*` config; native Laya or OpenAI-compatible server | [Decision models](laya.md) |
| Shell completion | `completion` | [CLI completions](cli.md#completions) |

## Context, memory and improvement

| Capability | Entry points | Guide |
| --- | --- | --- |
| Token budgets, repository context and retrieval | `context`, `docs`, context packs, repository map | [Context engineering](context.md), [architecture](architecture.md) |
| Persistent memory and forgetting | `memory` | [Self-improvement and memory](self-improvement.md) |
| Adaptive policies and regression history | `evolve`, `metrics` | [Self-improvement](self-improvement.md), [CLI](cli.md) |
| Repository knowledge graph | `graph` | [Knowledge graph](graph.md) |
| Evaluation and bounded experiments | `eval`, `autoresearch` | [Testing](testing.md), [autoresearch](autoresearch.md), [SLM learnings](slm-learnings.md) |

Adaptive harness policies and stored lessons are distinct from training neural
weights. The optional external decision model does not automatically train or
replace itself. Its role and limits are documented on the [decision-model page](laya.md).

## Operation and release

See the [dated release qualification record](release-readiness.md) for verified
checks and qualification limits from the latest audit.

- [Testing](testing.md): automated gates, fake-server binary acceptance, live-model
  release checks and how to interpret their evidence.
- [Permissions](permissions.md): scope protection, shell controls, review mode,
  trusted hooks and Studio access.
- [Troubleshooting](troubleshooting.md) and [FAQ](faq.md): setup and runtime recovery.
- [Architecture](architecture.md), [contributing](contributing.md) and
  [conventions](conventions.md): implementation and development workflow.
- [Migration](migration.md) and [changelog](changelog.md): changed behavior and
  compatibility. Maintainers should also follow the repository's
  [release checklist](https://github.com/UnicoLab/smlcode/blob/main/RELEASE.md).
