#!/usr/bin/env bash
set -euo pipefail

ALSA_DEVICE="plughw:CARD=Microphone,DEV=0"
SAMPLE_RATE=44100
BUFFER_SIZE=8192
OUTPUT_DIR="./calibration-data"
LABEL=""
DURATION=300

while [[ $# -gt 0 ]]; do
  case "$1" in
    --label)    LABEL="${2:-}";       shift 2 ;;
    --device)   ALSA_DEVICE="${2:-}"; shift 2 ;;
    --duration) DURATION="${2:-}";    shift 2 ;;
    --output)   OUTPUT_DIR="${2:-}";  shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

if [[ -z "${LABEL}" ]]; then
  echo "Usage: ./capture-session.sh --label <cd|vinyl|silence> [--duration <sec>]" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ANALYSER="${SCRIPT_DIR}/capture-analyse.py"

mkdir -p "${OUTPUT_DIR}"
TIMESTAMP="$(date '+%Y%m%d_%H%M%S')"
CSV_FILE="${OUTPUT_DIR}/${TIMESTAMP}_${LABEL}.csv"
TOTAL_WINDOWS=$(( DURATION * SAMPLE_RATE / BUFFER_SIZE ))

echo "Label:   ${LABEL}"
echo "Device:  ${ALSA_DEVICE}"
echo "Duration:${DURATION}s  (~${TOTAL_WINDOWS} windows)"
echo "Output:  ${CSV_FILE}"
echo ""
echo "Starting in 3s..."
sleep 3

arecord \
  -D "${ALSA_DEVICE}" \
  -f S16_LE \
  -r "${SAMPLE_RATE}" \
  -c 2 \
  -t raw \
  --duration="${DURATION}" \
  2>/dev/null \
| python3 "${ANALYSER}" "${CSV_FILE}" "${LABEL}" "${BUFFER_SIZE}" "${SAMPLE_RATE}" "${TOTAL_WINDOWS}"

echo ""
echo "Done: ${CSV_FILE}"
