package library

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrate_AppliesAllVersionsAndIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "library.db")

	lib, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer lib.Close()

	assertSchemaVersionCount(t, lib.DB(), currentSchemaVersion)
	assertTableAbsent(t, lib.DB(), "fingerprints")

	// Re-run migrations to ensure idempotence.
	if err := lib.migrate(); err != nil {
		t.Fatalf("migrate() second run failed: %v", err)
	}

	assertSchemaVersionCount(t, lib.DB(), currentSchemaVersion)
	assertTableAbsent(t, lib.DB(), "fingerprints")
}

func assertSchemaVersionCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&got); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if got != want {
		t.Fatalf("schema_migrations count = %d, want %d", got, want)
	}
}

func TestMergeShazamRecognitionSummaryIntoShazamio(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE recognition_summary (
		provider TEXT,
		event    TEXT,
		count    INTEGER DEFAULT 0,
		PRIMARY KEY(provider, event)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO recognition_summary (provider, event, count) VALUES
		('Shazam', 'match', 10),
		('Shazamio', 'match', 4),
		('Shazam', 'rate_limit', 2)`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO recognition_summary (provider, event, count)
			SELECT 'Shazamio', event, count FROM recognition_summary WHERE provider = 'Shazam'
			ON CONFLICT(provider, event) DO UPDATE SET count = count + excluded.count`,
		`DELETE FROM recognition_summary WHERE provider = 'Shazam'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	var matchCount, rateLimitCount int
	if err := db.QueryRow(
		`SELECT count FROM recognition_summary WHERE provider = 'Shazamio' AND event = 'match'`,
	).Scan(&matchCount); err != nil {
		t.Fatal(err)
	}
	if matchCount != 14 {
		t.Fatalf("merged Shazamio match count = %d, want 14", matchCount)
	}
	if err := db.QueryRow(
		`SELECT count FROM recognition_summary WHERE provider = 'Shazamio' AND event = 'rate_limit'`,
	).Scan(&rateLimitCount); err != nil {
		t.Fatal(err)
	}
	if rateLimitCount != 2 {
		t.Fatalf("Shazamio rate_limit count = %d, want 2", rateLimitCount)
	}
	var shazamRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM recognition_summary WHERE provider = 'Shazam'`).Scan(&shazamRows); err != nil {
		t.Fatal(err)
	}
	if shazamRows != 0 {
		t.Fatalf("leftover Shazam rows = %d, want 0", shazamRows)
	}
}

func assertTableAbsent(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&got)
	if err != sql.ErrNoRows {
		t.Fatalf("table %q unexpectedly present (got=%q err=%v)", table, got, err)
	}
}
