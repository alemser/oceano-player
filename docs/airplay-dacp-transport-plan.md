# AirPlay DACP Transport Control Plan

## Scope

Implement AirPlay transport controls (`play`, `pause`, `next`, `previous`) for Oceano Player using DACP data exposed by `shairport-sync` metadata/session context.

This plan covers backend-only work in this repository and explicit API contracts for downstream clients (including `oceano-player-ios`).

---

## Goals

- Add reliable AirPlay transport control when an active AirPlay session is present.
- Keep behavior safe and predictable when DACP data is missing, stale, or unreachable.
- Expose explicit capability/readiness so clients can avoid blind command attempts.
- Preserve existing playback/state semantics when no DACP session is available.

## Non-goals (for this phase)

- No UI redesign in this repository.
- No Bluetooth transport work in this plan.
- No new pairing UX beyond what AirPlay/shairport already provides.

---

## Current State (Baseline)

- AirPlay metadata is consumed from shairport-sync pipe for now playing state.
- There is no backend endpoint that sends transport commands to AirPlay controller.
- Clients cannot currently issue remote commands for AirPlay through Oceano backend.

---

## Architecture Overview

1. **Session Discovery Layer**
   - Extract and cache DACP session identifiers from shairport-sync metadata/session signals.
   - Track readiness lifecycle (`available`, `stale`, `unavailable`) with TTL.

2. **DACP Client Layer**
   - Send HTTP requests to DACP command paths for `play`, `pause`, `nextitem`, `previtem`.
   - Include required session headers/tokens (for example Active-Remote style values).
   - Apply strict timeouts and bounded retries.

3. **Web API Layer (`cmd/oceano-web`)**
   - Add transport endpoint:
     - `POST /api/airplay/transport` with `{ "action": "play|pause|next|previous" }`
   - Add capabilities endpoint:
     - `GET /api/airplay/transport-capabilities`
   - Return explicit status and machine-readable error reasons.

4. **State/Capability Exposure**
   - Include transport readiness in capabilities endpoint (and optionally status endpoint if needed).
   - Keep state output backward-compatible for existing consumers.

---

## Proposed API Contract

## `GET /api/airplay/transport-capabilities`

Example response:

```json
{
  "available": true,
  "session_state": "ready",
  "supported_actions": ["play", "pause", "next", "previous"],
  "reason": ""
}
```

`session_state` values:
- `ready`
- `no_airplay_session`
- `missing_dacp_context`
- `session_stale`
- `network_unreachable`

## `POST /api/airplay/transport`

Request:

```json
{ "action": "pause" }
```

Success response:

```json
{ "ok": true }
```

Error response (example):

```json
{
  "ok": false,
  "error": "airplay session is not ready",
  "reason": "missing_dacp_context"
}
```

Compatibility classification:
- **Additive** (new endpoints only).
- No breaking change to existing endpoints.

---

## Implementation Phases

## Phase 1: Capability & Session Tracking (No Commands Yet)

Deliverables:
- Internal DACP session cache with TTL and freshness checks.
- `GET /api/airplay/transport-capabilities`.
- Structured logs for session readiness transitions.

Acceptance:
- Capabilities endpoint reports stable readiness on active AirPlay sessions.
- No regressions in existing AirPlay metadata/state.

## Phase 2: Command Execution

Deliverables:
- DACP command client.
- `POST /api/airplay/transport`.
- Action validation + clear error reasons.

Acceptance:
- Commands work reliably during active session.
- Proper errors when session becomes stale/unavailable.

## Phase 3: Hardening

Deliverables:
- Retry policy (bounded, idempotent-safe).
- Rate limiting/debounce against command spam.
- Expanded metrics/logging for latency/failure class.

Acceptance:
- No request storms under repeated UI taps.
- Stable behavior under transient network failures.

---

## Reliability Rules

- Command timeout: short and strict (for example 1-2 seconds per attempt).
- At most one retry for transient network failures.
- Never block core state writer or metadata loops.
- Keep DACP context in memory only; refresh opportunistically from metadata events.

---

## Security/Privacy Notes

- Never log raw auth/session tokens.
- Redact sensitive headers from error logs.
- Reject transport commands when no validated active session exists.

---

