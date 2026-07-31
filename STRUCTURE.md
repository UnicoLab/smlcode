# 🗂️ SLMCode layout

Dedicated project — **not** inside the GoLangGraph repo.

Made with ♥ by [UnicoLab](https://unicolab.ai)

```text
slmcode/
├── cmd/slmcode/                 CLI + embedded Studio UI (ui/)
├── pkg/
│   ├── agents/                  14 specialist prompts + factory
│   ├── backends/                OpenAI-compat / Ollama / optional CLI backends
│   ├── cli/                     Colored TTY + live event formatter
│   ├── config/                  .slmcode/config.yaml + provider presets
│   ├── context/                 Markdown memory + scoped packs
│   ├── harness/                 Public New / OpenWorkspace API
│   ├── instructions/            AGENTS.md / PROJECT loader
│   ├── knowledge/               SKILLS.md + learned skill evolution
│   ├── learning/                Wave lessons / context deltas
│   ├── loop/                    Parallel execute + review/correct
│   ├── multipass/               Think → critique → refine
│   ├── orchestrator/            Code-driven pipeline
│   ├── permissions/             auto | dry-run | review
│   ├── plan/                    Kanban, sanitize, discover
│   ├── server/                  Studio HTTP + SSE
│   ├── session/                 Resumable run snapshots
│   ├── skills/                  SKILL.md loader (+ embedded)
│   ├── stream/                  Live event schema
│   └── workspace/               Real FS / git tools
├── skills/default/              Default skill packs (source)
├── test/e2e/                    Board + Studio API + live oMLX
├── docs/                        INSTALL / PROVIDERS / GUIDE / …
├── Formula/slmcode.rb           Homebrew formula
├── scripts/
│   ├── install-remote.sh        curl one-liner (prebuilt)
│   ├── install.ps1 / install.cmd
│   ├── install.sh               build-from-source installer
│   └── lint.sh
└── go.mod                       → github.com/piotrlaczkowski/GoLangGraph
```

## Dependency

```text
slmcode  ──go.mod──►  github.com/piotrlaczkowski/GoLangGraph
                         └── optional local replace for hacking
```

## Commands

```bash
# Users
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash

# Developers
make tidy && make lint && make test && make install-system
RUN_E2E=1 make e2e
slmcode studio
slmcode chat
```
