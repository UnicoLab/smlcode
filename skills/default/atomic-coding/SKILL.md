---
name: atomic-coding
description: Split work into tiny file-scoped tasks for SLMs; never load the whole repo.
triggers: refactor, implement, fix, code, edit
agents: worker, deep, corrector, splitter, planner
user-invocable: true
---

# Atomic coding for SLMs

- Touch the fewest files possible.
- Prefer `ws_edit` over rewriting whole files.
- State acceptance criteria before editing.
- If unsure of an API, use `ws_grep` / `ws_read` — do not invent.
- Keep outputs in the required JSON schema.
