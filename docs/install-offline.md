# 🔒 Install on a locked-down machine

For workstations where the network is filtered and the usual installers come
back `403`. **No Homebrew, no Go toolchain, no downloads.** If `git clone`
works, this works.

```bash
git clone --depth 1 https://github.com/UnicoLab/smlcode.git
cd smlcode
./scripts/install-offline.sh --add-to-path
```

Open a new terminal, then:

```bash
slmcode doctor
```

That is the whole thing. The macOS binaries live **inside the repository**, in
`prebuilt/`, so the clone *is* the download.

---

## Why the other installers fail there

Each one needs something a corporate proxy commonly blocks — and each fails
with a `403` that looks like a bug rather than a policy:

| Installer | Needs | Blocked when |
|---|---|---|
| `brew install --formula …` | Homebrew updating itself first | `brew update` hits `403` on GitHub |
| `scripts/install.sh` | a Go toolchain + module proxy | `go.dev` / `proxy.golang.org` blocked |
| `scripts/install-remote.sh` | `api.github.com` + `objects.githubusercontent.com` | release-asset downloads blocked |

`git clone` usually survives, because the git endpoint is allowlisted for work
that has to get done. `install-offline.sh` uses only that.

---

## What the script actually does

1. Detects your OS and CPU (`darwin`/`arm64` on Apple Silicon, `amd64` on Intel).
2. Finds the matching binary — `prebuilt/` first, then `dist/`.
3. Decompresses it into a temp directory.
4. **Verifies the SHA-256** against `prebuilt/SHA256SUMS`. A mismatch aborts.
5. Clears `com.apple.quarantine` on its own staged copy, so Gatekeeper does not
   show *"slmcode cannot be opened because the developer cannot be verified"*.
6. Runs `slmcode version` as a smoke test — a binary that will not start is
   never installed.
7. Copies it to `~/.local/bin/slmcode` (or `--system`), atomically.
8. Installs shell completions and records `~/.config/slmcode/install.json`.

It makes **zero network calls**. You can verify that with `--help`, or by
reading it — it is one file, `scripts/install-offline.sh`.

---

## Options

```bash
./scripts/install-offline.sh --list          # what does this checkout carry?
./scripts/install-offline.sh                 # → ~/.local/bin/slmcode
./scripts/install-offline.sh --add-to-path   # …and add that dir to your shell rc
./scripts/install-offline.sh --system        # → Homebrew prefix or /usr/local/bin
./scripts/install-offline.sh --prefix ~/tools/slmcode
./scripts/install-offline.sh --arch amd64    # force the Intel build (Rosetta 2)
./scripts/install-offline.sh --binary ./slmcode_0.19.1_darwin_arm64
./scripts/install-offline.sh --uninstall
```

`--system` calls `brew --prefix` only to locate a directory. It never runs
`brew update`, so it is safe on a machine where every other brew command 403s.

---

## Updating

`slmcode update` downloads a release asset — the exact thing your proxy is
blocking. On a locked-down machine, update the same way you installed:

```bash
cd smlcode
git pull
./scripts/install-offline.sh
```

`prebuilt/` is refreshed on `main` every time a release is cut, so `git pull`
brings the new binary with it.

---

## No git either? Sneakernet it

`--binary` takes any file, so the binary can arrive however it can arrive — a
USB stick, an internal artifact store, a colleague's machine:

```bash
# On a machine that CAN reach GitHub:
curl -fsSLO https://github.com/UnicoLab/smlcode/releases/latest/download/slmcode_0.19.1_darwin_arm64

# On the locked-down Mac, after copying the file across:
./scripts/install-offline.sh --binary ~/Downloads/slmcode_0.19.1_darwin_arm64
```

A file that arrives via a browser download **is** quarantined by macOS; the
script clears that attribute for you.

---

## Verifying you got the real thing

`prebuilt/SHA256SUMS` holds the checksums of the *uncompressed* binaries under
their release asset names, so its lines are byte-identical to the published
release `SHA256SUMS`. From any machine that can reach GitHub:

```bash
curl -fsSL "https://github.com/UnicoLab/smlcode/releases/download/v$(cat prebuilt/VERSION)/SHA256SUMS" \
  | grep darwin | diff - <(grep darwin prebuilt/SHA256SUMS) && echo "identical"
```

The installer checks the same digest before it copies anything onto your `PATH`.

---

## Troubleshooting

**`zsh: command not found: slmcode`**
`~/.local/bin` is not on your `PATH`. Re-run with `--add-to-path`, or add
`export PATH="$HOME/.local/bin:$PATH"` to `~/.zshrc` and open a new terminal.

**`"slmcode" cannot be opened because the developer cannot be verified`**
The file was quarantined (it came from a browser download or a `.zip`, not a
clone). Clear it: `xattr -c ~/.local/bin/slmcode`. Re-running the installer
does this for you.

**`bad CPU type in executable`**
Wrong architecture. Check with `uname -m` (`arm64` = Apple Silicon, `x86_64` =
Intel) and re-run with `--arch arm64` or `--arch amd64`. To run the Intel build
on Apple Silicon you also need `softwareupdate --install-rosetta`.

**`error: no SLMCode binary for darwin/arm64 in this checkout`**
The clone predates `prebuilt/`. `git pull`, then
`./scripts/install-offline.sh --list` to see what is there.

**`checksum mismatch`**
Do not install it. A TLS-inspecting proxy or a partial clone can rewrite bytes
in transit. Re-clone with `git clone --depth 1 …` and try again.

---

## Maintainers: keeping `prebuilt/` current

```bash
make prebuilt    # cross-compile, then rewrite prebuilt/ from dist/
```

`.github/workflows/release.yml` runs `scripts/update-prebuilt.sh` against the
just-published `dist/` on every tagged release, in the same commit that syncs
the Homebrew formula. The committed binaries are therefore *the release
binaries*, not a rebuild.

macOS only by default — Linux and Windows users are not the ones being blocked.
Each extra platform costs ~10 MiB of permanent git history per release; the
trade-off, and the migration path if it ever stops being worth it, are written
down in [`prebuilt/README.md`](https://github.com/UnicoLab/smlcode/blob/main/prebuilt/README.md).
