# Recognition Enhancement Plan (Triggers + Local Identification)

This document extends the earlier discussion into a concrete, incremental roadmap. It is designed to preserve today’s stable behavior (VU monitor, calibration, coordinator, ACRCloud/Shazam chain, library) while improving precision over time.

**Status (2026-04):** Milestone **R1 + R1b** (boundary telemetry + Listening Metrics) is on **`main`**. **R1c** (intra-track silence→audio coalesce) was **aborted** after field feedback: the heuristic suppressed too many legitimate track boundaries. Short-lived builds may still have rows with `outcome = suppressed_intra_track_silence` in `boundary_events`; the Listening Metrics UI labels those as **Legacy (intra-track experiment)** (`history.js`). A future retry should be **opt-in** (config flag) and/or gated on **Boundary-sensitive** (R8), not global defaults.

**R2 + R2b + R2c** are **Done** on branch **`feat/recognition-enhancement-plan`** (merge to `main` when ready): library **`format_resolved`** backfill on save; calibration **floor clamp** + **minimum off→on gap** + load-time log in `loadBoundaryCalibrationModel`.

**R7** (**boundary ↔ recognition follow-up** + **early-boundary** cohort flag + Listening Metrics aggregates) is **Done** on **`feat/recognition-enhancement-plan`** — see `boundary_events.followup_*`, **`GET /api/recognition/boundary-stats`**, `history.js`.

### Priority (concrete next work)

**R3** (optional bounded telemetry **nudges** to effective VU silence thresholds + duration pessimism, `advanced.r3_telemetry_nudges` in config, Advanced Settings UI) is **Done** on **`feat/recognition-enhancement-plan`**. **R8** (*Boundary-sensitive*) is also **Done** there. Continue telemetry-driven tuning using **Listening Metrics**. Treat **lifetime** provider stats and **period-scoped** boundary stats as complementary, not interchangeable denominators.

**RMS percentile learning** (`advanced.rms_percentile_learning`, SQLite **`rms_learning`**) is **Done**: autonomous **histograms of stable-silence vs stable-music RMS** per format (`vinyl` / `cd` / `physical`) derive VU silence enter/exit from distributional separation; **`autonomous_apply`** overrides wizard profile thresholds once minimum sample counts are met. Wizard **`vinyl_transition`** metrics remain recommended for energy-dip / gap behaviour. Code: `internal/library/rms_learning.go`, `cmd/oceano-state-manager/rms_percentile_learner.go`, `source_vu_monitor.go`, Advanced UI.

**Future (capture dynamics, not scheduled):** Very **quiet track openings** and **long live fade-ins** (low REC OUT level for many seconds) may delay **recognition** until capture gain or **capture/trigger policy** is adjusted. **RMS learning** helps **VU / silence classification** over time but does not replace **adequate analogue/digital level** into the capture card. Product/engineering options (documentation vs extended first capture vs bounded recognition-only gain vs opt-in ALSA assist) are captured under **Quiet program starts, live fades, and capture gain** in `docs/plans/recognition-flexible-providers-and-secrets.md`, aligned with backlog **B4**.

---

## Design principles (avoid regressions)

- **Feature flags / config toggles** for any new trigger logic or recognizer stages (e.g. local-first off by default).
- **Separate concerns:** (1) *when* to capture and fire triggers vs (2) *who* identifies the track (local vs cloud).
- **Telemetry before policy changes:** log structured events and outcomes before tightening thresholds in production.
- **Small PRs:** telemetry → optional local recognizer → optional statistical layer → optional ML-lite.
- **User overrides:** optional per-library-track hints (e.g. *Boundary-sensitive*) must remain explicit, documented, and reversible.
- **Pi-first resource budget:** heavy or optional work (fingerprints, local DB, inference) must not compete unfairly with **real-time** paths (VU socket read loop, PCM relay consumption). Prefer **strict concurrency limits**, **background/low-priority** execution where the runtime allows, and **load shedding** (skip local stage and fall through to cloud when CPU pressure or queues indicate risk to timely capture).

---

## Listening Metrics page (visibility contract)

