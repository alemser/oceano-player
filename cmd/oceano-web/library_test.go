package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"mime/multipart"
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
	if err := lib.generateBackup(backupPath, artDir); err != nil {
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
	if err := lib.generateBackup(backupPath, artDir); err != nil {
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
	if err := lib.generateBackup(backupPath, filepath.Join(dir, "artwork")); err != nil {
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
	if err := lib.generateBackup(backupPath, filepath.Join(dir, "artwork")); err != nil {
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
	if err := lib.generateBackup(backupPath, filepath.Join(dir, "artwork")); err != nil {
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
	if err := lib.generateBackup(backupPath, artDir); err != nil {
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
	script := restoreScriptContent(dbPath, "/tmp")

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
	script := restoreScriptContent("/tmp/test.db", "/tmp")
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
	script := restoreScriptContent(dbPath, "/tmp")
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
	registerBackupRoute(mux, "/nonexistent/library.db", "/tmp")

	r := httptest.NewRequest(http.MethodPost, "/api/library/export/backup", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST should return 405, got %d", w.Code)
	}
}

func TestBackupHandler_LibraryNotInitialised(t *testing.T) {
	mux := http.NewServeMux()
	registerBackupRoute(mux, "/nonexistent/library.db", "/tmp")

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
	registerBackupRoute(mux, dbPath, filepath.Join(dir, "artwork"))

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

// ── helpers for restore tests ──────────────────────────────────────────────

// buildBackupArchive creates an in-memory .tar.gz archive with the provided
// files. Each entry in files maps an archive path to its byte content.
func buildBackupArchive(t *testing.T, files map[string][]byte) *bytes.Buffer {
t.Helper()
var buf bytes.Buffer
gw := gzip.NewWriter(&buf)
tw := tar.NewWriter(gw)
for name, data := range files {
hdr := &tar.Header{Name: name, Size: int64(len(data)), Mode: 0o644, Typeflag: tar.TypeReg}
if err := tw.WriteHeader(hdr); err != nil {
t.Fatalf("tar header %s: %v", name, err)
}
if _, err := tw.Write(data); err != nil {
t.Fatalf("tar write %s: %v", name, err)
}
}
if err := tw.Close(); err != nil {
t.Fatalf("tar close: %v", err)
}
if err := gw.Close(); err != nil {
t.Fatalf("gzip close: %v", err)
}
return &buf
}

// buildRestoreRequest wraps archive data in a multipart POST request.
func buildRestoreRequest(t *testing.T, archive *bytes.Buffer) *http.Request {
t.Helper()
var body bytes.Buffer
mw := multipart.NewWriter(&body)
fw, err := mw.CreateFormFile("backup", "oceano-backup.tar.gz")
if err != nil {
t.Fatalf("create form file: %v", err)
}
if _, err := io.Copy(fw, archive); err != nil {
t.Fatalf("copy archive to form: %v", err)
}
mw.Close()
r := httptest.NewRequest(http.MethodPost, "/api/library/import/backup", &body)
r.Header.Set("Content-Type", mw.FormDataContentType())
return r
}

// --- restoreBackup ---

func TestRestoreBackup_RestoresDB(t *testing.T) {
src := t.TempDir()
dbPath := createTestDB(t, src, nil)

lib, err := openLibraryDB(dbPath)
if err != nil || lib == nil {
t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
}
backupPath := filepath.Join(src, "backup.tar.gz")
if err := lib.generateBackup(backupPath, filepath.Join(src, "artwork")); err != nil {
t.Fatalf("generateBackup: %v", err)
}
lib.close()

dst := t.TempDir()
destDB := filepath.Join(dst, "library.db")

f, err := os.Open(backupPath)
if err != nil {
t.Fatalf("open backup: %v", err)
}
defer f.Close()

if err := restoreBackup(f, destDB, filepath.Join(dst, "artwork")); err != nil {
t.Fatalf("restoreBackup: %v", err)
}
if _, err := os.Stat(destDB); err != nil {
t.Errorf("restored db not found: %v", err)
}
}

func TestRestoreBackup_RestoresArtwork(t *testing.T) {
src := t.TempDir()
artDir := filepath.Join(src, "artwork")
if err := os.MkdirAll(artDir, 0o755); err != nil {
t.Fatal(err)
}
artFile := filepath.Join(artDir, "oceano-artwork-test.jpg")
if err := os.WriteFile(artFile, []byte("fake-jpeg"), 0o644); err != nil {
t.Fatal(err)
}
dbPath := createTestDB(t, src, []string{artFile})
lib, err := openLibraryDB(dbPath)
if err != nil || lib == nil {
t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
}
backupPath := filepath.Join(src, "backup.tar.gz")
if err := lib.generateBackup(backupPath, artDir); err != nil {
t.Fatalf("generateBackup: %v", err)
}
lib.close()

dst := t.TempDir()
destDB := filepath.Join(dst, "library.db")
destArt := filepath.Join(dst, "artwork")

f, err := os.Open(backupPath)
if err != nil {
t.Fatalf("open backup: %v", err)
}
defer f.Close()

if err := restoreBackup(f, destDB, destArt); err != nil {
t.Fatalf("restoreBackup: %v", err)
}
restoredArt := filepath.Join(destArt, "oceano-artwork-test.jpg")
if _, err := os.Stat(restoredArt); err != nil {
t.Errorf("artwork not restored: %v", err)
}
}

func TestRestoreBackup_MissingLibraryDB(t *testing.T) {
archive := buildBackupArchive(t, map[string][]byte{
"artwork/some.jpg": []byte("fake"),
})
dst := t.TempDir()
err := restoreBackup(archive, filepath.Join(dst, "library.db"), filepath.Join(dst, "artwork"))
if err == nil {
t.Error("expected error when library.db is missing, got nil")
}
}

func TestRestoreBackup_RejectsPathTraversal(t *testing.T) {
dst := t.TempDir()
// Build an archive with a path-traversal artwork entry.
archive := buildBackupArchive(t, map[string][]byte{
"library.db":                []byte("fake-db"),
"artwork/../../../etc/evil": []byte("evil"),
})
destArt := filepath.Join(dst, "artwork")
if err := restoreBackup(archive, filepath.Join(dst, "library.db"), destArt); err != nil {
// restoreBackup may succeed (traversal entry is silently skipped).
t.Fatalf("restoreBackup: %v", err)
}
// The evil file must not have been created outside the artwork dir.
evil := filepath.Join(dst, "etc", "evil")
if _, err := os.Stat(evil); err == nil {
t.Error("path traversal: file was created outside artwork dir")
}
}

func TestRestoreBackup_NotGzip(t *testing.T) {
dst := t.TempDir()
err := restoreBackup(strings.NewReader("not a gzip file"), filepath.Join(dst, "library.db"), filepath.Join(dst, "artwork"))
if err == nil {
t.Error("expected error for non-gzip input")
}
}

// --- HTTP restore handler ---

func TestRestoreHandler_MethodNotAllowed(t *testing.T) {
mux := http.NewServeMux()
registerRestoreRoute(mux, "/nonexistent/library.db", "/tmp")

r := httptest.NewRequest(http.MethodGet, "/api/library/import/backup", nil)
w := httptest.NewRecorder()
mux.ServeHTTP(w, r)

if w.Code != http.StatusMethodNotAllowed {
t.Errorf("GET should return 405, got %d", w.Code)
}
}

func TestRestoreHandler_MissingFile(t *testing.T) {
mux := http.NewServeMux()
registerRestoreRoute(mux, "/nonexistent/library.db", "/tmp")

var body bytes.Buffer
mw := multipart.NewWriter(&body)
mw.Close()
r := httptest.NewRequest(http.MethodPost, "/api/library/import/backup", &body)
r.Header.Set("Content-Type", mw.FormDataContentType())
w := httptest.NewRecorder()
mux.ServeHTTP(w, r)

if w.Code != http.StatusBadRequest {
t.Errorf("missing backup field should return 400, got %d", w.Code)
}
}

func TestRestoreHandler_Success(t *testing.T) {
src := t.TempDir()
dbPath := createTestDB(t, src, nil)

lib, err := openLibraryDB(dbPath)
if err != nil || lib == nil {
t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
}
backupPath := filepath.Join(src, "backup.tar.gz")
if err := lib.generateBackup(backupPath, filepath.Join(src, "artwork")); err != nil {
t.Fatalf("generateBackup: %v", err)
}
lib.close()

backupData, err := os.ReadFile(backupPath)
if err != nil {
t.Fatalf("read backup: %v", err)
}

dst := t.TempDir()
destDB := filepath.Join(dst, "library.db")
destArt := filepath.Join(dst, "artwork")

mux := http.NewServeMux()
registerRestoreRoute(mux, destDB, destArt)

archive := bytes.NewBuffer(backupData)
req := buildRestoreRequest(t, archive)
w := httptest.NewRecorder()
mux.ServeHTTP(w, req)

if w.Code != http.StatusOK {
t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
}
if _, err := os.Stat(destDB); err != nil {
t.Errorf("restored db not found: %v", err)
}
}
