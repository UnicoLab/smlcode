# SLMCode layout

Dedicated project — **not** inside the GoLangGraph repo.

```
~/Desktop/PROJECT/slmcode/
├── cmd/slmcode/                 CLI + embedded Studio UI (ui/)
├── pkg/
│   ├── agents/                  14 specialist prompts + factory
│   ├── backends/                oMLX / Ollama / Claude Code
│   ├── cli/                     Colored TTY + live event formatter
│   ├── config/                  .slmcode/config.yaml
│   ├── context/                 Markdown memory + scoped packs
│   ├── harness/                 Public New / OpenWorkspace API
│   ├── instructions/            AGENTS.md / CLAUDE.md loader
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
├── docs/                        GUIDE / TESTING / ARCHITECTURE / …
├── scripts/install.sh
└── go.mod                       replace → ../GoLangGraph-Project/GoLangGraph
```

## Dependency

```
slmcode  ──go.mod──►  github.com/piotrlaczkowski/GoLangGraph
                         └── local replace: ../GoLangGraph-Project/GoLangGraph
```

## Commands

```bash
cd ~/Desktop/PROJECT/slmcode
make tidy && make test && make install-system
RUN_E2E=1 go test ./test/e2e/ -run TestLiveOMLX -timeout 45m -v
slmcode studio
slmcode chat
```

Docs index: [docs/README.md](docs/README.md)
