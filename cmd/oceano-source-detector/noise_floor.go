package main

import (
	"encoding/json"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"
)

// NoiseFloor holds the learned baseline for the capture path during silence.
// All values are averages of per-window RMS measurements.
type NoiseFloor struct {
	RMS       float64   `json:"rms"`
	StdDev    float64   `json:"stddev"`
	UpdatedAt time.Time `json:"updated_at"`
	Windows   int64     `json:"windows"`
}

// HybridThresholds is derived from a NoiseFloor and drives the Physical/None
// decision in the detection loop.
type HybridThresholds struct {
	RMS    float64
	StdDev float64
}

const (
	// adaptRate controls how quickly the EMA tracks silence windows.
	// 0.005 ≈ 200 windows (≈ 9 s at 21.5 Hz) to adapt significantly.
	adaptRate = 0.005

	// rmsMultiplier: threshold sits this many noise-floor stddevs above the mean.
	rmsMultiplier = 4.0

	// stddevMultiplier: variation threshold is this multiple of noise-floor variation.
	stddevMultiplier = 3.0

	// saveInterval is how often the learned state is flushed to disk.
	saveInterval = 5 * time.Minute

	// rollingWindow is the number of recent window-RMS values used to compute
	// the rolling StdDev (variation) that drives the hybrid AND condition.
	rollingWindow = 10
)

// defaultNoiseFloor returns conservative starting values that ensure silence
// is still classified as None while the learner has not converged yet.
// rmsThreshold will be 0.001 + 0.005*4 = 0.021 — well above any real noise
// floor but below typical music levels.
func defaultNoiseFloor() NoiseFloor {
	return NoiseFloor{
		RMS:    0.001,
		StdDev: 0.005,
	}
}

// Thresholds derives the HybridThresholds from the current noise floor estimate.
func (nf NoiseFloor) Thresholds() HybridThresholds {
	return HybridThresholds{
		RMS:    nf.RMS + nf.StdDev*rmsMultiplier,
		StdDev: nf.StdDev * stddevMultiplier,
	}
}

// loadNoiseFloor reads the persisted noise floor from path.
// Returns (defaultNoiseFloor(), false) when the file is absent or invalid.
func loadNoiseFloor(path string) (NoiseFloor, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultNoiseFloor(), false
	}
	var nf NoiseFloor
	if err := json.Unmarshal(data, &nf); err != nil || nf.RMS <= 0 || nf.StdDev <= 0 {
		log.Printf("noise floor: ignoring corrupt calibration file %s — re-learning", path)
		return defaultNoiseFloor(), false
	}
	return nf, true
}

// saveNoiseFloor writes the noise floor atomically (write-then-rename).
func saveNoiseFloor(path string, nf NoiseFloor) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(nf, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// noiseFloorLearner updates the noise floor estimate using an EMA and persists
// it periodically. It is only called from the detection loop and is not
// goroutine-safe — it must be driven by a single goroutine.
type noiseFloorLearner struct {
	path    string
	current NoiseFloor
	lastSave time.Time
}

func newNoiseFloorLearner(path string) *noiseFloorLearner {
	nf, ok := loadNoiseFloor(path)
	if ok {
		log.Printf("noise floor: loaded from %s (rms=%.5f stddev=%.5f windows=%d updated=%s)",
			path, nf.RMS, nf.StdDev, nf.Windows, nf.UpdatedAt.Format(time.RFC3339))
	} else {
		log.Printf("noise floor: starting from defaults (rms=%.5f stddev=%.5f) — will learn from silence",
			nf.RMS, nf.StdDev)
	}
	return &noiseFloorLearner{path: path, current: nf, lastSave: time.Now()}
}

// update refines the estimate using one silence window.
// windowRMS is the RMS of the current audio window.
// rollingStdDev is the StdDev of the last rollingWindow window-RMS values.
func (l *noiseFloorLearner) update(windowRMS, rollingStdDev float64) {
	l.current.RMS    = adaptRate*windowRMS    + (1-adaptRate)*l.current.RMS
	l.current.StdDev = adaptRate*rollingStdDev + (1-adaptRate)*l.current.StdDev
	l.current.Windows++
	l.current.UpdatedAt = time.Now().UTC()
	l.maybeSave()
}

func (l *noiseFloorLearner) maybeSave() {
	if time.Since(l.lastSave) < saveInterval {
		return
	}
	if err := saveNoiseFloor(l.path, l.current); err != nil {
		log.Printf("noise floor: save failed: %v", err)
	} else {
		t := l.current.Thresholds()
		log.Printf("noise floor: saved (rms=%.5f stddev=%.5f → rmsThresh=%.5f stddevThresh=%.5f windows=%d)",
			l.current.RMS, l.current.StdDev, t.RMS, t.StdDev, l.current.Windows)
	}
	l.lastSave = time.Now()
}

// flush writes the current estimate unconditionally (called on clean shutdown).
func (l *noiseFloorLearner) flush() {
	if err := saveNoiseFloor(l.path, l.current); err != nil {
		log.Printf("noise floor: flush failed: %v", err)
	}
}

// rollingStats maintains a fixed-size circular buffer of recent window-RMS
// values and computes their mean and standard deviation efficiently.
type rollingStats struct {
	buf  []float64
	pos  int
	full bool
}

func newRollingStats(n int) *rollingStats {
	return &rollingStats{buf: make([]float64, n)}
}

func (rs *rollingStats) push(v float64) {
	rs.buf[rs.pos] = v
	rs.pos = (rs.pos + 1) % len(rs.buf)
	if rs.pos == 0 {
		rs.full = true
	}
}

func (rs *rollingStats) stddev() float64 {
	n := len(rs.buf)
	if !rs.full {
		n = rs.pos
	}
	if n < 2 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += rs.buf[i]
	}
	mean := sum / float64(n)
	var variance float64
	for i := 0; i < n; i++ {
		d := rs.buf[i] - mean
		variance += d * d
	}
	return math.Sqrt(variance / float64(n))
}
