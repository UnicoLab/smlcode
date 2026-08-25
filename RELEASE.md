# Cutting a SLMCode release

The exact sequence for shipping a version, written for the person running it. It assumes
a clean checkout of `main`, push access to `UnicoLab/smlcode`, Go, Node 18+ and `make`.

The short version: **you tag; CI does the rest.** Everything between the tag landing and
the release appearing is `.github/workflows/release.yml`. Your job is to make sure the
tree deserves the tag, and to verify the published result afterwards.

> **Repo slug.** The GitHub repository is `UnicoLab/smlcode`. The Go module is
> `github.com/UnicoLab/slmcode`. Those two strings differ by two letters and both are
> correct. `scripts/check-repo-refs.sh` exists to stop the module path leaking into a
> download URL; if it fails, believe it.

---

## 0. Preflight (do this first, it is the part that actually catches things)

```bash
cd /path/to/smlcode
git switch main && git pull --ff-only
git status --porcelain          # must be empty
```

**Regenerate the web lockfile if it is stale.** `web/package-lock.json` currently predates
several `devDependencies` in `web/package.json`, so a strict `npm ci` refuses to run. CI
falls back to `npm install` and still builds, but the lockfile is the fix:

```bash
cd web && npm install && cd ..
git diff --stat web/package-lock.json
# if it changed:
git add web/package-lock.json && git commit -m "chore(web): regenerate package-lock.json"
```

**Build the Studio UI and run the full gate.** `make bootstrap` is what puts the real SPA
into `cmd/slmcode/ui/`; without it every binary you build locally serves the placeholder
page from `pkg/server`.

```bash
make bootstrap                  # npm deps + vite build + sync into cmd/slmcode/ui/
make check                      # gofmt, vet, golangci-lint (0 issues), coverage floor, -race, web lint+build
./scripts/check-version.sh      # version.go == Makefile == Formula
./scripts/check-repo-refs.sh    # no UnicoLab/slmcode download URLs
```

**Confirm your local binary really embeds the Studio** — this is the one failure mode that
silently ships:

```bash
make build
ls cmd/slmcode/ui/assets/*.js   # must list at least one bundle
./bin/slmcode studio            # open the printed URL (it carries ?t=…) — you should see
                                # the real Studio, not "Studio not built"
```

**Confirm the changelog is written.** `docs/changelog.md` needs a real `## vX.Y.Z` entry
with a **Breaking behaviour changes** section, cross-linked to `docs/migration.md`. The
generated commit-subject dump is a fallback for patch releases, not a substitute.

---

## 1. Bump, gate, commit and tag

```bash
scripts/prepare-release.sh 0.18.4 --dry-run     # look at the diff; nothing is committed
scripts/prepare-release.sh 0.18.4               # for real
```

What it does, in order:

1. Refuses if the tag exists or if any of the four release files are already dirty.
2. Sets the version in `cmd/slmcode/version.go`, `Makefile` and `Formula/slmcode.rb`.
3. Resets the four `sha256` values in the formula to the all-zero placeholder (CI fills
   them in after the binaries exist — see step 3).
4. Adds a changelog entry **only if** `docs/changelog.md` has no `## vX.Y.Z` heading yet.
5. Runs `scripts/check-version.sh --tag vX.Y.Z`, `scripts/check-repo-refs.sh`, `make check`.
6. Commits `chore: release vX.Y.Z` and creates the tag. **It does not push.**

For **v0.18.4 specifically**, the version and the changelog entry are already in the tree,
so the script reports *"No file changes needed — proceeding as a tag-only release"*, runs
the gate, and creates the tag with no release commit. That is correct.

Review before pushing:

```bash
git show --stat HEAD
git tag -v v0.18.4 2>/dev/null || git show v0.18.4 --stat | head
```

---

## 2. Push

```bash
git push origin main
git push origin v0.18.4          # this is what starts the release
```

Pushing the tag is the point of no return for the automation. Everything before it is
reversible with `git tag -d` and `git reset`.

---

## 3. What CI does (watch it, do not skip ahead)

`.github/workflows/release.yml`, in order. Roughly 30–45 minutes.

