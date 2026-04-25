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

func assertTableAbsent(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&got)
	if err != sql.ErrNoRows {
		t.Fatalf("table %q unexpectedly present (got=%q err=%v)", table, got, err)
	}
}
