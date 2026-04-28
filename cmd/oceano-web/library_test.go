package main

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// createTestDB creates a minimal SQLite library database in dir, inserts
// entries, and returns the database path.
func createTestDB(t *testing.T, dir string, artworkPaths []string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "library.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	db.SetMaxOpenConns(1)

	_, err = db.Exec(`CREATE TABLE collection (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		acrid        TEXT    UNIQUE,
		title        TEXT    NOT NULL,
		artist       TEXT    NOT NULL,
		album        TEXT,
		label        TEXT,
		released     TEXT,
		score        INTEGER,
		format       TEXT    DEFAULT 'Unknown',
		track_number TEXT,
		artwork_path TEXT,
		play_count   INTEGER NOT NULL DEFAULT 1,
		first_played TEXT    NOT NULL,
		last_played  TEXT    NOT NULL,
		duration_ms  INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		db.Close()
		t.Fatalf("create table: %v", err)
	}

	for i, ap := range artworkPaths {
		_, err = db.Exec(
			`INSERT INTO collection (title, artist, artwork_path, play_count, first_played, last_played)
			 VALUES (?, ?, ?, 1, '2024-01-01', '2024-01-01')`,
			"Track", fmt.Sprintf("Artist%c", 'A'+i), ap,
		)
		if err != nil {
			db.Close()
			t.Fatalf("insert row: %v", err)
		}
	}
	db.Close()
	return dbPath
}

// archiveEntries opens a .tar.gz archive and returns the list of entry names.
func archiveEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	entries := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		data, _ := io.ReadAll(tr)
		entries[hdr.Name] = data
	}
	return entries
}

// --- generateBackup ---

func TestGenerateBackup_ContainsRequiredFiles(t *testing.T) {
	dir := t.TempDir()

	// Create artwork files inside the managed artwork dir.
	artDir := filepath.Join(dir, "artwork")
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatal(err)
	}
	art1 := filepath.Join(artDir, "oceano-artwork-aabb.jpg")
	art2 := filepath.Join(artDir, "oceano-artwork-ccdd.jpg")
	if err := os.WriteFile(art1, []byte("fake-jpeg-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(art2, []byte("fake-jpeg-2"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := createTestDB(t, dir, []string{art1, art2})
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	backupPath := filepath.Join(dir, "backup.tar.gz")
	if err := lib.generateBackup(backupPath, artDir, ""); err != nil {
		t.Fatalf("generateBackup: %v", err)
	}

	entries := archiveEntries(t, backupPath)

	required := []string{
		"library.db",
		"restore.sh",
		"artwork/oceano-artwork-aabb.jpg",
		"artwork/oceano-artwork-ccdd.jpg",
	}
	for _, name := range required {
		if _, ok := entries[name]; !ok {
			t.Errorf("archive missing expected entry: %s", name)
		}
	}
}

func TestGenerateBackup_ArtworkContentPreserved(t *testing.T) {
	dir := t.TempDir()
	artDir := filepath.Join(dir, "artwork")
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		t.Fatal(err)
	}

	artFile := filepath.Join(artDir, "test.jpg")
	want := []byte("my-artwork-bytes")
	if err := os.WriteFile(artFile, want, 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := createTestDB(t, dir, []string{artFile})
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	backupPath := filepath.Join(dir, "backup.tar.gz")
	if err := lib.generateBackup(backupPath, artDir, ""); err != nil {
		t.Fatalf("generateBackup: %v", err)
	}

	entries := archiveEntries(t, backupPath)
	got, ok := entries["artwork/test.jpg"]
	if !ok {
		t.Fatal("archive missing artwork/test.jpg")
	}
	if string(got) != string(want) {
		t.Errorf("artwork content = %q, want %q", got, want)
	}
}

func TestGenerateBackup_MissingArtworkSkipped(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(dir, "artwork", "missing.jpg") // does not exist on disk

	dbPath := createTestDB(t, dir, []string{missingPath})
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	backupPath := filepath.Join(dir, "backup.tar.gz")
	if err := lib.generateBackup(backupPath, filepath.Join(dir, "artwork"), ""); err != nil {
		t.Fatalf("generateBackup with missing artwork should not fail: %v", err)
	}

	entries := archiveEntries(t, backupPath)
	if _, ok := entries["library.db"]; !ok {
		t.Error("archive should still contain library.db when artwork is missing")
	}
	if _, ok := entries["artwork/missing.jpg"]; ok {
		t.Error("archive should not contain missing artwork file")
	}
}

func TestGenerateBackup_NoArtwork(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir, nil)
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	backupPath := filepath.Join(dir, "backup.tar.gz")
	if err := lib.generateBackup(backupPath, filepath.Join(dir, "artwork"), ""); err != nil {
		t.Fatalf("generateBackup with no artwork: %v", err)
	}

	entries := archiveEntries(t, backupPath)
	if _, ok := entries["library.db"]; !ok {
		t.Error("archive missing library.db")
	}
	if _, ok := entries["restore.sh"]; !ok {
		t.Error("archive missing restore.sh")
	}
}

func TestGenerateBackup_ArtworkOutsideManagedDirSkipped(t *testing.T) {
	dir := t.TempDir()

	// Create a file outside the managed artwork dir.
	outsidePath := filepath.Join(dir, "secret.jpg")
	if err := os.WriteFile(outsidePath, []byte("sensitive"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := createTestDB(t, dir, []string{outsidePath})
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	backupPath := filepath.Join(dir, "backup.tar.gz")
	if err := lib.generateBackup(backupPath, filepath.Join(dir, "artwork"), ""); err != nil {
		t.Fatalf("generateBackup: %v", err)
	}

	entries := archiveEntries(t, backupPath)
	if _, ok := entries["artwork/secret.jpg"]; ok {
		t.Error("archive must not include artwork outside the managed artwork directory")
	}
}

func TestGenerateBackup_PreservesRelativeArtworkPaths(t *testing.T) {
	dir := t.TempDir()
	artDir := filepath.Join(dir, "artwork")
	if err := os.MkdirAll(filepath.Join(artDir, "set-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(artDir, "set-b"), 0o755); err != nil {
		t.Fatal(err)
	}

	artA := filepath.Join(artDir, "set-a", "cover.jpg")
	artB := filepath.Join(artDir, "set-b", "cover.jpg")
	if err := os.WriteFile(artA, []byte("art-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artB, []byte("art-b"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := createTestDB(t, dir, []string{artA, artB})
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	backupPath := filepath.Join(dir, "backup.tar.gz")
	if err := lib.generateBackup(backupPath, artDir, ""); err != nil {
		t.Fatalf("generateBackup: %v", err)
	}

	entries := archiveEntries(t, backupPath)
	if got := string(entries["artwork/set-a/cover.jpg"]); got != "art-a" {
		t.Errorf("artwork/set-a/cover.jpg content = %q, want %q", got, "art-a")
	}
	if got := string(entries["artwork/set-b/cover.jpg"]); got != "art-b" {
		t.Errorf("artwork/set-b/cover.jpg content = %q, want %q", got, "art-b")
	}
}

// --- restoreScriptContent ---

func TestRestoreScriptContent_ContainsPaths(t *testing.T) {
	dbPath := "/var/lib/oceano/library.db"
	script := restoreScriptContent(dbPath, "/tmp", "")

	checks := []string{
		"#!/usr/bin/env bash",
		"/var/lib/oceano/library.db",
		"/tmp",
	}
	for _, want := range checks {
		if !strings.Contains(script, want) {
			t.Errorf("restore script missing expected string: %q", want)
		}
	}
}

func TestRestoreScriptContent_IsExecutable(t *testing.T) {
	script := restoreScriptContent("/tmp/test.db", "/tmp", "")
	if !strings.HasPrefix(script, "#!/usr/bin/env bash\n") {
		prefix := script
		if len(prefix) > 40 {
			prefix = prefix[:40]
		}
		t.Errorf("script should start with bash shebang, got prefix: %q", prefix)
	}
}

func TestRestoreScriptContent_PathsAreShellQuoted(t *testing.T) {
	// Paths with spaces would be mishandled without proper quoting.
	dbPath := "/var/lib/oceano library/library.db"
	script := restoreScriptContent(dbPath, "/tmp", "")
	quoted := shellQuote(dbPath)
	if !strings.Contains(script, quoted) {
		t.Errorf("restore script should contain shell-quoted db path %q, script:\n%s", quoted, script)
	}
}

// --- shellQuote ---

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/var/lib/oceano/library.db", "'/var/lib/oceano/library.db'"},
		{"path with spaces", "'path with spaces'"},
		{"it's here", "'it'\\''s here'"},
		{"", "''"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- HTTP backup handler ---

func TestBackupHandler_MethodNotAllowed(t *testing.T) {
	mux := http.NewServeMux()
	registerBackupRoutes(mux, "/nonexistent/library.db", "/tmp", "")

	r := httptest.NewRequest(http.MethodPost, "/api/library/export/backup", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST should return 405, got %d", w.Code)
	}
}

func TestBackupHandler_LibraryNotInitialised(t *testing.T) {
	mux := http.NewServeMux()
	registerBackupRoutes(mux, "/nonexistent/library.db", "/tmp", "")

	r := httptest.NewRequest(http.MethodGet, "/api/library/export/backup", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("missing library should return 503, got %d", w.Code)
	}
}

func TestBackupHandler_ReturnsGzipArchive(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir, nil)

	mux := http.NewServeMux()
	registerBackupRoutes(mux, dbPath, filepath.Join(dir, "artwork"), "")

	r := httptest.NewRequest(http.MethodGet, "/api/library/export/backup", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "oceano-backup.tar.gz") {
		t.Errorf("Content-Disposition = %q, should contain oceano-backup.tar.gz", cd)
	}

	// Verify the body is a valid gzip stream.
	gr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("response body is not valid gzip: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read error: %v", err)
		}
		names = append(names, hdr.Name)
	}
	found := false
	for _, n := range names {
		if n == "library.db" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("archive should contain library.db, got: %v", names)
	}
}

func TestGetBoundaryEventStats(t *testing.T) {
	dir := t.TempDir()
	path := createTestDB(t, dir, nil)
	lib, err := openLibraryDB(path)
	if err != nil {
		t.Fatalf("openLibraryDB: %v", err)
	}
	defer lib.close()
	if _, err := lib.db.Exec(`
		INSERT INTO boundary_events (occurred_at, outcome, boundary_type, is_hard, physical_source, format_at_event, duration_ms, seek_ms)
		VALUES (datetime('now'), 'fired', 'silence->audio', 1, 'Physical', 'Physical', 0, 0),
		       (datetime('now'), 'suppressed_duration_guard', 'silence->audio', 0, 'Physical', 'Vinyl', 180000, 60000),
		       (datetime('now'), 'energy_change_cooldown', 'energy-change', 0, 'Physical', 'Physical', 0, 0)`); err != nil {
		t.Fatalf("insert boundary_events: %v", err)
	}
	stats, err := lib.getBoundaryEventStats(0, nil)
	if err != nil {
		t.Fatalf("getBoundaryEventStats: %v", err)
	}
	if stats.Total != 3 {
		t.Fatalf("total = %d, want 3", stats.Total)
	}
	if stats.ByOutcome["fired"] != 1 || stats.ByOutcome["suppressed_duration_guard"] != 1 {
		t.Fatalf("by_outcome mismatch: %+v", stats.ByOutcome)
	}
	if stats.ActionableTotal != 2 {
		t.Fatalf("actionable_total = %d, want 2 (cooldown excluded)", stats.ActionableTotal)
	}
	if stats.FireRate < 0 || stats.FireRate > 1 {
		t.Fatalf("fire_rate = %v, want 0..1", stats.FireRate)
	}
	wantRate := 0.5
	if stats.FireRate != wantRate {
		t.Fatalf("fire_rate = %v, want %v", stats.FireRate, wantRate)
	}
}

func TestGetBoundaryEventStats_ReadinessUsesR3Window(t *testing.T) {
	dir := t.TempDir()
	path := createTestDB(t, dir, nil)
	lib, err := openLibraryDB(path)
	if err != nil {
		t.Fatalf("openLibraryDB: %v", err)
	}
	defer lib.close()
	old := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	if _, err := lib.db.Exec(`
		INSERT INTO boundary_events (occurred_at, outcome, boundary_type, is_hard, physical_source, format_at_event, duration_ms, seek_ms, followup_outcome)
		VALUES (?, 'fired', 'silence->audio', 1, 'Physical', 'CD', 180000, 10000, 'matched')`, old); err != nil {
		t.Fatalf("insert: %v", err)
	}
	cfg := defaultConfig()
	cfg.Advanced.TelemetryNudges = &TelemetryNudgesConfig{
		Enabled:          true,
		LookbackDays:     3,
		MinFollowupPairs: 1,
	}
	stats, err := lib.getBoundaryEventStats(0, &cfg)
	if err != nil {
		t.Fatalf("getBoundaryEventStats: %v", err)
	}
	cr := stats.CalibrationReadiness
	if cr == nil {
		t.Fatal("expected calibration_readiness")
	}
	if cr.PairedFollowupsR3Window != 1 {
		t.Fatalf("paired = %d, want 1", cr.PairedFollowupsR3Window)
	}
	if !cr.ReadyForR3Nudges {
		t.Fatal("expected ready_for_r3_nudges with min_pairs=1 and one row")
	}
}

func TestDeleteEntry_ClearsBoundaryEventsCollectionRef(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "library.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE collection (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			acrid TEXT UNIQUE,
			shazam_id TEXT,
			title TEXT NOT NULL,
			artist TEXT NOT NULL,
			album TEXT,
			label TEXT,
			released TEXT,
			score INTEGER,
			format TEXT DEFAULT 'Unknown',
			track_number TEXT,
			artwork_path TEXT,
			play_count INTEGER NOT NULL DEFAULT 1,
			first_played TEXT NOT NULL,
			last_played TEXT NOT NULL,
			user_confirmed INTEGER NOT NULL DEFAULT 0,
			isrc TEXT,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			duration_fp_elapsed_ms INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE play_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			collection_id INTEGER REFERENCES collection(id)
		)`,
		`CREATE TABLE boundary_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			occurred_at TEXT NOT NULL,
			outcome TEXT NOT NULL,
			boundary_type TEXT NOT NULL DEFAULT '',
			is_hard INTEGER NOT NULL DEFAULT 0,
			physical_source TEXT NOT NULL DEFAULT '',
			format_at_event TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			seek_ms INTEGER NOT NULL DEFAULT 0,
			collection_id INTEGER REFERENCES collection(id)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v\n%s", err, s)
		}
	}
	if _, err := db.Exec(`INSERT INTO collection (title, artist, first_played, last_played) VALUES ('T', 'A', '2024-01-01', '2024-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO play_history (collection_id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO boundary_events (occurred_at, outcome, collection_id) VALUES ('2024-01-01T00:00:00Z', 'fired', 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()
	if err := lib.deleteEntry(1); err != nil {
		t.Fatalf("deleteEntry: %v", err)
	}
	var n int
	if err := lib.db.QueryRow(`SELECT COUNT(*) FROM collection`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("collection rows = %d, want 0", n)
	}
	var cid sql.NullInt64
	if err := lib.db.QueryRow(`SELECT collection_id FROM boundary_events WHERE id=1`).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	if cid.Valid && cid.Int64 != 0 {
		t.Fatalf("boundary_events.collection_id = %v, want NULL", cid)
	}
}

func TestUpdate_BackfillsBoundaryEventsFormatResolved(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir, nil)
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	if _, err := lib.db.Exec(`CREATE TABLE IF NOT EXISTS play_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_id INTEGER,
		title TEXT NOT NULL DEFAULT '',
		artist TEXT NOT NULL DEFAULT '',
		album TEXT,
		track_number TEXT,
		media_format TEXT,
		artwork_path TEXT
	)`); err != nil {
		t.Fatalf("create play_history: %v", err)
	}

	if _, err := lib.db.Exec(`
		INSERT INTO boundary_events (
			occurred_at, outcome, boundary_type, is_hard, physical_source,
			format_at_event, duration_ms, seek_ms, collection_id
		) VALUES ('2026-04-01T12:00:00Z','fired','silence->audio',1,'Physical','Physical',0,0,1)`); err != nil {
		t.Fatalf("insert boundary_events: %v", err)
	}

	if err := lib.update(1, "Track", "Artist", "", "", "", "Vinyl", "", "", 0, false); err != nil {
		t.Fatalf("update: %v", err)
	}
	var resolved sql.NullString
	var resolvedAt sql.NullString
	if err := lib.db.QueryRow(`SELECT format_resolved, format_resolved_at FROM boundary_events WHERE id=1`).Scan(&resolved, &resolvedAt); err != nil {
		t.Fatal(err)
	}
	if !resolved.Valid || resolved.String != "Vinyl" {
		t.Fatalf("format_resolved = %v, want Vinyl", resolved)
	}
	if !resolvedAt.Valid || resolvedAt.String == "" {
		t.Fatalf("format_resolved_at should be set")
	}

	if err := lib.update(1, "Track", "Artist", "", "", "", "Unknown", "", "", 0, false); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := lib.db.QueryRow(`SELECT format_resolved, format_resolved_at FROM boundary_events WHERE id=1`).Scan(&resolved, &resolvedAt); err != nil {
		t.Fatal(err)
	}
	if resolved.Valid || resolvedAt.Valid {
		t.Fatalf("want NULL resolution after Unknown, got resolved=%v at=%v", resolved, resolvedAt)
	}

	if err := lib.update(1, "Track", "Artist", "", "", "", "CD", "", "", 0, false); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := lib.db.QueryRow(`SELECT format_resolved FROM boundary_events WHERE id=1`).Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	if !resolved.Valid || resolved.String != "CD" {
		t.Fatalf("format_resolved = %v, want CD", resolved)
	}
}

func TestListRMSLearningRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir, nil)
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	empty, err := lib.listRMSLearningRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("want no rows, got %d", len(empty))
	}

	_, err = lib.db.Exec(`INSERT INTO rms_learning (
		format_key, updated_at, bins, max_rms, silence_counts, music_counts,
		silence_total, music_total, derived_enter, derived_exit
	) VALUES ('cd','2026-04-27T12:34:56Z',80,0.25,'[]','[]',50,150,0.0088,0.0101)`)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := lib.listRMSLearningRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.FormatKey != "cd" || r.SilenceTotal != 50 || r.MusicTotal != 150 {
		t.Fatalf("unexpected row: %+v", r)
	}
	if r.DerivedEnter == nil || math.Abs(*r.DerivedEnter-0.0088) > 1e-6 {
		t.Fatalf("derived_enter = %v", r.DerivedEnter)
	}
	if r.DerivedExit == nil || math.Abs(*r.DerivedExit-0.0101) > 1e-6 {
		t.Fatalf("derived_exit = %v", r.DerivedExit)
	}
	if r.UpdatedAt == "" {
		t.Fatal("want updated_at")
	}
}
