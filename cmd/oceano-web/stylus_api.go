package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type stylusCatalogItem struct {
	ID               int64  `json:"id"`
	Brand            string `json:"brand"`
	Model            string `json:"model"`
	StylusProfile    string `json:"stylus_profile"`
	MinHours         *int   `json:"min_hours,omitempty"`
	MaxHours         *int   `json:"max_hours,omitempty"`
	RecommendedHours int    `json:"recommended_hours"`
	SourceName       string `json:"source_name"`
	SourceURL        string `json:"source_url"`
	SourceNote       string `json:"source_note"`
	Confidence       string `json:"confidence"`
}

type stylusProfile struct {
	ID               int64   `json:"id"`
	CatalogID        *int64  `json:"catalog_id,omitempty"`
	Brand            string  `json:"brand"`
	Model            string  `json:"model"`
	StylusProfile    string  `json:"stylus_profile"`
	LifetimeHours    int     `json:"lifetime_hours"`
	InitialUsedHours float64 `json:"initial_used_hours"`
	InstalledAt      string  `json:"installed_at"`
	ReplacedAt       string  `json:"replaced_at,omitempty"`
	IsCustom         bool    `json:"is_custom"`
}

type stylusMetrics struct {
	VinylHoursSinceInstall float64 `json:"vinyl_hours_since_install"`
	StylusHoursTotal       float64 `json:"stylus_hours_total"`
	RemainingHours         float64 `json:"remaining_hours"`
	WearPercent            float64 `json:"wear_percent"`
	State                  string  `json:"state"`
}

type stylusGetResponse struct {
	Enabled bool           `json:"enabled"`
	Stylus  *stylusProfile `json:"stylus,omitempty"`
	Metrics stylusMetrics  `json:"metrics"`
}

type stylusPutRequest struct {
	Enabled          bool     `json:"enabled"`
	CatalogID        *int64   `json:"catalog_id,omitempty"`
	Brand            string   `json:"brand,omitempty"`
	Model            string   `json:"model,omitempty"`
	StylusProfile    string   `json:"stylus_profile,omitempty"`
	LifetimeHours    int      `json:"lifetime_hours,omitempty"`
	InitialUsedHours *float64 `json:"initial_used_hours,omitempty"`
	IsNew            *bool    `json:"is_new,omitempty"`
}

type stylusReplaceRequest struct {
	CatalogID        *int64   `json:"catalog_id,omitempty"`
	Brand            string   `json:"brand,omitempty"`
	Model            string   `json:"model,omitempty"`
	StylusProfile    string   `json:"stylus_profile,omitempty"`
	LifetimeHours    int      `json:"lifetime_hours,omitempty"`
	InitialUsedHours *float64 `json:"initial_used_hours,omitempty"`
	IsNew            *bool    `json:"is_new,omitempty"`
}

type stylusCatalogResponse struct {
	Items []stylusCatalogItem `json:"items"`
}

type stylusSeedItem struct {
	Brand            string
	Model            string
	StylusProfile    string
	MinHours         *int
	MaxHours         *int
	RecommendedHours int
	SourceName       string
	SourceURL        string
	SourceNote       string
	Confidence       string
}

type stylusServer struct {
	db *sql.DB
}

func intPtr(v int) *int {
	return &v
}

