#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────
#  Oceano Player — Install / Update Script
#  Supports: install (first run) and update (subsequent runs)
# ─────────────────────────────────────────────

INSTALL_DIR="/opt/oceano-player"
SRC_DIR="/opt/oceano-player/src"
CONFIG_FILE="/opt/oceano-player/config.env"
VERSION_FILE="/opt/oceano-player/version"
REPO_URL="https://github.com/alemser/oceano-player.git"

DEFAULT_BRANCH="main"

DEFAULT_AIRPLAY_NAME="Oceano"
DEFAULT_USB_MATCH="M780"
CONFIG_JSON="/etc/oceano/config.json"
DEFAULT_PREPLAY_WAIT_SECONDS="8"
DEFAULT_OUTPUT_STRATEGY="pipewire"

SHAIRPORT_CONF="/etc/shairport-sync.conf"
PREPLAY_WAIT_SCRIPT="/usr/local/bin/oceano-airplay-preplay-wait.sh"
BRIDGE_SCRIPT="/usr/local/bin/oceano-airplay-bridge.sh"
BRIDGE_SERVICE="/etc/systemd/system/oceano-airplay-bridge.service"
BRIDGE_WATCHDOG_SCRIPT="/usr/local/bin/oceano-bridge-watchdog.sh"
BRIDGE_WATCHDOG_SERVICE="/etc/systemd/system/oceano-bridge-watchdog.service"
DIRECT_WATCHDOG_SCRIPT="/usr/local/bin/oceano-direct-watchdog.sh"
DIRECT_WATCHDOG_SERVICE="/etc/systemd/system/oceano-direct-watchdog.service"
MODULES_LOAD_FILE="/etc/modules-load.d/oceano-player.conf"

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
  [[ -f "${CONFIG_FILE}" && -d "${SRC_DIR}/.git" ]]
}

get_installed_version() {
  if [[ -f "${VERSION_FILE}" ]]; then
    cat "${VERSION_FILE}"
  else
    echo "(unknown)"
  fi
}

get_latest_version() {
  git -C "${SRC_DIR}" describe --tags --always 2>/dev/null || git -C "${SRC_DIR}" rev-parse --short HEAD 2>/dev/null || echo "(no version)"
}

# ─── Go installation ─────────────────────────

GO_VERSION="1.24.1"
GO_INSTALL_DIR="/usr/local/go"

go_bin() {
  if command -v go >/dev/null 2>&1; then
    command -v go
  elif [[ -x "${GO_INSTALL_DIR}/bin/go" ]]; then
    echo "${GO_INSTALL_DIR}/bin/go"
  fi
}

ensure_go() {
  local existing
  existing="$(go_bin)"

  if [[ -n "${existing}" ]]; then
    local ver
    ver="$("${existing}" version 2>/dev/null | awk '{print $3}' | sed 's/go//')"
    log_ok "Go already installed: ${ver} (${existing})"
    return 0
  fi

  log_info "Go not found — installing Go ${GO_VERSION}..."

  local arch
  arch="$(uname -m)"
  case "${arch}" in
    aarch64|arm64) arch="arm64" ;;
    armv7l|armv6l) arch="armv6l" ;;
    x86_64)        arch="amd64" ;;
    *)
      log_error "Unsupported architecture: ${arch}"
      exit 1
      ;;
  esac

  local tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
  local url="https://go.dev/dl/${tarball}"
  local tmp="/tmp/${tarball}"

  log_info "Downloading ${url}..."
  curl -fsSL -o "${tmp}" "${url}" || {
    log_error "Failed to download Go. Check internet connection."
    exit 1
  }

  log_info "Extracting to ${GO_INSTALL_DIR}..."
  rm -rf "${GO_INSTALL_DIR}"
  tar -C /usr/local -xzf "${tmp}"
  rm -f "${tmp}"

  # Make go available in PATH for this session and future ones
  export PATH="${GO_INSTALL_DIR}/bin:${PATH}"

  if ! command -v go >/dev/null 2>&1; then
    log_error "Go installation failed — binary not found after extraction."
    exit 1
  fi

  log_ok "Go ${GO_VERSION} installed at ${GO_INSTALL_DIR}/bin/go"

  # Persist PATH for all users
  local profile="/etc/profile.d/go.sh"
  if [[ ! -f "${profile}" ]]; then
    echo 'export PATH="/usr/local/go/bin:$PATH"' > "${profile}"
    log_info "Added Go to PATH via ${profile}"
  fi
}

# ─── ALSA device detection ───────────────────

