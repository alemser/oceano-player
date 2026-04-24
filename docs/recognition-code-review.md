# Code Review: Recognition Mechanism

## Executive Summary

This document reviews the recognition mechanism in the oceano-player project, focusing on:
1. Source detection (Physical vs None)
2. Noise floor learning
3. Recognition triggers
4. Cooldown and suppression logic

**Status**: The recent fixes are working (debounce + cooldown). The implementation is sound but could benefit from some simplifications.

---

## 1. Source Detection (source-detector)

### 1.1 Detection Algorithm
Location: `cmd/oceano-source-detector/runtime.go:181-223`

The source-detector uses **asymmetric hysteresis** to detect Physical vs None:

```
Entry (None → Physical):
  - 3 consecutive frames (0.14s)
  - RMS >= threshold AND (stddev >= threshold OR RMS >= 3× threshold)

Exit (Physical → None):
  - 50 consecutive frames (~2.3s) below threshold
  - After Fix 12: increased from 15 to 50 frames
```

**Analysis**: The 50-frame debounce is appropriate for music pauses (1-2s typical). However, with vinyl inter-track gaps up to 5-10s, this could delay detection of track boundaries.

### 1.2 Hybrid Detection
Uses both RMS AND standard deviation:
- RMS = energy level
- StdDev = variation (distinguishes music from constant noise like transport hum)

**Issue**: For a-cappella music (low RMS, low StdDev), detection may fail because the hybrid condition requires BOTH high RMS AND high variation.

```go
// Line 215
if windowRMS >= thresh.RMS && (rollingStdDev >= thresh.StdDev || windowRMS >= thresh.RMS*rmsHighBypassFactor)
```

The `rmsHighBypassFactor` (3.0) helps bypass the stddev check when RMS is high.

---

## 2. Noise Floor Learning

### 2.1 Algorithm
Location: `cmd/oceano-source-detector/noise_floor.go:136-142`

```go
func (l *noiseFloorLearner) update(windowRMS, rollingStdDev float64) {
    l.current.RMS = adaptRate*windowRMS + (1-adaptRate)*l.current.RMS
    l.current.StdDev = adaptRate*rollingStdDev + (1-adaptRate)*l.current.StdDev
    l.current.Windows++
}
```

Where `adaptRate = 0.005` (slow adaptation ~200 windows = 9s to adapt significantly).

### 2.2 Learning Trigger
**Critical Issue Found**: The learner is updated when:

```go
// Line 231
if windowRMS < thresh.RMS {
    learner.update(windowRMS, rollingStdDev)
}
```

**Problem**: This triggers when RMS is below the THRESHOLD (not when detection is None). This means:
- Quiet passages in music with RMS below threshold → learned as silence
- Threshold adapts UP → quiet passages become undetected

**Impact**: The fix at line 231 should check `detected == SourceNone`, but this was explicitly changed in a previous commit to avoid CD transport noise contamination.

### 2.3 Per-Format Calibration
Calibration files are separate:
- `/var/lib/oceano/noise-floor-cd.json`
- `/var/lib/oceano/noise-floor-vinyl.json`

**Good**: Format-specific calibration is preserved.

---

## 3. Recognition Triggers (state-manager)

### 3.1 Trigger Sources
Three sources can trigger recognition:

1. **pollSourceFile** (lines 122-256): Watches `/tmp/oceano-source.json`
2. **VU monitor** (lines 270+): Watches silence→audio transitions
3. **Fallback timer**: Periodic re-check (default 5 minutes)

### 3.2 pollSourceFile Logic
Location: `cmd/oceano-state-manager/source_vu_monitor.go:135-228`

Key variables:
- `newSession`: gap > SessionGapThreshold (45s default)
- `resumedAfterIdle`: gap > IdleDelay (10s default)
- `resumedAfterSilence`: changed && gap >= 2s