The **Listening Metrics** screen (`cmd/oceano-web/static/history.html`, title *Listening Metrics*) is the main place operators see whether recognition and listening behaviour are healthy. Today it combines:

| Surface | API | Client |
|---------|-----|--------|
| Period plays, hours, top artists/albums, plays/hours by source | `GET /api/history/stats` | `cmd/oceano-web/static/history.js` (`loadStats`, `renderStats`, …) |
| Stylus wear / hours (when enabled) | `GET /api/stylus` | `history.js` (`loadStylusSummary`, `renderStylusSummary`) |
| Recognition provider counters (incl. **Trigger** boundary vs fallback timer, attempts/matches per provider) | `GET /api/recognition/stats` | `history.js` (`loadRecognitionStats`) — handler in `cmd/oceano-web/library.go` |
| VU boundary decisions (`boundary_events`: fired / suppressed / ignored / …) | `GET /api/recognition/boundary-stats?days=` | `history.js` (`loadBoundaryStats`, `renderBoundaryStats`) — same period toggle as header stats |

**Plan rule:** any optimisation that adds telemetry, a new recogniser stage, or new trigger semantics should **ship in the same change set** (or an immediately following PR) with:

1. **Persistence** — counters or rows the metrics page can aggregate (extend `recognition_summary`, `play_history`, or new tables as needed).
2. **API** — extend `playHistoryStatsResponse` / `/api/history/stats` and/or `/api/recognition/stats` (or a dedicated `GET` under `/api/recognition/…`) so the data is machine-readable and versioned.
3. **UI** — update `history.html` / `history.js` (and `history.css` if present) so new metrics are **visible**: cards, rows, or a short “health” summary (e.g. local-match rate, boundary suppression rate, backfill-corrected format counts).

This keeps “what is working well” obvious without reading logs on the Pi. Treat **empty states and failure copy** the same way as existing recognition stats (placeholder when no data or API errors).

**Fresh installs:** see **README.md → First-time setup → §4** for user-facing expectations (empty metrics at first, no “mode switch”, telemetry only from the running binary’s lifetime after each deploy).

---

## Operational persistence — SQLite, SD wear, and telemetry volume

Append-only **`boundary_events`** (and any growth in recognition event logging) increases **small random writes** on the library volume. On a Raspberry Pi this is often a **microSD card**, which can wear faster under sustained write amplification than SSD or eMMC.

**Already in place:** Oceano opens the library database with **`PRAGMA journal_mode=WAL`** so readers (e.g. `oceano-web` metrics handlers, `internal/library/library.go` and `cmd/oceano-web/library.go` open paths) and the state-manager writer get **better concurrency** than rollback-journal defaults. Keep WAL on for production.

**If insert rate becomes high** (dense boundary telemetry, micro-pauses):

- Consider **batching** or short **in-memory coalescing** windows before flush (trade-off: loss of per-event resolution unless the batch stores aggregates).
- Avoid holding **one huge write transaction** open during bursts; prefer **small, sequential commits** so read-heavy API paths stay responsive under WAL.
- Operational escape hatch: move **`library.db`** to **USB SSD** or industrial-grade media if telemetry is kept forever at high frequency — document, don’t hard-require.

Do not change **`PRAGMA synchronous`** or related durability knobs without measurement; Pi power-loss during a write is still a real risk.

---

## Physical format classification lag (Vinyl / CD vs “Physical”)

The UI can show **Physical** until the user assigns **Vinyl** or **CD** in the library. Recognition and boundary analytics must not assume the format is always known at event time.

### Implications for telemetry and learning

- Store **both**:
  - **`format_at_event`**: best-known format at boundary/trigger time (`Physical` | `Vinyl` | `CD` | `Unknown`).
  - **`format_resolved`**: nullable; filled when the user (or automation) later assigns a concrete format, plus **`format_resolved_at`**.
- When the user updates classification for a play session or library entry, run a **backfill** step that updates open analytics rows linked by `collection_id`, `play_history_id`, or a stable **session id** so aggregated statistics reflect the corrected label.
- Aggregations used for modeling should prefer **`format_resolved` when present**, else fall back to `format_at_event`, and treat **Physical** as a separate cohort until resolved (avoid training vinyl/CD-specific thresholds on unresolved rows).

