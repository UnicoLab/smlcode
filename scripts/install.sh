#!/usr/bin/env bash
# Install / update SLMCode system-wide (Claude Code–style: one binary on PATH).
#
# Usage:
#   ./scripts/install.sh              # user: ~/.local/bin
#   ./scripts/install.sh --system     # Homebrew /usr/local (preferred)
#   ./scripts/install.sh --prefix /opt/slmcode
#   PREFIX=/usr/local ./scripts/install.sh
#   ./scripts/install.sh --uninstall [--system]
#
# After first install, from anywhere:
#   slmcode update
#   slmcode update --check
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MODE="user"
DO_UNINSTALL=0
DO_COMPLETION=1
PREFIX_OVERRIDE=""
BIN_NAME="slmcode"

usage() {
  cat <<'EOF'
Install / update SLMCode onto PATH (use from any project, like Claude Code).

  ./scripts/install.sh                 User install → ~/.local/bin/slmcode
  ./scripts/install.sh --system        System install → Homebrew or /usr/local/bin
  ./scripts/install.sh --prefix DIR    Custom prefix (binary → DIR/bin/slmcode)
  ./scripts/install.sh --uninstall [--system]

Day-to-day updates (after first install):
  slmcode update              # rebuild from recorded source + reinstall
  slmcode update --check      # compare installed vs source
  make update                 # from the checkout (same as install-system)

Options:
  --no-completion   Skip shell completion install
  -h, --help        Show this help
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
  if [[ -L "${USER_BIN}" ]]; then
    rm -f "${USER_BIN}"
    echo "Removed symlink ${USER_BIN}"
  fi
  exit 0
fi

if ! command -v go >/dev/null 2>&1; then
  echo "error: Go toolchain required (https://go.dev/dl/)" >&2
  echo "  Prefer the no-Go one-liner instead:" >&2
  echo "  curl -fsSL https://raw.githubusercontent.com/UnicoLab/smlcode/main/scripts/install-remote.sh | bash" >&2
  exit 1
fi

# Optional local GoLangGraph checkout for hacking (go.mod replace).
GG="${GOLANGGRAPH:-${ROOT}/../GoLangGraph-Project/GoLangGraph}"
if [[ -d "${GG}" ]]; then
  echo "→ Using local GoLangGraph at ${GG}"
  export GOFLAGS="${GOFLAGS:-} -mod=mod"
else
  GG=""
fi

VERSION="$(grep -E '^\s*Version\s*=' "${ROOT}/cmd/slmcode/version.go" | head -1 | sed -E 's/.*"([^"]+)".*/\1/' || echo "dev")"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GIT_COMMIT="$(git -C "${ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
# Escape for ldflags (paths may contain spaces rarely)
LDFLAGS="-s -w -X main.Version=${VERSION} -X main.SourceRoot=${ROOT} -X main.GitCommit=${GIT_COMMIT} -X main.BuildTime=${BUILD_TIME}"

echo "Building SLMCode ${VERSION} (${GIT_COMMIT})…"
cd "${ROOT}"
mkdir -p "${ROOT}/bin"
go build -ldflags "${LDFLAGS}" -o "${ROOT}/bin/${BIN_NAME}" ./cmd/slmcode

echo "Installing → ${TARGET}"
run_as_needed mkdir -p "${BIN_DIR}"
# Atomic replace so `slmcode update` can overwrite the running binary safely
run_as_needed cp "${ROOT}/bin/${BIN_NAME}" "${TARGET}.new"
run_as_needed mv -f "${TARGET}.new" "${TARGET}"
run_as_needed chmod 755 "${TARGET}"

# PATH often prefers ~/.local/bin over Homebrew. Point the user copy at the
# system binary so `which slmcode` resolves to one install (Claude Code–style).
USER_BIN="${HOME}/.local/bin/${BIN_NAME}"
if [[ "${MODE}" == "system" && "${TARGET}" != "${USER_BIN}" ]]; then
  mkdir -p "${HOME}/.local/bin"
  if [[ -e "${USER_BIN}" || -L "${USER_BIN}" ]]; then
    rm -f "${USER_BIN}"
  fi
  ln -sf "${TARGET}" "${USER_BIN}"
  echo "Linked ${USER_BIN} → ${TARGET}"
fi

hash -r 2>/dev/null || true

# Persist source location for `slmcode update` from any directory
META_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/slmcode"
mkdir -p "${META_DIR}"
cat > "${META_DIR}/install.json" <<EOF
{
  "source": "${ROOT}",
  "golanggraph": "${GG}",
  "prefix": "${PREFIX}",
  "mode": "${MODE}",
  "method": "source",
  "version": "${VERSION}",
  "git_commit": "${GIT_COMMIT}",
  "binary": "${TARGET}",
  "repo": "UnicoLab/smlcode",
  "installed_at": "${BUILD_TIME}"
}
EOF
echo "Install meta → ${META_DIR}/install.json"

install_completions() {
  [[ "${DO_COMPLETION}" -eq 1 ]] || return 0
  local shell_name comp_dir tmp
  shell_name="$(basename "${SHELL:-zsh}")"
  tmp="$(mktemp)"
  case "${shell_name}" in
    zsh)
      "${ROOT}/bin/${BIN_NAME}" completion zsh > "${tmp}"
      if [[ "${MODE}" == "system" ]] && [[ -d "${PREFIX}/share/zsh/site-functions" ]]; then
        comp_dir="${PREFIX}/share/zsh/site-functions"
        echo "Shell completion → ${comp_dir}/_${BIN_NAME}"
        run_as_needed cp "${tmp}" "${comp_dir}/_${BIN_NAME}"
      else
        comp_dir="${HOME}/.zsh/completions"
        mkdir -p "${comp_dir}"
        echo "Shell completion → ${comp_dir}/_${BIN_NAME}"
        cp "${tmp}" "${comp_dir}/_${BIN_NAME}"
        if ! grep -q '\.zsh/completions' "${HOME}/.zshrc" 2>/dev/null; then
          echo
          echo "Add to ~/.zshrc for tab completion:"
          echo "  fpath=(~/.zsh/completions \$fpath)"
          echo "  autoload -Uz compinit && compinit"
        fi
      fi
      ;;
    bash)
      "${ROOT}/bin/${BIN_NAME}" completion bash > "${tmp}"
      if [[ "${MODE}" == "system" ]] && [[ -d "${PREFIX}/etc/bash_completion.d" ]]; then
        echo "Shell completion → ${PREFIX}/etc/bash_completion.d/${BIN_NAME}"
        run_as_needed cp "${tmp}" "${PREFIX}/etc/bash_completion.d/${BIN_NAME}"
      else
        mkdir -p "${HOME}/.local/share/bash-completion/completions"
        cp "${tmp}" "${HOME}/.local/share/bash-completion/completions/${BIN_NAME}"
        echo "Shell completion → ~/.local/share/bash-completion/completions/${BIN_NAME}"
      fi
      ;;
    fish)
      local fish_dir="${HOME}/.config/fish/completions"
      mkdir -p "${fish_dir}"
      "${ROOT}/bin/${BIN_NAME}" completion fish > "${fish_dir}/${BIN_NAME}.fish"
      echo "Shell completion → ${fish_dir}/${BIN_NAME}.fish"
      ;;
  esac
  rm -f "${tmp}"
}

