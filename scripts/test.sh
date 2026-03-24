#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

echo "[1/3] Shell syntax checks"
bash -n install.sh
bash -n update.sh

echo "[2/3] Go checks"
go test ./...

echo "[3/3] Python tests (spi-now-playing)"
if [[ -x "${ROOT_DIR}/spi-now-playing/.venv/bin/python" ]]; then
  "${ROOT_DIR}/spi-now-playing/.venv/bin/python" -m pytest "${ROOT_DIR}/spi-now-playing/tests" -q
else
  python3 -m pytest "${ROOT_DIR}/spi-now-playing/tests" -q
fi

echo "All checks passed."
