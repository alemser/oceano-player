# Recognition subsystem

**Scope:** `cmd/oceano-state-manager/` — the coordinator, VU monitor, boundary detector, provider
wrappers, and all configuration types that drive track identification for physical sources.

---

## Data flow

```
oceano-source-detector
  /tmp/oceano-pcm.sock  ──→  captureFromPCMSocket()   →  WAV file
  /tmp/oceano-vu.sock   ──→  runVUMonitor()            →  recognizeTrigger channel
  /tmp/oceano-source.json ─→ pollSourceFile()          →  recognizeTrigger channel (on resume)

recognizeTrigger channel
  └─→ recognitionCoordinator.run()
        ├── per-input policy check    (config.json → amplifier.inputs[].recognition_policy)
        ├── source guard              (skip if AirPlay / Bluetooth / not Physical)
        ├── backoff check             (rate-limit / error / no-match)
        ├── captureFromPCMSocket()    → WAV
        ├── ChainRecognizer.Recognize()
        │     ├── ACRCloudRecognizer  → HTTPS identify-*.acrcloud.com
        │     └── ShazamRecognizer    → local Python daemon (shazamio)
        ├── maybeConfirmCandidate()   (optional cross-provider second pass)
        └── applyRecognizedResult()
              ├── library.LookupByIDs()   (prefer saved metadata over provider)
              ├── fetchArtwork() / fetchArtworkFromSong()
              ├── library.RecordPlay()    (persist to SQLite)
              └── mgr state update        → markDirty() → /tmp/oceano-state.json
```

---

## Trigger mechanisms

Four paths send a `recognizeTrigger` to the coordinator goroutine.

### 1. VU boundary (`runVUMonitor` → `boundary_detector.go`)

Reads float32 stereo frames at ~21.5 Hz from `/tmp/oceano-vu.sock`.  
The `vuBoundaryDetector` classifies each frame and emits a `vuBoundaryOutcome`.

**Hard boundary** (`silence→audio`, `isBoundary=true`, `isHardBoundary=true`):  
`silenceFrames` (22) consecutive frames below threshold, then `activeFrames` (11) above.  
Requires `hardSilenceFrames` (40) of deep silence before the "hard" flag is set —
this arms the duration-guard bypass and signals a definitive track start (needle drop, CD track).  
On hard boundary: pre-existing recognition state is cleared immediately (UI shows "Identifying…").  
PCM skip: `captureSkipDuration` returns 2 s to flush stylus crackle from the buffer.

**Soft boundary** (`energy-change`, `isBoundary=true`, `isHardBoundary=false`):  
Two exponential moving averages (`slowEMA` α=0.005, `fastEMA` α=0.15) track signal energy.
A dip is confirmed when `fastEMA < slowEMA × energyDipRatio` (0.45) for at least
`energyDipMinFrames` (32) frames, then `fastEMA > slowEMA × energyRecoverRatio` (0.75).
The dip must not exceed `energyDipMaxFrames` (128 = 4 × minFrames) — longer dips are
treated as silence, not a gapless transition. A 30 s cooldown (`energyChangeCooldown`)
prevents repeated triggers on slowly fading tracks.  
On soft boundary: pre-existing recognition state is preserved until a new result arrives.  
PCM skip: 0 s (the new track is already playing cleanly).

**Duration-exceeded trigger** (fires from `readVUFrames`, treated as hard boundary):  
When elapsed time since last recognition exceeds `knownDuration + 10 s` (`durationExceededGrace`)
and no VU boundary fired (gapless/live albums), a hard trigger is generated.  
`detectedAt` is set to the theoretical track-end time so seek anchoring is correct.

**Stale-silence clear** (does not trigger recognition):  
After `staleSilenceKnownTrackClear` (25 s) of continuous silence, if seek position is past
`staleSilenceKnownTrackProgressFactor` (90%) of known duration, the current recognition result
is cleared. Prevents the UI from showing a stale track after the record stops.

