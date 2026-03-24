#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="/opt/oceano-player"
SRC_DIR="/opt/oceano-player/src"
CONFIG_FILE="/opt/oceano-player/config.env"
DEFAULT_REPO_URL="https://github.com/alemser/oceano-player.git"
DEFAULT_BRANCH="main"
DEFAULT_AIRPLAY_NAME="Triangle AirPlay"
DEFAULT_USB_MATCH="M780"
DEFAULT_PREPLAY_WAIT_SECONDS="8"
DEFAULT_OUTPUT_STRATEGY="loopback"
SHAIRPORT_CONF="/etc/shairport-sync.conf"
PREPLAY_WAIT_SCRIPT="/usr/local/bin/oceano-airplay-preplay-wait.sh"
BRIDGE_SCRIPT="/usr/local/bin/oceano-airplay-bridge.sh"
BRIDGE_SERVICE="/etc/systemd/system/oceano-airplay-bridge.service"
MODULES_LOAD_FILE="/etc/modules-load.d/oceano-player.conf"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

is_root() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]]
}

detect_alsa_device() {
  local match="$1"
  local ap_out card_id

  # Prefer stable ALSA card identifiers from `aplay -L`, e.g.:
  # plughw:CARD=M780,DEV=0
  ap_out="$(aplay -L 2>/dev/null)"
  card_id="$(
    awk -v m="$match" '
      BEGIN { IGNORECASE=1; dev="" }
      /^[^[:space:]].*/ { dev=$0; next }
      /^[[:space:]]+/ {
        if (dev ~ /^plughw:CARD=/ && index(tolower($0), tolower(m))) {
          sub(/^plughw:CARD=/, "", dev)
          sub(/,DEV=.*/, "", dev)
          print dev
          exit
        }
      }
    ' <<<"$ap_out"
  )"
  if [[ -n "$card_id" ]]; then
    echo "plughw:CARD=${card_id},DEV=0"
    return 0
  fi

  # Fallback to `aplay -l` numeric card/device index.
  local line card device
  line="$(aplay -l 2>/dev/null | awk -v m="$match" 'BEGIN{IGNORECASE=1} /card [0-9]+:.*device [0-9]+:/ && index(tolower($0), tolower(m)) {print; exit}')"
  if [[ -n "$line" ]]; then
    card="$(sed -E 's/.*card ([0-9]+):.*/\1/' <<<"$line")"
    device="$(sed -E 's/.*device ([0-9]+):.*/\1/' <<<"$line")"
    echo "plughw:${card},${device}"
    return 0
  fi
  return 1
}

write_shairport_config() {
  local airplay_name="$1"
  local alsa_device="$2"
  local preplay_wait_seconds="$3"
  local output_strategy="$4"
  local mixer_device="none"
  local shairport_output_device="${alsa_device}"

  if [[ "${output_strategy}" == "loopback" ]]; then
    # Use plughw for Loopback because some builds reject hw:* with
    # "Channels count not available" depending on negotiated stream params.
    shairport_output_device="plughw:CARD=Loopback,DEV=0"
    mixer_device="hw:CARD=Loopback"
  else
    # Some shairport-sync builds still probe an ALSA control device even when
    # mixer control is disabled. For plughw outputs, force a hw ctl path.
    if [[ "${alsa_device}" =~ ^plughw:CARD=([^,]+),DEV=([0-9]+)$ ]]; then
      mixer_device="hw:CARD=${BASH_REMATCH[1]}"
    elif [[ "${alsa_device}" =~ ^plughw:([0-9]+),([0-9]+)$ ]]; then
      mixer_device="hw:${BASH_REMATCH[1]}"
    fi
  fi

  if [[ -f "${SHAIRPORT_CONF}" && ! -f "${SHAIRPORT_CONF}.oceano.bak" ]]; then
    cp "${SHAIRPORT_CONF}" "${SHAIRPORT_CONF}.oceano.bak"
  fi

  cat > "${SHAIRPORT_CONF}" <<EOF
general =
{
  name = "${airplay_name}";
  interpolation = "soxr";
};

output =
{
  output_backend = "alsa";
};

alsa =
{
  output_device = "${shairport_output_device}";
  mixer_control_name = "none";
  mixer_device = "${mixer_device}";
};

metadata =
{
  enabled = "yes";
  include_cover_art = "yes";
  pipe_name = "/tmp/shairport-sync-metadata";
  pipe_timeout = 5000;
  cover_art_cache_directory = "/tmp/shairport-sync/.cache/coverart";
};

sessioncontrol =
{
  wait_for_completion = "yes";
  run_this_before_play_begins = "${PREPLAY_WAIT_SCRIPT} \"${shairport_output_device}\" ${preplay_wait_seconds}";
};
EOF
}

