# SLMCode Documentation

**Version 0.5.0** · Local SLM coding harness (Claude Code–style) on GoLangGraph + oMLX

| Doc | Read when you want to… |
|-----|------------------------|
| **[TESTING.md](TESTING.md)** | **Start here** — smoke test, Studio, chat, e2e |
| **[GUIDE.md](GUIDE.md)** | Daily CLI/Studio workflow |
| **[STUDIO.md](STUDIO.md)** | GUI panels + HTTP/SSE API |
| **[AGENTS.md](AGENTS.md)** | Specialist roster & coordinator actions |
| **[ARCHITECTURE.md](ARCHITECTURE.md)** | Internals, streaming, knowledge flywheel |

Parent overview: [../README.md](../README.md) · layout: [../STRUCTURE.md](../STRUCTURE.md)

## 30-second start

```bash
cd ~/Desktop/PROJECT/slmcode && make install-system
omlx start
mkdir -p /tmp/slm-demo && cd /tmp/slm-demo
printf 'package main\nfunc Hello() string { return "hi" }\n' > hello.go
slmcode init
slmcode run -v "Add a doc comment to Hello()"
slmcode studio   # http://127.0.0.1:7420
```

## Mental model

```
You (CLI / Studio / chat)
        │
        ▼
  Go orchestrator (not LLM-as-router)
        │
        ├─ context / explore|reuse / docs / architect
        ├─ plan → split → coordinator
        ├─ parallel workers + reviewer ↔ corrector
        └─ learn → test → memory → SKILLS.md evolve
        │
        ▼
  .slmcode/ markdown memory + board.json
```

Local SLMs stay sharp because each specialist gets a **tiny scoped pack**, not the whole repo.
