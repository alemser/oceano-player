#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────
#  Oceano Source Detector — Install / Update Script
#  Supports: install (first run) and update (subsequent runs)
#  Builds cmd/oceano-source-detector from source and installs as a systemd service.
# ─────────────────────────────────────────────

INSTALL_DIR="/opt/oceano-player"
SRC_DIR="/opt/oceano-player/src"
BINARY_SRC="cmd/oceano-source-detector"
BINARY_NAME="oceano-source-detector"
BINARY_DEST="/usr/local/bin/${BINARY_NAME}"
SERVICE_NAME="oceano-source-detector.service"
SERVICE_DEST="/etc/systemd/system/${SERVICE_NAME}"
OUTPUT_FILE="/tmp/oceano-source.json"

DEFAULT_BRANCH="main"
DEFAULT_ALSA_DEVICE="plughw:CARD=Microphone,DEV=0"
DEFAULT_SILENCE_THRESHOLD="0.0005"
DEFAULT_VINYL_THRESHOLD="0.15"
DEFAULT_DEBOUNCE="5"

# ─── Output colors ───────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
RESET='\033[0m'

log_info()    { echo -e "${CYAN}[INFO]${RESET}  $*"; }
log_ok()      { echo -e "${GREEN}[OK]${RESET}    $*"; }
log_warn()    { echo -e "${YELLOW}[WARN]${RESET}  $*"; }
log_error()   { echo -e "${RED}[ERROR]${RESET} $*" >&2; }
log_section() { echo -e "\n${BOLD}━━━ $* ━━━${RESET}"; }

# ─── Helpers ─────────────────────────────────

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    log_error "Required command not found: $1"
    exit 1
  }
}

is_root() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]]
}

is_installed() {
  [[ -f "${BINARY_DEST}" && -f "${SERVICE_DEST}" ]]
}

get_installed_version() {
  if "${BINARY_DEST}" --version 2>/dev/null; then
    return
  fi
  # Fall back to git hash of the installed source
  if [[ -d "${SRC_DIR}/.git" ]]; then
    git -C "${SRC_DIR}" rev-parse --short HEAD 2>/dev/null || echo "(unknown)"
  else
    echo "(unknown)"
  fi
}

# ─── Build ───────────────────────────────────

build_binary() {
  local branch="$1"
  local build_dir="${SRC_DIR}/${BINARY_SRC}"

  if [[ ! -d "${build_dir}" ]]; then
    log_error "Source not found at ${build_dir}"
    log_error "Make sure the repo is cloned and the branch '${branch}' has cmd/oceano-source-detector/main.go"
    exit 1
  fi

  log_info "Building ${BINARY_NAME} from ${build_dir}..."

  # Detect Go binary
  local go_bin
  if command -v go >/dev/null 2>&1; then
    go_bin="go"
  elif [[ -x "/usr/local/go/bin/go" ]]; then
    go_bin="/usr/local/go/bin/go"
  else
    log_error "Go not found. Install it with:"
    log_error "  curl -fsSL https://go.dev/dl/go1.22.linux-arm64.tar.gz | sudo tar -C /usr/local -xz"
    exit 1
  fi

  local go_version
  go_version="$("${go_bin}" version)"
  log_info "Using ${go_version}"

  GOFLAGS="" "${go_bin}" build -o "${BINARY_DEST}" "./${BINARY_SRC}"
  chmod 0755 "${BINARY_DEST}"
  log_ok "Binary installed at ${BINARY_DEST}"
}

# ─── Service ─────────────────────────────────

write_service() {
  local alsa_device="$1"
  local silence_threshold="$2"
  local vinyl_threshold="$3"
  local debounce="$4"

  cat > "${SERVICE_DEST}" <<EOF
[Unit]
Description=Oceano Source Detector (Vinyl / CD / None)
After=sound.target
Wants=sound.target

[Service]
Type=simple
ExecStart=${BINARY_DEST} \\
  --device ${alsa_device} \\
  --output ${OUTPUT_FILE} \\
  --silence-threshold ${silence_threshold} \\
  --vinyl-threshold ${vinyl_threshold} \\
  --debounce ${debounce}
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

  log_ok "Service file written to ${SERVICE_DEST}"
}

# ─── Main ────────────────────────────────────

