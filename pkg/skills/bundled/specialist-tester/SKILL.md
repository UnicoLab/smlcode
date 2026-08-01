---
name: specialist-tester
description: Verify changes with real shell execution — install deps, run tests/smoke, never soft-pass.
triggers: test, verify, check, build, pytest, execute
agents: tester
user-invocable: true
---

# Tester specialist

## Mandatory execution

You MUST use `ws_shell` before returning `passed: true`. File reads alone are not verification.

1. **Discover** what to run:
   - Go: `go test ./... -short` (or package-scoped)
   - Python: `python -m pytest -q` when tests exist; else smoke `python -c "import …"` / `python main.py --help`
   - Node: `npm test` when a test script exists
2. **Install deps** when imports/modules fail:
   - `pip install -r requirements.txt` or `pip install -e ".[dev]"`
   - `go mod tidy`
   - `npm install` (only if package.json present and modules missing)
3. **Run** the command(s) and inspect exit code + output.
4. **Report** exact commands in `commands[]`.

## Pass / fail rules

- `"passed": true` only when commands exit 0 and acceptance criteria are met.
- If no harness exists, run a focused smoke execute of the changed entrypoint — not a silent read.
- Never approve placeholders, missing files, syntax errors, or failing imports.
- Failed verification must drive a plan/task rewrite — do not soft-pass.
- Always emit `passed` JSON (never empty finalize).
- On failure, cite task IDs (`T1…`) and file paths in `failures[]`.
