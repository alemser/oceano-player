package main

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		last_played  TEXT    NOT NULL
	)`)
	if err != nil {
		db.Close()
		t.Fatalf("create table: %v", err)
	}

	for i, ap := range artworkPaths {
		_, err = db.Exec(
			`INSERT INTO collection (title, artist, artwork_path, play_count, first_played, last_played)
			 VALUES (?, ?, ?, 1, '2024-01-01', '2024-01-01')`,
			"Track", "Artist"+string(rune('A'+i)), ap,
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

	// Create artwork files.
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
	if err := lib.generateBackup(backupPath); err != nil {
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
	if err := lib.generateBackup(backupPath); err != nil {
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
	if err := lib.generateBackup(backupPath); err != nil {
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
	if err := lib.generateBackup(backupPath); err != nil {
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

// --- restoreScriptContent ---

func TestRestoreScriptContent_ContainsPaths(t *testing.T) {
	dbPath := "/var/lib/oceano/library.db"
	script := restoreScriptContent(dbPath)

	checks := []string{
		"#!/usr/bin/env bash",
		"/var/lib/oceano/library.db",
		"/var/lib/oceano/artwork",
		"restore.sh",
		// mkdir -p must appear before the cp that copies the database file.
		`mkdir -p "$(dirname "$DB_DEST")"`,
	}
	for _, want := range checks {
		if !strings.Contains(script, want) {
			t.Errorf("restore script missing expected string: %q", want)
		}
	}

	// Verify mkdir -p is placed before cp so the destination directory exists.
	mkdirIdx := strings.Index(script, `mkdir -p "$(dirname "$DB_DEST")"`)
	cpIdx := strings.Index(script, `cp "$SCRIPT_DIR/library.db"`)
	if mkdirIdx == -1 || cpIdx == -1 || mkdirIdx > cpIdx {
		t.Errorf("mkdir -p must appear before cp in restore script (mkdirIdx=%d cpIdx=%d)", mkdirIdx, cpIdx)
	}
}

func TestRestoreScriptContent_PathsAreShellQuoted(t *testing.T) {
	// Paths with spaces and special shell characters must be safely quoted.
	dbPath := "/var/lib/oceano's library/library.db"
	script := restoreScriptContent(dbPath)
	// Single-quote escaping: ' → '\''
	wantDBQuoted := `'/var/lib/oceano'\''s library/library.db'`
	if !strings.Contains(script, wantDBQuoted) {
		t.Errorf("restore script DB path not properly shell-quoted; want %q in:\n%s", wantDBQuoted, script)
	}
}

func TestRestoreScriptContent_IsExecutable(t *testing.T) {
	script := restoreScriptContent("/tmp/test.db")
	if !strings.HasPrefix(script, "#!/usr/bin/env bash\n") {
		t.Errorf("script should start with bash shebang, got: %q", script[:min(len(script), 40)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- HTTP backup handler ---

func TestBackupHandler_MethodNotAllowed(t *testing.T) {
	mux := http.NewServeMux()
	registerLibraryRoutes(mux, "/nonexistent/library.db", "/nonexistent/state.json")

	r := httptest.NewRequest(http.MethodPost, "/api/library/export/backup", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST should return 405, got %d", w.Code)
	}
}

func TestBackupHandler_LibraryNotInitialised(t *testing.T) {
	mux := http.NewServeMux()
	registerLibraryRoutes(mux, "/nonexistent/library.db", "/nonexistent/state.json")

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
	registerLibraryRoutes(mux, dbPath, "/nonexistent/state.json")

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
