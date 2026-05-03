#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Cycles recognizer_chain on the Pi (legacy path: no recognition.providers array),
# restarts oceano-state-manager, sends SIGUSR1 to force a recognition attempt,
# and greps recent logs for recognizer startup lines.
#
# Does not start loopback audio — pair with pi-loopback-capture-sim for full capture.
#
# Usage (on the Pi, with sudo):
#   sudo OCEANO_CONFIG=/etc/oceano/config.json ./scripts/pi-recognition-provider-smoke.sh
#   sudo ./scripts/pi-recognition-provider-smoke.sh --dry-run
set -euo pipefail

CFG="${OCEANO_CONFIG:-/etc/oceano/config.json}"
DRY_RUN=0
CHAINS=(acrcloud_first shazam_first audd_first acrcloud_only shazam_only audd_only)

usage() {
  cat <<'EOF'
Usage: pi-recognition-provider-smoke.sh [--dry-run] [--config PATH]

Requires: jq, systemd, oceano-state-manager.

Options:
  --dry-run       Print jq/systemctl commands only
  --config PATH   Config JSON (default: /etc/oceano/config.json or OCEANO_CONFIG)
  -h, --help      This help

Environment:
  OCEANO_CONFIG   Same as --config

Safety:
  Backs up the config once to <config>.bak.provider-smoke.<pid> and restores it
  on exit (trap), unless --dry-run.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=1; shift ;;
    --config) CFG="${2:?}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required (sudo apt install -y jq)" >&2
  exit 1
fi

BACKUP="${CFG}.bak.provider-smoke.$$"
restore() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    return 0
  fi
  if [[ -f "$BACKUP" ]]; then
    cp -a "$BACKUP" "$CFG"
    systemctl restart oceano-state-manager.service || true
    rm -f "$BACKUP"
    echo "Restored $CFG from backup."
  fi
}
trap restore EXIT INT TERM

if [[ "$DRY_RUN" -eq 0 ]]; then
  if [[ ! -r "$CFG" ]]; then
    echo "Cannot read $CFG (try sudo)" >&2
    exit 1
  fi
  cp -a "$CFG" "$BACKUP"
  echo "Backup: $BACKUP"
fi

apply_chain() {
  local chain="$1"
  local tmp
  tmp="$(mktemp)"
  # Legacy chain path: remove B0 providers so state-manager uses recognizer_chain.
  jq --arg c "$chain" \
    '.recognition.recognizer_chain = $c
     | del(.recognition.providers)
     | del(.recognition.merge_policy)' \
    "$CFG" >"$tmp"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "Would: cp $tmp -> $CFG && systemctl restart oceano-state-manager (chain=$chain)"
    rm -f "$tmp"
    return 0
  fi

  cp "$tmp" "$CFG"
  rm -f "$tmp"
  systemctl restart oceano-state-manager.service
  sleep 2
  systemctl kill --kill-who=main --signal=SIGUSR1 oceano-state-manager.service || true
  sleep 2
}

echo "=== Provider chain smoke (legacy recognizer_chain) ==="
for chain in "${CHAINS[@]}"; do
  echo "--- Chain: $chain ---"
  apply_chain "$chain"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    continue
  fi
  journalctl -u oceano-state-manager.service -n 80 --no-pager \
    | grep -E "recognizer: (chain policy=|using recognition\.providers|ACRCloud enabled|AudD enabled|Shazamio enabled|chain=)" \
    || echo "(no matching recognizer lines in last 80 log lines — check full journalctl)"
done

echo "Done. Config restored on exit."
