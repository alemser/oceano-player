# Recognition — Master Plan

**Language:** English (repository convention).

**Scope:** Single roadmap for **physical track recognition** — providers, coordinator, triggers (VU / continuity / timers), telemetry, quotas, security, companion **iOS** contract, and commercial/BYOK posture. This file **replaces** the former split plans (`recognition-flexible-providers-and-secrets.md`, `recognition-enhancement.md`, `recognition-provider-chain-improvement.md`, `recognition-shazamio-deferral-continuity-and-extensibility.md`), which were removed 2026-05-03 and merged here.

**Living references:** `docs/reference/recognition.md`, `docs/reference/recognition-architecture.md`, `docs/cross-repo-sync.md`, `docs/backend-recognition-providers-contract.md`, `docs/bug-boundary-suppression-after-silence.md`, `docs/metrics-snapshots/README.md` (optional stats snapshots / SQL examples).

---

## Feature matrix

| ID | Feature | Area | Status | Notes |
|----|---------|------|--------|--------|
| **Providers & chain** ||||
| P01 | ACRCloud recognizer (BYOK, HTTP, IPv4) | Providers | **Complete** | `internal/recognition/acrcloud.go` |
| P02 | AudD recognizer (BYOK token, multipart WAV) | Providers | **Complete** | `internal/recognition/audd.go` |
| P03 | Optional **shazamio** subprocess (community client; **not** official Shazam API) | Providers | **Complete** | `install-shazam.sh`; disclose ToS/commercial risk in UX |
| P04 | **`recognition.providers[]`** is the **only** runtime ordering input | Config | **Complete** | Empty / missing → recognition off; state `not_configured` |
| P05 | **`merge_policy`** with default **`first_success`** | Coordinator | **Complete** | Extended modes (P12) not implemented |
| P06 | **`recognizer_chain`** ignored for runtime plan build | Config | **Complete** | Legacy key may remain in JSON |
| P07 | **oceano-web** does not synthesize `providers` from `recognizer_chain` on save | Web | **Complete** | `cmd/oceano-web/recognition_materialize.go` |
| P08 | **Primary** order + **confirmer** (second `Recognize` on same WAV when configured) | Chain | **Complete** | `recognition_setup.go`, `recognition_coordinator.go` |
| P09 | **Shazamio continuity** monitor (parallel “same track?” channel) | Triggers | **Complete** | When `shazam` enabled + installed; CPU-heavy vs REST |
| P10 | **Rate-limit backoff** when provider returns **`ErrRateLimit`** | Resilience | **Complete** | ACR 3003 / 4001 / 4003; AudD; coordinator ~5 min backoff (`chain.go`) |
| P11 | **ChainRecognizer**: sequential primaries; **no** auto-advance on “low score” match | Coordinator | **Complete** | Needs P12 or coordinator change for score-based fallback |
| P12 | **`merge_policy`**: `best_score`, `require_agreement`, `arbitrate`, `prefer_provider`, `user_picks_on_conflict` | Coordinator | **Planned** | Spec table in § [Future merge policies](#future-merge-policies) |
| P13 | **Parallel** primaries (“fast mode”) + cancel slower calls | Coordinator | **Planned** | Higher cost; compose with P22 |
| P14 | **Per-provider HTTP timeout** | Providers | **Planned** | Today: shared / implicit timeouts |
| P15 | **AcoustID / Chromaprint** as **primary** recognizer | Providers | **Not pursued** | Short WAV vs full-track fingerprint model; key ignored if present |
| P16 | **Gracenote** (enterprise SDK) | Providers | **Deferred** | Poor fit for OSS Pi appliance |
| P17 | **SoundHound / Houndify** | Providers | **Deferred** | Evaluate if licensing acceptable |
| P18 | **TheAudioDB / MusicBrainz / Cover Art Archive** as **enrichment** after ID | Metadata | **Planned** | Name/MBID-based; not mystery-clip ID |
| P19 | **Continuity** config: `continuity.enabled`, `continuity.provider` (not Shazam-only) | Config | **Planned** | Migrate legacy Shazam-prefixed flags |
| P20 | **Custom provider** via **fixed-contract HTTPS** only (no arbitrary executables from JSON) | Extensibility | **Planned** | Unsafe: shell snippets, unsigned `.so`, arbitrary paths from network UI |
| P21 | **Delegated recognition** — secrets only on iOS; phone runs API or returns tokens | Security | **Planned** | Option A in § [Secret storage models](#secret-storage-models) |
| P22 | **UsageLimiter** + SQLite + **`usage_limits`** per provider + continuity budget | Quotas | **Planned** | Defaults off; stricter-of local vs vendor when P13 lands |
| P23 | **`POST /api/recognition/usage/reset`** (local counters only) | Quotas / ops | **Planned** | Disclaimer: does not restore vendor quota |
| P24 | **Quota / rate-limit** fields in **state + SSE + iOS** (not logs-only) | UX / contract | **Deferred** | P10 backoff shipped; UI surfacing deferred |
| P25 | **iOS** provider cards: reorder, toggles, masked credentials, **`providers` on save** | iOS | **Partial** | MVP; must always emit non-empty `providers` when recognition should run (`backend-recognition-providers-contract.md`) |
| P26 | **Web** config: sortable provider list parity (optional) | Web | **Deferred** | Product direction: **iOS first**; `oceano-web` = bootstrap/admin |
| P27 | **Per-provider “Test connection”** + verified badge | UX | **Planned** | Lightweight probe per provider |
| P28 | **Config presets** (e.g. low cost / balanced / high accuracy) | UX | **Planned** | Presets must not embed shared production keys |
| P29 | **Automatic chain fallback** when paid provider hits **vendor** monthly cap | Coordinator | **Planned** | Distinct from P10 backoff; needs vendor API signals or user-declared cap (P22) |
| P30 | **Local cache reuse** — skip new capture when same provider id / fingerprint | Cost | **Planned** | Same capture already reused for confirmer |
| **Triggers & calibration** ||||
| T01 | VU **silence→audio** + **energy** boundaries + **duration-exceeded** hard path (+ **10 s** grace) | Triggers | **Complete** | `source_vu_monitor.go`; grace in `docs/reference/recognition.md` |
| T02 | Append-only **`boundary_events`** | Telemetry | **Complete** | WAL on `library.db` |
| T03 | **Listening Metrics** — `/api/recognition/stats`, provider triggers | Telemetry | **Complete** | `history.js`, `cmd/oceano-web/library.go` |
| T04 | **`GET /api/recognition/boundary-stats`** + **`followup_*`**, **`early_boundary`** | Telemetry | **Complete** | Coordinator writes outcomes after recognition |
| T05 | **R1c** intra-track silence→audio coalesce | Triggers | **Aborted** | Too aggressive; legacy rows labelled in UI; retry only opt-in + e.g. R8-gated |
| T06 | **`format_resolved`** / **`format_resolved_at`** backfill on Vinyl/CD save | Telemetry | **Complete** | Late format correction for aggregates |
| T07 | Calibration **floor clamp** (R2b) + **minimum off→on gap** (R2c) | Calibration | **Complete** | `loadBoundaryCalibrationModel` |
| T08 | **`advanced.r3_telemetry_nudges`** (bounded silence/duration hints) | Calibration | **Complete** | Default off |
| T09 | **RMS percentile learning** (`rms_learning`, optional **`autonomous_apply`**) | Calibration | **Complete** | `internal/library/rms_learning.go`, `rms_percentile_learner.go` |
| T10 | **`GET /api/recognition/rms-learning`** + UI cards | Telemetry | **Complete** | Advanced JSON + metrics |
| T11 | **`boundary_sensitive`** on `collection` + VU consumption | Library | **Complete** | Energy-change duration lock; not applied to `silence→audio` (see `source_vu_monitor.go`) |
| T12 | **R4** `LocalLibraryRecognizer` (local-first) | Providers | **Planned** | Bounded worker pool; load-shedding |
| T13 | **R4b** local vs cloud metrics + ACR error-class breakdown | Telemetry | **Planned** | After R4 |
| T14 | **R5** fingerprint cache + **cloud re-verify** (TTL / 1-in-N / low score) | Cost | **Planned** | Avoid “local trap” without re-verify |
| T15 | **R6 / R6b** ML-lite boundary classifier + metrics | Triggers | **Planned** | Only if percentile / rules insufficient |
| T16 | **R9** low-confidence **`recognition_alternatives`** + carousel + pick-to-confirm | UX / state | **Planned** | ACR `metadata.music[1..]`; Shazam multi-match **TBD** (investigation log § [R9 investigation](#r9-multi-candidate-investigation)) |
| T17 | **R10** dual-policy shadow (active vs reference calibration) | Calibration | **Deferred** | Optional; RMS learning reduces need |
| T18 | **B4** quiet intros / live fades — **trigger & capture policy** (longer first capture, RMS gates) | Capture | **Planned** | Before recognition-only gain (T19) |
| T19 | **Recognition-only digital gain** or **opt-in ALSA** capture assist | Signal | **Deferred** | Fingerprint / false-boundary risk; telemetry first |
| T20 | Fix **stale track duration** suppressing **post-silence** boundary (`Time` → `Money` class bug) | Bugs | **Planned** | `docs/bug-boundary-suppression-after-silence.md` |
| T21 | **VU reconnect**: suppress **first** `silence→audio` only after socket reconnect | Triggers | **Complete** | Reduces false triggers after detector restart |
| **Product & compliance** ||||
| O01 | **Shazamio**: optional only; defer **commercial** marketing reliance | Product | **Complete** | Use `/api/recognition/stats` + `boundary_events` before removing continuity |
| O02 | **Minimum install** without recognition (Physical / AirPlay / BT still work) | Ops | **Complete** | README first-boot bar |
| O03 | **BYOK** disclosure — user is API customer; no bundled unlimited quota | Compliance | **Partial** | README + iOS vendor links; counsel for sold products |
| O04 | **`oceano-web`** = bootstrap / LAN admin until native parity | Product | **Complete** | Avoid web-only product features that duplicate iOS |

---

## Future merge policies

Shipped: **`first_success`** only (sequential primaries; advance on no match / eligible error; **not** on low-confidence match).

| Mode | Behaviour | Headless / UX |
|------|-----------|----------------|
| `first_success` | **Shipped** — first successful primary wins | Simple |
| `best_score` | Pick highest declared confidence across calls | Automatic |
| `require_agreement` | Require **N** providers to agree (normalised artist/title or ISRC/MBID) | May yield no match until timeout policy |
| `prefer_provider` | User truth ranking when scores tie | Complements order |
| `user_picks_on_conflict` | Expose `track_candidates[]` in state for picker | Needs SSE + iOS + Now Playing |
| `arbitrate` | Run **`arbitration`**-role providers when primaries conflict | Deterministic tie-break rules in docs |

**Recommendation:** Ship machine-local `best_score` + `require_agreement` + `arbitrate` before `user_picks_on_conflict`.

---

## Continuity vs main chain

- **Today:** Continuity is **Shazamio-specific** when `shazam` is in `recognition.providers` — periodic capture compares to displayed track; can trigger re-recognition on divergence.
- **Risk:** CPU cost; users without Shazamio still need sensible gapless behaviour (`duration-exceeded`, calibration).
- **Direction (P19):** Explicit `continuity.{enabled,provider,interval_secs,capture_duration_secs}`; provider default **`shazam`** for migration; later **ACRCloud** or “same as primary” if product defines comparable probes.
- **Hardware checklist** before shrinking continuity: gapless CD; vinyl side change; behaviour with Shazamio uninstalled.

---

## Secret storage models

| Option | Summary | Status |
|--------|---------|--------|
| **A — Phone only** | Pi sends fingerprint/job; iOS calls vendor with Keychain secrets; returns result | **Planned** (P21); latency / pairing UX |
| **B — Token broker** | Short-lived tokens to Pi; root secret never on Pi | **Planned** / advanced |
| **C — On Pi (`config.json`)** | Root-owned file, masked in previews | **Complete** (default supported path) |

### Conceptual `recognition` JSON (target / migration)

Illustrative shape for **P19**, **`credential_ref`**, and optional **`usage_limits`** (P22); not all keys exist in every release.

```json
"recognition": {
  "providers": [
    {
      "id": "acrcloud",
      "enabled": true,
      "roles": ["primary", "confirmer"],
      "credential_ref": "ios:acrcloud"
    },
    {
      "id": "audd",
      "enabled": true,
      "roles": ["primary"],
      "credential_ref": "config:audd"
    },
    { "id": "shazam", "enabled": false, "roles": ["primary"] }
  ],
  "merge_policy": "first_success",
  "continuity": {
    "enabled": false,
    "provider": "shazam",
    "interval_secs": 12,
    "capture_duration_secs": 4
  }
}
```

**Validation:** at least one enabled provider must have **`primary`** and be runnable (credentials / install). **`confirmer`** / **`arbitration`** without **primary** is invalid.

---

## BYOK / commercial posture (summary)

- Oceano ships **client code**; the **end user** holds the relationship with each vendor (ToS, billing, quotas).
- **Do not** ship a shared production API key for all customers without a written OEM/redistribution agreement.
- **Nominative** third-party naming only; no implied endorsement.
- Disclose **what** leaves the device (short WAV vs fingerprint) for privacy questionnaires (e.g. App Store).

---

## Quiet program starts, live fades, capture gain (B4)

Not a provider-order problem: **REC OUT level**, **silence threshold**, **capture length**, and **first-window** policy.

1. **Docs + manual gain** (today, best ROI) — target RMS band in README.
2. **Software:** optional longer first capture after Physical edge or `no_match` when post-edge RMS stays low (**T18**).
3. **Recognition-only bounded normalise** of WAV (**T19**, deferred).
4. **Opt-in ALSA gain automation** (**T19**, highest risk; card matrix + opt-in).

**Explicit non-goal:** full AGC on live tap without fingerprint quality spike.

---

## Third-party clarity: shazamio

Use package name **`shazamio`** in precise copy. **Not** an official Shazam / Apple API. Community client may hit Shazam-like backends — **ToS**, **blocking**, and **commercial** risk higher than ACRCloud / AudD. Prefer documented APIs for default retail story.

---

## R9 multi-candidate investigation

Pre-UX gate: log whether **shazamio** returns multiple `matches[]` on real captures.

| Date | Outcome | Notes |
|------|---------|-------|
| — | *pending* | Replace with finding; carousel may be **ACR-first** if Shazam stays single-match |

**Target optional state:**

```json
"recognition_alternatives": [
  { "title": "", "artist": "", "album": "", "score": 0, "acrid": "", "shazam_id": "" }
]
```

Gate alternatives on low score or config; SSE + `POST` to apply user pick → library/history.

---

## Shazamio & continuity — stance summary

| Topic | Now | Later |
|-------|-----|--------|
| Shazamio | Optional install; defer commercial reliance | Decide replace/remove using stats + counsel |
| Continuity | Shazamio when enabled in providers | P19: user-chosen provider, slower cadence, quota UX |
| Custom code in config | **Do not ship** | P20: HTTPS contract, fork, or signed bundles |
| Live / gapless | `duration-exceeded` + grace; RMS / calibration telemetry | Optional library/album “live gapless” heuristics |
| Quota UX in UI | Deferred (P24) | Surface `rate_limited` + user budgets (P22) |

**Telemetry guardrail:** high `fallback_timer` ≠ “many wrong tracks fixed” — validate with real sessions.

**SQLite:** WAL on; high-frequency telemetry → batching / retention if needed; avoid per-event fsync storms.

---

## Custom extensibility (safe order)

1. **Fork + compile** — new `internal/recognition` + signed release (safest).
2. **Fixed-contract HTTP recognizer** — HTTPS URL + auth + versioned JSON schema + WAV bytes (no arbitrary code).
3. **LAN sidecar** — user-controlled binary at `127.0.0.1` + same contract.
4. **Signed extension bundles** — only if demand justifies cosign/manifest cost.

Any route touching **`POST /api/config`**: atomic writes, no privilege escalation, **`cross-repo-sync.md`** for iOS.

---

## Live / gapless boundary policy (planning)

- **`duration-exceeded` grace (10 s):** fixed constant in `source_vu_monitor.go`; absorbs metadata vs pressing mismatch and processing lag (`docs/reference/recognition.md`).
- **On-device ML for transitions:** long-term research; prefer **export features → sidecar** over hot-path inference until validated.
- **Heuristic “live + gapless” mode:** combine metadata, library flags, **`boundary_sensitive`**, duration-exceeded — never claim 100% audio-only certainty; new keys need cross-repo checklist.

---

## Provider evaluation shortlist

**Acoustic ID (snippet-friendly):** ACRCloud ✅, AudD ✅, shazamio ✅ (unofficial), AcoustID ❌ (P15), Gracenote ❌ (P16), Houndify ⚠️ (P17).

**Enrichment (post-ID):** TheAudioDB, MusicBrainz API, Cover Art Archive (P18).

---

## Minimum executable install (green path)

1. Install stack; enable detector + state-manager (+ web optional).
2. Confirm units active; `/tmp/oceano-state.json` updates.
3. If recognition wanted: non-empty **`recognition.providers`** + credentials per enabled id (or Shazamio path).
4. After hand-editing JSON: restart **oceano-web** or `POST /api/config` so systemd args stay aligned.

---

## Execution order (backend first, then iOS)

| Step | Layer | Deliverable |
|------|--------|-------------|
| 1 | Backend | Explicit `providers` required; deprecate runtime `recognizer_chain` |
| 2 | Backend | `merge_policy` plumbing (`first_success` first) |
| 3 | Backend | Continuity flags (P19) when ready |
| 4 | iOS | Settings write **`recognition`** via `POST /api/config` |
| 5 | Backend + iOS | Delegated jobs (P21) after 1–4 stable |

Rule: additive JSON until deliberate major version.

---

## Deferred: provider quota / rate-limit UX

**Implemented:** ACR (and related) → `ErrRateLimit` → coordinator backoff.

**Deferred:** stable **`rate_limited`** / `rate_limited_until` in state for clients; user-declared monthly caps UI; vendor presets in-image (prefer generic templates in docs).

---

## Design principles (triggers & local stages)

- Feature **flags** for new trigger logic or recognizer stages.
- Separate **when** to capture vs **who** identifies.
- **Telemetry before** tightening production thresholds.
- Small PRs: instrumentation → optional recognizer → stats → ML-lite last.
- **User overrides** explicit and reversible (`boundary_sensitive`, future hints).
- **Pi-first:** bounded goroutines / worker pool for fingerprints; **never** block VU reader or PCM consumer on heavy work; load-shed to cloud.

---

## Listening Metrics visibility contract

Any new telemetry / recogniser / trigger semantics ships with:

1. **Persistence** (SQLite tables or columns).
2. **HTTP API** under `/api/recognition/…` or extended existing stats.
3. **UI** update to `history.html` / `history.js` (and CSS if needed).

Empty states match existing recognition stats patterns.

---

## Operational persistence (SQLite / SD)

- **WAL** enabled — keep for production.
- High insert rate → consider batching / coalescing; short transactions; optional move `library.db` to better media.
- Do not tune **`PRAGMA synchronous`** without measurement (power-loss risk).

---

## Physical format lag (Vinyl / CD vs “Physical”)

Store **`format_at_event`** and backfill **`format_resolved`** when user corrects library row. Aggregations prefer resolved when present. R6 training cohorts must handle unresolved **Physical**.

---

## Axis 1 — Learning (summary)

- **1A shipped:** `boundary_events` + metrics APIs; richer per-event RMS remains optional.
- **1B:** Percentiles / rolling aggregates per cohort; optional offline small classifiers → **bounded threshold nudges** only.
- **1C ML-lite:** Only if 1B insufficient; keep legacy path below confidence threshold.
- **1D:** Stylus / groove wear — extend longitudinal stats; input to 1B, not parallel silo.

---

## Axis 2 — Local identification (summary)

- **2A** `LocalLibraryRecognizer` (T12): cheap signals first; optional Chromaprint/AcoustID **behind flag** conflicts with P15 — prefer library keys / ISRC.
- **2B** (T14): fingerprint after cloud success + re-verify policy (TTL, 1-in-N, low local score → cloud).
- **2C:** Gate local attempts on capture length, rate limits, CPU pressure.

**Pi resources:** semaphore-limited pool; decouple fingerprint-after-success; timeouts everywhere.

---

## Axis 3 — False positives & hints (summary)

- **Early boundary** cohort: analytics only until validated; never block on single event (`internal/library/boundary_events.go` + `early_boundary` in SQLite).
- **R8** shipped for per-track **boundary-sensitive** hint.

---

## R2 bulk format edits (implementation note)

Mass library updates: chunked transactions, async job + fast HTTP response, single-writer discipline, idempotent backfill (see § **R2 bulk format edits** above).

---

## Milestones already delivered (historical)

| Milestone | Notes |
|-----------|--------|
| R1, R1b | `boundary_events` + Listening Metrics exposure |
| R1c | **Aborted** (T05) |
| R2, R2b, R2c, R7, R3, R8, RMS-L, RMS-V | Calibration, follow-ups, nudges, RMS learning + UI |

**Active backlog:** T12–T16, T18–T20, P12–P14, P18–P24, P27–P30.

---

## Risks (condensed)

SD wear from telemetry; false local matches without re-verify; format lag in all models; Pi optional deps; early-boundary over-interpretation; RMS `autonomous_apply` regression after hardware change.

---

## Open questions

1. Long-term: keep shazamio vs commercial SKU without it?
2. Delegated recognition (P21): acceptable added latency for vinyl?
3. If credentials are `ios:`-only and phone offline — fallback policy?
4. Default **`continuity.enabled`** when Shazamio absent?
5. Should vendor **429** auto-tighten local counters (P22) or advisory only?
6. Ship vendor-named quota presets vs docs-only templates?
7. Is **T18** enough for quiet material, or is **T19** required?

---

## In-repo code map

| Topic | Location |
|-------|----------|
| Provider chain + rate limit step | `internal/recognition/chain.go` |
| ACRCloud / AudD | `internal/recognition/acrcloud.go`, `audd.go` |
| Plan build | `cmd/oceano-state-manager/recognition_setup.go`, `recognition_config_load.go` |
| Coordinator + confirmer | `cmd/oceano-state-manager/recognition_coordinator.go` |
| VU / boundaries / duration-exceeded | `cmd/oceano-state-manager/source_vu_monitor.go` |
| Continuity | `cmd/oceano-state-manager/main.go` (Shazam continuity monitor) |
| WAV capture | `cmd/oceano-state-manager/recognizer.go` |
| Web materialize | `cmd/oceano-web/recognition_materialize.go` |
| Metrics handlers | `cmd/oceano-web/library.go` |

---

## Verification & doc hygiene

After edits to **`recognition.providers`** / **`merge_policy`** wiring: `docs/reference/recognition.md` (explicit provider list), `go test ./cmd/oceano-state-manager/... -short`, chain matrix test, Pi smoke per `AGENTS.md` / `.cursor/skills/pi-recognition-explicit-providers-smoke/SKILL.md`.

---

## Revision history

| Date | Change |
|------|--------|
| 2026-05-03 | First consolidated matrix from four split plans. |
| 2026-05-03 | Merged full narrative (merge modes, continuity, secrets, B4, R9, extensibility, axes, risks, open questions); removed obsolete split files; single source of truth. |
