# 🚀 Quick start

Goal: installed binary → “oh, it edited my file” in about a minute.

!!! info "Assumptions"
    You ran an [install](install.md) one-liner. You have **some** model reachable
    (oMLX, Ollama, LM Studio, cloud…). See [Providers](providers.md) if not.

---

<ol class="slm-steps" markdown>

<li markdown>
**Point at a model**

=== "oMLX"

    ```bash
    omlx start
    slmcode doctor
    ```

=== "Ollama"

    ```bash
    ollama serve
    slmcode config set provider ollama
    slmcode config set model qwen2.5-coder:14b
    slmcode config set endpoint http://127.0.0.1:11434
    slmcode doctor
    ```

=== "Other"

    Use flags/env/Studio Settings — [Providers](providers.md).

</li>

<li markdown>
**Init a playground**

```bash
mkdir -p /tmp/slm-demo && cd /tmp/slm-demo
printf 'package main\n\nfunc Hello() string { return "hi" }\n' > hello.go
printf '# Agents\n\nPrefer tiny Go edits and clear godoc comments.\n' > AGENTS.md
slmcode init
```

Edit `.slmcode/PROJECT.md` with two honest sentences about the stack.

</li>

<li markdown>
**Run the pipeline**

```bash
slmcode run -v "Add a Go doc comment to Hello() explaining it returns a greeting. Keep it tiny."
```

**Pass checklist**

- [ ] `hello.go` has a real `// Hello …` comment
- [ ] `slmcode board` shows completed work
- [ ] `slmcode session list` has a run
- [ ] `.slmcode/SKILLS.md` exists

</li>

<li markdown>
**Open a cockpit**

```bash
slmcode studio    # http://127.0.0.1:7420
# and/or
slmcode           # premium TUI
```

</li>

</ol>

---

## Level-ups

```bash
# Safer on real repos
slmcode config set permission review
slmcode run -v "Add a unit test for Hello()"
slmcode apply

# Help small models
slmcode config set think_passes 2
slmcode config set retries 2

# One specialist
slmcode run --agent explorer "Where is Hello defined?"
```

---

## Where next?

<div class="grid cards" markdown>

-   [Concepts](concepts.md) — why the loop looks like this
-   [User guide](guide.md) — daily driving
-   [Recipes](recipes.md) — copy-paste workflows
-   [TUI & chat](tui.md) — terminal UX deep dive

</div>

Made with ♥ by [UnicoLab](https://unicolab.ai)
