#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

echo "[1/3] Shell syntax checks"
bash -n install.sh
bash -n update.sh

echo "[2/3] Go checks"
go test ./...

echo "All checks passed."
