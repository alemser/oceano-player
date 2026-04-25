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

DEFAULT_BRANCH="main"
DEFAULT_DEVICE_MATCH="USB Microphone"
DEFAULT_ALSA_DEVICE=""
DEFAULT_SILENCE_THRESHOLD="0.025"
DEFAULT_DEBOUNCE="10"
DEFAULT_VU_SOCKET="/tmp/oceano-vu.sock"
DEFAULT_PCM_SOCKET="/tmp/oceano-pcm.sock"
DEFAULT_CAPTURE_GAIN=""

# Newline character used when building multi-line ExecStart strings.
NL=$'\n'

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

# set_capture_gain finds the capture card by matching device_match against
# /proc/asound/cards, sets the mic gain with amixer, and stores the new ALSA
# state so it survives reboots. A no-op when gain is empty.
set_capture_gain() {
  local gain="$1"
  local match="$2"

  [[ -z "${gain}" ]] && return 0

  local card_num
  card_num=$(grep -i "${match}" /proc/asound/cards 2>/dev/null | head -1 | awk '{print $1}')
  if [[ -z "${card_num}" ]]; then
    log_warn "Capture card matching '${match}' not found — --capture-gain ignored"
    return 0
  fi

  # Try 'Mic' first (most USB capture cards), fall back to 'Capture'.
  if amixer -c "${card_num}" sset 'Mic' "${gain}%" >/dev/null 2>&1; then
    alsactl store
    log_ok "Capture gain set to ${gain}% on card ${card_num} (alsactl store saved)"
  elif amixer -c "${card_num}" sset 'Capture' "${gain}%" >/dev/null 2>&1; then
    alsactl store
    log_ok "Capture gain (Capture control) set to ${gain}% on card ${card_num} (alsactl store saved)"
  else
    log_warn "Could not set capture gain on card ${card_num} — check control name with: amixer -c ${card_num}"
  fi
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
  local device_match="$1"
  local alsa_device="$2"
  local silence_threshold="$3"
  local debounce="$4"
  local vu_socket="$5"
  local pcm_socket="$6"

  # Build ExecStart programmatically to avoid heredoc line-continuation pitfalls.
  local exec_start="${BINARY_DEST}"
  exec_start+=" \\${NL}  --device-match \"${device_match}\""
  [[ -n "${alsa_device}" ]] && exec_start+=" \\${NL}  --device \"${alsa_device}\""
  exec_start+=" \\${NL}  --output \"${OUTPUT_FILE}\""
  exec_start+=" \\${NL}  --silence-threshold \"${silence_threshold}\""
  exec_start+=" \\${NL}  --debounce \"${debounce}\""
  exec_start+=" \\${NL}  --vu-socket \"${vu_socket}\""
  exec_start+=" \\${NL}  --pcm-socket \"${pcm_socket}\""

  cat > "${SERVICE_DEST}" <<EOF
[Unit]
Description=Oceano Source Detector (Physical media / None)
After=sound.target
Wants=sound.target

[Service]
Type=simple
ExecStart=${exec_start}
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

  log_ok "Service file written to ${SERVICE_DEST}"
}

