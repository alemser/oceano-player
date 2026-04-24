# Hybrid Source Detection with Adaptive Noise Floor

## Problem

The original source-detector used a single fixed RMS threshold to classify audio
as `Physical` or `None`. This broke in several ways:

- **CD transport noise**: the CD player's electronics produce a small signal even
  when no music is playing. With a low threshold this is classified as `Physical`,
  causing continuous recognition attempts that find nothing.
- **Vinyl inter-track silence**: groove noise between tracks sits just above the
  capture card's own noise floor. With a tight threshold this oscillates.
- **Quiet musical passages**: slow introductions (e.g. "Telegraph Road", "Shine On
  You Crazy Diamond") have low RMS but are real music. A fixed threshold either
  misses them or is tuned so low that it also catches transport noise.

The root cause: **RMS alone cannot distinguish constant low-level noise from music**.

## Solution: RMS + Variation (hybrid AND)

Music has two properties that constant noise lacks:

1. **Higher average level** (RMS above the noise floor)
2. **Higher variation** (the level changes significantly from moment to moment)

CD transport noise has a slightly elevated RMS but almost no variation — it is
essentially a constant hum. Vinyl groove noise between tracks is similar. Music,
even quiet music, has a constantly shifting dynamic envelope.

The hybrid detector requires **both** conditions to be true:

```go
isPhysical := rms >= rmsThreshold && rollingStdDev >= stddevThreshold
```

| Source state | RMS | Variation | Result |
|---|---|---|---|
| CD on, no music | slightly above floor | very low | None ✓ |
| CD playing music | high | high | Physical ✓ |
| Vinyl needle down, silent groove | slightly above floor | low | None ✓ |
| Vinyl playing music | high | high | Physical ✓ |
| Quiet intro (Telegraph Road) | low | low | None ✓ (recognition waits) |
| Quiet intro building up | rising | rising | Physical ✓ (caught early) |

`rollingStdDev` is the standard deviation of the last 10 window RMS values
(≈ 0.46 s at 44 100 Hz / 2048-sample windows). It measures how much the signal
level is changing right now, not whether it is loud.

## Adaptive noise floor — no calibration step

The thresholds are derived from the **noise floor**: the RMS and variation of the
capture path when no source is active. Rather than requiring the user to run a
calibration wizard, the source-detector learns the noise floor automatically.

### Learning algorithm

Every audio window (~46 ms) that is classified as `None`, the detector updates
its noise floor estimate using an exponential moving average (EMA):

```
noiseRMS    = α × windowRMS    + (1−α) × noiseRMS
noiseStdDev = α × rollingStdDev + (1−α) × noiseStdDev
```

`α = 0.005` → approximately 200 windows (≈ 9 seconds of silence) to adapt
significantly. Only `None` windows contribute — music windows are ignored, so
the estimate always tracks the silence baseline, never the music level.

Thresholds are derived from the current estimate:

```
rmsThreshold    = noiseRMS + noiseStdDev × 4.0
stddevThreshold = noiseStdDev × 3.0
```

### Why this works without user input

- **First run**: the system starts with conservative defaults
  (`noiseRMS = 0.001`, `noiseStdDev = 0.005`, giving `rmsThreshold ≈ 0.021`).
  Silence is well below this, so the first silence period immediately starts
  adapting the estimate downward toward the real noise floor.
- **Music playing at startup**: music sits well above the conservative defaults,
  so it is classified `Physical` and ignored by the learner. Adaptation begins
  as soon as the first silent window arrives.
- **After gain change**: the noise floor shifts (higher or lower). The EMA adapts
  within a few minutes of accumulated silence. No user action is needed.

### Persistence

The learned noise floor is saved to `/var/lib/oceano/noise-floor.json` every
5 minutes and on clean shutdown. On restart the file is loaded, so the detector
resumes with the last known estimate instead of starting from scratch.

```json
{
  "rms":         0.0042,
  "stddev":      0.0008,
  "updated_at":  "2026-04-24T10:15:00Z",
  "windows":     48210
}
```

If the file is absent (first install) or unreadable (corruption), the detector
falls back to the conservative defaults and re-learns.

## Relationship to VU monitor calibration

The hybrid source-detector only answers **is a physical source active right now?**
It does not detect track boundaries or silence between tracks within a physical
source — that is the job of the VU monitor in `oceano-state-manager`.

The VU monitor continues to use the per-input calibration profiles stored in
`/etc/oceano/config.json` (`advanced.calibration_profiles`) for track-boundary
detection. These profiles are set once via the noise-floor calibration wizard in
the web UI and are independent of the adaptive noise floor described here.

In practice:

| Layer | What it detects | Calibration |
|---|---|---|
| source-detector | Physical vs None | Adaptive, automatic |
| state-manager VU monitor | silence between tracks within Physical | Per-input wizard (optional) |

## Configuration flags

| Flag | Default | Description |
|---|---|---|
| `--calibration-file` | `/var/lib/oceano/noise-floor.json` | Path to persisted noise floor |
| `--stddev-threshold` | `0` (use adaptive) | Manual StdDev override; disables adaptive learning |
| `--silence-threshold` | `0` (use adaptive) | Manual RMS override; disables adaptive learning |

If both manual overrides are set, adaptive learning is disabled entirely and the
system behaves like the old fixed-threshold detector (useful for debugging).

## Web UI panel

The recognition configuration page shows a read-only **Source Detection** panel:

```
Source Detection
─────────────────────────────────────────────────
Noise floor   RMS 0.0042  ·  Variation 0.0008
Thresholds    RMS > 0.0066  ·  Variation > 0.0020
Updated       3 min ago  (48 210 windows)

The system continuously refines these values during
silence. Both the signal level and its variation must
exceed the thresholds for audio to be classified as a
physical source — this prevents CD transport noise and
vinyl groove noise from triggering recognition.
─────────────────────────────────────────────────
```

If the calibration file does not exist yet the panel shows:

```
Still learning — values will appear after the first
silent period (usually within 30 seconds of startup).
```

The panel refreshes automatically every 30 seconds. No user action is required.

## Edge cases

| Situation | Behaviour |
|---|---|
| First boot, music playing immediately | Conservative defaults classify music as Physical; learning starts on first silence |
| Gain changed while running | Thresholds adapt within a few minutes of silence |
| Calibration file corrupted | Ignored; falls back to defaults and re-learns |
| Persistent loud environment (no silence ever) | Estimate stays at defaults; conservative thresholds may cause missed detections in quiet passages — unlikely in home use |
| Manual `--silence-threshold` set | Adaptive learning disabled for RMS; fixed threshold used |

## Interaction with existing silence protection layers

The source-detector is not the only safeguard against spurious recognition. The
state-manager applies its own hysteresis on top, so brief `None` periods caused
by in-track silences do not affect the user experience:

| Gap (time source is None) | State-manager behaviour |
|---|---|
| < 277 ms | Source-detector debounce absorbs it — file never updated |
| 277 ms – 2 s | `gap < physicalResumeRecognitionGap` — nothing happens, track info preserved |
| 2 s – 10 s | `resumedAfterSilence` — recognition re-triggered in background, existing result **not** cleared; continuity check confirms same track |
| 10 s – 45 s | `resumedAfterIdle` — result cleared, fresh recognition on resume |
| > 45 s | New session — full reset |

### Specific scenarios

**In-track silence (~1 s, e.g. "Telegraph Road", "Shine On You Crazy Diamond"):**
The source-detector needs 6 consecutive None votes (≈ 277 ms) to flip state, then
6 Physical votes (≈ 277 ms) to flip back. The total None gap seen by the
state-manager is typically under 2 s → the `physicalResumeRecognitionGap` guard
fires, nothing visible to the user.

With the adaptive threshold (≈ 0.008) rather than the fixed default (0.025),
quiet musical passages with RMS 0.010–0.030 are now more likely to remain above
the threshold and stay classified as `Physical` throughout.

**Gapless albums:**
The source stays `Physical` continuously. The VU monitor in the state-manager
handles track-boundary detection within the Physical period. The source-detector
is entirely uninvolved in gapless transitions.

**Silence of 1.5–2 s within a track:**
May cross the 2-second threshold depending on poll timing. If it does,
`resumedAfterSilence` triggers a background recognition call but the displayed
result is preserved. The continuity logic confirms the same track and the display
does not change.

**The AND condition and quiet passages:**
Music with low RMS (quiet intro, diminuendo) still has significant variation —
reverb tails, subtle dynamics, bow noise on strings. The `rollingStdDev` of
window RMS values remains elevated even during pianissimo passages, so the AND
condition keeps them classified as `Physical` even when the RMS condition alone
would not.
