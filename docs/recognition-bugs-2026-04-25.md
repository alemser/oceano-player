# Recognition Bug Findings

Date: 2026-04-25

## Audio Levels During Incident

RMS levels from source-detector heartbeat (recorded every minute):

| Time | RMS | Source |
|------|-----|--------|
| 10:00:22 | 0.01046 | Low (silence zone beginning) |
| 10:01:22 | 0.14326 | Normal playback |
| 10:02:22 | 0.43945 | Normal playback |
| 10:03:22 | 0.17854 | Normal playback |
| 10:04:22 | 0.32832 | Normal playback |
| 10:05:22 | 0.29718 | Normal playback |
| 10:06:22 | 0.02006 | Silence zone |
| 10:07:22 | 0.00723 | Deep silence |
| 10:08:22 | 0.00555 | Deep silence |

**Threshold:** rms=0.00491 (adaptive noise floor)

**Observation:** When silence zone began at ~10:06, RMS dropped from 0.297 to 0.020, then further to 0.007. This is above the threshold but close to it, which may cause the VU monitor to oscillate between "silence" and "audio detected".

---

## Bugs Observed

### Bug 1: Display not cleared on silence

**Symptoms:**
- When side A ended and vinyl entered silence zone, display still showed the previous track
- After lifting the tonearm, track info persisted on screen

**Evidence:**
```
10:06:26 VU monitor: silence detected
# No "idle" log entry or track clearing follows
```

**Root Cause:**
Silence is detected but no log entry shows `state: idle` or track clearing. The display never receives the signal to clear the previous track.

**Suggested Fix:**
Check if `writeState()` is called when silence is detected and whether it sets `state: idle` and `track: null`.

---

### Bug 2: Multiple triggers on audio resumption

**Symptoms:**
- When audio resumed after silence, multiple triggers fired within 1 second
- Causes redundant recognition attempts and confusion

**Evidence:**
```
10:07:19 VU monitor: track boundary detected (silence->audio hard=true) — triggering recognition
10:07:19 VU monitor: track duration exceeded by 35s — firing hard recognition trigger
10:07:19 VU monitor: track boundary detected (duration-exceeded hard=true) — triggering recognition
10:07:20 VU monitor: silence detected
```

Three events in 1 second (10:07:19-10:07:20).

**Root Cause:**
Two detection mechanisms are both triggering simultaneously:
1. `silence->audio` boundary detection
2. `duration-exceeded` hard trigger

They are not mutually exclusive and fire together when silence-to-audio transition happens and track is past expected duration.

**Suggested Fix:**
Add mutual exclusion — if `silence->audio` fires, suppress `duration-exceeded` for a debounce period (e.g., 5 seconds).

---

### Bug 3: Seek calculation produces invalid values

**Symptoms:**
- Progress bar shows position far ahead of actual track position
- When next track starts, progress bar shows wrong position

**Evidence:**
```
seek=395s elapsed=408s duration=251s elapsed_pct=162.9
```
Seek value (395s) exceeds track duration (251s) by 144 seconds.

Another example:
```
seek=157s elapsed=274s duration=251s elapsed_pct=109.2
```

**Root Cause:**
The seek calculation is accumulating elapsed time incorrectly. Likely causes:
1. `audioStartTime` not being reset when new track is recognized
2. Using stale `duration` from previous track
3. Time accumulated from silence period being incorrectly included

**Suggested Fix:**
In `recognition_coordinator.go`, verify that:
- `audioStartTime` is reset to `now` when a new track result is received
- `seek` is calculated as `now - audioStartTime` NOT as accumulated elapsed time
- Duration used in percentage calculation is the current track's duration, not a stale one

---

### Bug 4: No-match loop on silence zone

**Symptoms:**
- During silence zone (between sides), system keeps retrying recognition
- Multiple "no match" attempts until audio resumes

**Evidence:**
```
10:07:20 VU monitor: silence detected
10:07:32 recognizer chain: ACRCloud: no match — trying next
10:07:33 recognizer chain: Shazam: no match — trying next
10:07:33 recognizer [ACRCloud→Shazam]: no match — retrying in 15s
10:07:46 recognizer chain: ACRCloud: no match — trying next
10:07:46 recognizer chain: Shazam: no match — trying next
10:07:46 recognizer [ACRCloud→Shazam]: no match — retrying in 15s
10:08:12 recognizer chain: ACRCloud: no match — trying next
10:08:13 recognizer chain: Shazam: no match — trying next
```

**Root Cause:**
Recognition is triggered despite silence being detected. The system should NOT attempt recognition during silence.

**Suggested Fix:**
Recognition coordinator should check VU state before attempting recognition. If silence is detected, skip recognition attempts until audio resumes.

