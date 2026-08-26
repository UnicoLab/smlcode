#!/usr/bin/env bash
# Install SLMCode from a binary that is already on this machine — no Homebrew,
# no Go toolchain, no download, no network of any kind.
#
# For locked-down workstations. The other three installers each need something a
# corporate proxy commonly refuses with a 403: `brew` has to update itself first,
# scripts/install.sh needs a Go toolchain to compile, and scripts/install-remote.sh
# needs api.github.com plus a release-asset fetch from objects.githubusercontent.com.
# `git clone` usually survives that filtering, so the binary ships inside the
# repository (prebuilt/) and this script is the last mile:
#
#   git clone --depth 1 https://github.com/UnicoLab/smlcode.git
#   cd smlcode
#   ./scripts/install-offline.sh
#
# Usage:
#   ./scripts/install-offline.sh                 # → ~/.local/bin/slmcode
#   ./scripts/install-offline.sh --add-to-path   # …and add that dir to your shell rc
#   ./scripts/install-offline.sh --system        # → Homebrew prefix or /usr/local/bin
#   ./scripts/install-offline.sh --prefix /opt/slmcode
#   ./scripts/install-offline.sh --binary ./slmcode_<version>_darwin_arm64
#   ./scripts/install-offline.sh --list
#   ./scripts/install-offline.sh --uninstall [--system]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_NAME="slmcode"
MODE="user"
DO_UNINSTALL=0
DO_COMPLETION=1
DO_LIST=0
ADD_TO_PATH=0
PREFIX_OVERRIDE=""
BINARY_OVERRIDE=""
OS_OVERRIDE=""
ARCH_OVERRIDE=""

usage() {
  cat <<'EOF'
Install SLMCode from a binary carried in this checkout (fully offline).

  ./scripts/install-offline.sh                  User install → ~/.local/bin/slmcode
  ./scripts/install-offline.sh --system         System install → Homebrew or /usr/local/bin
  ./scripts/install-offline.sh --prefix DIR     Custom prefix (binary → DIR/bin/slmcode)
  ./scripts/install-offline.sh --list           Show the binaries this checkout carries
  ./scripts/install-offline.sh --uninstall [--system]

Options:
  --binary PATH     Install this exact file (plain or .gz) instead of searching
  --os NAME         Override detected OS   (darwin | linux)
  --arch NAME       Override detected arch (arm64 | amd64)
  --add-to-path     Append the install dir to ~/.zshrc / ~/.bashrc if it is missing
  --no-completion   Skip shell completion install
  -h, --help        Show this help

Where it looks, in order:
  1. --binary PATH
  2. $SLMCODE_PREBUILT_DIR, if set
  3. prebuilt/   — macOS binaries committed to the repo (the offline channel)
  4. dist/       — output of a local `make release-binaries`

Updating on a locked-down machine (no release downloads, so not `slmcode update`):
  git pull && ./scripts/install-offline.sh
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --system|-s) MODE="system"; shift ;;
    --user|-u) MODE="user"; shift ;;
    --prefix) PREFIX_OVERRIDE="${2:-}"; shift 2 ;;
    --binary|-b) BINARY_OVERRIDE="${2:-}"; shift 2 ;;
    --os) OS_OVERRIDE="${2:-}"; shift 2 ;;
    --arch) ARCH_OVERRIDE="${2:-}"; shift 2 ;;
    --list|-l) DO_LIST=1; shift ;;
    --add-to-path) ADD_TO_PATH=1; shift ;;
    --uninstall) DO_UNINSTALL=1; shift ;;
    --no-completion) DO_COMPLETION=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

# ---------------------------------------------------------------- host & paths

HOST_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
HOST_ARCH="$(uname -m)"
case "${HOST_ARCH}" in
  x86_64|amd64) HOST_ARCH="amd64" ;;
  aarch64|arm64) HOST_ARCH="arm64" ;;
esac

OS="${OS_OVERRIDE:-${HOST_OS}}"
ARCH="${ARCH_OVERRIDE:-${HOST_ARCH}}"