### 2. Source file resume (`pollSourceFile`, every 2 s)

Reads `/tmp/oceano-source.json`. On `None → Physical` transitions:

| Gap since last Physical | Action |
|-------------------------|--------|
| > `SessionGapThreshold` (45 s) | **New session**: clear all recognition state, send `triggerPeriodicRecognition` |
| > `IdleDelay` (10 s) | **Resumed after idle**: same as new session but preserve format |
| ≥ `physicalResumeRecognitionGap` (2 s, hardcoded) | **Resumed within session**: clear state, queue trigger (handles needle lift/re-drop) |

Transient `None` flickers of ≤ `transientNoneIgnoreWindow` (8 s, hardcoded) are ignored to
absorb brief source-detector restarts without triggering a new session.

### 3. Shazam continuity monitor (`runShazamContinuityMonitor`, `main.go`)

Runs independently at `ShazamContinuityInterval` (8 s). Polls with a short capture
(`ShazamContinuityCaptureDuration`, 4 s). Used only when Shazam is available and
`shazamContinuityReady` is true (set after first successful match).

Confirmation logic:

- Within `ContinuityCalibrationGrace` (45 s) of the last match: requires
  `ContinuityRequiredSightingsUncalibrated` (3) consecutive sightings of the same
  `from→to` pair within `ContinuityMismatchConfirmWindow` (3 min).
- After the grace period: requires `ContinuityRequiredSightingsCalibrated` (2) sightings.

When within `EarlyCheckMargin` (20 s) of known track end, the monitor polls more aggressively.
When confirmed, sends `recognizeTrigger{isBoundary: true, detectedAt: firstSightingTime}` —
`detectedAt` carries the first-sighting timestamp so the coordinator's seek anchor is accurate.

### 4. SIGUSR1

`systemctl kill --kill-who=main --signal=SIGUSR1 oceano-state-manager.service`  
Forces `triggerBoundaryRecognition(false)` (soft boundary, no state clear).

---

## Coordinator loop (`recognition_coordinator.go`)

Receives triggers and applies guards in order:

```
1. Backoff check
   ├── Rate limit (429):   5 min (rateLimitBackoff — hardcoded)
   ├── Error:              30 s  (errorBackoff — hardcoded)
   └── No match:           NoMatchBackoff (default 15 s — configurable)
   Boundary triggers bypass all backoff except rate-limit.

2. Source guard: skip if !Physical || AirPlay || Bluetooth || VU in silence

3. Per-input policy (config.json):
   "off"          → set phase="off", skip
   "display_only" → run recognition, do not persist to library
   "library"      → run recognition, persist to library
   "auto"         → infer from input label ("phono"/"vinyl"/"cd") or device role

4. Capture: captureFromPCMSocket(PCMSocket, RecognizerCaptureDuration, skip)

5. Final source guard (post-capture): discard result if source changed during capture

6. Chain.Recognize() → result or nil

7. Same-track restore check (when result == current track):
   canRestore when preBoundarySeekMS >= BoundaryRestoreMinSeek (60 s)
   AND (for hard boundaries) preBoundaryElapsed < DurationPessimism × knownDuration
   If restorable: restore pre-boundary state, merge any new provider IDs, skip persist.

8. maybeConfirmCandidate() (when ConfirmationDelay > 0 and result is a new track):
   Skipped when: score >= ConfirmationBypassScore (95), or boundary trigger.
   If two providers configured: runs both in parallel on a fresh capture.
   If single provider: re-calls same provider.

9. applyRecognizedResult():
   a. library.LookupByIDs(ACRID, ShazamID) — known track overrides provider metadata
   b. fetchArtwork() by album, then by song title
   c. library.RecordPlay() if policy == "library"
   d. Read back final entry to pick up equivalent-metadata merges
   e. Compute seekMS from boundary/capture timing
   f. Write to mgr state, markDirty()
```

