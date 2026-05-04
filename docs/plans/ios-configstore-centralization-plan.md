# iOS ConfigStore Centralization Plan

## Context and Problem Statement

The current iOS configuration architecture uses multiple per-view clients that independently load and save `fullConfig` snapshots through `GET/POST /api/config`.  
This creates real operational issues:

- Sequential save overwrite risk (race between independent snapshots).
- Repeated stale cache behavior when ETag invalidation is delayed after save.
- Duplicated defensive patterns (`isEmpty`/merge guards) spread across multiple clients.

At the same time, the domain split at the UI layer is valid and should be preserved.

## Active Hotfix Status (Must Verify Before Refactor)

An overlapping hotfix may already be in progress in the iOS repository for amplifier/config cache issues.  
Treat the items below as **potentially already fixed**, but **mandatory to review and validate** before Phase 1 migration work continues.

Potentially fixed (verification required):

- `AmplifierConfigClient.load()` includes conditional skip/rehydration guard for empty `fullConfig` + `304` paths.
- ETag/local cache metadata is invalidated after successful `POST /api/config`.
- `AmplifierConfigClient.save()` triggers cache invalidation so polling readers observe fresh state.

Required verification gates:

- Code review confirms the intended behavior exists (not partial).
- Automated tests cover `save -> immediate reload -> view recreate`.
- Polling clients (`AmplifierClient`, `AmplifierVisibilityClient`) observe updates without stale reads.
- No duplicated workaround logic reintroduced in additional clients.

Execution rule:

- If hotfix is complete and validated, mark related checklist items as done and continue with planned phases.
- If partial, finish only missing pieces first, then resume the phased `ConfigStore` migration.

## Goal

Introduce a single app-lifetime `ConfigStore` as the only owner of `/api/config` I/O, while keeping domain-oriented clients/view models intact via slices/adapters.

## Execution Checklist

Use this checklist during implementation and PR review.

### Phase 0: Guardrails and Baseline

- [ ] Add snapshot tests for config mapping in existing writer clients.
- [ ] Add integration test for sequential save (`Physical Media` then `Streaming`).
- [ ] Add immediate post-save refresh smoke test (no stale app-layer state).
- [ ] Document current baseline behavior/failure mode in test descriptions.
- [ ] Confirm all Phase 0 tests are stable in CI.

### Phase 1: Introduce `ConfigStore`

- [ ] Create app-lifetime `ConfigStore` module.
- [ ] Implement `refresh(force:)`.
- [ ] Implement observable/publisher state exposure.
- [ ] Implement serialized `save(patch:)` pipeline.
- [ ] Invalidate local ETag/cache metadata after successful POST.
- [ ] Add unit tests for merge, serialization ordering, and ETag invalidation.
- [ ] Add adapter interfaces for incremental client migration.
- [ ] Verify no runtime behavior switch yet outside controlled adapters.

### Phase 2: Migrate High-Risk Writers

- [ ] Migrate `PhysicalMediaConfigClient` to store-backed reads/writes.
- [ ] Migrate `StreamingConfigClient` to store-backed reads/writes.
- [ ] Remove direct HTTP config writes from both migrated clients.
- [ ] Re-run sequential save test through migrated path.
- [ ] Validate no mapping regressions in migrated domain fields.

### Phase 3: Migrate Remaining Writers

- [ ] Migrate `AmplifierConfigClient`.
- [ ] Migrate `AdvancedConfigClient`.
- [ ] Migrate `MicGainWizardClient`.
- [ ] Migrate `DisplayClient` with app-lifetime behavior validation.
- [ ] Confirm all writer clients now use `ConfigStore`.
- [ ] Confirm no direct `POST /api/config` remains outside store.

### Phase 4: Cleanup and Hardening

- [ ] Remove duplicated merge/`isEmpty` defensive code from legacy clients.
- [ ] Remove dead config HTTP code paths.
- [ ] Consolidate shared config mapping helpers.
- [ ] Add save pipeline observability (queue depth, latency, failure category).
- [ ] Add CI guard against direct config POST outside `ConfigStore`.
- [ ] Final repo search confirms single save/refresh path ownership.

### QA and Release Readiness

- [ ] Manual QA: sequential save across two domains persists after app restart.
- [ ] Manual QA: display/now-playing save does not reset unrelated sections.
- [ ] Manual QA: advanced settings remain visible in wizard flows.
- [ ] Regression suite passes for all config-related tests.
- [ ] Release notes include config-store migration and known limitations.

## Non-Goals

- Solving true concurrent edit conflicts across multiple simultaneously active screens.
- Changing backend config schema or endpoint shapes.
- Rewriting all views in one shot.

## Target Architecture

### Single Source of Truth

- `ConfigStore` holds in-memory `fullConfig`.
- Only `ConfigStore` performs `GET /api/config` and `POST /api/config`.
- Domain clients read/write through store APIs, never directly through HTTP.

### Domain Slices