write_preplay_wait_script() {
  cat > "${PREPLAY_WAIT_SCRIPT}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

alsa_device="${1:-}"
wait_seconds="${2:-8}"

if [[ -z "${alsa_device}" ]]; then
  exit 0
fi

if ! [[ "${wait_seconds}" =~ ^[0-9]+$ ]]; then
  wait_seconds=8
fi

# Probe the output device with a very short silent raw stream.
# This can give USB DAC/amps in standby a chance to wake before shairport opens it.
attempt=0
while (( attempt < wait_seconds )); do
  if aplay -q -D "${alsa_device}" -t raw -f S16_LE -r 44100 -d 1 /dev/zero >/dev/null 2>&1; then
    exit 0
  fi
  sleep 1
  ((attempt += 1))
done

# Do not hard-fail the session hook: shairport will still attempt normal playback.
exit 0
EOF

  chmod 0755 "${PREPLAY_WAIT_SCRIPT}"
}

write_bridge_script() {
  cat > "${BRIDGE_SCRIPT}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

loopback_capture="${1:-hw:Loopback,1,0}"
playback_device="${2:-}"

if [[ -z "${playback_device}" ]]; then
  echo "Missing playback device" >&2
  exit 1
fi

while true; do
  if aplay -q -D "${playback_device}" -t raw -f S16_LE -r 44100 -d 1 /dev/zero >/dev/null 2>&1; then
    alsaloop -C "${loopback_capture}" -P "${playback_device}" -t 200000 -A 50000
  else
    sleep 2
  fi
done
EOF

  chmod 0755 "${BRIDGE_SCRIPT}"
}

write_bridge_service() {
  local alsa_device="$1"

  cat > "${BRIDGE_SERVICE}" <<EOF
[Unit]
Description=Oceano AirPlay Loopback Bridge
After=sound.target
Wants=sound.target

[Service]
Type=simple
ExecStart=${BRIDGE_SCRIPT} hw:Loopback,1,0 ${alsa_device}
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
}

enable_loopback_mode() {
  local alsa_device="$1"

  echo "snd-aloop" > "${MODULES_LOAD_FILE}"
  modprobe snd-aloop
  write_bridge_script
  write_bridge_service "${alsa_device}"
  systemctl daemon-reload
  systemctl enable oceano-airplay-bridge.service
  systemctl restart oceano-airplay-bridge.service
}

disable_loopback_mode() {
  systemctl disable --now oceano-airplay-bridge.service >/dev/null 2>&1 || true
  rm -f "${BRIDGE_SERVICE}"
  rm -f "${MODULES_LOAD_FILE}"
  systemctl daemon-reload
  systemctl reset-failed oceano-airplay-bridge.service >/dev/null 2>&1 || true
}

