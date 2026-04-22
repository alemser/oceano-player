#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────
#  Oceano State Manager — Install / Update Script
#  Builds cmd/oceano-state-manager from source and installs as a systemd service.
# ─────────────────────────────────────────────

INSTALL_DIR="/opt/oceano-player"
SRC_DIR="/opt/oceano-player/src"
BINARY_SRC="cmd/oceano-state-manager"
BINARY_NAME="oceano-state-manager"
BINARY_DEST="/usr/local/bin/${BINARY_NAME}"
SERVICE_NAME="oceano-state-manager.service"
SERVICE_DEST="/etc/systemd/system/${SERVICE_NAME}"

DEFAULT_BRANCH="main"
DEFAULT_METADATA_PIPE="/tmp/shairport-sync-metadata"
DEFAULT_SOURCE_FILE="/tmp/oceano-source.json"
DEFAULT_OUTPUT_FILE="/tmp/oceano-state.json"
DEFAULT_ARTWORK_DIR="/var/lib/oceano/artwork"
DEFAULT_PCM_SOCKET="/tmp/oceano-pcm.sock"
DEFAULT_VU_SOCKET="/tmp/oceano-vu.sock"
DEFAULT_RECOGNIZER_CAPTURE_DURATION="10s"
DEFAULT_RECOGNIZER_MAX_INTERVAL="5m0s"
DEFAULT_IDLE_DELAY="3s"
DEFAULT_LIBRARY_DB="/var/lib/oceano/library.db"

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
  local metadata_pipe="$1"
  local source_file="$2"
  local output_file="$3"
  local artwork_dir="$4"
  local acrcloud_host="$5"
  local acrcloud_access_key="$6"
  local acrcloud_secret_key="$7"
  local pcm_socket="$8"
  local vu_socket="$9"
  local recognizer_capture_duration="${10}"
  local recognizer_max_interval="${11}"
  local idle_delay="${12}"
  local library_db="${13}"

  # Build ExecStart programmatically to avoid heredoc line-continuation pitfalls.
  local exec_start="${BINARY_DEST}"
  exec_start+=" \\${NL}  --metadata-pipe \"${metadata_pipe}\""
  exec_start+=" \\${NL}  --source-file \"${source_file}\""
  exec_start+=" \\${NL}  --output \"${output_file}\""
  exec_start+=" \\${NL}  --artwork-dir \"${artwork_dir}\""
  exec_start+=" \\${NL}  --pcm-socket \"${pcm_socket}\""
  exec_start+=" \\${NL}  --vu-socket \"${vu_socket}\""
  exec_start+=" \\${NL}  --recognizer-capture-duration \"${recognizer_capture_duration}\""
  exec_start+=" \\${NL}  --recognizer-max-interval \"${recognizer_max_interval}\""
  exec_start+=" \\${NL}  --idle-delay \"${idle_delay}\""
  exec_start+=" \\${NL}  --library-db \"${library_db}\""
  if [[ -n "${acrcloud_host}" ]]; then
    exec_start+=" \\${NL}  --acrcloud-host \"${acrcloud_host}\""
    exec_start+=" \\${NL}  --acrcloud-access-key \"${acrcloud_access_key}\""
    exec_start+=" \\${NL}  --acrcloud-secret-key \"${acrcloud_secret_key}\""
  fi


  cat > "${SERVICE_DEST}" <<EOF
