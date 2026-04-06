# Automatic Amplifier Power Management & Kiosk Source Control

**Type**: Feature
**Category**: Hardware Integration / UX
**Scope**: v2 — requires Milestone 5 (Broadlink RM4 Mini) to be complete
**Depends on**: `AMPLIFIER_CONTROL_REVISED.md` implementation (Milestones 1–4 merged)

---

## Problem

After the Broadlink IR control is in place, two things are still manual:

1. **Amplifier wake-up**: The Magnat MR 780 enters standby automatically after ~20 minutes
   of silence. When AirPlay starts or a physical source is selected, the user must manually
   press the amp's power button to wake it. This is friction — especially when starting
   AirPlay remotely.

2. **Source/input switching**: The kiosk display (nowplaying.html) shows what is playing but
   gives no way to change the amplifier input. The user must either use the physical knob or
   open the web UI on another device.

---

## Context: What We Already Know

### Standby detection via REC OUT silence

The Magnat MR 780 enters standby after ~20 minutes of inactivity. When in standby, the
amplifier stops outputting signal on the REC OUT jack — the USB capture card records
**absolute silence** (RMS ≈ 0).

The `oceano-source-detector` already computes RMS from the REC OUT continuously and writes
`source = "None"` when the signal is below the silence threshold. Prolonged `source = "None"`
is therefore a reliable proxy for standby.

This solves the **cold-start problem**: instead of trusting the in-memory `powerOn` flag
(which resets on server restart), the system can infer standby from the silence duration
tracked in the state file — which persists independently.

### AirPlay does not appear on REC OUT

AirPlay audio goes directly from the Raspberry Pi to the USB DAC input on the amplifier. It
does **not** appear on the REC OUT capture card. This is expected: REC OUT reflects physical
sources (vinyl, CD) only. AirPlay detection happens via the shairport-sync metadata pipe.

### Default input

The amplifier default input is **USB Audio** (AirPlay). Physical sources (vinyl, CD) are
on PHONO. The user does not want auto-switching between physical inputs — that stays manual
or is handled by a future calibration feature (Vinyl vs CD detection).

---

## Proposed Solution

### 1 — Standby Inference via Idle Timer