case "${OS}" in
  darwin|linux) ;;
  msys*|mingw*|cygwin*)
    echo "error: this script is for macOS and Linux." >&2
    echo "  On Windows, run scripts/install.ps1 from the checkout:" >&2
    echo "    powershell -ExecutionPolicy Bypass -File scripts\\install.ps1" >&2
    exit 1
    ;;
  *)
    echo "error: unsupported OS: ${OS}" >&2
    exit 1
    ;;
esac
case "${ARCH}" in
  amd64|arm64) ;;
  *)
    echo "error: unsupported architecture: ${ARCH}" >&2
    exit 1
    ;;
esac

detect_system_prefix() {
  if [[ -n "${PREFIX_OVERRIDE}" ]]; then
    echo "${PREFIX_OVERRIDE}"
    return
  fi
  # `brew --prefix` and nothing else — this never runs `brew update`, so it is
  # safe on the very machines where every other brew command 403s.
  if command -v brew >/dev/null 2>&1; then
    local bp
    bp="$(brew --prefix 2>/dev/null || true)"
    if [[ -n "${bp}" && -d "${bp}/bin" ]]; then
      echo "${bp}"
      return
    fi
  fi
  if [[ -d /opt/homebrew/bin ]]; then
    echo "/opt/homebrew"
    return
  fi
  echo "/usr/local"
}

if [[ -n "${PREFIX:-}" && -z "${PREFIX_OVERRIDE}" ]]; then
  PREFIX_OVERRIDE="${PREFIX}"
fi

if [[ "${MODE}" == "system" ]]; then
  PREFIX="$(detect_system_prefix)"
else
  PREFIX="${PREFIX_OVERRIDE:-${HOME}/.local}"
fi

BIN_DIR="${PREFIX}/bin"
TARGET="${BIN_DIR}/${BIN_NAME}"

# Only a --system install may escalate. A user install onto a machine with no
# ~/.local yet sees both "BIN_DIR unwritable" and "parent unwritable" (a
# directory that does not exist is not writable); without the MODE test that
# ran `sudo mkdir -p ~/.local/bin` and left the user's own tree owned by root.
need_sudo() {
  [[ "${MODE}" == "system" ]] || return 1
  [[ ! -w "${BIN_DIR}" ]] && [[ ! -w "$(dirname "${BIN_DIR}")" ]]
}

run_as_needed() {
  if need_sudo; then
    echo "→ ${BIN_DIR} is not writable; using sudo"
    sudo "$@"
  else
    "$@"
  fi
}

# ------------------------------------------------------------------- uninstall

if [[ "${DO_UNINSTALL}" -eq 1 ]]; then
  if [[ -e "${TARGET}" || -L "${TARGET}" ]]; then
    run_as_needed rm -f "${TARGET}"
    echo "Removed ${TARGET}"
  else
    echo "Not installed at ${TARGET}"
  fi
  USER_BIN="${HOME}/.local/bin/${BIN_NAME}"
  if [[ -L "${USER_BIN}" && "$(readlink "${USER_BIN}" 2>/dev/null || true)" == "${TARGET}" ]]; then
    rm -f "${USER_BIN}"
    echo "Removed symlink ${USER_BIN}"
  fi
  META="${XDG_CONFIG_HOME:-${HOME}/.config}/slmcode/install.json"
  if [[ -f "${META}" ]]; then
    rm -f "${META}"
    echo "Removed ${META}"
  fi
  echo
  echo "Left in place (yours, not the installer's): per-project .slmcode/ directories"
  echo "and ~/.config/slmcode/ settings."
  exit 0
fi

# -------------------------------------------------------------- find a binary

SEARCH_DIRS=()
[[ -n "${SLMCODE_PREBUILT_DIR:-}" ]] && SEARCH_DIRS+=("${SLMCODE_PREBUILT_DIR}")
SEARCH_DIRS+=("${ROOT}/prebuilt" "${ROOT}/dist")

