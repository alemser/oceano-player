#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Exercises the explicit provider list path (non-empty recognition.providers + merge_policy),
# matching how the iOS app persists recognition configuration — not legacy recognizer_chain-only mode.
#
# For each fixture: writes config, restarts oceano-state-manager, sends SIGUSR1 to force a
# recognition attempt, greps recent logs for the providers-based recognizer startup line.
#
# Does not start loopback audio — pair with pi-loopback-capture-sim for PCM-driven runs.
#
# Policy: run after any change to explicit provider list wiring — see
# .cursor/skills/pi-recognition-explicit-providers-smoke/SKILL.md and docs/reference/recognition.md
#
# Usage (on the Pi, with sudo):
#   sudo OCEANO_CONFIG=/etc/oceano/config.json ./scripts/pi-recognition-provider-smoke.sh
#   sudo ./scripts/pi-recognition-provider-smoke.sh --dry-run
set -euo pipefail

CFG="${OCEANO_CONFIG:-/etc/oceano/config.json}"
DRY_RUN=0

usage() {
  cat <<'EOF'
Usage: pi-recognition-provider-smoke.sh [--dry-run] [--config PATH]

Requires: jq, systemd, oceano-state-manager.

This script only tests the explicit provider list path: recognition.providers[] + merge_policy (iOS-style).

Options:
  --dry-run       Print jq/systemctl actions only
  --config PATH   Config JSON (default: /etc/oceano/config.json or OCEANO_CONFIG)
  -h, --help      This help

Environment:
  OCEANO_CONFIG   Same as --config

Safety:
  Backs up the config once to <config>.bak.provider-smoke.<pid> and restores it
  on exit (trap), unless --dry-run.

Note:
  If credentials or Shazamio Python are missing on the Pi, some providers will be
  skipped at runtime — logs still show whether the explicit-provider loader path is active.
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

# Fixtures: JSON arrays for recognition.providers. Wire id "shazam" = Shazamio on Pi.
FIX_ACR_ONLY=$(jq -c -n '[{id:"acrcloud",enabled:true,roles:["primary"]}]')
FIX_THREE_PRIMARY=$(jq -c -n '[{id:"acrcloud",enabled:true,roles:["primary"]},{id:"audd",enabled:true,roles:["primary"]},{id:"shazam",enabled:true,roles:["primary"]}]')
FIX_ACR_PRIMARY_AUDD_CONF=$(jq -c -n '[{id:"acrcloud",enabled:true,roles:["primary"]},{id:"audd",enabled:true,roles:["confirmer"]}]')

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

apply_explicit_provider_fixture() {
  local label="$1"
  local providers_json="$2"
  local tmp
  tmp="$(mktemp)"
  jq --argjson p "$providers_json" \
    '.recognition.providers = $p
     | .recognition.merge_policy = "first_success"
     | .recognition.recognizer_chain = (.recognition.recognizer_chain // "acrcloud_first")' \
    "$CFG" >"$tmp"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "Would apply explicit-provider fixture $label: $providers_json"
    rm -f "$tmp"
    return 0
  fi

  cp "$tmp" "$CFG"
  rm -f "$tmp"
  systemctl restart oceano-state-manager.service
  sleep 2
  systemctl kill --kill-who=main --signal=SIGUSR1 oceano-state-manager.service || true
  sleep 2

  echo "--- journalctl after: $label ---"
  journalctl -u oceano-state-manager.service -n 80 --no-pager \
    | grep -E "recognizer: (using recognition\.providers|provider id=|ACRCloud enabled|AudD enabled|Shazamio enabled|resolved to no available primary|chain=)" \
    || echo "(no matching lines in last 80 — try: journalctl -u oceano-state-manager -f)"
}

echo "=== Explicit recognition.providers smoke (iOS-style path) ==="
apply_explicit_provider_fixture "acrcloud_primary_only" "$FIX_ACR_ONLY"
apply_explicit_provider_fixture "three_primary_acr_aud_shazam" "$FIX_THREE_PRIMARY"
apply_explicit_provider_fixture "acr_primary_aud_confirmer" "$FIX_ACR_PRIMARY_AUDD_CONF"

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "Dry run complete."
  exit 0
fi

echo "Done. Config restored on exit."
