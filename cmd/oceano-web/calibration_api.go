package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"time"
)

type vuSampleRequest struct {
	Seconds int `json:"seconds"`
}

type vuSampleResponse struct {
	Seconds int     `json:"seconds"`
	Samples int     `json:"samples"`
	AvgRMS  float64 `json:"avg_rms"`
	MinRMS  float64 `json:"min_rms"`
	MaxRMS  float64 `json:"max_rms"`
}

type vuSequenceResponse struct {
	Seconds      int       `json:"seconds"`
	Samples      int       `json:"samples"`
	AvgRMS       float64   `json:"avg_rms"`
	MinRMS       float64   `json:"min_rms"`
	MaxRMS       float64   `json:"max_rms"`
	SamplesPerSec float64  `json:"samples_per_sec"`
	RMS          []float64 `json:"rms"`
}

func registerCalibrationRoutes(mux *http.ServeMux, vuSocket string) {
	mux.HandleFunc("/api/calibration/vu-sample", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if vuSocket == "" {
			jsonError(w, "VU socket not configured", http.StatusBadRequest)
			return
		}
		var req vuSampleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		req.Seconds = clampSeconds(req.Seconds, 6, 2, 20)

		avg, minVal, maxVal, samples, err := sampleVU(vuSocket, time.Duration(req.Seconds)*time.Second)
		if err != nil {
			jsonError(w, "failed to sample VU socket", http.StatusBadGateway)
			return
		}
		jsonOK(w, vuSampleResponse{
			Seconds: req.Seconds,
			Samples: samples,
			AvgRMS:  avg,
			MinRMS:  minVal,
			MaxRMS:  maxVal,
		})
	})

	mux.HandleFunc("/api/calibration/vu-sequence", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if vuSocket == "" {
			jsonError(w, "VU socket not configured", http.StatusBadRequest)
			return
		}
		var req vuSampleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		req.Seconds = clampSeconds(req.Seconds, 18, 6, 30)

		rms, avg, minVal, maxVal, samples, err := sampleVUSequence(vuSocket, time.Duration(req.Seconds)*time.Second)
		if err != nil {
			jsonError(w, "failed to sample VU socket", http.StatusBadGateway)
			return
		}

		sps := 0.0
		if req.Seconds > 0 {
			sps = float64(samples) / float64(req.Seconds)
		}

		jsonOK(w, vuSequenceResponse{
			Seconds:       req.Seconds,
			Samples:       samples,
			AvgRMS:        avg,
			MinRMS:        minVal,
			MaxRMS:        maxVal,
			SamplesPerSec: sps,
			RMS:           rms,
		})
	})
}

func clampSeconds(v int, fallback int, min int, max int) int {
	if v <= 0 {
		v = fallback
	}
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	return v
}

func sampleVU(socketPath string, duration time.Duration) (float64, float64, float64, int, error) {
	rms, avg, minVal, maxVal, count, err := sampleVUSequence(socketPath, duration)
	_ = rms
	return avg, minVal, maxVal, count, err
}

func sampleVUSequence(socketPath string, duration time.Duration) ([]float64, float64, float64, float64, int, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, 0, 0, 0, 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(duration + 2*time.Second))

	buf := make([]byte, 8)
	deadline := time.Now().Add(duration)
	count := 0
	sum := 0.0
	minVal := math.Inf(1)
	maxVal := math.Inf(-1)
	rms := make([]float64, 0, 1024)

	for time.Now().Before(deadline) {
		if _, err := io.ReadFull(conn, buf); err != nil {
			if count > 0 {
				break
			}
			return nil, 0, 0, 0, 0, err
		}
		left := math.Float32frombits(binary.LittleEndian.Uint32(buf[0:4]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(buf[4:8]))
		avg := float64((left + right) / 2)
		if avg < 0 {
			avg = 0
		}
		rms = append(rms, avg)
		sum += avg
		if avg < minVal {
			minVal = avg
		}
		if avg > maxVal {
			maxVal = avg
		}
		count++
	}

	if count == 0 {
		return nil, 0, 0, 0, 0, io.EOF
	}
	return rms, sum / float64(count), minVal, maxVal, count, nil
}
