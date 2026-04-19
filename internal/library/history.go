package library

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// PlayHistoryEntry represents one play event in play_history.
type PlayHistoryEntry struct {
	ID                   int64
	CollectionID         int64 // 0 when track is not in local collection
	Title                string
	Artist               string
	Album                string
	TrackNumber          string
	Source               string // "AirPlay" | "Bluetooth" | "Physical"
	MediaFormat          string // "CD" | "Vinyl" | ""
	VinylSide            string // "A" | "B" | ""
	SampleRate           string
	BitDepth             string
	Codec                string
	ArtworkPath          string
	ArtworkSource        string // "recognized" | "none"
	RecognitionScore     int
	RecognitionProvider  string // "acrcloud" | "shazam" | "local" | ""
	RecognitionConfirmed bool
	MatchedLibrary       bool
	StartedAt            string // RFC3339
	EndedAt              string // RFC3339, empty while still playing
	ListenedSeconds      int
	DurationMs           int
	ISRC                 string
}

// PlayHistoryStats holds aggregated listening statistics.
type PlayHistoryStats struct {
	TotalPlays         int                 `json:"total_plays"`
	TotalListenedHours float64             `json:"total_listened_hours"`
	TopArtists         []PlayHistoryArtist `json:"top_artists"`
	TopAlbums          []PlayHistoryAlbum  `json:"top_albums"`
	PlaysBySource      map[string]int      `json:"plays_by_source"`
	Heatmap            map[string]int      `json:"heatmap"` // "YYYY-MM-DD" → count (last 365 days)
}

// PlayHistoryArtist is one row in the top-artists list.
type PlayHistoryArtist struct {
	Artist string `json:"artist"`
	Plays  int    `json:"plays"`
}

// PlayHistoryAlbum is one row in the top-albums list.
type PlayHistoryAlbum struct {
	Album  string `json:"album"`
	Artist string `json:"artist"`
	Plays  int    `json:"plays"`
}

// VinylSideFromTrackNumber extracts the side letter from a vinyl track number
// like "A2" → "A". Returns "" when the format is not recognised.
func VinylSideFromTrackNumber(tn string) string {
	if len(tn) == 0 {
		return ""
	}
	side := strings.ToUpper(string([]rune(tn)[0]))
	switch side {
	case "A", "B", "C", "D":
		return side
	}
	return ""
}