detect_alsa_device() {
  local match="$1"
  local ap_out card_id

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

# ─── Output strategy ──────────────────────────

write_shairport_config() {
  local airplay_name="$1"
  local alsa_device="$2"
  local preplay_wait_seconds="$3"
  local output_strategy="$4"

  if [[ -f "${SHAIRPORT_CONF}" && ! -f "${SHAIRPORT_CONF}.oceano.bak" ]]; then
    cp "${SHAIRPORT_CONF}" "${SHAIRPORT_CONF}.oceano.bak"
    log_info "Original shairport-sync.conf backed up to ${SHAIRPORT_CONF}.oceano.bak"
  fi

  # PipeWire mode: use the pa (PulseAudio-compatible) backend.
  # On Raspberry Pi OS Bookworm, pa routes through pipewire-pulse automatically.
  # No ALSA bridge or preplay-wait needed — PipeWire manages device availability.
  if [[ "${output_strategy}" == "pipewire" ]]; then
    cat > "${SHAIRPORT_CONF}" <<EOF
general =
{
  name = "${airplay_name}";
  interpolation = "soxr";
};

output =
{
  output_backend = "pa";
};

pa =
{
  application_name = "Shairport Sync";
  sink = "";
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
};
EOF
    return
  fi

  # ALSA-based modes (loopback / direct).
  local mixer_device="none"
  local shairport_output_device="${alsa_device}"

  if [[ "${output_strategy}" == "loopback" ]]; then
    shairport_output_device="plughw:CARD=Loopback,DEV=0"
    mixer_device="hw:CARD=Loopback"
  else
    if [[ "${alsa_device}" =~ ^plughw:CARD=([^,]+),DEV=([0-9]+)$ ]]; then
      mixer_device="hw:CARD=${BASH_REMATCH[1]}"
    elif [[ "${alsa_device}" =~ ^plughw:([0-9]+),([0-9]+)$ ]]; then
      mixer_device="hw:${BASH_REMATCH[1]}"
    fi
  fi

  # In loopback mode shairport_output_device is the always-present Loopback
  # device, so also pass the real DAC so the preplay-wait guards against the
  # amplifier not yet being on the USB input when playback starts.
  local preplay_cmd
  preplay_cmd="${PREPLAY_WAIT_SCRIPT} \\\"${shairport_output_device}\\\" ${preplay_wait_seconds}"
  if [[ "${output_strategy}" == "loopback" && -n "${alsa_device}" ]]; then
    preplay_cmd+=" \\\"${alsa_device}\\\""
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
  run_this_before_play_begins = "${preplay_cmd}";
};
EOF
}

write_preplay_wait_script() {
  cat > "${PREPLAY_WAIT_SCRIPT}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

# Arg 1: output device (checked first — required for direct mode; always
#         present Loopback device in loopback mode).
# Arg 2: max seconds to wait (default 8).
# Arg 3: real DAC device (optional; passed in loopback mode so we also gate
#         on the amplifier actually being on the USB input).
alsa_device="${1:-}"
wait_seconds="${2:-8}"
dac_device="${3:-}"

if [[ -z "${alsa_device}" ]]; then
  exit 0
fi

if ! [[ "${wait_seconds}" =~ ^[0-9]+$ ]]; then
  wait_seconds=8
fi

wait_for_device() {
  local dev="$1" secs="$2"
  local attempt=0
  while (( attempt < secs )); do
    if aplay -q -D "${dev}" -t raw -f S16_LE -r 44100 -d 1 /dev/zero >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    ((attempt += 1))
  done
  return 1
}

# Always check the shairport output device first.
wait_for_device "${alsa_device}" "${wait_seconds}" || true

# In loopback mode a real DAC device is also supplied.  Wait for it so we
# don't start playing into the loopback before the bridge can route audio
# to the amplifier (i.e. amp must be on the USB input).
if [[ -n "${dac_device}" ]]; then
  if ! wait_for_device "${dac_device}" "${wait_seconds}"; then
    echo "preplay-wait: DAC ${dac_device} not available after ${wait_seconds}s — proceeding anyway" >&2
  fi
fi

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

write_bridge_watchdog_script() {
  cat > "${BRIDGE_WATCHDOG_SCRIPT}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

alsa_device="${1:-${ALSA_DEVICE:-}}"
poll_interval="${2:-10}"

if [[ -z "${alsa_device}" ]]; then
  echo "Missing ALSA device" >&2
  exit 1
fi

last_available=0

while true; do
  if aplay -q -D "${alsa_device}" -t raw -f S16_LE -r 44100 -d 1 /dev/zero >/dev/null 2>&1; then
    current_available=1
  else
    current_available=0
  fi

  if (( current_available == 1 && last_available == 0 )); then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] DAC became available again, restarting bridge..." >&2
    if systemctl is-active --quiet oceano-airplay-bridge.service; then
      systemctl restart oceano-airplay-bridge.service
    fi
  fi

  last_available="${current_available}"
  sleep "${poll_interval}"
done
EOF
  chmod 0755 "${BRIDGE_WATCHDOG_SCRIPT}"
}

write_bridge_watchdog_service() {
  cat > "${BRIDGE_WATCHDOG_SERVICE}" <<EOF
[Unit]
Description=Oceano AirPlay Bridge Watchdog
After=oceano-airplay-bridge.service
Wants=oceano-airplay-bridge.service

[Service]
Type=simple
ExecStart=${BRIDGE_WATCHDOG_SCRIPT} \${ALSA_DEVICE} 10
EnvironmentFile=${CONFIG_FILE}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
}

# ─── Direct mode watchdog ─────────────────────

write_direct_watchdog_script() {
  cat > "${DIRECT_WATCHDOG_SCRIPT}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

alsa_device="${1:-${ALSA_DEVICE:-}}"
poll_interval="${2:-10}"

if [[ -z "${alsa_device}" ]]; then
  echo "Missing ALSA device" >&2
  exit 1
fi

# Check hardware presence via /proc/asound — no exclusive open needed,
# so this works even while shairport-sync holds the device during playback.
dac_present() {
  local dev="$1"
  if [[ "${dev}" =~ ^(plug)?hw:CARD=([^,]+) ]]; then
    grep -qi "${BASH_REMATCH[2]}" /proc/asound/cards 2>/dev/null
  elif [[ "${dev}" =~ ^(plug)?hw:([0-9]+) ]]; then
    [[ -d "/proc/asound/card${BASH_REMATCH[2]}" ]]
  else
    # Unknown format — fall back to aplay probe; treat "busy" as present.
    local err
    err=$(aplay -q -D "${dev}" -t raw -f S16_LE -r 44100 -d 1 /dev/zero 2>&1 || true)
    [[ -z "${err}" || "${err}" == *"Device or resource busy"* ]]
  fi
}

last_available=0

while true; do
  if dac_present "${alsa_device}"; then
    current_available=1
  else
    current_available=0
  fi

  if (( current_available == 1 && last_available == 0 )); then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] DAC became available, restarting shairport-sync..." >&2
    systemctl restart shairport-sync.service || true
  fi

  last_available="${current_available}"
  sleep "${poll_interval}"