main() {
  if ! is_root; then
    echo "Please run as root (use sudo): sudo ./install.sh" >&2
    exit 1
  fi

  local repo_url="${DEFAULT_REPO_URL}"
  local branch="${DEFAULT_BRANCH}"
  local airplay_name="${DEFAULT_AIRPLAY_NAME}"
  local usb_match="${DEFAULT_USB_MATCH}"
  local preplay_wait_seconds="${DEFAULT_PREPLAY_WAIT_SECONDS}"
  local output_strategy="${DEFAULT_OUTPUT_STRATEGY}"
  local alsa_device=""
  local airplay_name_set=0
  local usb_match_set=0
  local alsa_device_set=0
  local preplay_wait_seconds_set=0
  local output_strategy_set=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --repo)
        repo_url="${2:-}"
        shift 2
        ;;
      --branch)
        branch="${2:-}"
        shift 2
        ;;
      --airplay-name)
        airplay_name="${2:-}"
        airplay_name_set=1
        shift 2
        ;;
      --usb-match)
        usb_match="${2:-}"
        usb_match_set=1
        shift 2
        ;;
      --alsa-device)
        alsa_device="${2:-}"
        alsa_device_set=1
        shift 2
        ;;
      --preplay-wait-seconds)
        preplay_wait_seconds="${2:-}"
        preplay_wait_seconds_set=1
        shift 2
        ;;
      --output-strategy)
        output_strategy="${2:-}"
        output_strategy_set=1
        shift 2
        ;;
      -h|--help)
        echo "Usage: sudo ./install.sh [--repo <url>] [--branch <name>] [--airplay-name <name>] [--usb-match <text>] [--alsa-device <plughw:CARD=...,DEV=0|hw:x,y>] [--preplay-wait-seconds <0-60>] [--output-strategy <direct|loopback>]" >&2
        exit 0
        ;;
      *)
        echo "Unknown argument: $1" >&2
        exit 1
        ;;
    esac
  done

  if [[ -z "${repo_url}" || -z "${branch}" ]]; then
    echo "repo/branch cannot be empty" >&2
    exit 1
  fi

  require_cmd apt-get
  require_cmd systemctl
  require_cmd aplay
  require_cmd awk
  require_cmd sed

  echo "Installing OS dependencies..."
  apt-get update -y
  apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    shairport-sync \
    alsa-utils

  require_cmd git

  echo "Preparing directories..."
  mkdir -p "${INSTALL_DIR}"

  if [[ -f "${CONFIG_FILE}" ]]; then
    # shellcheck source=/dev/null
    source "${CONFIG_FILE}"
    if [[ "${airplay_name_set}" -eq 0 && -n "${AIRPLAY_NAME:-}" ]]; then
      airplay_name="${AIRPLAY_NAME}"
    fi
    if [[ "${usb_match_set}" -eq 0 && -n "${USB_MATCH:-}" ]]; then
      usb_match="${USB_MATCH}"
    fi
    if [[ "${alsa_device_set}" -eq 0 && -n "${ALSA_DEVICE:-}" ]]; then
      alsa_device="${ALSA_DEVICE}"
    fi
    if [[ "${preplay_wait_seconds_set}" -eq 0 && -n "${PREPLAY_WAIT_SECONDS:-}" ]]; then
      preplay_wait_seconds="${PREPLAY_WAIT_SECONDS}"
    fi
    if [[ "${output_strategy_set}" -eq 0 && -n "${OUTPUT_STRATEGY:-}" ]]; then
      output_strategy="${OUTPUT_STRATEGY}"
    fi
  fi

  if ! [[ "${preplay_wait_seconds}" =~ ^[0-9]+$ ]] || (( preplay_wait_seconds < 0 || preplay_wait_seconds > 60 )); then
    echo "--preplay-wait-seconds must be an integer between 0 and 60" >&2
    exit 1
  fi

  if [[ "${output_strategy}" != "direct" && "${output_strategy}" != "loopback" ]]; then
    echo "--output-strategy must be one of: direct, loopback" >&2
    exit 1
  fi

  echo "Cloning/updating source into ${SRC_DIR}..."
  if [[ -d "${SRC_DIR}/.git" ]]; then
    git -C "${SRC_DIR}" fetch --prune
    git -C "${SRC_DIR}" checkout "${branch}"
    git -C "${SRC_DIR}" pull --ff-only
  else
    rm -rf "${SRC_DIR}"
    git clone --branch "${branch}" --depth 1 "${repo_url}" "${SRC_DIR}"
  fi

  if [[ -z "${alsa_device}" ]]; then
    if alsa_device="$(detect_alsa_device "${usb_match}")"; then
      echo "Detected USB audio device '${usb_match}' as ${alsa_device}"
    else
      echo "Could not auto-detect USB device matching '${usb_match}'." >&2
      echo "Set explicitly with: --alsa-device 'plughw:CARD=M780,DEV=0'" >&2
      exit 1
    fi
  fi

  echo "Writing ${SHAIRPORT_CONF}..."
  write_preplay_wait_script
  write_shairport_config "${airplay_name}" "${alsa_device}" "${preplay_wait_seconds}" "${output_strategy}"
  if [[ "${output_strategy}" == "loopback" ]]; then
    enable_loopback_mode "${alsa_device}"
  else
    disable_loopback_mode
  fi
  cat > "${CONFIG_FILE}" <<EOF
AIRPLAY_NAME="${airplay_name}"
USB_MATCH="${usb_match}"
ALSA_DEVICE="${alsa_device}"
PREPLAY_WAIT_SECONDS="${preplay_wait_seconds}"
OUTPUT_STRATEGY="${output_strategy}"
EOF

  # Clean switch: this project now reuses distro shairport-sync service.
  systemctl disable --now oceano-player.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/oceano-player.service
  systemctl daemon-reload
  systemctl enable --now shairport-sync.service

  echo
  echo "Done."
  echo "- Service status: systemctl status shairport-sync.service"
  echo "- Logs: journalctl -u shairport-sync.service -f"
  echo "- AirPlay name: ${airplay_name}"
  echo "- ALSA device: ${alsa_device}"
  echo "- Standby wake wait: ${preplay_wait_seconds}s"
  echo "- Output strategy: ${output_strategy}"
  echo "- Metadata pipe for now-playing: /tmp/shairport-sync-metadata"
  echo "- Saved config: ${CONFIG_FILE}"
  echo "- Backup created (first run): ${SHAIRPORT_CONF}.oceano.bak"
}

main "$@"

