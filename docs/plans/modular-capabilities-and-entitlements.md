# Modular capabilities and entitlements foundation

## Goal

Define a robust, additive foundation to enable/disable backend modules (starting with Discogs enrichment) and support future commercial plans (tiers, billing, trials, BYOK) without breaking existing iOS and local deployments.

## Why now

Current recognition architecture already supports provider ordering and per-provider enable/disable. This is a strong technical base, but it is not yet a full product-layer entitlement model. We still couple:

- technical runtime configuration (what can run),
- credentials availability (what is configured),
- and future commercial access policy (what is allowed by plan).

Separating these concerns early reduces migration risk when introducing paid plans.

## Current strengths in Oceano

1. `recognition.providers[]` is explicit and additive.
2. Providers can be enabled/disabled per config.
3. Runtime degrades safely when providers are missing/unavailable.
4. BYOK posture already exists for external providers.
5. State and API evolution generally follow additive JSON rules.

These are exactly the right ingredients for a module system.

## Current gaps for a commercial-grade module platform

1. No first-class **capability registry** (single source of truth for module ids, states, and dependencies).
2. No explicit **entitlement layer** (plan allows X; runtime config enables Y; effective result is intersection).
3. No clear **module health contract** shared across state + HTTP + iOS.
4. No lifecycle states for commercial UX (`allowed`, `configured`, `active`, `degraded`, `blocked_by_plan`).
5. No versioned contract for plan migration (important when introducing billing later).

## Design principles

1. **Additive only** for API/state payloads.
2. **Policy over hardcoding**: capabilities declared in config/contract, not scattered conditional logic.
3. **Fail-open for core playback**: disabled/blocked modules must not break Physical/AirPlay/Bluetooth baseline.
4. **Explainable state**: every disabled behavior should have a machine-readable reason.
5. **BYOK-compatible by default** with optional future brokered credentials.

## Proposed model (minimal and robust)

### 1) Capability registry (backend)

Introduce canonical module ids and metadata in one place:

- `recognition.core` (baseline)
- `recognition.discogs_enrichment`
- `recognition.provider.acrcloud`
- `recognition.provider.shazam`
- `recognition.provider.audd`

Each capability declares:

- `id`
- `kind` (`core`, `provider`, `enrichment`, `ui_support`)
- `depends_on[]` (e.g. Discogs enrichment depends on recognition.core)
- `requires_credentials` (bool)
- `default_enabled` (bool)

### 2) Entitlement contract (plan/billing ready)

Add an optional entitlement source (local now, server-synced later):

```json
{
  "entitlements": {
    "version": 1,
    "source": "local",
    "capabilities": {
      "recognition.discogs_enrichment": true,
      "recognition.provider.shazam": false
    },
    "valid_until": "2026-12-31T23:59:59Z"
  }
}
```

`effective_enabled(module)` should be:

`plan_allows && user_enabled && dependencies_ready && credentials_valid`

### 3) Unified module status shape (state + API)

Expose a normalized shape for iOS and web:

```json
{
  "id": "recognition.discogs_enrichment",
  "allowed_by_plan": true,
  "enabled_by_user": true,
  "configured": true,
  "active": true,
  "degraded": false,
  "reason": null,
  "updated_at": "2026-05-04T15:30:00Z"
}
```

Reason examples:

- `blocked_by_plan`
- `missing_credentials`
- `dependency_not_ready`
- `rate_limited`
- `provider_unavailable`

### 4) Discogs as first module

Discogs is ideal as module #1 because it is:

- optional (non-core),
- post-recognition (non-blocking),
- externally constrained (rate limits, legal/commercial posture),
- user-visible in value and health.

This lets us prove the capability model with low risk.

## Rollout phases

### Phase A — Foundation (no billing dependency)

1. Add backend capability ids/constants.
2. Add effective module-state evaluator.
3. Expose module status in an additive endpoint:
   - `GET /api/modules/status`
4. Add optional entitlement block in config (local-only source).
5. Keep behavior unchanged when entitlements block is absent (backward compatibility).

### Phase B — Discogs integration on top

1. Gate Discogs enrichment through module evaluator.
2. Expose Discogs-specific health/reason fields using generic module status.
3. Surface in iOS:
   - Home warning card (dynamic when degraded/rate-limited/blocked)
   - Physical Media providers/config section

### Phase C — Commercial plans/billing

1. Add signed/server-provided entitlements source.
2. Add trial/expiration semantics (`valid_until`, grace).
3. Add explicit iOS messaging for plan-blocked capabilities.
4. Preserve offline behavior with cached entitlements and clear stale-state rules.

## iOS contract implications

To keep downstream safe:

1. Keep all additions optional and additive.
2. Reuse existing recognition fields for immediate UX.
3. Introduce module status endpoint as a separate surface (do not overload existing status unexpectedly).
4. Update cross-repo checklist in `docs/cross-repo-sync.md` whenever payload keys are added.

## Risks and mitigations

1. **Risk:** Entitlements logic scattered across handlers.
   **Mitigation:** Single evaluator package/function used by state + web handlers.
2. **Risk:** Confusion between configured vs allowed.
   **Mitigation:** Always expose both flags and an explicit reason.
3. **Risk:** Plan migration breaks offline devices.
   **Mitigation:** cached entitlements + expiry + deterministic fallback policy.
4. **Risk:** Third-party module outage degrades UX.
   **Mitigation:** non-blocking architecture, reason-coded degraded states, retries/backoff.

## Decision recommendation

Yes, there is strong potential and enough existing architecture to start now.

Recommended next step:

1. Implement **Phase A** first (capability registry + module status endpoint, additive only).
2. Use Discogs as first non-core module behind this layer.
3. Only then connect commercial plan/billing sources.

This preserves current stability while creating a clean path to paid tiers.
