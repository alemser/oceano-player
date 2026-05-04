package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestRecognitionDB creates an in-memory SQLite with just the tables
// needed for provider-health stats tests.
func newTestRecognitionDB(t *testing.T) *LibraryDB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`CREATE TABLE recognition_attempts (
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
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec(`CREATE INDEX recognition_attempts_occurred_at_idx ON recognition_attempts(occurred_at)`)
	if err != nil {
		t.Fatalf("create index: %v", err)
	}

	return &LibraryDB{db: db, path: path}
}

func insertAttempt(t *testing.T, db *LibraryDB, provider, outcome, occurredAt string) {
	t.Helper()
	_, err := db.db.Exec(
		`INSERT INTO recognition_attempts (occurred_at, provider, outcome) VALUES (?, ?, ?)`,
		occurredAt, provider, outcome,
	)
	if err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
}

// --- recognitionProviderHealthStats ---

func TestRecognitionProviderHealthStats_aggregatesByCanonicalID(t *testing.T) {
	lib := newTestRecognitionDB(t)

	now := time.Now().UTC()
	recent := now.Add(-10 * time.Minute).Format(time.RFC3339)
	old := now.Add(-48 * time.Hour).Format(time.RFC3339) // well outside 24h window (avoids SQLite text-compare edge case)

	insertAttempt(t, lib, "ACRCloud", "success", recent)
	insertAttempt(t, lib, "ACRCloud", "no_match", recent)
	insertAttempt(t, lib, "Shazam", "success", recent)
	insertAttempt(t, lib, "Shazamio", "success", recent)   // maps to "shazam"
	insertAttempt(t, lib, "ACRCloud", "success", old)      // excluded (>24h)

	stats, err := lib.recognitionProviderHealthStats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	acr := stats["acrcloud"]
	if acr == nil {
		t.Fatal("expected acrcloud stats")
	}
	if acr.Attempts != 2 {
		t.Errorf("acrcloud attempts = %d, want 2", acr.Attempts)
	}
	if acr.Successes != 1 {
		t.Errorf("acrcloud successes = %d, want 1", acr.Successes)
	}

	shazam := stats["shazam"]
	if shazam == nil {
		t.Fatal("expected shazam stats")
	}
	// "Shazam" + "Shazamio" both map to "shazam" → 2 attempts, 2 successes
	if shazam.Attempts != 2 {
		t.Errorf("shazam attempts = %d, want 2 (Shazam+Shazamio aggregated)", shazam.Attempts)
	}
	if shazam.Successes != 2 {
		t.Errorf("shazam successes = %d, want 2", shazam.Successes)
	}
}

func TestRecognitionProviderHealthStats_emptyTable(t *testing.T) {
	lib := newTestRecognitionDB(t)
	stats, err := lib.recognitionProviderHealthStats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty map, got %v", stats)
	}
}

func TestRecognitionProviderHealthStats_unknownProviderSkipped(t *testing.T) {
	lib := newTestRecognitionDB(t)
	recent := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	insertAttempt(t, lib, "Fingerprint", "success", recent)
	insertAttempt(t, lib, "Unknown", "success", recent)

	stats, err := lib.recognitionProviderHealthStats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected unknown providers to be skipped, got %v", stats)
	}
}

// --- isProviderCredentialed ---

func TestIsProviderCredentialed(t *testing.T) {
	full := Config{}
	full.Recognition.ACRCloudHost = "https://example.acrcloud.com"
	full.Recognition.ACRCloudAccessKey = "key"
	full.Recognition.ACRCloudSecretKey = "secret"
	full.Recognition.AudDAPIToken = "token"
	full.Recognition.ShazamioRecognizerEnabled = true

	empty := Config{}

	cases := []struct {
		id   string
		cfg  Config
		want bool
	}{
		{"acrcloud", full, true},
		{"acrcloud", empty, false},
		{"shazam", full, true},
		{"shazam", empty, false},
		{"audd", full, true},
		{"audd", empty, false},
		{"unknown", full, false},
	}
	for _, tc := range cases {
		got := isProviderCredentialed(tc.id, tc.cfg)
		if got != tc.want {
			t.Errorf("isProviderCredentialed(%q) with cfg = %v, got %v want %v", tc.id, tc.cfg.Recognition.ACRCloudHost != "", got, tc.want)
		}
	}
}

// --- handler integration ---

func TestProviderHealthHandler_noDBNoState(t *testing.T) {
	dir := t.TempDir()

	cfgPath := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	cfg.Recognition.Providers = []RecognitionProviderConfig{
		{ID: "acrcloud", Enabled: true},
	}
	data, _ := json.Marshal(cfg)
	os.WriteFile(cfgPath, data, 0o644)

	mux := http.NewServeMux()
	registerLibraryRoutes(mux, filepath.Join(dir, "missing.db"), filepath.Join(dir, "missing-state.json"), dir, cfgPath)

	req := httptest.NewRequest(http.MethodGet, "/api/recognition/provider-health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp providerHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DataComplete {
		t.Error("expected data_complete=false when state and DB are missing")
	}
	if len(resp.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(resp.Providers))
	}
	if resp.Providers[0].RateLimited {
		t.Error("expected rate_limited=false when state is missing")
	}
}

func TestProviderHealthHandler_rateLimitFromTopLevelBackoff(t *testing.T) {
	dir := t.TempDir()

	// Write state.json with top-level provider_backoff (not inside recognition).
	until := time.Now().Add(5 * time.Minute).Unix()
	stateJSON := map[string]interface{}{
		"source":           "AirPlay", // source is NOT Physical
		"state":            "playing",
		"provider_backoff": map[string]int64{"acrcloud": until},
	}
	stateData, _ := json.Marshal(stateJSON)
	statePath := filepath.Join(dir, "state.json")
	os.WriteFile(statePath, stateData, 0o644)

	cfgPath := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	cfg.Recognition.ACRCloudHost = "https://example.acrcloud.com"
	cfg.Recognition.ACRCloudAccessKey = "key"
	cfg.Recognition.ACRCloudSecretKey = "secret"
	cfg.Recognition.Providers = []RecognitionProviderConfig{
		{ID: "acrcloud", Enabled: true},
	}
	cfgData, _ := json.Marshal(cfg)
	os.WriteFile(cfgPath, cfgData, 0o644)

	mux := http.NewServeMux()
	registerLibraryRoutes(mux, filepath.Join(dir, "missing.db"), statePath, dir, cfgPath)

	req := httptest.NewRequest(http.MethodGet, "/api/recognition/provider-health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp providerHealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(resp.Providers))
	}
	p := resp.Providers[0]
	if !p.RateLimited {
		t.Error("expected rate_limited=true (from top-level provider_backoff, not recognition)")
	}
	if p.BackoffExpires == nil || *p.BackoffExpires != until {
		t.Errorf("expected backoff_expires=%d, got %v", until, p.BackoffExpires)
	}
	if !p.Configured {
		t.Error("expected configured=true (credentials present)")
	}
}