The periodic fallback timer fires at `RecognizerMaxInterval` (5 min) when no track is
identified yet, or at `RecognizerRefreshInterval` (2 min) after a successful recognition.
The timer is reset to `RecognizerMaxInterval` on every trigger received from the channel.

---

## Boundary suppression (duration guards)

Guards prevent false-positive boundary triggers during quiet passages mid-track.

```
shouldSuppressBoundary():
  suppress when elapsed < DurationPessimism × knownDuration
  (bypass when: hardSilence boundary + elapsed <= earlyBypassGuardWindow (45 s, hardcoded))

shouldIgnoreBoundaryAtMatureProgress():
  ignore when elapsed >= DurationPessimism × knownDuration AND elapsed < knownDuration
  (hands off to continuity monitor in this range when shazamContinuityReady)

shouldSuppressBoundarySensitiveBoundary():
  extra lock for library rows marked BoundarySensitive:
  suppress all non-duration-exceeded boundaries until elapsed >= knownDuration

effectiveDurationPessimism():
  base = DurationPessimism (default 0.75)
  if boundarySensitive: base + 0.12, capped at 0.98

durationGuardBypassUntil:
  armed for DurationGuardBypassWindow (20 s) after hardSilenceFrames (40) or
  energyDipMinFrames (32) threshold is crossed — allows a quick needle re-drop
  to trigger recognition at track start without being suppressed.
```

---

## Provider chain (`recognition_setup.go`)

Both recognizers are always instantiated independently. Continuity always uses Shazam,
regardless of the chain policy.

```
RecognizerChain     Chain order           Confirmer (used by maybeConfirmCandidate)
──────────────────  ────────────────────  ────────────────────────────────────────
"acrcloud_first"    ACRCloud → Shazam     Shazam (second in ordered slice)
"shazam_first"      Shazam → ACRCloud     ACRCloud
"acrcloud_only"     ACRCloud              nil (same-provider second call if confirm enabled)
"shazam_only"       Shazam                nil
```

`ChainRecognizer` (`internal/recognition/chain.go`) tries providers in order and returns the
first non-nil result. Each provider is wrapped in `statsRecognizer` for per-call telemetry.
`ShazamContinuity` gets its own stats name (`"ShazamContinuity"`) so chain and continuity
call counts are tracked separately in the library.

---

## Configurable parameters

### CLI flags / `Config` struct (`config_types.go`)

All have defaults set in `defaultConfig()`. Deployed via `oceano-web` which rewrites
systemd unit args on config save.

| Field | Default | Meaning |
|-------|---------|---------|
| `RecognizerCaptureDuration` | 7 s | Primary PCM capture length per attempt |
| `RecognizerMaxInterval` | 5 min | Fallback timer when no track identified yet |
| `RecognizerRefreshInterval` | 2 min | Periodic re-check after successful recognition (0 = disable) |
| `NoMatchBackoff` | 15 s | Retry wait after provider returns no result |
| `RecognizerChain` | `"acrcloud_first"` | Provider order; see table above |
| `VUSilenceThreshold` | 0.0095 | Base RMS floor for silence detection |
| `DurationPessimism` | 0.75 | Fraction of known duration before boundaries are allowed |
| `DurationGuardBypassWindow` | 20 s | Duration-guard bypass arm window after hard silence |
| `BoundaryRestoreMinSeek` | 60 s | Minimum seek before same-track restore is allowed |
| `ConfirmationDelay` | 0 (disabled) | Wait before second confirmation capture |
| `ConfirmationCaptureDuration` | 4 s | Capture length for confirmation call |
| `ConfirmationBypassScore` | 95 | Skip confirmation when primary score ≥ this |
| `ShazamContinuityInterval` | 8 s | Polling interval for continuity monitor |
| `ShazamContinuityCaptureDuration` | 4 s | Capture length for continuity polls |
| `ContinuityCalibrationGrace` | 45 s | Grace period after match; stricter sighting threshold |
| `ContinuityMismatchConfirmWindow` | 3 min | Window in which sightings are counted |
| `ContinuityRequiredSightingsCalibrated` | 2 | Sightings needed after grace to fire |
| `ContinuityRequiredSightingsUncalibrated` | 3 | Sightings needed during grace |
| `EarlyCheckMargin` | 20 s | How close to track end continuity monitor tightens |
| `IdleDelay` | 10 s | Track shown after silence before idle screen |
| `SessionGapThreshold` | 45 s | Max pause treated as inter-track vs. end of record |
| `PCMSocket` | `/tmp/oceano-pcm.sock` | PCM relay from source-detector |
| `VUSocket` | `/tmp/oceano-vu.sock` | VU frame socket |
| `LibraryDB` | `/var/lib/oceano/library.db` | SQLite path (empty = disable library) |
| `ArtworkDir` | `/var/lib/oceano/artwork` | Local artwork cache directory |
| `CalibrationConfigPath` | `/etc/oceano/config.json` | Runtime config for per-input policies and calibration |

