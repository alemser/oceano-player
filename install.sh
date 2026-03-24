#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="/opt/oceano-player"
SRC_DIR="/opt/oceano-player/src"
DEFAULT_REPO_URL="https://github.com/alemser/oceano-player.git"
DEFAULT_BRANCH="main"
DEFAULT_AIRPLAY_NAME="Triangle AirPlay"
DEFAULT_USB_MATCH="M780"
SHAIRPORT_CONF="/etc/shairport-sync.conf"

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
          if (match(dev, /CARD=([^,]+)/, a)) { print a[1]; exit }
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
  output_device = "${alsa_device}";
  mixer_control_name = "none";
};

metadata =
{
  enabled = "yes";
  include_cover_art = "yes";
  pipe_name = "/tmp/shairport-sync-metadata";
  pipe_timeout = 5000;
  cover_art_cache_directory = "/tmp/shairport-sync/.cache/coverart";
};
EOF
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
  local alsa_device=""

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
        shift 2
        ;;
      --usb-match)
        usb_match="${2:-}"
        shift 2
        ;;
      --alsa-device)
        alsa_device="${2:-}"
        shift 2
        ;;
      -h|--help)
        echo "Usage: sudo ./install.sh [--repo <url>] [--branch <name>] [--airplay-name <name>] [--usb-match <text>] [--alsa-device <hw:x,y>]" >&2
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
      echo "Set explicitly with: --alsa-device hw:1,0" >&2
      exit 1
    fi
  fi

  echo "Writing ${SHAIRPORT_CONF}..."
  write_shairport_config "${airplay_name}" "${alsa_device}"

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
  echo "- Metadata pipe for now-playing: /tmp/shairport-sync-metadata"
  echo "- Backup created (first run): ${SHAIRPORT_CONF}.oceano.bak"
}

main "$@"

