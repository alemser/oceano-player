package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func createResolveTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "library.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE collection (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			acrid          TEXT UNIQUE,
			title          TEXT NOT NULL,
			artist         TEXT NOT NULL,
			album          TEXT,
			label          TEXT,
			released       TEXT,
			score          INTEGER,
			format         TEXT DEFAULT 'Unknown',
			track_number   TEXT,
			artwork_path   TEXT,
			play_count     INTEGER NOT NULL DEFAULT 1,
			first_played   TEXT NOT NULL,
			last_played    TEXT NOT NULL,
			user_confirmed INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE fingerprints (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
			data     TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec schema stmt: %v", err)
		}
	}

	// target confirmed row
	if _, err := db.Exec(`
		INSERT INTO collection (id, title, artist, album, play_count, first_played, last_played, user_confirmed)
		VALUES (1, 'Target Song', 'Target Artist', 'Target Album', 3, '2024-01-01T00:00:00Z', '2024-01-02T00:00:00Z', 1)`); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	// unresolved fingerprint stub
	if _, err := db.Exec(`
		INSERT INTO collection (id, title, artist, play_count, first_played, last_played, user_confirmed)
		VALUES (2, '', '', 2, '2024-01-03T00:00:00Z', '2024-01-04T00:00:00Z', 0)`); err != nil {
		t.Fatalf("insert stub: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO fingerprints (entry_id, data) VALUES (2, '1,2,3'), (2, '4,5,6')`); err != nil {
		t.Fatalf("insert fingerprints: %v", err)
	}

	return dbPath
}

func TestLibrarySearch_ExcludesStubRows(t *testing.T) {
	dbPath := createResolveTestDB(t)
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	rows, err := lib.search("target", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("search returned %d rows, want 1", len(rows))
	}
	if rows[0].ID != 1 {
		t.Fatalf("search row id=%d, want 1", rows[0].ID)
	}
}

func TestResolveFingerprintStub_MovesFingerprintsAndDeletesStub(t *testing.T) {
	dbPath := createResolveTestDB(t)
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	target, err := lib.resolveFingerprintStub(2, 1)
	if err != nil {
		t.Fatalf("resolveFingerprintStub: %v", err)
	}
	if target == nil || target.ID != 1 {
		t.Fatalf("target=%v, want id=1", target)
	}

	var stubCount int
	if err := lib.db.QueryRow(`SELECT COUNT(*) FROM collection WHERE id=2`).Scan(&stubCount); err != nil {
		t.Fatalf("count stub rows: %v", err)
	}
	if stubCount != 0 {
		t.Fatalf("stub should be deleted, still found %d row(s)", stubCount)
	}

	var moved int
	if err := lib.db.QueryRow(`SELECT COUNT(*) FROM fingerprints WHERE entry_id=1`).Scan(&moved); err != nil {
		t.Fatalf("count moved fingerprints: %v", err)
	}
	if moved != 2 {
		t.Fatalf("fingerprints moved=%d, want 2", moved)
	}
}

func TestResolveEndpoint_RejectsNonStubSource(t *testing.T) {
	dbPath := createResolveTestDB(t)
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(statePath, []byte(`{"source":"None","state":"stopped","track":null,"updated_at":"2024-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write state file: %v", err)
	}

	mux := http.NewServeMux()
	registerLibraryRoutes(mux, dbPath, statePath, t.TempDir())

	// Entry 1 is a confirmed track, not a stub, and must be rejected as source.
	// Use target_id=2 so rejection happens because source id=1 is not an
	// unresolved stub, not because source and target IDs are equal.
	req := httptest.NewRequest(http.MethodPost, "/api/library/1/resolve", strings.NewReader(`{"target_id":2}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}
