package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	ACRID       string `json:"acrid"`
	ShazamID    string `json:"shazam_id"`
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
	// IsFingerprintStub is true for unresolved stub rows (user_confirmed = 0)
	// that have fingerprints attached.
	IsFingerprintStub bool `json:"is_fingerprint_stub"`
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
	l := &LibraryDB{db: db, path: path}
	// Enable WAL mode for concurrent access (readers + writer don't block each other)
	// This is essential since both the web UI and state-manager write to the database.
	if err := l.db.Ping(); err != nil {
		l.close()
		_ = l.db.Close()
		return nil, fmt.Errorf("library: ping after open: %w", err)
	}
	if _, err := l.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = l.db.Close()
		return nil, fmt.Errorf("library: set PRAGMA journal_mode=WAL: %w", err)
	}
	if _, err := l.db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		_ = l.db.Close()
		return nil, fmt.Errorf("library: set PRAGMA synchronous=NORMAL: %w", err)
	}
	if _, err := l.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		_ = l.db.Close()
		return nil, fmt.Errorf("library: set PRAGMA foreign_keys=ON: %w", err)
	}
	if _, err := l.db.Exec(`CREATE TABLE IF NOT EXISTS fingerprints (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		data     TEXT    NOT NULL
	)`); err != nil {
		_ = l.db.Close()
		return nil, fmt.Errorf("library: ensure fingerprints table exists: %w", err)
	}
	if _, err := l.db.Exec(`CREATE INDEX IF NOT EXISTS fingerprints_entry_id ON fingerprints(entry_id)`); err != nil {
		_ = l.db.Close()
		return nil, fmt.Errorf("library: ensure fingerprints index exists: %w", err)
	}
	// Ensure columns added by state-manager migrations are present.
	// ALTER TABLE returns an error if the column already exists; that is safe to ignore.
	ensureCols := []string{
		`ALTER TABLE collection ADD COLUMN acrid TEXT`,
		`ALTER TABLE collection ADD COLUMN shazam_id TEXT`,
		`ALTER TABLE collection ADD COLUMN user_confirmed INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS recognition_summary (
			provider TEXT,
			event    TEXT,
			count    INTEGER DEFAULT 0,
			PRIMARY KEY(provider, event)
		)`,
	}
	for _, stmt := range ensureCols {
		if _, err := l.db.Exec(stmt); err != nil {
			errText := strings.ToLower(err.Error())
			if !strings.Contains(errText, "duplicate column name") && !strings.Contains(errText, "already exists") {
				_ = l.db.Close()
				return nil, fmt.Errorf("library: ensure column exists (%s): %w", stmt, err)
			}
		}
	}
	if _, err := l.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS collection_acrid_uq ON collection(acrid) WHERE acrid IS NOT NULL AND acrid != ''`); err != nil {
		_ = l.db.Close()
		return nil, fmt.Errorf("library: ensure acrid index: %w", err)
	}
	if _, err := l.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS collection_shazam_id_uq ON collection(shazam_id) WHERE shazam_id IS NOT NULL AND shazam_id != ''`); err != nil {
		_ = l.db.Close()
		return nil, fmt.Errorf("library: ensure shazam_id index: %w", err)
	}
	return l, nil
}

// isConstraintError reports whether err is a SQLite unique/primary-key constraint violation.
func isConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "constraint failed")
}