| # | Step | Fails the release if |
|---|---|---|
| 1 | Validate the tag shape | the tag is not `vX.Y.Z` |
| 2 | `scripts/check-version.sh --tag` | the tag disagrees with `version.go` / `Makefile` / the formula |
| 3 | `scripts/check-repo-refs.sh` | a broken repo slug reached a download URL |
| 4 | `make web-deps` | npm cannot install by either route |
| 5 | Install golangci-lint `v2.13.1` (must be built with Go >= go.mod's toolchain) | — (without this, `scripts/lint.sh` *silently skips* linting) |
| 6 | **`make ui-react`** | the Vite build fails |
| 7 | Strip `*.map` from `cmd/slmcode/ui/` | — (keeps the TSX source out of the binaries) |
| 8 | `make check` | gofmt, vet, lint, coverage floor, race or web build fails |
| 9 | **Verify the real Studio is embedded** | `cmd/slmcode/ui/` has no `index.html` + `assets/*.js`, or a sourcemap survived |
| 10 | Cross-compile six binaries | any target fails |
| 11 | `sha256sum` → `SHA256SUMS` | — |
| 12 | Smoke-test `linux_amd64` | `version --json` reports the wrong version, an unstamped commit/build time, or a leaked `SourceRoot` |
| 13 | Create the GitHub Release | the upload fails |
| 14 | `scripts/update-formula.sh` in a fresh clone → push to `main` | a placeholder `sha256` survives, or the version line is wrong |
| 15 | Re-download every asset and `sha256sum -c` | what GitHub serves differs from what was built |

Steps 6, 9 and 12 are the ones added because the old workflow could publish a binary that
served "Studio not built" to every user without failing.

Published artifacts:

```
slmcode_0.18.4_darwin_arm64      slmcode_0.18.4_windows_amd64.exe
slmcode_0.18.4_darwin_amd64      slmcode_0.18.4_windows_arm64.exe
slmcode_0.18.4_linux_arm64       install.sh  install.ps1  install.cmd
slmcode_0.18.4_linux_amd64       SHA256SUMS
```

---

## 4. Verify the published release by hand

CI verifies the bytes. These are the things only a human on a real machine can check.

**Checksums and the binary:**

```bash
cd "$(mktemp -d)"
curl -fsSLO https://github.com/UnicoLab/smlcode/releases/download/v0.18.4/SHA256SUMS
curl -fsSLO https://github.com/UnicoLab/smlcode/releases/download/v0.18.4/slmcode_0.18.4_darwin_arm64
shasum -a 256 -c SHA256SUMS --ignore-missing        # must say OK
chmod +x slmcode_0.18.4_darwin_arm64
./slmcode_0.18.4_darwin_arm64 version --json        # version 0.18.4, real commit, real built
```

**The install one-liner, on a machine that has never had slmcode:**

```bash
curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
slmcode version                                     # 0.18.4
slmcode doctor
```

Watch for `✔ Checksum OK (sha256 …)` in the output. If you instead see a `⚠ could not
verify checksum` warning, the release is missing `SHA256SUMS` — treat that as a failed
release, not a cosmetic issue.

**Studio actually works (the whole point of steps 6/9 above):**

```bash
cd "$(mktemp -d)" && slmcode init && slmcode studio
# open the printed URL — it carries ?t=<token>. You must get the real Studio UI.
# "Studio not built" means CI shipped a placeholder: pull the release (step 6 below).
```

**Homebrew** — only after the `chore: sync Homebrew formula checksums for v0.18.4`
commit has landed on `main` (CI step 14). Before that, the formula carries all-zero
placeholder checksums and `brew install` will refuse; that is expected, not a break-in.

```bash
brew uninstall slmcode 2>/dev/null || true
brew install --formula https://raw.githubusercontent.com/UnicoLab/smlcode/main/Formula/slmcode.rb
slmcode version
brew test slmcode           # runs `version`, `version --json`, `init`, `status`, and an unknown-command exit-2 check
```

**Windows**, in a fresh PowerShell:

```powershell
irm https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install.ps1 | iex
slmcode version
```

Confirm `-> Checksum OK (sha256 …)` appears. This path had no checksum verification at all
before 0.18.4, so it is worth watching once.

**Self-update from the previous release:**

```bash
# on a machine still running 0.16.0
slmcode update --check      # must report v0.18.4 is available
slmcode update --yes
slmcode version             # 0.18.4
```

---

## 5. After it lands

- Confirm `main` has the `chore: sync Homebrew formula checksums` commit and that
  `./scripts/check-version.sh` on a fresh pull no longer reports placeholder checksums.
- Confirm the docs site rebuilt (`.github/workflows/docs.yml`) and that
  [Install](docs/install.md), [Migration notes](docs/migration.md) and
  [Changelog](docs/changelog.md) render.
- Announce the **breaking behaviour changes**, not the feature list. For 0.18.4 those are:
  hooks fail closed, project `mcp_servers` ignored, the tiered shell allowlist,
  `slmcode apply` interactive, HITL gates blocking when attended, the Studio session token,
  and the new `.slmcode/memory` + `.slmcode/evolve` directories.

---

## 6. Rolling back

**Before the tag is pushed** — nothing has happened:

```bash
git tag -d v0.18.4
git reset --hard origin/main
```

**After the tag is pushed but CI failed** — no release exists, so just fix and re-tag:

```bash
git push --delete origin v0.18.4
git tag -d v0.18.4
# fix, commit, then repeat from step 1
```

**After the release published and something is wrong** — do **not** delete and re-upload
the same tag. People and caches already have those bytes, and a tag that means two
different things is worse than a bad release.

1. Mark the GitHub release as a **pre-release** so `releases/latest` stops resolving to it.
   That immediately stops `slmcode update`, both install one-liners and the version notice
   from offering it, because all four read `/releases/latest`.
2. Revert the Homebrew formula on `main` to the previous version and its real checksums:
   ```bash
   git revert <sha-of-the-formula-sync-commit>
   git push origin main
   ```
   Users on `brew install --formula <raw url>` follow `main`, so this is the fastest lever.
3. Cut **v0.17.1** from a fixed tree using this document from step 0. A forward fix is the
   only rollback that reaches everyone.
4. If the release is actively harmful (a broken binary, a leaked secret), delete the
   assets from the GitHub release — keep the tag and the release page, with a note saying
   what happened and which version to use instead.

---

## Files this process touches

| File | Role |
|---|---|
| `cmd/slmcode/version.go` | the version compiled in when no `-ldflags` are given |
| `Makefile` (`VERSION ?=`) | what local builds stamp |
| `Formula/slmcode.rb` | Homebrew version + the four checksums CI syncs |
| `docs/changelog.md` | the release entry, with the breaking-changes table |
| `docs/migration.md` | the per-change detail the changelog links to |
| `scripts/prepare-release.sh` | bump + gate + commit + tag |
| `scripts/check-version.sh` | the drift guard (also runs in CI and in the release workflow) |
| `scripts/check-repo-refs.sh` | the repo-slug guard |
| `scripts/update-formula.sh` | post-release checksum sync, run by CI |
| `.github/workflows/release.yml` | everything after the tag is pushed |
