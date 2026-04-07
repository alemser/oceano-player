package library

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
	`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
	`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
	`CREATE TABLE IF NOT EXISTS fingerprints (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		data     TEXT    NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
	`DROP TABLE IF EXISTS fingerprints`,
	`CREATE TABLE fingerprints (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
		data     TEXT    NOT NULL
	)`,
	`CREATE INDEX fingerprints_entry_id ON fingerprints(entry_id)`,
	`ALTER TABLE collection ADD COLUMN shazam_id TEXT`,
	`CREATE UNIQUE INDEX IF NOT EXISTS collection_shazam_id_uq ON collection(shazam_id) WHERE shazam_id IS NOT NULL AND shazam_id != ''`,
}

type Library struct {
	db *sql.DB
}

type CollectionEntry struct {
	ID            int64
	ACRID         string
	ShazamID      string
	Title         string
	Artist        string
	Album         string
	Label         string
	Released      string
	Score         int
	Format        string
	TrackNumber   string
	ArtworkPath   string
	PlayCount     int
	FirstPlayed   string
	LastPlayed    string
	UserConfirmed bool
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

func (l *Library) lookupConfirmedByEquivalentMetadata(title, artist string) (*CollectionEntry, error) {
	if strings.TrimSpace(title) == "" || strings.TrimSpace(artist) == "" {
		return nil, nil
	}

	rows, err := l.db.Query(`
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist,
		       COALESCE(album,''), COALESCE(label,''), COALESCE(released,''),
		       COALESCE(score,0), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       play_count, first_played, last_played, user_confirmed
		FROM collection
		WHERE user_confirmed = 1 AND title != '' AND artist != ''`)
	if err != nil {
		return nil, fmt.Errorf("library: equivalent metadata lookup query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var e CollectionEntry
		var confirmed int
		if err := rows.Scan(
			&e.ID, &e.ACRID, &e.ShazamID, &e.Title, &e.Artist,
			&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
			&e.TrackNumber, &e.ArtworkPath,
			&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed,
		); err != nil {
			return nil, fmt.Errorf("library: equivalent metadata lookup scan: %w", err)
		}
		e.UserConfirmed = confirmed == 1
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

func (l *Library) lookupByColumn(col, value string) (*CollectionEntry, error) {
	if value == "" {
		return nil, nil
	}
	row := l.db.QueryRow(`
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist,
		       COALESCE(album,''), COALESCE(label,''), COALESCE(released,''),
		       COALESCE(score,0), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       play_count, first_played, last_played, user_confirmed
		FROM collection WHERE `+col+` = ?`, value)

	var e CollectionEntry
	var confirmed int
	err := row.Scan(
		&e.ID, &e.ACRID, &e.ShazamID, &e.Title, &e.Artist,
		&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
		&e.TrackNumber, &e.ArtworkPath,
		&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed,
	)
	e.UserConfirmed = confirmed == 1
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

func (l *Library) LookupByShazamID(shazamID string) (*CollectionEntry, error) {
	return l.lookupByColumn("shazam_id", shazamID)
}

func (l *Library) LookupByIDs(acrid, shazamID string) (*CollectionEntry, error) {
	if e, err := l.Lookup(acrid); err != nil || e != nil {
		return e, err
	}
	return l.LookupByShazamID(shazamID)
}

func (l *Library) RecordPlay(result *recognition.Result, artworkPath string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// API providers can return different IDs for what is effectively the same
	// track (e.g. remaster/release variants). When Shazam is present and a
	// confirmed metadata-equivalent row already exists, update that row instead
	// of creating a duplicate keyed by a new ACRID.
	allowEquivalentMerge := result.ShazamID != "" ||
		(result.ACRID != "" && strings.TrimSpace(result.Title) != "" && strings.TrimSpace(result.Artist) != "")
	if allowEquivalentMerge {
		if existing, err := l.lookupConfirmedByEquivalentMetadata(result.Title, result.Artist); err != nil {
			return 0, err
		} else if existing != nil {
			_, err := l.db.Exec(`
				UPDATE collection SET
					play_count     = play_count + 1,
					last_played    = ?,
					user_confirmed = 1,
					shazam_id      = CASE WHEN (COALESCE(shazam_id,'') = '') AND ? != '' THEN ? ELSE shazam_id END,
					title          = CASE WHEN ? > score THEN ? ELSE title END,
					artist         = CASE WHEN ? > score THEN ? ELSE artist END,
					album          = CASE WHEN ? > score THEN ? ELSE album END,
					score          = CASE WHEN ? > score THEN ? ELSE score END,
					artwork_path   = CASE WHEN (artwork_path IS NULL OR artwork_path = '') AND ? != '' THEN ? ELSE artwork_path END
				WHERE id = ?`,
				now,
				result.ShazamID, result.ShazamID,
				result.Score, result.Title,
				result.Score, result.Artist,
				result.Score, result.Album,
				result.Score, result.Score,
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
				(acrid, shazam_id, title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played, user_confirmed)
			VALUES (?,?,?,?,?,?,?,?,?,1,?,?,1)
			ON CONFLICT(acrid) DO UPDATE SET
				play_count     = play_count + 1,
				last_played    = excluded.last_played,
				user_confirmed = 1,
				shazam_id      = CASE WHEN (COALESCE(shazam_id,'') = '') AND excluded.shazam_id != '' THEN excluded.shazam_id ELSE shazam_id END,
				title          = CASE WHEN excluded.score > score THEN excluded.title ELSE title END,
				artist         = CASE WHEN excluded.score > score THEN excluded.artist ELSE artist END,
				album          = CASE WHEN excluded.score > score THEN excluded.album ELSE album END,
				score          = CASE WHEN excluded.score > score THEN excluded.score ELSE score END,
				artwork_path   = CASE WHEN (artwork_path IS NULL OR artwork_path = '') AND excluded.artwork_path != ''
				                 THEN excluded.artwork_path ELSE artwork_path END
			RETURNING id`,
			result.ACRID, result.ShazamID, result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now,
		).Scan(&id)
		return id, err
	}

	if result.ShazamID != "" {
		var id int64
		err := l.db.QueryRow(`
			INSERT INTO collection
				(shazam_id, title, artist, album, label, released, score,
				 artwork_path, play_count, first_played, last_played, user_confirmed)
			VALUES (?,?,?,?,?,?,?, ?,1,?,?,1)
			ON CONFLICT(shazam_id) WHERE shazam_id IS NOT NULL AND shazam_id != '' DO UPDATE SET
				play_count     = play_count + 1,
				last_played    = excluded.last_played,
				user_confirmed = 1,
				title          = CASE WHEN excluded.score > score THEN excluded.title ELSE title END,
				artist         = CASE WHEN excluded.score > score THEN excluded.artist ELSE artist END,
				album          = CASE WHEN excluded.score > score THEN excluded.album ELSE album END,
				score          = CASE WHEN excluded.score > score THEN excluded.score ELSE score END,
				artwork_path   = CASE WHEN (artwork_path IS NULL OR artwork_path = '') AND excluded.artwork_path != ''
				                 THEN excluded.artwork_path ELSE artwork_path END
			RETURNING id`,
			result.ShazamID, result.Title, result.Artist, result.Album,
			result.Label, result.Released, result.Score, artworkPath, now, now,
		).Scan(&id)
		return id, err
	}

	var id int64
	err := l.db.QueryRow(`SELECT id FROM collection WHERE title = ? AND artist = ?`, result.Title, result.Artist).Scan(&id)
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
	_, err = l.db.Exec(`UPDATE collection SET play_count = play_count + 1, last_played = ?, user_confirmed = 1 WHERE id = ?`, now, id)
	return id, err
}