done
EOF
  chmod 0755 "${DIRECT_WATCHDOG_SCRIPT}"
}

write_direct_watchdog_service() {
  cat > "${DIRECT_WATCHDOG_SERVICE}" <<EOF
[Unit]
Description=Oceano AirPlay Direct Watchdog
After=shairport-sync.service
Wants=shairport-sync.service

[Service]
Type=simple
ExecStart=${DIRECT_WATCHDOG_SCRIPT} \${ALSA_DEVICE} 10
EnvironmentFile=${CONFIG_FILE}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
}

# ─── Loopback mode ───────────────────────────

enable_loopback_mode() {
  local alsa_device="$1"
  echo "snd-aloop" > "${MODULES_LOAD_FILE}"
  modprobe snd-aloop
  write_bridge_script
  write_bridge_service "${alsa_device}"
  write_bridge_watchdog_script
  write_bridge_watchdog_service
  systemctl daemon-reload
  systemctl enable oceano-airplay-bridge.service
  systemctl enable oceano-bridge-watchdog.service
  systemctl restart oceano-airplay-bridge.service
  systemctl restart oceano-bridge-watchdog.service
}

disable_loopback_mode() {
  systemctl disable --now oceano-airplay-bridge.service >/dev/null 2>&1 || true
  systemctl disable --now oceano-bridge-watchdog.service >/dev/null 2>&1 || true
  # Systemd only kills the top-level bash scripts; their child processes (alsaloop)
  # and any orphaned script instances survive. Kill them all explicitly.
  pkill -f oceano-bridge-watchdog.sh >/dev/null 2>&1 || true
  pkill -f oceano-airplay-bridge.sh  >/dev/null 2>&1 || true
  pkill -x alsaloop                  >/dev/null 2>&1 || true
  rm -f "${BRIDGE_SERVICE}" "${BRIDGE_WATCHDOG_SERVICE}" "${MODULES_LOAD_FILE}"
  systemctl daemon-reload
  systemctl reset-failed oceano-airplay-bridge.service >/dev/null 2>&1 || true
  systemctl reset-failed oceano-bridge-watchdog.service >/dev/null 2>&1 || true
}

# ─── Direct mode ─────────────────────────────

enable_direct_watchdog() {
  write_direct_watchdog_script
  write_direct_watchdog_service
  systemctl daemon-reload
  systemctl enable oceano-direct-watchdog.service
  systemctl restart oceano-direct-watchdog.service
}

disable_direct_watchdog() {
  systemctl disable --now oceano-direct-watchdog.service >/dev/null 2>&1 || true
  rm -f "${DIRECT_WATCHDOG_SERVICE}"
  systemctl daemon-reload
  systemctl reset-failed oceano-direct-watchdog.service >/dev/null 2>&1 || true
}

# ─── PipeWire mode ───────────────────────────

# enable_pipewire_mode switches shairport-sync to the pa backend (PipeWire-pulse)
# and sets up the oceano-pipewire-default-sink user service to make the DAC the
# default PipeWire sink. This allows both AirPlay and Bluetooth to route through
# PipeWire to the DAC without ALSA exclusive-access conflicts.
enable_pipewire_mode() {
  local audio_user="$1"
  local audio_uid="$2"

  # Clean up ALSA bridge modes — they conflict with PipeWire DAC ownership.
  disable_loopback_mode
  disable_direct_watchdog

  # Drop-in: run shairport-sync as the audio user so it can reach the
  # per-user PipeWire-pulse socket at /run/user/<uid>/pulse/native.
  local dropin_dir="/etc/systemd/system/shairport-sync.service.d"
  mkdir -p "${dropin_dir}"
  cat > "${dropin_dir}/oceano-pipewire.conf" <<EOF
[Service]
User=${audio_user}
Environment=PULSE_SERVER=unix:/run/user/${audio_uid}/pulse/native
Environment=XDG_RUNTIME_DIR=/run/user/${audio_uid}
EOF
  log_info "shairport-sync will run as '${audio_user}' and connect to PipeWire-pulse."

  systemctl daemon-reload
}

disable_pipewire_mode() {
  # Remove the shairport-sync PipeWire drop-in (reverts to system user).
  rm -f /etc/systemd/system/shairport-sync.service.d/oceano-pipewire.conf
  rmdir /etc/systemd/system/shairport-sync.service.d 2>/dev/null || true

  # Disable the default-sink user service and remove its files.
  local audio_user
  audio_user="$(getent passwd | awk -F: '$3 >= 1000 && $6 ~ /^\/home/ {print $1; exit}')"
  if [[ -n "${audio_user}" ]]; then
    local audio_uid home_dir
    audio_uid="$(id -u "${audio_user}")"
    home_dir="$(getent passwd "${audio_user}" | cut -d: -f6)"
    if [[ -S "/run/user/${audio_uid}/bus" ]]; then
      su -l "${audio_user}" -s /bin/bash -c \
        "XDG_RUNTIME_DIR=/run/user/${audio_uid} \
         DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/${audio_uid}/bus \
         systemctl --user disable --now oceano-pipewire-default-sink.service" \
        >/dev/null 2>&1 || true
    fi
    rm -f "${home_dir}/.config/systemd/user/oceano-pipewire-default-sink.service"
  fi
  rm -f /usr/local/bin/oceano-pipewire-default-sink

  systemctl daemon-reload
}

