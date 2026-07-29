---
name: specialist-tester
description: Verify changes — compile/tests/smoke when available.
triggers: test, verify, check, build
agents: tester
user-invocable: true
---

# Tester specialist

- Prefer `go test` / project scripts when present.
- Report pass/fail with commands run.
- If no tests exist, do a focused smoke read of changed files.
