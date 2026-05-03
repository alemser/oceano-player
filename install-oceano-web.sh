#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────
#  Oceano Web — Install / Update Script
#  Builds cmd/oceano-web from source and installs as a systemd service.
# ─────────────────────────────────────────────

INSTALL_DIR="/opt/oceano-player"
SRC_DIR="/opt/oceano-player/src"
BINARY_SRC="cmd/oceano-web"
BINARY_NAME="oceano-web"
BINARY_DEST="/usr/local/bin/${BINARY_NAME}"
SERVICE_NAME="oceano-web.service"
SERVICE_DEST="/etc/systemd/system/${SERVICE_NAME}"
CONFIG_DIR="/etc/oceano"
BRIDGE_SRC="scripts/broadlink_bridge.py"
BRIDGE_DEST="/usr/local/lib/oceano/broadlink_bridge.py"
VENV_DIR="/opt/oceano-venv"
VENV_PYTHON="${VENV_DIR}/bin/python3"

DEFAULT_BRANCH="main"
DEFAULT_ADDR=":8080"
DEFAULT_CONFIG="${CONFIG_DIR}/config.json"
DEFAULT_LIBRARY_DB="/var/lib/oceano/library.db"

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

build_binary() {
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

write_service() {
  local addr="$1"
  local config="$2"
  local library_db="$3"

  cat > "${SERVICE_DEST}" <<EOF
[Unit]
Description=Oceano Web — HTTP API, SSE, and Now Playing static UI
After=network.target oceano-state-manager.service

[Service]
Type=simple
ExecStart=${BINARY_DEST} \\
  --addr "${addr}" \\
  --config "${config}" \\
  --library-db "${library_db}"
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

  log_ok "Service file written to ${SERVICE_DEST}"
}

main() {
  if ! is_root; then
    log_error "Please run as root: sudo ./install-oceano-web.sh"
    exit 1
  fi

  require_cmd systemctl
  require_cmd git

  local branch="${DEFAULT_BRANCH}"
  local addr="${DEFAULT_ADDR}"
  local config="${DEFAULT_CONFIG}"
  local library_db="${DEFAULT_LIBRARY_DB}"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --branch)      branch="${2:-}";      shift 2 ;;
      --addr)        addr="${2:-}";        shift 2 ;;
      --config)      config="${2:-}";      shift 2 ;;
      --library-db)  library_db="${2:-}";  shift 2 ;;
      -h|--help)
        echo "Usage: sudo ./install-oceano-web.sh [options]"
        echo ""
        echo "Options:"
        echo "  --branch <name>       Git branch to build (default: ${DEFAULT_BRANCH})"
        echo "  --addr <host:port>    Listen address (default: ${DEFAULT_ADDR})"
        echo "  --config <path>       Path to config file (default: ${DEFAULT_CONFIG})"
        echo "  --library-db <path>   Path to collection database (default: ${DEFAULT_LIBRARY_DB})"
        exit 0
        ;;
      *) log_error "Unknown argument: $1"; exit 1 ;;
    esac
  done

  local mode
  mode=$(is_installed && echo "UPDATE" || echo "INSTALL")

  echo -e "\n${BOLD}╔══════════════════════════════════════╗"
  echo -e "║     Oceano Web — ${mode}           ║"
  echo -e "╚══════════════════════════════════════╝${RESET}"

  log_section "Repository"
  if [[ ! -d "${SRC_DIR}/.git" ]]; then
    log_error "Repo not found at ${SRC_DIR}. Run main install.sh first."
    exit 1
  fi
  git -C "${SRC_DIR}" fetch origin
  git -C "${SRC_DIR}" reset --hard "origin/${branch}"
  log_ok "Repository synced to branch ${branch}."

  log_section "Config directory"
  mkdir -p "${CONFIG_DIR}"
  log_ok "Config directory ready at ${CONFIG_DIR}"

  log_section "Sudoers for ALSA mixer save"
  # Allow "alsactl store" with or without a card-number argument.
  # The mic-gain API calls "alsactl store <N>" to persist per-card state.
  printf 'oceano-web ALL=(root) NOPASSWD: /usr/sbin/alsactl store, /usr/sbin/alsactl store *\n' > /etc/sudoers.d/oceano-alsactl
  chmod 0440 /etc/sudoers.d/oceano-alsactl
  log_ok "Sudoers configured for mic gain persistence"

  log_section "ALSA state restore on boot"
  if systemctl enable alsa-restore.service 2>/dev/null; then
    log_ok "alsa-restore.service enabled — mic gain will survive reboots"
  else
    log_warn "alsa-restore.service not found — install alsa-utils if gain resets after reboot"
  fi

  log_section "Build"
  build_binary

  log_section "Broadlink bridge"
  mkdir -p "$(dirname "${BRIDGE_DEST}")"
  cp "${SRC_DIR}/${BRIDGE_SRC}" "${BRIDGE_DEST}"
  chmod 0755 "${BRIDGE_DEST}"
  log_ok "Bridge script installed at ${BRIDGE_DEST}"
  if ! command -v python3 >/dev/null 2>&1; then
    log_warn "python3 not found — skipping python-broadlink install"
    log_warn "Install Python 3 first: apt install python3 python3-venv"
  else
    if [[ ! -d "${VENV_DIR}" ]]; then
      log_info "Creating Python virtualenv at ${VENV_DIR}..."
      python3 -m venv "${VENV_DIR}"
    fi
    if "${VENV_PYTHON}" -c "import broadlink" 2>/dev/null; then
      log_ok "python-broadlink already installed in virtualenv"
    else
      log_info "Installing python-broadlink into ${VENV_DIR}..."
      "${VENV_DIR}/bin/pip" install --quiet broadlink
      log_ok "python-broadlink installed"
    fi
  fi

  log_section "systemd Service"
  write_service "${addr}" "${config}" "${library_db}"
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}"
  systemctl restart "${SERVICE_NAME}"
  log_ok "${SERVICE_NAME} is now running."

  log_section "Done"
  log_ok "${mode} completed successfully!"
  # Determine the Pi's primary IP for convenience
  local ip
  ip=$(hostname -I 2>/dev/null | awk '{print $1}') || ip="<pi-ip>"
  local port="${addr##*:}"
  echo -e "API base: ${BOLD}http://${ip}:${port}${RESET} — use iOS or POST /api/config for settings; ${BOLD}http://${ip}:${port}/nowplaying.html${RESET} for the local display."
  echo -e "Use ${BOLD}journalctl -u ${SERVICE_NAME} -f${RESET} to monitor logs."
}

main "$@"