### `config.json` (runtime, no restart needed for most)

Loaded from `CalibrationConfigPath` by the VU monitor and per-input policy resolver.

**Per-input recognition policy** (`amplifier.inputs[].recognition_policy`):

```json
"inputs": [{"id": "phono-1", "logical_name": "Phono", "recognition_policy": "library"}]
```

Valid values: `"library"` | `"display_only"` | `"off"` | `"auto"`.

**Calibration profiles** (`advanced.calibration_profiles`):

Override VU detector thresholds per physical input. Loaded on each VU socket reconnect
(and every 24 h during long uptimes, see `telemetryRefreshInterval`).

```json
"calibration_profiles": {
  "<input_id>": {
    "off": {"avg_rms": 0.002},
    "on":  {"avg_rms": 0.08},
    "vinyl_transition": {
      "gap_avg_rms": 0.003,
      "attack_avg_rms": 0.15,
      "gap_duration_secs": 1.5,
      "samples_per_sec": 21.5
    }
  }
}
```

`off.avg_rms` → `silenceEnterThreshold` / `silenceExitThreshold`.  
`vinyl_transition` → `transitionGapRMS`, `transitionMinMusicRMS`, `energyDipMinFrames`, `energyDipMaxFrames`.

**RMS percentile learning** (`advanced.rms_percentile_learning`):

Observes live RMS histograms for silence and music frames. When `autonomous_apply: true`
and sample counts exceed the minimums, overwrites `silenceEnterThreshold`/`silenceExitThreshold`
on the live detector without a restart. Applied every `persist_interval_secs`.

```json
"rms_percentile_learning": {
  "enabled": true,
  "autonomous_apply": false,
  "min_silence_samples": 400,
  "min_music_samples": 400,
  "persist_interval_secs": 120,
  "histogram_bins": 80,
  "histogram_max_rms": 0.25
}
```

**Telemetry nudges** (`advanced.r3_telemetry_nudges`):

Adjusts silence threshold and `DurationPessimism` based on boundary false-positive rates
derived from play history. Applied as additive deltas on top of calibration profiles and
RMS learning. Refreshed every 24 h.

```json
"r3_telemetry_nudges": {
  "enabled": true,
  "lookback_days": 14,
  "min_followup_pairs": 25,
  "baseline_false_positive_ratio": 0.10,
  "max_silence_threshold_delta": 0.004,
  "max_duration_pessimism_delta": 0.06,
  "early_track_progress_p75_threshold": 0.18,
  "early_track_extra_silence_delta": 0.001
}
```

---

## Hardcoded constants

These require a recompile. All are in `boundary_detector.go` or `source_vu_monitor.go`.