**Key Fix (Fix 15)**:
```go
// If we have a recent result, suppress resumption triggers
hasResult := m.recognitionResult != nil
recentSuccess := !m.lastRecognizedAt.IsZero() && 
               time.Now().Before(m.lastRecognizedAt.Add(120 * time.Second))

if hasResult && recentSuccess {
    // Clear flags so no trigger fires
    newSession = false
    resumedAfterIdle = false
    resumedAfterSilence = false
}
```

**Analysis**: This is the correct fix. The 120s (2 min) cooldown after a successful recognition prevents re-triggering during music pauses.

### 3.3 VU Monitor Logic
Location: `cmd/oceano-state-manager/source_vu_monitor.go:425-485`

The VU monitor has its OWN cooldown (20s):

```go
const boundaryCooldown = 20 * time.Second
recentAttemptFailed = time.Now().Before(m.lastRecognitionAttemptAt.Add(boundaryCooldown))

// Also suppress if already have result and not a hard boundary
if hasValidResult && !isHardBoundary {
    log.Printf("VU monitor: boundary suppressed — already have result and not a hard boundary")
    return
}
```

**Analysis**: Good - VU monitor has per-trigger suppression too.

---

## 4. Cooldown Mechanism

### 4.1 After Success
- **recentSuccess**: 120s cooldown (Fix 15) - PREVENTS triggers during music pauses

### 4.2 After Failure
- **recentFailure**: 20s cooldown - PREVENTS rapid retry after error/no-match

### 4.3 After Discard
Location: `recognition_coordinator.go:677`

When source changes during recognition, result is discarded:
```go
if !isPhysicalFinal || isAirPlayFinal || isBluetoothFinal {
    log.Printf("recognizer [%s]: discarding result — source changed during capture")
    c.mgr.lastRecognitionAttemptAt = time.Now() // FIX 11: Record to activate cooldown
    return
}
```

**Good**: Fix 11 properly sets the attempt time to activate cooldown after discard.

---

## 5. Known Issues / Areas for Improvement

### 5.1 Noise Floor Adaptation During Quiet Music
**Issue**: Quiet music passages (RMS < threshold) are being learned as silence.

**Current Behavior**:
- Lines 231 in runtime.go: `if windowRMS < thresh.RMS { learner.update(...) }`

**Recommendation**: Change to check `detected == SourceNone` to only learn when actually in silence state:
```go
// Alternative: only update when we're confident it's silence
if detected == SourceNone && windowRMS < thresh.RMS {
    learner.update(windowRMS, rollingStdDev)
}
```

However, this was explicitly changed previously to avoid transport noise contamination. Need to verify with actual vinyl use case.

### 5.2 Hard-coded Thresholds
**Issue**: Some thresholds are hard-coded in the Go code and not exposed via config.

- `recentSuccessCooldown` = 120s (should this be configurable?)
- `boundaryCooldown` = 20s (VU monitor)

### 5.3 VU vs pollSourceFile Overlap
**Observation**: Both VU monitor AND pollSourceFile can trigger recognition. While cooldowns prevent loops, there may be redundant triggers.

**Design Question**: Should we consolidate to a single trigger source?

---

## 6. Summary of Current Fixes

| Fix | Description | Status |
|-----|-------------|--------|
| Fix 12 | source-detector exit debounce 50 frames (2.3s) | ✅ Working |
| Fix 13 | IdleDelay 45s (reverted to 10s in Fix 16) | ✅ Reverted |
| Fix 14 | recentSuccess cooldown 120s | ✅ Working |
| Fix 15 | Check hasResult before calculating resumption | ✅ Working |
| Fix 16 | IdleDelay reverted to 10s | ✅ Done |

---

## 7. Recommendations

### 7.1 Low Priority - Monitor with Vinyl
The current implementation is tuned for CD-like content (regular intervals between tracks). Vinyl has more variable gaps. Monitor performance with vinyl and adjust `exitSilenceThreshold` if needed.

### 7.2 Consider Adding
1. **Format-specific debounce**: Vinyl could use longer exit debounce than CD
2. **Noise floor reset on format change**: When switching between CD/Vinyl, reset calibration

### 7.3 No Action Needed
The core mechanism is working. Current fixes correctly address the re-recognition loop issue.