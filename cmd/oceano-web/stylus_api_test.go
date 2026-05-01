package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newStylusMux(t *testing.T, dbPath string) *http.ServeMux {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := defaultConfig()
	cfg.Advanced.LibraryDB = dbPath
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	mux := http.NewServeMux()
	registerStylusRoutes(mux, cfgPath, dbPath)
	return mux
}

func requestJSON(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		payload = b
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestClassifyStylusState(t *testing.T) {
	cases := []struct {
		wear float64
		want string
	}{
		{wear: 0, want: "healthy"},
		{wear: 69.9, want: "healthy"},
		{wear: 70, want: "plan"},
		{wear: 90, want: "soon"},
		{wear: 101, want: "overdue"},
	}
	for _, tc := range cases {
		if got := classifyStylusState(tc.wear); got != tc.want {
			t.Fatalf("classifyStylusState(%v)=%q want %q", tc.wear, got, tc.want)
		}
	}
}

func TestStylusCatalogAndPutFlow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "library.db")
	mux := newStylusMux(t, dbPath)

	wCatalog := requestJSON(t, mux, http.MethodGet, "/api/stylus/catalog", nil)
	if wCatalog.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", wCatalog.Code, wCatalog.Body.String())
	}
	var catalog stylusCatalogResponse
	if err := json.NewDecoder(wCatalog.Body).Decode(&catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if len(catalog.Items) == 0 {
		t.Fatal("expected seeded stylus catalog")
	}

	wPut := requestJSON(t, mux, http.MethodPut, "/api/stylus", map[string]any{
		"enabled":    true,
		"catalog_id": catalog.Items[0].ID,
		"is_new":     true,
	})
	if wPut.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", wPut.Code, wPut.Body.String())
	}

	var getResp stylusGetResponse
	wGet := requestJSON(t, mux, http.MethodGet, "/api/stylus", nil)
	if wGet.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", wGet.Code, wGet.Body.String())
	}
	if err := json.NewDecoder(wGet.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if !getResp.Enabled || getResp.Stylus == nil {
		t.Fatalf("expected enabled active stylus, got %+v", getResp)
	}
	if getResp.Stylus.CatalogID == nil {
		t.Fatalf("expected catalog stylus profile")
	}
}

func TestStylusMetricsUseVinylHistory(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "library.db")
	mux := newStylusMux(t, dbPath)

	wPut := requestJSON(t, mux, http.MethodPut, "/api/stylus", map[string]any{
		"enabled":            true,
		"brand":              "Test",
		"model":              "Needle",
		"stylus_profile":     "Elliptical",
		"lifetime_hours":     100,
		"initial_used_hours": 10,
		"is_new":             false,
	})
	if wPut.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", wPut.Code, wPut.Body.String())
	}

	var putResp stylusGetResponse
	if err := json.NewDecoder(wPut.Body).Decode(&putResp); err != nil {
		t.Fatalf("decode put: %v", err)
	}
	if putResp.Stylus == nil {
		t.Fatal("expected stylus in response")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS play_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			listened_seconds INTEGER NOT NULL,
			media_format TEXT,
			started_at TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create play_history: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO play_history (listened_seconds, media_format, started_at)
		VALUES (7200, 'vinyl', ?)
	`, putResp.Stylus.InstalledAt); err != nil {
		t.Fatalf("insert play_history: %v", err)
	}

	wGet := requestJSON(t, mux, http.MethodGet, "/api/stylus", nil)
	if wGet.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", wGet.Code, wGet.Body.String())
	}
	var getResp stylusGetResponse
	if err := json.NewDecoder(wGet.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if getResp.Metrics.VinylHoursSinceInstall < 2 {
		t.Fatalf("expected vinyl hours >= 2, got %.2f", getResp.Metrics.VinylHoursSinceInstall)
	}
	if getResp.Metrics.StylusHoursTotal < 12 {
		t.Fatalf("expected total hours >= 12, got %.2f", getResp.Metrics.StylusHoursTotal)
	}
}

func TestStylusReplaceCreatesNewActiveProfile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "library.db")
	mux := newStylusMux(t, dbPath)

	wPut := requestJSON(t, mux, http.MethodPut, "/api/stylus", map[string]any{
		"enabled":        true,
		"brand":          "BrandA",
		"model":          "ModelA",
		"stylus_profile": "Elliptical",
		"lifetime_hours": 700,
		"is_new":         true,
	})
	if wPut.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", wPut.Code, wPut.Body.String())
	}

	wReplace := requestJSON(t, mux, http.MethodPost, "/api/stylus/replace", map[string]any{"is_new": true})
	if wReplace.Code != http.StatusOK {
		t.Fatalf("replace status=%d body=%s", wReplace.Code, wReplace.Body.String())
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var activeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stylus_profiles WHERE replaced_at IS NULL`).Scan(&activeCount); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected 1 active profile, got %d", activeCount)
	}

	var totalCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stylus_profiles`).Scan(&totalCount); err != nil {
		t.Fatalf("count total: %v", err)
	}
	if totalCount != 2 {
		t.Fatalf("expected 2 stylus profiles after replace, got %d", totalCount)
	}
}
