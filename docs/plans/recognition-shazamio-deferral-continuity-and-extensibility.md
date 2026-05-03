# Recognition: Shazamio Deferral, Continuity Provider Choice, and Safe Extensibility

## Purpose

This note captures **product and engineering direction** agreed for **documentation only** (no implementation commitment in this document). It complements:

- `docs/plans/recognition-flexible-providers-and-secrets.md` — flexible provider list, roles, BYOK secrets
- `docs/plans/recognition-provider-chain-improvement.md` — chain behaviour and coordinator semantics
- `docs/cross-repo-sync.md` — checklist when HTTP/config contracts change (`oceano-player-ios`)

---

## Current stance (defer Shazamio; decide from data)

**Shazamio** (`install-shazam.sh`, Python `shazamio`, continuity monitor) remains **technically optional** today. For a **commercial or scaled** offering, treat it as **non-strategic**: it is not a first-party licensed Shazam API, and redistribution/TOS risk does not “scale” with end users.

**Near-term:** defer further **product** investment in Shazamio (marketing, default installs, companion-app prominence). Use **existing telemetry** to steer later work:

- `GET /api/recognition/stats` — aggregated counters, including `Trigger.boundary` vs `Trigger.fallback_timer` (see `recognition_coordinator.go`).
- `boundary_events` and related follow-up fields in the library SQLite — VU/boundary path quality, not timer-specific “saved track” counts.

**Interpretation guardrail:** high `fallback_timer` counts mean “many runs started from the periodic path,” not automatically “many wrong tracks fixed.” Use trends plus real listening scenarios (vinyl vs gapless CD) before removing continuity features.

**SD card / SQLite:** keep metrics in the **existing library DB** (WAL already enabled in `internal/library.Open`). Any future high-volume telemetry should follow a **write-light** policy (batching, retention, optional `PRAGMA synchronous` alignment across all openers) — see discussion in flexible-providers plan and engineering standards; avoid hot-path per-event fsync storms.

---

## Future direction 1: Continuity without Shazamio (user-selected provider, lower cadence)

**Goal:** After reviewing stats, optionally **remove dependence on Shazamio** for gapless / soft-boundary continuity and replace it with a **user-selected provider** from the **supported** set (e.g. **ACRCloud**) running on a **lower cadence** than today’s Shazamio-oriented continuity loop.

**UX / contract (conceptual):**

- User picks **which provider** may act as **continuity / gapless helper** (or “none”).
- User sees an explicit **cost / quota warning** (BYOK): e.g. ACRCloud calls on a timer **consume plan credits**; free tiers vary by vendor and may be sufficient for light personal use (operator anecdote: free-tier signup + key/secret only — **not** a guarantee for all users or regions).

**iOS (and any native client):** continuity and provider enablement should be **opt-in** and clearly labeled (network, third-party TOS, billing). Any new config keys or semantics require `docs/cross-repo-sync.md` updates.

**Engineering note:** today’s continuity path is **Shazamio-specific** in the state manager; generalising to “call provider X on interval Y with policy Z” belongs with the **explicit provider list** and role work in the flexible-provider plan, not as a one-off fork.

---

## Future direction 2: “Custom provider” not shipped by default (security)

**Goal:** Allow advanced users to extend recognition with a **provider not bundled** in Oceano releases, under **explicit “at your own risk”** terms.

**Non-goals (unsafe defaults):**

- Arbitrary **executable paths** or **shell/Python snippets** in `config.json` from the network UI — high risk of **command injection**, supply-chain takeover, or accidental `curl | sh` patterns.
- Loading **unsigned shared objects** / plugins from user-writable paths without a **trust and signing** model.

**Safer alternatives (ordered from simpler to heavier):**

1. **Fork + compile:** Document that adding a new recognizer is a **Go code change** behind `internal/recognition` (or equivalent) and a **signed release** from the maintainer — safest default story.
2. **Fixed-contract HTTP recognizer:** Config supplies only **HTTPS URL**, **auth header** (or mTLS), and a **versioned JSON schema** for request/response. The Pi sends the same WAV bytes or a hash+upload flow the contract defines. **No arbitrary code** on the Pi beyond the generic HTTP client; risk shifts to **user-operated** endpoint (user’s server, their malware surface).
3. **Sidecar on LAN:** User runs a **separate container or binary** they installed; Oceano calls `http://127.0.0.1:…` with the same fixed contract. Isolation boundary is OS/network policy.
4. **Signed extension bundles (long-term):** e.g. manifest + pinned container image digest or **cosign**-verified artifact; Oceano only runs extensions that pass verification. High implementation and ops cost — only if there is clear demand.

Any “custom provider” feature that touches **`POST /api/config`** or systemd must preserve **atomic writes**, **no privilege escalation**, and **clear iOS contract** versioning per `docs/cross-repo-sync.md`.

---

## Summary

| Topic | Now | Later |
|-------|-----|--------|
| Shazamio | Defer product reliance; optional install only | Decide remove/replace using stats + legal posture |
| Continuity | Shazamio monitor when Shazam is an enabled primary and installed | User-chosen supported provider + slower cadence + quota UX |
| Custom providers | Do not ship arbitrary code injection | Prefer HTTP contract or fork; signed bundles only if justified |