# Newest version first, so a checkout that happens to carry two versions
# installs the newer one instead of whichever the shell globbed first.
list_candidates() {
  local dir="$1" want_os="$2" want_arch="$3" f
  [[ -d "${dir}" ]] || return 0
  shopt -s nullglob
  for f in "${dir}/${BIN_NAME}_"*"_${want_os}_${want_arch}" \
           "${dir}/${BIN_NAME}_"*"_${want_os}_${want_arch}.gz"; do
    [[ -f "${f}" ]] || continue
    printf '%s\t%s\n' "$(asset_version "$(basename "${f}")")" "${f}"
  done
  shopt -u nullglob
}

# slmcode_<version>_darwin_arm64.gz → <version>
asset_version() {
  local name="${1#"${BIN_NAME}_"}"
  name="${name%.gz}"
  printf '%s' "${name%%_*}"
}

# `head -1` can close the pipe on `sort` mid-write; under `pipefail` that
# surfaces as exit 141 and `set -e` would abort the search instead of moving on
# to the next directory. The `|| true` keeps a truncated read from looking like
# a failure — an empty result already means "nothing here".
pick_candidate() {
  local dir="$1"
  { list_candidates "${dir}" "${OS}" "${ARCH}" \
    | sort -t. -k1,1nr -k2,2nr -k3,3nr \
    | head -1 | cut -f2-; } || true
}

if [[ "${DO_LIST}" -eq 1 ]]; then
  echo "Host: ${HOST_OS}/${HOST_ARCH}   Installing for: ${OS}/${ARCH}"
  found=0
  for dir in "${SEARCH_DIRS[@]}"; do
    [[ -d "${dir}" ]] || continue
    shopt -s nullglob
    entries=("${dir}/${BIN_NAME}_"*)
    shopt -u nullglob
    [[ "${#entries[@]}" -gt 0 ]] || continue
    echo
    echo "${dir#"${ROOT}/"}/"
    for f in "${entries[@]}"; do
      [[ -f "${f}" ]] || continue
      printf '  %-42s %s\n' "$(basename "${f}")" "$(du -h "${f}" | cut -f1)"
      found=1
    done
  done
  [[ "${found}" -eq 1 ]] || echo "(no binaries found — run 'make prebuilt' or 'make release-binaries')"
  exit 0
fi

SOURCE=""
if [[ -n "${BINARY_OVERRIDE}" ]]; then
  if [[ ! -f "${BINARY_OVERRIDE}" ]]; then
    echo "error: --binary ${BINARY_OVERRIDE} does not exist" >&2
    exit 1
  fi
  SOURCE="${BINARY_OVERRIDE}"
else
  for dir in "${SEARCH_DIRS[@]}"; do
    SOURCE="$(pick_candidate "${dir}" || true)"
    [[ -n "${SOURCE}" ]] && break
  done
fi

if [[ -z "${SOURCE}" ]]; then
  echo "error: no SLMCode binary for ${OS}/${ARCH} in this checkout." >&2
  echo >&2
  echo "  Looked in:" >&2
  for dir in "${SEARCH_DIRS[@]}"; do echo "    ${dir}" >&2; done
  echo >&2
  echo "  Fixes, cheapest first:" >&2
  echo "    git pull                                  # the binaries ship in prebuilt/" >&2
  echo "    ./scripts/install-offline.sh --list       # see what this checkout carries" >&2
  if [[ "${OS}" == "darwin" && "${ARCH}" == "arm64" ]]; then
    echo "    ./scripts/install-offline.sh --arch amd64 # Apple Silicon can run this under Rosetta" >&2
  fi
  echo "    make release-binaries                     # build them yourself (needs Go)" >&2
  exit 1
fi

SOURCE_DIR="$(cd "$(dirname "${SOURCE}")" && pwd)"
ASSET="$(basename "${SOURCE}")"
ASSET="${ASSET%.gz}"
VERSION="$(asset_version "${ASSET}")"

echo "→ Source: ${SOURCE#"${ROOT}/"}"
echo "→ Target: ${TARGET}  (${OS}/${ARCH}, SLMCode ${VERSION})"

# The version this checkout would compile. When it has moved past the committed
# binary, the source is simply ahead of the last release — worth saying out
# loud, never worth refusing over.
SRC_VERSION="$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' \
  "${ROOT}/cmd/slmcode/version.go" 2>/dev/null | head -1 || true)"
