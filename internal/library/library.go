package library

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alemser/oceano-player/internal/recognition"
	_ "modernc.org/sqlite"
)

var migrations = []string{
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
	`ALTER TABLE collection ADD COLUMN user_confirmed INTEGER NOT NULL DEFAULT 0`,
	`CREATE TABLE fingerprints (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		data     TEXT    NOT NULL
	)`,
	`CREATE INDEX fingerprints_entry_id ON fingerprints(entry_id)`,
	// Legacy no-op placeholders kept to preserve historical migration numbering.
	`SELECT 1`,
	`SELECT 1`,
	`SELECT 1`,
	`CREATE TABLE IF NOT EXISTS fingerprints (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		data     TEXT    NOT NULL
	)`,
	// Legacy no-op placeholder kept to preserve historical migration numbering.
	`SELECT 1`,
	`DROP TABLE IF EXISTS fingerprints`,
	`CREATE TABLE fingerprints (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		data     TEXT    NOT NULL
	)`,
	`CREATE INDEX fingerprints_entry_id ON fingerprints(entry_id)`,
	`ALTER TABLE collection ADD COLUMN shazam_id TEXT`,
	`CREATE UNIQUE INDEX IF NOT EXISTS collection_shazam_id_uq ON collection(shazam_id) WHERE shazam_id IS NOT NULL AND shazam_id != ''`,
	`CREATE TABLE recognition_summary (
		provider TEXT,
		event    TEXT,
		count    INTEGER DEFAULT 0,
		PRIMARY KEY(provider, event)
	)`,
	`ALTER TABLE collection ADD COLUMN isrc TEXT`,
	`ALTER TABLE collection ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE collection ADD COLUMN duration_fp_elapsed_ms INTEGER NOT NULL DEFAULT 0`,
	`CREATE TABLE play_history (
		id                    INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_id         INTEGER REFERENCES collection(id),
		title                 TEXT    NOT NULL DEFAULT '',
		artist                TEXT    NOT NULL DEFAULT '',
		album                 TEXT    NOT NULL DEFAULT '',
		track_number          TEXT    NOT NULL DEFAULT '',
		source                TEXT    NOT NULL DEFAULT '',
		media_format          TEXT    NOT NULL DEFAULT '',
		vinyl_side            TEXT    NOT NULL DEFAULT '',
		samplerate            TEXT    NOT NULL DEFAULT '',
		bitdepth              TEXT    NOT NULL DEFAULT '',
		codec                 TEXT    NOT NULL DEFAULT '',
		artwork_path          TEXT    NOT NULL DEFAULT '',
		artwork_source        TEXT    NOT NULL DEFAULT '',
		recognition_score     INTEGER NOT NULL DEFAULT 0,
		recognition_provider  TEXT    NOT NULL DEFAULT '',
		recognition_confirmed INTEGER NOT NULL DEFAULT 0,
		matched_library       INTEGER NOT NULL DEFAULT 0,
		started_at            TEXT    NOT NULL,
		ended_at              TEXT,
		listened_seconds      INTEGER NOT NULL DEFAULT 0,
		duration_ms           INTEGER NOT NULL DEFAULT 0,
		isrc                  TEXT    NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX play_history_started_at ON play_history(started_at)`,
	// Fingerprints are no longer used by the runtime path and are dropped to
	// avoid maintaining stale schema objects.
	`DROP TABLE IF EXISTS fingerprints`,
	`CREATE TABLE boundary_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		occurred_at TEXT NOT NULL,
		outcome TEXT NOT NULL,
		boundary_type TEXT NOT NULL DEFAULT '',
		is_hard INTEGER NOT NULL DEFAULT 0,
		physical_source TEXT NOT NULL DEFAULT '',
		format_at_event TEXT NOT NULL DEFAULT '',
		format_resolved TEXT,
		format_resolved_at TEXT,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		seek_ms INTEGER NOT NULL DEFAULT 0,
		play_history_id INTEGER REFERENCES play_history(id),
		collection_id INTEGER REFERENCES collection(id)
	)`,
	`CREATE INDEX IF NOT EXISTS boundary_events_occurred_at_idx ON boundary_events(occurred_at)`,
	// R7: link boundary_events to post-recognition outcomes + early-boundary cohort flag.
	`ALTER TABLE boundary_events ADD COLUMN followup_outcome TEXT`,
	`ALTER TABLE boundary_events ADD COLUMN followup_acrid TEXT`,
	`ALTER TABLE boundary_events ADD COLUMN followup_shazam_id TEXT`,
	`ALTER TABLE boundary_events ADD COLUMN followup_collection_id INTEGER`,
	`ALTER TABLE boundary_events ADD COLUMN followup_play_history_id INTEGER`,
	`ALTER TABLE boundary_events ADD COLUMN followup_new_recording INTEGER`,
	`ALTER TABLE boundary_events ADD COLUMN early_boundary INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE boundary_events ADD COLUMN followup_recorded_at TEXT`,
	// R8: per-track hint for stricter VU / duration-guard behaviour.
	`ALTER TABLE collection ADD COLUMN boundary_sensitive INTEGER NOT NULL DEFAULT 0`,
	// RMS percentile learning: histograms of stable silence vs stable music (per format).
	`CREATE TABLE rms_learning (
		format_key TEXT NOT NULL PRIMARY KEY,
		updated_at TEXT NOT NULL,
		bins INTEGER NOT NULL,
		max_rms REAL NOT NULL,
		silence_counts TEXT NOT NULL,
		music_counts TEXT NOT NULL,
		silence_total INTEGER NOT NULL DEFAULT 0,
		music_total INTEGER NOT NULL DEFAULT 0,
		derived_enter REAL,
		derived_exit REAL
	)`,
	// Merge legacy recognition_summary provider key "Shazam" into "Shazamio" (Recognizer.Name).
	`INSERT INTO recognition_summary (provider, event, count)
		SELECT 'Shazamio', event, count FROM recognition_summary WHERE provider = 'Shazam'
		ON CONFLICT(provider, event) DO UPDATE SET count = count + excluded.count`,
	`DELETE FROM recognition_summary WHERE provider = 'Shazam'`,
	// Merge stats bucket "ShazamContinuity" → "ShazamioContinuity" (wrapWithStatsAs role name).
	`INSERT INTO recognition_summary (provider, event, count)
		SELECT 'ShazamioContinuity', event, count FROM recognition_summary WHERE provider = 'ShazamContinuity'
		ON CONFLICT(provider, event) DO UPDATE SET count = count + excluded.count`,
	`DELETE FROM recognition_summary WHERE provider = 'ShazamContinuity'`,
	// Client sync: monotonic library_version + changelog for incremental library API.
	`CREATE TABLE IF NOT EXISTS oceano_library_sync (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		library_version INTEGER NOT NULL DEFAULT 0
	)`,
	`INSERT OR IGNORE INTO oceano_library_sync (id, library_version) VALUES (1, 0)`,
	`CREATE TABLE IF NOT EXISTS library_changelog (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		version INTEGER NOT NULL,
		collection_id INTEGER NOT NULL,
		op TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS library_changelog_version_idx ON library_changelog(version)`,
	`DROP TRIGGER IF EXISTS collection_ai_library_sync`,
	`CREATE TRIGGER collection_ai_library_sync AFTER INSERT ON collection BEGIN
		UPDATE oceano_library_sync SET library_version = library_version + 1 WHERE id = 1;
		INSERT INTO library_changelog(version, collection_id, op)
			SELECT library_version, NEW.id, 'upsert' FROM oceano_library_sync WHERE id = 1;
	END`,
	`DROP TRIGGER IF EXISTS collection_au_library_sync`,
	`CREATE TRIGGER collection_au_library_sync AFTER UPDATE ON collection BEGIN
		UPDATE oceano_library_sync SET library_version = library_version + 1 WHERE id = 1;
		INSERT INTO library_changelog(version, collection_id, op)
			SELECT library_version, NEW.id, 'upsert' FROM oceano_library_sync WHERE id = 1;
	END`,
	`DROP TRIGGER IF EXISTS collection_ad_library_sync`,
	`CREATE TRIGGER collection_ad_library_sync AFTER DELETE ON collection BEGIN
		UPDATE oceano_library_sync SET library_version = library_version + 1 WHERE id = 1;
		INSERT INTO library_changelog(version, collection_id, op)
			SELECT library_version, OLD.id, 'delete' FROM oceano_library_sync WHERE id = 1;
	END`,
	// Baseline changelog for existing installs (does not re-run once library_changelog has rows).
	`INSERT INTO library_changelog(version, collection_id, op) SELECT 1, id, 'upsert' FROM collection WHERE NOT EXISTS (SELECT 1 FROM library_changelog LIMIT 1)`,
	`UPDATE oceano_library_sync SET library_version = (SELECT COALESCE(MAX(version), 0) FROM library_changelog) WHERE id = 1`,
	// T22: per-provider recognition attempts (trigger, RMS of capture WAV, latency, error class).
	`CREATE TABLE IF NOT EXISTS recognition_attempts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		occurred_at TEXT NOT NULL,
		provider TEXT NOT NULL,
		trigger_source TEXT NOT NULL DEFAULT '',
		boundary_event_id INTEGER,
		is_hard_boundary INTEGER NOT NULL DEFAULT 0,
		phase TEXT NOT NULL DEFAULT 'primary',
		skip_ms INTEGER NOT NULL DEFAULT 0,
		capture_duration_ms INTEGER NOT NULL DEFAULT 0,
		outcome TEXT NOT NULL,
		error_class TEXT NOT NULL DEFAULT '',
		latency_ms INTEGER NOT NULL DEFAULT 0,
		rms_mean REAL NOT NULL DEFAULT 0,
		rms_peak REAL NOT NULL DEFAULT 0,
		physical_format TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS recognition_attempts_occurred_at_idx ON recognition_attempts(occurred_at)`,
	// Discogs post-recognition enrichment fields (additive; null = not yet enriched).
	`ALTER TABLE collection ADD COLUMN discogs_url TEXT`,
	// Provenance: which metadata chain provider last wrote album/label/released/discogs_url.
	// Null means the fields were populated before provenance tracking was introduced.
	`ALTER TABLE collection ADD COLUMN metadata_provider TEXT`,
	// Provenance for artwork_path when it comes from a different source than text metadata (e.g. iTunes art after Discogs text).
	`ALTER TABLE collection ADD COLUMN artwork_provider TEXT`,
	// Top Discogs release candidates stored at enrich time for the release confirmation carousel.
	// Cleared when user_confirmed is set. JSON array; null when not yet enriched or already confirmed.
	`ALTER TABLE collection ADD COLUMN discogs_candidates_json TEXT`,
}

