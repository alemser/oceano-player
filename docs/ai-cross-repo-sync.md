# AI Cross-Repo Sync Checklist

Use this file whenever backend changes are made in `oceano-player`.

The iOS app (`oceano-player-ios`) is a direct consumer of backend endpoints and config semantics. Backend work is not complete until cross-repo impact is reviewed.

## When this checklist is mandatory

Run this checklist for any change touching:

- HTTP APIs (`cmd/oceano-web` handlers, routes, status codes, payload fields)
- playback/state semantics (`/tmp/oceano-state.json`, SSE event meaning, source priority)
- config schema/defaults (`/etc/oceano/config.json`, save/migration behavior)
- stylus, history/insights, recognition, amplifier/topology, IR payloads
- install/setup flows that change runtime behavior seen by clients

## Required steps

1. **Contract diff**
   - List all changed endpoints/fields/semantics.
   - Mark each item as `compatible`, `additive`, or `breaking`.

2. **Compatibility policy**
   - Prefer additive changes.
   - For removals/renames, keep compatibility aliases when possible.
   - If breaking behavior is unavoidable, document migration notes clearly.

3. **Documentation sync (same PR/commit)**
   - Update `README.md` sections that expose user-visible behavior.
   - Update `CLAUDE.md` if architecture/workflow assumptions changed.
   - Update any endpoint-specific docs/comments that became stale.

4. **iOS impact note**
   - Produce a short "iOS follow-up" note with:
     - affected screens/modules (e.g. Amplifier, Stylus, Insights, Config)
     - required app-side updates
     - risk level (`low` / `medium` / `high`)

5. **Verification**
   - Run backend tests (`go test ./...` or `make test`).
   - Validate changed endpoints manually (or with existing tests).
   - Confirm no accidental contract drift remains.

## Suggested iOS impact template

```md
## iOS follow-up (oceano-player-ios)

- Scope:
  - [ ] API payload changes:
  - [ ] Config schema/default changes:
  - [ ] Playback/state semantic changes:

- Affected app modules:
  - [ ] Now Playing
  - [ ] Config
  - [ ] Amplifier
  - [ ] Stylus
  - [ ] Insights
  - [ ] Other:

- Actions required:
  - 1)
  - 2)

- Risk:
  - [ ] low
  - [ ] medium
  - [ ] high
```

## Rule of thumb for agents

If you changed backend behavior and did not explicitly evaluate iOS impact, the task is incomplete.

---

## Log: 2026-04-30 — amplifier cycle navigation + config

**Backend (`main`, merge of `fix/unify-amplifier-cycle-navigation`)**

| Item | Type | Notes |
|------|------|--------|
| `POST /api/amplifier/next-input`, `POST /api/amplifier/prev-input` | **Semantic** | Single IR pulse per request (arming model); client must mirror “active selection window” before advancing local input index. |
| `POST /api/amplifier/select-input` body | **Additive** | Optional `target_input_id`, `current_input_id` (with `steps`) for shortest-path + server-side `last_known_input_id` persistence. |
| `amplifier.cycle_arming_settle_ms`, `cycle_step_next_wait_ms`, `cycle_step_prev_wait_ms` | **Additive** | Cycle-mode pacing; defaults in code + MR780 built-in profile values. |
| VU hard boundary duration guard bypass | **Semantic** | Physical recognition only; iOS consumes `/api/stream` state as before. |
| `recognition.detail`, `active_input_id`, `active_input_name` | **Additive** | Physical recognition status for kiosk/apps; terminal `off` / `no_match` take precedence over stale `recognizerBusyUntil` in state projection. |

**iOS (`oceano-player-ios`) — done in repo**

- [x] `AmplifierClient`: `nextInput` / `prevInput` only update index + `last-known-input` when selection was already active (1200 ms window, same as web `runtime.js`).
- [x] `AmplifierClient`: `selectInput` sends `target_input_id` + `current_input_id` with `steps`.
- [x] `PlayerState.Recognition`: optional `detail`, `active_input_id`, `active_input_name`.
- [x] Now Playing + Physical media setup: titles/subtitles reflect `off` / `no_match` / `identifying` + input pill when name is present.

**iOS follow-up (optional)**

- [ ] Config UI: expose cycle timing fields next to amplifier profile (parity with future web editor).
- [ ] Now Playing: clearer UX when recognition is skipped (e.g. input policy `off`) — separate UX task.

---

## Log: 2026-05-01 — AirPlay DACP transport backend (phase 2 backend)

**Backend (`feat/airplay-dacp-transport-phase1`)**

| Item | Type | Notes |
|------|------|--------|
| `GET /api/airplay/transport-capabilities` | **Semantic** | Now uses live DACP context from shairport metadata pipe (`acre`, `daid`, `clip`) in `oceano-web`; returns deterministic readiness (`ready`, `no_airplay_session`, `missing_dacp_context`, `session_stale`). |
| `POST /api/airplay/transport` | **Additive** | New endpoint with `{ "action": "play|pause|next|previous" }`; validates action and sends DACP request to AirPlay sender (`/ctrl-int/1/...`, `Active-Remote` header). |
| DACP command reliability | **Semantic** | 2-second timeout + bounded retry for transient network/timeout failures + per-action rate limiting (HTTP 429). Machine-readable failure reasons: `invalid_action`, `missing_dacp_context`, `session_stale`, `network_unreachable`, `dacp_error`, `rate_limited`. |
| AirPlay readiness observability | **Additive** | `oceano-web` now emits structured readiness transition logs (`event=readiness_transition`, `from`, `to`, `reason`, `available`) to simplify live diagnosis on Pi logs. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] Add transport controls in Now Playing when `source == AirPlay`.
- [ ] Gate enabled state by `GET /api/airplay/transport-capabilities`.
- [ ] Call `POST /api/airplay/transport` and surface failures non-blocking.

**Risk**

- [x] medium