| Constant | Value | Location | Meaning |
|----------|-------|----------|---------|
| `silenceFrames` | 22 (~1 s) | `source_vu_monitor.go` | Frames below threshold to enter silence |
| `activeFrames` | 11 (~0.5 s) | `source_vu_monitor.go` | Frames above threshold to exit silence |
| `hardSilenceFrames` | 40 | `boundary_detector.go` | Frames required for "hard" silence flag |
| `energyDipMinFrames` | 32 | `boundary_detector.go` | Min dip duration for soft boundary |
| `energyDipMaxFrames` | 128 (= 4 × min) | `boundary_detector.go` | Max dip before treated as silence |
| `energySlowAlpha` | 0.005 | `boundary_detector.go` | EMA weight for slow energy tracker |
| `energyFastAlpha` | 0.15 | `boundary_detector.go` | EMA weight for fast energy tracker |
| `energyDipRatio` | 0.45 | `boundary_detector.go` | `fastEMA / slowEMA` threshold for dip entry |
| `energyRecoverRatio` | 0.75 | `boundary_detector.go` | `fastEMA / slowEMA` threshold for dip exit |
| `energyWarmupFrames` | 200 | `boundary_detector.go` | Frames before energy detection activates |
| `energyChangeCooldown` | 30 s | `boundary_detector.go` | Min gap between consecutive soft boundaries |
| `rateLimitBackoff` | 5 min | `recognition_coordinator.go` | Backoff on HTTP 429 |
| `errorBackoff` | 30 s | `recognition_coordinator.go` | Backoff on provider error |
| `physicalResumeRecognitionGap` | 2 s | `source_vu_monitor.go` | Min None gap before clearing state on resume |
| `staleSilenceKnownTrackClear` | 25 s | `source_vu_monitor.go` | Silence duration before stale track cleared |
| `staleSilenceKnownTrackProgressFactor` | 0.90 | `source_vu_monitor.go` | Min track progress for stale clear |
| `earlyBypassGuardWindow` | 45 s | `source_vu_monitor.go` | Max elapsed for duration-guard bypass to apply |
| `transientNoneIgnoreWindow` | 8 s | `source_vu_monitor.go` | None flicker suppression window |
| `durationExceededGrace` | 10 s | `source_vu_monitor.go` | Grace past known duration before exceeded-trigger fires |
| `telemetryRefreshInterval` | 24 h | `source_vu_monitor.go` | How often detector settings reload during long uptime |
| `captureSkipDuration` (hard) | 2 s | `recognition_coordinator.go` | PCM skip for hard boundaries |
| `captureSkipDuration` (soft) | 0 s | `recognition_coordinator.go` | No skip for soft/gapless boundaries |
| `boundarySensitiveBoost` | +0.12, cap 0.98 | `source_vu_monitor.go` | Extra pessimism for sensitive tracks |

---

## Seek position tracking

`physicalSeekMS` + `physicalSeekUpdatedAt` are written on every recognition.  
The UI interpolates: `pos = seekMS + (now - seekUpdatedAt)`.

On boundary triggers: `seekMS = max(now - captureStart, now - lastBoundaryAt)`.  
On periodic triggers with a new track: `seekMS = now - captureStart`.  
On periodic triggers with the same track: seek is preserved (conservative, avoids jumps).  
On same-track restore: `seekMS = recoverSeekMSFromSnapshot(preBoundarySeekMS, preBoundarySeekUpdatedAt, now)`.

---

## Library persistence flow

`applyRecognizedResult()` writes to SQLite only when `persistToLibrary == true`
(policy `"library"`). Order of preference for metadata:

1. Provider result (ACRCloud or Shazam)
2. `library.LookupByIDs()` — overwrites title/artist/album/format/duration from known entry
3. `library.RecordPlay()` — upserts entry, returns `entryID`
4. `library.GetByID(entryID)` — reads back final entry to pick up any equivalent-metadata
   merge applied by the library (e.g. user-edited title on a previously known ACRID)
5. `fetchArtwork()` by album, then `fetchArtworkFromSong()` by title/artist

`BoundarySensitive` flag is set on the library entry by the user or by telemetry analysis.
It is read back after `RecordPlay` and stored in `mgr.physicalBoundarySensitive`.

---

## RecognitionResult type

