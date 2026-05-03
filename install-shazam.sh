#!/usr/bin/env bash
# install-shazam.sh — sets up the shazamio Python virtualenv used by
# oceano-state-manager as a fallback recognizer when ACRCloud returns no match.
#
# shazamio is a community library, not an official Shazam/Apple API product.
# Commercial deployments: see docs/plans/recognition-master-plan.md (Third-party clarity: shazamio).
# ("Third-party clarity: shazamio").
#
# Usage:
#   sudo ./install-shazam.sh [--venv /opt/shazam-env]
#
# After running, restart oceano-state-manager:
#   sudo systemctl restart oceano-state-manager.service
set -euo pipefail

VENV_DIR="/opt/shazam-env"

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --venv) VENV_DIR="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,7p' "$0" | sed 's/^# //'
            exit 0 ;;
        *) echo "[ERROR] Unknown argument: $1" >&2; exit 1 ;;
    esac
done

PYTHON_BIN="$VENV_DIR/bin/python"

# ── System dependencies ───────────────────────────────────────────────────────
echo "━━━ System packages ━━━"
apt-get install -y --no-install-recommends \
    python3 python3-venv python3-dev \
    ffmpeg socat \
    libffi-dev libssl-dev
echo "[OK]    System packages installed."

# ── Virtualenv ────────────────────────────────────────────────────────────────
echo ""
echo "━━━ Python virtualenv ━━━"
if [[ ! -d "$VENV_DIR" ]]; then
    python3 -m venv "$VENV_DIR"
    echo "[OK]    Virtualenv created at $VENV_DIR"
else
    echo "[INFO]  Virtualenv already exists at $VENV_DIR"
fi

"$VENV_DIR/bin/pip" install --upgrade pip --quiet

# audioop-lts is required on Python 3.13+ (audioop was removed from stdlib).
# Install it unconditionally — it's a no-op on older Pythons.
echo "[INFO]  Installing shazamio and dependencies ..."
"$VENV_DIR/bin/pip" install --quiet \
    shazamio \
    audioop-lts

echo "[OK]    shazamio installed."

# ── Smoke test ────────────────────────────────────────────────────────────────
echo ""
echo "━━━ Smoke test ━━━"
if "$PYTHON_BIN" -c "import shazamio; print('[OK]    shazamio import OK')"; then
    :
else
    echo "[ERROR] shazamio import failed — check the output above." >&2
    exit 1
fi

# ── Update config ─────────────────────────────────────────────────────────────
CONFIG="/etc/oceano/config.json"
if [[ -f "$CONFIG" ]]; then
    echo ""
    echo "━━━ Config ━━━"
    # Add or update shazam_python field in config.json.
    TMPCONFIG="$(mktemp)"
    python3 - "$CONFIG" "$PYTHON_BIN" "$TMPCONFIG" << 'PYEOF'
import sys, json
path, python_bin, out = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path) as f:
    cfg = json.load(f)
rec = cfg.setdefault("recognition", {})
rec["shazam_recognizer_enabled"] = True
rec.pop("shazam_python_bin", None)
cfg.pop("shazam_python", None)
with open(out, "w") as f:
    json.dump(cfg, f, indent=2)
    f.write("\n")
PYEOF
    mv "$TMPCONFIG" "$CONFIG"
    echo "[OK]    Set recognition.shazam_recognizer_enabled=true in $CONFIG (bundled path: $PYTHON_BIN)"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo "━━━ Done ━━━"
echo "[OK]    Shazamio (community client) fallback ready."
echo "        Python: $PYTHON_BIN"
echo ""
echo "Restart the state manager to activate:"
echo "  sudo systemctl restart oceano-state-manager.service"