### UI / state contract

- Any future “format-aware” hints (e.g. stylus wear, gap statistics) should subscribe to **library updates** (or periodic reconciliation) so displays and triggers refresh after late user edits, not only at first recognition.

---

## Axis 1 — Learning: pauses, live vs studio, vinyl vs CD, track vs intra-track silence

Today the stack already combines **VU-driven boundaries** (`source_vu_monitor.go`, `boundary_detector.go`), **calibration profiles** (`calibration_profile.go`), **duration guards**, and the **recognition coordinator** (`recognition_coordinator.go`). The evolution path is to add **data-driven calibration** on top of these mechanisms, not to replace them overnight.

### Phase 1A — Instrumentation (low risk, high value)

**Shipped (R1):** append-only `boundary_events` with outcome, boundary type, hard flag, physical source, `format_at_event`, duration/seek snapshots, reserved columns for `format_resolved` / linkage — enough for period aggregates on Listening Metrics.

**Still open (feeds R7 / models):** richer fields per event, for example:

- RMS summary around the transition (pre / during / post), silence duration, simple variance or “noise floor” proxies (live material often differs from steady studio pressings).
- Tighter `format_resolved` backfill when the user corrects Vinyl/CD (see **R2**).
- Outcome linkage: same recording vs new track vs no match after capture (not only VU outcome rows).

Persist new columns or a sidecar table with clear foreign keys to `play_history` / `collection` so **late format correction** can backfill without rewriting history.

### Phase 1B — Statistical layer (before “ML”)

- Start with **percentiles and rolling aggregates** per cohort (`Vinyl`, `CD`, unresolved `Physical`) to suggest safer defaults for silence frames, thresholds, and guard windows.
- Optional **lightweight classifiers** (logistic regression, small decision trees) trained **offline**; at runtime apply only **threshold nudges** within safe bounds, with fallback to current fixed calibration when confidence is low.

**Analog front-end / non-stationary noise floor:** high-compliance styli and groove wear change **surface noise**, HF content, and **inner-groove** behaviour, so “silence” RMS distributions are **not static** over months of play. When building 1B cohorts, consider a **future covariate** such as **stylus hours** (or wear band) from existing **Phase 1D** / stylus metrics so suggested silence thresholds can **drift slowly** with equipment age — always bounded and opt-in to auto-apply.

### Phase 1C — ML-lite (only when data supports it)

**Scope discipline:** treat ML-lite as **strictly optional** (“nice to have”). Well-calibrated **Phase 1B** outputs (rolling percentiles, bounded threshold nudges, simple logistic rules trained offline) are expected to capture the bulk of gain at a **fraction** of deploy complexity and RAM footprint versus shipping ONNX (or similar) on-device inference. Pursue 1C only when 1B is demonstrably insufficient on real telemetry.

- Same features as 1B; export a small model (e.g. sklearn → ONNX or hand-rolled rules) and run **cheap inference** on the Pi, or even table lookup by feature bins initially.
- Always keep **legacy path** when model confidence is below a cutoff or sample count is insufficient.

### Phase 1D — Stylus / groove wear (product)

**Note:** Groove noise / stylus-oriented signals and UI already exist in the project. This plan treats 1D as **extend with longitudinal statistics** (trend + hysteresis + conservative messaging), not as greenfield. New work should integrate with existing calibration and display flows rather than duplicating parallel concepts. **Link to 1B:** stylus life and cartridge family are plausible **inputs** to statistical silence calibration (see 1B paragraph above), not only a separate dashboard.

---

## Axis 2 — Local identification before ACRCloud / Shazam

Captured WAV is already available for each attempt. An optional **local-first** stage can reduce API usage and latency when the collection already knows the recording.

### Raspberry Pi 5 — CPU, I/O, and realtime paths

Chromaprint-style work (`fpcalc` or equivalent) plus **local DB lookups** concurrent with **PCM capture** and the **VU monitor** can create **CPU spikes or I/O contention** that are undesirable on a single-board host also running source-detector relay and recognition coordination.