var stylusSeedCatalog = []stylusSeedItem{
	{
		Brand:            "LP Gear",
		Model:            "CFN3600LE",
		StylusProfile:    "Elliptical",
		MinHours:         intPtr(500),
		MaxHours:         intPtr(1000),
		RecommendedHours: 750,
		SourceName:       "LP Gear Product Page",
		SourceURL:        "https://www.lpgear.com/product/LPGCFN3600LE.html",
		SourceNote:       "Product specification lists stylus life around 500-1000 hours.",
		Confidence:       "high",
	},
	{
		Brand:            "Denon",
		Model:            "DL-103",
		StylusProfile:    "Conical",
		MinHours:         intPtr(300),
		MaxHours:         intPtr(500),
		RecommendedHours: 500,
		SourceName:       "Stereophile",
		SourceURL:        "https://www.stereophile.com/content/denon-dl-103-phono-cartridge",
		SourceNote:       "Conservative cap for conical profile in broadcast-style use.",
		Confidence:       "medium",
	},
	{Brand: "Audio-Technica", Model: "AT-VM95C", StylusProfile: "Conical", MinHours: intPtr(300), MaxHours: intPtr(600), RecommendedHours: 500, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/LPGCFN3600LE.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Audio-Technica", Model: "AT-VM95E", StylusProfile: "Elliptical", MinHours: intPtr(300), MaxHours: intPtr(700), RecommendedHours: 600, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ATN95EX.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Audio-Technica", Model: "AT-VM95EN", StylusProfile: "Nude Elliptical", MinHours: intPtr(500), MaxHours: intPtr(1000), RecommendedHours: 800, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ORTS2MBLUE.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Audio-Technica", Model: "AT-VM95ML", StylusProfile: "MicroLine", MinHours: intPtr(800), MaxHours: intPtr(1200), RecommendedHours: 1000, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ATN95EX.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Audio-Technica", Model: "AT-VM95SH", StylusProfile: "Shibata", MinHours: intPtr(700), MaxHours: intPtr(1200), RecommendedHours: 1000, SourceName: "Profile baseline", SourceURL: "https://ortofon.com/products/2m-black", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Ortofon", Model: "2M Red", StylusProfile: "Elliptical", MinHours: intPtr(300), MaxHours: intPtr(700), RecommendedHours: 600, SourceName: "Profile baseline", SourceURL: "https://ortofon.com/products/2m-red", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Ortofon", Model: "2M Blue", StylusProfile: "Nude Elliptical", MinHours: intPtr(500), MaxHours: intPtr(1000), RecommendedHours: 800, SourceName: "Profile baseline", SourceURL: "https://ortofon.com/products/2m-blue", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Ortofon", Model: "2M Bronze", StylusProfile: "Fine Line", MinHours: intPtr(700), MaxHours: intPtr(1200), RecommendedHours: 1000, SourceName: "Profile baseline", SourceURL: "https://ortofon.com/products/2m-bronze", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Ortofon", Model: "2M Black", StylusProfile: "Shibata", MinHours: intPtr(700), MaxHours: intPtr(1200), RecommendedHours: 1000, SourceName: "Profile baseline", SourceURL: "https://ortofon.com/products/2m-black", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Nagaoka", Model: "MP-110", StylusProfile: "Elliptical", MinHours: intPtr(300), MaxHours: intPtr(700), RecommendedHours: 600, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ATN95EX.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Nagaoka", Model: "MP-150", StylusProfile: "Elliptical", MinHours: intPtr(300), MaxHours: intPtr(700), RecommendedHours: 600, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ATN95EX.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Sumiko", Model: "Oyster Rainier", StylusProfile: "Elliptical", MinHours: intPtr(300), MaxHours: intPtr(700), RecommendedHours: 600, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ATN95EX.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Sumiko", Model: "Oyster Moonstone", StylusProfile: "Elliptical", MinHours: intPtr(300), MaxHours: intPtr(700), RecommendedHours: 600, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ATN95EX.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Goldring", Model: "E3", StylusProfile: "Elliptical", MinHours: intPtr(300), MaxHours: intPtr(700), RecommendedHours: 600, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ATN95EX.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Rega", Model: "Nd3", StylusProfile: "Elliptical", MinHours: intPtr(300), MaxHours: intPtr(700), RecommendedHours: 600, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ATN95EX.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
	{Brand: "Grado", Model: "Prestige Blue3", StylusProfile: "Elliptical", MinHours: intPtr(300), MaxHours: intPtr(700), RecommendedHours: 600, SourceName: "Profile baseline", SourceURL: "https://www.lpgear.com/product/ATN95EX.html", SourceNote: "Conservative profile-based seed value.", Confidence: "low"},
}

func registerStylusRoutes(mux *http.ServeMux, dbPath string) {
	srv, err := openStylusServer(dbPath)
	if err != nil {
		log.Printf("stylus: disabled (%v)", err)
		return
	}

	mux.HandleFunc("/api/stylus", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			srv.handleGetStylus(w)
		case http.MethodPut:
			srv.handlePutStylus(w, r)
		default:
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/stylus/replace", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		srv.handleReplaceStylus(w, r)
	})

	mux.HandleFunc("/api/stylus/catalog", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		srv.handleGetCatalog(w)
	})
}

