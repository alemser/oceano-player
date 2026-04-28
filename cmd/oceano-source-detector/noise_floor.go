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

	// minSafeRMSThreshold is the lowest adaptive RMS threshold we accept from
	// persisted calibration. Lower values are typically produced when the
	// silence learner was trained in an unrealistically quiet window and can
	// cause persistent false Physical detection later (e.g. phono hum around
	// 0.004-0.006 being treated as active media).
	minSafeRMSThreshold = 0.006
)

// defaultNoiseFloor returns starting values used before the learner has
// converged. Values are chosen conservatively so the initial thresholds sit
// above real-world capture-card noise without requiring a calibration file:
//
//   rmsThreshold = RMS + StdDev*4 = 0.004 + 0.001*4 = 0.008
//     - above phono-preamp idle hum   (~0.005)         → not Physical ✓
//     - above CD-transport idle noise  (~0.003–0.006)   → not Physical ✓
//     - below vinyl groove noise avg   (~0.012)         → Physical ✓
//     - below any real music           (0.05+)          → Physical ✓
//
//   stddevThreshold = StdDev*3 = 0.003
//     - above CD-transport variation   (~0.001–0.002)   → entry blocked ✓
//     - below groove noise variation   (~0.005–0.010)   → entry passes ✓
//     - below music variation          (~0.010–0.030)   → entry passes ✓
//
// The previous RMS default of 0.001 gave rmsThreshold 0.005, which was
// marginally below a typical phono-preamp idle hum (0.005–0.006) and caused
// the source to be classified as Physical immediately on startup.
func defaultNoiseFloor() NoiseFloor {
	return NoiseFloor{
		RMS:    0.004,
		StdDev: 0.001,
	}
}

// Thresholds derives the HybridThresholds from the current noise floor estimate.
func (nf NoiseFloor) Thresholds() HybridThresholds {
	return HybridThresholds{
		RMS:    nf.RMS + nf.StdDev*rmsMultiplier,
		StdDev: nf.StdDev * stddevMultiplier,
	}
}

// maxSafeStdDevThreshold is the highest stddev threshold that still allows
// steady-music passages to be detected as Physical. Any stored noise floor
// whose derived stddev threshold exceeds this was likely contaminated by
// transport noise or music being incorrectly learned as silence.
const maxSafeStdDevThreshold = 0.004

// loadNoiseFloor reads the persisted noise floor from path.
// Returns (defaultNoiseFloor(), false) when the file is absent, invalid, or
// contains thresholds so high that they would suppress music detection.
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
	if nf.Thresholds().StdDev > maxSafeStdDevThreshold {
		log.Printf("noise floor: stored stddev threshold %.4f > %.4f (file contaminated by transport noise or previous defaults) — resetting", nf.Thresholds().StdDev, maxSafeStdDevThreshold)
		return defaultNoiseFloor(), false
	}
	if nf.Thresholds().RMS < minSafeRMSThreshold {
		log.Printf("noise floor: stored rms threshold %.4f < %.4f (likely overfit to unrealistically quiet baseline) — resetting", nf.Thresholds().RMS, minSafeRMSThreshold)
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
	path     string
	current  NoiseFloor
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
	l.current.RMS = adaptRate*windowRMS + (1-adaptRate)*l.current.RMS
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