## Testing Plan

## Unit Tests

- Session parser/cache:
  - valid context, missing fields, stale TTL.
- Capability endpoint:
  - each `session_state` value.
- Transport endpoint:
  - valid action, invalid action, unavailable session, DACP error mapping.

## Integration/Manual

- Start AirPlay stream from iPhone/Mac; verify `available=true`.
- Issue each action and confirm remote device behavior.
- Stop stream; verify capabilities transition to unavailable.
- Simulate stale context and confirm safe error responses.

---

## Rollout Plan

1. Merge Phase 1 first (readiness only).
2. Validate on Raspberry Pi with real AirPlay sessions.
3. Merge Phase 2 behind capability-aware clients.
4. Enable UI-side controls only when `available=true`.
5. Observe logs/metrics, then add Phase 3 hardening.

---

## iOS Follow-up (tracked, not implemented here)

- Consume `GET /api/airplay/transport-capabilities`.
- Show/enable AirPlay transport controls only when ready.
- Map backend `reason` codes to user-friendly messages.

Risk level: **medium** (network/session lifecycle complexity).

---

## iOS UX Mini-spec (Now Playing)

This section defines the product behavior requested for iOS Now Playing:
1) AirPlay transport controls in-app (when available), and
2) amplifier input alignment prompt for AirPlay sessions.

## A) AirPlay transport controls on iOS Now Playing

Display rules:
- Render transport controls only when `source == AirPlay`.
- Enable controls only when `GET /api/airplay/transport-capabilities` returns:
  - `available = true`
  - `session_state = "ready"`
- If `source == AirPlay` but capabilities are not ready, show disabled controls with helper copy:
  - `"AirPlay remote control unavailable for this session."`

Behavior:
- Button taps call `POST /api/airplay/transport`.
- On backend `ok=false`, show non-blocking toast/snackbar with mapped reason.
- Do not optimistically mutate playback state; rely on stream/state updates.

## B) AirPlay -> amplifier input alignment prompt

Goal:
- Ensure amplifier is on the correct USB input when streaming is via AirPlay.
- Never power on amplifier automatically in this flow.

Trigger conditions (all must be true):
- `source == AirPlay`
- Amplifier power state is `on` or `warming_up` (explicitly skip when `off`/`standby`/`unknown`)
- Amplifier input model is available
- `last_known_input_id` is known and differs from configured USB input

Prompt copy (example):
- Title: `"Switch amplifier to USB Audio?"`
- Body: `"AirPlay is active, but amplifier input is <current>. Switch to USB Audio now?"`
- Actions: `Switch now` / `Not now`

Action mapping:
- `Switch now` -> call existing amplifier input select flow (`select-input` target USB)
- `Not now` -> dismiss and apply cooldown

Debounce / anti-spam:
- Do not show more than once per AirPlay session unless:
  - user changed input again, or
  - a configurable cooldown elapsed (suggested: 10 minutes)
- If user chooses `Not now`, suppress prompt for current session cooldown window.

Safety checks before showing prompt:
- If USB DAC is not present, do not offer forced switch.
- Instead show passive notice:
  - `"USB DAC not detected. Check DAC connection to use AirPlay through USB input."`

## C) Suggested readiness endpoint extension (optional)

To simplify client logic, consider extending capabilities payload with:

```json
{
  "available": true,
  "session_state": "ready",
  "supported_actions": ["play", "pause", "next", "previous"],
  "reason": "",
  "amp_alignment": {
    "eligible": true,
    "recommended_switch": true,
    "current_input_id": "30",
    "current_input_label": "CD",
    "target_input_id": "40",
    "target_input_label": "USB Audio",
    "usb_dac_present": true
  }
}
```

This keeps iOS decision logic deterministic and avoids duplicating backend inference.

## D) Rollout sequence for UX pieces

1. Ship DACP capability + transport endpoints first.
2. Add iOS transport controls gated by capabilities.
3. Add amplifier alignment prompt (cooldown + safety checks).
4. Observe user behavior, then tune prompt frequency/copy.

---

## Execution Checklist (P1/P2/P3)

Use this as the implementation tracker.

## P1 — Backend readiness (capabilities only)

