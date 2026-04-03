package main

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// LibraryEntry is the JSON representation of a collection row.
type LibraryEntry struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	Label       string `json:"label"`
	Released    string `json:"released"`
	Format      string `json:"format"`
	TrackNumber string `json:"track_number"`
	ArtworkPath string `json:"artwork_path"`
	PlayCount   int    `json:"play_count"`
	FirstPlayed string `json:"first_played"`
	LastPlayed  string `json:"last_played"`
}

// LibraryDB wraps the collection SQLite database for the web UI.
// It is intentionally a separate type from the state-manager Library so the
// web binary has no compile-time dependency on the state-manager package.
type LibraryDB struct {
	db   *sql.DB
	path string
}

// openLibraryDB opens the SQLite database at path (read-write).
// Returns nil without error when the file does not exist yet.
func openLibraryDB(path string) (*LibraryDB, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("library: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	return &LibraryDB{db: db, path: path}, nil
}

func (l *LibraryDB) close() {
	if l != nil && l.db != nil {
		l.db.Close()
	}
}

// recentArtworks returns the last 8 distinct entries that have artwork,
// ordered by last_played. Used to populate the artwork picker in the edit modal.
func (l *LibraryDB) recentArtworks() ([]LibraryEntry, error) {
	rows, err := l.db.Query(`
		SELECT id, title, artist, COALESCE(album,''), COALESCE(artwork_path,'')
		FROM collection
		WHERE artwork_path IS NOT NULL AND artwork_path != ''
		ORDER BY last_played DESC LIMIT 8`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LibraryEntry
	for rows.Next() {
		var e LibraryEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Artist, &e.Album, &e.ArtworkPath); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// list returns all entries ordered by last_played descending.
func (l *LibraryDB) list() ([]LibraryEntry, error) {
	rows, err := l.db.Query(`
		SELECT id, title, artist, COALESCE(album,''), COALESCE(label,''),
		       COALESCE(released,''), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       play_count, first_played, last_played
		FROM collection ORDER BY last_played DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LibraryEntry
	for rows.Next() {
		var e LibraryEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Artist, &e.Album, &e.Label,
			&e.Released, &e.Format, &e.TrackNumber, &e.ArtworkPath,
			&e.PlayCount, &e.FirstPlayed, &e.LastPlayed); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// update patches editable fields for a single entry.
func (l *LibraryDB) update(id int64, title, artist, album, label, released, format, trackNumber, artworkPath string) error {
	_, err := l.db.Exec(`
		UPDATE collection
		SET title=?, artist=?, album=?, label=?, released=?, format=?, track_number=?, artwork_path=?
		WHERE id=?`,
		title, artist, album, label, released, format, trackNumber, artworkPath, id,
	)
	return err
}

// deleteEntry removes a single entry by ID.
func (l *LibraryDB) deleteEntry(id int64) error {
	_, err := l.db.Exec(`DELETE FROM collection WHERE id=?`, id)
	return err
}

// generateBackup creates a compressed archive at destPath containing:
//   - library.db  (a clean copy of the SQLite database via VACUUM INTO)
//   - artwork/*   (all image files referenced by collection rows)
//   - restore.sh  (a script that copies both back to their original locations)
func (l *LibraryDB) generateBackup(destPath string) error {
	// 1. Create a clean database copy using VACUUM INTO so the archive
	//    always contains a self-consistent snapshot.
	tmpDB, err := os.CreateTemp("", "oceano-db-backup-*.db")
	if err != nil {
		return fmt.Errorf("backup: temp db: %w", err)
	}
	tmpDBPath := tmpDB.Name()
	tmpDB.Close()
	defer os.Remove(tmpDBPath)

	if _, err := l.db.Exec(`VACUUM INTO ?`, tmpDBPath); err != nil {
		return fmt.Errorf("backup: vacuum into: %w", err)
	}

	// 2. Collect distinct artwork paths referenced by the collection.
	rows, err := l.db.Query(`
		SELECT DISTINCT artwork_path FROM collection
		WHERE artwork_path IS NOT NULL AND artwork_path != ''`)
	if err != nil {
		return fmt.Errorf("backup: query artworks: %w", err)
	}
	var artworks []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return fmt.Errorf("backup: scan artwork: %w", err)
		}
		artworks = append(artworks, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backup: artworks: %w", err)
	}

	// 3. Create the .tar.gz archive.
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("backup: create archive: %w", err)
	}
	defer f.Close()

	gw, err := gzip.NewWriterLevel(f, gzip.DefaultCompression)
	if err != nil {
		return fmt.Errorf("backup: gzip writer: %w", err)
	}
	tw := tar.NewWriter(gw)

	addFile := func(srcPath, arcName string) error {
		fi, err := os.Stat(srcPath)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:    arcName,
			Size:    fi.Size(),
			Mode:    int64(fi.Mode().Perm()),
			ModTime: fi.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tw, src)
		return err
	}

	// Add database snapshot.
	if err := addFile(tmpDBPath, "library.db"); err != nil {
		return fmt.Errorf("backup: add db: %w", err)
	}

	// Add artwork files from the managed artwork directory only
	// (skip missing/unresolvable files and deduplicate by archive name).
	allowedArtworkDir, err := filepath.Abs(filepath.Join(filepath.Dir(l.path), "artwork"))
	if err != nil {
		return fmt.Errorf("backup: resolve artwork dir: %w", err)
	}
	if resolvedAllowedArtworkDir, err := filepath.EvalSymlinks(allowedArtworkDir); err == nil {
		allowedArtworkDir = resolvedAllowedArtworkDir
	}

	seenArtworks := make(map[string]bool)
	for _, ap := range artworks {
		if ap == "" {
			continue
		}

		info, err := os.Lstat(ap)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}

		resolvedPath, err := filepath.EvalSymlinks(ap)
		if err != nil {
			continue
		}

		relToAllowedDir, err := filepath.Rel(allowedArtworkDir, resolvedPath)
		if err != nil || relToAllowedDir == ".." || strings.HasPrefix(relToAllowedDir, ".."+string(os.PathSeparator)) {
			continue
		}

		arcName := filepath.Join("artwork", filepath.Base(resolvedPath))
		if seenArtworks[arcName] {
			continue
		}
		seenArtworks[arcName] = true
		if err := addFile(resolvedPath, arcName); err != nil {
			return fmt.Errorf("backup: add artwork %s: %w", resolvedPath, err)
		}
	}

	// Add restore script.
	script := restoreScriptContent(l.path)
	hdr := &tar.Header{
		Name:    "restore.sh",
		Size:    int64(len(script)),
		Mode:    0o755,
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: restore script header: %w", err)
	}
	if _, err := io.WriteString(tw, script); err != nil {
		return fmt.Errorf("backup: restore script body: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("backup: tar close: %w", err)
	}
	return gw.Close()
}

// restoreScriptContent returns a bash script that restores the database and
// artwork files from the extracted archive back to their original locations.
func restoreScriptContent(dbPath string) string {
	artworkDir := filepath.Join(filepath.Dir(dbPath), "artwork")
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
# Oceano collection restore script.
# Extract the archive, then run: bash restore.sh
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DB_DEST="%s"
ARTWORK_DEST="%s"

cp "$SCRIPT_DIR/library.db" "$DB_DEST"
echo "Database restored to $DB_DEST"

if [ -d "$SCRIPT_DIR/artwork" ]; then
  mkdir -p "$ARTWORK_DEST"
  cp -r "$SCRIPT_DIR/artwork/." "$ARTWORK_DEST/"
  echo "Artwork restored to $ARTWORK_DEST"
fi

echo "Restore complete."
`, dbPath, artworkDir)
}

// ── HTTP handlers ──────────────────────────────────────────────────────────

// registerLibraryRoutes wires all /api/library/* endpoints into mux.
// libraryDBPath is read from the running state-manager service file so the web
// UI always talks to the same database without extra configuration.
func registerLibraryRoutes(mux *http.ServeMux, libraryDBPath string, stateFilePath string) {
	// GET  /api/library        → list all entries
	// PUT  /api/library/{id}   → update entry metadata
	// DELETE /api/library/{id} → remove entry
	// POST /api/library/{id}/artwork → upload artwork image
	// GET  /api/library/{id}/artwork → serve artwork file

	mux.HandleFunc("/api/library", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		defer lib.close()

		entries, err := lib.list()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []LibraryEntry{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	})

	// GET /api/library/export/backup — generate and download a full backup archive.
	// Each request generates a fresh archive containing the database, all artwork
	// images referenced by collection rows, and a bash restore script.
	mux.HandleFunc("/api/library/export/backup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib == nil {
			http.Error(w, "library not initialised", http.StatusServiceUnavailable)
			return
		}
		defer lib.close()

		tmp, err := os.CreateTemp("", "oceano-backup-*.tar.gz")
		if err != nil {
			http.Error(w, "cannot create backup", http.StatusInternalServerError)
			return
		}
		tmpPath := tmp.Name()
		tmp.Close()
		defer os.Remove(tmpPath)

		if err := lib.generateBackup(tmpPath); err != nil {
			http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		bf, err := os.Open(tmpPath)
		if err != nil {
			http.Error(w, "backup unavailable", http.StatusInternalServerError)
			return
		}
		defer bf.Close()
		fi, _ := bf.Stat()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="oceano-backup.tar.gz"`)
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
		io.Copy(w, bf) //nolint:errcheck
	})

	// GET /api/library/artworks — recent tracks with artwork, for the picker.
	mux.HandleFunc("/api/library/artworks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		defer lib.close()

		entries, err := lib.recentArtworks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []LibraryEntry{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	})

	mux.HandleFunc("/api/library/", func(w http.ResponseWriter, r *http.Request) {
		// Path is either /api/library/{id} or /api/library/{id}/artwork
		path := strings.TrimPrefix(r.URL.Path, "/api/library/")
		parts := strings.SplitN(path, "/", 2)
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			http.Error(w, "invalid id", http.StatusBadRequest)
			return
		}
		sub := ""
		if len(parts) == 2 {
			sub = parts[1]
		}

		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib == nil {
			http.Error(w, "library not initialised", http.StatusServiceUnavailable)
			return
		}
		defer lib.close()

		switch {
		case sub == "artwork" && r.Method == http.MethodGet:
			handleGetArtwork(w, r, lib, id)
		case sub == "artwork" && r.Method == http.MethodPost:
			handleUploadArtwork(w, r, lib, id, libraryDBPath)
		case sub == "" && r.Method == http.MethodPut:
			handleUpdateEntry(w, r, lib, id, stateFilePath)
		case sub == "" && r.Method == http.MethodDelete:
			handleDeleteEntry(w, lib, id)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})
}

func handleGetArtwork(w http.ResponseWriter, r *http.Request, lib *LibraryDB, id int64) {
	var artworkPath string
	err := lib.db.QueryRow(`SELECT COALESCE(artwork_path,'') FROM collection WHERE id=?`, id).Scan(&artworkPath)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil || artworkPath == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, artworkPath)
}

func handleUploadArtwork(w http.ResponseWriter, r *http.Request, lib *LibraryDB, id int64, dbPath string) {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20) // 5 MB limit

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	file, header, err := r.FormFile("artwork")
	if err != nil {
		http.Error(w, "missing artwork field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
		http.Error(w, "only jpg/png accepted", http.StatusBadRequest)
		return
	}

	artworkDir := filepath.Join(filepath.Dir(dbPath), "artwork")
	if err := os.MkdirAll(artworkDir, 0o755); err != nil {
		http.Error(w, "cannot create artwork dir", http.StatusInternalServerError)
		return
	}

	destPath := filepath.Join(artworkDir, fmt.Sprintf("%d-%d%s", id, time.Now().UnixNano(), ext))
	dst, err := os.Create(destPath)
	if err != nil {
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "write error", http.StatusInternalServerError)
		return
	}

	if _, err := lib.db.Exec(`UPDATE collection SET artwork_path=? WHERE id=?`, destPath, id); err != nil {
		http.Error(w, "db update error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"artwork_path": destPath})
}

func handleUpdateEntry(w http.ResponseWriter, r *http.Request, lib *LibraryDB, id int64, stateFilePath string) {
	var body struct {
		Title       string `json:"title"`
		Artist      string `json:"artist"`
		Album       string `json:"album"`
		Label       string `json:"label"`
		Released    string `json:"released"`
		Format      string `json:"format"`
		TrackNumber string `json:"track_number"`
		ArtworkPath string `json:"artwork_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Title == "" || body.Artist == "" {
		http.Error(w, "title and artist are required", http.StatusBadRequest)
		return
	}
	// Validate format
	switch body.Format {
	case "Vinyl", "CD", "Unknown", "":
	default:
		http.Error(w, "format must be Vinyl, CD or Unknown", http.StatusBadRequest)
		return
	}
	if body.Format == "" {
		body.Format = "Unknown"
	}

	if err := lib.update(id, body.Title, body.Artist, body.Album, body.Label, body.Released, body.Format, body.TrackNumber, body.ArtworkPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	patchStateFile(stateFilePath, body.Title, body.Artist, body.Album, body.Format, body.ArtworkPath)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// patchStateFile updates the live state JSON if a physical track is currently
// playing. Since only one physical source is ever active, any entry being
// edited must be the one on screen.
func patchStateFile(path, title, artist, album, format, artworkPath string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state struct {
		Source    string          `json:"source"`
		State     string          `json:"state"`
		Track     json.RawMessage `json:"track"`
		UpdatedAt string          `json:"updated_at"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	// Only patch when a physical source is active with recognised track metadata.
	switch state.Source {
	case "Physical", "CD", "Vinyl":
	default:
		return
	}
	if string(state.Track) == "null" || len(state.Track) == 0 {
		return
	}

	var track map[string]interface{}
	if err := json.Unmarshal(state.Track, &track); err != nil {
		return
	}
	track["title"] = title
	track["artist"] = artist
	track["album"] = album
	if artworkPath != "" {
		track["artwork_path"] = artworkPath
	}
	tb, err := json.Marshal(track)
	if err != nil {
		return
	}
	state.Track = json.RawMessage(tb)
	if format == "CD" || format == "Vinyl" {
		state.Source = format
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func handleDeleteEntry(w http.ResponseWriter, lib *LibraryDB, id int64) {
	if err := lib.deleteEntry(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
