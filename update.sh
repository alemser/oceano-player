#!/usr/bin/env bash
set -euo pipefail

APP_NAME="oceano-player"
INSTALL_DIR="/opt/oceano-player"
SRC_DIR="/opt/oceano-player/src"
SYSTEMD_UNIT_NAME="oceano-player.service"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

is_root() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]]
}

main() {
  if ! is_root; then
    echo "Please run as root (use sudo): sudo ./update.sh" >&2
    exit 1
  fi

  require_cmd systemctl
  require_cmd go
  require_cmd git

  if [[ ! -d "${SRC_DIR}/.git" ]]; then
    echo "Source repo not found at ${SRC_DIR}." >&2
    echo "Run: sudo ./install.sh  (it will clone the repo and set everything up)" >&2
    exit 1
  fi

  echo "Updating source in ${SRC_DIR}..."
  git -C "${SRC_DIR}" pull --ff-only

  echo "Building ${APP_NAME} from ${SRC_DIR}..."
  (
    cd "${SRC_DIR}"
    go mod tidy
    mkdir -p bin
    go build -o "bin/${APP_NAME}" "./cmd/${APP_NAME}"
  )

  echo "Deploying updated binary and unit..."
  install -m 0755 "${SRC_DIR}/bin/${APP_NAME}" "${INSTALL_DIR}/bin/${APP_NAME}"
  install -m 0644 "${SRC_DIR}/systemd/${SYSTEMD_UNIT_NAME}" "${INSTALL_DIR}/systemd/${SYSTEMD_UNIT_NAME}"
  install -m 0644 "${INSTALL_DIR}/systemd/${SYSTEMD_UNIT_NAME}" "/etc/systemd/system/${SYSTEMD_UNIT_NAME}"
  systemctl daemon-reload

  # Keep distro shairport-sync disabled; oceano-player supervises it.
  systemctl disable --now shairport-sync.service >/dev/null 2>&1 || true

  echo "Restarting service..."
  systemctl restart "${SYSTEMD_UNIT_NAME}"

  echo
  echo "Done."
  echo "- Service status: systemctl status ${SYSTEMD_UNIT_NAME}"
  echo "- Logs: journalctl -u ${SYSTEMD_UNIT_NAME} -f"
  echo "- Config preserved at: ${INSTALL_DIR}/config.yaml"
}

main "$@"