install_completions || echo "(completion install skipped — run: slmcode completion zsh)"

# PATH check
case ":${PATH}:" in
  *":${BIN_DIR}:"*) ;;
  *)
    echo
    echo "⚠  ${BIN_DIR} is not on your PATH. Add to ~/.zshrc:"
    echo "  export PATH=\"${BIN_DIR}:\$PATH\""
    ;;
esac

echo
echo "✔ Installed ${TARGET}"
if command -v "${BIN_NAME}" >/dev/null 2>&1; then
  echo "✔ On PATH as: $(command -v "${BIN_NAME}")"
  "${BIN_NAME}" version || true
else
  echo "✔ Binary ready (open a new shell if 'slmcode' is not found)"
fi
echo
echo "Update later from anywhere:"
echo "  slmcode update           # rebuild + reinstall"
echo "  slmcode update --check   # see if source changed"
echo "  make update              # from ${ROOT}"
echo
echo "Quick start (any project):"
echo "  omlx start                 # local LLM"
echo "  cd your-project"
echo "  slmcode init"
echo "  slmcode run -v \"your task\""
echo "  slmcode studio             # http://127.0.0.1:7420"
echo "  slmcode chat               # interactive REPL"
echo
if command -v omlx >/dev/null 2>&1; then
  echo "oMLX detected — ensure: omlx start"
else
  echo "Tip: point provider at any OpenAI-compatible LLM (see docs/PROVIDERS.md)."
fi
echo "Built ${BUILD_TIME}"
Made with ♥ by UnicoLab — https://unicolab.ai · AI & Innovation