var currentSchemaVersion = len(migrations)

// Library wraps a SQLite database that tracks recognised physical-media tracks.
type Library struct {
	db *sql.DB
}

type CollectionEntry struct {
	ID                int64
	ACRID             string
	ShazamID          string
	Title             string
	Artist            string
	Album             string
	Label             string
	Released          string
	Score             int
	Format            string
	TrackNumber       string
	ArtworkPath       string
	PlayCount         int
	FirstPlayed       string
	LastPlayed        string
	UserConfirmed     bool
	DurationMs        int
	BoundarySensitive bool
	DiscogsURL             string
	MetadataProvider       string
	ArtworkProvider        string
	DiscogsCandidatesJSON  string
}

var (
	canonicalPartRE = regexp.MustCompile(`[^a-z0-9]+`)
	parenPartRE     = regexp.MustCompile(`\s*[\(\[].*?[\)\]]\s*`)
	wordPartRE      = regexp.MustCompile(`[a-z0-9]+`)
)

func canonicalTrackPart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = parenPartRE.ReplaceAllString(s, " ")
	s = canonicalPartRE.ReplaceAllString(s, "")
	return s
}

func canonicalArtistTokens(s string) map[string]struct{} {
	s = strings.ToLower(strings.TrimSpace(s))
	s = parenPartRE.ReplaceAllString(s, " ")
	tokens := wordPartRE.FindAllString(s, -1)
	ignore := map[string]struct{}{
		"the": {}, "and": {}, "feat": {}, "featuring": {},
		"group": {}, "band": {}, "orchestra": {}, "ensemble": {},
		"quartet": {}, "trio": {}, "choir": {},
	}
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if _, skip := ignore[token]; skip {
			continue
		}
		set[token] = struct{}{}
	}
	return set
}

