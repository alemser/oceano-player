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
	// v2: add fingerprint column for local acoustic fingerprinting via fpcalc/Chromaprint.
	`ALTER TABLE collection ADD COLUMN fingerprint TEXT`,
	// v3: unique index on fingerprint for fast lookup.
	// SQLite treats NULL values as distinct in unique indexes, so multiple rows
	// may have fingerprint=NULL (for pre-fingerprint entries).
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_collection_fingerprint ON collection(fingerprint)`,
	// v4: replace the fingerprint index with a partial unique index so missing
	// fingerprints stored as NULL or '' do not conflict with each other.
	`DROP INDEX IF EXISTS idx_collection_fingerprint`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_collection_fingerprint
		ON collection(fingerprint)
		WHERE fingerprint IS NOT NULL AND fingerprint != ''`,
	// v6: dedicated fingerprints table — allows multiple fingerprints per track.
	// Each capture of the same track may produce a different fingerprint depending
	// on the capture offset; storing all observed fingerprints maximises cache hits.
	// Existing records without a fingerprint accumulate entries here on re-recognition.
	`CREATE TABLE IF NOT EXISTS track_fingerprints (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		fingerprint   TEXT    NOT NULL,
		UNIQUE(fingerprint)
	)`,
	// v7: backfill existing collection.fingerprint values into track_fingerprints.
	`INSERT OR IGNORE INTO track_fingerprints (collection_id, fingerprint)
		SELECT id, fingerprint FROM collection
		WHERE fingerprint IS NOT NULL AND fingerprint != ''`,
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

// LookupByFingerprint searches the collection by Chromaprint acoustic fingerprint.
// Returns (nil, nil) when no matching track is found.
func (l *Library) LookupByFingerprint(fp string) (*CollectionEntry, error) {
	if fp == "" {
		return nil, nil
	}
	row := l.db.QueryRow(`
		SELECT c.id, COALESCE(c.acrid,''), c.title, c.artist,
		       COALESCE(c.album,''), COALESCE(c.label,''), COALESCE(c.released,''),
		       COALESCE(c.score,0), COALESCE(c.format,'Unknown'),
		       COALESCE(c.track_number,''), COALESCE(c.artwork_path,''),
		       c.play_count, c.first_played, c.last_played
		FROM track_fingerprints tf
		JOIN collection c ON tf.collection_id = c.id
		WHERE tf.fingerprint = ?`, fp)

	var e CollectionEntry
	err := row.Scan(&e.ID, &e.ACRID, &e.Title, &e.Artist,
		&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
		&e.TrackNumber, &e.ArtworkPath,
		&e.PlayCount, &e.FirstPlayed, &e.LastPlayed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("library: lookup by fingerprint: %w", err)
	}
	return &e, nil
}

// RecordPlay upserts a track into the collection by acrid (or fingerprint when
// acrid is empty) and increments its play count.
// User-edited fields (track_number, artwork_path, format) are never overwritten
// by ACRCloud data — only updated when the new score is higher.
// fingerprint is the Chromaprint fingerprint from fpcalc; pass empty string when
// unavailable. If the track already exists in the collection (matched by acrid or
// an existing fingerprint) and lacks a fingerprint, this fingerprint is stored —
// so existing records accumulate fingerprints over time and cache hits increase.
func (l *Library) RecordPlay(result *RecognitionResult, artworkPath, fingerprint string) error {
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
				score        = CASE WHEN excluded.score > score THEN excluded.score   ELSE score   END,
				artwork_path = CASE WHEN (artwork_path IS NULL OR artwork_path = '') AND excluded.artwork_path != ''
				               THEN excluded.artwork_path ELSE artwork_path END`,
			result.ACRID, result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now,
		)
		if err != nil {
			return err
		}
		if fingerprint != "" {
			var id int64
			if err := l.db.QueryRow(`SELECT id FROM collection WHERE acrid = ?`, result.ACRID).Scan(&id); err != nil {
				return fmt.Errorf("library: get collection id: %w", err)
			}
			return l.addFingerprint(id, fingerprint)
		}
		return nil
	}

	if fingerprint != "" {
		// No ACRID but fingerprint present.
		// Check whether this fingerprint already maps to a collection entry.
		var id int64
		err := l.db.QueryRow(
			`SELECT collection_id FROM track_fingerprints WHERE fingerprint = ?`, fingerprint,
		).Scan(&id)

		if err == nil {
			// Known fingerprint: update play count on the linked entry.
			_, err = l.db.Exec(
				`UPDATE collection SET play_count = play_count + 1, last_played = ? WHERE id = ?`,
				now, id,
			)
			return err
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("library: fingerprint lookup: %w", err)
		}

		// New fingerprint: create a new collection entry and link the fingerprint.
		res, err := l.db.Exec(`
			INSERT INTO collection
				(title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played)
			VALUES (?,?,?,?,?,?,?,1,?,?)`,
			result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now,
		)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return l.addFingerprint(id, fingerprint)
	}

	// Fallback: no acrid and no fingerprint — match by title+artist.
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

// loadFingerprints returns all fingerprints stored for a collection entry.
func (l *Library) loadFingerprints(collectionID int64) ([]string, error) {
	rows, err := l.db.Query(
		`SELECT fingerprint FROM track_fingerprints WHERE collection_id = ? ORDER BY id`,
		collectionID,
	)
	if err != nil {
		return nil, fmt.Errorf("library: load fingerprints: %w", err)
	}
	defer rows.Close()
	var fps []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, fmt.Errorf("library: scan fingerprint: %w", err)
		}
		fps = append(fps, fp)
	}
	return fps, rows.Err()
}

// addFingerprint stores a fingerprint for a collection entry.
// If the fingerprint already exists (for this or any other entry), it is silently skipped.
func (l *Library) addFingerprint(collectionID int64, fingerprint string) error {
	if fingerprint == "" {
		return nil
	}
	_, err := l.db.Exec(
		`INSERT OR IGNORE INTO track_fingerprints (collection_id, fingerprint) VALUES (?, ?)`,
		collectionID, fingerprint,
	)
	return err
}

// Close closes the underlying database connection.
func (l *Library) Close() error {
	return l.db.Close()
}
