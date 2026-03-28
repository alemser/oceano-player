#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────
#  Oceano Source Detector — Install / Update Script
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

# Calibrated defaults for your hardware (DIGITNOW on Card 2)
DEFAULT_BRANCH="main"
DEFAULT_ALSA_DEVICE="plughw:2,0"
DEFAULT_SILENCE_THRESHOLD="0.008"
DEFAULT_BASS_VINYL_THRESHOLD="0.0012"
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
    exit 1
  fi

  log_info "Building ${BINARY_NAME} from ${build_dir}..."

  local go_bin
  if command -v go >/dev/null 2>&1; then
    go_bin="go"
  elif [[ -x "/usr/local/go/bin/go" ]]; then
    go_bin="/usr/local/go/bin/go"
  else
    log_error "Go not found. Please install Go (1.21+) first."
    exit 1
  fi

  GOFLAGS="" "${go_bin}" build -C "${SRC_DIR}" -o "${BINARY_DEST}" "./${BINARY_SRC}"
  chmod 0755 "${BINARY_DEST}"
  log_ok "Binary installed at ${BINARY_DEST}"
}

# ─── Service ─────────────────────────────────

write_service() {
  local alsa_device="$1"
  local silence_threshold="$2"
  local bass_vinyl_threshold="$3"
  local debounce="$4"

  cat > "${SERVICE_DEST}" <<EOF
[Unit]
Description=Oceano Source Detector (Vinyl / CD / None)
After=sound.target
Wants=sound.target

[Service]
Type=simple
ExecStart=${BINARY_DEST} \\
  --device "${alsa_device}" \\
  --output "${OUTPUT_FILE}" \\
  --silence-threshold "${silence_threshold}" \\
  --bass-vinyl-threshold "${bass_vinyl_threshold}" \\
  --debounce "${debounce}" \\
  --verbose
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

  # Parse arguments
  local branch="${DEFAULT_BRANCH}"
  local alsa_device="${DEFAULT_ALSA_DEVICE}"
  local silence_threshold="${DEFAULT_SILENCE_THRESHOLD}"
  local bass_vinyl_threshold="${DEFAULT_BASS_VINYL_THRESHOLD}"
  local debounce="${DEFAULT_DEBOUNCE}"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --branch)               branch="${2:-}";             shift 2 ;;
      --device)               alsa_device="${2:-}";        shift 2 ;;
      --silence-threshold)    silence_threshold="${2:-}";  shift 2 ;;
      --bass-vinyl-threshold) bass_vinyl_threshold="${2:-}"; shift 2 ;;
      --debounce)             debounce="${2:-}";           shift 2 ;;
      -h|--help)
        echo "Usage: sudo ./install-source-detector.sh [options]"
        echo ""
        echo "Options:"
        echo "  --branch <name>            Git branch to build (default: ${DEFAULT_BRANCH})"
        echo "  --device <hw>              ALSA device (default: ${DEFAULT_ALSA_DEVICE})"
        echo "  --silence-threshold <f>    RMS threshold for silence (default: ${DEFAULT_SILENCE_THRESHOLD})"
        echo "  --bass-vinyl-threshold <f> Bass threshold for Vinyl (default: ${DEFAULT_BASS_VINYL_THRESHOLD})"
        echo "  --debounce <n>             Consecutive windows (default: ${DEFAULT_DEBOUNCE})"
        exit 0
        ;;
      *) log_error "Unknown argument: $1"; exit 1 ;;
    esac
  done

  local mode
  mode=$(is_installed && echo "UPDATE" || echo "INSTALL")

  echo -e "\n${BOLD}╔══════════════════════════════════════╗"
  echo -e "║   Oceano Source Detector — ${mode}    ║"
  echo -e "╚══════════════════════════════════════╝${RESET}"

  # Repository Sync
  log_section "Repository"
  if [[ ! -d "${SRC_DIR}/.git" ]]; then
    log_error "Repo not found at ${SRC_DIR}. Run main install.sh first."
    exit 1
  fi

  git -C "${SRC_DIR}" fetch origin
  git -C "${SRC_DIR}" reset --hard "origin/${branch}"
  log_ok "Repository synced to branch ${branch}."

  # Build
  log_section "Build"
  build_binary "${branch}"

  # Service Configuration
  log_section "systemd Service"
  write_service "${alsa_device}" "${silence_threshold}" "${bass_vinyl_threshold}" "${debounce}"
  
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}"
  systemctl restart "${SERVICE_NAME}"
  log_ok "${SERVICE_NAME} is now running."

  # Summary
  log_section "Done"
  log_ok "${mode} completed successfully!"
  echo -e "Use ${BOLD}journalctl -u ${SERVICE_NAME} -f${RESET} to monitor logs."
}

main "$@"