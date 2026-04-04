package main

import (
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

// ── HTTP handlers ──────────────────────────────────────────────────────────

// registerLibraryRoutes wires all /api/library/* endpoints into mux.
// libraryDBPath is read from the running state-manager service file so the web
// UI always talks to the same database without extra configuration.
func registerLibraryRoutes(mux *http.ServeMux, libraryDBPath string, stateFilePath string, artworkDir string) {
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
			handleUploadArtwork(w, r, lib, id, managedArtworkDir(libraryDBPath, artworkDir))
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
