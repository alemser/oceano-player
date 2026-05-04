# Discogs Integration — Implementation Plan

**Language:** English (repository convention).

**Owner repo:** `oceano-player` (backend contract owner)  
**Downstream:** `oceano-player-ios` (strict consumer; additive contract only).

**Primary reference:** `docs/reference/discogs-integration.md`

---

## Goal

Deliver Discogs as an **optional, post-recognition enrichment provider** for physical media metadata (vinyl/CD context), without changing recognition hot-path semantics or breaking current API/config consumers.

---

## Delivery model

Work is split into small PRs with explicit acceptance criteria and completion checklist.

### PR1 — Config foundation (safe, no runtime calls)

**Status:** Complete (2026-05-04)

**Scope**
- Add `recognition.discogs` config block to backend config models.
- Keep defaults conservative (`enabled=false`).
- Ensure read/write persistence through `GET/POST /api/config`.
- Parse Discogs config into state-manager runtime config (for future PR wiring).

**Files (expected)**
- `cmd/oceano-web/config.go`
- `cmd/oceano-state-manager/config_types.go`
- `cmd/oceano-state-manager/recognition_config_load.go`
- Tests in `cmd/oceano-web/*_test.go` and/or `cmd/oceano-state-manager/*_test.go`

**Acceptance criteria**
- Config remains backward-compatible when `recognition.discogs` is absent.
- Saving config preserves Discogs keys and defaults.
- No Discogs network calls are introduced in this PR.

**Checklist**
- [x] `recognition.discogs` struct added in web config model.
- [x] `recognition.discogs` struct added in state-manager runtime config model.
- [x] Config loader normalizes defaults when fields are omitted.
- [x] Tests updated/added for defaulting and persistence.
- [x] PR1 marked complete in this plan.

---

### PR2 — Discogs client + async enrichment hook

**Status:** Complete (2026-05-04)

**Scope**
- Add Discogs HTTP client module (timeouts, retry policy, rate-limit handling, user agent).
- Trigger enrichment asynchronously after successful recognition acceptance.
- Keep recognition-state updates non-blocking even if Discogs fails.

**Files (expected)**
- `internal/recognition/discogs_client.go` (new)
- `cmd/oceano-state-manager/discogs_enrichment.go` (new)
- `cmd/oceano-state-manager/recognition_coordinator.go`
- Supporting tests

**Acceptance criteria**
- Recognition hot path latency is unaffected by Discogs availability.
- Discogs failures degrade gracefully (log + skip, no playback/state regression).
- Candidate selection is deterministic and conservative.

**Checklist**
- [x] Discogs client added with bounded timeout/retry.
- [x] Async enrichment trigger wired post-recognition.
- [x] Deterministic scoring/match policy implemented.
- [x] Unit tests cover client + scoring + failure fallback.
- [x] PR2 marked complete in this plan.

---

### PR3 — Persistence + additive API/state exposure

**Status:** Complete (2026-05-04)

**Scope**
- Add additive Discogs fields to library schema.
- Persist selected enrichment fields.
- Expose optional Discogs metadata in API/state surfaces where appropriate.

**Files (expected)**
- `internal/library/library.go`
- `cmd/oceano-web/library.go`
- `cmd/oceano-web/state_stream_payload.go`
- Documentation updates + tests

**Acceptance criteria**
- Schema migration is additive and safe for existing installs.
- Existing clients keep working when Discogs fields are absent.
- No silent contract drift for iOS.

**Checklist**
- [x] Additive DB migration for Discogs fields (`discogs_url TEXT` on `collection`).
- [x] Persistence/read models updated (`CollectionEntry.DiscogsURL`, `UpdateDiscogsEnrichment`, all SELECT queries).
- [x] API/state exposure remains optional/additive (`track.discogs_url omitempty`, `LibraryEntry.DiscogsURL omitempty`).
- [x] `docs/cross-repo-sync.md` updated with iOS follow-up entry.
- [x] PR3 marked complete in this plan.

---

### PR4 — Optional backfill/re-enrichment worker (post-MVP)

**Status:** Planned (optional)

**Scope**
- Background re-enrichment of older library rows with missing Discogs fields.
- Throttled worker, resumable cursor, request budget controls.

**Checklist**
- [ ] Worker implemented with bounded concurrency.
- [ ] Resume cursor + budget/rate control added.
- [ ] Operational docs added.
- [ ] PR4 marked complete in this plan.

---

## Cross-repo contract guardrails (mandatory)

For any API/config/state behavior change in this roadmap:
- Keep changes **additive** by default.
- Update `docs/cross-repo-sync.md` in the same PR.
- Include iOS follow-up notes (affected modules + risk level).
- Avoid payload key renames/removals without compatibility path.

---

## Progress log

| Date | PR | Update |
|------|----|--------|
| 2026-05-04 | PR1 | Plan created, PR1 started (config foundation). |
| 2026-05-04 | PR1 | Completed: Discogs config model + normalization + tests in web/state-manager. |
| 2026-05-04 | PR2 | Completed: Discogs HTTP client + async post-recognition enrichment hook + tests. |
| 2026-05-04 | PR3 | Completed: DB migration + CollectionEntry/LibraryEntry + UpdateDiscogsEnrichment + TrackInfo.DiscogsURL + state/API exposure. |
| 2026-05-04 | PR3 | Post-review closure: fixed `discogs_url` rehydration from library, added missing PR3 tests (state/library/web), and updated main docs (`README.md`, recognition/http references). |

