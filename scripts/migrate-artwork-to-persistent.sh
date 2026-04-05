#!/usr/bin/env bash
set -euo pipefail

# One-off migration helper.
# Migrates artwork files from ephemeral storage (typically /tmp) to
# persistent storage (/var/lib/oceano/artwork), updates config.json,
# and rewrites SQLite artwork_path records.

CONFIG_JSON="/etc/oceano/config.json"
DEFAULT_DB="/var/lib/oceano/library.db"
DEFAULT_TARGET="/var/lib/oceano/artwork"
DEFAULT_SOURCE="/tmp"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
RESET='\033[0m'

log_info()  { echo -e "${CYAN}[INFO]${RESET}  $*"; }
log_ok()    { echo -e "${GREEN}[OK]${RESET}    $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${RESET}  $*"; }
log_error() { echo -e "${RED}[ERROR]${RESET} $*" >&2; }

usage() {
  cat <<EOF
Usage: sudo ./scripts/migrate-artwork-to-persistent.sh [options]

Options:
  --config <path>     Path to config.json (default: ${CONFIG_JSON})
  --db <path>         Path to library.db (default: ${DEFAULT_DB})
  --from <dir>        Source artwork directory (default: config advanced.artwork_dir or ${DEFAULT_SOURCE})
  --to <dir>          Target artwork directory (default: ${DEFAULT_TARGET})
  --dry-run           Print planned actions only (no changes)
  -h, --help          Show this help
EOF
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    log_error "Missing required command: $1"
    exit 1
  }
}

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  log_error "Please run as root (sudo)."
  exit 1
fi

require_cmd python3

config_path="${CONFIG_JSON}"
library_db="${DEFAULT_DB}"
source_dir=""
target_dir="${DEFAULT_TARGET}"
dry_run=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config) config_path="${2:-}"; shift 2 ;;
    --db)     library_db="${2:-}"; shift 2 ;;
    --from)   source_dir="${2:-}"; shift 2 ;;
    --to)     target_dir="${2:-}"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) log_error "Unknown argument: $1"; usage; exit 1 ;;
  esac
done

if [[ -z "${source_dir}" ]]; then
  if [[ -f "${config_path}" ]]; then
    source_dir="$(python3 - "${config_path}" <<'PY'
import json
import sys

cfg_path = sys.argv[1]
try:
    with open(cfg_path, 'r', encoding='utf-8') as f:
        cfg = json.load(f)
    adv = cfg.get('advanced') or {}
    print((adv.get('artwork_dir') or '').strip())
except Exception:
    print('')
PY
)"
  fi
  source_dir="${source_dir:-${DEFAULT_SOURCE}}"
fi

source_dir="${source_dir%/}"
target_dir="${target_dir%/}"

log_info "Config file: ${config_path}"
log_info "Library DB:  ${library_db}"
log_info "Source dir:  ${source_dir}"
log_info "Target dir:  ${target_dir}"

if [[ "${source_dir}" == "${target_dir}" ]]; then
  log_warn "Source and target directories are the same. Files will not be moved."
fi

if [[ "${dry_run}" -eq 0 ]]; then
  mkdir -p "${target_dir}"
fi

move_from_dir() {
  local from_dir="$1"
  local moved=0
  local deduped=0

  [[ -d "${from_dir}" ]] || {
    log_info "No directory at ${from_dir} (skipping file move)."
    echo "0 0"
    return
  }

  shopt -s nullglob
  for src in "${from_dir}"/oceano-artwork-*; do
    [[ -f "${src}" ]] || continue
    local base dest
    base="$(basename "${src}")"
    dest="${target_dir}/${base}"

    if [[ "${dry_run}" -eq 1 ]]; then
      if [[ -e "${dest}" ]]; then
        deduped=$((deduped + 1))
      else
        moved=$((moved + 1))
      fi
      continue
    fi

    if [[ -e "${dest}" ]]; then
      rm -f "${src}"
      deduped=$((deduped + 1))
    else
      mv "${src}" "${dest}"
      moved=$((moved + 1))
    fi
  done
  shopt -u nullglob

  echo "${moved} ${deduped}"
}