Track the duration of continuous `source = "None"` in `oceano-web`. After a configurable
threshold (default: **20 minutes**, matching the Magnat's standby timeout), mark the
amplifier as `powerOn = false` in the in-memory state.

```
REC OUT silence  ─── continuous ──→  > 20 min  ─→  assume standby
                                                       amp.powerOn = false
```

This timer resets any time `source` transitions away from `"None"` (i.e., audio is detected
on REC OUT) or when the amplifier is explicitly controlled via the API.

**Why not use the RMS value directly?**
The source file (`/tmp/oceano-source.json`) is already written by `oceano-source-detector`
with `source = "None" | "Physical"`. No changes to the detector are needed. The web server
watches this file already (for SSE); the standby timer runs alongside that watcher.

### 2 — Auto-Wake on Source Transition

When `oceano-web` detects a source transition that requires the amplifier:

| Event | Condition | Action |
|---|---|---|
| `source` → `"AirPlay"` | `amp.powerOn == false` | `PowerOn()` → wait warm-up → `SetInput(airplay_input)` |
| `source` → `"AirPlay"` | `amp.powerOn == true` AND `currentInput != airplay_input` | `SetInput(airplay_input)` |
| `source` → `"Physical"` | `amp.powerOn == false` | `PowerOn()` → wait warm-up → `SetInput(physical_input)` |
| `source` → `"Physical"` | `amp.powerOn == true` AND `currentInput != physical_input` | `SetInput(physical_input)` |
| `source` → `"None"` for > standby timeout | — | Mark `powerOn = false` (no IR command — amp went to standby on its own) |

**Warm-up delay note**: The Magnat MR 780 takes ~30 seconds after `PowerOn()` before audio
is available (`AudioReady() = true`). AirPlay audio may start before the amp is ready. This
is acceptable — the first seconds of audio will be missed, but playback continues normally
once the amp warms up. A future enhancement could buffer the first audio frames if
shairport-sync supports it.

### 3 — Kiosk Source Control (nowplaying.html)

Add a source/input switcher to the kiosk display. This appears as a row of tappable buttons
at the bottom of the now-playing screen, visible only when `amplifier.enabled = true` in
config.

**UI design**:
```
[ ⏻ USB / AirPlay ]   [ PHONO ]   [ CD ]   [ AUX ]
```

- Each button sends `POST /api/amplifier/input` with the corresponding input ID.
- The active input is highlighted.
- If `amp.powerOn == false`, tapping any input button first wakes the amp (`PowerOn`) then
  sets the input — same flow as the auto-switch above.
- Polling `/api/amplifier/state` every 5 s keeps the highlighted button in sync.

The USB/AirPlay button is the default and is always shown first.

---

## Configuration

New `auto_switch` sub-section under `amplifier` in `/etc/oceano/config.json`:

```json
"amplifier": {
  "auto_switch": {
    "enabled": true,
    "airplay_input": "USB",
    "physical_input": "PHONO",
    "standby_timeout_minutes": 20
  }
}
```

| Field | Default | Description |
|---|---|---|
| `enabled` | `false` | Master toggle. When false, no automatic switching occurs. |
| `airplay_input` | `"USB"` | Input ID to select when AirPlay starts. |
| `physical_input` | `"PHONO"` | Input ID to select when a physical source is detected. |
| `standby_timeout_minutes` | `20` | Minutes of `source = "None"` before assuming standby. Must match the amp's own standby timer. |

The web UI exposes these fields in the Amplifier config section of the settings drawer.

---

## Implementation Plan

### Changes to existing code

| File | Change |
|---|---|
| `cmd/oceano-web/config.go` | Add `AutoSwitchConfig` struct; embed in `AmplifierConfig` |
| `cmd/oceano-web/amplifier_api.go` | Add `watchSourceForAutoSwitch(stateFile, amp, cfg)` goroutine |
| `cmd/oceano-web/main.go` | Start the watcher goroutine on startup if `auto_switch.enabled` |
| `cmd/oceano-web/static/index.html` | Add `auto_switch` fields to Amplifier config section |
| `cmd/oceano-web/static/nowplaying.html` | Add input switcher button row; poll `/api/amplifier/state` |

### New goroutine: `watchSourceForAutoSwitch`

Runs in `oceano-web`. Watches `/tmp/oceano-state.json` (already polled for SSE) for source
changes. Maintains a `noneStart time.Time` to track how long `source = "None"` has been
continuous.

```
loop (500 ms poll):
  read source from state file
  if source == "None":
    if noneStart.IsZero(): noneStart = now
    if now - noneStart > standbyTimeout: amp.markStandby()  // no IR sent
  else:
    noneStart = zero
    if source changed since last tick:
      handleSourceTransition(source, amp, cfg)
```

### No changes to oceano-source-detector or oceano-state-manager

The feature is implemented entirely in `oceano-web`. The source detector and state manager
continue to operate as-is.

---

## Open Questions

### Q1: Amp already on at server start

**Decision: persist assumed state to disk.**

On each state change (`powerOn`, `currentInput`), write `/tmp/oceano-amp-state.json`
atomically (tmp → rename, same pattern as the main state file). On startup, load that file
if it exists to restore the last known assumed state.

This means a server restart mid-session does not lose the assumed state. The standby timer
still corrects the state after 20 minutes of silence — so even if the persisted state is
stale (e.g. amp was manually switched off), the system self-corrects within one standby
cycle.

### Q2: Physical input auto-switch: PHONO vs CD

The `physical_input` config is a single fixed value. Vinyl and CD both show as `source =
"Physical"`. Auto-detecting which one is playing (to switch to PHONO vs CD input
respectively) requires the Vinyl/CD calibration feature (future, separate spec).

For now: `physical_input` defaults to `"PHONO"`. User sets it to whichever physical input
they use most.

### Q3: Auto power-off

**Decision: no explicit `PowerOff` command.**

The Magnat handles its own standby timer. Sending `PowerOff` via IR would be redundant and
could interfere with the tube warm-up cycle. `standby_timeout_minutes` is used solely to
update our assumed state — it never triggers an IR command.

### Q4: Kiosk touch targets

The 7" 1024×600 display has limited vertical space. The input switcher row must not overlap
the track info or artwork. Design constraint: max 4 buttons, each min 60 px tall for reliable
touch input. If more than 4 inputs are configured, show only the first 4 or use a compact
carousel.

---

## Acceptance Criteria

### Must Have

- [ ] `auto_switch` config section parsed and exposed in web UI
- [ ] `watchSourceForAutoSwitch` goroutine starts when `enabled = true`
- [ ] AirPlay start → amp wakes (if standby) + input set to `airplay_input`
- [ ] Physical source start → amp wakes (if standby) + input set to `physical_input`
- [ ] 20 min of `source = "None"` → `powerOn` marked false (no IR sent)
- [ ] Kiosk (nowplaying.html) shows input switcher row when `amplifier.enabled = true`
- [ ] Active input highlighted in kiosk
- [ ] Tapping an input in kiosk: wakes amp if needed + sets input
- [ ] Unit tests for source-transition logic (mock amp + mock state file)

### Out of Scope

- [ ] Vinyl vs CD auto-detection (requires calibration data)
- [ ] Explicit `PowerOff` on idle (opt-in, future)
- [ ] Audio buffering during warm-up (shairport-sync limitation)
- [ ] More than one `physical_input` mapping

---

## Related

- **Depends on**: [AMPLIFIER_CONTROL_REVISED.md](AMPLIFIER_CONTROL_REVISED.md) — Milestones 1–5 complete
- **Blocked by**: Broadlink RM4 Mini hardware (for end-to-end testing)
- **Related**: Vinyl vs CD auto-detection (future spec)
