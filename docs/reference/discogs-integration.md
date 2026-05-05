# Discogs integration opportunities for Oceano Player

This document explains how Discogs can add value to Oceano Player recognition, library quality, and user workflows without replacing the current audio-identification providers.

## Why Discogs is relevant here

Discogs is strongest for **release-level metadata** (pressings, catalog numbers, labels, track positions, sides, year/country, credits).  
Oceano's existing recognizers (ACRCloud, optional `shazamio`, AudD) are strongest for **"what is this audio clip?"**.

The best combined strategy is:

1. Use current providers to identify the recording quickly.
2. Use Discogs to enrich and disambiguate release details for physical media (especially vinyl and CD).

## What Discogs can improve in this project

### 1) Better physical metadata quality

After a successful recognition, Discogs can enrich:

- release title variants and canonical artist naming
- label and catalog number (`catno`)
- release year and country
- genres/styles useful for browsing
- detailed tracklist position (`A1`, `B2`, `CD1-03`, etc.)

This is high-value for vinyl/CD sessions where users care about release context, not only track title.

### 2) Side-aware display (vinyl UX)

Discogs track positions often include side hints (`A`, `B`, `C`, `D`).  
This can improve:

- "Vinyl side + track" chips in Now Playing
- confidence for boundary-sensitive behavior by side transitions
- library browsing grouped by side/order

### 3) Better conflict resolution between providers

When providers disagree (or return sparse metadata), Discogs can act as a metadata arbiter:

- normalize title/artist variants
- prefer release metadata that matches known catalog/format
- reduce visible metadata oscillation between recognition attempts

### 4) Local-library matching quality

Discogs IDs (release/master) can become stable keys to merge equivalent rows and avoid duplicates in `collection`.

## What Discogs should not be used for

- **Primary clip recognition** in the hot path (not designed as low-latency mystery-clip matcher).
- Replacing ACRCloud/Shazam-style providers for first identification.
- Tight real-time loops on every VU event (API quotas and latency are a bad fit).

## Integration model that fits Oceano architecture

## Stage A (recommended first): post-recognition enrichment

Trigger only after a provider match is accepted:

1. `recognition_coordinator` obtains a track result.
2. enqueue async enrichment job (non-blocking for state updates).
3. fetch Discogs candidates using artist/title/(album optional).
4. resolve best candidate with deterministic scoring rules.
5. fetch the selected **release** resource and match the recognized track title against the release **tracklist** to obtain `track_number` (e.g. CD index `3`, vinyl position `A2`).
6. persist selected Discogs fields into library (including additive `track_number` when matched).

Important: if Discogs fails, playback state still updates normally.

### Track position (`track_number`) normalization

Discogs `tracklist[].position` values are passed through `CanonicalDiscogsTrackPosition` before they reach `/tmp/oceano-state.json` and the library:

| Examples from Discogs | Stored value | Notes |
|----------------------|--------------|--------|
| `1`, `12` | `1`, `12` | Pure CD index — unchanged. |
| `a2`, `A-2`, `B.3` | `A2`, `B3` | Side letter + track index — uppercase side, strip redundant separators. |
| `2A`, `3d`, `12-A` | `2A`, `3D`, `12A` | Index + side letter — uppercase letter (`3D` matches the “digit + side” pattern). |
| `CD1-3`, `1-11`, `cd2-11` | Same (trim/collapse spaces only) | Multi-disc / compound labels — no structural rewrite. |

The HDMI Now Playing UI (`parseVinylTrackRef` in `static/nowplaying/helpers.js`) recognises **vinyl-style** refs with sides **A–D** for split chips (“Side X · Track Y”). Numeric-only refs render as “Track N”; sides **E+** or uncommon formats still show the raw `track_number` string on the chip row.

Library edits via `PATCH` on `/api/library/...` apply the same `CanonicalDiscogsTrackPosition` rules when saving `track_number`, matching the uppercase convention used by the iOS app (e.g. `1a` → `1A`).