# Move from configured source first, then /tmp as a safety net if different.
read -r moved_a deduped_a < <(move_from_dir "${source_dir}")
if [[ "${source_dir}" != "${DEFAULT_SOURCE}" ]]; then
  read -r moved_b deduped_b < <(move_from_dir "${DEFAULT_SOURCE}")
else
  moved_b=0
  deduped_b=0
fi

moved_total=$((moved_a + moved_b))
deduped_total=$((deduped_a + deduped_b))
log_ok "Artwork files migrated: moved=${moved_total}, deduped=${deduped_total}"

update_db_prefix() {
  local old_dir="$1"

  [[ -f "${library_db}" ]] || { echo 0; return; }
  [[ -n "${old_dir}" ]] || { echo 0; return; }

  python3 - "${library_db}" "${old_dir}" "${target_dir}" "${dry_run}" <<'PY'
import os
import sqlite3
import sys

db_path, old_dir, new_dir, dry_run = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
if not os.path.exists(db_path):
    print(0)
    raise SystemExit(0)

old_prefix = old_dir.rstrip('/') + '/'
new_prefix = new_dir.rstrip('/') + '/'
pattern = old_prefix + 'oceano-artwork-%'

conn = sqlite3.connect(db_path)
try:
    cur = conn.cursor()
    cur.execute("SELECT COUNT(*) FROM collection WHERE artwork_path LIKE ?", (pattern,))
    count = int(cur.fetchone()[0] or 0)
    if dry_run:
        print(count)
        raise SystemExit(0)

    cur.execute(
        "UPDATE collection "
        "SET artwork_path = replace(artwork_path, ?, ?) "
        "WHERE artwork_path LIKE ?",
        (old_prefix, new_prefix, pattern),
    )
    conn.commit()
    print(cur.rowcount)
finally:
    conn.close()
PY
}

updated_from_source="$(update_db_prefix "${source_dir}")"
if [[ "${source_dir}" != "${DEFAULT_SOURCE}" ]]; then
  updated_from_tmp="$(update_db_prefix "${DEFAULT_SOURCE}")"
else
  updated_from_tmp=0
fi
updated_total=$((updated_from_source + updated_from_tmp))

if [[ -f "${library_db}" ]]; then
  if [[ "${dry_run}" -eq 1 ]]; then
    log_info "Dry run: ${updated_total} DB row(s) would be updated"
  else
    log_ok "Library paths updated: ${updated_total} record(s)"
  fi
else
  log_info "Library DB not found at ${library_db} (skipped DB update)"
fi

if [[ -f "${config_path}" ]]; then
  if [[ "${dry_run}" -eq 1 ]]; then
    log_info "Dry run: would set advanced.artwork_dir=${target_dir} in ${config_path}"
  else
    python3 - "${config_path}" "${target_dir}" <<'PY'
import json
import os
import sys

cfg_path, target = sys.argv[1], sys.argv[2]
with open(cfg_path, 'r', encoding='utf-8') as f:
    cfg = json.load(f)
adv = cfg.get('advanced')
if not isinstance(adv, dict):
    adv = {}
    cfg['advanced'] = adv
adv['artwork_dir'] = target
tmp = cfg_path + '.tmp'
with open(tmp, 'w', encoding='utf-8') as f:
    json.dump(cfg, f, indent=2)
    f.write('\n')
os.replace(tmp, cfg_path)
PY
    log_ok "Config updated: advanced.artwork_dir=${target_dir}"
  fi
else
  log_warn "Config file not found at ${config_path} (skipped config update)"
fi

if [[ "${dry_run}" -eq 1 ]]; then
  log_info "Dry run complete. No changes written."
else
  log_info "Migration complete. Restart services to apply config:"
  echo "  sudo systemctl restart oceano-state-manager.service oceano-web.service"
fi