**Backend (`oceano-player`)**
- [ ] Add DACP session cache module (in-memory, TTL, freshness state).
- [ ] Parse and store DACP session context from shairport metadata/session events.
- [ ] Add `GET /api/airplay/transport-capabilities` endpoint.
- [ ] Return stable `session_state` (`ready`, `no_airplay_session`, `missing_dacp_context`, `session_stale`, `network_unreachable`).
- [ ] Add structured logs for session readiness transitions.

**Tests**
- [ ] Unit tests: valid DACP context parsing.
- [ ] Unit tests: missing/invalid context handling.
- [ ] Endpoint tests: all `session_state` variants.

**Acceptance**
- [ ] During active AirPlay session, endpoint reports `available=true`.
- [ ] Without session, endpoint reports deterministic unavailable reason.
- [ ] No regressions in existing `/api/status` and stream behavior.

---

## P2 — Backend transport commands + iOS controls

**Backend (`oceano-player`)**
- [ ] Implement DACP client (play/pause/next/previous mappings).
- [ ] Add `POST /api/airplay/transport` with action validation.
- [ ] Return machine-readable `reason` on failure (`session_stale`, `missing_dacp_context`, etc.).
- [ ] Add timeout + bounded retry policy (safe defaults).

**iOS (`oceano-player-ios`)**
- [ ] Add AirPlay transport section in Now Playing.
- [ ] Show controls only when `source == AirPlay`.
- [ ] Enable controls only when capabilities endpoint says `available=true`.
- [ ] Show passive helper text when controls are unavailable.
- [ ] Handle backend errors with non-blocking feedback (toast/banner).

**Tests**
- [ ] Backend endpoint tests for valid/invalid actions.
- [ ] Backend tests for unavailable-session error mapping.
- [ ] iOS manual QA: controls appear/disappear correctly by source/capability state.

**Acceptance**
- [ ] Play/pause/next/previous work in active AirPlay sessions.
- [ ] No app crash or stuck state when DACP is unavailable.
- [ ] Existing Bluetooth/physical playback UX unchanged.

---

## P3 — Input alignment UX (AirPlay -> USB)

**Backend (`oceano-player`)**
- [ ] Expose/derive amplifier alignment eligibility (either via new `amp_alignment` in capabilities or existing endpoints composition).
- [ ] Include DAC present signal for safe prompt decisions.

**iOS (`oceano-player-ios`)**
- [ ] Add “Switch to USB Audio?” prompt when trigger conditions are met.
- [ ] Implement `Switch now` action via amplifier input select endpoint.
- [ ] Implement `Not now` cooldown suppression (session-scoped + timed cooldown).
- [ ] Add passive warning when USB DAC not detected (no forced switch CTA).

**Tests**
- [ ] Manual QA matrix:
  - [ ] AirPlay active + amp on + wrong input -> prompt appears once
  - [ ] User selects Not now -> suppressed during cooldown
  - [ ] USB DAC missing -> warning only, no forced switch
  - [ ] Correct input already selected -> no prompt

**Acceptance**
- [ ] Prompt is helpful, not spammy.
- [ ] Amp is reliably aligned to USB when user accepts.
- [ ] No power-on command is sent by this flow.

---

## File-level Task Map

Suggested primary files for implementation:

**Backend (`oceano-player`)**
- `cmd/oceano-web/`:
  - [ ] add AirPlay transport handlers/routes
  - [ ] add capabilities composition
- `cmd/oceano-state-manager/`:
  - [ ] extend shairport metadata/session extraction if needed for DACP context
- `internal/` (new package or existing helper area):
  - [ ] DACP client + session cache abstractions

**iOS (`oceano-player-ios`)**
- `OceanoPlayer/Features/NowPlaying/NowPlayingView.swift`
- `OceanoPlayer/Networking/*` (AirPlay capabilities + transport client)
- Optional prompt state helper in app-level navigation/session state

---

## Definition of Done (overall)

- [ ] P1/P2/P3 checklists completed.
- [ ] Backend and iOS contracts documented and synchronized.
- [ ] Manual validation on real Pi + real AirPlay sender.
- [ ] Fallback paths validated (no session, stale session, DAC absent).
- [ ] Merged to `main` with rollout notes in release/change log.
