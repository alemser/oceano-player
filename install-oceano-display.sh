#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────
#  Oceano Display — Install / Update Script
#
#  Installs a systemd service that launches Chromium in kiosk mode
#  showing the Oceano now-playing UI on an attached HDMI or DSI screen.
#
#  Target hardware: 7" HDMI monitor, 1024×600 (or any HDMI/DSI display).
#  The service only starts when a compatible display is detected.
# ─────────────────────────────────────────────

SERVICE_NAME="oceano-display.service"
SERVICE_DEST="/etc/systemd/system/${SERVICE_NAME}"
DISPLAY_CHECK_SCRIPT="/usr/local/bin/oceano-display-check"
WRAPPER_SCRIPT="/usr/local/bin/oceano-display-launch"

DEFAULT_WEB_ADDR="http://localhost:8080"
DEFAULT_DISPLAY_USER="pi"

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

is_root() { [[ "${EUID:-$(id -u)}" -eq 0 ]]; }

# ─── Check whether Chromium is available ─────

find_chromium() {
  for candidate in chromium-browser chromium; do
    if command -v "$candidate" >/dev/null 2>&1; then
      echo "$candidate"
      return 0
    fi
  done
  echo ""
}

# ─── Write the display-check helper ──────────
# This script is called by the systemd service's ExecStartPre to abort startup
# when no compatible HDMI or DSI display is detected, preventing the service
# from failing on headless (no-screen) deployments.

write_display_check() {
  cat > "${DISPLAY_CHECK_SCRIPT}" <<'SCRIPT'
#!/usr/bin/env bash
# Exits 0 if a connected HDMI or DSI display is detected via the DRM subsystem,
# non-zero otherwise.  Called by oceano-display.service ExecStartPre.
set -euo pipefail

FOUND=false
shopt -s nullglob
for status_file in /sys/class/drm/card*-*/status; do
  [[ -f "$status_file" ]] || continue
  connector=$(basename "$(dirname "$status_file")")
  if [[ "$connector" == *HDMI* || "$connector" == *DSI* || "$connector" == *DP* ]]; then
    if [[ "$(cat "$status_file")" == "connected" ]]; then
      FOUND=true
      break
    fi
  fi
done
shopt -u nullglob

if [[ "$FOUND" == "true" ]]; then
  exit 0
else
  echo "oceano-display-check: no HDMI/DSI display detected — skipping display launch" >&2
  exit 1
fi
SCRIPT
  chmod 0755 "${DISPLAY_CHECK_SCRIPT}"
  log_ok "Display check helper written to ${DISPLAY_CHECK_SCRIPT}"
}

# ─── Write the kiosk launch wrapper ──────────
# Wraps Chromium with the flags needed for a clean kiosk experience.
# Disables password manager, crash dialogs, updates, and GPU sandbox issues
# common on Pi.

write_launch_wrapper() {
  local chromium_bin="$1"
  local web_addr="$2"

  cat > "${WRAPPER_SCRIPT}" <<SCRIPT
#!/usr/bin/env bash
# Oceano Display — Chromium kiosk launcher.
# Called by oceano-display.service after display detection passes.
set -euo pipefail

NOWPLAYING_URL="${web_addr}/nowplaying.html"

# Prevent Chromium from showing the "restore pages?" dialog after an unclean
# shutdown by clearing its lock file before each launch.
CHROME_DATA=\${HOME}/.config/chromium
[[ -d "\${CHROME_DATA}" ]] && rm -f "\${CHROME_DATA}/SingletonLock"

# Set the environment so Chromium can find the display.
# X11: use :0 (set by the display server started ahead of this service).
export DISPLAY=\${DISPLAY:-:0}

exec ${chromium_bin} \\
  --kiosk \\
  --noerrdialogs \\
  --disable-infobars \\
  --no-first-run \\
  --disable-session-crashed-bubble \\
  --disable-features=TranslateUI \\
  --disable-pinch \\
  --overscroll-history-navigation=0 \\
  --check-for-update-interval=315360000 \\
  --disable-background-networking \\
  --disable-sync \\
  --disable-translate \\
  --password-store=basic \\
  --use-mock-keychain \\
  --window-size=1024,600 \\
  "\${NOWPLAYING_URL}"
SCRIPT
  chmod 0755 "${WRAPPER_SCRIPT}"
  log_ok "Kiosk launch wrapper written to ${WRAPPER_SCRIPT}"
}

# ─── Write the systemd service ────────────────