// getRecognitionStats returns a map of provider -> event -> count.
func (l *LibraryDB) getRecognitionStats() (map[string]map[string]int, error) {
	rows, err := l.db.Query(`SELECT provider, event, count FROM recognition_summary`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]map[string]int)
	for rows.Next() {
		var p, e string
		var c int
		if err := rows.Scan(&p, &e, &c); err != nil {
			return nil, err
		}
		if _, ok := stats[p]; !ok {
			stats[p] = make(map[string]int)
		}
		stats[p][e] = c
	}
	return stats, nil
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
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist, COALESCE(album,''), COALESCE(label,''),
		       COALESCE(released,''), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       play_count, first_played, last_played,
		       CASE WHEN user_confirmed = 0
		                 AND EXISTS (SELECT 1 FROM fingerprints f WHERE f.entry_id = collection.id)
		            THEN 1 ELSE 0 END AS is_fingerprint_stub
		FROM collection ORDER BY last_played DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LibraryEntry
	for rows.Next() {
		var e LibraryEntry
		var stub int
		if err := rows.Scan(&e.ID, &e.ACRID, &e.ShazamID, &e.Title, &e.Artist, &e.Album, &e.Label, &e.Released, &e.Format,
			&e.TrackNumber, &e.ArtworkPath, &e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &stub); err != nil {
			return nil, err
		}
		e.IsFingerprintStub = (stub == 1)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// search returns user-confirmed tracks matching title/artist/album query.
// Stub rows (empty metadata) are excluded by design.
func (l *LibraryDB) search(q string, limit int) ([]LibraryEntry, error) {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return []LibraryEntry{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	like := "%" + q + "%"
	rows, err := l.db.Query(`
		SELECT id, title, artist, COALESCE(album,''), COALESCE(format,'Unknown')
		FROM collection
		WHERE user_confirmed = 1
		  AND title != ''
		  AND artist != ''
		  AND (LOWER(title) LIKE ? OR LOWER(artist) LIKE ? OR LOWER(COALESCE(album,'')) LIKE ?)
		ORDER BY last_played DESC
		LIMIT ?`, like, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]LibraryEntry, 0, limit)
	for rows.Next() {
		var e LibraryEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Artist, &e.Album, &e.Format); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// resolveFingerprintStub attaches all fingerprints from stubID to targetID,
// merges play counters, and removes the unresolved stub row.
func (l *LibraryDB) resolveFingerprintStub(stubID, targetID int64) (*LibraryEntry, error) {
	if stubID <= 0 || targetID <= 0 {
		return nil, fmt.Errorf("stub_id and target_id are required")
	}
	if stubID == targetID {
		return nil, fmt.Errorf("stub_id and target_id must be different")
	}

	tx, err := l.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var stubPlayCount int
	var stubLastPlayed, stubACRID, stubShazamID string
	err = tx.QueryRow(`
		SELECT play_count, last_played, COALESCE(acrid,''), COALESCE(shazam_id,'')
		FROM collection
		WHERE id = ? AND user_confirmed = 0`, stubID).
		Scan(&stubPlayCount, &stubLastPlayed, &stubACRID, &stubShazamID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("entry %d is not an unresolved fingerprint stub", stubID)
	}
	if err != nil {
		return nil, err
	}

	var fpCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM fingerprints WHERE entry_id = ?`, stubID).Scan(&fpCount); err != nil {
		return nil, err
	}
	if fpCount == 0 {
		return nil, fmt.Errorf("stub %d has no fingerprints", stubID)
	}

	target := &LibraryEntry{}
	err = tx.QueryRow(`
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist, COALESCE(album,''), COALESCE(format,'Unknown'),
		       COALESCE(artwork_path,''), play_count, last_played
		FROM collection
		WHERE id = ? AND title != '' AND artist != ''`, targetID).
		Scan(&target.ID, &target.ACRID, &target.ShazamID, &target.Title, &target.Artist, &target.Album, &target.Format,
			&target.ArtworkPath, &target.PlayCount, &target.LastPlayed)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("target %d is not a valid identified track", targetID)
	}
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`UPDATE fingerprints SET entry_id = ? WHERE entry_id = ?`, targetID, stubID); err != nil {
		return nil, err
	}

	// Update target with stub's provider IDs if target's fields are empty.
	// Unique constraint violations are expected when another track already holds that ID — skip silently.
	if target.ACRID == "" && stubACRID != "" {
		if _, err := tx.Exec(`UPDATE collection SET acrid = ? WHERE id = ?`, stubACRID, targetID); err != nil {
			if !isConstraintError(err) {
				log.Printf("library: resolveFingerprintStub: set acrid on target %d: %v", targetID, err)
			}
		}
	}
	if target.ShazamID == "" && stubShazamID != "" {
		if _, err := tx.Exec(`UPDATE collection SET shazam_id = ? WHERE id = ?`, stubShazamID, targetID); err != nil {
			if !isConstraintError(err) {
				log.Printf("library: resolveFingerprintStub: set shazam_id on target %d: %v", targetID, err)
			}
		}
	}

	if _, err := tx.Exec(`
		UPDATE collection
		SET play_count = play_count + ?,
		    last_played = CASE WHEN last_played < ? THEN ? ELSE last_played END,
		    user_confirmed = 1
		WHERE id = ?`, stubPlayCount, stubLastPlayed, stubLastPlayed, targetID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM collection WHERE id = ?`, stubID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	target.PlayCount += stubPlayCount
	if stubLastPlayed > target.LastPlayed {
		target.LastPlayed = stubLastPlayed
	}
	return target, nil
}

// update patches editable fields for a single entry.
func (l *LibraryDB) update(id int64, title, artist, album, label, released, format, trackNumber, artworkPath string) error {
	_, err := l.db.Exec(`
		UPDATE collection
		SET title=?, artist=?, album=?, label=?, released=?, format=?, track_number=?, artwork_path=?,
		    user_confirmed=1
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

// ── HTTP handlers ──────────────────────────────────────────────────────────

// registerLibraryRoutes wires all /api/library/* endpoints into mux.
// libraryDBPath is read from the running state-manager service file so the web
// UI always talks to the same database without extra configuration.
func registerLibraryRoutes(mux *http.ServeMux, libraryDBPath string, stateFilePath string, artworkDir string) {
	// GET  /api/library        → list all entries
	// GET  /api/library/search?q=...&limit=20 → search confirmed tracks
	// PUT  /api/library/{id}   → update entry metadata
	// DELETE /api/library/{id} → remove entry
	// POST /api/library/{id}/artwork → upload artwork image
	// POST /api/library/{id}/resolve → resolve fingerprint stub to existing track
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

	mux.HandleFunc("/api/library/search", func(w http.ResponseWriter, r *http.Request) {
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

		limit := 20
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				limit = n
			}
		}
		entries, err := lib.search(r.URL.Query().Get("q"), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	})

	// GET /api/recognition/stats — get stats for ACRCloud, Shazam, Fingerprint.
	mux.HandleFunc("/api/recognition/stats", func(w http.ResponseWriter, r *http.Request) {
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
			w.Write([]byte("{}"))
			return
		}
		defer lib.close()

		stats, err := lib.getRecognitionStats()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
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
			handleUploadArtwork(w, r, lib, id, artworkDir)
		case sub == "resolve" && r.Method == http.MethodPost:
			handleResolveStub(w, r, lib, id, stateFilePath)
		case sub == "" && r.Method == http.MethodPut:
			handleUpdateEntry(w, r, lib, id, stateFilePath)
		case sub == "" && r.Method == http.MethodDelete:
			handleDeleteEntry(w, lib, id)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})
}

func handleResolveStub(w http.ResponseWriter, r *http.Request, lib *LibraryDB, stubID int64, stateFilePath string) {
	var body struct {
		TargetID int64 `json:"target_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	target, err := lib.resolveFingerprintStub(stubID, body.TargetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	patchStateFile(stateFilePath, target.Title, target.Artist, target.Album, target.Format, target.ArtworkPath)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"target":  target,
		"stub_id": stubID,
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

func handleUploadArtwork(w http.ResponseWriter, r *http.Request, lib *LibraryDB, id int64, artworkDir string) {
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
	body.Format = strings.TrimSpace(body.Format)
	body.TrackNumber = strings.TrimSpace(body.TrackNumber)
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
		Format    string          `json:"format,omitempty"`
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
	var track map[string]interface{}
	if string(state.Track) != "null" && len(state.Track) > 0 {
		if err := json.Unmarshal(state.Track, &track); err != nil {
			// If unmarshal fails, we'll just overwrite it.
			track = make(map[string]interface{})
		}
	} else {
		track = make(map[string]interface{})
	}

	track["title"] = title
	track["artist"] = artist
	track["album"] = album
	track["format"] = format
	if artworkPath != "" {
		track["artwork_path"] = artworkPath
	}

	tb, err := json.Marshal(track)
	if err != nil {
		return
	}
	state.Track = json.RawMessage(tb)
	normFormat := strings.TrimSpace(format)
	if normFormat == "CD" || normFormat == "Vinyl" {
		state.Source = normFormat
		state.Format = normFormat
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
