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
	// v1: initial schema — physical media recognition results
	`CREATE TABLE plays (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		played_at    TEXT    NOT NULL,
		title        TEXT    NOT NULL,
		artist       TEXT    NOT NULL,
		album        TEXT,
		label        TEXT,
		released     TEXT,
		score        INTEGER,
		artwork_path TEXT,
		source       TEXT    NOT NULL DEFAULT 'Physical'
	)`,
}

// Library persists physical-media recognition results to a local SQLite database.
// All methods are safe for concurrent use; only one writer goroutine is expected.
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

	// Single writer; WAL mode for concurrent reads by future tools (e.g. web UI).
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

// RecordPlay inserts a recognised track into the library.
// Errors are logged by the caller and do not interrupt normal playback.
func (l *Library) RecordPlay(result *RecognitionResult, artworkPath string) error {
	_, err := l.db.Exec(
		`INSERT INTO plays (played_at, title, artist, album, label, released, score, artwork_path)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339),
		result.Title,
		result.Artist,
		result.Album,
		result.Label,
		result.Released,
		result.Score,
		artworkPath,
	)
	if err != nil {
		return fmt.Errorf("library: insert play: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (l *Library) Close() error {
	return l.db.Close()
}