write_service() {
  local user="$1"

  # Resolve the user's home directory safely for the SingletonLock cleanup.
  local passwd_entry
  passwd_entry=$(getent passwd -- "${user}" || true)
  if [[ -z "${passwd_entry}" ]]; then
    log_error "User '${user}' does not exist"
    exit 1
  fi

  local user_home
  user_home=$(printf '%s\n' "${passwd_entry}" | cut -d: -f6)
  if [[ -z "${user_home}" ]]; then
    log_error "Could not resolve home directory for user '${user}'"
    exit 1
  fi

  cat > "${SERVICE_DEST}" <<EOF
[Unit]
Description=Oceano Display — Now Playing kiosk (HDMI/DSI)
Documentation=https://github.com/alemser/oceano-player
After=graphical.target oceano-web.service
Wants=oceano-web.service
# Only start when a compatible display is connected.
# ExecStartPre exits non-zero on headless Pi → service is skipped gracefully.
ConditionPathExists=/sys/class/drm

[Service]
Type=simple
User=${user}
Environment=HOME=${user_home}
# Skip cleanly if no display is detected; unlike ExecStartPre, ExecCondition
# does not count a non-zero exit here as a unit failure.
ExecCondition=${DISPLAY_CHECK_SCRIPT}
# Brief pause to let the display server and oceano-web fully initialise.
ExecStartPre=/bin/sleep 4
ExecStart=${WRAPPER_SCRIPT}
Restart=on-failure
RestartSec=8
# On Pi the display may not be ready immediately after graphical.target —
# increase the start timeout to avoid premature failure.
TimeoutStartSec=30

[Install]
WantedBy=graphical.target
EOF

  log_ok "Service file written to ${SERVICE_DEST}"
}

# ─── X server / compositor note ──────────────
# The service depends on a running X server or Wayland compositor to provide
# the display (:0).  On Raspberry Pi OS Desktop this is handled automatically
# by the graphical.target.  On Raspberry Pi OS Lite you must set up a minimal
# X session first.  See README.md for details.

print_compositor_note() {
  log_warn "If you are running Raspberry Pi OS Lite (no desktop), you must"
  log_warn "also configure a minimal X session before the kiosk can start."
  log_warn "Run the following to install the minimum required packages:"
  echo ""
  echo "    sudo apt-get install -y xorg openbox"
  echo ""
  log_warn "Then create /home/${1}/.xinitrc containing:"
  echo ""
  echo "    #!/bin/sh"
  echo "    exec ${WRAPPER_SCRIPT}"
  echo ""
  log_warn "And enable auto-login for the user '${1}' via raspi-config."
}

# ─── Main ─────────────────────────────────────

main() {
  if ! is_root; then
    log_error "Please run as root: sudo ./install-oceano-display.sh"
    exit 1
  fi

  local web_addr="${DEFAULT_WEB_ADDR}"
  local display_user="${DEFAULT_DISPLAY_USER}"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --web-addr)     web_addr="${2:-}";     shift 2 ;;
      --user)         display_user="${2:-}"; shift 2 ;;
      -h|--help)
        echo "Usage: sudo ./install-oceano-display.sh [options]"
        echo ""
        echo "Installs the Oceano now-playing kiosk service for HDMI/DSI displays."
        echo ""
        echo "Options:"
        echo "  --web-addr <url>    Base URL of oceano-web (default: ${DEFAULT_WEB_ADDR})"
        echo "  --user <name>       Linux user to run Chromium as (default: ${DEFAULT_DISPLAY_USER})"
        echo ""
        echo "The service starts Chromium in kiosk mode at:"
        echo "  <web-addr>/nowplaying.html"
        echo ""
        echo "Display detection: the service checks /sys/class/drm for a connected"
        echo "HDMI or DSI panel before starting Chromium. On a headless Pi (no display)"
        echo "the service exits cleanly without error."
        exit 0
        ;;
      *) log_error "Unknown argument: $1"; exit 1 ;;
    esac
  done

  local mode="INSTALL"
  [[ -f "${SERVICE_DEST}" ]] && mode="UPDATE"

  echo -e "\n${BOLD}╔══════════════════════════════════════╗"
  echo -e "║   Oceano Display — ${mode}         ║"
  echo -e "╚══════════════════════════════════════╝${RESET}"

  log_section "Chromium"
  local chromium_bin
  chromium_bin=$(find_chromium)
  if [[ -z "${chromium_bin}" ]]; then
    log_warn "Chromium not found. Installing chromium-browser..."
    apt-get install -y chromium-browser
    chromium_bin=$(find_chromium)
    if [[ -z "${chromium_bin}" ]]; then
      log_error "Unable to install or locate Chromium. Aborting."
      exit 1
    fi
  fi
  log_ok "Chromium binary: $(command -v "${chromium_bin}")"

  log_section "Display Check Helper"
  write_display_check

  log_section "Kiosk Launch Wrapper"
  write_launch_wrapper "${chromium_bin}" "${web_addr}"

  log_section "systemd Service"
  write_service "${display_user}"
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}"

  # Only start immediately if a display is currently connected.
  if ${DISPLAY_CHECK_SCRIPT} 2>/dev/null; then
    systemctl restart "${SERVICE_NAME}"
    log_ok "${SERVICE_NAME} started."
  else
    log_warn "No display detected right now — service is enabled and will start on next boot"
    log_warn "when a compatible HDMI/DSI screen is connected."
  fi

  log_section "Done"
  log_ok "${mode} completed successfully!"
  echo ""
  echo -e "The now-playing UI is served at: ${BOLD}${web_addr}/nowplaying.html${RESET}"
  echo -e "Monitor logs with: ${BOLD}journalctl -u ${SERVICE_NAME} -f${RESET}"
  echo ""

  # Raspberry Pi OS Lite needs extra setup.
  if ! dpkg -l lightdm xfce4 \* 2>/dev/null | grep -q "^ii"; then
    if ! dpkg -l labwc wayfire openbox 2>/dev/null | grep -q "^ii"; then
      print_compositor_note "${display_user}"
    fi
  fi
}

main "$@"