**Design responses (when implementing 2A / 2B):**

- Use an explicit **worker pool** in Go: e.g. a **buffered channel** as a semaphore (`make(chan struct{}, N)`) plus a fixed maximum **N** concurrent fingerprint jobs, so rapid track boundaries cannot enqueue **unbounded** goroutines that starve the rest of the process.
- Run fingerprint generation and cache queries on that **strictly bounded** pool (or a single worker) so they cannot stack unbounded goroutines behind rapid boundaries.
- **Decouple** heavy steps from the hot path where practical: e.g. enqueue fingerprint-after-success **after** cloud match is committed, with backpressure and drop-to-skip if the queue is deep (metadata still correct from cloud).
- **Load shedding:** if system load (or wall-clock budget for the recognition attempt) exceeds thresholds, **skip local** and proceed to cloud immediately — predictable behaviour beats marginal latency savings.
- Never block the **VU reader** or PCM consumer on local recognition work; local stages run in the coordinator’s recognition flow with explicit **timeouts**, not unbounded CPU.

### Phase 2A — `LocalLibraryRecognizer` (new `Recognizer`)

- Implement `Recognize(ctx, wavPath)` in `internal/recognition` (or adjacent package) that:
  - Uses **cheap signals first**: duration, optional ISRC from last successful match, library keys (`acrid`, `shazam_id`).
  - Optionally adds **Chromaprint / AcoustID** (`fpcalc` + local DB or external AcoustID lookup) behind a flag and optional dependency — tradeoffs: CPU, packaging, and external service policy vs fully local cache.
- Wire via existing `ChainRecognizer`: e.g. `NewChainRecognizer(local, acr, shazam)` when enabled in config.

### Phase 2B — Fingerprint cache after successful cloud match

- After ACRCloud/Shazam success, compute and store a fingerprint (and metadata) keyed by `collection.id`.
- On subsequent plays, **match locally first**; on failure or low score, fall through to cloud as today.

**Cache staleness / “good enough” local trap:** a locally matched **live** or **alternate** pressing might satisfy a coarse fingerprint or duration gate and **never** reach the cloud again, leaving the UI stuck on a suboptimal canonical. Mitigations to design in the same milestone as 2B:

- **Sporadic cloud verification:** e.g. force a full cloud chain on **1 in N** successful local hits (configurable `N`, e.g. 10), or whenever **local confidence age** (wall time or play count since last cloud confirmation) exceeds a threshold.
- **Confidence age / TTL:** store `last_cloud_verified_at` or play counter per cache entry; after TTL or N local-only plays, require cloud for the next attempt.
- **Low-score local:** if local match score or margin is below a conservative cutoff, always fall through to cloud (same spirit as existing confirmation bypass rules).

### Phase 2C — Cost / latency policy

- Gate local attempts on capture length, rate-limit state, or “soft boundary” context so behavior stays predictable on the Pi.
- Combine with **Pi resource** rules above: same gates apply to **skipping** local work under pressure (see Raspberry Pi 5 subsection).

---

## Axis 3 — False-positive diagnostics and user hints (not started; design only)

This axis captures ideas that are **worth planning** but should stay **conservative** in product behavior: use them for telemetry, soft UI hints, and optional policy nudges — not hard errors or intrusive alerts.

### Early re-recognition vs track end (“suspicious timing”)

**Motivation:** Some material (e.g. a cappella or sparse vocals on CD) produces **vocal pauses** that resemble **inter-track silence**, so VU boundaries can fire **well before** the provider-reported track duration. That pattern is a **useful signal** for tuning and for spotting “difficult” albums; it is **not** proof of a bug (wrong metadata, alternate master, short hidden track, gapless segue, or intentional structure can all explain it).

**Planned approach (when implemented):**

- After a boundary-triggered recognition completes, persist **linkage** between the existing `boundary_events` row (or successor) and outcome: same recording ID vs new track vs no match, plus **seek/duration snapshot** at decision time.
- Derive a conservative **“early boundary”** boolean (example only; thresholds TBD): e.g. provider `duration_ms` above a minimum **and** estimated progress below a fraction α of duration **and** boundary `outcome = fired`. Treat as **cohort analytics** first (counts on Listening Metrics, optional “tracks with repeated early boundaries”).
- **Never** auto-block recognition from a single event; prefer **aggregates** and **repeat offenders** (same `collection_id` or stable title/artist key) before suggesting calibration review.