---

### Bug 5: State file shows track=null but state=playing

**Evidence:**
```json
{
  "source": "Vinyl",
  "format": "Vinyl",
  "state": "playing",
  "track": null,
  "updated_at": "2026-04-25T09:08:16Z"
}
```

**Root Cause:**
Inconsistent state: `state: "playing"` but `track: null`. The display likely checks both fields and shows track if `state: playing`, ignoring the null track.

**Suggested Fix:**
State should be `"idle"` when no track is identified. The condition should be:
```go
if track == nil {
    state = "idle"
} else {
    state = "playing"
}
```

---

## Priority Fix Order

1. **Bug 5** (State inconsistency) — Easiest, likely fixes display issue immediately
2. **Bug 1** (Display not cleared) — Check if state write happens on silence
3. **Bug 4** (No-match loop during silence) — Stop recognition attempts during silence
4. **Bug 2** (Multiple triggers) — Add debounce between detection mechanisms
5. **Bug 3** (Seek calculation) — Reset timing variables on new track recognition

## Files to Review

- `cmd/oceano-state-manager/internal/state.go` — state writing logic
- `cmd/oceano-state-manager/internal/vu_monitor.go` — VU detection and boundary triggering
- `cmd/oceano-state-manager/internal/recognition_coordinator.go` — seek calculation and trigger logic

---

## Additional Analysis: Mic Gain Reduction Fix

**User Report:** After reducing mic gain, the "no-match loop during silence" problem stopped.

### New RMS Data After Gain Adjustment

| Time | RMS | Source | Event |
|------|-----|--------|-------|
| 10:11:22 | 0.00628 | Physical | Silence zone |
| 10:12:22 | 0.00634 | Physical | Silence zone |
| 10:13:22 | 0.00553 | Physical | Silence zone |
| 10:14:22 | 0.00600 | Physical | Silence zone |
| 10:15:22 | 0.00605 | Physical | Silence zone |
| 10:16:22 | 0.00559 | Physical | Silence zone |
| 10:17:06 | 0.00485 | **None** | Physical → None (below threshold) |
| 10:17:22 | 0.24657 | **Physical** | None → Physical (audio detected) |
| 10:18:13 | 0.00451 | **None** | Physical → None |
| 10:18:22 | 0.00472 | None | Silence |
| 10:19:22 | 0.00425 | None | Silence |
| 10:20:22 | 0.00443 | None | Silence |

### Key Observations

1. **Source detection is now working correctly:**
   - 10:17:06 — RMS dropped to 0.00485 (below threshold 0.00491) → correctly detected as `None`
   - 10:17:22 — RMS jumped to 0.24657 → correctly detected as `Physical`
   - 10:18:13 — RMS dropped to 0.00451 → correctly detected as `None`

2. **VU monitor behavior improved:**
   ```
   10:17:22 VU monitor: boundary suppressed (silence->audio) — source is not Physical
   ```
   This shows the VU monitor is correctly ignoring audio when source is not Physical.

3. **Recognition stopped during silence:**
   No "no match" loop during the silence period from 10:17:06 to 10:17:22.

### Why This Fixed the Bug

**Before mic gain reduction:**
- Silence RMS was around 0.007 (close to threshold 0.00491)
- Small fluctuations could cross threshold and back
- Caused oscillation between `Physical` and `None` source detection
- VU monitor kept detecting "audio" even during vinyl silence because RMS fluctuated above threshold

**After mic gain reduction:**
- Silence RMS is now consistently around 0.004-0.006 (closer to threshold but still above)
- The threshold adaptive system recalculated: `thresh rms=0.00491`
- Clear separation between silence (~0.005) and actual audio (0.15-0.40)
- Source detector correctly identifies `None` during silence

### Revised Understanding

**Original Bug 4** ("No-match loop during silence") was not just a state manager bug — it was exacerbated by the mic gain being too high:

1. High mic gain amplified the vinyl's inherent noise floor
2. Silence RMS (~0.007) was too close to detection threshold (~0.005)
3. Minor audio fluctuations kept triggering source as `Physical` and VU as "audio detected"
4. Recognition kept attempting during what should have been silence

**This does NOT mean Bug 4 is completely fixed** — there are still code-level issues:
- State manager still doesn't check source before attempting recognition (log shows it tried to recognize something at 10:17:36 when source was None)
- The `boundary suppressed (silence->audio) — source is not Physical` message suggests this check now exists, but was it always there?

**Recommendation:** Verify the source check in recognition coordinator is working correctly. The no-match loop before 10:17 suggests the check may have been missing or ineffective with high mic gain.