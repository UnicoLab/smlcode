# 📦 Install

Get one `slmcode` binary on your `PATH`. Same energy as the big coding CLIs — fewer zeros on the invoice.

!!! success "No Go required"
    The one-liners download a **prebuilt release**. Save the compiler for weekends.

---

## ⚡ One-liners (recommended)

=== "macOS / Linux / WSL"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
    ```

    System-wide (Homebrew prefix or `/usr/local` — may ask for `sudo`):

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash -s -- --system
    ```

    Pin a version (because “latest” is a lifestyle choice):

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh \
      | bash -s -- --version v0.5.17
    ```

=== "Windows PowerShell"

    ```powershell
    irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
    ```

    Installs under `%LOCALAPPDATA%\slmcode\bin` and adds it to your user `PATH`.

=== "Windows CMD"

    ```bat
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.cmd -o install.cmd && install.cmd && del install.cmd
    ```

=== "Homebrew"

    ```bash
    brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
    ```

    Or tap this repo:

    ```bash
    brew tap UnicoLab/smlcode https://github.com/UnicoLab/smlcode
    brew install slmcode
    ```

---

## ✅ Did it work?

```bash
slmcode version
slmcode doctor
```

You want: binary on `PATH`, a provider/model listed, and (ideally) a happy reachability check.

Then:

```bash
cd your-project
slmcode init
slmcode                 # premium TUI
# or
slmcode run -v "say hello in a tiny README"
```

→ Continue in [Quick start](quickstart.md).

---

## 🔄 Update

```bash
slmcode update          # re-download latest release (binary installs)
slmcode update --check  # peek without changing anything
```

Or re-run the one-liner. Homebrew folks:

```bash
# after the formula / tap is updated
brew upgrade slmcode
```

!!! note "Source installs"
    If you installed with `make install` / `scripts/install.sh`, `slmcode update` rebuilds from your checkout instead.

---

## 🗑️ Uninstall

=== "curl installer"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh \
      | bash -s -- --uninstall

    # system install
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh \
      | bash -s -- --uninstall --system
    ```

=== "Homebrew"

    ```bash
    brew uninstall slmcode
    ```

=== "Windows"

    Delete `%LOCALAPPDATA%\slmcode\bin\slmcode.exe`,
    or save `install.ps1` locally and run with `-Uninstall`.

---

## 🛠️ Build from source (developers)

Needs **Go 1.23+**. GoLangGraph comes from the module proxy — no sibling clone required.

```bash
git clone https://github.com/UnicoLab/smlcode.git
cd smlcode
make install-system     # → brew prefix or /usr/local/bin
# or
make install            # → ~/.local/bin
./scripts/install.sh --system
```

Optional local GoLangGraph for hacking: set `GOLANGGRAPH=/path/to/GoLangGraph`.

---

## 🧭 Where does the binary land?

| Mode | Location |
|------|----------|
| User (default) | `~/.local/bin/slmcode` |
| System | `$(brew --prefix)/bin` or `/usr/local/bin` |
| Windows | `%LOCALAPPDATA%\slmcode\bin\slmcode.exe` |
| Homebrew | Cellar → linked on `PATH` |

Install metadata: `~/.config/slmcode/install.json`
(Windows: `%APPDATA%\slmcode\install.json`)

---

## 🆘 Troubleshooting

| Symptom | Fix |
|---------|-----|
| `command not found` | Add `~/.local/bin` (or system prefix) to `PATH`, open a new shell |
| Checksum mismatch | Re-run installer; check corporate proxies rewriting downloads |
| Windows SmartScreen | Unblock the binary or use the PowerShell one-liner |
| Go build fails | Use the **one-liner** — you don't need Go |
| `doctor` red | Start oMLX / Ollama / your gateway — see [Providers](providers.md) |

!!! tip "Still stuck?"
    Open an issue on [GitHub](https://github.com/UnicoLab/smlcode/issues) with `slmcode doctor` output (redact keys!).

Made with ♥ by [UnicoLab](https://unicolab.ai)