Expose summaries on **Listening Metrics** under the same visibility contract as other recognition stats.

### Per-track user flag (“hints” to recognition / boundaries)

**Motivation:** Operators know problem tracks (e.g. a specific Tracy Chapman cut). A **library-level opt-in** lets the system apply **gentler or stricter** boundary/confirmation behaviour for those rows only, without changing global defaults for everyone.

**Naming (UX / JSON — pick one primary; keep synonyms out of the schema):**

| User-facing label (examples) | Notes |
|------------------------------|--------|
| **Boundary-sensitive** | Short, accurate: VU/track-boundary logic is what struggles. |
| **Challenging for auto-boundaries** | Plain language; slightly long for a chip. |
| **Ambiguous gaps** | Emphasises pause vs track-change confusion. |

**Recommended schema direction:** a single boolean on `collection` (e.g. `boundary_sensitive INTEGER NOT NULL DEFAULT 0`) **or** a small enum if more hints are added later (e.g. `recognition_hint`: `none` | `boundary_sensitive`). The UI should explain in one sentence: *“More vocal pauses may be mistaken for track changes; the system can use stricter checks for this track.”*

**Behaviour (when implemented; all behind explicit user toggle):**

- **Hints**, not mandates: e.g. slightly **longer confirmation** window, **stricter** duration guard for that `collection_id`, or **prefer** continuity / provider duration for suppression — exact mapping is a product decision once telemetry exists.
- Respect **Physical → Vinyl/CD** lag: the flag applies to the **library entry**; if the user changes format later, the hint remains on the same row.
- Show the flag in the **library editor** and optionally a small badge in recognition/history context so operators remember why behaviour differs.

**PR placeholder:** implement after boundary ↔ outcome linkage exists so the flag can be validated against real play data (avoid tuning a hint with no feedback loop).

---

## R2 — Format backfill (implementation notes: bulk updates vs API reads)

When the user bulk-edits format (e.g. **500** library rows **Vinyl** in one action), naïve “one transaction updates everything” work can still stress SQLite **writer throughput** and **lock windows** even with WAL — readers are much less blocked than rollback mode, but **one writer at a time** remains.

**Recommended shape for R2:**

1. **WAL stays on** (already enabled) so `GET` metrics and history pages can continue **concurrent reads** while the writer progresses.
2. **Chunked writes:** apply `UPDATE` in **batches** (e.g. 50–200 rows per transaction, or `WHERE rowid BETWEEN …` pages), **commit** between chunks, and optionally `time.Sleep(1–5ms)` or `runtime.Gosched()` between chunks to yield to readers under interactive load.
3. **Async job, fast HTTP response:** the web save handler should **enqueue** backfill work (in-process queue, or a `pending_backfill` table) and return **quickly** (e.g. 202 Accepted + job id, or success with “reconciliation running” flag). Do not block the browser until all 500 rows are rewritten.
4. **Single writer discipline:** run the heavy backfill from **one goroutine** (or `oceano-state-manager` if library writes are centralized there) to avoid interleaved multi-writer contention on the same `library.db`.
5. **Idempotent updates:** backfill should be safe to **retry** (same target `format_resolved` idempotently) so partial failures after power loss do not corrupt analytics linkage.

This answers the “Listening Metrics API while mass categorisation runs” concern: **WAL + short transactions + async job** keeps the UI responsive; extreme bulk remains an **ops** problem (disk class, off-peak batch) if volumes grow further.

---

## Roadmap: low-confidence matches — primary pick + alternative carousel

### Product idea

When automatic detection returns a **low confidence** match, the UI should:

1. Still **promote the best-ranked result** (highest relevance / score) as the default “now playing” line.
2. Offer a **small horizontal carousel** (or equivalent) of **other candidates** returned by the same identification pass, so the listener can **tap the correct track** without re-running capture.

