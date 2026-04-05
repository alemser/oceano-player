#!/usr/bin/env bash
# test-acoustid.sh — capture audio from the PCM socket and query AcoustID.
# Usage: ./scripts/test-acoustid.sh [--duration 20] [--pcm-socket /tmp/oceano-pcm.sock]
#
# Requirements:
#   fpcalc (libchromaprint-tools), ffmpeg, curl, jq
#   oceano-source-detector must be running (provides the PCM socket)
set -euo pipefail

# ── Defaults ─────────────────────────────────────────────────────────────────
ACOUSTID_CLIENT="0cAcPUvHVU"
PCM_SOCKET="/tmp/oceano-pcm.sock"
DURATION=20          # seconds to capture — longer = better accuracy
SAMPLE_RATE=44100
CHANNELS=2
BYTES_PER_SAMPLE=2   # S16_LE

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --duration)   DURATION="$2";    shift 2 ;;
        --pcm-socket) PCM_SOCKET="$2";  shift 2 ;;
        --client)     ACOUSTID_CLIENT="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,5p' "$0" | sed 's/^# //'
            exit 0 ;;
        *) echo "[ERROR] Unknown argument: $1" >&2; exit 1 ;;
    esac
done

# ── Dependency checks ─────────────────────────────────────────────────────────
for cmd in fpcalc ffmpeg curl jq; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "[ERROR] $cmd not found. Install it first." >&2
        exit 1
    fi
done

if [[ ! -S "$PCM_SOCKET" ]]; then
    echo "[ERROR] PCM socket not found: $PCM_SOCKET" >&2
    echo "        Is oceano-source-detector running?" >&2
    exit 1
fi

# ── Capture ───────────────────────────────────────────────────────────────────
TOTAL_BYTES=$(( DURATION * SAMPLE_RATE * CHANNELS * BYTES_PER_SAMPLE ))
WAV_FILE="$(mktemp /tmp/acoustid-test-XXXXXX.wav)"
trap 'rm -f "$WAV_FILE"' EXIT

echo "[INFO]  Capturing ${DURATION}s of PCM from $PCM_SOCKET ..."

# Read raw PCM from socket, then convert to WAV via ffmpeg.
# nc (netcat) reads until it has enough bytes or the socket closes.
RAW_FILE="$(mktemp /tmp/acoustid-raw-XXXXXX.pcm)"
trap 'rm -f "$WAV_FILE" "$RAW_FILE"' EXIT

# Use dd to read exactly TOTAL_BYTES from the socket.
# The socket stays open, so we pipe through head -c to limit the read.
socat -u "UNIX-CONNECT:$PCM_SOCKET" STDOUT 2>/dev/null | \
    head -c "$TOTAL_BYTES" > "$RAW_FILE" &
SOCAT_PID=$!

# Wait until we have enough bytes (with timeout).
WAITED=0
while [[ $(stat -c%s "$RAW_FILE" 2>/dev/null || echo 0) -lt "$TOTAL_BYTES" ]]; do
    sleep 1
    WAITED=$(( WAITED + 1 ))
    if [[ $WAITED -gt $(( DURATION + 10 )) ]]; then
        kill "$SOCAT_PID" 2>/dev/null || true
        echo "[ERROR] Timed out waiting for PCM data." >&2
        exit 1
    fi
    echo "[INFO]  Received $(stat -c%s "$RAW_FILE" 2>/dev/null || echo 0) / $TOTAL_BYTES bytes ..."
done
kill "$SOCAT_PID" 2>/dev/null || true

echo "[INFO]  Converting raw PCM to WAV ..."
ffmpeg -loglevel error \
    -f s16le -ar "$SAMPLE_RATE" -ac "$CHANNELS" -i "$RAW_FILE" \
    "$WAV_FILE"

# ── Fingerprint ───────────────────────────────────────────────────────────────
echo "[INFO]  Generating Chromaprint fingerprint (fpcalc, duration=${DURATION}s) ..."
FPCALC_OUT="$(fpcalc -length "$DURATION" "$WAV_FILE")"

FP_DURATION="$(echo "$FPCALC_OUT" | grep '^DURATION=' | cut -d= -f2)"
FP_DATA="$(echo "$FPCALC_OUT" | grep '^FINGERPRINT=' | cut -d= -f2)"

if [[ -z "$FP_DATA" ]]; then
    echo "[ERROR] fpcalc did not produce a fingerprint." >&2
    echo "        fpcalc output:" >&2
    echo "$FPCALC_OUT" >&2
    exit 1
fi

echo "[INFO]  Duration: ${FP_DURATION}s"
echo "[INFO]  Fingerprint: ${FP_DATA:0:60}..."

# ── AcoustID lookup ───────────────────────────────────────────────────────────
echo "[INFO]  Querying AcoustID ..."
RESPONSE="$(curl -s \
    "https://api.acoustid.org/v2/lookup" \
    --data-urlencode "client=${ACOUSTID_CLIENT}" \
    --data-urlencode "duration=${FP_DURATION}" \
    --data-urlencode "fingerprint=${FP_DATA}" \
    --data-urlencode "meta=recordings+releasegroups+releases+tracks")"

echo ""
echo "── Raw AcoustID response ────────────────────────────────────────────────"
echo "$RESPONSE" | jq .
echo ""

STATUS="$(echo "$RESPONSE" | jq -r '.status')"
if [[ "$STATUS" != "ok" ]]; then
    echo "[ERROR] AcoustID returned status: $STATUS" >&2
    exit 1
fi

RESULT_COUNT="$(echo "$RESPONSE" | jq '.results | length')"
if [[ "$RESULT_COUNT" -eq 0 ]]; then
    echo "[RESULT] No match found."
    exit 0
fi

echo "── Best match ───────────────────────────────────────────────────────────"
echo "$RESPONSE" | jq -r '
    .results[0] |
    "Score:   \(.score)",
    "ID:      \(.id)",
    (
        if (.recordings | length) > 0 then
            .recordings[0] |
            "Title:   \(.title // "unknown")",
            "Artist:  \((.artists // [{}])[0].name // "unknown")",
            (
                if (.releasegroups | length) > 0 then
                    .releasegroups[0] |
                    "Album:   \(.title // "unknown")",
                    "Type:    \(.type // "unknown")"
                else "Album:   (no release group)"
                end
            )
        else "Recording: (no recording metadata)"
        end
    )
'
