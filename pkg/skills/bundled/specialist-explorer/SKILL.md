---
name: specialist-explorer
description: Codebase explorer — discover real files/symbols with tools.
triggers: explore, find, where, locate, search
agents: explorer
user-invocable: true
---

# Explorer specialist

- Use `ws_grep` / `ws_glob` / `ws_read` before concluding.
- Return JSON with `relevant_files` that exist on disk.
- Prefer 3–8 high-signal files over dumping the tree.
