#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────
#  Oceano Source Detector — Install / Update Script
#  Builds from source and installs as a systemd service.
# ─────────────────────────────────────────────

INSTALL_DIR="/opt/oceano-player"
SRC_DIR="/opt/oceano-player/src"
BINARY_SRC="cmd/oceano-source-detector"
BINARY_NAME="oceano-source-detector"
BINARY_DEST="/usr/local/bin/${BINARY_NAME}"
SERVICE_NAME="oceano-source-detector.service"
SERVICE_DEST="/etc/systemd/system/${SERVICE_NAME}"
OUTPUT_FILE="/tmp/oceano-source.json"

# Valores padrão calibrados para o seu hardware (DIGITNOW no Card 2)
DEFAULT_BRANCH="main"
DEFAULT_ALSA_DEVICE="plughw:2,0"
DEFAULT_SILENCE_THRESHOLD="0.008"
DEFAULT_BASS_VINYL_THRESHOLD="0.0025"
DEFAULT_DEBOUNCE="5"

# ─── Cores para Output ───────────────────────────
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
    log_error "Comando necessário não encontrado: $1"
    exit 1
  }
}

is_root() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]]
}

is_installed() {
  [[ -f "${BINARY_DEST}" && -f "${SERVICE_DEST}" ]]
}

# ─── Build ───────────────────────────────────

build_binary() {
  local branch="$1"
  local build_dir="${SRC_DIR}/${BINARY_SRC}"

  if [[ ! -d "${build_dir}" ]]; then
    log_error "Código fonte não encontrado em ${build_dir}"
    exit 1
  fi

  log_info "Compilando ${BINARY_NAME}..."

  local go_bin
  if command -v go >/dev/null 2>&1; then
    go_bin="go"
  elif [[ -x "/usr/local/go/bin/go" ]]; then
    go_bin="/usr/local/go/bin/go"
  else
    log_error "Go não encontrado. Instale o Go primeiro."
    exit 1
  fi

  GOFLAGS="" "${go_bin}" build -C "${SRC_DIR}" -o "${BINARY_DEST}" "./${BINARY_SRC}"
  chmod 0755 "${BINARY_DEST}"
  log_ok "Binário instalado em ${BINARY_DEST}"
}

# ─── Service (systemd) ───────────────────────

write_service() {
  local alsa_device="$1"
  local silence_threshold="$2"
  local bass_vinyl_threshold="$3"
  local debounce="$4"

  log_info "Escrevendo arquivo de serviço com:"
  log_info "  - Bass Vinyl Threshold: ${bass_vinyl_threshold}"

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

  log_ok "Serviço escrito em ${SERVICE_DEST}"
}

# ─── Main ────────────────────────────────────

main() {
  if ! is_root; then
    log_error "Por favor, execute como root: sudo ./install-source-detector.sh"
    exit 1
  fi

  require_cmd systemctl
  require_cmd git
  require_cmd arecord

  local branch="${DEFAULT_BRANCH}"
  local alsa_device="${DEFAULT_ALSA_DEVICE}"
  local silence_threshold="${DEFAULT_SILENCE_THRESHOLD}"
  local bass_vinyl_threshold="${DEFAULT_BASS_VINYL_THRESHOLD}"
  local debounce="${DEFAULT_DEBOUNCE}"

  # Parse arguments
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --branch)               branch="${2:-}";             shift 2 ;;
      --device)               alsa_device="${2:-}";        shift 2 ;;
      --silence-threshold)    silence_threshold="${2:-}";  shift 2 ;;
      --bass-vinyl-threshold) bass_vinyl_threshold="${2:-}"; shift 2 ;;
      --debounce)             debounce="${2:-}";           shift 2 ;;
      -h|--help)
        echo "Uso: sudo ./install-source-detector.sh [opções]"
        echo ""
        echo "Opções:"
        echo "  --device <hw>             Dispositivo ALSA (Padrão: ${DEFAULT_ALSA_DEVICE})"
        echo "  --silence-threshold <f>   Threshold de silêncio (Padrão: ${DEFAULT_SILENCE_THRESHOLD})"
        echo "  --bass-vinyl-threshold <f> Threshold de graves para Vinil (Padrão: ${DEFAULT_BASS_VINYL_THRESHOLD})"
        exit 0
        ;;
      *) log_error "Argumento desconhecido: $1"; exit 1 ;;
    esac
  done

  mode=$(is_installed && echo "UPDATE" || echo "INSTALL")

  echo -e "\n${BOLD}╔══════════════════════════════════════╗"
  echo -e "║   Oceano Source Detector — ${mode}    ║"
  echo -e "╚══════════════════════════════════════╝${RESET}"

  # Sincronizar Repo
  log_section "Repositório"
  if [[ ! -d "${SRC_DIR}/.git" ]]; then
    log_error "Repo não encontrado em ${SRC_DIR}."
    exit 1
  fi
  
  git -C "${SRC_DIR}" fetch origin
  git -C "${SRC_DIR}" reset --hard "origin/${branch}"
  log_ok "Repositório atualizado no branch ${branch}."

  # Build
  log_section "Build"
  build_binary "${branch}"

  # Service
  log_section "Configuração do systemd"
  write_service "${alsa_device}" "${silence_threshold}" "${bass_vinyl_threshold}" "${debounce}"
  
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}"
  systemctl restart "${SERVICE_NAME}"
  log_ok "Serviço ${SERVICE_NAME} está a correr."

  log_section "Concluído"
  echo -e "Use ${BOLD}journalctl -u ${SERVICE_NAME} -f${RESET} para ver os logs."
}

main "$@"