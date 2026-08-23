#!/usr/bin/env bash
# SLMCode one-liner installer — downloads a prebuilt binary from GitHub Releases.
#
# macOS / Linux / WSL:
#   curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
#
# Pin a version:
#   curl -fsSL …/install-remote.sh | bash -s -- --version v0.5.17
#
# System-wide (may prompt for sudo):
#   curl -fsSL …/install-remote.sh | bash -s -- --system
#
# Uninstall:
#   curl -fsSL …/install-remote.sh | bash -s -- --uninstall
set -euo pipefail

REPO="${SLMCODE_REPO:-UnicoLab/smlcode}"
BIN_NAME="slmcode"
VERSION_SPEC="${SLMCODE_VERSION:-latest}"
MODE="user"
DO_UNINSTALL=0
PREFIX_OVERRIDE=""

usage() {
  cat <<'EOF'
SLMCode installer (prebuilt binary from GitHub Releases)

  curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash
  curl -fsSL …/install-remote.sh | bash -s -- --system
  curl -fsSL …/install-remote.sh | bash -s -- --version v0.5.17
  curl -fsSL …/install-remote.sh | bash -s -- --uninstall

Options:
  --system, -s       Install to Homebrew prefix or /usr/local/bin
  --user, -u         Install to ~/.local/bin (default)
  --prefix DIR       Custom prefix (binary → DIR/bin/slmcode)
  --version VER      Tag or semver (v0.5.17 / 0.5.17 / latest)
  --uninstall        Remove the installed binary
  -h, --help         Show help

Env:
  SLMCODE_VERSION    Same as --version
  SLMCODE_REPO       GitHub owner/repo (default: UnicoLab/smlcode)
EOF
}

detect_system_prefix() {
  if [[ -n "${PREFIX_OVERRIDE}" ]]; then
    echo "${PREFIX_OVERRIDE}"
    return
  fi
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

while [[ $# -gt 0 ]]; do
  case "$1" in
    --system|-s) MODE="system"; shift ;;
    --user|-u) MODE="user"; shift ;;
    --prefix) PREFIX_OVERRIDE="${2:-}"; shift 2 ;;
    --version|-v) VERSION_SPEC="${2:-}"; shift 2 ;;
    --uninstall) DO_UNINSTALL=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

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

need_sudo() {
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
  exit 0
fi

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "error: unsupported architecture: ${ARCH}" >&2
    exit 1
    ;;
esac
case "${OS}" in
  linux|darwin) ;;
  msys*|mingw*|cygwin*)
    echo "error: use PowerShell on Windows: irm https://raw.githubusercontent.com/${REPO}/main/scripts/install.ps1 | iex" >&2
    exit 1
    ;;
  *)
    echo "error: unsupported OS: ${OS}" >&2
    exit 1
    ;;
esac

if ! command -v curl >/dev/null 2>&1; then
  echo "error: curl is required" >&2
  exit 1
fi

normalize_tag() {
  local v="$1"
  if [[ "${v}" == "latest" ]]; then
    echo "latest"
    return
  fi
  v="${v#v}"
  echo "v${v}"
}

TAG="$(normalize_tag "${VERSION_SPEC}")"
API="https://api.github.com/repos/${REPO}/releases/latest"
if [[ "${TAG}" != "latest" ]]; then
  API="https://api.github.com/repos/${REPO}/releases/tags/${TAG}"
fi

echo "→ Resolving release (${TAG}) from ${REPO}…"
RELEASE_JSON="$(curl -fsSL -H "Accept: application/vnd.github+json" "${API}")"
TAG_NAME="$(printf '%s' "${RELEASE_JSON}" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
if [[ -z "${TAG_NAME}" ]]; then
  echo "error: could not resolve release tag (check ${API})" >&2
  exit 1
fi
VERSION="${TAG_NAME#v}"
ASSET="slmcode_${VERSION}_${OS}_${ARCH}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG_NAME}/${ASSET}"
SUMS_URL="https://github.com/${REPO}/releases/download/${TAG_NAME}/SHA256SUMS"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT
BIN_PATH="${TMPDIR}/${BIN_NAME}"

echo "→ Downloading ${ASSET}…"
curl -fsSL -o "${BIN_PATH}" "${DOWNLOAD_URL}"
chmod +x "${BIN_PATH}"

if curl -fsSL -o "${TMPDIR}/SHA256SUMS" "${SUMS_URL}" 2>/dev/null; then
  EXPECTED="$(awk -v f="${ASSET}" '$2 == f { print $1; exit }' "${TMPDIR}/SHA256SUMS")"
  if [[ -n "${EXPECTED}" ]]; then
    if command -v shasum >/dev/null 2>&1; then
      GOT="$(shasum -a 256 "${BIN_PATH}" | awk '{print $1}')"
    elif command -v sha256sum >/dev/null 2>&1; then
      GOT="$(sha256sum "${BIN_PATH}" | awk '{print $1}')"
    else
      GOT=""
    fi
    if [[ -n "${GOT}" && "${GOT}" != "${EXPECTED}" ]]; then
      echo "error: checksum mismatch for ${ASSET}" >&2
      echo "  expected ${EXPECTED}" >&2
      echo "  got      ${GOT}" >&2
      exit 1
    fi
    echo "✔ Checksum OK"
  else
    echo "⚠ could not verify checksum: ${ASSET} not listed in SHA256SUMS" >&2
  fi
else
  echo "⚠ could not verify checksum: failed to download SHA256SUMS from ${SUMS_URL}" >&2
fi

echo "→ Installing to ${TARGET}"
run_as_needed mkdir -p "${BIN_DIR}"
run_as_needed cp "${BIN_PATH}" "${TARGET}.new"
run_as_needed mv -f "${TARGET}.new" "${TARGET}"
run_as_needed chmod 755 "${TARGET}"

USER_BIN="${HOME}/.local/bin/${BIN_NAME}"
if [[ "${MODE}" == "system" && "${TARGET}" != "${USER_BIN}" ]]; then
  mkdir -p "${HOME}/.local/bin"
  rm -f "${USER_BIN}"
  ln -sf "${TARGET}" "${USER_BIN}"
  echo "→ Linked ${USER_BIN} → ${TARGET}"
fi

META_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/slmcode"
mkdir -p "${META_DIR}"
cat > "${META_DIR}/install.json" <<EOF
{
  "source": "",
  "prefix": "${PREFIX}",
  "mode": "${MODE}",
  "method": "binary",
  "version": "${VERSION}",
  "git_commit": "",
  "binary": "${TARGET}",
  "repo": "${REPO}",
  "installed_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF

hash -r 2>/dev/null || true

case ":${PATH}:" in
  *":${BIN_DIR}:"*) ;;
  *)
    echo
    echo "⚠  ${BIN_DIR} is not on your PATH. Add:"
    echo "  export PATH=\"${BIN_DIR}:\$PATH\""
    ;;
esac

echo
echo "✔ SLMCode ${VERSION} installed → ${TARGET}"
if command -v "${BIN_NAME}" >/dev/null 2>&1; then
  "${BIN_NAME}" version || true
fi
echo
echo "Next:"
echo "  slmcode doctor"
echo "  cd your-project && slmcode init && slmcode"
echo
echo "Update later:"
echo "  slmcode update"
echo "  # or re-run this installer"
echo
echo "Made with ♥ by UnicoLab — https://unicolab.ai"