func canonicalTokenSubset(a, b map[string]struct{}) bool {
	if len(a) == 0 || len(a) > len(b) {
		return false
	}
	for token := range a {
		if _, ok := b[token]; !ok {
			return false
		}
	}
	return true
}

func canonicalArtistsEquivalent(a, b string) bool {
	aNorm := canonicalTrackPart(a)
	bNorm := canonicalTrackPart(b)
	if aNorm == "" || bNorm == "" {
		return false
	}
	if aNorm == bNorm {
		return true
	}
	aTokens := canonicalArtistTokens(a)
	bTokens := canonicalArtistTokens(b)
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return false
	}
	if len(aTokens) == len(bTokens) && canonicalTokenSubset(aTokens, bTokens) {
		return true
	}
	shorter := aTokens
	longer := bTokens
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	return len(shorter) >= 2 && canonicalTokenSubset(shorter, longer)
}

func canonicalTracksEquivalent(aTitle, aArtist, bTitle, bArtist string) bool {
	aT := canonicalTrackPart(aTitle)
	bT := canonicalTrackPart(bTitle)
	if aT == "" || bT == "" {
		return false
	}
	return aT == bT && canonicalArtistsEquivalent(aArtist, bArtist)
}

// LookupByTitleArtist searches the collection for an entry matching title and
// artist using canonical fuzzy matching. Used as a fallback when ID-based
// lookup fails (e.g. the same recording appears under different release IDs).
func (l *Library) LookupByTitleArtist(title, artist string) (*CollectionEntry, error) {
	return l.lookupByEquivalentMetadata(title, artist)
}

