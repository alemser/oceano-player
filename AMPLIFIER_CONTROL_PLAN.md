# Amplifier Control — Implementation Plan

> **Spec reference:** `AMPLIFIER_CONTROL_REVISED.md`  
> **Status:** Planning  
> **Last updated:** 2026-04-06

---

## Code Layout

```
internal/
  amplifier/
    interfaces.go          # RemoteDevice, Amplifier, CDPlayer, Input, ErrNotSupported
    broadlink_client.go    # BroadlinkClient interface + MockClient + real subprocess client
    magnat_amplifier.go    # MagnatAmplifier: implements Amplifier; MR 780 IR codes, inputs, warm-up/switch timers
    yamaha_remote.go       # YamahaRemoteDevice: implements RemoteDevice; CD-S300 IR codes

cmd/oceano-web/
  amplifier_api.go         # REST handlers: /api/amplifier/* and /api/cdplayer/*
  config.go                # AmplifierConfig + CDPlayerConfig added to Config struct
  static/index.html        # Amplifier config panel (added to existing page)
  static/pair.html         # Dedicated pairing wizard page (new file)

scripts/
  broadlink_bridge.py      # Python subprocess bridge for Broadlink SDK (Milestone 5)
```

**Design principle:** each device is its own file/struct. `MagnatAmplifier` and `YamahaRemoteDevice`
both hold a `BroadlinkClient` internally. Adding a new device = new file, no existing code touched.

---

## Milestones

### Milestone 1 — Interfaces & Config
*No hardware required. Pure Go types; no side effects.*

- [ ] Define `RemoteDevice`, `Amplifier`, `CDPlayer` interfaces in `internal/amplifier/interfaces.go`
- [ ] Define `Input` type and `ErrNotSupported` error constant
- [ ] Add `AmplifierConfig` and `CDPlayerConfig` to `Config` struct in `cmd/oceano-web/config.go`
  - `AmplifierConfig` must include `IRCodes map[string]string` (keyed by command name, e.g. `"power_on"`, `"volume_up"`, `"input_usb"`) — codes are learned via RM4 Mini and stored here; not hardcoded in device structs
  - `CDPlayerConfig` same pattern: `IRCodes map[string]string`
- [ ] JSON serialization/deserialization round-trip works
- [ ] Unit tests for config parsing

**Done when:** `go build ./...` passes; config schema reads and writes correctly.

> **Note on IR codes:** The actual IR learning workflow (endpoints + UI) is deferred to Milestone 5,
> when the Broadlink RM4 Mini is available. The `IRCodes` field is defined now so the config schema
> doesn't need retrofitting later. Device structs load codes from config — never hardcoded constants.

---

### Milestone 2 — State Machine & Mock Adapter
*No hardware required. Pure state logic; no network calls.*

- [ ] `BroadlinkClient` interface in `broadlink_client.go` (single method: `SendIRCode(code string) error`)
- [ ] `MockBroadlinkClient` in same file — returns `nil` unconditionally
- [ ] `MagnatAmplifier` in `magnat_amplifier.go`:
  - Fields: `powerOn bool`, `currentInput Input`, `audioReady bool`, internal timers
  - `PowerOn()` → sets `audioReady = false`, starts 30 s goroutine → sets `audioReady = true`
  - `SetInput(id)` → validates against `InputList()`, sets `audioReady = false`, starts 2 s goroutine
  - `NextInput()` → cycles `InputList()` in order
  - `AudioReady()` → returns current flag
  - `CurrentState()` → returns `powerOn`
  - IR codes for Magnat MR 780 (placeholder constants until hardware confirmed)
- [ ] `YamahaRemoteDevice` in `yamaha_remote.go`:
  - Implements `Play`, `Pause`, `Stop`, `Next`, `Previous`
  - Query methods (`CurrentTrack`, etc.) return `0, ErrNotSupported`
  - IR codes for Yamaha CD-S300 (placeholder constants until hardware confirmed)
- [ ] Unit tests:
  - Power on → wait → `AudioReady()` becomes true
  - `SetInput` valid ID → delay → ready
  - `SetInput` invalid ID → `ErrNotSupported` (or appropriate error)
  - `NextInput` wraps around correctly

**Done when:** all unit tests pass; no network or subprocess calls anywhere.

---

### Milestone 3 — REST API
*No hardware required. Uses mock adapter from Milestone 2.*

- [ ] `amplifier_api.go` in `cmd/oceano-web/`:
  - `GET  /api/amplifier/state` — full state JSON (see spec for schema)
  - `POST /api/amplifier/power` — `{"action": "on"|"off"}`
  - `POST /api/amplifier/volume` — `{"direction": "up"|"down"}`
  - `POST /api/amplifier/input` — `{"id": "USB"|"PHONO"|...}`
  - `POST /api/amplifier/next-input`
  - `GET  /api/cdplayer/state`
  - `POST /api/cdplayer/transport` — `{"action": "play"|"pause"|"stop"|"next"|"prev"}`
  - `POST /api/amplifier/pair-start` — stubbed (returns fake pairing_id)
  - `GET  /api/amplifier/pair-status` — stubbed (returns "success" after first poll)
  - `POST /api/amplifier/pair-complete` — stubbed (writes token to config)