func openStylusServer(dbPath string) (*stylusServer, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("stylus: create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("stylus: open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("stylus: ping db: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("stylus: set WAL: %w", err)
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("stylus: set synchronous: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("stylus: set foreign_keys: %w", err)
	}
	s := &stylusServer{db: db}
	if err := s.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.seedCatalog(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *stylusServer) ensureSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS stylus_catalog (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			brand TEXT NOT NULL,
			model TEXT NOT NULL,
			stylus_profile TEXT NOT NULL,
			min_hours INTEGER,
			max_hours INTEGER,
			recommended_hours INTEGER NOT NULL,
			source_name TEXT NOT NULL,
			source_url TEXT NOT NULL,
			source_note TEXT NOT NULL,
			confidence TEXT NOT NULL DEFAULT 'medium',
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(brand, model)
		)`,
		`CREATE TABLE IF NOT EXISTS stylus_profiles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			enabled INTEGER NOT NULL DEFAULT 0,
			catalog_id INTEGER,
			brand TEXT NOT NULL,
			model TEXT NOT NULL,
			stylus_profile TEXT NOT NULL,
			lifetime_hours INTEGER NOT NULL,
			initial_used_hours REAL NOT NULL DEFAULT 0,
			installed_at TEXT NOT NULL,
			replaced_at TEXT,
			notes TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (catalog_id) REFERENCES stylus_catalog(id)
		)`,
		`CREATE INDEX IF NOT EXISTS stylus_profiles_active_idx ON stylus_profiles(replaced_at, installed_at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("stylus: ensure schema: %w", err)
		}
	}
	return nil
}

