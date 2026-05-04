# Metadata Enrichment Chain Plan

This document translates the Discogs addendum into an implementation-oriented plan
with file-by-file scope, sequencing, and verification gates.

## Scope

Build a decoupled, configurable metadata/artwork enrichment chain that runs
after recognition success, with provider ordering, fallback, and merge policies.

Out of scope for this phase:

- replacing recognition providers
- changing existing recognition provider contracts
- introducing breaking API field renames/removals

## Design constraints

1. Keep backward compatibility for existing clients (`oceano-player-ios`, nowplaying).
2. Keep runtime behavior safe on Raspberry Pi (bounded retries, no hot-loop API spam).
3. Keep migrations additive and restore-safe.
4. Keep user-edited metadata precedence over auto-enrichment.

## Target config (new section)

Add a new section in config JSON:

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

## Proposed package/file layout

### 1) New internal metadata package

- `internal/metadata/types.go`
  - `EnrichmentRequest`
  - `EnrichmentPatch`
  - `Provider` interface
  - merge policy enum/constants

- `internal/metadata/chain.go`
  - coordinator executing ordered providers
  - policy handling (`first_success`, `fill_missing_then_stop`, `collect_all_best_effort`)
  - telemetry hooks (attempt/success/no_match/error/rate_limit)

- `internal/metadata/merge.go`
  - deterministic patch merge rules
  - "fill only missing" helper
  - optional "prefer higher confidence" helpers

- `internal/metadata/errors.go`
  - shared error classes (`ErrNoMatch`, `ErrRateLimit`, etc.)

### 2) Provider adapters

- `internal/metadata/provider_payload.go`
  - turns current recognition result into baseline patch

- `internal/metadata/provider_itunes.go`
  - wraps existing iTunes artwork lookup functions
  - returns artwork patch only

- `internal/metadata/provider_discogs.go`
  - wraps Discogs metadata enrichment
  - optional artwork from Discogs image fields when present

### 3) State-manager wiring

- `cmd/oceano-state-manager/config_types.go`
  - add typed config structures for `metadata_enrichment`

- `cmd/oceano-state-manager/recognition_config_load.go`
  - parse + normalize metadata enrichment config
  - defaults and validation

- `cmd/oceano-state-manager/main.go`
  - construct metadata providers from config
  - pass chain coordinator to recognition pipeline

- `cmd/oceano-state-manager/recognition_coordinator.go`
  - replace direct hardcoded enrichment/artwork fallbacks with chain invocation
  - keep existing behavior flags until rollout is complete

### 4) Persistence/provenance

- `internal/library/library.go`
  - additive fields (later phase):
    - `metadata_provider`, `metadata_updated_at`
    - `artwork_provider`, `artwork_updated_at`
  - keep current additive Discogs fields intact

- `cmd/oceano-web/library.go`
  - read/expose new additive fields safely
  - preserve old fields and response shapes

## Runtime behavior contract

1. Recognition succeeds.
2. Coordinator builds request from recognized result + known IDs.
3. Providers run in configured order.
4. Patches merge deterministically via configured policy.
5. Final patch is applied to in-memory state and persisted once.
6. User-curated fields are never overwritten by weaker auto enrichment.

## Merge policy rules (minimum)

### `first_success`

- first provider returning non-empty patch ends chain

### `fill_missing_then_stop` (recommended default)

- keep running while mandatory target fields remain missing
- stop when all target fields are filled

### `collect_all_best_effort`

- execute all enabled providers
- merge with deterministic precedence (order + confidence tie-break)

## Data model additions (phase 2+)

Additive migration candidates:

- `collection.metadata_provider TEXT`
- `collection.metadata_updated_at TEXT`
- `collection.artwork_provider TEXT`
- `collection.artwork_updated_at TEXT`
- optional Discogs hardening:
  - `discogs_release_id TEXT`
  - `discogs_master_id TEXT`
  - `discogs_match_score INTEGER`
  - `discogs_enriched_at TEXT`

## Backup/restore safety requirements

Any phase touching schema/bootstrap/restore must satisfy:

1. No schema drift between actual columns and `schema_migrations`.
2. Library restore always ensures state-manager reopens DB handle.
3. Preflight check reports compatibility before destructive restore.
4. Restore of older backups reaches a stable startup without duplicate-column failures.

## Rollout plan (PR-by-PR)

### PR 1: Foundations

- new `internal/metadata` contracts and chain skeleton
- config structs + loader defaults (disabled by default)
- no behavioral change when disabled

### PR 2: iTunes + payload providers

- implement `provider_payload` + `provider_itunes`
- wire chain in coordinator behind config flag
- parity behavior with current artwork fallback

### PR 3: Discogs provider migration

- move existing Discogs enrichment into provider adapter
- remove direct coordinator coupling
- add provider telemetry counters

### PR 4: provenance fields + API exposure

- additive migration + persistence
- expose new fields in web API additively
- cross-repo sync docs update

## Test plan (mandatory)

### Unit tests

- provider ordering
- merge policies and stop conditions
- no-match/error/rate-limit fallback
- "do not overwrite user metadata"

### Integration tests

- recognition -> chain -> persistence end-to-end
- restore old backup -> startup -> no schema mismatch
- state output remains valid for nowplaying and iOS consumption

### Required commands

- `go test ./cmd/oceano-state-manager/... -short`
- `go test ./cmd/oceano-web/...`

When provider wiring changes touch recognition provider orchestration, also run:

- chain matrix tests in state-manager
- Pi smoke script per repository standards when available

## Documentation update checklist

In the same change set for each rollout step:

1. `README.md` (config reference + behavior notes)
2. `docs/reference/discogs-integration.md` (status and chain behavior)
3. `docs/cross-repo-sync.md` (iOS contract impact)
4. `AGENTS.md` / `CLAUDE.md` if operational guardrails change

## Acceptance criteria

1. Metadata/artwork provider chain is fully config-driven and orderable.
2. At least two providers can be enabled in fallback sequence without code edits.
3. Restore/preflight path remains stable with no schema/migration drift errors.
4. Existing client payloads remain backward-compatible.