main() {
  if ! is_root; then
    log_error "Please run as root: sudo ./install-source-detector.sh"
    exit 1
  fi

  require_cmd systemctl
  require_cmd git
  require_cmd arecord

  # ── Parse arguments ──
  local branch="${DEFAULT_BRANCH}"
  local alsa_device="${DEFAULT_ALSA_DEVICE}"
  local silence_threshold="${DEFAULT_SILENCE_THRESHOLD}"
  local vinyl_threshold="${DEFAULT_VINYL_THRESHOLD}"
  local debounce="${DEFAULT_DEBOUNCE}"
  local alsa_device_set=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --branch)             branch="${2:-}";             shift 2 ;;
      --device)             alsa_device="${2:-}";        alsa_device_set=1; shift 2 ;;
      --silence-threshold)  silence_threshold="${2:-}";  shift 2 ;;
      --vinyl-threshold)    vinyl_threshold="${2:-}";    shift 2 ;;
      --debounce)           debounce="${2:-}";           shift 2 ;;
      -h|--help)
        echo "Usage: sudo ./install-source-detector.sh [options]"
        echo ""
        echo "Options:"
        echo "  --branch <n>               Git branch to build from (default: '${DEFAULT_BRANCH}')"
        echo "  --device <plughw:...>      ALSA capture device (default: '${DEFAULT_ALSA_DEVICE}')"
        echo "  --silence-threshold <f>    RMS threshold for silence (default: ${DEFAULT_SILENCE_THRESHOLD})"
        echo "  --vinyl-threshold <f>      Low-freq energy ratio for vinyl (default: ${DEFAULT_VINYL_THRESHOLD})"
        echo "  --debounce <n>             Consecutive windows before committing state (default: ${DEFAULT_DEBOUNCE})"
        exit 0
        ;;
      *) log_error "Unknown argument: $1"; exit 1 ;;
    esac
  done

  # ── Detect mode ──
  local mode
  if is_installed; then
    mode="update"
  else
    mode="install"
  fi

  # ── Banner ──
  echo -e "\n${BOLD}╔══════════════════════════════════════╗"
  if [[ "${mode}" == "install" ]]; then
    echo -e "║   Oceano Source Detector — INSTALL    ║"
  else
    echo -e "║   Oceano Source Detector — UPDATE     ║"
  fi
  echo -e "╚══════════════════════════════════════╝${RESET}"

  if [[ "${branch}" != "${DEFAULT_BRANCH}" ]]; then
    echo -e "${YELLOW}${BOLD}  ⚠ Development branch: ${branch}${RESET}"
    echo -e "${YELLOW}  Do not use in production without testing!${RESET}"
  fi

  if [[ "${mode}" == "update" ]]; then
    log_info "Currently installed: $(get_installed_version)"
  fi

  # ── Ensure repo is present ──
  if [[ ! -d "${SRC_DIR}/.git" ]]; then
    log_error "Repo not found at ${SRC_DIR}."
    log_error "Run install.sh first to clone the repo, then re-run this script."
    exit 1
  fi

  # ── Sync repo to requested branch ──
  log_section "Repository"
  local before current_branch
  before="$(git -C "${SRC_DIR}" rev-parse HEAD 2>/dev/null || echo "")"
  current_branch="$(git -C "${SRC_DIR}" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")"

  if [[ "${current_branch}" != "${branch}" ]]; then
    log_warn "Current branch: '${current_branch}' → switching to '${branch}'..."
    git -C "${SRC_DIR}" fetch origin
    git -C "${SRC_DIR}" reset --hard
    git -C "${SRC_DIR}" clean -fd >/dev/null
    git -C "${SRC_DIR}" checkout "${branch}"
    git -C "${SRC_DIR}" reset --hard "origin/${branch}"
    log_ok "Switched to branch '${branch}'."
  else
    log_info "Syncing branch '${branch}'..."
    git -C "${SRC_DIR}" fetch origin
    git -C "${SRC_DIR}" reset --hard "origin/${branch}"
    git -C "${SRC_DIR}" clean -fd >/dev/null
    local after
    after="$(git -C "${SRC_DIR}" rev-parse HEAD 2>/dev/null || echo "")"
    if [[ "${before}" == "${after}" ]]; then
      log_info "Already up to date (${after:0:8})."
    else
      log_ok "Updated: ${before:0:8} → ${after:0:8}"
    fi
  fi

  # ── Build ──
  log_section "Build"
  build_binary "${branch}"

  # ── Service ──
  log_section "systemd Service"
  write_service "${alsa_device}" "${silence_threshold}" "${vinyl_threshold}" "${debounce}"
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}"
  systemctl restart "${SERVICE_NAME}"
  log_ok "${SERVICE_NAME} is running."

  # ── Summary ──
  log_section "Done"
  if [[ "${mode}" == "install" ]]; then
    log_ok "Installation completed successfully!"
  else
    log_ok "Update completed successfully!"
  fi

  echo -e "
${BOLD}Configuration summary:${RESET}
  Branch             : ${branch}
  ALSA device        : ${alsa_device}
  Silence threshold  : ${silence_threshold}
  Vinyl threshold    : ${vinyl_threshold}
  Debounce windows   : ${debounce}
  Output file        : ${OUTPUT_FILE}

${BOLD}Useful commands:${RESET}
  systemctl status ${SERVICE_NAME}
  journalctl -u ${SERVICE_NAME} -f
  cat ${OUTPUT_FILE}

${BOLD}Calibration tip:${RESET}
  Run with arm up (motor on, needle off the record) and check the logs.
  Adjust --silence-threshold and --vinyl-threshold until classification is stable.
  Then re-run this script with the tuned values to update the service.
"
}

main "$@"