func (s *stylusServer) seedCatalog() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("stylus: seed begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO stylus_catalog (
			brand, model, stylus_profile, min_hours, max_hours, recommended_hours,
			source_name, source_url, source_note, confidence, is_active, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(brand, model) DO UPDATE SET
			stylus_profile=excluded.stylus_profile,
			min_hours=excluded.min_hours,
			max_hours=excluded.max_hours,
			recommended_hours=excluded.recommended_hours,
			source_name=excluded.source_name,
			source_url=excluded.source_url,
			source_note=excluded.source_note,
			confidence=excluded.confidence,
			is_active=1,
			updated_at=CURRENT_TIMESTAMP`)
	if err != nil {
		return fmt.Errorf("stylus: seed prepare: %w", err)
	}
	defer stmt.Close()

	for _, item := range stylusSeedCatalog {
		if _, err := stmt.Exec(
			item.Brand,
			item.Model,
			item.StylusProfile,
			item.MinHours,
			item.MaxHours,
			item.RecommendedHours,
			item.SourceName,
			item.SourceURL,
			item.SourceNote,
			item.Confidence,
		); err != nil {
			return fmt.Errorf("stylus: seed exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("stylus: seed commit: %w", err)
	}
	return nil
}

func (s *stylusServer) handleGetCatalog(w http.ResponseWriter) {
	items, err := s.listCatalog()
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	jsonOK(w, stylusCatalogResponse{Items: items})
}

func (s *stylusServer) handleGetStylus(w http.ResponseWriter) {
	resp, err := s.currentState()
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	jsonOK(w, resp)
}

func (s *stylusServer) handlePutStylus(w http.ResponseWriter, r *http.Request) {
	var req stylusPutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.upsertStylus(req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.currentState()
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	jsonOK(w, resp)
}

func (s *stylusServer) handleReplaceStylus(w http.ResponseWriter, r *http.Request) {
	var req stylusReplaceRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if err := s.replaceStylus(req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.currentState()
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	jsonOK(w, resp)
}

func (s *stylusServer) listCatalog() ([]stylusCatalogItem, error) {
	rows, err := s.db.Query(`
		SELECT id, brand, model, stylus_profile,
			min_hours, max_hours, recommended_hours,
			source_name, source_url, source_note, confidence
		FROM stylus_catalog
		WHERE is_active = 1
		ORDER BY brand, model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]stylusCatalogItem, 0)
	for rows.Next() {
		var it stylusCatalogItem
		var minHours, maxHours sql.NullInt64
		if err := rows.Scan(
			&it.ID,
			&it.Brand,
			&it.Model,
			&it.StylusProfile,
			&minHours,
			&maxHours,
			&it.RecommendedHours,
			&it.SourceName,
			&it.SourceURL,
			&it.SourceNote,
			&it.Confidence,
		); err != nil {
			return nil, err
		}
		if minHours.Valid {
			v := int(minHours.Int64)
			it.MinHours = &v
		}
		if maxHours.Valid {
			v := int(maxHours.Int64)
			it.MaxHours = &v
		}
		items = append(items, it)
	}
	if items == nil {
		items = []stylusCatalogItem{}
	}
	return items, rows.Err()
}