main() {
  if ! is_root; then
    log_error "Please run as root: sudo ./install-source-detector.sh"
    exit 1
  fi

  require_cmd systemctl
  require_cmd git
  require_cmd arecord
  require_cmd python3

  local branch="${DEFAULT_BRANCH}"
  local device_match="${DEFAULT_DEVICE_MATCH}"
  local alsa_device="${DEFAULT_ALSA_DEVICE}"
  local silence_threshold="${DEFAULT_SILENCE_THRESHOLD}"
  local debounce="${DEFAULT_DEBOUNCE}"
  local vu_socket="${DEFAULT_VU_SOCKET}"
  local pcm_socket="${DEFAULT_PCM_SOCKET}"
  local capture_gain="${DEFAULT_CAPTURE_GAIN}"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --branch)            branch="${2:-}";            shift 2 ;;
      --device-match)      device_match="${2:-}";      shift 2 ;;
      --device)            alsa_device="${2:-}";       shift 2 ;;
      --silence-threshold) silence_threshold="${2:-}"; shift 2 ;;
      --debounce)          debounce="${2:-}";          shift 2 ;;
      --vu-socket)         vu_socket="${2:-}";         shift 2 ;;
      --pcm-socket)        pcm_socket="${2:-}";        shift 2 ;;
      --capture-gain)      capture_gain="${2:-}";      shift 2 ;;
      -h|--help)
        echo "Usage: sudo ./install-source-detector.sh [options]"
        echo ""
        echo "Options:"
        echo "  --branch <name>             Git branch to build (default: ${DEFAULT_BRANCH})"
        echo "  --device-match <str>        Substring to match in /proc/asound/cards (default: '${DEFAULT_DEVICE_MATCH}')"
        echo "  --device <hw>               Explicit ALSA fallback device (optional)"
        echo "  --silence-threshold <f>     RMS below this = no physical source (default: ${DEFAULT_SILENCE_THRESHOLD})"
        echo "  --debounce <n>              Majority vote window size (default: ${DEFAULT_DEBOUNCE})"
        echo "  --vu-socket <path>          Unix socket for VU meter frames (default: ${DEFAULT_VU_SOCKET})"
        echo "  --pcm-socket <path>         Unix socket for raw PCM relay (default: ${DEFAULT_PCM_SOCKET})"
        echo "  --capture-gain <0-100>      Set mic capture gain % and persist with alsactl store (optional)"
        exit 0
        ;;
      *) log_error "Unknown argument: $1"; exit 1 ;;
    esac
  done

  # If config.json exists and CLI args didn't override, read values from it.
  local config_file="/etc/oceano/config.json"
  if [[ -f "${config_file}" ]] && command -v python3 >/dev/null 2>&1; then
    _cfg() { python3 -c "import json,sys; c=json.load(open('${config_file}')); print(c$1)" 2>/dev/null || true; }
    _dev="$(_cfg "['audio_input']['device']")";       [[ -n "${_dev}" ]]  && alsa_device="${_dev}"
    _dm="$(_cfg "['audio_input']['device_match']")";  [[ -n "${_dm}" ]]   && device_match="${_dm}"
    _st="$(_cfg "['audio_input']['silence_threshold']")"; [[ -n "${_st}" ]] && silence_threshold="${_st}"
    _db="$(_cfg "['audio_input']['debounce_windows']")";  [[ -n "${_db}" ]] && debounce="${_db}"
    _vu="$(_cfg "['advanced']['vu_socket']")";        [[ -n "${_vu}" ]]   && vu_socket="${_vu}"
    _pcm="$(_cfg "['advanced']['pcm_socket']")";      [[ -n "${_pcm}" ]]  && pcm_socket="${_pcm}"
    _cg="$(_cfg "['audio_input']['capture_gain']")";  [[ -n "${_cg}" ]]   && capture_gain="${_cg}"
    log_info "Configuration loaded from ${config_file}"
  fi

  local mode
  mode=$(is_installed && echo "UPDATE" || echo "INSTALL")

  echo -e "\n${BOLD}╔══════════════════════════════════════╗"
  echo -e "║   Oceano Source Detector — ${mode}    ║"
  echo -e "╚══════════════════════════════════════╝${RESET}"

  log_section "Repository"
  if [[ ! -d "${SRC_DIR}/.git" ]]; then
    log_error "Repo not found at ${SRC_DIR}. Run main install.sh first."
    exit 1
  fi
  git -C "${SRC_DIR}" fetch origin
  git -C "${SRC_DIR}" reset --hard "origin/${branch}"
  log_ok "Repository synced to branch ${branch}."

  log_section "Build"
  build_binary

  log_section "systemd Service"
  write_service "${device_match}" "${alsa_device}" "${silence_threshold}" "${debounce}" "${vu_socket}" "${pcm_socket}"
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}"
  systemctl restart "${SERVICE_NAME}"
  log_ok "${SERVICE_NAME} is now running."

  if [[ -n "${capture_gain}" ]]; then
    log_section "Capture Gain"
    set_capture_gain "${capture_gain}" "${device_match}"
  fi

  log_section "Done"
  log_ok "${mode} completed successfully!"
  echo -e "Use ${BOLD}journalctl -u ${SERVICE_NAME} -f${RESET} to monitor logs."
}

main "$@"
