package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	_ "modernc.org/sqlite"
)

// historyDB wraps the library SQLite database for history queries in the web UI.
// It operates read-only and does not depend on the state-manager library package.
type historyDB struct {
	db *sql.DB
}

func openHistoryDB(path string) (*historyDB, error) {
	db, err := openLibraryDB(path)
	if err != nil || db == nil {
		return nil, err
	}
	return &historyDB{db: db.db}, nil
}

// PlayHistoryItem is the JSON shape served to the UI.
type PlayHistoryItem struct {
	ID                  int64  `json:"id"`
	CollectionID        int64  `json:"collection_id,omitempty"`
	Title               string `json:"title"`
	Artist              string `json:"artist"`
	Album               string `json:"album"`
	TrackNumber         string `json:"track_number,omitempty"`
	Source              string `json:"source"`
	MediaFormat         string `json:"media_format,omitempty"`
	VinylSide           string `json:"vinyl_side,omitempty"`
	SampleRate          string `json:"samplerate,omitempty"`
	BitDepth            string `json:"bitdepth,omitempty"`
	Codec               string `json:"codec,omitempty"`
	ArtworkPath         string `json:"artwork_path,omitempty"`
	RecognitionScore    int    `json:"recognition_score,omitempty"`
	RecognitionProvider string `json:"recognition_provider,omitempty"`
	MatchedLibrary      bool   `json:"matched_library,omitempty"`
	StartedAt           string `json:"started_at"`
	EndedAt             string `json:"ended_at,omitempty"`
	ListenedSeconds     int    `json:"listened_seconds"`
	DurationMs          int    `json:"duration_ms,omitempty"`
}

type playHistoryListResponse struct {
	Plays  []PlayHistoryItem `json:"plays"`
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

type playHistoryStatsResponse struct {
	TotalPlays         int                     `json:"total_plays"`
	TotalListenedHours float64                 `json:"total_listened_hours"`
	TopArtists         []playHistoryArtistStat `json:"top_artists"`
	TopAlbums          []playHistoryAlbumStat  `json:"top_albums"`
	PlaysBySource      map[string]int          `json:"plays_by_source"`
	HoursBySource      map[string]float64      `json:"hours_by_source"`
}

type playHistoryArtistStat struct {
	Artist string `json:"artist"`
	Plays  int    `json:"plays"`
}

type playHistoryAlbumStat struct {
	Album  string `json:"album"`
	Artist string `json:"artist"`
	Plays  int    `json:"plays"`
}

func registerHistoryRoutes(mux *http.ServeMux, dbPath string) {
	h, err := openHistoryDB(dbPath)
	if err != nil || h == nil {
		return
	}

	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		plays, total, err := h.listPlays(limit, offset)
		if err != nil {
			jsonError(w, "db error", http.StatusInternalServerError)
			return
		}
		jsonOK(w, playHistoryListResponse{
			Plays:  plays,
			Total:  total,
			Limit:  limit,
			Offset: offset,
		})
	})

	mux.HandleFunc("/api/history/artwork/", func(w http.ResponseWriter, r *http.Request) {
		idStr := r.URL.Path[len("/api/history/artwork/"):]
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		var artworkPath string
		if err := h.db.QueryRow(`SELECT COALESCE(artwork_path,'') FROM play_history WHERE id=?`, id).Scan(&artworkPath); err != nil || artworkPath == "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, artworkPath)
	})

	mux.HandleFunc("/api/history/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		stats, err := h.getStats()
		if err != nil {
			jsonError(w, "db error", http.StatusInternalServerError)
			return
		}
		jsonOK(w, stats)
	})
}

