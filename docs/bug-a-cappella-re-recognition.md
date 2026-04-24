# Bug Analysis: A-Cappella Music Re-Recognition

## Symptom

- During a-cappella music (Tracy Chapman "Fast Car" live): recognition triggers multiple times
- Each quiet verse/chorus transition causes re-recognition

## Root Cause (Confirmed by Gemini)

### Problem 1: Exit Has No Hold Time
**Current exit condition:**
```go
if current == SourcePhysical {
    if windowRMS >= thresh.RMS {
        detected = SourcePhysical
    }
}  // Next frame below threshold → instantly becomes None!
```

- Music is not continuous - dramatic pauses drop RMS instantly
- System declares "None" 
- When music returns, must re-pass Entry → cuts attacks during quiet passages

### Problem 2: Entry Has No Debounce  
**Current entry condition:**
```go
if windowRMS >= thresh.RMS && (rollingStdDev >= thresh.StdDev || windowRMS >= thresh.RMS*3) {
    detected = SourcePhysical
}  // Single frame triggers!
```

- Vinyl pops/crackle trigger instantly
- System registers "Physical" seconds before real music

---

## Complete Fix

### 1. Exit: Add Hold Time (Sustained Silence Counter)

```go
var exitSilenceFrames int
const exitSilenceThreshold = 15  // ~0.7s sustained silence to exit

// In detection loop:
if current == SourcePhysical {
    if windowRMS >= thresh.RMS {
        exitSilenceFrames = 0  // Reset - music present
        detected = SourcePhysical
    } else {
        exitSilenceFrames++  // Increment during silence
        if exitSilenceFrames > exitSilenceThreshold {
            detected = SourceNone  // True silence - can exit
        } else {
            detected = SourcePhysical  // Debounced - stay in Physical
        }
    }
}
```

### 2. Entry: Add Debounce (Require Consecutive Frames)

```go
var entryTriggerFrames int
const entryTriggerThreshold = 3  // ~0.14s (3 frames) to prevent vinyl pops

// In detection loop:
if windowRMS >= thresh.RMS && (rollingStdDev >= thresh.StdDev || windowRMS >= thresh.RMS*3) {
    entryTriggerFrames++
    if entryTriggerFrames >= entryTriggerThreshold {
        detected = SourcePhysical
    }
} else {
    entryTriggerFrames = 0  // Reset if condition fails
}

// Don't update learner during entry debounce either
if windowRMS < thresh.RMS && entryTriggerFrames == 0 {
    learner.update(windowRMS, rollingStdDev)
}
```

### 3. Combined Logic

```go
var exitSilenceFrames int
var entryTriggerFrames int

const (
    exitSilenceThreshold = 15  // ~0.7s sustained silence to exit
    entryTriggerThreshold = 3    // ~0.14s sustained to enter
)

detected := SourceNone

if current == SourcePhysical {
    // EXIT: Needs sustained silence
    if windowRMS >= thresh.RMS {
        exitSilenceFrames = 0
        detected = SourcePhysical
    } else {
        exitSilenceFrames++
        if exitSilenceFrames > exitSilenceThreshold {
            detected = SourceNone
        }
    }
} else {
    // ENTRY: Needs sustained high signal
    if windowRMS >= thresh.RMS && (rollingStdDev >= thresh.StdDev || windowRMS >= thresh.RMS*3) {
        entryTriggerFrames++
        if entryTriggerFrames >= entryTriggerThreshold {
            detected = SourcePhysical
        }
    } else {
        entryTriggerFrames = 0
    }
}

// Learner: Only real silence (not transitioning to Physical)
if windowRMS < thresh.RMS && (current == SourcePhysical && detected == SourceNone) {
    // Actually exiting - don't learn
} else if windowRMS < thresh.RMS {
    learner.update(windowRMS, rollingStdDev)
}
```

---

## Decision Table (Final)

| Scenario | RMS | StdDev | Frames | Result |
|----------|-----|-------|--------|--------|
| Vinyl pop | 0.5 | 0.1 | 1 | 🕑 Wait (debounce) |
| Vinyl pop | 0.5 | 0.1 | 3 | ✅ Physical |
| A-cappella quiet verse | 0.01 | 0.002 | sustained | ✅ Physical (stays) |
| A-cappella chorus | 0.08 | 0.008 | 1 | 🕑 Wait → Physical |
| Between tracks | 0.001 | 0.0001 | 14 | 🕑 Wait (hold) |
| Between tracks | 0.001 | 0.0001 | 15 | ✅ None |
| Needle lift | 0.001 | 0.0001 | 20 | ✅ None |

---

## Files to Modify

`cmd/oceano-source-detector/runtime.go`:
- Add `exitSilenceFrames` counter
- Add `entryTriggerFrames` counter
- Modify detection logic to use debounce/hold

## Summary

| Fix | Problem | Solution |
|-----|---------|---------|
| Exit Hold | Quiet passages cut music | 15-frame sustain required |
| Entry Debounce | Vinyl pops trigger early | 3-frame sustain required |
| Both | A-cappella/quiet music stable | ✅ |