func (l *Library) UpsertStub(fps []recognition.Fingerprint, threshold float64, maxShift int) (*CollectionEntry, error) {
	if len(fps) == 0 {
		return nil, fmt.Errorf("library: UpsertStub: no fingerprints provided")
	}

	entry, err := l.FindByFingerprints(fps, threshold, maxShift)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if entry != nil {
		if _, err := l.db.Exec(`UPDATE collection SET play_count = play_count + 1, last_played = ? WHERE id = ?`, now, entry.ID); err != nil {
			return nil, fmt.Errorf("library: stub update: %w", err)
		}
		_ = l.SaveFingerprints(entry.ID, fps)
		return entry, nil
	}

	var id int64
	if err := l.db.QueryRow(`
		INSERT INTO collection (title, artist, play_count, first_played, last_played, user_confirmed)
		VALUES ('','',1,?,?,0)
		RETURNING id`, now, now).Scan(&id); err != nil {
		return nil, fmt.Errorf("library: stub insert: %w", err)
	}
	if err := l.SaveFingerprints(id, fps); err != nil {
		return nil, fmt.Errorf("library: stub save fingerprints: %w", err)
	}

	return &CollectionEntry{ID: id, FirstPlayed: now, LastPlayed: now, PlayCount: 1}, nil
}

