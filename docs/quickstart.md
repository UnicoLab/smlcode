# 🚀 Quick start

Goal: installed binary → “oh, it edited my file” in about a minute. ⏱️
Secondary goal: smile at least once. 😄

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">☕</span>
<p class="slm-banner__text" markdown>
<strong>Assumptions:</strong> you ran an <a href="install.md">install</a> one-liner and have
<em>some</em> model reachable (oMLX, Ollama, LM Studio, cloud…). No model? See
<a href="providers.md">Providers</a> before yelling at the harness.
</p>
</div>

---

<ol class="slm-steps" markdown>

<li markdown>
**🔌 Point at a model**

=== "🍎 oMLX"

    ```bash
    omlx start
    slmcode doctor
    ```

    Green doctor = green light. Red doctor = “have you tried turning the model on?”

=== "🦙 Ollama"

    ```bash
    ollama serve
    slmcode config set provider ollama
    slmcode config set model qwen2.5-coder:14b
    slmcode config set endpoint http://127.0.0.1:11434
    slmcode doctor
    ```

=== "🌌 Other"

    Flags, env, Studio Settings, or a **stack**:

    ```bash
    slmcode stack list
    slmcode stack apply deepseek   # needs DEEPSEEK_API_KEY or /auth set
    slmcode doctor
    ```

    Details → [Providers](providers.md). Bring your own brain.

</li>

<li markdown>
**🛝 Init a playground**

```bash
mkdir -p /tmp/slm-demo && cd /tmp/slm-demo
printf 'package main\n\nfunc Hello() string { return "hi" }\n' > hello.go
printf '# Agents\n\nPrefer tiny Go edits and clear godoc comments.\n' > AGENTS.md
slmcode init              # detects Go and applies the go pack for you
```

`init` prints the pack it picked (`pack: go (detected)`). Override it with
`slmcode blocks apply <pack>` if you disagree — `slmcode blocks list` shows all thirteen.

Edit `.slmcode/PROJECT.md` with two honest sentences about the stack.
(Lying to your own memory file is a bold strategy. We don’t recommend it.)

</li>

<li markdown>
**🎯 Run the pipeline**

```bash
slmcode run -v "Add a Go doc comment to Hello() explaining it returns a greeting. Keep it tiny."
```

**Pass checklist** ✅

- [ ] the run ends with a **Changes** block naming `hello.go` and a `+N −M` count
- [ ] `hello.go` has a real `// Hello …` comment
- [ ] `slmcode board` shows completed work
- [ ] `slmcode session list` has a run
- [ ] `.slmcode/SKILLS.md` exists (the flywheel sneezed)

Got `⚠ no files changed` instead? That is the harness telling you the truth: the model claimed an
edit it never made, and the evidence gate refused it. The **Next** lines under it are the ones to
follow — `slmcode task show T1` prints the reviewer's verdict, the gate that refused the task and
the (empty) diff of its focus files. Smaller scope and a sharper acceptance line fix this more
often than a bigger model does.

</li>

<li markdown>
**🕹️ Open a cockpit**

```bash
slmcode studio    # clicky mode — open the URL it PRINTS
# and/or
slmcode           # premium TUI — keyboard mode
```

Pick your fighter. Both talk to the same harness. 🥊

!!! warning "Open the URL Studio prints, not `http://127.0.0.1:7420`"
    Studio can read this repo, rewrite its config, store your API keys and start runs, so it is
    behind a per-launch session token: the URL it prints looks like
    `http://127.0.0.1:7420/?t=8f3c…`. A bare `http://127.0.0.1:7420/` gets a 401 page telling you
    to go back to the terminal. Opening the tokenised URL once mints a cookie and the token stops
    appearing in the address bar. → [Studio security model](studio.md#security-model)

</li>

</ol>

---

## Level-ups 🎮

```bash
# Safer on real repos (stage first, apply later)
slmcode config set permission review
slmcode run -v "Add a unit test for Hello()"
slmcode apply       # interactive: a/s/e/v/r/A/q per file (--all to apply everything)

# Help small models think twice (literally)
slmcode config set think_passes 2
slmcode config set retries 2

# One specialist, no full circus
slmcode run --agent explorer "Where is Hello defined?"
```

<div class="slm-joke" markdown>
<span class="slm-joke__emoji">🦃</span>
<p markdown>
<strong>Turkey rule reminder:</strong> tiny prompt, tiny scope, tiny win.
If you ask a 7B model to “refactor the platform”, it will invent a platform.
</p>
</div>

---

## Where next? 🗺️

<div class="grid cards" markdown>

-   🧠 [Concepts](concepts.md) — why the loop looks like this
-   🧭 [User guide](guide.md) — daily driving without chaos
-   🧪 [Recipes](recipes.md) — copy-paste workflows
-   🖥️ [TUI & chat](tui.md) — terminal UX deep dive

</div>

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
