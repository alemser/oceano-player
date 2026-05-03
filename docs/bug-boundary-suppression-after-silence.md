# Bug: Boundary Suppression After Silence Prevents Track Recognition
Date: 2026-05-03  
Investigation: Pi logs analysis for "Money" track recognition failure

## Summary
After a silence gap between tracks ("Time" → "Money"), the VU monitor incorrectly suppressed the silence→audio boundary trigger because it used the **previous track's duration** ("Time": 5m24s) to decide whether to suppress the new track's boundary.

## Timeline from Pi Logs (2026-05-03)

| Time | Event | Details |
|------|-------|---------|
| 18:20:45 | "Time" track ends | `elapsed_pct=106.2%` (duration exceeded) |
| 18:21:40 | Silence detected | Gap between tracks starts |
| 18:21:44 | **Boundary SUPPRESSED** | `1m19s elapsed, checks active from 4m3s (track 5m24s)` |
| 18:22:45 | Recognition finally succeeds | "The Great Gig In the Sky" recognized (after periodic timer fires) |

## Root Cause

### The Problem
In `cmd/oceano-state-manager/source_vu_monitor.go`, the `fireBoundaryTrigger()` function (line 464) checks suppression **before** the silence handler clears the stale track state.

When the silence→audio transition fires for "Money":
1. `durationMs = 324000` (5m24s from "Time" - still in memory)
2. `seekMS = 79000` (79s since last recognition)
3. `effPess = 0.75` (default DurationPessimism)
4. `suppressUntil = 324s * 0.75 = 243s` (4m3s)
5. `elapsed = 79s`
6. Since `79s < 243s` → **Boundary SUPPRESSED!**

### Code Path (source_vu_monitor.go:600-624)

```go
// Silence handler - lines 600-624
if out.inSilence && !staleSilenceCleared && out.silenceElapsed > 0 {
    m.mu.Lock()
    durationMS := 0
    seekMS := int64(0)
    seekUpdatedAt := time.Time{}
    if m.recognitionResult != nil {
        durationMS = m.recognitionResult.DurationMs  // Still "Time": 324s!
        seekMS = m.physicalSeekMS
        seekUpdatedAt = m.physicalSeekUpdatedAt
    }
    m.mu.Unlock()
    
    // Check if silence is long enough to clear (requires 90% of track duration)
    if shouldClearStaleRecognitionOnSilence(durationMS, seekMS, seekUpdatedAt, now, out.silenceElapsed) {
        if m.clearStalePhysicalRecognitionOnSilence("prolonged-silence", out.silenceElapsed) {
            staleSilenceCleared = true
        }
    }
}
```

Then at lines 627-629, the boundary fires:
```go
if out.boundary {
    durationExceededFiredForSeek = time.Time{}
    fireBoundaryTrigger(out.boundaryType, out.boundaryHard, time.Time{})
}
```

But inside `fireBoundaryTrigger()` (lines 488-496):
```go
if !bypassDurationGuards && shouldSuppressBoundary(durationMs, seekMS, seekUpdatedAt, durationGuardBypassUntil, now, effPess) {
    elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
    trackDuration := time.Duration(durationMs) * time.Millisecond
    suppressUntil := time.Duration(float64(trackDuration) * effPess)
    log.Printf("VU monitor: boundary suppressed (%s) — %s elapsed, checks active from %s (track %s)",
        reason, elapsed.Round(time.Second), suppressUntil.Round(time.Second), trackDuration.Round(time.Second))
    return  // <-- Returns without triggering recognition!
}
```

### Why State Wasn't Cleared

