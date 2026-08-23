# ❓ FAQ & troubleshooting

The “it's not you, it's the context window” desk. 🎫
Take a number. Or just `Ctrl+F`.

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🧯</span>
<p class="slm-banner__text" markdown>
<strong>Order of operations:</strong> <code>slmcode doctor</code> → permissions → prompt size →
“did I ask it to rewrite the universe?” Usually it’s #4.
</p>
</div>

---

## Building blocks 🧱

??? question "🧱 How do I switch between Go and Python pipelines?"
    ```bash
    slmcode blocks apply go      # Go pipeline + go-worker + go-tester
    slmcode blocks apply python  # Python pipeline + python-worker + pytest
    slmcode blocks apply react   # React/TS pipeline + react-tester
    ```
    In Studio: Settings → Pack Selector, or Pipeline tab → preset selector.

??? question "🧱 Can I create my own pipeline or agent blocks?"
    Yes! Drop YAML files in `.slmcode/blocks/pipelines/` or `.slmcode/blocks/agents/`.
    Blocks are `api_version: blocks/v1` documents with marketplace metadata (id, version, author, license, tags).
    Run `slmcode blocks validate` to check your custom blocks. See [🧱 Blocks](blocks.md) for the full schema.

??? question "🧱 Where are the built-in blocks?"
    Embedded in the binary via `go:embed` — always available. Project-level blocks in `.slmcode/blocks/` override them.
    The discovery order is: project → user → env → builtin (first ID wins).

---

## Install & PATH 📦

??? question "👻 `slmcode: command not found`"
    Add the install dir to `PATH` and open a **new** shell:

    - User install → `~/.local/bin`
    - System → `$(brew --prefix)/bin` or `/usr/local/bin`
    - Windows → `%LOCALAPPDATA%\slmcode\bin`

    ```bash
    hash -r
    which slmcode
    slmcode version
    ```

??? question "🔐 Checksum mismatch on install"
    Re-run the installer. Corporate SSL inspection sometimes rewrites downloads.
    Try from a clean network or download the asset from GitHub Releases manually.

??? question "🍺 Homebrew SHA256 fails"
    Formula SHAs track a specific release build. Update to latest `main` formula URL
    or reinstall after a formula sync commit.

---

## Doctor & providers 🩺

??? question "🔴 `doctor` shows unreachable LLM"
    1. Is the server up? (`omlx start`, `ollama serve`, LM Studio running…)
    2. Does `endpoint` match reality (note `/v1` vs bare host)?
    3. Is the API key set for cloud providers?
    4. Try:

    ```bash
    curl -s "$ENDPOINT/models" | head
    slmcode config
    slmcode doctor
    ```

??? question "🪐 Wrong model / wrong gateway"
    Per-agent endpoints register unique backend keys. Check `.slmcode/agents/*.yaml`
    and Studio → Agents. Global config can be fine while one specialist phones Mars.

??? question "🦙 Ollama vs OpenAI-compat confusion"
    Ollama uses its native API. Everything else is Chat Completions.
    Don't point `provider: ollama` at an OpenAI URL (or vice versa) unless you enjoy puzzles.

---

## Quality issues 🥴

??? question "📂 It edits the wrong files"
    - Set a correct `model_profiles.<family>.context_limit`, or lower `context_role_budget` / `repo_map_tokens`
    - Pin `atomic-coding`
    - Add a stern `AGENTS.md`
    - Force a fresh explore once:

    ```bash
    SLMCODE_FORCE_EXPLORE=1 slmcode run -v "…"
    ```

??? question "🥴 JSON / tool calls look drunk"
    Expected on weaker SLMs. The harness repairs common breakage and retries.
    Raise `think_passes` / `retries`. Prefer models with decent tool-calling.

??? question "🤷 Reviewer always rejects (or always accepts)"
    Check disk evidence in the Live feed. Prefer `permission: review` on important repos.
    Speculative cancel + disk rename acceptance exist specifically for flaky judges.

??? question "🐢 Second run is slow / re-explores everything"
    It shouldn't — explore reuse should kick in. If CONTEXT is empty or wiped, explore returns.
    Don't delete `.slmcode/` for fun mid-project. The flywheel has feelings.

---

## Studio & TUI 🎛️

??? question "🚧 Studio port already in use"
    ```bash
    slmcode studio --listen 127.0.0.1:7421
    ```

??? question "👻 UI looks stale after I hacked `ui/`"
    Rebuild the Go binary — assets are `go:embed`'d.

    ```bash
    make build && ./bin/slmcode studio
    ```

??? question "🛟 Resume doesn't continue where I left off"
    Confirm `.slmcode/queries/<id>/react/` exists. `/resume` needs a checkpoint from `/stop` or Ctrl+C —
    not a crashed machine mid-write with no flush.

---

## Safety 🛡️

??? question "✍️ Did it write to disk?"
    ```bash
    slmcode diff
    ls .slmcode/pending/   # if permission=review
    ```

??? question "🎭 How do I demo without touching files?"
    ```bash
    slmcode config set permission dry-run
    # or
    slmcode run --dry-run "…"
    ```

---

## Still stuck? 🆘

1. `slmcode doctor` (redact keys)
2. `slmcode run -v "…"` snippet of the failing phase
3. Open a GitHub issue: [UnicoLab/smlcode](https://github.com/UnicoLab/smlcode/issues)

<div class="slm-joke" markdown>
<span class="slm-joke__emoji">🧃</span>
<p markdown>
<strong>Emotional support tip:</strong> if the model invents an API that doesn’t exist,
it’s not gaslighting you — it’s optimistic. Lower context. Pin a skill. Try again with snacks.
</p>
</div>

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