```go
type RecognitionResult struct {
  ACRID      string  // ACRCloud access_key / unique ID
  ShazamID   string  // Shazam track key
  ISRC       string  // International Standard Recording Code (ACRCloud)
  Title      string
  Artist     string
  Album      string
  Label      string  // Record label
  Released   string  // Release date
  Score      int     // Match confidence (0–100, Shazam has no score)
  Format     string  // "CD", "Vinyl", "Streaming" — used for UI chips
  DurationMs int     // Track duration from provider
  TrackNumber string // "3", "A2", "B4" — from library, not providers
}
```

Type-aliased in `cmd/oceano-state-manager/recognizer.go`:
```go
type RecognitionResult = internalrecognition.Result
type Recognizer = internalrecognition.Recognizer
```

---

## Recognition phases (state machine)

`mgr.recognitionPhase` is a string exposed in `/tmp/oceano-state.json` so the UI can
distinguish transient states from a stable "no track identified" result.

| Phase | Set by | UI meaning |
|-------|--------|------------|
| `""` (empty) | After successful recognition (`applyRecognizedResult`) | Track identified — show metadata |
| `"no_match"` | After provider returns nil (`handleNoMatch`) | Was trying to identify, failed — may retry |
| `"off"` | When input policy is `"off"` | Recognition disabled for this input |

The phase is **not** set to `"identifying"` — the UI infers this from the combination of
`source == "Physical"` and `track` being empty. The `recognizerBusyUntil` timestamp on `mgr`
tracks when the coordinator is actively capturing/recording, but is not exposed in state.

---

## Confirmation flow (`maybeConfirmCandidate`)

Triggered only for **non-boundary** (periodic/fallback) triggers when a **new track candidate**
is detected and `ConfirmationDelay > 0` (default 0 = disabled).

```
Conditions to SKIP confirmation:
  1. ConfirmationDelay == 0  (disabled)
  2. Score >= ConfirmationBypassScore (95)  (high confidence)
  3. isBoundaryTrigger == true  (boundary triggers never wait)

When confirmation runs:
  1. Wait ConfirmationDelay (default 0, i.e. immediate if enabled)
  2. Capture fresh PCM (ConfirmationCaptureDuration, default 4 s)
  3. If two providers configured (chain + confirmer differ):
     → Run BOTH providers in parallel on same WAV
     → chooseConfirmationResult picks the better match
  4. If single provider:
     → Re-call same provider
  5. Compare confirmation result with original:
     → Same ACRID → confirmed, merge ShazamID if missing
     → Same title+artist (tracksEquivalent) → confirmed
     → Different → keep original, log disagreement
```

`chooseConfirmationResult` prefers the result with a non-empty ID; if both have IDs,
prefers the primary recognizer's result. Errors or nil from confirmation fall back to
the original candidate — confirmation never rejects a valid primary match on its own.

---

## Same-track restore

When a boundary trigger fires but recognition returns the **same track** as before, the
coordinator checks whether the boundary was a false positive (quiet passage, not a track change).

```
restore allowed when ALL of:
  1. sameTrackForStateContinuity(preBoundaryResult, newResult) == true
     (same ACRID, same ShazamID, or equivalent title+artist)
  2. preBoundarySeekMS >= BoundaryRestoreMinSeek (60 s)
     — the track had progressed enough to make a mid-track boundary suspicious
  3. For hard boundaries: preBoundaryElapsed < DurationPessimism × knownDuration
     — the boundary happened before the expected track-end window

restore BLOCKED when:
  — Seek is too early (< 60 s): could be a real intro→main transition
  — Elapsed exceeds DurationPessimism × knownDuration: track was likely ending

On restore:
  — Reinstates pre-boundary recognitionResult, artworkPath, entryID, boundarySensitive
  — Recalculates seekMS: recoverSeekMSFromSnapshot(seekMS, seekUpdatedAt, now)
  — Merges any new provider IDs from the fresh result (e.g. ShazamID added to ACRCloud match)
  — Skips library persistence (no new play recorded)
  — Logs: "same track confirmed — restoring pre-boundary result"

On blocked restore:
  — Applies the fresh result as a new track (progress resets, new library entry)
```