# ─── Bluetooth ───────────────────────────────

# setup_bluetooth configures the built-in Bluetooth adapter so the Pi is
# permanently discoverable and paired devices can stream audio to it.
# The device name is set to match the AirPlay name for consistency.
# Audio codec negotiation (SBC / AAC / Opus) is handled by PipeWire automatically
# when libspa-0.2-bluetooth is present (pre-installed on Raspberry Pi OS Bookworm).
setup_bluetooth() {
  local device_name="$1"
  local bt_conf="/etc/bluetooth/main.conf"

  # Enable the Bluetooth service if not already running.
  systemctl enable bluetooth.service >/dev/null 2>&1 || true
  systemctl start  bluetooth.service 2>/dev/null || true

  if [[ -f "${bt_conf}" ]]; then
    # Set device name — used for both discoverability and BLE advertising.
    if grep -qE '^\s*Name\s*=' "${bt_conf}"; then
      sed -i "s|^\s*Name\s*=.*|Name = ${device_name}|" "${bt_conf}"
    else
      # Add under [General] section if present, otherwise append.
      if grep -q '^\[General\]' "${bt_conf}"; then
        sed -i "/^\[General\]/a Name = ${device_name}" "${bt_conf}"
      else
        printf '\n[General]\nName = %s\n' "${device_name}" >> "${bt_conf}"
      fi
    fi

    # Make the adapter always discoverable (default timeout is 180 s).
    if grep -qE '^\s*#?\s*DiscoverableTimeout\s*=' "${bt_conf}"; then
      sed -i "s|^\s*#\?\s*DiscoverableTimeout\s*=.*|DiscoverableTimeout = 0|" "${bt_conf}"
    else
      if grep -q '^\[General\]' "${bt_conf}"; then
        sed -i '/^\[General\]/a DiscoverableTimeout = 0' "${bt_conf}"
      else
        printf 'DiscoverableTimeout = 0\n' >> "${bt_conf}"
      fi
    fi

    # Auto-enable the adapter on boot (in case it was powered off).
    if grep -qE '^\s*#?\s*AutoEnable\s*=' "${bt_conf}"; then
      sed -i "s|^\s*#\?\s*AutoEnable\s*=.*|AutoEnable = true|" "${bt_conf}"
    else
      if grep -q '^\[Policy\]' "${bt_conf}"; then
        sed -i '/^\[Policy\]/a AutoEnable = true' "${bt_conf}"
      fi
    fi

    systemctl restart bluetooth.service &
  else
    log_warn "Bluetooth config not found at ${bt_conf} — skipping name/discoverability setup."
  fi

  # shairport-sync overwrites the adapter alias with the AirPlay name on every start.
  # Use dbus-send directly (reliable in non-interactive context) via a dedicated
  # oneshot service that runs after shairport-sync has finished its own setup.
  cat > /etc/systemd/system/oceano-bt-alias.service <<EOF
[Unit]
Description=Restore Bluetooth adapter alias to ${device_name}
After=shairport-sync.service
Wants=shairport-sync.service

[Service]
Type=oneshot
ExecStartPre=/bin/sleep 2
ExecStart=/usr/bin/dbus-send --system --print-reply --dest=org.bluez /org/bluez/hci0 org.freedesktop.DBus.Properties.Set string:org.bluez.Adapter1 string:Alias variant:string:${device_name}
RemainAfterExit=no

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable oceano-bt-alias.service
  log_ok "Bluetooth alias '${device_name}' will be restored after shairport-sync starts."

  # Install a persistent auto-pairing agent so headless pairing works without
  # a touchscreen. bt-agent -c NoInputNoOutput accepts all pairing requests automatically.
  if command -v bt-agent >/dev/null 2>&1; then
    cat > /etc/systemd/system/bt-agent.service <<'EOF'
[Unit]
Description=Bluetooth auto-pairing agent
After=bluetooth.service
Requires=bluetooth.service

[Service]
ExecStart=/usr/bin/bt-agent -c NoInputNoOutput
Restart=on-failure
RestartSec=5
StandardOutput=null
StandardError=null

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable bt-agent.service
    log_ok "Bluetooth auto-pairing agent enabled (starts on next boot)."
  else
    log_warn "bt-agent not found — manual pairing confirmation required (install bluez-tools to fix)."
  fi

  # Warn if dbus-monitor is missing — the state manager bluetooth monitor needs it.
  if ! command -v dbus-monitor >/dev/null 2>&1; then
    log_warn "dbus-monitor not found — Bluetooth metadata monitoring will be disabled."
    log_warn "Install it with: sudo apt install dbus"
  fi

  log_ok "Bluetooth configured: device name='${device_name}', alias='${device_name}', always discoverable."
  log_info "To pair: Settings → Bluetooth → '${device_name}'"
  log_info "Pair once; the Pi will remember trusted devices across reboots."
}

# ─── WirePlumber routing ─────────────────────

