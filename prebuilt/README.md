# `prebuilt/` — SLMCode binaries carried inside the repository

macOS builds of the current release, committed to git so that **`git clone` is
itself the delivery mechanism**.

## Why this exists

Every other way to install SLMCode needs something a corporate proxy commonly
refuses with a `403`:

| Path | Needs | Fails when |
|---|---|---|
| `brew install --formula …` | Homebrew updating itself | `brew update` is blocked |
| `scripts/install.sh` | a Go toolchain | go.dev / the module proxy is blocked |
| `scripts/install-remote.sh` | `api.github.com` + `objects.githubusercontent.com` | release-asset downloads are blocked |

On a locked-down workstation `git clone` typically still works, because the git
endpoint is allowlisted and nothing else is. So the binary ships with the
source:

```bash
git clone --depth 1 https://github.com/UnicoLab/smlcode.git
cd smlcode
./scripts/install-offline.sh
```

`scripts/install-offline.sh` decompresses, verifies the checksum, clears the
macOS quarantine attribute, smoke-tests the binary and puts it on `PATH`. It
makes no network calls at all.

## What is in here

```
VERSION                            the release these binaries were built from
SHA256SUMS                         sha256 of the UNCOMPRESSED binaries
slmcode_<version>_darwin_arm64.gz  Apple Silicon
slmcode_<version>_darwin_amd64.gz  Intel (and Apple Silicon under Rosetta 2)
```

The checksums are of the *uncompressed* binaries, under their release asset
names, so a line here is byte-identical to the matching line in the published
release `SHA256SUMS`. To confirm these files are the same builds GitHub serves:

```bash
curl -fsSL https://github.com/UnicoLab/smlcode/releases/download/v$(cat prebuilt/VERSION)/SHA256SUMS \
  | grep darwin | diff - <(grep darwin prebuilt/SHA256SUMS) && echo "identical"
```

## How it is refreshed

```bash
make prebuilt          # cross-compiles, then rewrites this directory
```

`.github/workflows/release.yml` runs the same script on `main` immediately after
a release is published, in the same commit that syncs the Homebrew formula — so
a fresh clone always carries the newest release, never a build from some
in-between commit.

Only one version lives here at a time: `scripts/update-prebuilt.sh` deletes the
previous binaries before writing the new ones.

## The cost, stated plainly

Committed binaries never leave git history. Each release adds roughly **20 MiB**
of blobs that stay in the repository forever, and a full clone gets slower for
everyone with every release.

Two things keep that bounded:

- **macOS only by default.** Linux and Windows users are not the ones being
  blocked — they take the one-liner. Override with
  `PREBUILT_PLATFORMS="darwin/arm64 linux/amd64" make prebuilt` if that changes,
  and know that it is ~10 MiB per platform per release.
- **`--depth 1` in every documented clone.** A shallow clone downloads only the
  tip, so the user's download stays ~20 MiB no matter how long the history gets.

If history growth does become a problem, the migration is to move this directory
to a force-pushed orphan branch (`git clone --depth 1 --branch prebuilt …`),
which keeps exactly one release's worth of objects alive. The installer already
searches `$SLMCODE_PREBUILT_DIR` first, so that move needs no change to how
anyone installs.