## Stage B (optional): re-enrichment for existing library

Background job for rows missing `label/catno/side-position`.

- bounded worker pool
- resumable cursor
- throttled requests

## Stage C: proactive release confirmation carousel

For unconfirmed physical tracks, the system proactively offers the user a choice of release candidates rather than waiting for a manual correction action. The library grows naturally during normal listening sessions without requiring deliberate curation.

### Flow

1. Track is recognised (ACRCloud/AudD) → best Discogs candidate is applied as usual.
2. Entry is written to `collection` with `confirmed = false`.
3. iOS detects `confirmed = false` on a physical source track (CD/Vinyl) → shows a non-blocking release carousel automatically (e.g. bottom drawer, does not interrupt playback).
4. Carousel displays top N candidates (recommended: 5) ordered by Discogs score, each showing: artwork thumbnail, album title, label, year, country, format.
5. User confirms the pre-selected candidate or picks another → `POST /api/library/:id/select-release` → `confirmed = true`, selected release fields written to library.
6. From this point the library entry is authoritative: future recognitions of the same track never overwrite curated fields.

### Rules

- Carousel only appears for physical sources (CD, Vinyl, Physical). AirPlay/Bluetooth have no pressing ambiguity.
- Only shown after recognition succeeds (`phase = identified`), never while identifying.
- If the user dismisses or ignores, do not re-prompt for the same track within the same listening session.
- Already-confirmed entries (`confirmed = true`) never trigger the carousel.

### Backend pieces required

**Candidate storage**
`pickBestDiscogsResult` currently discards all candidates except the best. Needs to return (or separately store) the top N scored candidates while the entry is unconfirmed. Two options:
- **Re-query on demand** — simpler; `GET /api/library/:id/release-candidates` triggers a fresh Discogs search. One extra API call per user interaction.
- **Persist top N at enrich time** — store candidates as JSON in a `discogs_candidates_json` column; no extra Discogs call when the user opens the carousel. Preferred for offline/rate-limit resilience.

Recommended: persist at enrich time, clear `discogs_candidates_json` after confirmation.

**New endpoints**
- `GET /api/library/:id/release-candidates` — returns stored candidates (or triggers re-query if column is empty).
- `POST /api/library/:id/select-release` — writes selected candidate fields to library, sets `confirmed = true`, clears `discogs_candidates_json`.

**`confirmed` field**
Already exists in `collection`. No schema change needed for the confirmation flow itself; only `discogs_candidates_json` is additive.

### iOS contract

SSE / `GET /api/status` payload includes `confirmed` on the `track` object (already present). iOS reads this field to decide whether to show the carousel. No new SSE fields required.

Carousel data comes from `GET /api/library/:id/release-candidates` — called once when the carousel is shown, not on every SSE tick.

### Lifecycle and library authority

Once `confirmed = true`:
- `recognition_coordinator` skips field updates for curated fields (`album`, `label`, `released`, `artwork_path`) on future recognitions of the same entry.
- Strategy B (`library_album_priority`) automatically uses this entry as the ground truth.
- After a few listening sessions the collection reaches critical mass and the carousel stops appearing for known tracks.

## Suggested data fields (additive)

Potential `collection` additive fields:

- `discogs_release_id`
- `discogs_master_id`
- `discogs_resource_url`
- `discogs_label`
- `discogs_catno`
- `discogs_country`
- `discogs_year`
- `discogs_genres_json`
- `discogs_styles_json`
- `discogs_track_position` (ex: `A2`, `B1`)

These should be optional and nullable to preserve backward compatibility.

## Matching strategy (deterministic and conservative)

Use a weighted score, for example:

- exact/near title match
- artist token overlap
- optional album overlap
- optional format hints (Vinyl/CD)
- optional track position consistency

Conservative rule: if confidence is below threshold, do not overwrite existing curated metadata automatically.

### Release-type penalties (backlog)

