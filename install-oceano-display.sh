#!/usr/bin/env bash
set -euo pipefail

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Oceano Display — Kiosk Setup"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    echo "Please run as root: sudo ./install-oceano-display.sh"
    exit 1
fi

if [ -f /usr/local/bin/oceano-display-check ]; then
    echo "Kiosk already installed. To reinstall, remove the display scripts first:"
    echo "  sudo rm -f /usr/local/bin/oceano-display-check /usr/local/bin/oceano-display-launch"
    echo "  sudo ./install-oceano-display.sh"
    exit 0
fi

if [ ! -d /sys/class/drm ]; then
    echo "No display subsystem found. This script is for Raspberry Pi."
    exit 1
fi

# Check if display is connected
HAS_DISPLAY=false
for status_file in /sys/class/drm/card*/status; do
    if [ -f "$status_file" ] && [ "$(cat "$status_file")" = "connected" ]; then
        HAS_DISPLAY=true
        break
    fi
done

if [ "$HAS_DISPLAY" = "false" ]; then
    echo "No HDMI/DSI display detected. Connect a display and run this again."
    exit 1
fi

echo "A display has been detected."
echo ""
read -p "Install kiosk display? [Y/n]: " ASK
ASK="${ASK:-Y}"

if [[ "$ASK" =~ ^[Nn] ]]; then
    echo "Cancelled."
    exit 0
fi

echo ""
read -p "User to run kiosk (needs sudo for autologin) [$(whoami)]: " KIOSK_USER
KIOSK_USER="${KIOSK_USER:-$(whoami)}"

if ! id "$KIOSK_USER" >/dev/null 2>&1; then
    echo "User '$KIOSK_USER' not found."
    exit 1
fi

echo ""
echo "Installing X server, virtual framebuffer, and Chromium (with full dependencies)..."
export DEBIAN_FRONTEND=noninteractive
# Do not use "|| true" here — silent apt failures left Chromium half-installed and caused missing .so errors.
apt-get update -qq || true
apt-get install -y --no-install-recommends \
  xserver-xorg-core xserver-xorg xinit xvfb x11-utils xauth \
  || { echo "ERROR: could not install X / Xvfb packages." >&2; exit 1; }

if ! command -v chromium >/dev/null 2>&1 && ! [ -f /usr/lib/chromium/chromium ]; then
  # Install Chromium *with* Recommends so fonts and GTK/GBM stacks are present (avoids missing-library kiosk crashes).
  apt-get install -y chromium \
    || apt-get install -y chromium-browser \
    || { echo "ERROR: could not install Chromium from apt." >&2; exit 1; }
fi

HOME_DIR=$(getent passwd "$KIOSK_USER" | cut -d: -f6)

# Create display check helper
cat > /usr/local/bin/oceano-display-check <<'CHECKEOF'
#!/bin/bash
set -e
FOUND=false
shopt -s nullglob
for status_file in /sys/class/drm/card*/status; do
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
[[ "$FOUND" == "true" ]] && exit 0 || exit 1
CHECKEOF
chmod 0755 /usr/local/bin/oceano-display-check

# Find chromium binary (prefer real binary over wrapper)
CHROMIUM_BIN=""
if [ -f /usr/lib/chromium/chromium ]; then
    CHROMIUM_BIN=/usr/lib/chromium/chromium
elif command -v chromium >/dev/null 2>&1; then
    CHROMIUM_BIN=$(command -v chromium)
fi

if [ -z "$CHROMIUM_BIN" ]; then
    echo "Chromium not found."
    exit 1
fi

# Create kiosk launch wrapper
cat > /usr/local/bin/oceano-display-launch <<LAUNCHEOF
#!/bin/bash
set -e
NOWPLAYING_URL="http://localhost:8080/nowplaying.html"
CHROME_DATA=\${HOME}/.config/chromium
[[ -d "\${CHROME_DATA}" ]] && rm -f "\${CHROME_DATA}/SingletonLock"

