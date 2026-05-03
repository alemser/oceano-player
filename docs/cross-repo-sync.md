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
| `GET /api/airplay/transport-capabilities` | **Semantic** | Now reads DACP context projected by `oceano-state-manager` into `state.json` (`active_remote`, `dacp_id`, `client_ip`) so `oceano-web` does not consume shairport metadata directly. Returns deterministic readiness (`ready`, `no_airplay_session`, `missing_dacp_context`, `session_stale`, `amp_off`). |
| `POST /api/airplay/transport` | **Additive** | New endpoint with `{ "action": "play|pause|next|previous" }`; validates action and sends DACP request to AirPlay sender (`/ctrl-int/1/...`, `Active-Remote` header). |
| DACP command reliability | **Semantic** | 2-second timeout + bounded retry for transient network/timeout failures + per-action rate limiting (HTTP 429). Machine-readable failure reasons: `invalid_action`, `missing_dacp_context`, `session_stale`, `network_unreachable`, `dacp_error`, `rate_limited`. |
| AirPlay readiness observability | **Additive** | `oceano-web` now emits structured readiness transition logs (`event=readiness_transition`, `from`, `to`, `reason`, `available`) to simplify live diagnosis on Pi logs. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] Add transport controls in Now Playing when `source == AirPlay`.
- [ ] Gate enabled state by `GET /api/airplay/transport-capabilities`.
- [ ] Call `POST /api/airplay/transport` and surface failures non-blocking.

**Risk**

- [x] medium

---

## Log: 2026-05-02 — `recognition.acoustid_client_key` (additive config)

**Backend**

| Item | Type | Notes |
|------|------|-------|
| `recognition.acoustid_client_key` in `/etc/oceano/config.json` | **Additive / legacy** | Optional string; may appear in `GET/POST /api/config` payloads and `oceano-state-manager` CLI. **AcoustID is not a product provider** (short-capture model); if non-empty, state-manager logs that the key is **ignored**. See `docs/plans/recognition-flexible-providers-and-secrets.md`. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] If the app mirrors full config JSON: **ignore** or hide `acoustid_client_key` in UX (optional strip on save is OK); no AcoustID feature work.

**Risk**

- [x] low

**Amendment (same day):** AcoustID removed from the recognition roadmap; prefer **ACRCloud**, **AudD**, optional **`shazamio`**, and enrichment (MusicBrainz / TheAudioDB / Cover Art Archive).

**Removal (later):** `recognition.acoustid_client_key`, web UI field, `--acoustid-client-key`, and state-manager config field **deleted**. Existing JSON may still contain the key until the user saves config from the web UI (or edits JSON); it is ignored.

---

## Log: 2026-05-02 — Documentation: `shazamio` vs official Shazam API

**Backend**

| Item | Type | Notes |
|------|------|-------|
| Recognition docs / README | **Compatible** | Clarifies optional Python **`shazamio`** path is **not** a first-party Shazam developer API; **commercial / ToS** implications called out. No `config.json` or API shape change. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] If the app exposes recognition provider copy: use **`shazamio`** (or “optional community client”) wording where appropriate; avoid implying an official **Shazam API** partnership; align App Store / privacy disclosures with unofficial third-party access if applicable.

**Risk**

- [x] low

---

## Log: 2026-05-02 — AudD recognition (additive config + state)

**Backend**

| Item | Type | Notes |
|------|------|-------|
| `recognition.audd_api_token` | **Additive** | Optional BYOK string; Recognition Configuration page + `--audd-api-token` on `oceano-state-manager`. |
| `recognition.recognizer_chain` | **Additive** | New values: `audd_first`, `audd_only`. Existing values unchanged; `acrcloud_first` / `shazam_first` insert **AudD** between other providers when `audd_api_token` is non-empty. |
| `recognition.provider` (physical `RecognitionStatus`) | **Additive** | May be `"audd"` when the match came from AudD; treat unknown provider strings as generic “matched” if needed. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] Config: read/write `audd_api_token`; chain options `audd_first` / `audd_only`.
- [ ] UI: accept `recognition.provider === "audd"` if branching on provider.

**Risk**

- [x] low

---

## Log: 2026-05-02 — `recognition.providers[]` + `merge_policy` (explicit provider list, additive)

**Backend**

| Item | Type | Notes |
|------|------|-------|
| `recognition.providers` | **Additive** | Optional JSON array of `{ "id", "enabled", "roles", "credential_ref"? }`. Known `id` values: `acrcloud`, `audd`, `shazam`. Empty `roles` ⇒ entry skipped (per plan). When **non-empty**, `oceano-state-manager` reads ordering from **`--calibration-config`** (default `/etc/oceano/config.json`) and **does not use `--recognizer-chain`** for primary/confirmer construction. When **omitted** or empty: legacy **`recognizer_chain`** behaviour unchanged. **`oceano-web` `POST /api/config`:** if `providers` is **missing** or **`[]`**, the server **materializes** `providers` + `merge_policy: first_success` from `recognizer_chain` and credential fields before writing JSON (non-empty `providers` in the body are left as-is). |
| `recognition.merge_policy` | **Additive** | Optional string; default `first_success` when `providers` is used. Other values are logged and treated as `first_success` until extended merge_policy / coordinator work lands. |

**iOS follow-up (`oceano-player-ios`)**