- [ ] Wire handlers into `main.go` (behind `amplifier.enabled` config flag)
- [ ] Return `404` when amplifier not configured/enabled
- [ ] Integration tests using `httptest` + mock adapter

**Done when:** all endpoints testable via `curl`; integration tests pass.

---

### Milestone 4 — Web UI
*No hardware required. Needs Milestone 3 API.*

- [ ] Amplifier config panel in `index.html`:
  - Enable/disable toggle
  - Broadlink device IP + port fields
  - Inputs list (label + ID pairs, editable)
  - Warm-up seconds and input switch delay fields
  - "Open Pairing Wizard" button
- [ ] Amplifier control widget in `index.html` (or sidebar):
  - Power On / Power Off buttons
  - Volume + / Volume − buttons
  - Input selector dropdown
  - Status line: current input, power state, audio ready indicator
- [ ] `pair.html` — dedicated pairing wizard:
  - Step 1: IP + PIN entry, "Start Pairing" button
  - Step 2: spinner + polling `pair-status` every 500 ms
  - Step 3: success (token display + "Done") or failure (error message + "Retry")

**Done when:** full UI flow works end-to-end against mock API.

---

### Milestone 5 — Real Broadlink SDK
*Requires RM4 Mini hardware.*

- [ ] `broadlink_bridge.py` Python script:
  - Commands via stdin (JSON lines), responses via stdout (JSON lines)
  - Supports: `pair`, `send_ir_code`, `get_device_info`
  - Uses `python-broadlink` package
- [ ] `RealBroadlinkClient` in `broadlink_client.go`:
  - Spawns `broadlink_bridge.py` subprocess on first use
  - Sends/receives JSON lines
  - Falls back gracefully (logs error, returns 503) if Python not available
- [ ] Replace `MockBroadlinkClient` with `RealBroadlinkClient` when config has valid pairing token
- [ ] Real pairing flow: `pair-start` → handshake → token → `pair-complete` writes config
- [ ] Confirm/update IR codes for Magnat MR 780 and Yamaha CD-S300
  - Check Broadlink's built-in IR database first (via Broadlink app after pairing)
  - Magnat MR 780: likely not in database (niche brand) → IR learning required
  - Yamaha CD-S300: likely in database → verify codes work before falling back to learning
- [ ] IR learning workflow (design deferred until hardware is available):
  - `broadlink_bridge.py` must expose `enter_learning_mode` + `get_learned_code` commands
  - New endpoints `POST /api/amplifier/learn-start` and `GET /api/amplifier/learn-status`
  - Web UI section "Learn IR Codes" with one "Learn" button per command
  - Learned codes written to `ir_codes` in `config.json`

**Done when:** power on/off and input switch commands execute on real amplifier.

---

### Milestone 6 — Hardware Validation & Documentation
*Requires RM4 Mini + Magnat MR 780 + Yamaha CD-S300.*

- [ ] Verify actual warm-up time (adjust default from 30 s if needed)
- [ ] Verify actual input switch delay (adjust default from 2 s if needed)
- [ ] Test all REST endpoints against real hardware
- [ ] Confirm Magnat input cycling behavior (no direct-set via IR; must cycle from known state)
- [ ] Update `README.md`: hardware prerequisites, network setup, pairing instructions
- [ ] Update `CLAUDE.md`: architecture section, deployment notes
- [ ] Update install scripts if new service or binary is added

**Done when:** real hardware test checklist in spec acceptance criteria all green.

---

## Hardware Dependency Summary

| Milestone | Needs RM4 Mini | Can start now |
|-----------|---------------|---------------|
| 1 — Interfaces & Config | No | Yes |
| 2 — State Machine & Mock | No | Yes |
| 3 — REST API | No | Yes |
| 4 — Web UI | No | Yes |
| 5 — Broadlink SDK | Yes | No |
| 6 — Hardware Validation | Yes | No |

---

## Open Questions (from spec)

1. **IR database coverage** — Are Magnat MR 780 and Yamaha CD-S300 in Broadlink's pre-programmed database? If not, manual IR learning workflow needed.
2. **Magnat input cycling** — No direct-set via IR remote; must cycle from a known starting input. Need a calibration step or "reset to default input" command at startup.
3. **Auto-input switching** — Out of scope for v1; tracked as separate v2+ feature.
4. **Volume state** — IR-only, no feedback. v1 provides +/− buttons only; v2+ can add heuristic tracking.
5. **CD track/time queries** — Yamaha CD-S300 may not support IR queries; fields return `null` if unsupported.
