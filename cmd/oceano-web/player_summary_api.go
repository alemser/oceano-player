package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
)

// PlayerSummaryResponse is a compact view for cheap polling (GET /api/player/summary).
type PlayerSummaryResponse struct {
	Source                 string                    `json:"source"`
	State                  string                    `json:"state"`
	Format                 string                    `json:"format,omitempty"`
	PhysicalDetectorActive bool                      `json:"physical_detector_active"`
	Track                  *PlayerSummaryTrack       `json:"track"`
	Recognition            *PlayerSummaryRecognition `json:"recognition,omitempty"`
	LibraryVersion         int64                     `json:"library_version"`
	UpdatedAt              string                    `json:"updated_at"`
}

// PlayerSummaryTrack is a reduced track payload for summary polling.
type PlayerSummaryTrack struct {
	Title         string `json:"title,omitempty"`
	Artist        string `json:"artist,omitempty"`
	Album         string `json:"album,omitempty"`
	TrackNumber   string `json:"track_number,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
	SeekMS        int64  `json:"seek_ms"`
	SeekUpdatedAt string `json:"seek_updated_at,omitempty"`
}

// PlayerSummaryRecognition mirrors recognition.status fields needed by UIs.
type PlayerSummaryRecognition struct {
	Phase    string `json:"phase"`
	Detail   string `json:"detail,omitempty"`
	Provider string `json:"provider,omitempty"`
	Score    int    `json:"score,omitempty"`
}

type playerStateWire struct {
	Source                 string                    `json:"source"`
	Format                 string                    `json:"format,omitempty"`
	State                  string                    `json:"state"`
	Track                  *PlayerSummaryTrack       `json:"track"`
	Recognition            *PlayerSummaryRecognition `json:"recognition,omitempty"`
	PhysicalDetectorActive bool                      `json:"physical_detector_active"`
	UpdatedAt              string                    `json:"updated_at"`
}

func handlePlayerSummary(configPath, libraryDBPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg, err := loadConfig(configPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raw, err := os.ReadFile(cfg.Advanced.StateFile)
		if err != nil {
			http.Error(w, `{"error":"state file not found"}`, http.StatusServiceUnavailable)
			return
		}
		var wire playerStateWire
		if err := json.Unmarshal(raw, &wire); err != nil {
			http.Error(w, "invalid state json", http.StatusInternalServerError)
			return
		}
		out := PlayerSummaryResponse{
			Source:                 wire.Source,
			State:                  wire.State,
			Format:                 wire.Format,
			PhysicalDetectorActive: wire.PhysicalDetectorActive,
			Track:                  wire.Track,
			Recognition:            wire.Recognition,
			UpdatedAt:              wire.UpdatedAt,
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib != nil {
			v, err := lib.getLibraryVersion()
			if err == nil {
				out.LibraryVersion = v
			}
			lib.close()
		}
		body, err := json.Marshal(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		etag := weakJSONETag(body)
		w.Header().Set("ETag", etag)
		w.Header().Set("X-Oceano-Library-Version", strconv.FormatInt(out.LibraryVersion, 10))
		w.Header().Set("Cache-Control", "private, no-cache")
		if configETagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}
