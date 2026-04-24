package main

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
)

const defaultNoiseFloorPath = "/var/lib/oceano/noise-floor.json"

// noiseFloorFile mirrors the JSON written by the source-detector.
type noiseFloorFile struct {
	RMS       float64   `json:"rms"`
	StdDev    float64   `json:"stddev"`
	UpdatedAt time.Time `json:"updated_at"`
	Windows   int64     `json:"windows"`
}

type noiseFloorResponse struct {
	RMS            float64 `json:"rms"`
	StdDev         float64 `json:"stddev"`
	RMSThreshold   float64 `json:"rms_threshold"`
	StdDevThreshold float64 `json:"stddev_threshold"`
	UpdatedAt      string  `json:"updated_at,omitempty"`
	Windows        int64   `json:"windows"`
	Learned        bool    `json:"learned"`
}

func registerNoiseFloorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/noise-floor", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		data, err := os.ReadFile(defaultNoiseFloorPath)
		if err != nil {
			// File absent: detector has not yet learned a noise floor.
			jsonOK(w, noiseFloorResponse{Learned: false})
			return
		}

		var nf noiseFloorFile
		if err := json.Unmarshal(data, &nf); err != nil || nf.RMS <= 0 {
			jsonOK(w, noiseFloorResponse{Learned: false})
			return
		}

		const rmsMultiplier    = 4.0
		const stddevMultiplier = 3.0

		resp := noiseFloorResponse{
			RMS:             nf.RMS,
			StdDev:          nf.StdDev,
			RMSThreshold:    nf.RMS + nf.StdDev*rmsMultiplier,
			StdDevThreshold: nf.StdDev * stddevMultiplier,
			Windows:         nf.Windows,
			Learned:         true,
		}
		if !nf.UpdatedAt.IsZero() {
			resp.UpdatedAt = nf.UpdatedAt.UTC().Format(time.RFC3339)
		}
		jsonOK(w, resp)
	})
}
