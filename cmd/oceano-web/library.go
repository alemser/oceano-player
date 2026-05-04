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
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// LibraryEntry is the JSON representation of a collection row.
type LibraryEntry struct {
	ID            int64  `json:"id"`
	ACRID         string `json:"acrid"`
	ShazamID      string `json:"shazam_id"`
	Title         string `json:"title"`
	Artist        string `json:"artist"`
	Album         string `json:"album"`
	Label         string `json:"label"`
	Released      string `json:"released"`
	Format        string `json:"format"`
	TrackNumber   string `json:"track_number"`
	ArtworkPath   string `json:"artwork_path"`
	DurationMs    int    `json:"duration_ms"`
	PlayCount     int    `json:"play_count"`
	FirstPlayed   string `json:"first_played"`
	LastPlayed    string `json:"last_played"`
	UserConfirmed bool   `json:"user_confirmed"`
	// BoundarySensitive marks tracks where quiet passages are often mistaken for
	// track boundaries (R8); the state manager nudges duration-based VU guards.
	BoundarySensitive bool   `json:"boundary_sensitive"`
	DiscogsURL        string `json:"discogs_url,omitempty"`
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
		`CREATE TABLE IF NOT EXISTS boundary_events (
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
			play_history_id INTEGER,
			collection_id INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS boundary_events_occurred_at_idx ON boundary_events(occurred_at)`,
		`ALTER TABLE boundary_events ADD COLUMN followup_outcome TEXT`,
		`ALTER TABLE boundary_events ADD COLUMN followup_acrid TEXT`,
		`ALTER TABLE boundary_events ADD COLUMN followup_shazam_id TEXT`,
		`ALTER TABLE boundary_events ADD COLUMN followup_collection_id INTEGER`,
		`ALTER TABLE boundary_events ADD COLUMN followup_play_history_id INTEGER`,
		`ALTER TABLE boundary_events ADD COLUMN followup_new_recording INTEGER`,
		`ALTER TABLE boundary_events ADD COLUMN early_boundary INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE boundary_events ADD COLUMN followup_recorded_at TEXT`,
		`ALTER TABLE collection ADD COLUMN boundary_sensitive INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE collection ADD COLUMN discogs_url TEXT`,
		`CREATE TABLE IF NOT EXISTS rms_learning (
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
	// Idempotent: merge legacy recognition_summary rows (provider key "Shazam" → "Shazamio").
	// Keep in sync with internal/library migrations after rms_learning.
	for _, stmt := range []string{
		`INSERT INTO recognition_summary (provider, event, count)
			SELECT 'Shazamio', event, count FROM recognition_summary WHERE provider = 'Shazam'
			ON CONFLICT(provider, event) DO UPDATE SET count = count + excluded.count`,
		`DELETE FROM recognition_summary WHERE provider = 'Shazam'`,
		`INSERT INTO recognition_summary (provider, event, count)
			SELECT 'ShazamioContinuity', event, count FROM recognition_summary WHERE provider = 'ShazamContinuity'
			ON CONFLICT(provider, event) DO UPDATE SET count = count + excluded.count`,
		`DELETE FROM recognition_summary WHERE provider = 'ShazamContinuity'`,
	} {
		if _, err := l.db.Exec(stmt); err != nil {
			_ = l.db.Close()
			return nil, fmt.Errorf("library: merge legacy shazam recognition summary: %w", err)
		}
	}
	if err := ensureLibrarySyncSchema(l.db); err != nil {
		_ = l.db.Close()
		return nil, err
	}
	if err := l.reconcileSchemaMigrations(); err != nil {
		_ = l.db.Close()
		return nil, err
	}
	return l, nil
}

// reconcileSchemaMigrations keeps schema_migrations aligned with columns that
// may already exist in restored databases.
//
// Why this matters:
// - Older backups can contain additive columns (e.g. discogs_url) while
//   schema_migrations lags behind.
// - If state-manager later runs its official migration chain, it can fail on
//   duplicate-column errors unless the version marker is reconciled.
func (l *LibraryDB) reconcileSchemaMigrations() error {
	if l == nil || l.db == nil {
		return nil
	}
	if _, err := l.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return fmt.Errorf("library: ensure schema_migrations: %w", err)
	}

	var maxVersion int
	if err := l.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		return fmt.Errorf("library: read max schema version: %w", err)
	}

	rows, err := l.db.Query(`PRAGMA table_info(collection)`)
	if err != nil {
		return fmt.Errorf("library: inspect collection columns: %w", err)
	}
	defer rows.Close()

	hasDiscogsURL := false
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("library: scan table_info(collection): %w", err)
		}
		if strings.EqualFold(name, "discogs_url") {
			hasDiscogsURL = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("library: iterate table_info(collection): %w", err)
	}

	// Keep in sync with internal/library migrations: v52 adds collection.discogs_url.
	if hasDiscogsURL && maxVersion < 52 {
		if _, err := l.db.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES (52)`); err != nil {
			return fmt.Errorf("library: reconcile migration v52: %w", err)
		}
		log.Printf("library: reconciled schema_migrations to include v52 (discogs_url present)")
	}
	return nil
}