In `shouldClearStaleRecognitionOnSilence()` (source_vu_monitor.go:84-100):
```go
func shouldClearStaleRecognitionOnSilence(durationMs int, seekMS int64, seekUpdatedAt, now time.Time, silenceElapsed time.Duration) bool {
    if silenceElapsed <= 0 {
        return false
    }
    if durationMs > 0 && !seekUpdatedAt.IsZero() {
        delta := now.Sub(seekUpdatedAt).Milliseconds()
        if delta < 0 {
            delta = 0
        }
        elapsedMS := seekMS + delta
        minProgressMS := int64(float64(durationMs) * staleSilenceKnownTrackProgressFactor) // 0.90
        if elapsedMS < minProgressMS {
            return false  // <-- For "Time": 79s < 291.6s (4m51s), so returns false!
        }
    }
    // ...
}
```

The silence was only ~60s, but the code requires **4m51s** (90% of 5m24s) of silence to clear "Time" state!

## The Fix

When a track's duration has been exceeded (`elapsed > durationMs`, like "Time" at 106.2%), the state should be cleared immediately on silence, regardless of the 90% requirement.

### Option 1: Fix `shouldClearStaleRecognitionOnSilence()`
```go
func shouldClearStaleRecognitionOnSilence(durationMs int, seekMS int64, seekUpdatedAt, now time.Time, silenceElapsed time.Duration) bool {
    if silenceElapsed <= 0 {
        return false
    }
    if durationMs > 0 && !seekUpdatedAt.IsZero() {
        delta := now.Sub(seekUpdatedAt).Milliseconds()
        if delta < 0 {
            delta = 0
        }
        elapsedMS := seekMS + delta
        
        // If track duration exceeded, clear immediately
        if elapsedMS > int64(durationMs) {
            return true  // <-- Add this check
        }
        
        minProgressMS := int64(float64(durationMs) * staleSilenceKnownTrackProgressFactor)
        if elapsedMS < minProgressMS {
            return false
        }
    }
    // ...
}
```

### Option 2: Fix `fireBoundaryTrigger()` to check if duration exceeded
```go
fireBoundaryTrigger := func(reason string, isHardBoundary bool, detectedAt time.Time) {
    m.mu.Lock()
    var durationMs int
    var seekMS int64
    var seekUpdatedAt time.Time
    // ...
    m.mu.Unlock()
    
    // If track duration exceeded, don't suppress
    if durationMs > 0 && seekMS > 0 {
        elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
        if elapsed > time.Duration(durationMs)*time.Millisecond {
            // Duration exceeded - this is definitely a new track
            // Skip suppression checks
        }
    }
    
    // Original suppression checks...
}
```

## Impact
- **Affected scenario:** Any album where track duration is known and a silence gap occurs between tracks
- **User impact:** New track not recognized until the next periodic timer fires (up to `RecognizerRefreshInterval` = 2 minutes)
- **Severity:** Medium-High (delays track recognition by minutes)

## Verification
After fix, play "Dark Side of the Moon" vinyl:
1. "Time" should end
2. ~60s silence
3. "Money" should be recognized immediately on silence→audio boundary
4. Check logs: no "boundary suppressed" message for the "Money" track

---

## Resolution (2026-05-03)

Implemented in `source_vu_monitor.go`:

1. **`shouldClearStaleRecognitionOnSilence`** — If `elapsedMS >= durationMs`, clear after the existing 25s silence debounce (explicit track-end path). If silence lasts **`staleSilenceForceClearAfter` (50s)** with known duration, clear **without** the 90% progress floor so stuck seek / wrong catalog length cannot block the next track’s silence→audio boundary.

2. **`shouldSuppressBoundary`** — If `elapsed >= full track duration`, do not suppress (aligns with mature-progress “track is over” semantics).

3. **`shouldBypassDurationGuardsForBoundary`** — Hard `silence->audio` bypass when `elapsed >= track duration` before the pessimism-window check.

Unit tests: `TestShouldClearStaleRecognitionOnSilence_DurationExceededRespectsMinSilence`, `TestShouldClearStaleRecognitionOnSilence_ForceClearAfterLongSilenceDespiteLowProgress`, extended `TestShouldBypassDurationGuardsForBoundary`.
