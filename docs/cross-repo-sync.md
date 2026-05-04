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

---

## Log: 2026-05-04 — Discogs enrichment: persistence + additive API/state exposure (PR3)

**Backend (`oceano-state-manager` + `internal/library` + `oceano-web`)**

| Item | Type | Notes |
|------|------|-------|
| `track.discogs_url` in `GET /api/stream` + `GET /api/status` | **Additive** | URL string; present in the Physical source track object when Discogs enrichment succeeds; omitted (`omitempty`) otherwise. |
| `discogs_url` in `GET /api/library` + `GET /api/library/changes` | **Additive** | Present on `LibraryEntry` when persisted; empty string when not yet enriched. |
| SQLite `collection.discogs_url` | **Additive** | New nullable column via migration; existing rows default to `NULL` (mapped as empty string). |
| `UpdateDiscogsEnrichment` (internal) | **Additive** | State-manager persists Discogs URL + supplementary fields (`album`, `label`, `released`) after async enrichment; additive policy — no overwrites of existing values. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] **Config / Physical Media screen (MVP)**: add Discogs controls aligned with existing provider UX:
  - `Enable Discogs enrichment` toggle -> maps to `recognition.discogs.enabled`
  - `Discogs token` secure input (BYOK) -> maps to `recognition.discogs.token`
  - Save via `POST /api/config` preserving the full `recognition` object (including `recognition.providers` flow)
- [ ] **Validation (MVP)**: if `recognition.discogs.enabled == true` and `token` is empty, block save with inline guidance.
- [ ] **Advanced Discogs tuning** (`timeout_secs`, `max_retries`, `cache_ttl_hours`) is intentionally **deferred** for a later iOS iteration; backend defaults remain authoritative for now.
- [ ] **Now Playing / Physical Media screen**: when `track.discogs_url` is non-empty, optionally surface a Discogs link or badge. No crash risk — field is `omitempty` and clients that ignore it are unaffected.
- [ ] **Library screen**: `discogs_url` may appear on `LibraryEntry`; safe to display or ignore per design decision.

**Risk**

- [x] low — purely additive; no field renames or removals.

---

## Log: 2026-05-04 — Per-provider rate-limit feedback (`recognition` in state.json + `/api/recognition/provider-health`)

**Contract owner:** [`docs/reference/recognition.md`](reference/recognition.md) — "Rate-limit backoff fields" section

**Backend (`oceano-state-manager` + `oceano-web`)**

| Item | Type | Notes |
|------|------|-------|
| `recognition.rate_limited_providers` | **Additive** | `[]string` of canonical IDs in backoff; omitted when empty. Canonical IDs: `"acrcloud"`, `"shazam"`, `"audd"`. |
| `recognition.backoff_expires` | **Additive** | `map[string]int64` — Unix epoch second per rate-limited provider; omitted when empty. Use `backoff_expires[id] - now` for countdown. |
| `GET /api/recognition/provider-health` | **Additive** | Snapshot for config screen: per-provider `configured`, `rate_limited`, `backoff_expires`, `attempts_24h`, `success_rate_24h`, `last_success_at`, `last_attempt_at`. All timestamps epoch int64. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] **Home screen — display status area**: when `recognition.phase == "matched"` and `source == "Physical"`, show "Reconhecido por \<provider\>" using `recognition.provider`. If `recognition.provider` is empty, show "Reconhecido" (fallback — no crash).
- [ ] **Home screen — dynamic alert card**: when `recognition.rate_limited_providers` is non-empty, show a card listing affected providers and countdown derived from `recognition.backoff_expires[id] - Date.now()/1000`. Card must disappear automatically when the SSE delivers a state without rate-limited providers.
- [ ] **Physical media config — provider list**: call `GET /api/recognition/provider-health` on screen appear and pull-to-refresh. Show per-provider dot: green (not rate-limited), yellow (rate-limited + time remaining), grey (not configured / no attempts). Do **not** recompute `rate_limited` client-side — trust the endpoint's boolean.
- [ ] `provider` display names are owned by the iOS client (map from canonical ID: `acrcloud` → "ACRCloud", `shazam` → "Shazam", `audd` → "AudD").

---

## Log: 2026-05-03 — Lightweight HTTP: VU-gated SSE, player summary, library sync

**Contract owner:** [`docs/reference/http-lightweight-clients.md`](reference/http-lightweight-clients.md)

**Backend (`oceano-web` + `oceano-state-manager`)**