func (h *historyDB) listPlays(limit, offset int) ([]PlayHistoryItem, int, error) {
	var total int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM play_history`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("history: count: %w", err)
	}

	rows, err := h.db.Query(`
		SELECT ph.id, COALESCE(ph.collection_id,0),
		       COALESCE(NULLIF(c.title,''),  ph.title,  ''),
		       COALESCE(NULLIF(c.artist,''), ph.artist, ''),
		       COALESCE(NULLIF(c.album,''),  ph.album,  ''),
		       COALESCE(NULLIF(c.track_number,''), ph.track_number, ''),
		       COALESCE(ph.source,''),
		       COALESCE(NULLIF(c.format,''), ph.media_format, ''),
		       COALESCE(ph.vinyl_side,''),
		       COALESCE(ph.samplerate,''), COALESCE(ph.bitdepth,''), COALESCE(ph.codec,''),
		       COALESCE(NULLIF(c.artwork_path,''), ph.artwork_path, ''),
		       COALESCE(ph.recognition_score,0), COALESCE(ph.recognition_provider,''),
		       COALESCE(ph.matched_library,0),
		       ph.started_at, COALESCE(ph.ended_at,''),
		       COALESCE(ph.listened_seconds,0), COALESCE(ph.duration_ms,0)
		FROM play_history ph
		LEFT JOIN collection c ON c.id = ph.collection_id
		ORDER BY ph.started_at DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("history: list: %w", err)
	}
	defer rows.Close()

	var items []PlayHistoryItem
	for rows.Next() {
		var it PlayHistoryItem
		var matched int
		if err := rows.Scan(
			&it.ID, &it.CollectionID,
			&it.Title, &it.Artist, &it.Album,
			&it.TrackNumber, &it.Source,
			&it.MediaFormat, &it.VinylSide,
			&it.SampleRate, &it.BitDepth, &it.Codec,
			&it.ArtworkPath,
			&it.RecognitionScore, &it.RecognitionProvider,
			&matched,
			&it.StartedAt, &it.EndedAt,
			&it.ListenedSeconds, &it.DurationMs,
		); err != nil {
			return nil, 0, fmt.Errorf("history: scan: %w", err)
		}
		it.MatchedLibrary = matched == 1
		items = append(items, it)
	}
	if items == nil {
		items = []PlayHistoryItem{}
	}
	return items, total, rows.Err()
}

// mediaKey returns a display key for a (source, media_format) pair, collapsing
// "Physical/Vinyl" → "Vinyl" and "Physical/CD" → "CD" for clearer UI labelling.
const mediaKeySQLExpr = `CASE
		WHEN source='Physical' AND lower(media_format)='vinyl' THEN 'Vinyl'
		WHEN source='Physical' AND lower(media_format)='cd'    THEN 'CD'
		ELSE source
	END`

func (h *historyDB) getStats() (*playHistoryStatsResponse, error) {
	stats := &playHistoryStatsResponse{
		PlaysBySource: make(map[string]int),
		HoursBySource: make(map[string]float64),
	}

	h.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(listened_seconds),0) / 3600.0
		FROM play_history`).Scan(&stats.TotalPlays, &stats.TotalListenedHours)

	rows, err := h.db.Query(`
		SELECT artist, COUNT(*) AS cnt FROM play_history
		WHERE artist != '' AND started_at >= datetime('now','-30 days')
		GROUP BY artist ORDER BY cnt DESC LIMIT 5`)
	if err == nil {
		for rows.Next() {
			var a playHistoryArtistStat
			rows.Scan(&a.Artist, &a.Plays)
			stats.TopArtists = append(stats.TopArtists, a)
		}
		rows.Close()
	}

	rows, err = h.db.Query(`
		SELECT album, artist, COUNT(*) AS cnt FROM play_history
		WHERE album != '' AND started_at >= datetime('now','-30 days')
		GROUP BY album, artist ORDER BY cnt DESC LIMIT 5`)
	if err == nil {
		for rows.Next() {
			var a playHistoryAlbumStat
			rows.Scan(&a.Album, &a.Artist, &a.Plays)
			stats.TopAlbums = append(stats.TopAlbums, a)
		}
		rows.Close()
	}

	// Play counts and listening hours grouped by media key (Vinyl/CD/AirPlay/…).
	rows, err = h.db.Query(`
		SELECT ` + mediaKeySQLExpr + ` AS src_key,
		       COUNT(*),
		       COALESCE(SUM(listened_seconds),0) / 3600.0
		FROM play_history
		GROUP BY src_key`)
	if err == nil {
		for rows.Next() {
			var src string
			var cnt int
			var hrs float64
			rows.Scan(&src, &cnt, &hrs)
			stats.PlaysBySource[src] = cnt
			stats.HoursBySource[src] = hrs
		}
		rows.Close()
	}

	if stats.TopArtists == nil {
		stats.TopArtists = []playHistoryArtistStat{}
	}
	if stats.TopAlbums == nil {
		stats.TopAlbums = []playHistoryAlbumStat{}
	}

	return stats, nil
}

// jsonOK and jsonError are defined in amplifier_api.go; referenced here for clarity.
var _ = json.Marshal
