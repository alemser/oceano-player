# Hybrid Source Detection with Persistent Calibration

## Problem

The current `source-detector` uses a **fixed RMS threshold** (default `0.008`) to decide between `Physical` and `None`. This works poorly because:

1. **Vinyl**: noise floor is always above zero, but silence between tracks has low **variation**
2. **CD**: digital silence has RMS near zero AND near-zero **variation**
3. **Passages** (e.g., "Telegraph Road", "Shine On You Crazy Diamond"): near-silence that should not trigger recognition

## Solution

### 1. Hybrid Detection (RMS + Variation)

```go
isPhysical := rms >= rmsThreshold || stddev >= stddevThreshold
```

| Source | RMS | StdDev | Result |
|--------|-----|--------|--------|
| Vinyl silence | > 0 (noise floor) | **low** | Not Physical → correct |
| CD silence | ~0 | **very low** | Not Physical → correct |
| Vinyl music | > 0 | **high** | Physical → correct |
| CD music | > 0 | **high** | Physical → correct |
| Telegraph Road (quiet passage) | low | **low** | Not Physical → correct |

### 2. Calibration Strategy: Measure Once, Reuse

To preserve detection speed:

1. **First boot**: measure noise floor for ~10s, save to persistent file
2. **Subsequent boots**: load from file immediately (no delay)
3. **Manual recalibration**: via API or flag when needed

```
Boot 1:  [measure 10s] → save /var/lib/oceano/noise-floor.json → detect
Boot 2+: load /var/lib/oceano/noise-floor.json → detect immediately
```

```
noise-floor.json
{
  "measured_at": "2025-04-23T10:00:00Z",
  "rms": 0.0055,
  "stddev": 0.0012,
  "samples": 53
}
```

### 3. Threshold Calculation

```go
rmsThreshold    = noiseFloor.RMS + noiseFloor.StdDev * 3.0
stddevThreshold = noiseFloor.StdDev * 2.5
```

### 4. Manual Override

Keep existing flags as fallbacks:
- `--silence-threshold` (RMS only)
- `--stddev-threshold` (new)
- `--calibration-file` (path to noise-floor.json)

## Architecture

```
arecord
    │
    ▼
source-detector
    │
    ├── Load /var/lib/oceano/noise-floor.json (or measure if missing)
    ├── RMS + stddev → Physical/None
    └── VU frames → state-manager (unchanged)
```

### Changes Required

1. **source-detector**:
   - Add `--calibration-file` flag (default: `/var/lib/oceano/noise-floor.json`)
   - Add `--stddev-threshold` flag (default: computed from calibration)
   - Add `--calibrate` flag to force re-measurement
   - Calculate stddev per buffer window (2048 samples)
   - Replace `rms >= threshold` with hybrid logic

2. **calibration measurement**:
   - Run in background during first boot
   - Collect RMS values over ~10 seconds
   - Calculate median RMS and stddev
   - Write to JSON file

3. **state-manager** (unchanged):
   - Keeps using `calibration_profile.go` for boundary detection

## Detection Speed

| Scenario | Delay |
|----------|-------|
| First boot (no calibration file) | ~10s for measurement + majority vote |
| Subsequent boots | 0s (load from file) |
| With `--calibrate` flag | ~10s for measurement |

## Edge Cases

1. **Calibration file corrupted**: fallback to default threshold, warn in logs
2. **Environment changed significantly**: user runs `--calibrate` or calls recalibration API
3. **No audio device during boot**: measurement fails → use defaults
4. **Sudden noise spike**: stddev catches it (variation high even if RMS normal)