This prevents the progress bar from jumping back to 0:00 on quiet passages that the VU
detector mistakes for track boundaries.

---

## Play history recording (`play_history.go`)

A separate goroutine (`runPlayHistoryRecorder`) polls every **5 seconds** and writes
play records to the library database. Independent of the recognition coordinator.

```
Tick flow:
  1. snapshotForHistory() — reads current mgr state under mutex
  2. Build key: "source\0title\0artist"
  3. If key unchanged → no-op (still playing same track)
  4. If key changed:
     a. Close previous play record (lib.ClosePlayHistory)
     b. Open new record (lib.OpenPlayHistory)
  5. Special case: Physical "unknown" placeholder → update in-place
     when recognition completes (preserves listening time from track start)
```

**Physical placeholder**: when `Physical` source is active but recognition hasn't completed,
a placeholder entry is opened with only `Source: "Physical"` and `MediaFormat`. When recognition
succeeds, `UpdateOpenPlayHistory` fills in title/artist/album/score. This ensures no listening
time is lost during the 7–15 s recognition delay.

**Backdated start**: `StartedAt` is computed as `physicalSeekUpdatedAt - physicalSeekMS` so the
play history reflects the actual track start time, not the recognition completion time.

Priority for snapshots: **Physical > AirPlay > Bluetooth** (matches state_output.go).

---

## Boundary followup tracking (`boundary_followup.go`)

Every recognition attempt triggered by a VU boundary event records a followup row in the
library for telemetry analysis (false-positive detection, calibration nudges).

```go
type BoundaryRecognitionFollowup struct {
  Outcome           FollowupOutcome  // see table below
  PostACRID         string
  PostShazamID      string
  PostCollectionID  int64
  PostPlayHistoryID int64
  NewRecording      *bool  // true=new track, false=same track, nil=unknown
}
```

| Outcome | Meaning |
|---------|---------|
| `FollowupOutcomeMatched` | Recognition succeeded, track identified |
| `FollowupOutcomeSameTrackRestored` | Same track re-confirmed, pre-boundary state restored |
| `FollowupOutcomeNoMatch` | Provider returned no match |
| `FollowupOutcomeCaptureError` | PCM socket read failed |
| `FollowupOutcomeRecognitionError` | Provider returned an error |
| `FollowupOutcomeDiscarded` | Source changed during capture/recognition (result discarded) |
| `FollowupOutcomeSkippedCoordinator` | Skipped by source guard, input policy, or VU silence |

Linked to the play history event via `evID` (boundary event ID). Used by the telemetry
nudge system to compute false-positive rates per input.

---

## Recognition events

Every trigger fires a `RecordRecognitionEvent` call in the library:

| Trigger | Event Type |
|---------|-----------|
| VU boundary (hard/soft) | `("Trigger", "boundary")` |
| Fallback timer | `("Trigger", "fallback_timer")` |
| Shazam continuity | `("Trigger", "boundary")` (via `triggerBoundaryRecognition(false)`) |

These are independent from boundary followups and provide a complete audit trail of all
recognition attempts, including those that were skipped before capture.

---

## PCM capture (`recognizer.go`)

`captureFromPCMSocket` reads from `/tmp/oceano-pcm.sock` (relayed by `oceano-source-detector`):

```
Format: S16_LE, stereo, 44100 Hz
Flow:
  1. Connect to Unix socket
  2. If skipDuration > 0: discard skipBytes (hard boundary: 2 s = 352800 bytes)
  3. Read duration × sampleRate × channels × bytesPerSample
  4. Write WAV file: oceano-rec-<nanotime>.wav in os.TempDir()
  5. Caller deletes after recognition
```

**Why not arecord?** Opening ALSA twice blocks the device. The source-detector captures once
and relays via PCM socket; the state manager reads from the socket without contention.