# setup_wireplumber_routing installs a user systemd service that sets the USB
# DAC as the default PipeWire sink after WirePlumber starts, routing Bluetooth
# audio to the DAC instead of whatever PipeWire picks as the default.
#
# Uses wpctl set-default (matching by description) instead of WirePlumber Lua
# rules, which vary across WirePlumber versions and can break the ALSA monitor.
#
# The AirPlay pipeline (shairport-sync → ALSA Loopback → bridge → DAC) is
# unaffected because it writes to the ALSA device layer directly, bypassing
# PipeWire entirely.
setup_wireplumber_routing() {
  local usb_match="$1"
  local audio_user="$2"
  local audio_uid="$3"

  # Derive the ALSA card long name (e.g. "MR 780") from the aplay -l output:
  #   card 4: M780 [MR 780], device 0: USB Audio [USB Audio]
  #                  ^^^^^^ this part, inside the brackets
  local card_longname=""
  local aplay_line
  aplay_line="$(aplay -l 2>/dev/null | awk -v m="${usb_match}" \
    'BEGIN{IGNORECASE=1} /card [0-9]+:.*device [0-9]+:/ && index(tolower($0), tolower(m)) {print; exit}')"
  if [[ -n "${aplay_line}" ]]; then
    card_longname="$(echo "${aplay_line}" | sed -E 's/.*card [0-9]+: [^ ]+ \[([^]]+)\].*/\1/')"
    [[ "${card_longname}" == "${aplay_line}" ]] && card_longname=""
  fi

  local dac_desc="${card_longname:-${usb_match}}"
  if [[ -n "${card_longname}" ]]; then
    log_info "PipeWire routing: DAC description detected as '${dac_desc}'"
  else
    log_warn "PipeWire routing: could not detect card long name — using '${dac_desc}'"
  fi

  local home_dir
  home_dir="$(getent passwd "${audio_user}" | cut -d: -f6)"

  # Remove any stale Lua config that may have broken the ALSA monitor.
  rm -f "${home_dir}/.config/wireplumber/main.lua.d/90-oceano-default-sink.lua"

  # Write a small helper script: finds the DAC in wpctl by description and
  # calls wpctl set-default. Runs after WirePlumber to survive device numbering
  # changes across reboots.
  local script_path="/usr/local/bin/oceano-pipewire-default-sink"
  cat > "${script_path}" <<EOF
#!/usr/bin/env bash
# Set the Oceano DAC as the default PipeWire sink.
# Called by oceano-pipewire-default-sink.service after WirePlumber starts.
# If the DAC is not found immediately, retries for up to 60 s (boot can be slow).
set -euo pipefail
DAC_DESC="${dac_desc}"
for attempt in \$(seq 1 12); do
  node_id="\$(wpctl status 2>/dev/null \
    | awk -v desc="\${DAC_DESC}" 'index(\$0, desc) && match(\$0, /[0-9]+\\./) {
        id=substr(\$0, RSTART, RLENGTH-1); if (id+0>0) {print id; exit}
      }')"
  if [[ -n "\${node_id}" ]]; then
    wpctl set-default "\${node_id}"
    echo "oceano-pipewire-default-sink: set default sink to node \${node_id} (\${DAC_DESC}) on attempt \${attempt}"
    exit 0
  fi
  echo "oceano-pipewire-default-sink: attempt \${attempt} — '\${DAC_DESC}' not yet visible in PipeWire, retrying in 5 s..."
  sleep 5
done
echo "oceano-pipewire-default-sink: DAC '\${DAC_DESC}' not found in PipeWire after 12 attempts (60 s)"
exit 1
EOF
  chmod +x "${script_path}"

  # Install as a user systemd service so it runs in the user's PipeWire session.
  local svc_dir="${home_dir}/.config/systemd/user"
  mkdir -p "${svc_dir}"
  cat > "${svc_dir}/oceano-pipewire-default-sink.service" <<'EOF'
[Unit]
Description=Set Oceano DAC as default PipeWire sink
After=wireplumber.service pipewire.service
Wants=wireplumber.service pipewire.service
StartLimitIntervalSec=120
StartLimitBurst=6

[Service]
Type=oneshot
ExecStart=/usr/local/bin/oceano-pipewire-default-sink
RemainAfterExit=no
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
EOF
  chown -R "${audio_user}:${audio_user}" "${svc_dir}"

  # Enable lingering so the user's systemd session (and PipeWire) starts on boot
  # without requiring an active login. Without this, user services never run headlessly.
  loginctl enable-linger "${audio_user}" 2>/dev/null || true
  log_info "Linger enabled for '${audio_user}' — user services will start at boot."

  # Enable and start the service as the audio user.
  local started=false
  if [[ -S "/run/user/${audio_uid}/bus" ]]; then
    su -l "${audio_user}" -s /bin/bash -c \
      "XDG_RUNTIME_DIR=/run/user/${audio_uid} \
       DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/${audio_uid}/bus \
       systemctl --user daemon-reload && \
       systemctl --user enable oceano-pipewire-default-sink.service && \
       systemctl --user restart oceano-pipewire-default-sink.service" \
      2>&1 && started=true || true
  fi

  if ${started}; then
    log_ok "PipeWire: DAC set as default sink (Bluetooth audio will route to DAC)."
  else
    log_warn "PipeWire routing service installed — will activate on next boot (linger enabled)."
    log_info "To apply immediately (as ${audio_user}):"
    log_info "  systemctl --user enable --now oceano-pipewire-default-sink.service"
  fi
}

# ─── Repository ──────────────────────────────

clone_repo() {
  local branch="$1"
  log_info "Cloning repository to ${SRC_DIR} (branch: ${branch})..."
  mkdir -p "${INSTALL_DIR}"
  git clone --branch "${branch}" "${REPO_URL}" "${SRC_DIR}"
  log_ok "Repository cloned successfully (branch: ${branch})."
}

