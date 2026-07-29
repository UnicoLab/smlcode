---
name: markdown-memory
description: Keep CONTEXT.md / MEMORY.md / PLAN.md accurate and short for SLMs.
triggers: memory, context, document, remember, lesson
agents: context, memory, docs, coordinator
user-invocable: true
---

# Markdown memory

- Prefer append bullets over rewriting whole docs.
- Keep CONTEXT.md under ~80 lines of live facts.
- MEMORY.md = durable conventions / pitfalls only.
- Never invent file paths — only cite inventory or tool results.