[Unit]
Description=Oceano State Manager (unified playback state + ACRCloud recognition)
After=shairport-sync.service oceano-source-detector.service
Wants=shairport-sync.service

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
    log_error "Please run as root: sudo ./install-source-manager.sh"
    exit 1
  fi

  require_cmd systemctl
  require_cmd git
  require_cmd python3

  local branch="${DEFAULT_BRANCH}"
  local metadata_pipe="${DEFAULT_METADATA_PIPE}"
  local source_file="${DEFAULT_SOURCE_FILE}"
  local output_file="${DEFAULT_OUTPUT_FILE}"
  local artwork_dir="${DEFAULT_ARTWORK_DIR}"
  local acrcloud_host=""
  local acrcloud_access_key=""
  local acrcloud_secret_key=""
  local pcm_socket="${DEFAULT_PCM_SOCKET}"
  local vu_socket="${DEFAULT_VU_SOCKET}"
  local recognizer_capture_duration="${DEFAULT_RECOGNIZER_CAPTURE_DURATION}"
  local recognizer_max_interval="${DEFAULT_RECOGNIZER_MAX_INTERVAL}"
  local idle_delay="${DEFAULT_IDLE_DELAY}"
  local library_db="${DEFAULT_LIBRARY_DB}"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --branch)                       branch="${2:-}";                       shift 2 ;;
      --metadata-pipe)                metadata_pipe="${2:-}";                shift 2 ;;
      --source-file)                  source_file="${2:-}";                  shift 2 ;;
      --output)                       output_file="${2:-}";                  shift 2 ;;
      --artwork-dir)                  artwork_dir="${2:-}";                  shift 2 ;;
      --acrcloud-host)                acrcloud_host="${2:-}";                shift 2 ;;
      --acrcloud-access-key)          acrcloud_access_key="${2:-}";          shift 2 ;;
      --acrcloud-secret-key)          acrcloud_secret_key="${2:-}";          shift 2 ;;
      --pcm-socket)                   pcm_socket="${2:-}";                   shift 2 ;;
      --vu-socket)                    vu_socket="${2:-}";                    shift 2 ;;
      --recognizer-capture-duration)  recognizer_capture_duration="${2:-}";  shift 2 ;;
      --recognizer-max-interval)      recognizer_max_interval="${2:-}";      shift 2 ;;
      --idle-delay)                   idle_delay="${2:-}";                   shift 2 ;;
      --library-db)                   library_db="${2:-}";                   shift 2 ;;
      -h|--help)
        echo "Usage: sudo ./install-source-manager.sh [options]"
        echo ""
        echo "Options:"
        echo "  --branch <name>                        Git branch to build (default: ${DEFAULT_BRANCH})"
        echo "  --metadata-pipe <path>                 shairport-sync metadata FIFO"
        echo "  --source-file <path>                   oceano-source-detector output JSON"
        echo "  --output <path>                        output state JSON file"
        echo "  --artwork-dir <path>                   artwork cache directory"
        echo "  --acrcloud-host <host>                 ACRCloud API host"
        echo "  --acrcloud-access-key <key>            ACRCloud access key"
        echo "  --acrcloud-secret-key <secret>         ACRCloud secret key"
        echo "  --pcm-socket <path>                    PCM relay socket from source detector"
        echo "  --vu-socket <path>                     VU socket from source detector"
        echo "  --recognizer-capture-duration <dur>    capture duration per attempt (default: ${DEFAULT_RECOGNIZER_CAPTURE_DURATION})"
        echo "  --recognizer-max-interval <dur>        fallback re-recognition interval (default: ${DEFAULT_RECOGNIZER_MAX_INTERVAL})"
        echo "  --idle-delay <dur>                     time to keep showing last track after audio stops (default: ${DEFAULT_IDLE_DELAY})"
        echo "  --library-db <path>                    SQLite library database path (default: ${DEFAULT_LIBRARY_DB})"
        exit 0
        ;;
      *) log_error "Unknown argument: $1"; exit 1 ;;
    esac
  done

  # If config.json exists and CLI args didn't override, read values from it.
  local config_file="/etc/oceano/config.json"
  if [[ -f "${config_file}" ]] && command -v python3 >/dev/null 2>&1; then
    _cfg() { python3 -c "import json,sys; c=json.load(open('${config_file}')); print(c$1)" 2>/dev/null || true; }
    [[ -z "${acrcloud_host}" ]]        && acrcloud_host="$(_cfg "['recognition']['acrcloud_host']")"
    [[ -z "${acrcloud_access_key}" ]]  && acrcloud_access_key="$(_cfg "['recognition']['acrcloud_access_key']")"
    [[ -z "${acrcloud_secret_key}" ]]  && acrcloud_secret_key="$(_cfg "['recognition']['acrcloud_secret_key']")"
    _src="$(_cfg "['advanced']['source_file']")"; [[ -n "${_src}" ]] && source_file="${_src}"
    _out="$(_cfg "['advanced']['state_file']")";  [[ -n "${_out}" ]] && output_file="${_out}"
    _art="$(_cfg "['advanced']['artwork_dir']")"; [[ -n "${_art}" ]] && artwork_dir="${_art}"
    _vu="$(_cfg "['advanced']['vu_socket']")";   [[ -n "${_vu}" ]]  && vu_socket="${_vu}"
    _pcm="$(_cfg "['advanced']['pcm_socket']")"; [[ -n "${_pcm}" ]] && pcm_socket="${_pcm}"
    _meta="$(_cfg "['advanced']['metadata_pipe']")"; [[ -n "${_meta}" ]] && metadata_pipe="${_meta}"
    log_info "Configuration loaded from ${config_file}"
  fi

  local mode
  mode=$(is_installed && echo "UPDATE" || echo "INSTALL")

  echo -e "\n${BOLD}╔══════════════════════════════════════╗"
  echo -e "║   Oceano State Manager — ${mode}     ║"
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

  log_section "Library Database"
  local lib_dir
  lib_dir="$(dirname "${library_db}")"
  mkdir -p "${lib_dir}"
  chown "$(stat -c '%u:%g' /etc/oceano 2>/dev/null || echo "root:root")" "${lib_dir}" 2>/dev/null || true
  log_ok "Library directory ready at ${lib_dir}"

  log_section "Artwork Directory"
  mkdir -p "${artwork_dir}"
  chown "$(stat -c '%u:%g' /etc/oceano 2>/dev/null || echo "root:root")" "${artwork_dir}" 2>/dev/null || true
  log_ok "Artwork directory ready at ${artwork_dir}"


  log_section "systemd Service"
  write_service "${metadata_pipe}" "${source_file}" "${output_file}" "${artwork_dir}" \
    "${acrcloud_host}" "${acrcloud_access_key}" "${acrcloud_secret_key}" \
    "${pcm_socket}" "${vu_socket}" "${recognizer_capture_duration}" "${recognizer_max_interval}" \
    "${idle_delay}" "${library_db}"
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}"
  systemctl restart "${SERVICE_NAME}"
  log_ok "${SERVICE_NAME} is now running."

  log_section "Done"
  log_ok "${mode} completed successfully!"
  echo -e "Use ${BOLD}journalctl -u ${SERVICE_NAME} -f${RESET} to monitor logs."
}

main "$@"