cleanup() { [[ -n "\${XVFB_PID:-}" ]] && kill "\${XVFB_PID}" 2>/dev/null; }
trap cleanup EXIT

Xvfb :99 -screen 0 1024x600x24 -nolisten tcp &
XVFB_PID=\$!
export DISPLAY=:99
sleep 2

exec ${CHROMIUM_BIN} \
  --kiosk \
  --no-sandbox \
  --disable-dev-shm-usage \
  --noerrdialogs \
  --disable-infobars \
  --no-first-run \
  --disable-session-crashed-bubble \
  --disable-features=TranslateUI \
  --check-for-update-interval=315360000 \
  --disable-background-networking \
  --disable-sync \
  --password-store=basic \
  --use-mock-keychain \
  --window-size=1024,600 \
  --hide-cursor \
  --app="\${NOWPLAYING_URL}"
LAUNCHEOF
chmod 0755 /usr/local/bin/oceano-display-launch

# Create xinitrc
cat > "${HOME_DIR}/.xinitrc" <<XINITEOF
#!/bin/sh
exec /usr/local/bin/oceano-display-launch
XINITEOF
chown "$KIOSK_USER:$KIOSK_USER" "${HOME_DIR}/.xinitrc"
chmod 0755 "${HOME_DIR}/.xinitrc"

# X session desktop (used if the user starts a graphical session manually)
mkdir -p /usr/share/xsessions
cat > /usr/share/xsessions/oceano-kiosk.desktop <<DESKTOPEOF
[Desktop Entry]
Name=Oceano Kiosk
Comment=Oceano Now Playing Display
Exec=/usr/local/bin/oceano-display-launch
Type=Application
DESKTOPEOF

# LightDM autologin only when LightDM is actually installed (many Lite images have no display manager).
if command -v lightdm >/dev/null 2>&1; then
  mkdir -p /etc/lightdm/lightdm.conf.d
  cat > /etc/lightdm/lightdm.conf.d/oceano-kiosk.conf <<AUTOLOGINEOF
[Seat:*]
autologin-user=${KIOSK_USER}
autologin-user-timeout=0
autologin-session=oceano-kiosk
user-session=oceano-kiosk
AUTOLOGINEOF
  cat > "${HOME_DIR}/.dmrc" <<DMRCEOF
[Desktop]
Session=oceano-kiosk
DMRCEOF
  chown "$KIOSK_USER:$KIOSK_USER" "${HOME_DIR}/.dmrc"
  chmod 0644 "${HOME_DIR}/.dmrc"
  echo "LightDM autologin configured for user ${KIOSK_USER} (reboot to apply if you use a graphical boot)."
else
  echo "LightDM not installed — kiosk runs via systemd + Xvfb only. To use LightDM autologin: sudo apt install lightdm then re-run this script or copy conf from the README."
fi

# Create systemd service
cat > /etc/systemd/system/oceano-display.service <<SVCEOF
[Unit]
Description=Oceano Display — Now Playing kiosk (HDMI/DSI)
After=network.target oceano-web.service
Wants=oceano-web.service
ConditionPathExists=/sys/class/drm

[Service]
Type=simple
User=${KIOSK_USER}
ExecCondition=/usr/local/bin/oceano-display-check
ExecStartPre=/bin/sleep 2
ExecStart=/usr/local/bin/oceano-display-launch
Restart=on-failure
RestartSec=5
TimeoutStartSec=30

[Install]
WantedBy=multi-user.target
SVCEOF
chmod 0644 /etc/systemd/system/oceano-display.service
systemctl daemon-reload
systemctl enable oceano-display.service
# Start immediately so the kiosk appears without an extra reboot (ExecCondition still requires a connected panel).
if systemctl start oceano-display.service 2>/dev/null; then
  echo "Started oceano-display.service now."
else
  echo "Note: oceano-display.service did not start (e.g. display disconnected). It will start on next boot if a panel is connected."
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Kiosk Setup Complete"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "The service is enabled. If the screen did not turn on, reboot once: sudo reboot"
echo ""
echo "Monitor logs: journalctl -u oceano-display.service -f"