This targets ambiguous segments (live vs studio, compilations, short samples, noisy vinyl).

### Is it feasible?

**Yes, with provider-specific work.**

| Provider | Multi-candidate data today | Notes |
|----------|----------------------------|--------|
| **ACRCloud** | Response JSON already includes `metadata.music` as an **array** of hits with per-hit **`score`** (0–100). | `internal/recognition/acrcloud.go` currently maps **`Music[0]` only** and discards the rest. Extending the recognizer to return **top N** (e.g. 3–5) ordered by score is straightforward. |
| **Shazam (shazamio)** | The daemon reads **`matches[0]`** for score/duration only. | Shazam’s full JSON may expose **multiple `matches`**; the Python bridge would need to **serialize** additional matches (title/artist/album/shazam id/score if present) for a carousel. Verify against real `recognize()` payloads. |

**Coordinator / state (decision):** extend the **existing** `track` object (or sibling field at the same level as `track` in unified state — match whatever `oceano-state.json` already uses for “now playing”) with an **optional** array:

```json
"recognition_alternatives": [
  { "title": "", "artist": "", "album": "", "score": 0, "acrid": "", "shazam_id": "" }
]
```

Omit the field when `score >= threshold` or when the provider returned a single candidate. **Rationale:** one schema for SSE + file consumers; TTL is implicit (“cleared on next state write / boundary”).

**Prerequisite (before betting UX on Shazam):** log capture or a one-off experiment proving whether **shazamio** returns **multiple** `matches[]` for real vinyl/CD captures. If only one match ever appears, R9’s carousel is **ACR-first**; document outcome in this file and trim R9 scope accordingly.

**Pre-backlog gate (not a PR):** the prerequisite above is **time-boxed investigation** (issue optional, branch optional). It does **not** need its own PR row — **record the conclusion** (single vs multi match, with example payload redacted) in a short subsection **“R9 — Shazam cardinality (resolved)”** in this document **before** R9 enters the active backlog. Until then, treat R9 as **blocked on that write-up**, not on an invisible “spike” dependency.

**Independence:** R9 does **not** depend on **R4 / R5** (local library / fingerprints); it only extends provider parsing + state + UI.

**UI surfaces:** `nowplaying.html` (primary + carousel), optionally the web status row / config “last recognition” debug. **SSE** (`/api/stream`) must carry the extra field when present.

**User correction:** `POST` (or existing library/history endpoint extended) to **apply selected candidate** → update SQLite library row / play history / clear alternatives in state — aligns with **user overrides** principle already in this doc.

**Risks**

- **Extra API payload** on every match if alternatives are always sent — mitigate by **gating** on low score or explicit “always show top-3” flag.
- **Wrong tap** — treat selection as **explicit user_confirmed** (or equivalent) for analytics.
- **Shazam** — if only one match is ever returned, carousel is a no-op for that provider; ACR-only path still delivers value.

### Suggested implementation milestone (new PR row)

Ship **after** stable multi-candidate parsing + state schema; UI can be phase 2.

### Resolution log — Shazam `matches[]` (for R9)

Append a row after the pre-backlog investigation completes. Until then, plan **ACR-first** multi-candidate UX; add Shazam alternatives only if this log shows **multi**-match payloads are available in production.

| Date | Outcome | Notes |
|------|---------|-------|
| — | *pending* | *Replace with finding (e.g. “single match only”) + optional link to redacted sample / version pin.* |

---

## Autonomous calibration: RMS percentile learning (shipped)

### Behaviour

While **Physical** is active, the VU loop classifies **stable silence** (≥ ~1.5 s in the detector’s silence state) vs **stable music** (active audio above a small margin over the global floor) and increments **fixed-width RMS histograms** per format key **`vinyl` / `cd` / `physical`** in the library SQLite table **`rms_learning`**. On each persist interval, counts are saved and **percentiles** derive **silence enter / exit** when silence and music distributions separate (`DeriveSilenceThresholdsFromHistograms` in `internal/library/rms_learning.go`). Thresholds remain **floor-clamped** to **`advanced.vu_silence_threshold`** (R2b).

