# 📦 Install

Get one `slmcode` binary on your `PATH`. Same energy as the big coding CLIs —
fewer zeros on the invoice. 💅

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">🪄</span>
<p class="slm-banner__text" markdown>
<strong>Plot twist:</strong> you do <em>not</em> need Go installed.
One-liners fetch a shiny GitHub Release. Keep the compiler for contributing (or vibes).
</p>
</div>

!!! success "🎉 No Go required"
    Prebuilt binaries for macOS / Linux / Windows. Your laptop can stay a laptop.

---

## Choose your path 🗺️

<div class="grid cards" markdown>

-   :material-apple: **🍎 macOS / Linux / WSL**

    ---

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
    ```

    Classic. Reliable. Slightly mysterious until `doctor` smiles.

-   :material-microsoft-windows: **🪟 Windows**

    ---

    ```powershell
    irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
    ```

    PowerShell one-liner. SmartScreen may raise an eyebrow — unblock and proceed.

-   :material-glass-mug-variant: **🍺 Homebrew**

    ---

    ```bash
    brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
    ```

    For people who install everything with brew, including existential dread.

</div>

---

## One-liners (all the flavors) 🍦

=== "🍎 User install"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
    ```

=== "🧰 System install"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash -s -- --system
    ```

    Lands in Homebrew prefix or `/usr/local` (may ask for `sudo`). Also symlinks into
    `~/.local/bin` so PATH priority doesn't fork your brain. 🧠

=== "📌 Pin a version"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh \
      | bash -s -- --version v0.18.4
    ```

=== "🪟 PowerShell"

    ```powershell
    irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
    ```

=== "🪟 CMD"

    ```bat
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.cmd -o install.cmd && install.cmd && del install.cmd
    ```

=== "🍺 Brew tap"

    ```bash
    brew tap UnicoLab/smlcode https://github.com/UnicoLab/smlcode
    brew install slmcode
    ```

---

## Verify ✅

```bash
slmcode version
slmcode doctor
```

Every install path above verifies a SHA-256 checksum for you: the shell installer and
the PowerShell installer both fetch the release's `SHA256SUMS` and refuse to install on a
mismatch, and Homebrew checks the `sha256` in the formula. If a checksum could **not** be
fetched, the installer says so loudly rather than pretending it checked.

To check by hand, or to verify a binary you downloaded from the Releases page:

```bash
curl -fsSLO https://github.com/UnicoLab/smlcode/releases/download/v0.18.4/SHA256SUMS
shasum -a 256 -c SHA256SUMS --ignore-missing     # macOS
sha256sum -c SHA256SUMS --ignore-missing         # Linux
```

You want: binary on `PATH`, a provider/model listed, and a model server that answers the phone.
`doctor` exits **4** when the provider check fails — an unreachable endpoint, a rejected or
missing API key, or a model the endpoint does not serve — and the message says which.

<div class="slm-cmd" markdown>
<div class="slm-cmd__bar" markdown><span>next up</span><span>quick start 🚀</span></div>

```bash
cd your-project
slmcode init
slmcode                 # premium TUI — board goes brrr
```

</div>

→ [⏱️ Quick start](quickstart.md) · [🔌 Providers](providers.md)

---

## Update / uninstall ♻️

=== "⬆️ Update"

    ```bash
    slmcode update
    slmcode update --check
    ```

    Binary installs re-download the latest release asset for your OS/arch, verify it against
    the release `SHA256SUMS`, and replace the running binary atomically — no `curl | bash`.
    Source installs rebuild from the checkout recorded in `~/.config/slmcode/install.json`.
    `--check` reports without installing; `--yes` skips the confirmation prompt.

=== "🗑️ Uninstall (curl)"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh \
      | bash -s -- --uninstall
    ```

=== "🍺 Uninstall (brew)"

    ```bash
    brew uninstall slmcode
    ```

---

## Where does it land? 📍

| Mode | Location |
|------|----------|
| 👤 User | `~/.local/bin/slmcode` |
| 🧰 System | `$(brew --prefix)/bin` or `/usr/local/bin` |
| 🪟 Windows | `%LOCALAPPDATA%\slmcode\bin\slmcode.exe` |
| 🍺 Homebrew | Cellar → linked on `PATH` |

Metadata: `~/.config/slmcode/install.json` (Windows: `%APPDATA%\slmcode\install.json`).

---

## Build from source 🛠️

Needs **Go 1.23+**. Module proxy pulls GoLangGraph — no sibling clone required.

```bash
git clone https://github.com/UnicoLab/smlcode.git
cd smlcode
make install-system
# or make install
```

**Node is optional — for a source build.** `make install` builds the Studio SPA when `npm` is
available and the registry is reachable; when it is not, it says so and installs anyway with the
built-in placeholder page. The CLI, the API and every command are unaffected — only the Studio
*web page* is missing, and `slmcode studio` tells you that on startup. Build it later with
`make bootstrap`.

!!! success "Released binaries always ship the real Studio"
    This caveat applies to **source builds only**. Every binary published to GitHub Releases —
    which is what the curl one-liner, the PowerShell one-liner, Homebrew and `slmcode update`
    all install — is built by CI with the Studio SPA compiled in, and the release workflow
    **fails outright** rather than publishing a binary that would serve the placeholder. If you
    installed with a one-liner, `slmcode studio` gives you the real UI.

Optional: `GOLANGGRAPH=/path/to/GoLangGraph` for local framework hacking. Bring snacks.

---

## Troubleshooting 🧯

| Symptom | Fix |
|---------|-----|
| `command not found` 👻 | Fix `PATH`, open a **new** shell |
| Checksum mismatch 🔐 | Re-run; check proxies / TLS inspection |
| SmartScreen 🛡️ | Unblock or use PowerShell one-liner |
| Go build fails 💥 | Use the **one-liner** (seriously) |

More comedy → [❓ FAQ](faq.md)

<div class="slm-joke" markdown>
<span class="slm-joke__emoji">💚</span>
<p markdown>
<strong>Pro tip:</strong> if <code>slmcode doctor</code> is green, resist the urge to
“just tweak one more config”. Ship a tiny task first. Future-you will send a thank-you emoji.
</p>
</div>

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
