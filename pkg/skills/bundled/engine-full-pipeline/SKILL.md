---
name: engine-full-pipeline
description: Full SLMCode engine — context→explore→plan→split→execute→review→test→memory.
triggers: full, pipeline, engine, end-to-end
agents: "*"
user-invocable: true
---

# Full engine mode

Use the complete specialist pipeline. Prefer this for multi-file or ambiguous work.
Reference extra skills with `@skill:name`. Pin project skills under `.slmcode/skills/`.
