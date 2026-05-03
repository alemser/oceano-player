package main

import (
	"database/sql"
	"fmt"
)

// ensureLibrarySyncSchema applies oceano_library_sync + changelog + triggers.
// Keep in sync with internal/library migrations after recognition_summary ShazamContinuity merge
// and recognition_attempts (T22).
func ensureLibrarySyncSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS oceano_library_sync (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			library_version INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT OR IGNORE INTO oceano_library_sync (id, library_version) VALUES (1, 0)`,
		`CREATE TABLE IF NOT EXISTS library_changelog (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			version INTEGER NOT NULL,
			collection_id INTEGER NOT NULL,
			op TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS library_changelog_version_idx ON library_changelog(version)`,
		`DROP TRIGGER IF EXISTS collection_ai_library_sync`,
		`CREATE TRIGGER collection_ai_library_sync AFTER INSERT ON collection BEGIN
			UPDATE oceano_library_sync SET library_version = library_version + 1 WHERE id = 1;
			INSERT INTO library_changelog(version, collection_id, op)
				SELECT library_version, NEW.id, 'upsert' FROM oceano_library_sync WHERE id = 1;
		END`,
		`DROP TRIGGER IF EXISTS collection_au_library_sync`,
		`CREATE TRIGGER collection_au_library_sync AFTER UPDATE ON collection BEGIN
			UPDATE oceano_library_sync SET library_version = library_version + 1 WHERE id = 1;
			INSERT INTO library_changelog(version, collection_id, op)
				SELECT library_version, NEW.id, 'upsert' FROM oceano_library_sync WHERE id = 1;
		END`,
		`DROP TRIGGER IF EXISTS collection_ad_library_sync`,
		`CREATE TRIGGER collection_ad_library_sync AFTER DELETE ON collection BEGIN
			UPDATE oceano_library_sync SET library_version = library_version + 1 WHERE id = 1;
			INSERT INTO library_changelog(version, collection_id, op)
				SELECT library_version, OLD.id, 'delete' FROM oceano_library_sync WHERE id = 1;
		END`,
		`INSERT INTO library_changelog(version, collection_id, op) SELECT 1, id, 'upsert' FROM collection WHERE NOT EXISTS (SELECT 1 FROM library_changelog LIMIT 1)`,
		`UPDATE oceano_library_sync SET library_version = (SELECT COALESCE(MAX(version), 0) FROM library_changelog) WHERE id = 1`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("library sync schema: %w", err)
		}
	}
	return nil
}
