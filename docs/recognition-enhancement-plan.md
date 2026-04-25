# Recognition Enhancement Plan (Triggers + Local Identification)

This document extends the earlier discussion into a concrete, incremental roadmap. It is designed to preserve today’s stable behavior (VU monitor, calibration, coordinator, ACRCloud/Shazam chain, library) while improving precision over time.

**Related work branch:** `recognition-phase2-precision` — use this branch for phased experiments and PRs aligned with this plan.

---

## Design principles (avoid regressions)

- **Feature flags / config toggles** for any new trigger logic or recognizer stages (e.g. local-first off by default).
- **Separate concerns:** (1) *when* to capture and fire triggers vs (2) *who* identifies the track (local vs cloud).
- **Telemetry before policy changes:** log structured events and outcomes before tightening thresholds in production.
- **Small PRs:** telemetry → optional local recognizer → optional statistical layer → optional ML-lite.

---

## Listening Metrics page (visibility contract)

The **Listening Metrics** screen (`cmd/oceano-web/static/history.html`, title *Listening Metrics*) is the main place operators see whether recognition and listening behaviour are healthy. Today it combines:

| Surface | API | Client |
|---------|-----|--------|
| Period plays, hours, top artists/albums, plays/hours by source | `GET /api/history/stats` | `cmd/oceano-web/static/history.js` (`loadStats`, `renderStats`, …) |
| Stylus wear / hours (when enabled) | `GET /api/stylus` | `history.js` (`loadStylusSummary`, `renderStylusSummary`) |
| Recognition provider counters (incl. **Trigger** boundary vs fallback timer, attempts/matches per provider) | `GET /api/recognition/stats` | `history.js` (`loadRecognitionStats`) — handler in `cmd/oceano-web/library.go` |

**Plan rule:** any optimisation that adds telemetry, a new recogniser stage, or new trigger semantics should **ship in the same change set** (or a immediately following PR) with:

1. **Persistence** — counters or rows the metrics page can aggregate (extend `recognition_summary`, `play_history`, or new tables as needed).
2. **API** — extend `playHistoryStatsResponse` / `/api/history/stats` and/or `/api/recognition/stats` (or a dedicated `GET` under `/api/recognition/…`) so the data is machine-readable and versioned.
3. **UI** — update `history.html` / `history.js` (and `history.css` if present) so new metrics are **visible**: cards, rows, or a short “health” summary (e.g. local-match rate, boundary suppression rate, backfill-corrected format counts).

This keeps “what is working well” obvious without reading logs on the Pi. Treat **empty states and failure copy** the same way as existing recognition stats (placeholder when no data or API errors).

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

For each boundary-related event (fired, suppressed hard/soft, coordinator skip), persist structured fields, for example:

- RMS summary around the transition (pre / during / post), silence duration, simple variance or “noise floor” proxies (live material often differs from steady studio pressings).
- `format_at_event` / `format_resolved` (see above), approximate seek, provider-reported duration when available.
- Outcome: trigger led to capture? match same track / new track / no match? confirmation path taken?

Store in a dedicated table (e.g. `boundary_events`) or extend existing telemetry (`recognition_summary` / `play_history`) with clear foreign keys so **late format correction** can backfill.

### Phase 1B — Statistical layer (before “ML”)

- Start with **percentiles and rolling aggregates** per cohort (`Vinyl`, `CD`, unresolved `Physical`) to suggest safer defaults for silence frames, thresholds, and guard windows.
- Optional **lightweight classifiers** (logistic regression, small decision trees) trained **offline**; at runtime apply only **threshold nudges** within safe bounds, with fallback to current fixed calibration when confidence is low.

### Phase 1C — ML-lite (only when data supports it)

- Same features as 1B; export a small model (e.g. sklearn → ONNX or hand-rolled rules) and run **cheap inference** on the Pi, or even table lookup by feature bins initially.
- Always keep **legacy path** when model confidence is below a cutoff or sample count is insufficient.

### Phase 1D — Stylus / groove wear (product)

**Note:** Groove noise / stylus-oriented signals and UI already exist in the project. This plan treats 1D as **extend with longitudinal statistics** (trend + hysteresis + conservative messaging), not as greenfield. New work should integrate with existing calibration and display flows rather than duplicating parallel concepts.

---

## Axis 2 — Local identification before ACRCloud / Shazam

Captured WAV is already available for each attempt. An optional **local-first** stage can reduce API usage and latency when the collection already knows the recording.

### Phase 2A — `LocalLibraryRecognizer` (new `Recognizer`)

- Implement `Recognize(ctx, wavPath)` in `internal/recognition` (or adjacent package) that:
  - Uses **cheap signals first**: duration, optional ISRC from last successful match, library keys (`acrid`, `shazam_id`).
  - Optionally adds **Chromaprint / AcoustID** (`fpcalc` + local DB or external AcoustID lookup) behind a flag and optional dependency — tradeoffs: CPU, packaging, and external service policy vs fully local cache.
- Wire via existing `ChainRecognizer`: e.g. `NewChainRecognizer(local, acr, shazam)` when enabled in config.

### Phase 2B — Fingerprint cache after successful cloud match

- After ACRCloud/Shazam success, compute and store a fingerprint (and metadata) keyed by `collection.id`.
- On subsequent plays, **match locally first**; on failure or low score, fall through to cloud as today.

### Phase 2C — Cost / latency policy

- Gate local attempts on capture length, rate-limit state, or “soft boundary” context so behavior stays predictable on the Pi.

---

## Suggested PR sequence (can map to `recognition-phase2-precision`)

| PR | Scope | Risk |
|----|--------|------|
| R1 | `boundary_events` (or equivalent) + linkage ids for backfill; no trigger behavior change | Low |
| R1b | (same milestone) Expose aggregates on **Listening Metrics** (API + `history.js`) — even a minimal “boundary events logged / period” card | Low |
| R2 | Backfill job when user updates Vinyl/CD classification; docs + tests | Low |
| R3 | Optional percentile-based nudges to calibration inputs (bounded) | Low–medium |
| R4 | `LocalLibraryRecognizer` + config flag + tests | Medium |
| R4b | Extend `/api/recognition/stats` (or equivalent) + metrics UI for **local vs cloud** attempt/match counts | Low–medium |
| R5 | Post-match fingerprint persistence + local lookup | Medium |
| R6 | Offline-trained classifier for boundary confidence (optional) | Medium–high |
| R6b | If R6 ships: model health / confidence distribution on metrics page (optional chart or percentile text) | Medium |

---

## Risks

- **False local matches** (alternate takes, live vs studio) — keep cloud confirmation for ambiguous scores.
- **Physical format lag** — all analytics and models must support **late correction** (see above).
- **Dependencies on Pi** — prefer optional components (like Shazam env) and clear install docs.

---

## Immediate next step

Open **`recognition-phase2-precision`** and land **R1 only**: append-only telemetry with stable IDs and `format_at_event` / `format_resolved` columns, **without** changing when boundaries fire. That unlocks everything else with minimal regression risk.

Include **R1b** in the same milestone when feasible: wire the first aggregates to **Listening Metrics** (`/api/history/stats` or `/api/recognition/stats` + `history.js`) so new data is visible from day one.
