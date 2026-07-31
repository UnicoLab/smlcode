# 🗂️ SLMCode layout

Dedicated project — **not** inside the GoLangGraph repo.

Made with ♥ by [UnicoLab](https://unicolab.ai)

```text
slmcode/
├── cmd/slmcode/                 CLI + embedded Studio UI (ui/)
├── pkg/                         Engine packages (agents, loop, server, …)
├── skills/default/              Default skill packs (source)
├── test/e2e/                    Board + Studio API + live oMLX
├── docs/                        MkDocs Material pages (→ GitHub Pages)
│   ├── index.md
│   ├── install.md / quickstart.md / providers.md
│   ├── guide.md / studio.md / agents.md / testing.md
│   ├── architecture.md / contributing.md
│   ├── assets/ · stylesheets/ · overrides/
├── Formula/slmcode.rb           Homebrew formula
├── scripts/
│   ├── install-remote.sh        curl one-liner (prebuilt)
│   ├── install.ps1 / install.cmd
│   ├── install.sh               build-from-source installer
│   └── lint.sh
├── mkdocs.yml
├── requirements-docs.txt
└── go.mod                       → github.com/piotrlaczkowski/GoLangGraph
```

## Docs site

```bash
make docs-serve    # http://127.0.0.1:8000
make docs-build    # strict build → site/
```

Published: https://unicolab.github.io/smlcode/

## Commands

```bash
# Users
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash

# Developers
make tidy && make lint && make test && make docs-build && make install-system
RUN_E2E=1 make e2e
```