- **`autonomous_apply`**: when true and minimum sample counts are met, learned enter/exit **override** wizard profile thresholds for the VU boundary detector (including live updates via `SetSilenceEnterExit` after each flush). **`vinyl_transition`** gap metrics from the wizard are **unchanged** and still feed energy-dip behaviour.
- **R3 telemetry nudges** remain additive on top of whichever base (profile or learned) is active before the detector is constructed for a VU socket session; order is documented in `source_vu_monitor.go`.

### Configuration

`advanced.rms_percentile_learning`: **`enabled`**, **`autonomous_apply`**, **`min_silence_samples`**, **`min_music_samples`**, **`persist_interval_secs`**, optional **`histogram_bins`**, **`histogram_max_rms`**. Tune via **`/etc/oceano/config.json`** (iOS / `POST /api/config`); the legacy **Advanced** web page was removed.

### Optional future: dual-policy shadow (R10)

**Not required** for RMS learning. Optional research: periodically compare **Policy A** (current stack) vs a **frozen reference Policy B** on recent `boundary_events` / metrics — opt-out, bounded I/O, suggest-first if ever promoted.

### Interaction today: global VU threshold vs calibration profiles vs RMS learning

`loadBoundaryCalibrationModel` still resolves wizard profiles and R2b/R2c rules first; **persisted `rms_learning`** may then override enter/exit when **`rms_percentile_learning.autonomous_apply`** is on; the in-process learner continues to refine histograms during the VU session.

### VU reconnect (implemented behaviour)

After each **new connection** to the VU Unix socket, the monitor **suppresses the first `silence→audio` boundary only** (`source_vu_monitor.go`) — separate from calibration. It reduces false triggers after stream/detector restarts (`unexpected EOF`) but **does not** replace threshold tuning when hum ramps **above** the effective silence threshold on a **single** stable connection.

---

## Suggested PR sequence

### Completed / aborted (archive — do not use for sprint planning)

| PR | Scope | Risk | Status |
|----|--------|------|--------|
| R1 | `boundary_events` (or equivalent) + linkage ids for backfill; no trigger behavior change | Low | **Done** (on `main`) |
| R1b | (same milestone) Expose aggregates on **Listening Metrics** (API + `history.js`) — even a minimal “boundary events logged / period” card | Low | **Done** |
| R1c | Coalesce redundant **silence→audio** in early segment of known track — **aborted** (too aggressive); retry behind flag / per-track hint; legacy DB rows + UI label (see status) | Low | **Aborted** |
| R2 | Backfill **`format_resolved`** / **`format_resolved_at`** on **`boundary_events`** when the user saves Vinyl/CD/Unknown for a library row (`LibraryDB.update`; async bulk-edit queue remains future-only per plan notes) | Low | **Done** (`feat/recognition-enhancement-plan`) |
| R2b | **Calibration floor clamp** in `loadBoundaryCalibrationModel`: profile-derived enter/exit never below global `fallbackSilenceThreshold`; hysteresis preserved | Low | **Done** (`feat/recognition-enhancement-plan`) |
| R2c | **Minimum off→on gap** ε for profile-derived thresholds; tiny-gap profiles fall back to global thresholds; load-time log | Low | **Done** (`feat/recognition-enhancement-plan`) |
| R7 | Link **`boundary_events`** to post-recognition outcomes (`followup_*`, `early_boundary`); VU path inserts fired row **before** enqueueing recognition so rows carry stable ids; coordinator writes outcomes (matched / no_match / errors / skipped / same-track restored); Metrics API + **Listening Metrics** UI | Medium | **Done** (`feat/recognition-enhancement-plan`) |
| RMS-L | — | **RMS percentile learning:** `rms_learning` histograms + `autonomous_apply` VU threshold override; `rms_percentile_learning` in config + Advanced UI; `SetSilenceEnterExit` live updates | Medium | **Done** |
| RMS-V | **RMS-L** | **Visual feedback for RMS learning:** `/api/recognition/rms-learning` extended with `readiness_level`, `silence_pct`, `music_pct`, `min_silence_samples`, `min_music_samples`, `autonomous_apply`; snapshot widget replaced with per-format cards (status chip + progress bars + derived thresholds); CSS in `index.css` | Low | **Done** |

