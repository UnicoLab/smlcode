---
name: engine-specialist
description: Run a single specialist (skip full pipeline) via mode=specialist.
triggers: specialist, single-agent, focused
agents: "*"
user-invocable: true
---

# Specialist mode

When `mode: specialist` + `specialist: <role>`, only that role runs (plus inventory/context pack).
Use for focused explore/implement/review passes. Combine with `@skill:name`.