if [[ -n "${SRC_VERSION}" && -n "${VERSION}" && "${SRC_VERSION}" != "${VERSION}" ]]; then
  echo "note: this checkout's source is ${SRC_VERSION}; the binary being installed is ${VERSION}."
  echo "      prebuilt/ is refreshed when a release is cut, so it trails unreleased commits."
fi

# --------------------------------------------------------- unpack + verify

TMPDIR_INSTALL="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_INSTALL}"' EXIT
STAGED="${TMPDIR_INSTALL}/${BIN_NAME}"

if [[ "${SOURCE}" == *.gz ]]; then
  echo "→ Decompressing…"
  if ! gzip -dc "${SOURCE}" > "${STAGED}"; then
    echo "error: could not decompress ${SOURCE}" >&2
    echo "  If this clone came from a zip, or git rewrote the file, re-clone with:" >&2
    echo "    git clone --depth 1 https://github.com/UnicoLab/smlcode.git" >&2
    exit 1
  fi
else
  cp "${SOURCE}" "${STAGED}"
fi
chmod +x "${STAGED}"

sha256_of() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  fi
}

# SHA256SUMS lists the UNCOMPRESSED binaries under their release asset names, so
# these lines are directly comparable with the published release SHA256SUMS.
SUMS="${SOURCE_DIR}/SHA256SUMS"
if [[ -f "${SUMS}" ]]; then
  EXPECTED="$(awk -v f="${ASSET}" '$2 == f { print $1; exit }' "${SUMS}")"
  if [[ -z "${EXPECTED}" ]]; then
    echo "⚠ ${ASSET} is not listed in ${SUMS#"${ROOT}/"} — installing unverified." >&2
  else
    GOT="$(sha256_of "${STAGED}")"
    if [[ -z "${GOT}" ]]; then
      # Never print "checksum OK" when nothing was hashed.
      echo "⚠ neither shasum nor sha256sum is installed — the binary was NOT verified." >&2
    elif [[ "${GOT}" != "${EXPECTED}" ]]; then
      echo "error: checksum mismatch for ${ASSET}" >&2
      echo "  expected ${EXPECTED}" >&2
      echo "  got      ${GOT}" >&2
      echo "  Refusing to install. Re-clone the repository; if it persists, the file" >&2
      echo "  was altered in transit or in this working tree." >&2
      exit 1
    else
      echo "✔ Checksum OK (sha256 ${GOT})"
    fi
  fi
else
  echo "⚠ no SHA256SUMS next to ${ASSET} — installing unverified." >&2
fi

# ------------------------------------------------------------ macOS gatekeeper

if [[ "${OS}" == "darwin" && "${HOST_OS}" == "darwin" ]]; then
  # A git clone never sets com.apple.quarantine, but a downloaded .zip of the
  # repo does, and it propagates to every extracted file — which is exactly the
  # "slmcode cannot be opened because the developer cannot be verified" dialog.
  # Clearing it on our own staged copy is not a Gatekeeper bypass for anything
  # else on the system.
  if command -v xattr >/dev/null 2>&1; then
    xattr -c "${STAGED}" 2>/dev/null || true
  fi
  # Apple Silicon refuses to exec an arm64 binary with no code signature at all.
  # The Go linker ad-hoc signs darwin/arm64 builds, so this only fires if the
  # signature was damaged; re-signing changes the bytes, hence only after the
  # checksum above has already passed.
  if [[ "${ARCH}" == "arm64" ]] && command -v codesign >/dev/null 2>&1; then
    if ! codesign -v "${STAGED}" >/dev/null 2>&1; then
      echo "→ Ad-hoc signing (the shipped signature did not verify)…"
      codesign --force --sign - "${STAGED}" >/dev/null 2>&1 \
        || echo "⚠ could not ad-hoc sign; if macOS refuses to run it, install Xcode CLT: xcode-select --install" >&2
    fi
  fi
fi

# --------------------------------------------------------------- smoke test

RUNNABLE=0
if [[ "${OS}" == "${HOST_OS}" ]] && { [[ "${ARCH}" == "${HOST_ARCH}" ]] || [[ "${HOST_ARCH}" == "arm64" && "${ARCH}" == "amd64" && "${HOST_OS}" == "darwin" ]]; }; then
  RUNNABLE=1