- Keep domain boundaries (`physical media`, `streaming`, `amplifier`, `advanced`, `display`, `wizard`).
- Each domain reads a typed slice from store state and submits typed patch intents.

### Serialized Save Pipeline

- All save operations go through a single serialized queue/actor.
- Save flow:
  1. Read current store `fullConfig`.
  2. Apply domain patch merge.
  3. POST merged payload.
  4. Replace in-memory `fullConfig` with server-confirmed result.
  5. Invalidate local ETag/cache metadata.

### Cache/ETag Policy

- After any successful POST, mark ETag as stale locally.
- Next refresh must force revalidation.
- Prefer backend response contract that returns authoritative updated config and fresh ETag.

## Implementation Strategy (Phased)

## Phase 0: Guardrails and Baseline

- Add snapshot tests for config mapping in each existing client.
- Add integration tests for sequential save scenario:
  - Save A (physical media), then Save B (streaming), verify both changes persist.
- Add smoke test for immediate reload after save (no stale `304` behavior at app layer).

Exit criteria:

- Baseline tests reproduce current failure modes or assert current behavior clearly.

## Phase 1: Introduce `ConfigStore` (No Behavioral Switch Yet)

- Implement store with:
  - `refresh(force:)`
  - `state` publisher/observable
  - `save(patch:)` (serialized)
  - ETag invalidation after successful save
- Keep existing clients untouched for runtime behavior.
- Add adapter interfaces so existing clients can be moved incrementally.

Exit criteria:

- Store unit tests pass for refresh, merge, serialization, and cache invalidation logic.

## Phase 2: Migrate High-Risk Writers First

Migrate first:

1. `PhysicalMediaConfigClient`
2. `StreamingConfigClient`

Reason: these flows are most likely to be edited sequentially and currently produce overwrite risk.

Exit criteria:

- Sequential save integration test passes through migrated path.
- No regression in domain mapping fields for these two clients.

## Phase 3: Migrate Remaining Writers

Migrate:

- `AmplifierConfigClient`
- `AdvancedConfigClient`
- `MicGainWizardClient`
- `DisplayClient` (special care due to app-lifetime behavior)

Exit criteria:

- All write clients use store-backed save path.
- No direct `POST /api/config` outside `ConfigStore`.

## Phase 4: Cleanup and Hardening

- Remove duplicated merge/empty-snapshot defensive code from old clients.
- Remove dead HTTP config code paths.
- Consolidate config mapping helpers in a shared module.
- Add telemetry/logging for:
  - Save queue depth
  - Save latency
  - Save failure categories

Exit criteria:

- Single code path for save/refresh is enforced.
- Static search confirms no legacy config POST writers remain.

## Risk Register and Mitigations

## Risk: Mapping regressions in low-frequency fields

- Affected areas: `advanced`, `display`, `metadata_enrichment`.
- Mitigation: golden/snapshot tests + fixture-based end-to-end mapping tests.

## Risk: Behavior drift during mixed migration period

- Mitigation: adapter layer preserving old client interfaces while redirecting I/O to store.

## Risk: Perceived stale config after save

- Mitigation: explicit local ETag invalidation + forced refresh policy after save completion.

## Risk: Hidden writer remains outside store

- Mitigation: CI check/search rule failing if new direct config POST usage appears outside store module.

## Testing Plan

## Unit Tests

- `ConfigStore` merge correctness per domain patch.
- Save serialization ordering and no lost update in sequential requests.
- ETag invalidation behavior after successful POST.

## Integration Tests

- Sequential writes across domains preserve both updates.
- Refresh immediately after save returns latest config at app layer.
- Failure paths: network error on save keeps local consistency and surfaces actionable error.

## Manual QA Checklist

- Edit and save Physical Media, then Streaming quickly; verify both settings persist after app restart.
- Save Display/Now Playing options; verify no collateral reset in unrelated sections.
- Save Advanced values; verify wizard screens still read expected values.

## Delivery Plan (PR Slicing)

1. **PR 1**: Add `ConfigStore` + tests + adapters (no client migration).
2. **PR 2**: Migrate Physical Media + Streaming clients.
3. **PR 3**: Migrate Amplifier + Advanced + Mic Gain Wizard.
4. **PR 4**: Migrate Display client + cleanup/remove legacy code.
5. **PR 5** (optional): observability and CI guardrails for direct writer prevention.

## Acceptance Criteria

- Exactly one module owns `/api/config` HTTP reads/writes.
- Sequential save overwrite issue is eliminated for single-device app usage.
- Post-save immediate reload no longer serves stale app-layer state.
- Existing domain-level UI boundaries are preserved.

## Cross-Repo Coordination Notes

Because `oceano-player-ios` is a strict downstream consumer of backend config behavior:

- Keep backend `/api/config` response shape unchanged.
- If backend ETag semantics are adjusted, document contract expectations explicitly.
- Track any required backend follow-up in cross-repo sync documentation and release notes.