| Item | Type | Notes |
|------|------|-------|
| `GET /api/stream` | **Compatible** | Default SSE payload **omits** top-level `vu` (use `?vu=1` to match on-disk JSON). Named SSE event **`library`** with `{"library_version":n}` when the counter bumps. |
| `GET /api/status` | **Compatible** | Same `vu` rule: default omit; `?vu=1` includes meters. |
| `GET /api/player/summary` | **Additive** | Small state + `library_version`; **`ETag` / `304`**; header **`X-Oceano-Library-Version`**. |
| `GET /api/library` | **Compatible** | **`ETag` / `304`** + **`X-Oceano-Library-Version`** on full list response. |
| `GET /api/library/changes` | **Additive** | `since_version` → `deleted_ids` + `upserts` + current `library_version`. |
| `PlayerState.vu` | **Additive** | State file may include `vu` levels (throttled writes from VU socket). |
| SQLite `oceano_library_sync` + `library_changelog` | **Additive** | Triggers on `collection` maintain monotonic **`library_version`**. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] Prefer **`GET /api/player/summary`** with **`If-None-Match`** for foreground polling; use SSE without `vu` unless showing meters.
- [ ] Library: use **`GET /api/library`** `ETag`/`304` and/or **`GET /api/library/changes`**; optionally listen for SSE **`event: library`**.
- [ ] Confirm any code that assumed **`vu` always present** on SSE/status is updated (default is now omitted).

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

## Log: 2026-05-03 — Recognition attempt telemetry (`recognition_attempts`)

**Backend**

| Item | Type | Notes |
|------|------|--------|
| SQLite **`recognition_attempts`** | **Additive** | Append-only rows from `oceano-state-manager` (per provider call under coordinator context): trigger, phase, skip/duration, WAV RMS mean/peak, `physical_format` key aligned with **`rms_learning.format_key`**, latency, `error_class`. |
| **`GET /api/recognition/attempts?limit=`** | **Additive** | Optional diagnostics JSON (default 100, max 500). See `docs/reference/http-lightweight-clients.md`. |

**iOS follow-up (`oceano-player-ios`)**

- None required. Optional developer / LAN tooling may consume the endpoint.

**Risk**

- [x] low (SD write volume: one row per successful provider HTTP attempt; bounded by recognition cadence).

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
| `recognition.acoustid_client_key` in `/etc/oceano/config.json` | **Additive / legacy** | Optional string; may appear in `GET/POST /api/config` payloads and `oceano-state-manager` CLI. **AcoustID is not a product provider** (short-capture model); if non-empty, state-manager logs that the key is **ignored**. See `docs/plans/recognition-master-plan.md` (feature **P15**). |

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
| `recognition.shazam_recognizer_enabled` | **Compatible** | When explicitly `false`, `oceano-state-manager` forces `recognition.providers` shazam entries to `enabled: false` on load (subprocess not started). When omitted or `true`, shazam rows are unchanged. |
| `recognition.shazam_python_bin` | **Deprecated** | Ignored at runtime; cleared on save. `loadConfig` migrates to `shazam_recognizer_enabled` when that key is absent (legacy: explicit empty `shazam_python_bin` → off; key omitted → on; root `shazam_python` from older `install-shazam.sh` → on). |
| `internal/recognition.BundledShazamioPythonBin` | **Additive** | Constant matching the venv from `install-shazam.sh`; **authoritative** interpreter path for Shazamio (optional non-empty `--shazam-python` overrides for debugging only). |
| `oceano-web` → systemd `--shazam-python` | **Compatible** | Always written as `BundledShazamioPythonBin`. `oceano-state-manager` starts Shazamio only when an enabled shazam provider is present after applying `shazam_recognizer_enabled`; empty CLI value still uses the bundled path in code when shazam participates. |

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

## Log: 2026-05-03 — `GET /api/config` ETag; recognition POST restart dedup

**Backend (`oceano-web`)**

| Item | Type | Notes |
|------|------|-------|
| `ETag` + `304` | **Compatible** | `GET /api/config` sets `ETag` (SHA-256 of JSON body), `Cache-Control: private, no-cache`; `If-None-Match` → `304 Not Modified` when unchanged. Documented in `docs/backend-recognition-providers-contract.md`. |
| `POST /api/config` | **Compatible** | `oceano-state-manager` restart skipped when `recognition` differs only by materializer normalization (nil vs empty `providers`, default `merge_policy`) — same on-disk semantics as before. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] Optional: send `If-None-Match` on config refresh to use `304` (bandwidth / battery).

---

## Log: 2026-05-03 — `recognition.providers` required; `recognizer_chain` deprecated for runtime

**Companion contract (downstream repo):** `oceano-player-ios/docs/backend-recognition-providers-contract.md` — in-tree mirror: `docs/backend-recognition-providers-contract.md`. Checklist for implementers (non-empty `providers` on POST, GET upgrade path, `not_configured`, UX copy). Item 3 there must state that the **client** merges `providers` into the POST body; **`oceano-web` does not** synthesize providers from `recognizer_chain`.

