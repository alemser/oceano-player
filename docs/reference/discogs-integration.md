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
5. persist selected Discogs fields into library.

Important: if Discogs fails, playback state still updates normally.

## Stage B (optional): re-enrichment for existing library

Background job for rows missing `label/catno/side-position`.

- bounded worker pool
- resumable cursor
- throttled requests

## Stage C (optional): user-assisted correction

Expose top Discogs candidates in admin/iOS workflows when confidence is low.

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
