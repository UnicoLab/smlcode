# 📦 Install

Get one `slmcode` binary on your `PATH`. Same energy as the big coding CLIs — fewer zeros on the invoice.

!!! success "No Go required"
    One-liners download a **prebuilt GitHub Release**. Keep the compiler for contributing.

---

## Choose your path

<div class="grid cards" markdown>

-   :material-apple: **macOS / Linux / WSL**

    ---

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
    ```

-   :material-microsoft-windows: **Windows**

    ---

    ```powershell
    irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
    ```

-   :material-glass-mug-variant: **Homebrew**

    ---

    ```bash
    brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
    ```

</div>

---

## One-liners (all options)

=== "macOS / Linux / WSL — user"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
    ```

=== "macOS / Linux — system"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash -s -- --system
    ```

    Installs to Homebrew prefix or `/usr/local` (may prompt for `sudo`). Also symlinks into `~/.local/bin` so PATH priority doesn't fork your brain.

=== "Pin a version"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh \
      | bash -s -- --version v0.5.17
    ```

=== "Windows PowerShell"

    ```powershell
    irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
    ```

=== "Windows CMD"

    ```bat
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.cmd -o install.cmd && install.cmd && del install.cmd
    ```

=== "Homebrew tap"

    ```bash
    brew tap UnicoLab/smlcode https://github.com/UnicoLab/smlcode
    brew install slmcode
    ```

---

## Verify

```bash
slmcode version
slmcode doctor
```

You want a binary on `PATH`, a provider/model listed, and a reachable model server.

<div class="slm-cmd" markdown>
<div class="slm-cmd__bar" markdown><span>next</span><span>quick start</span></div>

```bash
cd your-project
slmcode init
slmcode                 # premium TUI
```

</div>

→ [Quick start](quickstart.md) · [Providers](providers.md)

---

## Update / uninstall

=== "Update"

    ```bash
    slmcode update
    slmcode update --check
    ```

    Binary installs re-download the latest release. Source installs rebuild from your checkout.

=== "Uninstall (curl)"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh \
      | bash -s -- --uninstall
    ```

=== "Uninstall (brew)"

    ```bash
    brew uninstall slmcode
    ```

---

## Where does it land?

| Mode | Location |
|------|----------|
| User | `~/.local/bin/slmcode` |
| System | `$(brew --prefix)/bin` or `/usr/local/bin` |
| Windows | `%LOCALAPPDATA%\slmcode\bin\slmcode.exe` |
| Homebrew | Cellar → linked on `PATH` |

Metadata: `~/.config/slmcode/install.json` (Windows: `%APPDATA%\slmcode\install.json`).

---

## Build from source

Needs **Go 1.23+**. Module proxy pulls GoLangGraph — no sibling clone required.

```bash
git clone https://github.com/UnicoLab/smlcode.git
cd smlcode
make install-system
# or make install
```

Optional: `GOLANGGRAPH=/path/to/GoLangGraph` for local framework hacking.

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `command not found` | Fix `PATH`, new shell |
| Checksum mismatch | Re-run; check proxies |
| SmartScreen | Unblock or use PowerShell one-liner |
| Go build fails | Use the **one-liner** |

More → [FAQ](faq.md)

Made with ♥ by [UnicoLab](https://unicolab.ai)