- [x] Send and preserve `recognition.providers` + `merge_policy` on `POST /api/config` when the loaded config had a non-empty `providers` array (explicit provider list); otherwise omit those keys and keep legacy `recognizer_chain` + credential fields only. Cards still drive order/toggles; each enabled provider with credentials is saved as `roles: ["primary"]` (unknown provider ids in the snapshot are appended unchanged; `credential_ref` is preserved per id).
- [x] Validate at least one enabled primary with credentials before save (same rules as legacy card validation); footer copy when the explicit provider list is active.
- **Deferred (not iOS now):** Option A delegated recognition (phone-mediated API calls / **I2**) — only after `providers` + config contract are stable end-to-end.

**Risk**

- [x] low

---

## Log: 2026-05-02 — Shazam: bundled Python path + `shazam_recognizer_enabled`

**Backend**

| Item | Type | Notes |
|------|------|-------|
| `recognition.shazam_recognizer_enabled` | **Additive** | Boolean; when `true`, `oceano-web` passes `--shazam-python` with fixed path `recognition.BundledShazamioPythonBin` (`/opt/shazam-env/bin/python`). When `false`, passes an empty flag value → Shazamio client disabled. |
| `recognition.shazam_python_bin` | **Deprecated** | Ignored at runtime; cleared on save. `loadConfig` migrates to `shazam_recognizer_enabled` when that key is absent (legacy: explicit empty `shazam_python_bin` → off; key omitted → on; root `shazam_python` from older `install-shazam.sh` → on). |
| `internal/recognition.BundledShazamioPythonBin` | **Additive** | Constant matching the venv from `install-shazam.sh`. |
| `oceano-state-manager` default `--shazam-python` | **Compatible** | Default empty so the systemd `ExecStart` from the web UI is authoritative. |

**iOS follow-up (`oceano-player-ios`)**

- [x] Send `shazam_recognizer_enabled` (bool) on save; remove `shazam_python_bin` from POST; infer bool on GET when absent (aligned with web migration). No Python path field in Physical Media UI; user-facing **Shazamio** naming; JSON `id` / state `shazam` unchanged.

**Contract note (wire vs product)**

- Keep wire `id` **`shazam`** and `recognizer_chain` values **`shazam_*`** for the community Shazamio path; a future official Shazam API would use a **new** provider id (e.g. `shazam_official`), not a rename of `shazam`.
- iOS should keep a Swift property name obviously tied to **`shazam_recognizer_enabled`** (e.g. `shazamRecognizerEnabled`) unless both repos adopt explicit `CodingKeys` + docs.
- If the Pi resets or migrates SQLite / `recognition_summary` buckets, Insights counters may change without an iOS contract change unless API shapes or keys change.

**Risk**

- [x] low

---

## Log: 2026-05-03 — Embedded web configuration hub removed

**Backend (`oceano-web`)**

| Item | Type | Notes |
|------|------|-------|
| Static HTML hub | **Removed** | `index.html`, `config.html`, hub pages (`/streaming`, `/topology`, `/recognition`, …), and their JS/CSS were deleted from `cmd/oceano-web/static/`. **`GET /` redirects to `/nowplaying.html`**. |
| HTTP APIs | **Unchanged** | `GET/POST /api/config`, library/history/recognition/amplifier/stylus routes, SSE, artwork, etc. remain for **`oceano-player-ios`** and automation. |

**iOS follow-up (`oceano-player-ios`)**

- [x] Remove any in-app links or help text that pointed users at `http://<pi>:8080/` for **browser-based** configuration (except **`/nowplaying.html`** if you intentionally deep-link the HDMI preview).
- [x] Confirm first-run copy tells users to use **the app** or **`sudo oceano-setup`** for Pi-side bootstrap, not a web checklist.

**Risk**

- [x] medium for operators who relied on the browser hub; mitigated by git history and iOS / `oceano-setup` / `POST /api/config`.

---

## Log: 2026-05-03 — `recognition.providers` required; `recognizer_chain` deprecated for runtime

**Backend**

| Item | Type | Notes |
|------|------|-------|
| `recognition.providers` | **Breaking** | Physical recognition runs only when `recognition` exists in `/etc/oceano/config.json` and `providers` is a **non-empty** array that yields at least one runnable **primary** (credentials / install). Missing `recognition`, omitted `providers`, or `[]` disables recognition. |
| `recognition.recognizer_chain` | **Deprecated** | Still written by older clients / systemd flags; **ignored** when building the recognition plan. |
| `POST /api/config` materialize | **Breaking** | No longer synthesizes `providers` from `recognizer_chain`; empty/absent list stays empty (merge_policy default `first_success` when saving). |
| `recognition` → state | **Additive** | `recognition.phase === "not_configured"` and `detail === "no_recognition_providers"` when no runnable primary chain. Now Playing shows setup copy. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] Always persist a non-empty `recognition.providers` when the user enables ACRCloud / AudD / Shazamio slots (do not rely on `recognizer_chain` alone after upgrade).
- [ ] Surface `not_configured` in Physical Media UX if the backend reports it (optional polish).

**Risk**

- [ ] **High** for Pi configs that never stored `recognition.providers` — mitigated by one **Save** from iOS or a one-time `jq` edit; see README troubleshooting *Track recognition not working*.
