#!/usr/bin/env bash
# migrate-artwork-dir.sh
# Migrates artwork_path entries in the Oceano library database from an old
# directory to a new one. Files that still exist are moved; missing files
# have their path cleared so the state-manager re-downloads them.
#
# Usage:
#   sudo ./migrate-artwork-dir.sh [--db <path>] [--old <dir>] [--new <dir>] [--dry-run]
#
# Defaults:
#   --db   /var/lib/oceano/library.db
#   --old  /tmp
#   --new  /var/lib/oceano/artwork

set -euo pipefail

DB="/var/lib/oceano/library.db"
OLD_DIR="/tmp"
NEW_DIR="/var/lib/oceano/artwork"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --db)    DB="$2";      shift 2 ;;
    --old)   OLD_DIR="$2"; shift 2 ;;
    --new)   NEW_DIR="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

if [[ ! -f "$DB" ]]; then
  echo "Database not found: $DB"
  exit 1
fi

mkdir -p "$NEW_DIR"

moved=0
cleared=0
skipped=0

# Read all rows that have an artwork_path starting with OLD_DIR.
while IFS='|' read -r id artwork_path; do
  [[ -z "$artwork_path" ]] && continue

  if [[ -f "$artwork_path" ]]; then
    # File still exists — move it to the new directory.
    filename="$(basename "$artwork_path")"
    new_path="$NEW_DIR/$filename"
    if [[ "$DRY_RUN" == true ]]; then
      echo "[DRY-RUN] would move: $artwork_path → $new_path (id=$id)"
    else
      mv "$artwork_path" "$new_path"
      sqlite3 "$DB" "UPDATE collection SET artwork_path='$new_path' WHERE id=$id;"
      echo "Moved:   $artwork_path → $new_path (id=$id)"
    fi
    ((moved++))
  else
    # File is gone — clear the path so the state-manager re-downloads it.
    if [[ "$DRY_RUN" == true ]]; then
      echo "[DRY-RUN] would clear missing path: $artwork_path (id=$id)"
    else
      sqlite3 "$DB" "UPDATE collection SET artwork_path='' WHERE id=$id;"
      echo "Cleared: $artwork_path (id=$id, file missing — will re-download)"
    fi
    ((cleared++))
  fi
done < <(sqlite3 "$DB" "SELECT id, artwork_path FROM collection WHERE artwork_path LIKE '${OLD_DIR}/%';")

echo ""
echo "Done. Moved: $moved  Cleared: $cleared  Skipped: $skipped"
if [[ "$DRY_RUN" == true ]]; then
  echo "(dry-run — no changes were made)"
fi
