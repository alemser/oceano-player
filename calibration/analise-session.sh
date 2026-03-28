#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────
#  Oceano Source Detector — Session Analyser
#
#  Reads all CSV files in ./calibration-data,
#  prints per-label statistics, and suggests
#  threshold values for install-source-detector.sh.
#
#  Usage:
#    ./analyse-session.sh
#    ./analyse-session.sh --dir ./calibration-data
# ─────────────────────────────────────────────

DATA_DIR="./calibration-data"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir) DATA_DIR="${2:-}"; shift 2 ;;
    -h|--help)
      echo "Usage: ./analyse-session.sh [--dir <path>]"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

if [[ ! -d "${DATA_DIR}" ]]; then
  echo "Data directory not found: ${DATA_DIR}" >&2
  echo "Run capture-session.sh first." >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ANALYSER="${SCRIPT_DIR}/analyse-session.py"

if [[ ! -f "${ANALYSER}" ]]; then
  echo "analyse-session.py not found at ${ANALYSER}" >&2
  echo "Make sure analyse-session.py is in the same directory as this script." >&2
  exit 1
fi

python3 "${ANALYSER}" "${DATA_DIR}"