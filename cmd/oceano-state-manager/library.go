package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

	// v2: user_confirmed — 1 when the entry has reliable metadata (either
	// identified by ACRCloud or manually confirmed by the user in the library
	// editor). Only user_confirmed=1 entries cause ACRCloud to be skipped on
	// a local fingerprint hit. Existing rows default to 0 — no behaviour change.
	`ALTER TABLE collection ADD COLUMN user_confirmed INTEGER NOT NULL DEFAULT 0`,

	// v3: fingerprints — one row per Chromaprint fingerprint window captured
	// during a recognition attempt. Multiple rows per collection entry allow
	// the sliding-window BER matcher to compensate for timing jitter between
	// plays. Cascade delete keeps the table clean when an entry is removed.
	`CREATE TABLE fingerprints (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		data     TEXT    NOT NULL
	)`,
	`CREATE INDEX fingerprints_entry_id ON fingerprints(entry_id)`,

	// v5–v7: placeholders for migrations that existed in older deployments.
	// These slots must remain to keep version numbers aligned with databases
	// that were created before this migration sequence was consolidated.
	`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
	`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
	`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,

	// v8: ensure the fingerprints table exists on databases that were created
	// by an older schema where the table was named 'track_fingerprints'.
	// The CREATE is a no-op if 'fingerprints' already exists (fresh installs).
	`CREATE TABLE IF NOT EXISTS fingerprints (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		data     TEXT    NOT NULL
	)`,

	// v9: placeholder — the original v9 attempted CREATE INDEX but could fail on
	// databases where the renamed 'fingerprints' table had different column names.
	// Replaced by v10–v12 which rebuild the table with the correct schema.
	`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,

	// v10–v12: rebuild fingerprints with the correct schema. Older deployments may
	// have had the table renamed from 'track_fingerprints' with incompatible columns.
	// Dropping and recreating is safe — fingerprints are re-captured on next play.
	`DROP TABLE IF EXISTS fingerprints`,
	`CREATE TABLE fingerprints (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		data     TEXT    NOT NULL
	)`,
	`CREATE INDEX fingerprints_entry_id ON fingerprints(entry_id)`,
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
	ID            int64
	ACRID         string
	Title         string
	Artist        string
	Album         string
	Label         string
	Released      string
	Score         int
	Format        string // "Vinyl" | "CD" | "Unknown"
	TrackNumber   string // e.g. "1", "2", "1A", "1B", "2B"
	ArtworkPath   string
	PlayCount     int
	FirstPlayed   string
	LastPlayed    string
	UserConfirmed bool // true = skip ACRCloud on fingerprint hit
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
		       play_count, first_played, last_played, user_confirmed
		FROM collection WHERE acrid = ?`, acrid)

	var e CollectionEntry
	var confirmed int
	err := row.Scan(&e.ID, &e.ACRID, &e.Title, &e.Artist,
		&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
		&e.TrackNumber, &e.ArtworkPath,
		&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed)
	e.UserConfirmed = confirmed == 1
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
// Sets user_confirmed=1 so future fingerprint hits skip ACRCloud.
// Returns the entry ID so the caller can associate fingerprints with this play.
func (l *Library) RecordPlay(result *RecognitionResult, artworkPath string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	if result.ACRID != "" {
		var id int64
		err := l.db.QueryRow(`
			INSERT INTO collection
				(acrid, title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played, user_confirmed)
			VALUES (?,?,?,?,?,?,?,?,1,?,?,1)
			ON CONFLICT(acrid) DO UPDATE SET
				play_count     = play_count + 1,
				last_played    = excluded.last_played,
				user_confirmed = 1,
				title        = CASE WHEN excluded.score > score THEN excluded.title   ELSE title   END,
				artist       = CASE WHEN excluded.score > score THEN excluded.artist  ELSE artist  END,
				album        = CASE WHEN excluded.score > score THEN excluded.album   ELSE album   END,
				score        = CASE WHEN excluded.score > score THEN excluded.score   ELSE score   END,
				artwork_path = CASE WHEN (artwork_path IS NULL OR artwork_path = '') AND excluded.artwork_path != ''
				               THEN excluded.artwork_path ELSE artwork_path END
			RETURNING id`,
			result.ACRID, result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now,
		).Scan(&id)
		return id, err
	}

	// Fallback: no acrid — match by title+artist.
	var id int64
	err := l.db.QueryRow(
		`SELECT id FROM collection WHERE title = ? AND artist = ?`,
		result.Title, result.Artist,
	).Scan(&id)

	if err == sql.ErrNoRows {
		err = l.db.QueryRow(`
			INSERT INTO collection
				(title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played, user_confirmed)
			VALUES (?,?,?,?,?,?,?,1,?,?,1)
			RETURNING id`,
			result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now,
		).Scan(&id)
		return id, err
	}
	if err != nil {
		return 0, fmt.Errorf("library: fallback lookup: %w", err)
	}
	_, err = l.db.Exec(
		`UPDATE collection SET play_count = play_count + 1, last_played = ?, user_confirmed = 1 WHERE id = ?`,
		now, id,
	)
	return id, err
}

// UpsertStub finds an existing entry matching any of the given fingerprints or
// creates a new stub entry with empty metadata. In both cases it stores the
// new fingerprints and increments the play count.
// Stubs have user_confirmed=0 so ACRCloud is still attempted on future plays
// until the user fills in metadata via the library editor.
func (l *Library) UpsertStub(fps []Fingerprint, threshold float64, maxShift int) (*CollectionEntry, error) {
	if len(fps) == 0 {
		return nil, fmt.Errorf("library: UpsertStub: no fingerprints provided")
	}

	// Try to find an existing entry via fingerprint.
	entry, err := l.FindByFingerprints(fps, threshold, maxShift)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if entry != nil {
		// Known stub or confirmed entry — just bump the play count.
		if _, err := l.db.Exec(
			`UPDATE collection SET play_count = play_count + 1, last_played = ? WHERE id = ?`,
			now, entry.ID,
		); err != nil {
			return nil, fmt.Errorf("library: stub update: %w", err)
		}
		_ = l.SaveFingerprints(entry.ID, fps)
		return entry, nil
	}

	// New unrecognised track — create a stub with empty metadata.
	var id int64
	if err := l.db.QueryRow(`
		INSERT INTO collection (title, artist, play_count, first_played, last_played, user_confirmed)
		VALUES ('','',1,?,?,0)
		RETURNING id`, now, now,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("library: stub insert: %w", err)
	}
	if err := l.SaveFingerprints(id, fps); err != nil {
		return nil, fmt.Errorf("library: stub save fingerprints: %w", err)
	}

	return &CollectionEntry{
		ID:          id,
		FirstPlayed: now,
		LastPlayed:  now,
		PlayCount:   1,
	}, nil
}

// SaveFingerprints stores all fingerprint windows for an entry in a single
// transaction. All inserts succeed or none do.
func (l *Library) SaveFingerprints(entryID int64, fps []Fingerprint) error {
	tx, err := l.db.Begin()
	if err != nil {
		return fmt.Errorf("library: save fingerprints begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(`INSERT INTO fingerprints (entry_id, data) VALUES (?,?)`)
	if err != nil {
		return fmt.Errorf("library: save fingerprints prepare: %w", err)
	}
	defer stmt.Close()

	for _, fp := range fps {
		if len(fp) == 0 {
			continue
		}
		parts := make([]string, len(fp))
		for i, v := range fp {
			parts[i] = strconv.FormatUint(uint64(v), 10)
		}
		if _, err := stmt.Exec(entryID, strings.Join(parts, ",")); err != nil {
			return fmt.Errorf("library: save fingerprint: %w", err)
		}
	}
	return tx.Commit()
}

// FindByFingerprints scans all stored fingerprints in a single pass and returns
// the collection entry with the lowest BER across all query fingerprints fps,
// provided that BER is below threshold.
// Using multiple query fingerprints (captured at different offsets) reduces
// false negatives when a single window does not align well with stored windows.
// The scan is O(stored_fingerprints × len(fps)); for typical library sizes
// (~1 000 entries × 2 stored windows × 2 query windows) this is well under 10 ms.
// Returns (nil, nil) when no match is found.
func (l *Library) FindByFingerprints(fps []Fingerprint, threshold float64, maxShift int) (*CollectionEntry, error) {
	if len(fps) == 0 {
		return nil, nil
	}

	rows, err := l.db.Query(`
		SELECT f.entry_id, f.data,
		       COALESCE(c.acrid,''), c.title, c.artist,
		       COALESCE(c.album,''), COALESCE(c.label,''), COALESCE(c.released,''),
		       COALESCE(c.score,0), COALESCE(c.format,'Unknown'),
		       COALESCE(c.track_number,''), COALESCE(c.artwork_path,''),
		       c.play_count, c.first_played, c.last_played, c.user_confirmed
		FROM fingerprints f
		JOIN collection c ON c.id = f.entry_id`)
	if err != nil {
		return nil, fmt.Errorf("library: fingerprint scan: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		entry CollectionEntry
		ber   float64
	}
	best := candidate{ber: threshold}

	for rows.Next() {
		var entryID int64
		var data string
		var e CollectionEntry
		var confirmed int
		if err := rows.Scan(
			&entryID, &data,
			&e.ACRID, &e.Title, &e.Artist,
			&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
			&e.TrackNumber, &e.ArtworkPath,
			&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed,
		); err != nil {
			return nil, fmt.Errorf("library: fingerprint row scan: %w", err)
		}
		e.ID = entryID
		e.UserConfirmed = confirmed == 1

		stored, parseErr := ParseFingerprint(data)
		if parseErr != nil {
			continue
		}
		// Score this stored fingerprint against every query window; take the best.
		for _, fp := range fps {
			if b := BER(fp, stored, maxShift); b < best.ber {
				best = candidate{entry: e, ber: b}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: fingerprint scan rows: %w", err)
	}
	if best.ber >= threshold {
		return nil, nil
	}
	return &best.entry, nil
}

// PruneStub removes a stub entry (title='', artist='', user_confirmed=0) by ID.
// Called after ACRCloud identifies a track that was previously stubbed, so the
// orphaned stub does not remain in the library. The CASCADE on the fingerprints
// table removes associated fingerprint rows automatically.
// Safe to call on non-stub entries — the WHERE guard prevents accidental deletion.
func (l *Library) PruneStub(id int64) error {
	_, err := l.db.Exec(
		`DELETE FROM collection WHERE id=? AND title='' AND artist='' AND user_confirmed=0`,
		id,
	)
	return err
}

// PruneMatchingStubs scans all unconfirmed stubs (title='', artist='') and
// deletes any whose stored fingerprints match fps below threshold.
// excludeID is the just-identified entry so it is never accidentally deleted.
// Called after a successful ACRCloud recognition to clean up stubs that were
// created during earlier no-match cycles for the same track.
func (l *Library) PruneMatchingStubs(fps []Fingerprint, threshold float64, maxShift int, excludeID int64) {
	rows, err := l.db.Query(`
		SELECT f.entry_id, f.data FROM fingerprints f
		JOIN collection c ON c.id = f.entry_id
		WHERE c.user_confirmed = 0 AND c.title = '' AND c.artist = ''
		  AND f.entry_id != ?`, excludeID)
	if err != nil {
		return
	}
	defer rows.Close()

	matched := make(map[int64]bool)
	for rows.Next() {
		var entryID int64
		var data string
		if err := rows.Scan(&entryID, &data); err != nil {
			continue
		}
		stored, err := ParseFingerprint(data)
		if err != nil {
			continue
		}
		for _, fp := range fps {
			if BER(fp, stored, maxShift) < threshold {
				matched[entryID] = true
				break
			}
		}
	}
	rows.Close()

	for id := range matched {
		if _, err := l.db.Exec(
			`DELETE FROM collection WHERE id=? AND title='' AND artist='' AND user_confirmed=0`, id,
		); err == nil {
			log.Printf("library: pruned orphaned stub %d", id)
		}
	}
}

// Close closes the underlying database connection.
func (l *Library) Close() error {
	return l.db.Close()
}