sync_repo() {
  local branch="$1"
  local before current_branch

  before="$(git -C "${SRC_DIR}" rev-parse HEAD 2>/dev/null || echo "")"
  current_branch="$(git -C "${SRC_DIR}" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")"

  # Branch changed — clean checkout with no conflicts
  if [[ "${current_branch}" != "${branch}" ]]; then
    log_warn "Current branch: '${current_branch}' → switching to '${branch}'..."
    git -C "${SRC_DIR}" fetch origin
    git -C "${SRC_DIR}" reset --hard
    git -C "${SRC_DIR}" clean -fd >/dev/null
    git -C "${SRC_DIR}" checkout "${branch}"
    git -C "${SRC_DIR}" reset --hard "origin/${branch}"
    log_ok "Switched to branch '${branch}'."
    return
  fi

  log_info "Syncing branch '${branch}'..."
  git -C "${SRC_DIR}" fetch origin

  # Hard reset ensures no conflicts regardless of local state
  git -C "${SRC_DIR}" reset --hard "origin/${branch}"
  git -C "${SRC_DIR}" clean -fd >/dev/null

  local after
  after="$(git -C "${SRC_DIR}" rev-parse HEAD 2>/dev/null || echo "")"

  if [[ "${before}" == "${after}" ]]; then
    log_info "Repository already up to date (${after:0:8})."
  else
    log_ok "Repository updated: ${before:0:8} → ${after:0:8}"
  fi
}

# ─── Initialize /etc/oceano/config.json ──────

# write_initial_config creates /etc/oceano/config.json on first install so that
# all services start with consistent defaults from the very beginning.
# On update (file already exists) it is a no-op — user settings are preserved.
write_initial_config() {
  local airplay_name="$1"
  local alsa_device="$2"

  if [[ -f "${CONFIG_JSON}" ]]; then
    log_info "Config already exists at ${CONFIG_JSON} — preserving existing settings."
    return
  fi

  mkdir -p "$(dirname "${CONFIG_JSON}")"
  # Use python3 to write valid JSON — avoids shell quoting issues with device paths.
  python3 -c "
import json, sys
cfg = {
  'audio_input':  {'device_match': 'USB Microphone', 'device': '', 'silence_threshold': 0.025, 'debounce_windows': 10},
  'audio_output': {'airplay_name': sys.argv[1], 'device_match': '', 'device': sys.argv[2]},
  'recognition':  {'acrcloud_host': 'identify-eu-west-1.acrcloud.com', 'acrcloud_access_key': '', 'acrcloud_secret_key': '', 'capture_duration_secs': 10, 'max_interval_secs': 300},
  'advanced':     {'vu_socket': '/tmp/oceano-vu.sock', 'pcm_socket': '/tmp/oceano-pcm.sock', 'source_file': '/tmp/oceano-source.json', 'state_file': '/tmp/oceano-state.json', 'artwork_dir': '/var/lib/oceano/artwork', 'metadata_pipe': '/tmp/shairport-sync-metadata'},
  'display':      {'ui_preset': 'high_contrast_rotate', 'cycle_time': 30, 'standby_timeout': 600, 'external_artwork_enabled': True},
}
print(json.dumps(cfg, indent=2))
" "${airplay_name}" "${alsa_device}" > "${CONFIG_JSON}"
  chmod 0644 "${CONFIG_JSON}"
  log_ok "Config initialized at ${CONFIG_JSON}"
}

# ─── Save installed version ───────────────────

save_version() {
  local version
  version="$(get_latest_version)"
  echo "${version}" > "${VERSION_FILE}"
  log_info "Installed version: ${version}"
}

# ─── Main ────────────────────────────────────

