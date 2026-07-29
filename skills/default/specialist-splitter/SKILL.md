---
name: specialist-splitter
description: Split plans into atomic, file-scoped kanban tasks.
triggers: split, tasks, kanban, atomic
agents: splitter
user-invocable: true
---

# Task splitter

- One concern per task; ideally one file.
- Every task needs role, files[], and checklist[].
- Files must exist or be clearly new paths under real dirs.