fi

if [[ "${RUNNABLE}" -eq 1 ]]; then
  if SLMCODE_SKIP_UPDATE_CHECK=1 "${STAGED}" version >/dev/null 2>&1; then
    echo "✔ Binary runs on this machine"
  else
    echo "error: the binary would not run on this machine." >&2
    echo >&2
    SLMCODE_SKIP_UPDATE_CHECK=1 "${STAGED}" version >&2 || true
    echo >&2
    if [[ "${HOST_OS}" == "darwin" ]]; then
      echo "  macOS usually means one of:" >&2
      echo "    • Gatekeeper quarantine → xattr -c '${STAGED}'" >&2
      if [[ "${ARCH}" == "amd64" && "${HOST_ARCH}" == "arm64" ]]; then
        echo "    • Rosetta 2 is not installed → softwareupdate --install-rosetta" >&2
        echo "      (or re-run without --arch amd64 to use the native arm64 build)" >&2
      fi
      echo "    • wrong architecture → this Mac is ${HOST_ARCH}; re-run with --arch ${HOST_ARCH}" >&2
    fi
    exit 1
  fi
fi

# ------------------------------------------------------------------- install

echo "→ Installing…"
run_as_needed mkdir -p "${BIN_DIR}"
# Staged copy then atomic rename, so replacing a running binary is safe.
run_as_needed cp "${STAGED}" "${TARGET}.new"
run_as_needed mv -f "${TARGET}.new" "${TARGET}"
run_as_needed chmod 755 "${TARGET}"

# PATH usually prefers ~/.local/bin over Homebrew; point the user copy at the
# system one so `which slmcode` resolves to a single install.
USER_BIN="${HOME}/.local/bin/${BIN_NAME}"
if [[ "${MODE}" == "system" && "${TARGET}" != "${USER_BIN}" ]]; then
  mkdir -p "${HOME}/.local/bin"
  rm -f "${USER_BIN}"
  ln -sf "${TARGET}" "${USER_BIN}"
  echo "→ Linked ${USER_BIN} → ${TARGET}"
fi

# 0700/0600, matching pkg/installmeta.Save: install.json names the upstream that
# `slmcode update` fetches from, so write access to it chooses what runs next.
#
# method is "binary" (not "source"): there is no Go toolchain here, so a future
# `slmcode update` must take the download path, not try to recompile. On a
# machine that blocks release downloads that update will fail — by design; the
# supported refresh is `git pull && ./scripts/install-offline.sh`.
META_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/slmcode"
mkdir -p "${META_DIR}"
chmod 700 "${META_DIR}" 2>/dev/null || true
(umask 077; : > "${META_DIR}/install.json")
cat > "${META_DIR}/install.json" <<EOF
{
  "source": "",
  "prefix": "${PREFIX}",
  "mode": "${MODE}",
  "method": "binary",
  "version": "${VERSION}",
  "git_commit": "$(git -C "${ROOT}" rev-parse --short HEAD 2>/dev/null || echo "")",
  "binary": "${TARGET}",
  "repo": "UnicoLab/smlcode",
  "installed_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
chmod 600 "${META_DIR}/install.json" 2>/dev/null || true

hash -r 2>/dev/null || true

# ------------------------------------------------------------------ trimmings

install_completions() {
  [[ "${DO_COMPLETION}" -eq 1 ]] || return 0
  [[ "${RUNNABLE}" -eq 1 ]] || return 0
  local shell_name comp_dir tmp
  shell_name="$(basename "${SHELL:-zsh}")"
  tmp="$(mktemp)"
  case "${shell_name}" in
    zsh)
      "${TARGET}" completion zsh > "${tmp}" 2>/dev/null || { rm -f "${tmp}"; return 0; }
      if [[ "${MODE}" == "system" && -d "${PREFIX}/share/zsh/site-functions" ]]; then
        comp_dir="${PREFIX}/share/zsh/site-functions"
        run_as_needed cp "${tmp}" "${comp_dir}/_${BIN_NAME}"
      else
        comp_dir="${HOME}/.zsh/completions"
        mkdir -p "${comp_dir}"
        cp "${tmp}" "${comp_dir}/_${BIN_NAME}"
        if ! grep -q '\.zsh/completions' "${HOME}/.zshrc" 2>/dev/null; then
          echo
          echo "Add to ~/.zshrc for tab completion:"
          echo "  fpath=(~/.zsh/completions \$fpath)"
          echo "  autoload -Uz compinit && compinit"
        fi
      fi
      echo "Shell completion → ${comp_dir}/_${BIN_NAME}"
      ;;
    bash)
      "${TARGET}" completion bash > "${tmp}" 2>/dev/null || { rm -f "${tmp}"; return 0; }
      comp_dir="${HOME}/.local/share/bash-completion/completions"
      mkdir -p "${comp_dir}"
      cp "${tmp}" "${comp_dir}/${BIN_NAME}"
      echo "Shell completion → ${comp_dir}/${BIN_NAME}"
      ;;
    fish)
      comp_dir="${HOME}/.config/fish/completions"
      mkdir -p "${comp_dir}"
      "${TARGET}" completion fish > "${comp_dir}/${BIN_NAME}.fish" 2>/dev/null || return 0
      echo "Shell completion → ${comp_dir}/${BIN_NAME}.fish"
      ;;
  esac
  rm -f "${tmp}"
}

