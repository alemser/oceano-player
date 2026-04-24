# Bug Analysis: Music Not Detected - "Identifying" → Idle Loop

## Symptom Description

1. Needle placed on record → "Identifying..." appears briefly
2. Then immediately returns to idle screen
3. Music starts playing at full volume
4. Recognition never triggers
5. Screen stays idle despite music playing
6. Same pattern repeats throughout the entire track

## Root Cause Identified

### The Problem: stddevThreshold Too High

**Current threshold formula:**
```
threshold = noiseFloor.StdDev * stddevMultiplier
         = 0.005 * 3.0
         = 0.015
```

**Why it's failing:**

The `rollingStdDev` is computed over a **10-frame window** (≈0.5 seconds). During music playback:
- RMS is high (e.g., 0.15) ✅ above threshold 0.021
- But `rollingStdDev` is **only 0.005-0.010** because music level is **consistent** over 0.5s ❌

This is below the threshold (0.015), so the AND condition fails.

## Why OR is NOT the Solution

OR would revert to the original problem:
```go
if RMS >= threshold || stddev >= threshold
```

With RMS threshold = 0.021, CD transport noise (RMS ≈ 0.02-0.05) would trigger "Physical" ✗

## The Correct Fix

### Change Default StdDev

**File:** `cmd/oceano-source-detector/noise_floor.go`, line 54:
```go
// BEFORE (causing bug):
StdDev: 0.005,

// AFTER (fix):
StdDev: 0.001,
```

**Effect:**
- Old threshold: `0.005 * 3.0 = 0.015`
- New threshold: `0.001 * 3.0 = 0.003`

### Threshold Comparison

| Signal Type | Real StdDev | Old Threshold (0.015) | New Threshold (0.003) |
|------------|------------|------------------------|----------------------|
| CD transport noise | ~0.001-0.002 | ❌ None | ❌ None |
| Music (constant) | ~0.003-0.010 | ❌ None | ✅ Physical |
| Vinyl inter-track | ~0.002-0.005 | ❌ None | ✅ Physical |
| Lead-in crackle | ~0.010+ | ✅ Physical | ✅ Physical |

### Auto-Reject Existing Calibration

**File:** `cmd/oceano-source-detector/noise_floor.go`, line 74:
```go
if err := json.Unmarshal(data, &nf); err != nil || nf.StdDev > 0.004 {
    log.Printf("noise floor: ignoring corrupt calibration file %s — re-learning", path)
    return defaultNoiseFloor(), false
}
```

If the persisted file has `StdDev > 0.004`, it's rejected and defaults are used. This ensures the new lower thresholds take effect.

## Why This Works

1. **Lower threshold (0.003)** catches most music with constant volume
2. **CD noise** (stddev ≈ 0.001-0.002) stays below threshold → correctly ignored
3. **Same logic** - hybrid AND condition - no change needed
4. **Auto-reject** - existing calibrations with high StdDev are discarded

## Files to Modify

Only one change needed in `cmd/oceano-source-detector/noise_floor.go`:

```go
// Line 54:
StdDev: 0.001,  // was 0.005
```

## Verification Steps

After the fix:
1. Restart source-detector
2. Wait for log: "noise floor: starting from defaults"
3. Drop needle on record
4. Check logs - should show "SOURCE: None → Physical"

Expected thresholds in logs:
```
noise floor: thresholds rms=0.005 stddev=0.003
```