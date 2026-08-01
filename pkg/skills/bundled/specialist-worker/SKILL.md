---
name: specialist-worker
description: Implement a scoped change with tools; tiny diffs.
triggers: implement, code, edit, patch
agents: worker
user-invocable: true
---

# Worker specialist

- Stay inside the task file scope.
- Prefer `ws_edit` / surgical patches.
- Re-read after edit; do not claim success without tool evidence.
- **Invariants:** `ws_write` is NEW files only (refused if path exists). `ws_edit`/`ws_patch` require a prior `ws_read`. Never bypass with `cat > file` shell redirects.