main() {
  if ! is_root; then
    log_error "Please run as root: sudo ./install.sh"
    exit 1
  fi

  require_cmd systemctl
  require_cmd git
  require_cmd curl
  require_cmd aplay
  require_cmd awk
  require_cmd sed
  require_cmd python3

  # ── Parse arguments ──
  local airplay_name="${DEFAULT_AIRPLAY_NAME}"
  local usb_match="${DEFAULT_USB_MATCH}"
  local preplay_wait_seconds="${DEFAULT_PREPLAY_WAIT_SECONDS}"
  local output_strategy="${DEFAULT_OUTPUT_STRATEGY}"
  local branch="${DEFAULT_BRANCH}"
  local alsa_device=""
  local airplay_name_set=0 usb_match_set=0 alsa_device_set=0
  local preplay_wait_seconds_set=0 output_strategy_set=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --airplay-name)         airplay_name="${2:-}";          airplay_name_set=1;          shift 2 ;;
      --usb-match)            usb_match="${2:-}";             usb_match_set=1;             shift 2 ;;
      --alsa-device)          alsa_device="${2:-}";           alsa_device_set=1;           shift 2 ;;
      --preplay-wait-seconds) preplay_wait_seconds="${2:-}";  preplay_wait_seconds_set=1;  shift 2 ;;
      --output-strategy)      output_strategy="${2:-}";       output_strategy_set=1;       shift 2 ;;
      --branch)               branch="${2:-}";                                             shift 2 ;;
      -h|--help)
        echo "Usage: sudo ./install.sh [options]"
        echo ""
        echo "Options:"
        echo "  --branch <name>                Branch to install/update (default: '${DEFAULT_BRANCH}')"
        echo "  --airplay-name <name>          AirPlay name (default: '${DEFAULT_AIRPLAY_NAME}')"
        echo "  --usb-match <text>             Text to match USB DAC (default: '${DEFAULT_USB_MATCH}')"
        echo "  --alsa-device <plughw:...>     Explicit ALSA device"
        echo "  --preplay-wait-seconds <0-60>  Seconds to wait before playback (default: ${DEFAULT_PREPLAY_WAIT_SECONDS})"
        echo "  --output-strategy <pipewire|loopback|direct>  Output strategy (default: ${DEFAULT_OUTPUT_STRATEGY})"
        exit 0
        ;;
      *) log_error "Unknown argument: $1"; exit 1 ;;
    esac
  done

  # ── Detect mode: install or update ──
  local mode
  if is_installed; then
    mode="update"
  else
    mode="install"
  fi

  # ── Banner ──
  echo -e "\n${BOLD}╔══════════════════════════════════════╗"
  if [[ "${mode}" == "install" ]]; then
    echo -e "║   Oceano Player — FRESH INSTALL       ║"
  else
    echo -e "║   Oceano Player — UPDATE              ║"
  fi
  echo -e "╚══════════════════════════════════════╝${RESET}"

  if [[ "${branch}" != "${DEFAULT_BRANCH}" ]]; then
    echo -e "${YELLOW}${BOLD}  ⚠ Development branch: ${branch}${RESET}"
    echo -e "${YELLOW}  Do not use in production without testing!${RESET}"
  fi

  if [[ "${mode}" == "update" ]]; then
    log_info "Currently installed version: $(get_installed_version)"
  fi

  # ── Load existing config (update) ──
  # Prefer config.json (managed by web UI) over config.env for AirPlay name and ALSA device,
  # so that changes made in the web UI survive a re-install/update.
  if [[ -f "${CONFIG_JSON}" ]] && command -v python3 >/dev/null 2>&1; then
    _cfg() { python3 -c "import json,sys; c=json.load(open('${CONFIG_JSON}')); print(c$1)" 2>/dev/null || true; }
    _name="$(_cfg "['audio_output']['airplay_name']")"; [[ "${airplay_name_set}" -eq 0 && -n "${_name}" ]] && airplay_name="${_name}"
    _dev="$(_cfg "['audio_output']['device']")";        [[ "${alsa_device_set}" -eq 0  && -n "${_dev}"  ]] && alsa_device="${_dev}"
  fi
  if [[ -f "${CONFIG_FILE}" ]]; then
    source "${CONFIG_FILE}"
    [[ "${airplay_name_set}" -eq 0 && -n "${AIRPLAY_NAME:-}" && -z "${_name:-}" ]] && airplay_name="${AIRPLAY_NAME}"
    [[ "${usb_match_set}" -eq 0 && -n "${USB_MATCH:-}" ]]                && usb_match="${USB_MATCH}"
    [[ "${alsa_device_set}" -eq 0 && -n "${ALSA_DEVICE:-}" && -z "${_dev:-}" ]] && alsa_device="${ALSA_DEVICE}"
    [[ "${preplay_wait_seconds_set}" -eq 0 && -n "${PREPLAY_WAIT_SECONDS:-}" ]] && preplay_wait_seconds="${PREPLAY_WAIT_SECONDS}"
    [[ "${output_strategy_set}" -eq 0 && -n "${OUTPUT_STRATEGY:-}" ]]    && output_strategy="${OUTPUT_STRATEGY}"
  fi

  # ── Validate ──
  if ! [[ "${preplay_wait_seconds}" =~ ^[0-9]+$ ]] || (( preplay_wait_seconds < 0 || preplay_wait_seconds > 60 )); then
    log_error "--preplay-wait-seconds must be an integer between 0 and 60"
    exit 1
  fi
  if [[ "${output_strategy}" != "pipewire" && "${output_strategy}" != "direct" && "${output_strategy}" != "loopback" ]]; then
    log_error "--output-strategy must be one of: pipewire, loopback, direct"
    exit 1
  fi

  # ── Audio user — detected early; needed for PipeWire mode and BT routing ──
  local audio_user audio_uid
  audio_user="$(getent passwd | awk -F: '$3 >= 1000 && $6 ~ /^\/home/ {print $1; exit}')"
  audio_uid="$(id -u "${audio_user}" 2>/dev/null || echo "")"

  # ── System dependencies ──
  log_section "System Dependencies"
  log_info "Installing system packages..."
  apt-get update -qq
  apt-get install -y --no-install-recommends shairport-sync alsa-utils libchromaprint-tools ffmpeg bluez bluez-tools dbus libspa-0.2-bluetooth
  log_ok "System packages ready."

  # ── Bluetooth ──
  log_section "Bluetooth"
  # Use the Bluetooth name from config if set; otherwise strip " AirPlay" suffix
  # from the AirPlay name so both stay in sync without the confusing suffix.
  local bt_name
  bt_name="$(_cfg "['bluetooth']['name']" 2>/dev/null)" || bt_name=""
  if [[ -z "${bt_name}" ]]; then
    bt_name="${airplay_name% AirPlay}"
    [[ -z "${bt_name}" ]] && bt_name="${airplay_name}"
  fi
  setup_bluetooth "${bt_name}"

  # ── WirePlumber default-sink routing (pipewire mode only) ──
  if [[ "${output_strategy}" == "pipewire" && -n "${audio_user}" ]]; then
    log_section "PipeWire Routing"
    setup_wireplumber_routing "${usb_match}" "${audio_user}" "${audio_uid}"
  fi

  # ── Repository ──
  log_section "Repository"
  if [[ "${mode}" == "install" ]]; then
    clone_repo "${branch}"
  else
    sync_repo "${branch}"
  fi

  # ── Audio device detection ──
  log_section "Audio Device"
  if [[ "${alsa_device_set}" -eq 1 ]]; then
    # Explicitly provided via --alsa-device: trust it as-is.
    log_info "Using manually specified ALSA device: ${alsa_device}"
  else
    # Always re-detect by name on every run — card numbers can change when
    # the DAC is power-cycled or reconnected. Use the stored device only as
    # a last-resort fallback if the DAC is currently unreachable.
    local detected=""
    if detected="$(detect_alsa_device "${usb_match}")"; then
      if [[ "${detected}" != "${alsa_device}" && -n "${alsa_device}" ]]; then
        log_warn "Card number changed: ${alsa_device} → ${detected} (DAC was power-cycled?)"
      fi
      alsa_device="${detected}"
      log_ok "USB device '${usb_match}' detected: ${alsa_device}"
    elif [[ -n "${alsa_device}" ]]; then
      log_warn "Could not detect '${usb_match}' — DAC may be off. Using last known device: ${alsa_device}"
    else
      log_error "Could not detect USB device matching '${usb_match}'."
      echo ""
      echo -e "${YELLOW}  Make sure your USB DAC / amplifier is:${RESET}"
      echo -e "${YELLOW}    1. Powered on${RESET}"
      echo -e "${YELLOW}    2. Connected via USB to the Pi${RESET}"
      echo ""
      echo -e "  List all current ALSA playback devices to find yours:"
      echo -e "  ${BOLD}aplay -l${RESET}"
      echo ""
      echo -e "  Then re-run with the correct match string or explicit device:"
      echo -e "  ${BOLD}sudo ./install.sh --usb-match 'YourDAC'${RESET}"
      echo -e "  ${BOLD}sudo ./install.sh --alsa-device 'plughw:1,0'${RESET}"
      exit 1
    fi
  fi

  # ── Save config — must happen before services start (watchdog uses EnvironmentFile) ──
  mkdir -p "${INSTALL_DIR}"
  cat > "${CONFIG_FILE}" <<EOF
