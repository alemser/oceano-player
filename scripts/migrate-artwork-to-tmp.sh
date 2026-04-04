#!/usr/bin/env bash
# migrate-artwork-to-tmp.sh
#
# Moves artwork files saved by the web UI from /var/lib/oceano/artwork
# into /tmp, then updates every artwork_path in the collection DB to
# reflect the new location.
#
# Safe to run multiple times; files already in /tmp are skipped.
# If the oceano-web service is running, restart it after this script
# so it serves artwork from the new path.
#
# Usage:
#   sudo bash scripts/migrate-artwork-to-tmp.sh
#   sudo bash scripts/migrate-artwork-to-tmp.sh --db /custom/path/library.db

set -euo pipefail

DB_PATH="/var/lib/oceano/library.db"
OLD_ART_DIR="/var/lib/oceano/artwork"
NEW_ART_DIR="/tmp"

# Allow overriding the DB path via --db flag.
while [[ $# -gt 0 ]]; do
    case "$1" in
        --db) DB_PATH="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

if [[ ! -f "$DB_PATH" ]]; then
    echo "ERROR: library database not found at $DB_PATH"
    exit 1
fi

if [[ ! -d "$OLD_ART_DIR" ]]; then
    echo "Nothing to migrate: $OLD_ART_DIR does not exist."
    exit 0
fi

echo "==> Migrating artwork from $OLD_ART_DIR to $NEW_ART_DIR"
echo "    Database: $DB_PATH"
echo ""

# Read all distinct artwork_path values that live under OLD_ART_DIR.
mapfile -t old_paths < <(sqlite3 "$DB_PATH" \
    "SELECT DISTINCT artwork_path FROM collection
     WHERE artwork_path IS NOT NULL AND artwork_path != ''
       AND artwork_path LIKE '${OLD_ART_DIR}/%';")

if [[ ${#old_paths[@]} -eq 0 ]]; then
    echo "No artwork paths in the DB point to $OLD_ART_DIR. Nothing to do."
    exit 0
fi

moved=0
skipped=0
missing=0

for old_path in "${old_paths[@]}"; do
    filename="$(basename "$old_path")"
    new_path="${NEW_ART_DIR}/${filename}"

    if [[ ! -f "$old_path" ]]; then
        echo "  SKIP (file missing on disk): $old_path"
        (( missing++ )) || true
        continue
    fi

    if [[ "$old_path" == "$new_path" ]]; then
        echo "  SKIP (already in $NEW_ART_DIR): $filename"
        (( skipped++ )) || true
        continue
    fi

    echo "  MOVE: $old_path -> $new_path"
    cp "$old_path" "$new_path"

    # Update every row in the DB that points to this old path.
    sqlite3 "$DB_PATH" \
        "UPDATE collection SET artwork_path='${new_path//\'/\'\'}' WHERE artwork_path='${old_path//\'/\'\'}';"

    rm "$old_path"
    (( moved++ )) || true
done

echo ""
echo "==> Done. Moved: $moved  Already in /tmp: $skipped  Missing on disk: $missing"

# Remove the old artwork dir if it is now empty.
if [[ -d "$OLD_ART_DIR" ]] && [[ -z "$(ls -A "$OLD_ART_DIR")" ]]; then
    rmdir "$OLD_ART_DIR"
    echo "    Removed empty directory: $OLD_ART_DIR"
fi

echo ""
echo "Restart the web service to pick up the new paths:"
echo "    sudo systemctl restart oceano-web.service"