### Active backlog

| PR | Depends on / prerequisite | Scope | Risk | Status |
|----|---------------------------|--------|------|--------|
| R3 | — | Optional telemetry-based bounded nudges (same_track_restored vs matched + P75 seek/duration on matched fires); **off** by default via `advanced.r3_telemetry_nudges`; R3 `silenceNudge` kept fresh via `telemetryRefreshInterval = 24 h` soft-reconnect in `readVUFrames` (no separate goroutine needed) | Low–medium | **Done** (`feat/recognition-enhancement-plan`) |
| R4 | — | `LocalLibraryRecognizer` + config flag + tests | Medium | Pending |
| R4b | **R4** | Extend `/api/recognition/stats` (or equivalent) + metrics UI for **local vs cloud** attempt/match counts; optional **ACR error class** breakdown (timeout vs rate limit vs DNS) for operator health | Low–medium | Pending |
| R5 | — | Post-match fingerprint persistence + local lookup; **cloud re-verify** policy (TTL, 1-in-N plays, or low local score → cloud) bundled with cache | Medium | Pending |
| R6 | — | Offline-trained classifier for boundary confidence (optional) | Medium–high | Pending |
| R6b | **R6** | If R6 ships: model health / confidence distribution on metrics page (optional chart or percentile text) | Medium | Pending |
| R8 | — | Library **per-track hint** (recommended label: *Boundary-sensitive*; schema e.g. `boundary_sensitive`) + web UI + state-manager consumption for optional policy nudges | Medium | **Done** (`feat/recognition-enhancement-plan`) |
| R9 | **Pre-R9 investigation** documented in this file (Shazam `matches[]` cardinality — see **Low-confidence UX** + pre-backlog gate above). **Not** blocked on R4/R5 | **Low-confidence UX:** parse ACRCloud `metadata.music[1..]` (and Shazam multi-match **if** investigation showed it is feasible) → optional `recognition_alternatives` in state + threshold config; **now playing carousel** + API to **apply user-selected candidate** (library/history integration) | Medium | Pending |
| R10 | Optional hardening | **Dual-policy shadow (optional):** compare active calibration vs frozen **reference** defaults on recent telemetry; suggest-only or gated promotion; **not** required now that **RMS percentile learning** covers autonomous silence thresholds | High | **Research** — optional; do not block on shadow for RMS learning |

---

## Risks

- **SD card wear / I/O** — high-frequency append-only telemetry and future local fingerprint stores increase **write volume** on typical Pi microSD; mitigate with **WAL** (already on), **bounded write batching**, optional DB relocation to better media, and monitoring free space (see **Operational persistence** above).
- **False local matches** (alternate takes, live vs studio) — keep cloud confirmation for ambiguous scores; with a **fingerprint cache (2B)** the additional risk is **permanent local short-circuit** of cloud correction unless **sporadic verification** or **confidence age / TTL** (see 2B) is implemented from day one of caching.
- **Physical format lag** — all analytics and models must support **late correction** (see above).
- **Dependencies on Pi** — prefer optional components (like Shazam env) and clear install docs.
- **“Early boundary” heuristics** — easy to over-interpret; keep as analytics until validated on real collections; never block playback or recognition on a single signal.
- **RMS histogram learning** — `rms_learning` writes are **batched** on `persist_interval_secs` (default ~2 min), not per VU frame; histogram size is bounded (bins × JSON). Disable **`autonomous_apply`** if behaviour regresses after a hardware change.
- **Auto-tuned calibration (R10 shadow, if ever shipped)** — dual-policy comparison remains optional; mitigate with bounded I/O and opt-out ([Operational persistence](#operational-persistence--sqlite-sd-wear-and-telemetry-volume)).

---

## Immediate next step

See **[Priority (concrete next work)](#priority-concrete-next-work)** — next suggested milestone: **R4** (local library recognizer) or **R5**; **R2**, **R3**, **R7**, and **R8** are archived as **Done** above.
