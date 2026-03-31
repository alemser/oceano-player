package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// migrations is an ordered list of SQL statements that evolve the schema.
// To add a new migration, append a new entry — never modify existing ones.
var migrations = []string{
	// v1: collection — one row per known track, keyed by ACRCloud acrid.
	// track_number is a free-form label (e.g. "1", "2", "1A", "1B") used to
	// group tracks into albums/sides for future album view.
	`CREATE TABLE collection (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		acrid        TEXT    UNIQUE,
		title        TEXT    NOT NULL,
		artist       TEXT    NOT NULL,
		album        TEXT,
		label        TEXT,
		released     TEXT,
		score        INTEGER,
		format       TEXT    CHECK(format IN ('Vinyl','CD','Unknown')) DEFAULT 'Unknown',
		track_number TEXT,
		artwork_path TEXT,
		play_count   INTEGER NOT NULL DEFAULT 1,
		first_played TEXT    NOT NULL,
		last_played  TEXT    NOT NULL
	)`,
}

// Library persists physical-media recognition results to a local SQLite
// collection. The ACRCloud acrid is the primary lookup key — it is stable
// across different audio captures of the same recording.
type Library struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and applies any pending
// migrations. Safe to call on an existing populated database.
func Open(path string) (*Library, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("library: mkdir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("library: open: %w", err)
	}

	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("library: pragma wal: %w", err)
	}

	l := &Library{db: db}
	if err := l.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return l, nil
}

// migrate creates the schema_migrations tracking table if absent, then applies
// any numbered migrations that have not yet been recorded.
func (l *Library) migrate() error {
	if _, err := l.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY
	)`); err != nil {
		return fmt.Errorf("library: bootstrap migrations table: %w", err)
	}

	for i, stmt := range migrations {
		version := i + 1
		var exists int
		_ = l.db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
		if exists == 1 {
			continue
		}
		if _, err := l.db.Exec(stmt); err != nil {
			return fmt.Errorf("library: migration v%d: %w", version, err)
		}
		if _, err := l.db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			return fmt.Errorf("library: record migration v%d: %w", version, err)
		}
		log.Printf("library: applied migration v%d", version)
	}
	return nil
}

// CollectionEntry is a row from the collection table.
type CollectionEntry struct {
	ID          int64
	ACRID       string
	Title       string
	Artist      string
	Album       string
	Label       string
	Released    string
	Score       int
	Format      string // "Vinyl" | "CD" | "Unknown"
	TrackNumber string // e.g. "1", "2", "1A", "1B", "2B"
	ArtworkPath string
	PlayCount   int
	FirstPlayed string
	LastPlayed  string
}

// Lookup searches the collection by ACRCloud acrid.
// Returns (nil, nil) when the track is not yet in the collection.
func (l *Library) Lookup(acrid string) (*CollectionEntry, error) {
	if acrid == "" {
		return nil, nil
	}
	row := l.db.QueryRow(`
		SELECT id, COALESCE(acrid,''), title, artist,
		       COALESCE(album,''), COALESCE(label,''), COALESCE(released,''),
		       COALESCE(score,0), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       play_count, first_played, last_played
		FROM collection WHERE acrid = ?`, acrid)

	var e CollectionEntry
	err := row.Scan(&e.ID, &e.ACRID, &e.Title, &e.Artist,
		&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
		&e.TrackNumber, &e.ArtworkPath,
		&e.PlayCount, &e.FirstPlayed, &e.LastPlayed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("library: lookup: %w", err)
	}
	return &e, nil
}

// RecordPlay upserts a track into the collection by acrid and increments its
// play count. When acrid is empty, falls back to matching by (title, artist).
// User-edited fields (track_number, artwork_path, format) are never overwritten
// by ACRCloud data — only updated when the new score is higher.
func (l *Library) RecordPlay(result *RecognitionResult, artworkPath string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	if result.ACRID != "" {
		_, err := l.db.Exec(`
			INSERT INTO collection
				(acrid, title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played)
			VALUES (?,?,?,?,?,?,?,?,1,?,?)
			ON CONFLICT(acrid) DO UPDATE SET
				play_count   = play_count + 1,
				last_played  = excluded.last_played,
				title        = CASE WHEN excluded.score > score THEN excluded.title   ELSE title   END,
				artist       = CASE WHEN excluded.score > score THEN excluded.artist  ELSE artist  END,
				album        = CASE WHEN excluded.score > score THEN excluded.album   ELSE album   END,
				score        = CASE WHEN excluded.score > score THEN excluded.score   ELSE score   END`,
			result.ACRID, result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now,
		)
		return err
	}

	// Fallback: no acrid — match by title+artist.
	var id int64
	err := l.db.QueryRow(
		`SELECT id FROM collection WHERE title = ? AND artist = ?`,
		result.Title, result.Artist,
	).Scan(&id)

	if err == sql.ErrNoRows {
		_, err = l.db.Exec(`
			INSERT INTO collection
				(title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played)
			VALUES (?,?,?,?,?,?,?,1,?,?)`,
			result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now,
		)
		return err
	}
	if err != nil {
		return fmt.Errorf("library: fallback lookup: %w", err)
	}
	_, err = l.db.Exec(
		`UPDATE collection SET play_count = play_count + 1, last_played = ? WHERE id = ?`,
		now, id,
	)
	return err
}

// Close closes the underlying database connection.
func (l *Library) Close() error {
	return l.db.Close()
}