**Backend (`oceano-player` — shipped)**

| Item | Type | Notes |
|------|------|-------|
| `recognition.providers` | **Breaking** | Physical recognition runs only when `recognition` exists in `/etc/oceano/config.json` and `providers` is a **non-empty** array that yields at least one runnable **primary** (credentials / install). Missing `recognition`, omitted `providers`, or `[]` disables recognition. |
| `recognition.recognizer_chain` | **Deprecated** | Still written by older clients / systemd flags; **ignored** when building the recognition plan. |
| `POST /api/config` materialize | **Breaking** | No longer synthesizes `providers` from `recognizer_chain`; empty/absent list stays empty (merge_policy default `first_success` when saving). |
| `recognition` → state | **Additive** | `recognition.phase === "not_configured"` and `detail === "no_recognition_providers"` when no runnable primary chain. Now Playing shows setup copy. |

**Backend checklist (this repo)**

- [x] `oceano-state-manager`: recognition plan built **only** from `recognition.providers`; empty/missing → disabled + logs.
- [x] `recognition_config_load.go`: load empty `providers` when `recognition` present; clear when `recognition` absent.
- [x] `oceano-web`: `materializeRecognitionProvidersIfEmpty` no longer builds providers from `recognizer_chain`.
- [x] `PlayerState.recognition` + kiosk `nowplaying/main.js`: `not_configured` / `no_recognition_providers`.
- [x] `README.md`, `docs/reference/recognition.md`, `docs/metrics-snapshots/README.md` (upgrade `jq` example).

**iOS follow-up (`oceano-player-ios`)**

- [x] In-tree contract + checklist: `docs/backend-recognition-providers-contract.md` (linked above).
- [x] Persist non-empty `recognition.providers` on Physical Media save when recognition should run; do not rely on `recognizer_chain` alone (per contract doc — **verify on device** before release).
- [x] GET without `providers` / empty: pre-fill from card model + prompt Save, or merge pre-filled list into **outgoing POST** (Pi does not infer providers).
- [x] SSE / status: handle `phase === "not_configured"` + `detail === "no_recognition_providers"` (per contract doc — **verify on device**).

**Risk**

- [x] **High** for Pi configs that never stored `recognition.providers` — mitigated by **Save** from iOS, contract doc, `docs/metrics-snapshots/README.md` `jq` example, and README troubleshooting *Track recognition not working*; re-open if field reports fail after upgrade.

---

## Log: 2026-05-03 — VU boundary policy, seek anchoring, ACR quota backoff

**Backend (`oceano-state-manager` + `internal/recognition`)**

| Item | Type | Notes |
|------|------|-------|
| `shouldSuppressBoundarySensitiveBoundary` + `silence->audio` | **Compatible** | Boundary-sensitive **full-track** lock applies to **energy-change** only, not `silence->audio` (avoids stacking with narrowed hard-silence bypass). See `source_vu_monitor.go`. |
| `computeRecognizedSeekMS` (periodic, first ID) | **Compatible** | When `previousResult == nil`, periodic seek uses **max** of capture elapsed and **wall time** since `physicalStartedAt`; hard-boundary **no match** re-anchors `physicalStartedAt` to `lastBoundaryAt`. Unified state **seek** fields may better match real playback after slow multi-attempt identification. |
| ACRCloud JSON **3003** | **Compatible** | Treated as **`ErrRateLimit`** (with **4001**, **4003**) → coordinator **5 min** `rateLimitBackoff`, not 30 s generic error loop. Reduces API hammering after plan quota exhaustion. |

**Documentation / planning**

| Item | Notes |
|------|-------|
| `docs/reference/recognition.md` | Error table: ACR quota codes → 5 min backoff. |
| `docs/plans/recognition-master-plan.md` | **Deferred: Provider quota / rate-limit UX** (§ *Deferred: provider quota…*); **Backend backoff (implemented)** for ACR 3003 / related codes → `ErrRateLimit`. Live/gapless / Shazamio stance in same file. |

**iOS follow-up (`oceano-player-ios`)**

- [ ] **Optional release note:** physical progress / “time into track” may look different after long `no_match` streaks (seek anchoring) — no new JSON keys.
- [ ] **Future (deferred):** providers screen for quota exceeded + optional user budget / reset day — see flexible-providers plan; no contract fields yet.

**Risk**

- [x] **low** for HTTP/config contracts; **medium** for edge listening scenarios (live/gapless) — validate on device after deploy.