// getRecognitionStats returns a map of provider -> event -> count.
func (l *LibraryDB) getRecognitionStats() (map[string]map[string]int, error) {
	rows, err := l.db.Query(`
		SELECT provider, event, count
		FROM recognition_summary
		WHERE provider != 'Fingerprint'`)
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

// rmsLearningRowDTO is one row for GET /api/recognition/rms-learning.
type rmsLearningRowDTO struct {
	FormatKey      string   `json:"format_key"`
	SilenceTotal   int64    `json:"silence_total"`
	MusicTotal     int64    `json:"music_total"`
	DerivedEnter   *float64 `json:"derived_enter,omitempty"`
	DerivedExit    *float64 `json:"derived_exit,omitempty"`
	UpdatedAt      string   `json:"updated_at"`
	ReadinessLevel string   `json:"readiness_level"` // "collecting" | "separating" | "ready"
	SilencePct     int      `json:"silence_pct"`     // 0–100, capped at 100
	MusicPct       int      `json:"music_pct"`       // 0–100, capped at 100
}

// rmsLearningListResponse is JSON for GET /api/recognition/rms-learning.
type rmsLearningListResponse struct {
	Rows              []rmsLearningRowDTO `json:"rows"`
	MinSilenceSamples int                 `json:"min_silence_samples"`
	MinMusicSamples   int                 `json:"min_music_samples"`
	AutonomousApply   bool                `json:"autonomous_apply"`
}

type importRMSBaselineRequest struct {
	Overwrite bool `json:"overwrite"`
}

func rmsReadinessLevel(silN, musN int64, minSil, minMus int, hasDerived bool) string {
	if silN < int64(minSil) || musN < int64(minMus) {
		return "collecting"
	}
	if !hasDerived {
		return "separating"
	}
	return "ready"
}

func rmsPct(n int64, min int) int {
	if min <= 0 {
		return 100
	}
	v := int(n * 100 / int64(min))
	if v > 100 {
		return 100
	}
	return v
}

func (l *LibraryDB) listRMSLearningRows() ([]rmsLearningRowDTO, error) {
	rows, err := l.db.Query(`
		SELECT format_key, silence_total, music_total, derived_enter, derived_exit, updated_at
		FROM rms_learning
		ORDER BY format_key`)
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "no such table") {
			return []rmsLearningRowDTO{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	var out []rmsLearningRowDTO
	for rows.Next() {
		var r rmsLearningRowDTO
		var de, dx sql.NullFloat64
		if err := rows.Scan(&r.FormatKey, &r.SilenceTotal, &r.MusicTotal, &de, &dx, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if de.Valid {
			v := de.Float64
			r.DerivedEnter = &v
		}
		if dx.Valid {
			v := dx.Float64
			r.DerivedExit = &v
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []rmsLearningRowDTO{}
	}
	return out, nil
}

// recognitionAttemptRowDTO is one row for GET /api/recognition/attempts.
type recognitionAttemptRowDTO struct {
	ID                  int64   `json:"id"`
	OccurredAt          string  `json:"occurred_at"`
	Provider            string  `json:"provider"`
	TriggerSource       string  `json:"trigger_source"`
	BoundaryEventID     *int64  `json:"boundary_event_id,omitempty"`
	IsHardBoundary      bool    `json:"is_hard_boundary"`
	Phase               string  `json:"phase"`
	SkipMs              int     `json:"skip_ms"`
	CaptureDurationMs   int     `json:"capture_duration_ms"`
	Outcome             string  `json:"outcome"`
	ErrorClass          string  `json:"error_class,omitempty"`
	LatencyMs           int     `json:"latency_ms"`
	RMSMean             float64 `json:"rms_mean"`
	RMSPeak             float64 `json:"rms_peak"`
	PhysicalFormat      string  `json:"physical_format"`
}

type recognitionAttemptListResponse struct {
	Rows []recognitionAttemptRowDTO `json:"rows"`
}

func (l *LibraryDB) listRecognitionAttempts(limit int) ([]recognitionAttemptRowDTO, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := l.db.Query(`
		SELECT id, occurred_at, provider, trigger_source, boundary_event_id, is_hard_boundary,
			phase, skip_ms, capture_duration_ms, outcome, error_class, latency_ms,
			rms_mean, rms_peak, physical_format
		FROM recognition_attempts
		ORDER BY id DESC
		LIMIT ?`, limit)
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "no such table") {
			return []recognitionAttemptRowDTO{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []recognitionAttemptRowDTO
	for rows.Next() {
		var r recognitionAttemptRowDTO
		var boundary sql.NullInt64
		var hard int
		if err := rows.Scan(&r.ID, &r.OccurredAt, &r.Provider, &r.TriggerSource, &boundary, &hard,
			&r.Phase, &r.SkipMs, &r.CaptureDurationMs, &r.Outcome, &r.ErrorClass, &r.LatencyMs,
			&r.RMSMean, &r.RMSPeak, &r.PhysicalFormat); err != nil {
			return nil, err
		}
		r.IsHardBoundary = hard != 0
		if boundary.Valid {
			v := boundary.Int64
			r.BoundaryEventID = &v
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []recognitionAttemptRowDTO{}
	}
	return out, nil
}

// boundaryStatsResponse is JSON for GET /api/recognition/boundary-stats.
type boundaryStatsResponse struct {
	PeriodDays           int                           `json:"period_days"`
	Total                int                           `json:"total"`
	ByOutcome            map[string]int                `json:"by_outcome"`
	ActionableTotal      int                           `json:"actionable_total"`
	FireRate             float64                       `json:"fire_rate"` // fraction of actionable outcomes that fired; -1 if not applicable
	FollowupTotals       map[string]int                `json:"followup_totals,omitempty"`
	EarlyBoundaryTotal   int                           `json:"early_boundary_total"`
	FollowupLinkedTotal  int                           `json:"followup_linked_total"`
	CalibrationReadiness *boundaryCalibrationReadiness `json:"calibration_readiness,omitempty"`
}

// boundaryCalibrationReadiness explains whether paired follow-up telemetry is
// sufficient for bounded R3 nudges and clarifies what is (not) persisted as RMS.
type boundaryCalibrationReadiness struct {
	R3LookbackDays               int    `json:"r3_lookback_days"`
	MinFollowupPairsRequired     int    `json:"min_followup_pairs_required"`
	PairedFollowupsR3Window      int    `json:"paired_followups_r3_window"`
	ReadinessLevel               string `json:"readiness_level"`
	ReadinessMessage             string `json:"readiness_message"`
	ReadyForR3Nudges             bool   `json:"ready_for_r3_nudges"`
	R3TelemetryNudgesEnabled     bool   `json:"r3_telemetry_nudges_enabled"`
	AutonomousCalibrationEnabled bool   `json:"autonomous_calibration_enabled"`
	EffectiveCalibrationNudgesOn bool   `json:"effective_calibration_nudges_on"`
	RmsLearningEnabled           bool   `json:"rms_learning_enabled"`
	RmsAutonomousApply           bool   `json:"rms_autonomous_apply"`
	RmsCaptureNote               string `json:"rms_capture_note"`
}

// providerHealthEntry is one row in GET /api/recognition/provider-health.
type providerHealthEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	// Configured is true when the provider is enabled in recognition.providers[]
	// AND has non-empty credentials in config (e.g. host/key/secret for ACRCloud).
	// false means "enabled but credentials missing" — provider will not run.
	Configured     bool     `json:"configured"`
	RateLimited    bool     `json:"rate_limited"`
	BackoffExpires *int64   `json:"backoff_expires,omitempty"` // Unix epoch second; key omitted (not null) when not rate-limited
	LastSuccessAt  *int64   `json:"last_success_at,omitempty"` // Unix epoch second; key omitted when no success in 24h
	LastAttemptAt  *int64   `json:"last_attempt_at,omitempty"` // Unix epoch second; key omitted when no attempt in 24h
	Attempts24h    int      `json:"attempts_24h"`
	SuccessRate24h *float64 `json:"success_rate_24h,omitempty"` // key omitted when no attempts; otherwise 0.0–1.0
}

// providerHealthResponse is JSON for GET /api/recognition/provider-health.
// DataComplete is false when state.json or the SQLite DB was unavailable —
// the client should treat the data as a best-effort snapshot.
type providerHealthResponse struct {
	Providers    []providerHealthEntry `json:"providers"`
	DataComplete bool                  `json:"data_complete"`
}

// stateFileProviderBackoff is the minimal parse target for extracting the
// top-level provider_backoff map from /tmp/oceano-state.json. This field is
// always present when providers are rate-limited, regardless of the active
// source (unlike recognition.backoff_expires which is omitted during AirPlay).
type stateFileProviderBackoff struct {
	ProviderBackoff map[string]int64 `json:"provider_backoff"`
}

// isProviderCredentialed reports whether provider id has the required
// credentials in cfg to actually run. "Enabled" in providers[] is necessary
// but not sufficient — missing credentials prevent the provider from starting.
func isProviderCredentialed(id string, cfg Config) bool {
	switch id {
	case "acrcloud":
		return strings.TrimSpace(cfg.Recognition.ACRCloudHost) != "" &&
			strings.TrimSpace(cfg.Recognition.ACRCloudAccessKey) != "" &&
			strings.TrimSpace(cfg.Recognition.ACRCloudSecretKey) != ""
	case "shazam":
		return cfg.Recognition.ShazamioRecognizerEnabled
	case "audd":
		return strings.TrimSpace(cfg.Recognition.AudDAPIToken) != ""
	default:
		return false
	}
}

// providerHealth24hStats holds aggregated attempt stats for one canonical provider ID.
type providerHealth24hStats struct {
	Attempts      int
	Successes     int
	LastSuccessAt int64 // Unix epoch second; 0 if none
	LastAttemptAt int64 // Unix epoch second; 0 if none
}

// dbProviderToCanonicalID maps a recognizer Name() as stored in recognition_attempts
// to the stable canonical ID used in config.json and state.json.
func dbProviderToCanonicalID(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "acrcloud"):
		return "acrcloud"
	case strings.Contains(lower, "shazam"):
		return "shazam"
	case strings.Contains(lower, "audd"):
		return "audd"
	default:
		return ""
	}
}

// providerDisplayName returns a human-readable label for a canonical provider ID.
func providerDisplayName(id string) string {
	switch id {
	case "acrcloud":
		return "ACRCloud"
	case "shazam":
		return "Shazam"
	case "audd":
		return "AudD"
	default:
		return id
	}
}

// recognitionProviderHealthStats returns per-canonical-ID attempt stats for
// the last 24 hours, aggregating all DB provider name variants that share an ID.
func (l *LibraryDB) recognitionProviderHealthStats() (map[string]*providerHealth24hStats, error) {
	rows, err := l.db.Query(`
		SELECT provider,
		       COUNT(*) AS attempts,
		       SUM(CASE WHEN outcome = 'success' THEN 1 ELSE 0 END) AS successes,
		       MAX(CASE WHEN outcome = 'success'
		               THEN CAST(strftime('%s', occurred_at) AS INTEGER)
		               ELSE NULL END) AS last_success_at,
		       MAX(CAST(strftime('%s', occurred_at) AS INTEGER)) AS last_attempt_at
		FROM recognition_attempts
		WHERE occurred_at >= datetime('now', '-24 hours')
		GROUP BY provider`)
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*providerHealth24hStats)
	for rows.Next() {
		var dbName string
		var attempts, successes int
		var lastSuccessAt, lastAttemptAt sql.NullInt64
		if err := rows.Scan(&dbName, &attempts, &successes, &lastSuccessAt, &lastAttemptAt); err != nil {
			return nil, err
		}
		id := dbProviderToCanonicalID(dbName)
		if id == "" {
			continue
		}
		if _, ok := result[id]; !ok {
			result[id] = &providerHealth24hStats{}
		}
		s := result[id]
		s.Attempts += attempts
		s.Successes += successes
		if lastSuccessAt.Valid && lastSuccessAt.Int64 > s.LastSuccessAt {
			s.LastSuccessAt = lastSuccessAt.Int64
		}
		if lastAttemptAt.Valid && lastAttemptAt.Int64 > s.LastAttemptAt {
			s.LastAttemptAt = lastAttemptAt.Int64
		}
	}
	return result, rows.Err()
}

func (l *LibraryDB) countR3PairedFollowupsSince(sinceRFC3339 string) (int, error) {
	var n int
	err := l.db.QueryRow(`
		SELECT COUNT(*) FROM boundary_events
		WHERE occurred_at >= ?
		  AND outcome = 'fired'
		  AND followup_outcome IN ('matched', 'same_track_restored')`, sinceRFC3339).Scan(&n)
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "no such table") || strings.Contains(errText, "no such column") {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}

func buildBoundaryCalibrationReadiness(l *LibraryDB, cfg *Config) *boundaryCalibrationReadiness {
	lookback := 14
	minPairs := 25
	r3On := false
	if cfg.Advanced.TelemetryNudges != nil {
		t := cfg.Advanced.TelemetryNudges
		r3On = t.Enabled
		if t.LookbackDays > 0 {
			lookback = t.LookbackDays
		}
		if t.MinFollowupPairs > 0 {
			minPairs = t.MinFollowupPairs
		}
	}
	auto := cfg.Advanced.AutonomousCalibration != nil && cfg.Advanced.AutonomousCalibration.Enabled
	effective := r3On || auto

	since := time.Now().UTC().Add(-time.Duration(lookback) * 24 * time.Hour).Format(time.RFC3339Nano)
	paired, _ := l.countR3PairedFollowupsSince(since)

	level, msg := classifyCalibrationReadinessLevel(paired, minPairs)
	ready := paired >= minPairs

	rmsEn := true // default: collect without applying, matching state-manager default
	rmsAp := false
	if cfg.Advanced.RMSPercentileLearning != nil {
		rms := cfg.Advanced.RMSPercentileLearning
		if rms.Enabled != nil {
			rmsEn = *rms.Enabled
		}
		rmsAp = rms.AutonomousApply
	}

	return &boundaryCalibrationReadiness{
		R3LookbackDays:               lookback,
		MinFollowupPairsRequired:     minPairs,
		PairedFollowupsR3Window:      paired,
		ReadinessLevel:               level,
		ReadinessMessage:             msg,
		ReadyForR3Nudges:             ready,
		R3TelemetryNudgesEnabled:     r3On,
		AutonomousCalibrationEnabled: auto,
		EffectiveCalibrationNudgesOn: effective,
		RmsLearningEnabled:           rmsEn,
		RmsAutonomousApply:           rmsAp,
		RmsCaptureNote:               "Per-event RMS is not stored in boundary_events. Optional RMS percentile learning writes aggregated histograms to table rms_learning (per format_key). Table recognition_attempts stores per-provider capture WAV RMS (mean/peak, 0..1) keyed by the same normalized format for diagnostics. Wizard calibration_profiles remain useful for vinyl gap (transition) metrics.",
	}
}

func classifyCalibrationReadinessLevel(paired, minRequired int) (level, message string) {
	if minRequired <= 0 {
		minRequired = 25
	}
	switch {
	case paired < minRequired:
		return "insufficient", "Not enough paired follow-ups (matched + same_track_restored on fired rows) in the R3 lookback window; bounded nudges stay idle until the minimum is met."
	case paired < minRequired*2:
		return "low", "Borderline sample size; nudges may apply but false-positive ratio estimates are noisy."
	case paired < minRequired*4:
		return "adequate", "Enough paired events for stable bounded nudges under typical drift."
	default:
		return "strong", "Rich paired telemetry; good confidence for telemetry-driven calibration nudges within configured caps."
	}
}

func (l *LibraryDB) getBoundaryEventStats(days int, cfg *Config) (*boundaryStatsResponse, error) {
	out := &boundaryStatsResponse{
		PeriodDays: days,
		ByOutcome:  make(map[string]int),
		FireRate:   -1,
	}
	var (
		rows *sql.Rows
		err  error
	)
	if days <= 0 {
		rows, err = l.db.Query(`SELECT outcome, COUNT(*) FROM boundary_events GROUP BY outcome`)
	} else {
		cut := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339Nano)
		rows, err = l.db.Query(`SELECT outcome, COUNT(*) FROM boundary_events WHERE occurred_at >= ? GROUP BY outcome`, cut)
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return out, nil
		}
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o string
		var c int
		if err := rows.Scan(&o, &c); err != nil {
			return nil, err
		}
		out.ByOutcome[o] = c
		out.Total += c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	actionableOutcomes := []string{
		"fired",
		"suppressed_duration_guard",
		"ignored_mature_progress",
		"suppressed_not_physical",
		"trigger_channel_full",
	}
	for _, k := range actionableOutcomes {
		out.ActionableTotal += out.ByOutcome[k]
	}
	if out.ActionableTotal > 0 {
		out.FireRate = float64(out.ByOutcome["fired"]) / float64(out.ActionableTotal)
	}

	out.FollowupTotals = make(map[string]int)
	var fuRows *sql.Rows
	if days <= 0 {
		fuRows, err = l.db.Query(`SELECT COALESCE(followup_outcome,''), COUNT(*) FROM boundary_events WHERE followup_outcome IS NOT NULL AND followup_outcome != '' GROUP BY followup_outcome`)
	} else {
		cut := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339Nano)
		fuRows, err = l.db.Query(`SELECT COALESCE(followup_outcome,''), COUNT(*) FROM boundary_events WHERE occurred_at >= ? AND followup_outcome IS NOT NULL AND followup_outcome != '' GROUP BY followup_outcome`, cut)
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such column") {
			return out, nil
		}
		return nil, err
	}
	defer fuRows.Close()
	for fuRows.Next() {
		var o string
		var c int
		if err := fuRows.Scan(&o, &c); err != nil {
			return nil, err
		}
		out.FollowupTotals[o] = c
		out.FollowupLinkedTotal += c
	}
	if err := fuRows.Err(); err != nil {
		return nil, err
	}

	var earlyN int
	if days <= 0 {
		err = l.db.QueryRow(`
			SELECT COUNT(*) FROM boundary_events
			WHERE IFNULL(early_boundary,0) != 0 AND followup_outcome IS NOT NULL AND followup_outcome != ''`).Scan(&earlyN)
	} else {
		cut := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339Nano)
		err = l.db.QueryRow(`
			SELECT COUNT(*) FROM boundary_events
			WHERE occurred_at >= ? AND IFNULL(early_boundary,0) != 0 AND followup_outcome IS NOT NULL AND followup_outcome != ''`, cut).Scan(&earlyN)
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such column") {
			return out, nil
		}
		return nil, err
	}
	out.EarlyBoundaryTotal = earlyN
	if cfg != nil {
		out.CalibrationReadiness = buildBoundaryCalibrationReadiness(l, cfg)
	}
	return out, nil
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
		       COALESCE(duration_ms,0), play_count, first_played, last_played, COALESCE(user_confirmed,0),
		       COALESCE(boundary_sensitive,0), COALESCE(discogs_url,'')
		FROM collection ORDER BY last_played DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LibraryEntry
	for rows.Next() {
		var e LibraryEntry
		var confirmed, boundarySens int
		if err := rows.Scan(&e.ID, &e.ACRID, &e.ShazamID, &e.Title, &e.Artist, &e.Album, &e.Label, &e.Released, &e.Format,
			&e.TrackNumber, &e.ArtworkPath, &e.DurationMs, &e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed, &boundarySens,
			&e.DiscogsURL); err != nil {
			return nil, err
		}
		e.UserConfirmed = confirmed == 1
		e.BoundarySensitive = boundarySens == 1
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// getLibraryVersion returns oceano_library_sync.library_version (0 if missing).
func (l *LibraryDB) getLibraryVersion() (int64, error) {
	if l == nil || l.db == nil {
		return 0, nil
	}
	var v sql.NullInt64
	err := l.db.QueryRow(`SELECT library_version FROM oceano_library_sync WHERE id = 1`).Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

// entryByID returns one collection row or nil if not found.
func (l *LibraryDB) entryByID(id int64) (*LibraryEntry, error) {
	if l == nil || l.db == nil {
		return nil, nil
	}
	row := l.db.QueryRow(`
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist, COALESCE(album,''), COALESCE(label,''),
		       COALESCE(released,''), COALESCE(format,'Unknown'),
		       COALESCE(track_number,''), COALESCE(artwork_path,''),
		       COALESCE(duration_ms,0), play_count, first_played, last_played, COALESCE(user_confirmed,0),
		       COALESCE(boundary_sensitive,0), COALESCE(discogs_url,'')
		FROM collection WHERE id = ?`, id)
	var e LibraryEntry
	var confirmed, boundarySens int
	err := row.Scan(&e.ID, &e.ACRID, &e.ShazamID, &e.Title, &e.Artist, &e.Album, &e.Label, &e.Released, &e.Format,
		&e.TrackNumber, &e.ArtworkPath, &e.DurationMs, &e.PlayCount, &e.FirstPlayed, &e.LastPlayed, &confirmed, &boundarySens,
		&e.DiscogsURL)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.UserConfirmed = confirmed == 1
	e.BoundarySensitive = boundarySens == 1
	return &e, nil
}

// LibraryChangesResponse is JSON for GET /api/library/changes.
type LibraryChangesResponse struct {
	LibraryVersion int64          `json:"library_version"`
	DeletedIDs     []int64        `json:"deleted_ids"`
	Upserts        []LibraryEntry `json:"upserts"`
}

// libraryChangesSince returns rows touched after sinceVersion (exclusive), ordered by changelog seq.
func (l *LibraryDB) libraryChangesSince(sinceVersion int64) (*LibraryChangesResponse, error) {
	cur, err := l.getLibraryVersion()
	if err != nil {
		return nil, err
	}
	out := &LibraryChangesResponse{LibraryVersion: cur, DeletedIDs: []int64{}, Upserts: []LibraryEntry{}}
	if sinceVersion < 0 {
		sinceVersion = 0
	}
	if sinceVersion >= cur {
		return out, nil
	}
	rows, err := l.db.Query(`
		SELECT collection_id, op FROM library_changelog WHERE version > ? ORDER BY seq`, sinceVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	upsertIDs := make(map[int64]struct{})
	for rows.Next() {
		var cid int64
		var op string
		if err := rows.Scan(&cid, &op); err != nil {
			return nil, err
		}
		switch strings.ToLower(strings.TrimSpace(op)) {
		case "delete":
			out.DeletedIDs = append(out.DeletedIDs, cid)
			delete(upsertIDs, cid)
		case "upsert":
			upsertIDs[cid] = struct{}{}
		default:
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id := range upsertIDs {
		e, err := l.entryByID(id)
		if err != nil {
			return nil, err
		}
		if e != nil {
			out.Upserts = append(out.Upserts, *e)
		}
	}
	// Map iteration order is non-deterministic; stable order helps tests and clients.
	if len(out.Upserts) > 1 {
		sort.Slice(out.Upserts, func(i, j int) bool { return out.Upserts[i].ID < out.Upserts[j].ID })
	}
	if len(out.DeletedIDs) > 1 {
		sort.Slice(out.DeletedIDs, func(i, j int) bool { return out.DeletedIDs[i] < out.DeletedIDs[j] })
	}
	return out, nil
}

// search returns user-confirmed tracks matching title/artist/album query.
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

// update patches editable fields for a single entry and cascades the changes
// to any play_history rows linked to the same collection entry so the history
// reflects corrected metadata (format badge, title, artwork, etc.) immediately.
func (l *LibraryDB) update(id int64, title, artist, album, label, released, format, trackNumber, artworkPath string, durationMs int, boundarySensitive bool) error {
	boundarySens := 0
	if boundarySensitive {
		boundarySens = 1
	}
	_, err := l.db.Exec(`
		UPDATE collection
		SET title=?, artist=?, album=?, label=?, released=?, format=?, track_number=?, artwork_path=?,
		    duration_ms=CASE WHEN ? > 0 THEN ? ELSE duration_ms END,
		    user_confirmed=1,
		    boundary_sensitive=?
		WHERE id=?`,
		title, artist, album, label, released, format, trackNumber, artworkPath,
		durationMs, durationMs, boundarySens, id,
	)
	if err != nil {
		return err
	}
	_, err = l.db.Exec(`
		UPDATE play_history
		SET title=?, artist=?, album=?, track_number=?,
		    media_format=?,
		    artwork_path=CASE WHEN ? != '' THEN ? ELSE artwork_path END
		WHERE collection_id=?`,
		title, artist, album, trackNumber,
		format,
		artworkPath, artworkPath,
		id,
	)
	if err != nil {
		return err
	}
	return l.backfillBoundaryEventsFormatResolvedForCollection(id, format)
}

// backfillBoundaryEventsFormatResolvedForCollection sets format_resolved on
// boundary telemetry rows for this library entry (R2). Idempotent for the same
// resolved label; clearing Unknown removes resolution so analytics fall back to
// format_at_event only.
func (l *LibraryDB) backfillBoundaryEventsFormatResolvedForCollection(collectionID int64, libraryFormat string) error {
	if l == nil || l.db == nil {
		return nil
	}
	norm := strings.TrimSpace(libraryFormat)
	if norm == "" {
		norm = "Unknown"
	}
	switch norm {
	case "Vinyl", "CD":
		ts := time.Now().UTC().Format(time.RFC3339Nano)
		_, err := l.db.Exec(`
			UPDATE boundary_events
			SET format_resolved = ?, format_resolved_at = ?
			WHERE collection_id = ?`,
			norm, ts, collectionID,
		)
		if err != nil {
			errText := strings.ToLower(err.Error())
			if strings.Contains(errText, "no such table") || strings.Contains(errText, "no such column") {
				return nil
			}
			return err
		}
		return nil
	default:
		_, err := l.db.Exec(`
			UPDATE boundary_events
			SET format_resolved = NULL, format_resolved_at = NULL
			WHERE collection_id = ?`,
			collectionID,
		)
		if err != nil {
			errText := strings.ToLower(err.Error())
			if strings.Contains(errText, "no such table") || strings.Contains(errText, "no such column") {
				return nil
			}
			return err
		}
		return nil
	}
}

// resolveStub merges an unconfirmed entry (stub) into an existing confirmed
// target: play_history rows are repointed, play counts merged, provider IDs
// copied if the target's fields are empty, then the stub is deleted.
func (l *LibraryDB) resolveStub(stubID, targetID int64) (*LibraryEntry, error) {
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
	defer tx.Rollback()

	var stubPlayCount int
	var stubLastPlayed, stubACRID, stubShazamID string
	err = tx.QueryRow(`
		SELECT play_count, last_played, COALESCE(acrid,''), COALESCE(shazam_id,'')
		FROM collection
		WHERE id = ? AND user_confirmed = 0`, stubID).
		Scan(&stubPlayCount, &stubLastPlayed, &stubACRID, &stubShazamID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("entry %d is not an unresolved stub", stubID)
	}
	if err != nil {
		return nil, err
	}

	target := &LibraryEntry{}
	var confirmed int
	err = tx.QueryRow(`
		SELECT id, COALESCE(acrid,''), COALESCE(shazam_id,''), title, artist,
		       COALESCE(album,''), play_count, last_played, COALESCE(user_confirmed,0)
		FROM collection WHERE id = ?`, targetID).
		Scan(&target.ID, &target.ACRID, &target.ShazamID, &target.Title, &target.Artist,
			&target.Album, &target.PlayCount, &target.LastPlayed, &confirmed)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("target entry %d not found", targetID)
	}
	if err != nil {
		return nil, err
	}
	target.UserConfirmed = confirmed == 1

	// Repoint play_history rows from stub to target.
	if _, err := tx.Exec(`UPDATE play_history SET collection_id=? WHERE collection_id=?`, targetID, stubID); err != nil {
		return nil, err
	}

	// Merge play count and last_played.
	newCount := target.PlayCount + stubPlayCount
	newLast := target.LastPlayed
	if stubLastPlayed > newLast {
		newLast = stubLastPlayed
	}
	if _, err := tx.Exec(`UPDATE collection SET play_count=?, last_played=? WHERE id=?`,
		newCount, newLast, targetID); err != nil {
		return nil, err
	}

	// Copy provider IDs to target only if target has none AND no other entry owns the ID.
	// Skip silently if the ID already belongs to another confirmed entry (e.g. the stub
	// was misidentified as a different track and carries that track's provider IDs).
	if target.ACRID == "" && stubACRID != "" {
		var clash int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM collection WHERE acrid=? AND id!=?`, stubACRID, targetID).Scan(&clash)
		if clash == 0 {
			if _, err := tx.Exec(`UPDATE collection SET acrid=? WHERE id=?`, stubACRID, targetID); err != nil {
				return nil, err
			}
		}
	}
	if target.ShazamID == "" && stubShazamID != "" {
		var clash int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM collection WHERE shazam_id=? AND id!=?`, stubShazamID, targetID).Scan(&clash)
		if clash == 0 {
			if _, err := tx.Exec(`UPDATE collection SET shazam_id=? WHERE id=?`, stubShazamID, targetID); err != nil {
				return nil, err
			}
		}
	}

	// Delete the stub (play_history already repointed, no FK violation).
	if _, err := tx.Exec(`DELETE FROM collection WHERE id=?`, stubID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	target.PlayCount = newCount
	target.LastPlayed = newLast
	return target, nil
}

// deleteEntry removes a single entry by ID, nulling out any play_history
// references first to satisfy the foreign-key constraint.
func (l *LibraryDB) deleteEntry(id int64) error {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE play_history SET collection_id=NULL WHERE collection_id=?`, id); err != nil {
		return err
	}
	// boundary_events.collection_id references collection when the schema was
	// created by the state-manager migrations; without this UPDATE, DELETE fails
	// with FOREIGN KEY constraint under PRAGMA foreign_keys=ON.
	if _, err := tx.Exec(`UPDATE boundary_events SET collection_id=NULL WHERE collection_id=?`, id); err != nil {
		errText := strings.ToLower(err.Error())
		if !strings.Contains(errText, "no such table") && !strings.Contains(errText, "no such column") {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM collection WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ── HTTP handlers ──────────────────────────────────────────────────────────

// registerLibraryRoutes wires all /api/library/* endpoints into mux.
// libraryDBPath is read from the running state-manager service file so the web
// UI always talks to the same database without extra configuration.
func registerLibraryRoutes(mux *http.ServeMux, libraryDBPath string, stateFilePath string, artworkDir string, configPath string) {
	// GET  /api/library        → list all entries
	// GET  /api/library/search?q=...&limit=20 → search confirmed tracks
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
			body := []byte("[]")
			etag := weakJSONETag(body)
			w.Header().Set("ETag", etag)
			w.Header().Set("X-Oceano-Library-Version", "0")
			w.Header().Set("Cache-Control", "private, no-cache")
			if configETagMatches(r.Header.Get("If-None-Match"), etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
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
		body, err := json.Marshal(entries)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		libVer, _ := lib.getLibraryVersion()
		etag := weakJSONETag(body)
		w.Header().Set("ETag", etag)
		w.Header().Set("X-Oceano-Library-Version", strconv.FormatInt(libVer, 10))
		w.Header().Set("Cache-Control", "private, no-cache")
		if configETagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})

	// GET /api/library/changes?since_version=N — incremental collection updates.
	mux.HandleFunc("/api/library/changes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		since := int64(0)
		if raw := strings.TrimSpace(r.URL.Query().Get("since_version")); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
				since = v
			}
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&LibraryChangesResponse{})
			return
		}
		defer lib.close()
		out, err := lib.libraryChangesSince(since)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
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

	// GET /api/recognition/stats — get stats for active recognition providers.
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

	// GET /api/recognition/boundary-stats?days=30 — VU boundary telemetry (R1 / R1b).
	mux.HandleFunc("/api/recognition/boundary-stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		days := 30
		if dStr := r.URL.Query().Get("days"); dStr != "" {
			if d, err := strconv.Atoi(dStr); err == nil && d >= 0 {
				days = d
			}
		}
		if lib == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&boundaryStatsResponse{
				PeriodDays: days, ByOutcome: map[string]int{}, FireRate: -1,
			})
			return
		}
		defer lib.close()
		cfgSnap := defaultConfig()
		if c, err := loadConfig(configPath); err == nil {
			cfgSnap = c
		}
		stats, err := lib.getBoundaryEventStats(days, &cfgSnap)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})

	// GET /api/recognition/rms-learning — per-format histogram totals and derived VU thresholds.
	mux.HandleFunc("/api/recognition/rms-learning", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfgSnap := defaultConfig()
		if c, err := loadConfig(configPath); err == nil {
			cfgSnap = c
		}
		minSil, minMus := 400, 400
		autoApply := false
		if cfgSnap.Advanced.RMSPercentileLearning != nil {
			if v := cfgSnap.Advanced.RMSPercentileLearning.MinSilenceSamples; v > 0 {
				minSil = v
			}
			if v := cfgSnap.Advanced.RMSPercentileLearning.MinMusicSamples; v > 0 {
				minMus = v
			}
			autoApply = cfgSnap.Advanced.RMSPercentileLearning.AutonomousApply
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&rmsLearningListResponse{
				Rows: []rmsLearningRowDTO{}, MinSilenceSamples: minSil, MinMusicSamples: minMus,
			})
			return
		}
		defer lib.close()
		list, err := lib.listRMSLearningRows()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for i := range list {
			r := &list[i]
			r.ReadinessLevel = rmsReadinessLevel(r.SilenceTotal, r.MusicTotal, minSil, minMus, r.DerivedEnter != nil)
			r.SilencePct = rmsPct(r.SilenceTotal, minSil)
			r.MusicPct = rmsPct(r.MusicTotal, minMus)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&rmsLearningListResponse{
			Rows: list, MinSilenceSamples: minSil, MinMusicSamples: minMus, AutonomousApply: autoApply,
		})
	})

	// GET /api/recognition/attempts?limit=100 — recent per-provider recognition attempts (telemetry).
	mux.HandleFunc("/api/recognition/attempts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		limit := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				limit = n
			}
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&recognitionAttemptListResponse{Rows: []recognitionAttemptRowDTO{}})
			return
		}
		defer lib.close()
		rows, err := lib.listRecognitionAttempts(limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&recognitionAttemptListResponse{Rows: rows})
	})

	// POST /api/recognition/rms-learning/import-default — import baseline histograms for empty setups.
	mux.HandleFunc("/api/recognition/rms-learning/import-default", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req importRMSBaselineRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib == nil {
			if err := os.MkdirAll(filepath.Dir(libraryDBPath), 0o755); err != nil {
				jsonError(w, "failed to create library directory", http.StatusInternalServerError)
				return
			}
			f, err := os.OpenFile(libraryDBPath, os.O_CREATE, 0o644)
			if err != nil {
				jsonError(w, "failed to create library database", http.StatusInternalServerError)
				return
			}
			_ = f.Close()
			lib, err = openLibraryDB(libraryDBPath)
			if err != nil {
				jsonError(w, "failed to open library database", http.StatusInternalServerError)
				return
			}
			if lib == nil {
				jsonError(w, "library database unavailable", http.StatusInternalServerError)
				return
			}
		}
		defer lib.close()

		imported, hadExisting, err := lib.importDefaultRMSLearningBaseline(req.Overwrite)
		if err != nil {
			jsonError(w, "failed to import baseline: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if hadExisting {
			jsonError(w, "rms learning data already exists; retry with overwrite=true", http.StatusConflict)
			return
		}
		jsonOK(w, map[string]any{
			"ok":               true,
			"imported_formats": imported,
			"source":           "default_baseline",
			"note":             "Enable adaptive tuning and RMS autonomous apply, then Save & Restart Services.",
		})
	})

	// GET /api/recognition/provider-health — per-provider snapshot for the config screen.
	// Combines current rate-limit state from state.json with 24h attempt stats from SQLite.
	// Rate-limited is derived from backoff_expires > now (not from attempt heuristics).
	mux.HandleFunc("/api/recognition/provider-health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		cfg, _ := loadConfig(configPath)
		dataComplete := true

		// Read rate-limit backoff from the top-level provider_backoff field in
		// state.json. This field is present regardless of the active source
		// (unlike recognition.backoff_expires, which is omitted during AirPlay/BT).
		backoffExpires := map[string]int64{}
		if data, err := os.ReadFile(stateFilePath); err != nil {
			log.Printf("provider-health: state file unavailable: %v", err)
			dataComplete = false
		} else {
			var snap stateFileProviderBackoff
			if err := json.Unmarshal(data, &snap); err != nil {
				log.Printf("provider-health: state file parse error: %v", err)
				dataComplete = false
			} else if snap.ProviderBackoff != nil {
				backoffExpires = snap.ProviderBackoff
			}
		}

		// Query 24h attempt stats from SQLite.
		stats := map[string]*providerHealth24hStats{}
		if lib, err := openLibraryDB(libraryDBPath); err != nil {
			log.Printf("provider-health: library DB unavailable: %v", err)
			dataComplete = false
		} else if lib != nil {
			defer lib.close()
			if s, err := lib.recognitionProviderHealthStats(); err != nil {
				log.Printf("provider-health: stats query error: %v", err)
				dataComplete = false
			} else if s != nil {
				stats = s
			}
		}

		now := time.Now().Unix()
		entries := make([]providerHealthEntry, 0, len(cfg.Recognition.Providers))
		for _, p := range cfg.Recognition.Providers {
			if !p.Enabled {
				continue
			}
			entry := providerHealthEntry{
				ID:          p.ID,
				DisplayName: providerDisplayName(p.ID),
				Configured:  isProviderCredentialed(p.ID, cfg),
			}
			if until, ok := backoffExpires[p.ID]; ok && until > now {
				entry.RateLimited = true
				entry.BackoffExpires = &until
			}
			if s, ok := stats[p.ID]; ok {
				entry.Attempts24h = s.Attempts
				if s.Attempts > 0 {
					rate := float64(s.Successes) / float64(s.Attempts)
					entry.SuccessRate24h = &rate
				}
				if s.LastSuccessAt > 0 {
					v := s.LastSuccessAt
					entry.LastSuccessAt = &v
				}
				if s.LastAttemptAt > 0 {
					v := s.LastAttemptAt
					entry.LastAttemptAt = &v
				}
			}
			entries = append(entries, entry)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, no-cache")
		json.NewEncoder(w).Encode(providerHealthResponse{Providers: entries, DataComplete: dataComplete})
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
		case sub == "" && r.Method == http.MethodPut:
			handleUpdateEntry(w, r, lib, id, stateFilePath)
		case sub == "" && r.Method == http.MethodDelete:
			handleDeleteEntry(w, lib, id)
		case sub == "resolve" && r.Method == http.MethodPost:
			handleResolveStub(w, r, lib, id)
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
		Title             string `json:"title"`
		Artist            string `json:"artist"`
		Album             string `json:"album"`
		Label             string `json:"label"`
		Released          string `json:"released"`
		Format            string `json:"format"`
		TrackNumber       string `json:"track_number"`
		ArtworkPath       string `json:"artwork_path"`
		DurationMs        int    `json:"duration_ms"`
		BoundarySensitive bool   `json:"boundary_sensitive"`
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

	if err := lib.update(id, body.Title, body.Artist, body.Album, body.Label, body.Released, body.Format, body.TrackNumber, body.ArtworkPath, body.DurationMs, body.BoundarySensitive); err != nil {
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

func handleResolveStub(w http.ResponseWriter, r *http.Request, lib *LibraryDB, stubID int64) {
	var body struct {
		TargetID int64 `json:"target_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	target, err := lib.resolveStub(stubID, body.TargetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(target)
}