**Skip duration rationale**: After a needle drop, the PCM socket buffer contains ~2 s of the
previous track's audio plus stylus crackle. Discarding this ensures the recognition sample
starts cleanly on the new track.

---

## Error handling summary

| Error | Backoff | State effect |
|-------|---------|-------------|
| HTTP 429 (rate limit) | 5 min (`rateLimitBackoff`) | `backoffRateLimited = true` |
| Provider error (network, timeout) | 30 s (`errorBackoff`) | `backoffRateLimited = false` |
| No match | `NoMatchBackoff` (15 s default) | Phase → `"no_match"`, clears state on hard boundary |
| Capture error (socket) | 30 s (`errorBackoff`) | Followup: `CaptureError` |
| Source changed during capture | None (discard) | Followup: `Discarded`, no backoff |

**Boundary trigger bypass**: All backoff types except rate-limit are bypassed on boundary
triggers. Rate-limit (429) is never bypassed — the provider will reject the request anyway.

---

## Explicit provider list (mandatory verification)

If your change touches **non-empty `recognition.providers[]`**, **`recognition.merge_policy`**, loading them from `CalibrationConfigPath`, building the runtime plan from that list, or the **web / JSON types** that round-trip those keys, complete this before merge:

1. `go test ./cmd/oceano-state-manager/... -count=1 -short`
2. `go test ./cmd/oceano-state-manager -run TestBuildRecognitionPlanFromChain_matrix -count=1`
3. On a Pi with Oceano installed: `sudo ./scripts/pi-recognition-provider-smoke.sh --dry-run`, then run without `--dry-run` (script restores `config.json` on exit).

**Typical files:** `recognition_setup.go`, `recognition_config_load.go`, `recognition_providers*.go`, `config_types.go` (`RecognitionProvider*`), `cmd/oceano-web/config.go` (`Providers`, `MergePolicy`), and `internal/recognition/` when **provider `id` → implementation** mapping changes.

Agent-oriented checklist: `.cursor/skills/pi-recognition-explicit-providers-smoke/SKILL.md`. For capture → PCM → recognition end-to-end, combine with **pi-loopback-capture-sim**.

**Persistence:** `oceano-web` `POST /api/config` writes a non-empty `recognition.providers` list whenever the request would otherwise store an empty explicit list — synthesized from `recognizer_chain` and which credentials are set — so disk state matches the contract the state manager prefers.

---

## Key files

| File | Responsibility |
|------|---------------|
| `recognition_coordinator.go` | Main loop, guards, confirmation, apply, same-track restore |
| `source_vu_monitor.go` | VU socket reader, boundary detector host, source poller, continuity monitor |
| `boundary_detector.go` | VU frame classifier (silence/audio/energy-change) |
| `recognition_setup.go` | Provider instantiation, chain assembly, stats wrapping |
| `recognition_input_policy.go` | config.json policy resolution (auto/library/display_only/off) |
| `recognizer.go` | PCM capture, WAV encoding, type aliases |
| `calibration_profile.go` | Per-input calibration profiles, RMS learning, telemetry nudges |
| `rms_percentile_learner.go` | Live RMS histogram collection and autonomous threshold adjustment |
| `calibration_telemetry_nudges.go` | False-positive rate analysis → threshold deltas |
| `play_history.go` | 5 s poll → SQLite play records, Physical placeholder handling |
| `boundary_followup.go` | Links boundary events to followup rows for telemetry |
| `stats_recognizer.go` | Per-provider call counting (chain vs continuity tracked separately) |
| `track_helpers.go` | Track equivalence, restore threshold, seek recovery helpers |
| `internal/recognition/types.go` | `Result`, `Recognizer` interface, `ErrRateLimit` |
| `internal/recognition/chain.go` | `ChainRecognizer` — ordered provider try |
| `internal/recognition/acrcloud.go` | ACRCloud HTTPS client |
| `internal/recognition/shazam.go` | Shazam Python daemon client |