install_completions || echo "(completion install skipped — run: ${BIN_NAME} completion zsh)"

on_path() {
  case ":${PATH}:" in
    *":${BIN_DIR}:"*) return 0 ;;
    *) return 1 ;;
  esac
}

add_to_path() {
  local line="export PATH=\"${BIN_DIR}:\$PATH\"" rc
  case "$(basename "${SHELL:-zsh}")" in
    zsh) rc="${HOME}/.zshrc" ;;
    bash) rc="${HOME}/.bashrc" ;;
    fish)
      # fish uses its own syntax; `export PATH=…` in config.fish is a syntax error.
      echo "⚠ For fish, run this once instead (it persists):" >&2
      echo "  fish_add_path ${BIN_DIR}" >&2
      return 0
      ;;
    *)
      echo "⚠ --add-to-path only knows zsh, bash and fish; add this to your shell's rc file:" >&2
      echo "  ${line}" >&2
      return 0
      ;;
  esac
  if [[ -f "${rc}" ]] && grep -qF "${BIN_DIR}" "${rc}"; then
    echo "→ ${rc} already mentions ${BIN_DIR}"
    return 0
  fi
  printf '\n# Added by slmcode install-offline.sh\n%s\n' "${line}" >> "${rc}"
  echo "→ Added ${BIN_DIR} to PATH in ${rc} (open a new shell, or: source ${rc})"
}

echo
echo "✔ SLMCode ${VERSION} installed → ${TARGET}"
if [[ "${RUNNABLE}" -eq 1 ]]; then
  SLMCODE_SKIP_UPDATE_CHECK=1 "${TARGET}" version || true
fi

if ! on_path; then
  if [[ "${ADD_TO_PATH}" -eq 1 ]]; then
    add_to_path
  else
    echo
    echo "⚠  ${BIN_DIR} is not on your PATH. Either re-run with --add-to-path, or add:"
    case "$(basename "${SHELL:-zsh}")" in
      fish)
        # fish has no `export`; fish_add_path writes the universal variable.
        echo "     fish_add_path ${BIN_DIR}"
        echo "   (run it once — it persists)"
        ;;
      bash)
        echo "     export PATH=\"${BIN_DIR}:\$PATH\""
        echo "   to ~/.bashrc, then open a new terminal."
        ;;
      *)
        echo "     export PATH=\"${BIN_DIR}:\$PATH\""
        echo "   to ~/.zshrc, then open a new terminal."
        ;;
    esac
  fi
fi

cat <<EOF

Next:
  slmcode doctor
  cd your-project && slmcode init && slmcode

Updating on a locked-down machine (release downloads are blocked, so
\`slmcode update\` will not work — this is the supported path):
  cd ${ROOT} && git pull && ./scripts/install-offline.sh

Made with ♥ by UnicoLab — https://unicolab.ai
EOF
