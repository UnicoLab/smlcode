# 🚀 Quick start

Goal: go from “installed binary” to “oh, it actually edited my file” without reading a novel.

!!! info "Assumptions"
    You already ran an [install one-liner](install.md). You have **some** model server reachable (oMLX, Ollama, LM Studio, cloud…).

---

## 1. Point at a model (30 seconds)

=== "oMLX (Apple Silicon default)"

    ```bash
    omlx start
    slmcode doctor
    ```

=== "Ollama"

    ```bash
    ollama serve   # if not already running
    slmcode config set provider ollama
    slmcode config set model qwen2.5-coder:14b
    slmcode config set endpoint http://127.0.0.1:11434
    slmcode doctor
    ```

=== "Anything else"

    See [Providers](providers.md) — flags, env vars, Studio Settings. Same harness.

---

## 2. Init a playground

```bash
mkdir -p /tmp/slm-demo && cd /tmp/slm-demo
printf 'package main\n\nfunc Hello() string { return "hi" }\n' > hello.go
printf '# Agents\n\nPrefer tiny Go edits and clear godoc comments.\n' > AGENTS.md
slmcode init
```

Peek at `.slmcode/PROJECT.md` and write two sentences about the stack. Future-you will thank present-you.

---

## 3. Run the pipeline

```bash
slmcode run -v "Add a Go doc comment to Hello() explaining it returns a greeting. Keep it tiny."
```

Watch the live stream: specialists appear, tasks move, reviewer argues with reality (politely).

**Pass criteria (aka “did the robot behave?”):**

- [ ] `hello.go` has a real `// Hello …` comment
- [ ] `slmcode board` shows work completed
- [ ] `slmcode session list` has a run
- [ ] `.slmcode/SKILLS.md` exists (the flywheel woke up)

---

## 4. Open the cockpit

```bash
slmcode studio
# → http://127.0.0.1:7420
```

Type a query, hit **Run**, stare at the pipeline strip like it's a music visualizer.

Or stay in the terminal:

```bash
slmcode          # premium TUI
slmcode chat     # classic REPL
```

---

## 5. Level-ups (optional, spicy)

```bash
# Safer first: stage writes instead of applying them
slmcode config set permission review
slmcode run -v "Add a unit test for Hello()"
slmcode apply
slmcode diff

# Make small models think twice
slmcode config set think_passes 2
slmcode config set retries 2

# One specialist only
slmcode run --agent explorer "Where is Hello defined?"
```

---

## 🎉 You're in

Next stops:

- [User guide](guide.md) — skills, permissions, sessions, day-to-day
- [Studio](studio.md) — GUI + API
- [Agents](agents.md) — the 14-person circus
- [Testing](testing.md) — prove it on your machine

Made with ♥ by [UnicoLab](https://unicolab.ai)