func (s *stylusServer) currentState() (*stylusGetResponse, error) {
	active, err := s.activeStylusProfile()
	if err != nil {
		return nil, err
	}
	resp := &stylusGetResponse{
		Enabled: false,
		Metrics: stylusMetrics{State: "healthy"},
	}
	if active == nil {
		return resp, nil
	}

	resp.Enabled = active.Enabled
	resp.Stylus = &stylusProfile{
		ID:               active.ID,
		CatalogID:        active.CatalogID,
		Brand:            active.Brand,
		Model:            active.Model,
		StylusProfile:    active.StylusProfile,
		LifetimeHours:    active.LifetimeHours,
		InitialUsedHours: active.InitialUsedHours,
		InstalledAt:      active.InstalledAt,
		IsCustom:         active.CatalogID == nil,
	}

	vinylHours, err := s.vinylHoursSince(active.InstalledAt)
	if err != nil {
		return nil, err
	}
	total := active.InitialUsedHours + vinylHours
	remaining := math.Max(float64(active.LifetimeHours)-total, 0)
	wear := 0.0
	if active.LifetimeHours > 0 {
		wear = (total / float64(active.LifetimeHours)) * 100
		if wear > 999 {
			wear = 999
		}
	}
	state := classifyStylusState(wear)
	resp.Metrics = stylusMetrics{
		VinylHoursSinceInstall: round2(vinylHours),
		StylusHoursTotal:       round2(total),
		RemainingHours:         round2(remaining),
		WearPercent:            round2(wear),
		State:                  state,
	}
	return resp, nil
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func classifyStylusState(wearPercent float64) string {
	switch {
	case wearPercent > 100:
		return "overdue"
	case wearPercent >= 90:
		return "soon"
	case wearPercent >= 70:
		return "plan"
	default:
		return "healthy"
	}
}

type stylusProfileRow struct {
	ID               int64
	Enabled          bool
	CatalogID        *int64
	Brand            string
	Model            string
	StylusProfile    string
	LifetimeHours    int
	InitialUsedHours float64
	InstalledAt      string
	ReplacedAt       string
}

func (s *stylusServer) activeStylusProfile() (*stylusProfileRow, error) {
	row := s.db.QueryRow(`
		SELECT id, enabled, catalog_id, brand, model, stylus_profile,
			lifetime_hours, initial_used_hours, installed_at, COALESCE(replaced_at,'')
		FROM stylus_profiles
		WHERE replaced_at IS NULL
		ORDER BY id DESC
		LIMIT 1`)

	var out stylusProfileRow
	var enabledInt int
	var catalogID sql.NullInt64
	if err := row.Scan(
		&out.ID,
		&enabledInt,
		&catalogID,
		&out.Brand,
		&out.Model,
		&out.StylusProfile,
		&out.LifetimeHours,
		&out.InitialUsedHours,
		&out.InstalledAt,
		&out.ReplacedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	out.Enabled = enabledInt == 1
	if catalogID.Valid {
		v := catalogID.Int64
		out.CatalogID = &v
	}
	return &out, nil
}

func (s *stylusServer) vinylHoursSince(installedAt string) (float64, error) {
	var hours float64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(listened_seconds), 0) / 3600.0
		FROM play_history
		WHERE LOWER(COALESCE(media_format, '')) = 'vinyl'
		  AND started_at >= ?`, installedAt).Scan(&hours)
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "no such table") && strings.Contains(errText, "play_history") {
			return 0, nil
		}
		return 0, err
	}
	return hours, nil
}

func (s *stylusServer) upsertStylus(req stylusPutRequest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("db error")
	}
	defer tx.Rollback()

	active, err := queryActiveStylusTx(tx)
	if err != nil {
		return fmt.Errorf("db error")
	}

	if !req.Enabled {
		if active != nil {
			if _, err := tx.Exec(`
				UPDATE stylus_profiles
				SET enabled=0, updated_at=CURRENT_TIMESTAMP
				WHERE id=?`, active.ID); err != nil {
				return fmt.Errorf("db error")
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("db error")
		}
		return nil
	}

	resolved, err := resolveStylusDefinitionTx(tx, req.CatalogID, req.Brand, req.Model, req.StylusProfile, req.LifetimeHours)
	if err != nil {
		return err
	}
	initialUsed := normalizeInitialUsed(req.IsNew, req.InitialUsedHours)
	if initialUsed < 0 {
		return fmt.Errorf("initial_used_hours must be >= 0")
	}

	if active == nil {
		if _, err := tx.Exec(`
			INSERT INTO stylus_profiles (
				enabled, catalog_id, brand, model, stylus_profile,
				lifetime_hours, initial_used_hours, installed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, 1, resolved.CatalogID, resolved.Brand, resolved.Model, resolved.StylusProfile,
			resolved.LifetimeHours, initialUsed, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("db error")
		}
	} else {
		if _, err := tx.Exec(`
			UPDATE stylus_profiles
			SET enabled=?, catalog_id=?, brand=?, model=?, stylus_profile=?,
				lifetime_hours=?, initial_used_hours=?, updated_at=CURRENT_TIMESTAMP
			WHERE id=?`,
			1, resolved.CatalogID, resolved.Brand, resolved.Model, resolved.StylusProfile,
			resolved.LifetimeHours, initialUsed, active.ID,
		); err != nil {
			return fmt.Errorf("db error")
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db error")
	}
	return nil
}

func (s *stylusServer) replaceStylus(req stylusReplaceRequest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("db error")
	}
	defer tx.Rollback()

	active, err := queryActiveStylusTx(tx)
	if err != nil {
		return fmt.Errorf("db error")
	}
	if active == nil {
		return fmt.Errorf("no active stylus profile")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`
		UPDATE stylus_profiles
		SET replaced_at=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, now, active.ID); err != nil {
		return fmt.Errorf("db error")
	}

	catalogID := req.CatalogID
	brand := req.Brand
	model := req.Model
	profile := req.StylusProfile
	lifetime := req.LifetimeHours
	if catalogID == nil && strings.TrimSpace(brand) == "" && strings.TrimSpace(model) == "" && strings.TrimSpace(profile) == "" && lifetime <= 0 {
		catalogID = active.CatalogID
		brand = active.Brand
		model = active.Model
		profile = active.StylusProfile
		lifetime = active.LifetimeHours
	}

	resolved, err := resolveStylusDefinitionTx(tx, catalogID, brand, model, profile, lifetime)
	if err != nil {
		return err
	}
	initialUsed := normalizeInitialUsed(req.IsNew, req.InitialUsedHours)
	if initialUsed < 0 {
		return fmt.Errorf("initial_used_hours must be >= 0")
	}

	enabledInt := 0
	if active.Enabled {
		enabledInt = 1
	}
	if _, err := tx.Exec(`
		INSERT INTO stylus_profiles (
			enabled, catalog_id, brand, model, stylus_profile,
			lifetime_hours, initial_used_hours, installed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		enabledInt, resolved.CatalogID, resolved.Brand, resolved.Model, resolved.StylusProfile,
		resolved.LifetimeHours, initialUsed, now,
	); err != nil {
		return fmt.Errorf("db error")
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db error")
	}
	return nil
}

func normalizeInitialUsed(isNew *bool, initialUsed *float64) float64 {
	if isNew != nil && *isNew {
		return 0
	}
	if initialUsed != nil {
		return *initialUsed
	}
	// If neither a value nor explicit "new" flag is provided, assume new.
	return 0
}

type resolvedStylusDef struct {
	CatalogID     *int64
	Brand         string
	Model         string
	StylusProfile string
	LifetimeHours int
}

func resolveStylusDefinitionTx(tx *sql.Tx, catalogID *int64, brand, model, stylusProfile string, lifetimeHours int) (*resolvedStylusDef, error) {
	if catalogID != nil && *catalogID > 0 {
		row := tx.QueryRow(`
			SELECT id, brand, model, stylus_profile, recommended_hours
			FROM stylus_catalog
			WHERE id=? AND is_active=1`, *catalogID)
		var out resolvedStylusDef
		var id int64
		if err := row.Scan(&id, &out.Brand, &out.Model, &out.StylusProfile, &out.LifetimeHours); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("catalog_id not found")
			}
			return nil, fmt.Errorf("db error")
		}
		out.CatalogID = &id
		return &out, nil
	}

	brand = strings.TrimSpace(brand)
	model = strings.TrimSpace(model)
	stylusProfile = strings.TrimSpace(stylusProfile)
	if brand == "" || model == "" || stylusProfile == "" {
		return nil, fmt.Errorf("brand, model and stylus_profile are required for custom stylus")
	}
	if lifetimeHours <= 0 {
		return nil, fmt.Errorf("lifetime_hours must be > 0")
	}
	return &resolvedStylusDef{
		CatalogID:     nil,
		Brand:         brand,
		Model:         model,
		StylusProfile: stylusProfile,
		LifetimeHours: lifetimeHours,
	}, nil
}

func queryActiveStylusTx(tx *sql.Tx) (*stylusProfileRow, error) {
	row := tx.QueryRow(`
		SELECT id, enabled, catalog_id, brand, model, stylus_profile,
			lifetime_hours, initial_used_hours, installed_at, COALESCE(replaced_at,'')
		FROM stylus_profiles
		WHERE replaced_at IS NULL
		ORDER BY id DESC
		LIMIT 1`)
	var out stylusProfileRow
	var enabledInt int
	var catalogID sql.NullInt64
	if err := row.Scan(
		&out.ID,
		&enabledInt,
		&catalogID,
		&out.Brand,
		&out.Model,
		&out.StylusProfile,
		&out.LifetimeHours,
		&out.InitialUsedHours,
		&out.InstalledAt,
		&out.ReplacedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	out.Enabled = enabledInt == 1
	if catalogID.Valid {
		v := catalogID.Int64
		out.CatalogID = &v
	}
	return &out, nil
}
