#!/usr/bin/env bash
set -euo pipefail

APP_NAME="oceano-player"
INSTALL_DIR="/opt/oceano-player"
SRC_DIR="/opt/oceano-player/src"
SYSTEMD_UNIT_NAME="oceano-player.service"
DEFAULT_REPO_URL="https://github.com/alemser/oceano-player.git"
DEFAULT_BRANCH="main"

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
    echo "Please run as root (use sudo): sudo ./install.sh" >&2
    exit 1
  fi

  local repo_url="${DEFAULT_REPO_URL}"
  local branch="${DEFAULT_BRANCH}"

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
      -h|--help)
        echo "Usage: sudo ./install.sh [--repo <url>] [--branch <name>]" >&2
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

echo "Installing OS dependencies..."
apt-get update -y
apt-get install -y --no-install-recommends \
  ca-certificates \
  git \
  shairport-sync \
  golang-go

  require_cmd go
  require_cmd git

  echo "Preparing directories..."
  mkdir -p "${INSTALL_DIR}/bin" "${INSTALL_DIR}/systemd"

  echo "Cloning/updating source into ${SRC_DIR}..."
  if [[ -d "${SRC_DIR}/.git" ]]; then
    git -C "${SRC_DIR}" fetch --prune
    git -C "${SRC_DIR}" checkout "${branch}"
    git -C "${SRC_DIR}" pull --ff-only
  else
    rm -rf "${SRC_DIR}"
    git clone --branch "${branch}" --depth 1 "${repo_url}" "${SRC_DIR}"
  fi

  echo "Building ${APP_NAME}..."
  (
    cd "${SRC_DIR}"
    go mod tidy
    go build -o "bin/${APP_NAME}" "./cmd/${APP_NAME}"
  )

  echo "Deploying to ${INSTALL_DIR}..."
  install -m 0755 "${SRC_DIR}/bin/${APP_NAME}" "${INSTALL_DIR}/bin/${APP_NAME}"
  install -m 0644 "${SRC_DIR}/systemd/${SYSTEMD_UNIT_NAME}" "${INSTALL_DIR}/systemd/${SYSTEMD_UNIT_NAME}"

  if [[ ! -f "${INSTALL_DIR}/config.yaml" ]]; then
    install -m 0644 "${SRC_DIR}/config.yaml" "${INSTALL_DIR}/config.yaml"
    echo "Installed default config at ${INSTALL_DIR}/config.yaml"
  else
    echo "Keeping existing config at ${INSTALL_DIR}/config.yaml"
  fi

  # Disable distro-provided shairport-sync service to avoid duplicate instances
  # (port 5000 conflict) since oceano-player supervises shairport-sync itself.
  systemctl disable --now shairport-sync.service >/dev/null 2>&1 || true

  echo "Installing systemd unit..."
  install -m 0644 "${INSTALL_DIR}/systemd/${SYSTEMD_UNIT_NAME}" "/etc/systemd/system/${SYSTEMD_UNIT_NAME}"
  systemctl daemon-reload
  systemctl enable --now "${SYSTEMD_UNIT_NAME}"

  echo
  echo "Done."
  echo "- Service status: systemctl status ${SYSTEMD_UNIT_NAME}"
  echo "- Logs: journalctl -u ${SYSTEMD_UNIT_NAME} -f"
  echo "- Edit config: sudo nano ${INSTALL_DIR}/config.yaml"
}

main "$@"

