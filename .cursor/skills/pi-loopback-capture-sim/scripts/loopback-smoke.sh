#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Prints ALSA loopback hints and example aplay/arecord commands for Oceano capture simulation.
# Does not modify config.json or systemd units.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: loopback-smoke.sh [--wav PATH]

Requires: snd-aloop loaded (sudo modprobe snd-aloop).

This script does not start long-running processes; it only prints device lines
from aplay/arecord and example commands for manual copy-paste.

Options:
  --wav PATH   Optional WAV path to mention in the aplay example (default: /tmp/oceano-loopback-test.wav)
  -h, --help   Show this help
EOF
}

WAV_PATH="/tmp/oceano-loopback-test.wav"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --wav)
      WAV_PATH="${2:?}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if ! lsmod | grep -q '^snd_aloop'; then
  echo "snd-aloop is not loaded. Run: sudo modprobe snd-aloop" >&2
  exit 1
fi

echo "=== aplay -l (look for 'Loopback') ==="
aplay -l || true

echo
echo "=== arecord -l (look for 'Loopback') ==="
arecord -l || true

echo
echo "=== Example (replace CARD with the loopback card index) ==="
cat <<EOF
# 1) Convert any audio to the format Oceano expects (run on Mac or Pi):
#    ffmpeg -y -i input.wav -ac 2 -ar 44100 -sample_fmt s16 ${WAV_PATH}

# 2) Play into loopback playback (CARD = loopback card number from aplay -l):
while true; do aplay -D plughw:CARD,0,0 -c 2 -r 44100 -f S16_LE ${WAV_PATH}; done

# 3) In oceano-web -> Audio Input, set capture to loopback CAPTURE, e.g.:
#    plughw:CARD,1,0
#    Then Save & Restart Services.

# 4) Watch detector:
#    journalctl -u oceano-source-detector.service -f
EOF