func (l *Library) lookupByEquivalentMetadata(title, artist string) (*CollectionEntry, error) {
	if strings.TrimSpace(title) == "" || strings.TrimSpace(artist) == "" {
		return nil, nil
	}

	rows, err := l.db.Query(`
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist,
		       COALESCE(album,''), COALESCE(label,''), COALESCE(released,''),
		       COALESCE(score,0), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       play_count, first_played, last_played, user_confirmed,
		       COALESCE(duration_ms,0), COALESCE(boundary_sensitive,0),
		       COALESCE(discogs_url,''), COALESCE(metadata_provider,''), COALESCE(artwork_provider,'')
		FROM collection
		WHERE title != '' AND artist != ''`)
	if err != nil {
		return nil, fmt.Errorf("library: equivalent metadata lookup query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var e CollectionEntry
		var confirmed, boundarySens int
		if err := rows.Scan(
			&e.ID, &e.ACRID, &e.ShazamID, &e.Title, &e.Artist,
			&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
			&e.TrackNumber, &e.ArtworkPath,
			&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed,
			&e.DurationMs, &boundarySens, &e.DiscogsURL, &e.MetadataProvider, &e.ArtworkProvider,
		); err != nil {
			return nil, fmt.Errorf("library: equivalent metadata lookup scan: %w", err)
		}
		e.UserConfirmed = confirmed == 1
		e.BoundarySensitive = boundarySens == 1
		if canonicalTracksEquivalent(title, artist, e.Title, e.Artist) {
			return &e, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: equivalent metadata lookup rows: %w", err)
	}
	return nil, nil
}

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

func (l *Library) DB() *sql.DB {
	return l.db
}

// LibraryVersion returns the monotonic counter bumped on collection INSERT/UPDATE/DELETE.
// Used by HTTP clients for ETag / If-None-Match and incremental library sync.
func (l *Library) LibraryVersion() (int64, error) {
	if l == nil || l.db == nil {
		return 0, nil
	}
	var v sql.NullInt64
	err := l.db.QueryRow(`SELECT library_version FROM oceano_library_sync WHERE id = 1`).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

func (l *Library) migrate() error {
	if _, err := l.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY
	)`); err != nil {
		return fmt.Errorf("library: bootstrap migrations table: %w", err)
	}

	for i, stmt := range migrations {
		version := i + 1
		var exists int
		err := l.db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
		if err == nil && exists == 1 {
			continue
		}
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("library: check migration v%d: %w", version, err)
		}

		tx, err := l.db.Begin()
		if err != nil {
			return fmt.Errorf("library: begin migration v%d: %w", version, err)
		}
		if _, err := tx.Exec(stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("library: migration v%d: %w", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("library: record migration v%d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("library: commit migration v%d: %w", version, err)
		}
		log.Printf("library: applied migration v%d", version)
	}
	return nil
}

func (l *Library) lookupByColumn(col, value string) (*CollectionEntry, error) {
	if value == "" {
		return nil, nil
	}
	row := l.db.QueryRow(`
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist,
		       COALESCE(album,''), COALESCE(label,''), COALESCE(released,''),
		       COALESCE(score,0), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       play_count, first_played, last_played, user_confirmed,
		       COALESCE(duration_ms,0), COALESCE(boundary_sensitive,0),
		       COALESCE(discogs_url,''), COALESCE(metadata_provider,''), COALESCE(artwork_provider,'')
		FROM collection WHERE `+col+` = ?`, value)

	var e CollectionEntry
	var confirmed, boundarySens int
	err := row.Scan(
		&e.ID, &e.ACRID, &e.ShazamID, &e.Title, &e.Artist,
		&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
		&e.TrackNumber, &e.ArtworkPath,
		&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed,
		&e.DurationMs, &boundarySens, &e.DiscogsURL, &e.MetadataProvider, &e.ArtworkProvider,
	)
	e.UserConfirmed = confirmed == 1
	e.BoundarySensitive = boundarySens == 1
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("library: lookup by %s: %w", col, err)
	}
	return &e, nil
}

func (l *Library) Lookup(acrid string) (*CollectionEntry, error) {
	return l.lookupByColumn("acrid", acrid)
}

// RecordRecognitionEvent increments a counter in recognition_summary.
func (l *Library) RecordRecognitionEvent(provider, event string) {
	if l == nil || l.db == nil {
		return
	}
	_, err := l.db.Exec(`
		INSERT INTO recognition_summary (provider, event, count)
		VALUES (?, ?, 1)
		ON CONFLICT(provider, event) DO UPDATE SET count = count + 1`,
		provider, event)
	if err != nil {
		log.Printf("library: RecordRecognitionEvent: %v", err)
	}
}

// GetRecognitionStats returns a map of provider -> event -> count.
func (l *Library) GetRecognitionStats() (map[string]map[string]int, error) {
	if l == nil || l.db == nil {
		return nil, nil
	}
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

func (l *Library) GetByID(id int64) (*CollectionEntry, error) {
	if id <= 0 {
		return nil, nil
	}
	row := l.db.QueryRow(`
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist,
		       COALESCE(album,''), COALESCE(label,''), COALESCE(released,''),
		       COALESCE(score,0), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       play_count, first_played, last_played, user_confirmed,
		       COALESCE(duration_ms,0), COALESCE(boundary_sensitive,0),
		       COALESCE(discogs_url,''), COALESCE(metadata_provider,''), COALESCE(artwork_provider,'')
		FROM collection WHERE id = ?`, id)
	var e CollectionEntry
	var confirmed, boundarySens int
	err := row.Scan(
		&e.ID, &e.ACRID, &e.ShazamID, &e.Title, &e.Artist,
		&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
		&e.TrackNumber, &e.ArtworkPath,
		&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed,
		&e.DurationMs, &boundarySens, &e.DiscogsURL, &e.MetadataProvider, &e.ArtworkProvider,
	)
	e.UserConfirmed = confirmed == 1
	e.BoundarySensitive = boundarySens == 1
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("library: lookup by id: %w", err)
	}
	return &e, nil
}

func (l *Library) LookupByShazamID(shazamID string) (*CollectionEntry, error) {
	return l.lookupByColumn("shazam_id", shazamID)
}

func preferLookupCandidate(current, alternate *CollectionEntry) *CollectionEntry {
	if current == nil {
		return alternate
	}
	if alternate == nil {
		return current
	}

	if alternate.UserConfirmed != current.UserConfirmed {
		if alternate.UserConfirmed {
			return alternate
		}
		return current
	}

	currentHasDuration := current.DurationMs > 0
	alternateHasDuration := alternate.DurationMs > 0
	if alternateHasDuration != currentHasDuration {
		if alternateHasDuration {
			return alternate
		}
		return current
	}

	if alternate.Score > current.Score {
		return alternate
	}
	if current.Score > alternate.Score {
		return current
	}

	if alternate.PlayCount > current.PlayCount {
		return alternate
	}
	return current
}

func (l *Library) LookupByIDs(acrid, shazamID string) (*CollectionEntry, error) {
	byACR, err := l.Lookup(acrid)
	if err != nil {
		return nil, err
	}
	byShazam, err := l.LookupByShazamID(shazamID)
	if err != nil {
		return nil, err
	}
	return preferLookupCandidate(byACR, byShazam), nil
}

// FindPhysicalMatch searches the library for a confirmed physical-media entry
// that matches the given title and artist using canonical fuzzy matching.
// Returns nil when no match is found. Used to enrich streaming state with
// information about a corresponding vinyl or CD in the local collection.
//
// Only user-confirmed entries with format Vinyl or CD are considered — unconfirmed
// rows (auto-created stubs) and Unknown-format entries are excluded to avoid
// showing a misleading "In collection" chip for unverified data.
func (l *Library) FindPhysicalMatch(title, artist string) (*CollectionEntry, error) {
	if strings.TrimSpace(title) == "" || strings.TrimSpace(artist) == "" {
		return nil, nil
	}

	rows, err := l.db.Query(`
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist,
		       COALESCE(album,''), COALESCE(label,''), COALESCE(released,''),
		       COALESCE(score,0), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       play_count, first_played, last_played, user_confirmed,
		       COALESCE(duration_ms,0), COALESCE(boundary_sensitive,0),
		       COALESCE(discogs_url,''), COALESCE(metadata_provider,''), COALESCE(artwork_provider,'')
		FROM collection
		WHERE title != '' AND artist != ''
		  AND user_confirmed = 1
		  AND format IN ('Vinyl','CD')`)
	if err != nil {
		return nil, fmt.Errorf("library: physical match query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var e CollectionEntry
		var confirmed, boundarySens int
		if err := rows.Scan(
			&e.ID, &e.ACRID, &e.ShazamID, &e.Title, &e.Artist,
			&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
			&e.TrackNumber, &e.ArtworkPath,
			&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed,
			&e.DurationMs, &boundarySens, &e.DiscogsURL, &e.MetadataProvider, &e.ArtworkProvider,
		); err != nil {
			return nil, fmt.Errorf("library: physical match scan: %w", err)
		}
		e.UserConfirmed = confirmed == 1
		e.BoundarySensitive = boundarySens == 1
		if canonicalTracksEquivalent(title, artist, e.Title, e.Artist) {
			return &e, nil
		}
	}
	return nil, rows.Err()
}

// RecordPlay logs a track playback in the collection.
// It handles identifying existing tracks by ACRID, ShazamID, or Title/Artist.
// If it's a new track, it's created as unconfirmed (user_confirmed = 0) so the
// user can associate it with existing entries if it's a duplicate.
func (l *Library) RecordPlay(result *recognition.Result, artworkPath string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// API providers can return different IDs for what is effectively the same
	// track (e.g. remaster/release variants). When Shazam is present and a
	// confirmed metadata-equivalent row already exists, update that row instead
	// of creating a duplicate keyed by a new ACRID.
	allowEquivalentMerge := result.ShazamID != "" ||
		(result.ACRID != "" && strings.TrimSpace(result.Title) != "" && strings.TrimSpace(result.Artist) != "") ||
		(strings.EqualFold(result.MatchSource, "audd") && strings.TrimSpace(result.Title) != "" && strings.TrimSpace(result.Artist) != "")
	if allowEquivalentMerge {
		if existing, err := l.lookupByEquivalentMetadata(result.Title, result.Artist); err != nil {
			return 0, err
		} else if existing != nil {
			_, err := l.db.Exec(`
				UPDATE collection SET
					play_count     = play_count + 1,
					last_played    = ?,
					shazam_id      = CASE WHEN (COALESCE(shazam_id,'') = '') AND ? != '' THEN ? ELSE shazam_id END,
					isrc           = CASE WHEN (COALESCE(isrc,'') = '') AND ? != '' THEN ? ELSE isrc END,
					title          = CASE WHEN ? > score THEN ? ELSE title END,
					artist         = CASE WHEN ? > score THEN ? ELSE artist END,
					album          = CASE WHEN ? > score THEN ? ELSE album END,
					score          = CASE WHEN ? > score THEN ? ELSE score END,
					duration_ms    = CASE WHEN ? > 0 THEN ? ELSE duration_ms END,
					artwork_path   = CASE WHEN (artwork_path IS NULL OR artwork_path = '') AND ? != '' THEN ? ELSE artwork_path END
				WHERE id = ?`,
				now,
				result.ShazamID, result.ShazamID,
				result.ISRC, result.ISRC,
				result.Score, result.Title,
				result.Score, result.Artist,
				result.Score, result.Album,
				result.Score, result.Score,
				result.DurationMs, result.DurationMs,
				artworkPath, artworkPath,
				existing.ID,
			)
			if err != nil {
				return 0, fmt.Errorf("library: equivalent metadata update: %w", err)
			}
			log.Printf("library: merged equivalent confirmed track into existing row id=%d (score=%d shazam_id=%q)", existing.ID, result.Score, result.ShazamID)
			return existing.ID, nil
		}
	}

	if result.ACRID != "" {
		var id int64
		err := l.db.QueryRow(`
			INSERT INTO collection
				(acrid, shazam_id, isrc, title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played, user_confirmed, duration_ms)
			VALUES (?,?,?,?,?,?,?,?,?,?,1,?,?,0,?)
			ON CONFLICT(acrid) DO UPDATE SET
				play_count     = play_count + 1,
				last_played    = excluded.last_played,
				shazam_id      = CASE WHEN (COALESCE(shazam_id,'') = '') AND excluded.shazam_id != '' THEN excluded.shazam_id ELSE shazam_id END,
				isrc           = CASE WHEN (COALESCE(isrc,'') = '') AND excluded.isrc != '' THEN excluded.isrc ELSE isrc END,
				title          = CASE WHEN excluded.score > score THEN excluded.title ELSE title END,
				artist         = CASE WHEN excluded.score > score THEN excluded.artist ELSE artist END,
				album          = CASE WHEN excluded.score > score THEN excluded.album ELSE album END,
				score          = CASE WHEN excluded.score > score THEN excluded.score ELSE score END,
				duration_ms    = CASE WHEN excluded.duration_ms > 0 THEN excluded.duration_ms ELSE duration_ms END,
				artwork_path   = CASE WHEN (artwork_path IS NULL OR artwork_path = '') AND excluded.artwork_path != ''
				                 THEN excluded.artwork_path ELSE artwork_path END
			RETURNING id`,
			result.ACRID, result.ShazamID, result.ISRC, result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now, result.DurationMs,
		).Scan(&id)
		return id, err
	}

	if result.ShazamID != "" {
		var id int64
		err := l.db.QueryRow(`
			INSERT INTO collection
				(shazam_id, isrc, title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played, user_confirmed, duration_ms)
			VALUES (?,?,?,?,?,?,?,?,?,1,?,?,0,?)
			ON CONFLICT(shazam_id) WHERE shazam_id IS NOT NULL AND shazam_id != '' DO UPDATE SET
				play_count     = play_count + 1,
				last_played    = excluded.last_played,
				isrc           = CASE WHEN (COALESCE(isrc,'') = '') AND excluded.isrc != '' THEN excluded.isrc ELSE isrc END,
				title          = CASE WHEN excluded.score > score THEN excluded.title ELSE title END,
				artist         = CASE WHEN excluded.score > score THEN excluded.artist ELSE artist END,
				album          = CASE WHEN excluded.score > score THEN excluded.album ELSE album END,
				score          = CASE WHEN excluded.score > score THEN excluded.score ELSE score END,
				duration_ms    = CASE WHEN excluded.duration_ms > 0 THEN excluded.duration_ms ELSE duration_ms END,
				artwork_path   = CASE WHEN (artwork_path IS NULL OR artwork_path = '') AND excluded.artwork_path != ''
				                 THEN excluded.artwork_path ELSE artwork_path END
			RETURNING id`,
			result.ShazamID, result.ISRC, result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now, result.DurationMs,
		).Scan(&id)
		return id, err
	}

	var id int64
	err := l.db.QueryRow(`SELECT id FROM collection WHERE title = ? AND artist = ?`, result.Title, result.Artist).Scan(&id)
	if err == sql.ErrNoRows {
		err = l.db.QueryRow(`
			INSERT INTO collection
				(title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played, user_confirmed, duration_ms)
			VALUES (?,?,?,?,?,?,?,1,?,?,0,?)
			RETURNING id`,
			result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now, result.DurationMs,
		).Scan(&id)
		return id, err
	}
	if err != nil {
		return 0, fmt.Errorf("library: fallback lookup: %w", err)
	}
	_, err = l.db.Exec(`UPDATE collection SET play_count = play_count + 1, last_played = ? WHERE id = ?`, now, id)
	return id, err
}

// UpdateEnrichmentPatch persists enrichment fields from a metadata chain provider
// for an existing collection row using an additive policy: existing non-empty
// values are never overwritten so user edits are preserved. artworkPath is applied
// only when the row has no artwork_path yet; track_number is additive in the same way.
// Entries with user_confirmed = 1 are never touched — user-curated data takes precedence.
func (l *Library) UpdateEnrichmentPatch(id int64, discogsURL, album, label, released, trackNumber, metadataProvider, artworkPath, artworkProvider string) error {
	if l == nil || l.db == nil || id <= 0 {
		return nil
	}
	_, err := l.db.Exec(`
		UPDATE collection SET
			discogs_url       = CASE WHEN COALESCE(discogs_url,'') = ''       AND ? != '' THEN ? ELSE discogs_url END,
			album             = CASE WHEN COALESCE(album,'') = ''             AND ? != '' THEN ? ELSE album END,
			label             = CASE WHEN COALESCE(label,'') = ''             AND ? != '' THEN ? ELSE label END,
			released          = CASE WHEN COALESCE(released,'') = ''          AND ? != '' THEN ? ELSE released END,
			track_number      = CASE WHEN COALESCE(track_number,'') = ''       AND ? != '' THEN ? ELSE track_number END,
			metadata_provider = CASE WHEN COALESCE(metadata_provider,'') = '' AND ? != '' THEN ? ELSE metadata_provider END,
			artwork_path      = CASE WHEN COALESCE(artwork_path,'') = ''       AND ? != '' THEN ? ELSE artwork_path END,
			artwork_provider  = CASE WHEN COALESCE(artwork_provider,'') = ''  AND ? != '' AND ? != '' THEN ? ELSE artwork_provider END
		WHERE id = ? AND user_confirmed = 0`,
		discogsURL, discogsURL,
		album, album,
		label, label,
		released, released,
		trackNumber, trackNumber,
		metadataProvider, metadataProvider,
		artworkPath, artworkPath,
		artworkPath, artworkProvider, artworkProvider,
		id,
	)
	if err != nil {
		return fmt.Errorf("library: update enrichment patch id=%d: %w", id, err)
	}
	return nil
}

// GetCandidatesJSON returns the raw JSON of stored Discogs release candidates for an entry,
// or an empty string when none are stored or the entry is already confirmed.
func (l *Library) GetCandidatesJSON(id int64) (string, error) {
	if l == nil || l.db == nil || id <= 0 {
		return "", nil
	}
	var raw string
	err := l.db.QueryRow(
		`SELECT COALESCE(discogs_candidates_json,'') FROM collection WHERE id = ?`, id,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return raw, err
}

// ConfirmRelease writes the user-selected release fields to an entry, sets user_confirmed=1,
// and clears the candidate list so the carousel is not shown again.
func (l *Library) ConfirmRelease(id int64, album, label, released, discogsURL, artworkPath string) error {
	if l == nil || l.db == nil || id <= 0 {
		return nil
	}
	_, err := l.db.Exec(`
		UPDATE collection SET
			album                   = CASE WHEN ? != '' THEN ? ELSE album END,
			label                   = CASE WHEN ? != '' THEN ? ELSE label END,
			released                = CASE WHEN ? != '' THEN ? ELSE released END,
			discogs_url             = CASE WHEN ? != '' THEN ? ELSE discogs_url END,
			artwork_path            = CASE WHEN ? != '' THEN ? ELSE artwork_path END,
			user_confirmed          = 1,
			discogs_candidates_json = NULL
		WHERE id = ?`,
		album, album,
		label, label,
		released, released,
		discogsURL, discogsURL,
		artworkPath, artworkPath,
		id,
	)
	if err != nil {
		return fmt.Errorf("library: confirm release id=%d: %w", id, err)
	}
	return nil
}

// StoreCandidatesJSON persists the Discogs release candidate list for an unconfirmed entry.
// Skipped for user-confirmed entries so the carousel is never shown again after the user has chosen.
func (l *Library) StoreCandidatesJSON(id int64, candidatesJSON string) error {
	if l == nil || l.db == nil || id <= 0 || strings.TrimSpace(candidatesJSON) == "" {
		return nil
	}
	_, err := l.db.Exec(
		`UPDATE collection SET discogs_candidates_json = ? WHERE id = ? AND user_confirmed = 0`,
		candidatesJSON, id,
	)
	if err != nil {
		return fmt.Errorf("library: store candidates id=%d: %w", id, err)
	}
	return nil
}

func (l *Library) Close() error {
	return l.db.Close()
}
