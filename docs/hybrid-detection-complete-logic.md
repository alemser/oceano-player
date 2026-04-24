# Hybrid Physical Source Detection - Complete Logic

## Overview

This document contains all the logic for the hybrid adaptive source detection system that distinguishes physical audio sources (vinyl/CD) from system noise (CD transport noise, vinyl inter-track groove noise).

---

## Core Constants (noise_floor.go)

```go
const (
    adaptRate         = 0.005  // EMA rate: ~200 windows (9s) to adapt significantly
    rmsMultiplier   = 4.0    // RMS threshold = noiseFloor + 4 * stddev
    stddevMultiplier = 3.0    // StdDev threshold = 3 * noiseFloor
    saveInterval   = 5 * time.Minute
    rollingWindow = 10      // 10 frames (~0.5s) for rolling stddev
)

const maxSafeStdDevThreshold = 0.004  // Reject files above this

func defaultNoiseFloor() NoiseFloor {
    return NoiseFloor{
        RMS:    0.001,
        StdDev: 0.001,  // Key: low default → low threshold 0.003
    }
}
```

---

## Core Types

```go
type NoiseFloor struct {
    RMS       float64   `json:"rms"`
    StdDev    float64   `json:"stddev"`
    UpdatedAt time.Time `json:"updated_at"`
    Windows   int64     `json:"windows"`
}

type HybridThresholds struct {
    RMS    float64
    StdDev float64
}
```

---

## Threshold Calculation

```go
func (nf NoiseFloor) Thresholds() HybridThresholds {
    return HybridThresholds{
        RMS:    nf.RMS + nf.StdDev * rmsMultiplier,    // 0.001 + 0.001*4 = 0.005
        StdDev: nf.StdDev * stddevMultiplier,       // 0.001 * 3 = 0.003
    }
}
```

---

## File Persistence (Auto-Reject Contaminated)

```go
func loadNoiseFloor(path string) (NoiseFloor, bool) {
    data, err := os.ReadFile(path)
    if err != nil {
        return defaultNoiseFloor(), false
    }
    var nf NoiseFloor
    if err := json.Unmarshal(data, &nf); err != nil || nf.RMS <= 0 || nf.StdDev <= 0 {
        return defaultNoiseFloor(), false
    }
    // KEY: Reject contaminated files
    if nf.Thresholds().StdDev > maxSafeStdDevThreshold {
        log.Printf("noise floor: stored stddev threshold %.4f > %.4f — resetting",
            nf.Thresholds().StdDev, maxSafeStdDevThreshold)
        return defaultNoiseFloor(), false
    }
    return nf, true
}
```

---

## Rolling Statistics (for StdDev)

```go
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
```

---

## Noise Floor Learner

```go
type noiseFloorLearner struct {
    path    string
    current NoiseFloor
    lastSave time.Time
}

func newNoiseFloorLearner(path string) *noiseFloorLearner {
    nf, ok := loadNoiseFloor(path)
    if !ok {
        log.Printf("noise floor: using defaults")
    }
    return &noiseFloorLearner{path: path, current: nf, lastSave: time.Now()}
}

func (l *noiseFloorLearner) update(windowRMS, rollingStdDev float64) {
    l.current.RMS    = adaptRate * windowRMS    + (1-adaptRate) * l.current.RMS
    l.current.StdDev = adaptRate * rollingStdDev + (1-adaptRate) * l.current.StdDev
    l.current.Windows++
    l.current.UpdatedAt = time.Now().UTC()
    l.maybeSave()
}

func (l *noiseFloorLearner) maybeSave() {
    if time.Since(l.lastSave) < saveInterval {
        return
    }
    saveNoiseFloor(l.path, l.current)
    l.lastSave = time.Now()
}
```

---

## Detection Logic with Hysteresis (runtime.go)

```go
// Constants
const rmsHighBypassFactor = 5.0  // Bypass stddev check if RMS ≥ 5× threshold

// Detection loop
detected := SourceNone

if current == SourcePhysical {
    // EXIT: Only RMS matters - original behavior
    // Sustained quiet passages stay Physical
    if windowRMS >= thresh.RMS {
        detected = SourcePhysical
    }
} else {
    // ENTRY: Requires BOTH + high-RMS bypass
    // Filters CD transport noise (high RMS, low variation)
    // Allows music with high RMS bypass
    if windowRMS >= thresh.RMS && (rollingStdDev >= thresh.StdDev || windowRMS >= thresh.RMS*rmsHighBypassFactor) {
        detected = SourcePhysical
    }
}

// KEY: Learn only from REAL silence (not CD noise classified as None)
if windowRMS < thresh.RMS {
    learner.update(windowRMS, rollingStdDev)
}
```

---

## Decision Table

| Signal Type | RMS | StdDev | 5× Bypass | State Transition | Result |
|------------|-----|--------|------------|--------------|--------|
| CD transport noise | 0.02-0.05 | 0.001 | no | None→None | None ✓ |
| Vinyl inter-track | 0.005-0.01 | 0.002 | no | None→None | None ✓ |
| A-cappella (quiet) | 0.01 | 0.002 | no | None→None | None |
| Music normal | 0.1 | 0.008 | no | **None→Physical** | Physical ✓ |
| Music loud | 0.3 | 0.01 | **yes** | **None→Physical** | Physical ✓ |
| Quiet passage (in track) | 0.02 | 0.002 | no | Physical→Physical | Physical ✓ |
| Between tracks | 0.001 | 0.0001 | no | Physical→None | None ✓ |

---

## Why This Works

1. **Entry (None→Physical):** AND gate filters constant hum (CD noise) but allows high-volume music bypass
2. **Exit (Physical→None):** Only RMS matters - original behavior for in-track dynamics
3. **Low default (0.001):** Threshold 0.003 catches steady music
4. **Auto-reject:** Files contaminated with old high values are reset
5. **Correct learner:** Only learns from real silence (RMS < threshold)