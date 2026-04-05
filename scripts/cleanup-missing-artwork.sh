#!/bin/bash
set -euo pipefail

# Cleanup script: Remove artwork_path from collection records when the
# referenced file doesn't exist. Useful after migrations or crashes.

DB="${1:-/var/lib/oceano/library.db}"
DRY_RUN="${2:-}"

if [[ ! -f "$DB" ]]; then
  echo "Error: Database not found at $DB"
  exit 1
fi

# Find all entries with artwork_path references where the file doesn't exist
python3 - "$DB" "$DRY_RUN" <<'PYTHON'
import os
import sqlite3
import sys

db_path, dry_run_flag = sys.argv[1], sys.argv[2] if len(sys.argv) > 2 else ""
is_dry_run = (dry_run_flag == "--dry-run")

conn = sqlite3.connect(db_path)
conn.row_factory = sqlite3.Row
cur = conn.cursor()

# Get all artwork paths
cur.execute("""
  SELECT id, title, artist, artwork_path 
  FROM collection 
  WHERE artwork_path IS NOT NULL AND artwork_path != ''
""")

missing = []
for row in cur.fetchall():
    path = row['artwork_path']
    if not os.path.exists(path):
        missing.append((row['id'], row['title'], row['artist'], path))
        print(f"Missing: id={row['id']} \"{row['artist']} — {row['title']}\" → {path}")

if not missing:
    print("No missing artwork files found.")
    sys.exit(0)

print(f"\nFound {len(missing)} orphaned records.")

if is_dry_run:
    print("[DRY RUN] Would clear artwork_path for these entries.")
    sys.exit(0)

# Clear artwork_path for missing files
for entry_id, _, _, _ in missing:
    cur.execute("UPDATE collection SET artwork_path = NULL WHERE id = ?", (entry_id,))

conn.commit()
conn.close()

print(f"✓ Cleared artwork_path for {len(missing)} entries.")
PYTHON
