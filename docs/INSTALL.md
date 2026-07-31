# 📦 Install SLMCode

Get a single `slmcode` binary on your `PATH` — same vibe as the big coding CLIs,
minus the token bill. Pick your adventure 👇

Made with ♥ by [UnicoLab](https://unicolab.ai)

---

## ⚡ One-liners (recommended)

### macOS / Linux / WSL

```bash
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
```

System-wide (Homebrew prefix or `/usr/local` — may ask for `sudo`):

```bash
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash -s -- --system
```

Pin a version:

```bash
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh \
  | bash -s -- --version v0.5.16
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
```

### Windows (CMD)

```bat
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.cmd -o install.cmd && install.cmd && del install.cmd
```

### Homebrew

```bash
brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
```

Or tap this repo:

```bash
brew tap UnicoLab/smlcode https://github.com/UnicoLab/smlcode
brew install slmcode
```

---

## ✅ Verify

```bash
slmcode version
slmcode doctor
```

You should see the binary path + active provider/model. Then:

```bash
cd your-project
slmcode init
slmcode                 # premium TUI
# or
slmcode run -v "say hello in a tiny README"
```

---

## 🔄 Update

```bash
slmcode update          # re-download latest release (binary installs)
slmcode update --check  # dry status
```

Or re-run the one-liner. Homebrew:

```bash
brew upgrade slmcode    # after re-installing the formula / tap update
```

---

## 🗑️ Uninstall

```bash
# macOS / Linux (user install)
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh \
  | bash -s -- --uninstall

# system install
curl -fsSL …/install-remote.sh | bash -s -- --uninstall --system

# Homebrew
brew uninstall slmcode
```

Windows: delete `%LOCALAPPDATA%\slmcode\bin\slmcode.exe` (or re-run `install.ps1` saved locally with `-Uninstall`).

---

## 🛠️ Build from source (developers)

Needs Go 1.23+. GoLangGraph is pulled from the module proxy (no local clone required).

```bash
git clone https://github.com/UnicoLab/smlcode.git
cd smlcode
make install-system     # → brew prefix or /usr/local/bin
# or
make install            # → ~/.local/bin
./scripts/install.sh --system
```

---

## 🧭 What gets installed?

| Mode | Binary location |
|------|-----------------|
| User (default) | `~/.local/bin/slmcode` |
| System | `$(brew --prefix)/bin` or `/usr/local/bin` |
| Windows | `%LOCALAPPDATA%\slmcode\bin\slmcode.exe` |
| Homebrew | Cellar → linked on PATH |

Install metadata lands in `~/.config/slmcode/install.json` (Windows: `%APPDATA%\slmcode\install.json`) so `slmcode update` knows how you installed.

---

## 🆘 Troubleshooting

| Symptom | Fix |
|---------|-----|
| `slmcode: command not found` | Add `~/.local/bin` (or the system prefix) to `PATH`, open a new shell |
| Checksum mismatch | Re-run the installer; confirm you're not behind a broken proxy |
| Windows SmartScreen | Unblock the binary or install via PowerShell one-liner |
| Go build fails | Use the **one-liner** — no Go required |

Next: **[PROVIDERS.md](PROVIDERS.md)** (any LLM) · **[GUIDE.md](GUIDE.md)** (daily workflow)
