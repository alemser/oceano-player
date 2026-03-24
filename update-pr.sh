#!/usr/bin/env bash
set -euo pipefail

SRC_DIR="/opt/oceano-player/src"
DEFAULT_BRANCH="main"
DEFAULT_REMOTE="origin"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

is_root() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]]
}

usage() {
  cat <<'EOF'
Usage: sudo ./update-pr.sh [options]

Options:
  --branch <name>                 Git branch to deploy (default: main)
  --remote <name>                 Git remote to fetch from (default: origin)
  --airplay-name <name>           Passed to update.sh
  --usb-match <text>              Passed to update.sh
  --alsa-device <device>          Passed to update.sh
  --preplay-wait-seconds <0-60>   Passed to update.sh
  --output-strategy <mode>        Passed to update.sh (direct|loopback)
  -h, --help                      Show this help

Examples:
  sudo ./update-pr.sh --branch deadling-with-disconection
  sudo ./update-pr.sh --branch deadling-with-disconection --output-strategy loopback --preplay-wait-seconds 8
EOF
}

main() {
  if ! is_root; then
    echo "Please run as root (use sudo): sudo ./update-pr.sh" >&2
    exit 1
  fi

  require_cmd git
  require_cmd systemctl

  local branch="${DEFAULT_BRANCH}"
  local remote="${DEFAULT_REMOTE}"
  local update_args=()

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --branch)
        branch="${2:-}"
        shift 2
        ;;
      --remote)
        remote="${2:-}"
        shift 2
        ;;
      --airplay-name|--usb-match|--alsa-device|--preplay-wait-seconds|--output-strategy)
        update_args+=("$1" "${2:-}")
        shift 2
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        echo "Unknown argument: $1" >&2
        usage >&2
        exit 1
        ;;
    esac
  done

  if [[ -z "${branch}" ]]; then
    echo "--branch cannot be empty" >&2
    exit 1
  fi
  if [[ -z "${remote}" ]]; then
    echo "--remote cannot be empty" >&2
    exit 1
  fi

  if [[ ! -d "${SRC_DIR}/.git" ]]; then
    echo "Repository not found at ${SRC_DIR}." >&2
    echo "Run install first: sudo ./install.sh" >&2
    exit 1
  fi

  echo "Updating source from ${remote}/${branch} in ${SRC_DIR}..."
  git -C "${SRC_DIR}" fetch --prune "${remote}"
  git -C "${SRC_DIR}" checkout "${branch}"
  git -C "${SRC_DIR}" pull --ff-only "${remote}" "${branch}"

  echo "Applying service update..."
  (cd "${SRC_DIR}" && ./update.sh "${update_args[@]}")

  echo
  echo "Done. Service quick checks:"
  systemctl --no-pager --full status shairport-sync.service | head -n 12 || true
  systemctl --no-pager --full status oceano-airplay-bridge.service | head -n 12 || true
  echo
  echo "Recent logs (last 20 lines):"
  journalctl -u shairport-sync.service -n 20 --no-pager || true
}

main "$@"
