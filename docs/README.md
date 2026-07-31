# 📚 SLMCode Docs

Welcome! SLMCode is a coding harness that is **SLM-first** and **LLM-friendly** —
plan → specialists → self-critic → learn, with a live TUI + Studio.

Made with ♥ by [UnicoLab](https://unicolab.ai)

---

## 🗺️ Where should I go?

| Doc | Read when you want to… |
|-----|------------------------|
| **[INSTALL.md](INSTALL.md)** | ⚡ Get `slmcode` on PATH (one-liners, brew, Windows) |
| **[PROVIDERS.md](PROVIDERS.md)** | 🔌 Point it at oMLX / Ollama / OpenAI / *any* gateway |
| **[GUIDE.md](GUIDE.md)** | 🧭 Daily CLI + Studio workflow |
| **[TESTING.md](TESTING.md)** | ✅ Smoke test, Studio, chat, e2e |
| **[STUDIO.md](STUDIO.md)** | 🎨 GUI panels + HTTP/SSE API |
| **[AGENTS.md](AGENTS.md)** | 🧩 Specialist roster & coordinator |
| **[ARCHITECTURE.md](ARCHITECTURE.md)** | 🏗️ Internals, streaming, knowledge flywheel |

Parent overview: [../README.md](../README.md) · layout: [../STRUCTURE.md](../STRUCTURE.md)

---

## ⏱️ 60-second start

```bash
# Install (no Go required)
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash

slmcode doctor
mkdir -p /tmp/slm-demo && cd /tmp/slm-demo
printf 'package main\nfunc Hello() string { return "hi" }\n' > hello.go
slmcode init
slmcode run -v "Add a doc comment to Hello()"
slmcode studio   # http://127.0.0.1:7420
```

Using Ollama instead of oMLX?

```bash
slmcode config set provider ollama
slmcode config set model qwen2.5-coder:14b
slmcode config set endpoint http://127.0.0.1:11434
```

---

## 🧠 Mental model

```text
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
        │
        ▼
  your provider (omlx · ollama · openai · anything OpenAI-compat)
```

Small models stay sharp because each specialist gets a **tiny scoped pack**,
not the whole repo. Big models still get structure, telemetry, and a resume story.

---

## 💖 UnicoLab

SLMCode is part of the [UnicoLab](https://unicolab.ai) open toolkit —
built so local and private coding agents can feel first-class.