AIRPLAY_NAME="${airplay_name}"
USB_MATCH="${usb_match}"
ALSA_DEVICE="${alsa_device}"
PREPLAY_WAIT_SECONDS="${preplay_wait_seconds}"
OUTPUT_STRATEGY="${output_strategy}"
EOF

  # ── Configuration ──
  log_section "Configuration"
  log_info "Writing support scripts..."
  write_preplay_wait_script
  log_info "Applying shairport-sync configuration..."
  write_shairport_config "${airplay_name}" "${alsa_device}" "${preplay_wait_seconds}" "${output_strategy}"

  if [[ "${output_strategy}" == "pipewire" ]]; then
    if [[ -z "${audio_user}" ]]; then
      log_error "PipeWire mode requires a non-root user with UID ≥ 1000 and a /home directory."
      exit 1
    fi
    log_info "Enabling PipeWire mode (audio user: ${audio_user})..."
    disable_pipewire_mode  # clean up any previous state
    enable_pipewire_mode "${audio_user}" "${audio_uid}"
    log_ok "PipeWire mode active — shairport-sync and Bluetooth both route through PipeWire."
  elif [[ "${output_strategy}" == "loopback" ]]; then
    log_info "Enabling loopback mode..."
    disable_pipewire_mode
    disable_direct_watchdog
    enable_loopback_mode "${alsa_device}"
    log_ok "Loopback mode active."
  else
    log_info "Enabling direct mode..."
    disable_pipewire_mode
    disable_loopback_mode
    enable_direct_watchdog
    log_ok "Direct mode active."
  fi

  # ── systemd services ──
  log_section "systemd Services"
  systemctl disable --now oceano-player.service >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/oceano-player.service
  systemctl daemon-reload
  systemctl enable --now shairport-sync.service
  systemctl restart shairport-sync.service
  log_ok "shairport-sync.service is running."

  # ── Version ──
  save_version

  # ── Initial config — must exist before sub-scripts so they read consistent defaults ──
  write_initial_config "${airplay_name}" "${alsa_device}"

  # ── Initial display.env — written once so oceano-now-playing has env vars from the start ──
  local display_env="/etc/oceano/display.env"
  if [[ ! -f "${display_env}" ]]; then
    cat > "${display_env}" <<'DISPLAYENV'
UI_PRESET=high_contrast_rotate
CYCLE_TIME=30
STANDBY_TIMEOUT=600
EXTERNAL_ARTWORK_ENABLED=true
DISPLAYENV
    chmod 0644 "${display_env}"
    log_ok "Display env initialized at ${display_env}"
  fi

  # ── Go runtime ──
  log_section "Go Runtime"
  ensure_go

  # ── Go services ──
  log_section "Source Detector"
  bash "${SRC_DIR}/install-source-detector.sh" --branch "${branch}"

  log_section "State Manager"
  bash "${SRC_DIR}/install-source-manager.sh" --branch "${branch}"

  log_section "Web UI"
  bash "${SRC_DIR}/install-oceano-web.sh" --branch "${branch}"

  # ── Summary ──
  log_section "Done"
  if [[ "${mode}" == "install" ]]; then
    log_ok "Installation completed successfully!"
  else
    log_ok "Update completed successfully!"
    log_info "Installed version: $(get_installed_version)"
  fi

  local ip
  ip=$(hostname -I 2>/dev/null | awk '{print $1}') || ip="<pi-ip>"

  echo -e "
${BOLD}Configuration summary:${RESET}
  Branch             : ${branch}
  AirPlay name       : ${airplay_name}
  Bluetooth name     : ${bt_name}
  ALSA device        : ${alsa_device}
  Output strategy    : ${output_strategy}$( [[ "${output_strategy}" == "pipewire" ]] && echo " (shairport-sync + BT → PipeWire → DAC)" )
  Preplay wait       : ${preplay_wait_seconds}s$( [[ "${output_strategy}" == "pipewire" ]] && echo " (unused in PipeWire mode)" )
  Config saved to    : ${CONFIG_FILE}
  Version            : $(get_installed_version)

${BOLD}Web UI:${RESET}
  http://${ip}:8080
  (set ACRCloud credentials and audio devices here)

${BOLD}Useful commands:${RESET}
  systemctl status shairport-sync.service oceano-source-detector.service oceano-state-manager.service oceano-web.service
  journalctl -u oceano-web.service -f
"
}

main "$@"