func (l *Library) HasFingerprints(entryID int64) bool {
	var count int
	l.db.QueryRow(`SELECT COUNT(*) FROM fingerprints WHERE entry_id=?`, entryID).Scan(&count)
	return count > 0
}

func (l *Library) SaveFingerprints(entryID int64, fps []recognition.Fingerprint) error {
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

func (l *Library) FindByFingerprints(fps []recognition.Fingerprint, threshold float64, maxShift int) (*CollectionEntry, error) {
	if len(fps) == 0 {
		return nil, nil
	}

	rows, err := l.db.Query(`
		SELECT f.entry_id, f.data,
		       COALESCE(c.acrid,''), COALESCE(c.shazam_id,''), c.title, c.artist,
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

	type entryState struct {
		entry     CollectionEntry
		bestBERs  []float64
		worstBest float64
	}
	entries := make(map[int64]*entryState)

	for rows.Next() {
		var entryID int64
		var data string
		var e CollectionEntry
		var confirmed int
		if err := rows.Scan(
			&entryID, &data,
			&e.ACRID, &e.ShazamID, &e.Title, &e.Artist,
			&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
			&e.TrackNumber, &e.ArtworkPath,
			&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed,
		); err != nil {
			return nil, fmt.Errorf("library: fingerprint row scan: %w", err)
		}
		e.ID = entryID
		e.UserConfirmed = confirmed == 1

		stored, parseErr := recognition.ParseFingerprint(data)
		if parseErr != nil {
			continue
		}

		state, ok := entries[entryID]
		if !ok {
			state = &entryState{entry: e, bestBERs: make([]float64, len(fps)), worstBest: 1.0}
			for i := range state.bestBERs {
				state.bestBERs[i] = 1.0
			}
			entries[entryID] = state
		}
		for i, fp := range fps {
			if b := recognition.BER(fp, stored, maxShift); b < state.bestBERs[i] {
				state.bestBERs[i] = b
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: fingerprint scan rows: %w", err)
	}

	var bestEntry *CollectionEntry
	bestScore := threshold
	for _, state := range entries {
		state.worstBest = 0.0
		for _, b := range state.bestBERs {
			if b > state.worstBest {
				state.worstBest = b
			}
		}
		label := state.entry.Artist + " — " + state.entry.Title
		if label == " — " {
			label = fmt.Sprintf("stub id=%d", state.entry.ID)
		}
		log.Printf("library: fingerprint candidate id=%d %q worst-best-ber=%.3f threshold=%.2f", state.entry.ID, label, state.worstBest, threshold)
		if state.worstBest < bestScore {
			bestScore = state.worstBest
			e := state.entry
			bestEntry = &e
		}
	}
	return bestEntry, nil
}

func (l *Library) FindConfirmedByFingerprints(fps []recognition.Fingerprint, threshold float64, maxShift int) (*CollectionEntry, error) {
	if len(fps) == 0 {
		return nil, nil
	}

	rows, err := l.db.Query(`
		SELECT f.entry_id, f.data,
		       COALESCE(c.acrid,''), COALESCE(c.shazam_id,''), c.title, c.artist,
		       COALESCE(c.album,''), COALESCE(c.label,''), COALESCE(c.released,''),
		       COALESCE(c.score,0), COALESCE(c.format,'Unknown'),
		       COALESCE(c.track_number,''), COALESCE(c.artwork_path,''),
		       c.play_count, c.first_played, c.last_played, c.user_confirmed
		FROM fingerprints f
		JOIN collection c ON c.id = f.entry_id
		WHERE c.user_confirmed = 1 AND c.title != '' AND c.artist != ''`)
	if err != nil {
		return nil, fmt.Errorf("library: confirmed fingerprint scan: %w", err)
	}
	defer rows.Close()

	type entryState struct {
		entry     CollectionEntry
		bestBERs  []float64
		worstBest float64
	}
	entries := make(map[int64]*entryState)

	for rows.Next() {
		var entryID int64
		var data string
		var e CollectionEntry
		var confirmed int
		if err := rows.Scan(
			&entryID, &data,
			&e.ACRID, &e.ShazamID, &e.Title, &e.Artist,
			&e.Album, &e.Label, &e.Released, &e.Score, &e.Format,
			&e.TrackNumber, &e.ArtworkPath,
			&e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed,
		); err != nil {
			return nil, fmt.Errorf("library: confirmed fingerprint row scan: %w", err)
		}
		e.ID = entryID
		e.UserConfirmed = confirmed == 1

		stored, parseErr := recognition.ParseFingerprint(data)
		if parseErr != nil {
			continue
		}

		state, ok := entries[entryID]
		if !ok {
			state = &entryState{entry: e, bestBERs: make([]float64, len(fps)), worstBest: 1.0}
			for i := range state.bestBERs {
				state.bestBERs[i] = 1.0
			}
			entries[entryID] = state
		}
		for i, fp := range fps {
			if b := recognition.BER(fp, stored, maxShift); b < state.bestBERs[i] {
				state.bestBERs[i] = b
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: confirmed fingerprint scan rows: %w", err)
	}

	var bestEntry *CollectionEntry
	bestScore := threshold
	for _, state := range entries {
		state.worstBest = 0.0
		for _, b := range state.bestBERs {
			if b > state.worstBest {
				state.worstBest = b
			}
		}
		label := state.entry.Artist + " — " + state.entry.Title
		log.Printf("library: confirmed fingerprint candidate id=%d %q worst-best-ber=%.3f threshold=%.2f", state.entry.ID, label, state.worstBest, threshold)
		if state.worstBest < bestScore {
			bestScore = state.worstBest
			e := state.entry
			bestEntry = &e
		}
	}
	return bestEntry, nil
}

func (l *Library) PruneStub(id int64) error {
	_, err := l.db.Exec(`DELETE FROM collection WHERE id=? AND title='' AND artist='' AND user_confirmed=0`, id)
	return err
}

// PromoteStubFingerprints moves all fingerprint rows from an unresolved stub to
// an identified entry, then prunes the source stub if it is still unresolved.
func (l *Library) PromoteStubFingerprints(stubID, entryID int64) error {
	if stubID <= 0 || entryID <= 0 || stubID == entryID {
		return nil
	}

	tx, err := l.db.Begin()
	if err != nil {
		return fmt.Errorf("library: promote stub fingerprints begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`UPDATE fingerprints SET entry_id=? WHERE entry_id=?`, entryID, stubID); err != nil {
		return fmt.Errorf("library: promote stub fingerprints move: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM collection WHERE id=? AND title='' AND artist='' AND user_confirmed=0`, stubID); err != nil {
		return fmt.Errorf("library: promote stub fingerprints prune: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("library: promote stub fingerprints commit: %w", err)
	}
	return nil
}

func (l *Library) PruneMatchingStubs(fps []recognition.Fingerprint, threshold float64, maxShift int, excludeID int64) {
	if len(fps) == 0 {
		return
	}

	rows, err := l.db.Query(`
		SELECT f.entry_id, f.data FROM fingerprints f
		JOIN collection c ON c.id = f.entry_id
		WHERE c.user_confirmed = 0 AND c.title = '' AND c.artist = ''
		  AND f.entry_id != ?`, excludeID)
	if err != nil {
		return
	}
	defer rows.Close()

	type stubState struct {
		bestBER []float64
	}
	stubs := make(map[int64]*stubState)
	for rows.Next() {
		var entryID int64
		var data string
		if err := rows.Scan(&entryID, &data); err != nil {
			continue
		}
		stored, err := recognition.ParseFingerprint(data)
		if err != nil {
			continue
		}
		state, ok := stubs[entryID]
		if !ok {
			state = &stubState{bestBER: make([]float64, len(fps))}
			for i := range state.bestBER {
				state.bestBER[i] = 1.0
			}
			stubs[entryID] = state
		}
		for i, fp := range fps {
			if b := recognition.BER(fp, stored, maxShift); b < state.bestBER[i] {
				state.bestBER[i] = b
			}
		}
	}

	for id, state := range stubs {
		worstBest := 0.0
		for _, b := range state.bestBER {
			if b > worstBest {
				worstBest = b
			}
		}
		if worstBest >= threshold {
			continue
		}
		if _, err := l.db.Exec(`DELETE FROM collection WHERE id=? AND title='' AND artist='' AND user_confirmed=0`, id); err == nil {
			log.Printf("library: pruned orphaned stub %d", id)
		}
	}
}

func (l *Library) PruneRecentStubs(since time.Time, excludeID int64) {
	sinceStr := since.UTC().Format(time.RFC3339)
	rows, err := l.db.Query(`
		SELECT id FROM collection
		WHERE title = '' AND artist = '' AND user_confirmed = 0
		  AND id != ?
		  AND first_played >= ?`, excludeID, sinceStr)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if _, err := l.db.Exec(`DELETE FROM collection WHERE id=? AND title='' AND artist='' AND user_confirmed=0`, id); err == nil {
			log.Printf("library: pruned recent stub %d (created after boundary at %s)", id, sinceStr)
		}
	}
}

func (l *Library) Close() error {
	return l.db.Close()
}