The current `scoreDiscogsCandidate` only adds positive scores (artist/title/album/format/year bonuses).
It does not penalize releases that are unlikely to be what the user is physically playing.

Known false-positive patterns:
- **Compilations** — Discogs `format` array contains `"Compilation"`; title often contains "Best Of", "Greatest Hits", "Collection". A user playing a studio album vinyl should not get enriched with compilation metadata.
- **Live albums** — Discogs `style` array contains `"Live"` or title contains "Live at…", "Live in…". Recognizers match live recordings against the same ACRCloud fingerprint as the studio version but the release context is wrong.
- **Unofficial / bootleg pressings** — `format` contains `"Unofficial Release"` or `"Bootleg"`.

Planned scoring adjustments (not yet implemented):
- subtract ~20 pts when `format` contains `"Compilation"`
- subtract ~15 pts when `style` or title contains live indicators
- subtract ~10 pts for unofficial/bootleg formats
- add ~10 pts when `format` contains `"Album"` (studio album preference)

This is a targeted change to `scoreDiscogsCandidate` in `internal/recognition/discogs.go` with coverage via the existing `TestPickBestDiscogsResult` matrix test.

### Release disambiguation without Discogs (backlog)

When Discogs is disabled the scoring penalties above never run. The album field comes directly from ACRCloud/AudD, which may already carry the wrong release context ("Best of...", "Live at..."). Two complementary strategies are planned, both user-configurable:

**Strategy A — suspicious album filter (`album_filter_heuristics`)**
Before persisting enriched metadata, apply a text heuristic pass on the `album` field. If the value matches known compilation/live patterns (e.g. contains "Best Of", "Greatest Hits", "Live at", "Live in", "Collection", "Anniversary Edition") the field is dropped and stored as empty rather than written with a likely-wrong value. `title` and `artist` are always kept. This is provider-agnostic, lightweight, and safe as a default.

**Strategy B — library-priority override (`library_album_priority`)**
When the recognised track matches an existing entry in the local collection (by title + artist), the library's `album` value takes precedence over whatever the recognition provider returned. Requires the collection to be populated first; grows more useful over time as the user curates their library.

Both strategies are independent and can be combined. Suggested config shape (additive, all optional):

```json
{
  "recognition": {
    "album_disambiguation": {
      "filter_heuristics": true,
      "library_priority": false
    }
  }
}
```

Recommended default: `filter_heuristics: true`, `library_priority: false` (safe out-of-the-box). Users with a well-curated collection can enable `library_priority` to get the most accurate release context without needing Discogs at all.

Implementation notes:
- Strategy A lives in the recognition coordinator, after result is accepted and before `UpdateEnrichmentPatch` is called. No new packages required.
- Strategy B reuses the existing `syncFromLibrary` / `FindPhysicalMatch` path; add precedence logic when `library_album_priority` is true.
- Both strategies should be no-ops when the field is already empty, and must never overwrite user-curated library entries.

## API and operational considerations

- Discogs uses HTTP with rate-limit headers (respect server-provided limits).
- Add per-provider timeout and retry policy with jitter.
- Cache successful lookups locally to avoid repeated calls.
- Keep an explicit user agent string and token handling policy.

## Security and compliance

- Treat Discogs token as BYOK credential, similar to other providers.
- Document outbound data (metadata query terms, not raw audio).
- Do not make Discogs mandatory for baseline functionality.

## Commercial version implications

For a commercial SKU, Discogs integration must be treated as a licensed external dependency, not just a technical feature.

### Recommended commercial credential model: user account (BYOA)

Preferred model for commercial deployment:

- each Oceano user connects their **own Discogs account/token**
- enrichment requests run under that user credential
- no shared "global Oceano Discogs key" is required

Why this is preferred:

- clearer ToS posture for distributed commercial devices
- per-user quotas/rate limits stay scoped to the user account
- lower legal and operational risk versus a vendor-shared key
- easier to offer Discogs as optional premium enrichment

Implementation posture:

- keep Discogs integration optional (`enabled=false` until user connects)
- expose clear connection state (`not_connected`, `connected`, `token_invalid`, `rate_limited`)
- never block core recognition when user Discogs auth is absent/invalid
- support disconnect/revoke and token refresh paths

### Key implications

- **Terms and attribution:** verify Discogs API terms, branding, and required attribution text in product UI/docs.
- **Rate-limit reliability risk:** commercial fleets can exceed limits quickly if requests are not cached/throttled.
- **Operational support burden:** enrichment mismatches become customer-support tickets ("wrong pressing shown").
- **Regional/privacy review:** outbound metadata lookups may require explicit disclosure in privacy/legal flows.
- **Vendor-change risk:** API behavior/policies can change; commercial roadmap needs contingency paths.

### Strategies to make commercial adoption easier

1. **Feature-tier gating**
   - Keep Discogs as an optional enrichment module (`enabled=false` by default for baseline deploys).
   - Allow SKU-level toggles (OSS/community/commercial presets).

2. **Strong fallback behavior**
   - Never block recognition/state updates when Discogs is unavailable.
   - Persist last-known-good metadata and mark enrichment freshness separately.

3. **Cost and quota controls**
   - Add request budgets per day/month and burst limits.
   - Prefer cache-first lookups and background batch enrichment.

4. **Attribution and legal readiness**
   - Centralize attribution strings and provider disclosures in one config/doc source.
   - Add release checklist item for third-party metadata compliance.

5. **Supportability tooling**
   - Store enrichment provenance (`source=discogs`, match score, selected candidate id).
   - Expose simple "why this release was chosen" diagnostics for support/iOS/admin UI.

6. **Commercial-safe architecture**
   - Keep a provider abstraction so Discogs can be swapped/disabled without schema breakage.
   - Use additive fields and nullable columns to avoid hard coupling to one vendor.

## iOS / downstream impact

If new Discogs fields are added to API/state payloads:

- keep changes additive
- update `docs/cross-repo-sync.md`
- provide fallback behavior when fields are missing

No breaking change should be introduced for existing iOS flows.

### iOS config UX recommendation (MVP)

For the first iOS rollout, keep Discogs settings minimal:

- expose only `recognition.discogs.enabled` (toggle)
- expose only `recognition.discogs.token` (secure BYOK field)
- defer advanced knobs (`timeout_secs`, `max_retries`, `cache_ttl_hours`) to a later iteration

This mirrors the provider-oriented setup experience while keeping Discogs activation simple and safe.

## Risks and mitigations

- Wrong pressing selected -> keep confidence threshold + manual override.
- Rate-limit bursts -> queue + cache + backoff.
- Metadata drift over time -> store source and update timestamp.

## Recommended rollout order

1. Add internal Discogs client (disabled by default).
2. Add post-recognition enrichment behind config flag.
3. Add persistence fields (additive migration) and telemetry.
4. Validate on real vinyl/CD sessions.
5. Only then expose optional UI controls.

## Success metrics

- Reduced manual metadata corrections per week.
- Higher completeness of library rows (`label/catno/year/track_position`).
- Stable Now Playing metadata during physical sessions.
- No regression in recognition latency perceived by users.
- Commercial KPI: low enrichment-related support incidents per active device/month.

---

## Addendum: Decoupled and configurable metadata/artwork provider chain

This addendum defines the implementation plan for evolving the current Discogs integration
into a fully decoupled metadata + artwork enrichment pipeline, similar to recognition providers.

### Goals

1. Make metadata/artwork providers pluggable and orderable.
2. Support fallback chains when a provider returns no result or errors.
3. Keep behavior deterministic via explicit merge policies.
4. Preserve backwards compatibility for current API clients (including iOS).

### Target architecture

Keep recognition and enrichment as separate concerns:

- **Recognition pipeline** identifies the recording (`acrcloud`, `audd`, `shazam`).
- **Metadata enrichment pipeline** runs after accepted recognition and enriches:
  - album / label / released / track number
  - artwork
  - provider provenance fields