// OpenPlayHistory inserts an in-progress play record and returns the row ID.
// Returns 0 without error when the library is nil (recording disabled).
func (l *Library) OpenPlayHistory(e PlayHistoryEntry) (int64, error) {
	if l == nil || l.db == nil {
		return 0, nil
	}
	if e.StartedAt == "" {
		e.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	var collID *int64
	if e.CollectionID > 0 {
		collID = &e.CollectionID
	}
	var id int64
	err := l.db.QueryRow(`
		INSERT INTO play_history (
			collection_id, title, artist, album, track_number,
			source, media_format, vinyl_side,
			samplerate, bitdepth, codec,
			artwork_path, artwork_source,
			recognition_score, recognition_provider, recognition_confirmed, matched_library,
			started_at, duration_ms, isrc
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		RETURNING id`,
		collID, e.Title, e.Artist, e.Album, e.TrackNumber,
		e.Source, e.MediaFormat, e.VinylSide,
		e.SampleRate, e.BitDepth, e.Codec,
		e.ArtworkPath, e.ArtworkSource,
		e.RecognitionScore, e.RecognitionProvider,
		boolInt(e.RecognitionConfirmed), boolInt(e.MatchedLibrary),
		e.StartedAt, e.DurationMs, e.ISRC,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("history: open play: %w", err)
	}
	return id, nil
}

// UpdateOpenPlayHistory refreshes metadata on an in-progress play record.
// Safe to call with id=0 (no-op).
func (l *Library) UpdateOpenPlayHistory(id int64, e PlayHistoryEntry) error {
	if l == nil || l.db == nil || id <= 0 {
		return nil
	}
	var collID *int64
	if e.CollectionID > 0 {
		collID = &e.CollectionID
	}
	_, err := l.db.Exec(`
		UPDATE play_history
		SET collection_id           = ?,
		    title                   = ?,
		    artist                  = ?,
		    album                   = ?,
		    track_number            = ?,
		    source                  = ?,
		    media_format            = ?,
		    vinyl_side              = ?,
		    samplerate              = ?,
		    bitdepth                = ?,
		    codec                   = ?,
		    artwork_path            = ?,
		    artwork_source          = ?,
		    recognition_score       = ?,
		    recognition_provider    = ?,
		    recognition_confirmed   = ?,
		    matched_library         = ?,
		    started_at              = COALESCE(NULLIF(?, ''), started_at),
		    duration_ms             = ?,
		    isrc                    = ?
		WHERE id = ? AND ended_at IS NULL`,
		collID, e.Title, e.Artist, e.Album, e.TrackNumber,
		e.Source, e.MediaFormat, e.VinylSide,
		e.SampleRate, e.BitDepth, e.Codec,
		e.ArtworkPath, e.ArtworkSource,
		e.RecognitionScore, e.RecognitionProvider,
		boolInt(e.RecognitionConfirmed), boolInt(e.MatchedLibrary),
		e.StartedAt, e.DurationMs, e.ISRC,
		id)
	if err != nil {
		return fmt.Errorf("history: update open play: %w", err)
	}
	return nil
}

// ClosePlayHistory sets ended_at and listened_seconds on an open play record.
// Safe to call with id=0 (no-op).
func (l *Library) ClosePlayHistory(id int64, endedAt time.Time) {
	if l == nil || l.db == nil || id <= 0 {
		return
	}
	endStr := endedAt.UTC().Format(time.RFC3339)
	_, err := l.db.Exec(`
		UPDATE play_history
		SET ended_at         = ?,
		    listened_seconds = CAST(
		        (julianday(?) - julianday(started_at)) * 86400 AS INTEGER
		    )
		WHERE id = ? AND ended_at IS NULL`,
		endStr, endStr, id)
	if err != nil {
		log.Printf("history: close play id=%d: %v", id, err)
	}
}

// ListPlayHistory returns play_history rows ordered by started_at DESC.
// Returns the slice, the total row count, and any error.
func (l *Library) ListPlayHistory(limit, offset int) ([]PlayHistoryEntry, int, error) {
	if l == nil || l.db == nil {
		return nil, 0, nil
	}
	var total int
	if err := l.db.QueryRow(`SELECT COUNT(*) FROM play_history`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("history: count: %w", err)
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := l.db.Query(`
		SELECT id, COALESCE(collection_id, 0),
		       COALESCE(title,''), COALESCE(artist,''), COALESCE(album,''),
		       COALESCE(track_number,''), COALESCE(source,''),
		       COALESCE(media_format,''), COALESCE(vinyl_side,''),
		       COALESCE(samplerate,''), COALESCE(bitdepth,''), COALESCE(codec,''),
		       COALESCE(artwork_path,''), COALESCE(artwork_source,''),
		       COALESCE(recognition_score,0), COALESCE(recognition_provider,''),
		       COALESCE(recognition_confirmed,0), COALESCE(matched_library,0),
		       started_at, COALESCE(ended_at,''),
		       COALESCE(listened_seconds,0), COALESCE(duration_ms,0),
		       COALESCE(isrc,'')
		FROM play_history
		ORDER BY started_at DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("history: list: %w", err)
	}
	defer rows.Close()

	var entries []PlayHistoryEntry
	for rows.Next() {
		var e PlayHistoryEntry
		var confirmed, matched int
		if err := rows.Scan(
			&e.ID, &e.CollectionID,
			&e.Title, &e.Artist, &e.Album,
			&e.TrackNumber, &e.Source,
			&e.MediaFormat, &e.VinylSide,
			&e.SampleRate, &e.BitDepth, &e.Codec,
			&e.ArtworkPath, &e.ArtworkSource,
			&e.RecognitionScore, &e.RecognitionProvider,
			&confirmed, &matched,
			&e.StartedAt, &e.EndedAt,
			&e.ListenedSeconds, &e.DurationMs,
			&e.ISRC,
		); err != nil {
			return nil, 0, fmt.Errorf("history: scan: %w", err)
		}
		e.RecognitionConfirmed = confirmed == 1
		e.MatchedLibrary = matched == 1
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// GetPlayHistoryStats returns aggregated listening statistics.
func (l *Library) GetPlayHistoryStats() (*PlayHistoryStats, error) {
	if l == nil || l.db == nil {
		return &PlayHistoryStats{PlaysBySource: map[string]int{}, Heatmap: map[string]int{}}, nil
	}
	stats := &PlayHistoryStats{
		PlaysBySource: make(map[string]int),
		Heatmap:       make(map[string]int),
	}

	// Total plays + listened hours — close rows before next query (MaxOpenConns=1)
	l.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(listened_seconds), 0) / 3600.0
		FROM play_history`).Scan(&stats.TotalPlays, &stats.TotalListenedHours)

	// Top artists (last 30 days)
	rows, err := l.db.Query(`
		SELECT artist, COUNT(*) AS cnt
		FROM play_history
		WHERE artist != '' AND started_at >= datetime('now', '-30 days')
		GROUP BY artist ORDER BY cnt DESC LIMIT 5`)
	if err == nil {
		for rows.Next() {
			var a PlayHistoryArtist
			if rows.Scan(&a.Artist, &a.Plays) == nil {
				stats.TopArtists = append(stats.TopArtists, a)
			}
		}
		rows.Close()
	}

	// Top albums (last 30 days)
	rows, err = l.db.Query(`
		SELECT album, artist, COUNT(*) AS cnt
		FROM play_history
		WHERE album != '' AND started_at >= datetime('now', '-30 days')
		GROUP BY album, artist ORDER BY cnt DESC LIMIT 5`)
	if err == nil {
		for rows.Next() {
			var a PlayHistoryAlbum
			if rows.Scan(&a.Album, &a.Artist, &a.Plays) == nil {
				stats.TopAlbums = append(stats.TopAlbums, a)
			}
		}
		rows.Close()
	}

	// Plays by source (all time)
	rows, err = l.db.Query(`SELECT source, COUNT(*) FROM play_history GROUP BY source`)
	if err == nil {
		for rows.Next() {
			var src string
			var cnt int
			if rows.Scan(&src, &cnt) == nil {
				stats.PlaysBySource[src] = cnt
			}
		}
		rows.Close()
	}

	// Heatmap: last 365 days
	rows, err = l.db.Query(`
		SELECT date(started_at), COUNT(*)
		FROM play_history
		WHERE started_at >= datetime('now', '-365 days')
		GROUP BY date(started_at)`)
	if err == nil {
		for rows.Next() {
			var d string
			var cnt int
			if rows.Scan(&d, &cnt) == nil {
				stats.Heatmap[d] = cnt
			}
		}
		rows.Close()
	}

	return stats, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