### Proposed internal contract

Create a provider-agnostic contract (e.g. `internal/metadata/types.go`):

- `EnrichmentRequest`:
  - `title`, `artist`, `album`, `format`
  - provider IDs if known (`acrid`, `shazam_id`, `isrc`)
- `EnrichmentPatch` (all fields optional):
  - `album`, `label`, `released`, `track_number`
  - `discogs_url`
  - `artwork` (URL and/or persisted local path contract)
  - `provider`, `confidence`
- `MetadataProvider` interface:
  - `Name() string`
  - `Enrich(ctx, req) (*EnrichmentPatch, error)`

Provider rule: providers return patches only; persistence happens in one coordinator.

### Config model (chain + order + policy)

Add `metadata_enrichment` to config:

```json
{
  "metadata_enrichment": {
    "enabled": true,
    "merge_policy": "fill_missing_then_stop",
    "providers": [
      { "id": "provider_payload", "enabled": true, "roles": ["metadata", "artwork"] },
      { "id": "itunes", "enabled": true, "roles": ["artwork"] },
      { "id": "discogs", "enabled": true, "roles": ["metadata", "artwork"] }
    ],
    "artwork": {
      "enabled": true,
      "download_timeout_secs": 10
    }
  }
}
```

Recommended merge policies:

- `first_success`
- `fill_missing_then_stop` (recommended default)
- `collect_all_best_effort`

### Coordinator behavior

Implement a `MetadataChain` coordinator that:

1. Executes providers in configured order.
2. Applies explicit merge policy.
3. Maintains per-provider telemetry (`attempt`, `success`, `no_match`, `error`, `rate_limited`).
4. Uses bounded retries/backoff for transient failures.
5. Marks state dirty only when the merged payload changes output-visible fields.

### Data model evolution (additive)

Keep additive migrations only; avoid breaking existing payloads.

Near-term additive fields (optional but recommended):

- `metadata_provider`
- `metadata_updated_at`
- `artwork_provider`
- `artwork_updated_at`

Discogs-specific hardening (optional):

- `discogs_release_id`
- `discogs_master_id`
- `discogs_match_score`
- `discogs_enriched_at`

### Provider rollout order

1. `provider_payload` (reuse recognized metadata, no external call)
2. `itunes` (artwork fallback)
3. `discogs` (metadata + optional artwork from Discogs image fields)
4. `musicbrainz` (future; optional metadata normalization)

### Backup / restore compatibility requirements

Any metadata/artwork provider evolution must preserve restore safety:

1. Restore must not leave DB schema/version drift.
2. If `library.db` is replaced, state-manager must reopen the DB (service restart or equivalent safe handoff).
3. Forward compatibility: older backups must either migrate cleanly or be explicitly reconciled at startup.
4. Preflight compatibility checks should remain available before restore.

### iOS / API compatibility constraints

- Keep all API changes additive.
- Do not remove or rename existing fields without a compatibility path.
- Record contract-impacting changes in `docs/cross-repo-sync.md`.
- Keep fallback behavior explicit when enrichment fields are unavailable.

### Verification checklist (mandatory)

For chain/config changes:

- unit tests for provider ordering and merge policy
- fallback tests for `no_match`, error, rate-limit
- persistence tests ensuring user-curated metadata is not overwritten
- restore regression tests (old backup -> startup -> no schema conflict)
- `go test ./cmd/oceano-state-manager/... -short`
- `go test ./cmd/oceano-web/...`

### Incremental PR plan

1. **PR 1**: contracts + config loader + no-op coordinator scaffold
2. **PR 2**: payload + iTunes providers in chain
3. **PR 3**: Discogs provider migration to chain + telemetry wiring
4. **PR 4**: additive provenance fields + docs/cross-repo sync updates

This staged approach keeps runtime behavior stable while enabling configurable,
orderable metadata/artwork enrichment at parity with recognition provider orchestration.
