# Recognition Provider Chain Improvement Plan

## Overview
This document outlines ideas to improve the recognition provider chain in `oceano-state-manager`, making it more flexible, resilient, and user-configurable. The goal is to support multiple well-known providers out of the box and let the user select, order, and manage them through the web UI.

---

## 1. New Providers to Add (Pre-configurable)

The following providers should be added to the existing ACRCloud and Shazam integration, with pre-configured endpoints and credential templates so the user only needs to supply API keys:

| Provider | Type | Notes |
|----------|------|-------|
| **AudD.io** | Commercial | Simple API, Spotify metadata integration, generous free tier |
| **AcoustID + Chromaprint** (MusicBrainz) | Open-source / Free | No cost, no artwork natively (requires separate Cover Art Archive lookup) |
| **Gracenote** (Nielsen) | Commercial | Used by many commercial players, high accuracy |
| **SoundHound** | Commercial | Available API, alternative to Shazam for confirmation |

Each provider should implement the same `internal/recognition.Provider` interface so the chain can invoke them uniformly.

---

## 2. Improvements to the Current Chain (Without Changing Providers)

### 2.1 Role-based Selection
Allow marking each provider with a role in the web UI:
- `primary` — first attempted for identification
- `confirmer` — validates the result from `primary` before accepting
- `fallback` — tried only when `primary` fails or rate-limits

### 2.2 Configurable Order
Let the user drag-and-drop providers in the web UI to define the attempt order. Persisted in `config.json` as an ordered list.

### 2.3 Per-Provider Timeout
Each provider should have its own timeout setting (e.g., ACRCloud: 5s, AudD: 8s) instead of a global chain timeout. The chain moves to the next provider when the timeout is reached.

### 2.4 Intelligent Backoff
Providers that return rate-limit errors or temporary failures enter an automatic cooldown period (e.g., 5 minutes) and are skipped in subsequent chains until the cooldown expires.

### 2.5 Cross-Confirmation
When `primary` identifies a track, `confirmer` runs a second recognition on the same capture. If both return the same track (by matching artist + title, or by provider IDs), the result is accepted with higher confidence. This reduces false positives.

### 2.6 Local Cache Reuse
If multiple providers return the same fingerprint or provider-specific ID (e.g., ACRCloud ACRID, Shazam track ID), the result is accepted immediately without a new audio capture, reducing API calls and latency.

---

## 3. Cost and Quota Management

### 3.1 Usage Dashboard
Add a section in the web UI showing:
- Monthly request count per provider
- Plan limit (user-configured)
- Remaining quota bar

### 3.2 Automatic Quota Fallback
When a paid provider reaches its monthly quota, the chain automatically switches to available free providers until the next billing cycle.

### 3.3 Connection Test
Add a "Test Connection" button for each provider in the config page. On click, the backend makes a lightweight API call (or sends a sample fingerprint) to verify credentials and network reachability, showing a "Verified ✓" badge on success.

---

## 4. Web UI Configuration UX

### 4.1 Provider Toggles
- Each provider has an on/off toggle
- Credential fields appear only when the provider is enabled
- Disabled providers are skipped entirely in the chain

### 4.2 Presets
Offer preset configurations for quick setup:
- **Low Cost** — only free providers (AcoustID, AudD free tier)
- **High Accuracy** — all providers active, cross-confirmation enabled
- **Balanced** — one paid + two free providers

### 4.3 Status Indicators
- "Verified ✓" badge after successful connection test
- "Rate limited" warning with cooldown timer
- "Quota exhausted" alert with upgrade hint

---

## 5. Ambitious Idea: Parallel Recognition

Instead of invoking providers sequentially, dispatch 2–3 providers **in parallel** for the same audio capture. Accept the first result that returns with confidence above a threshold (configurable per provider). This significantly reduces identification latency on the NowPlaying display.

**Considerations:**
- Higher API cost (multiple calls per capture)
- Needs a way to cancel slower providers once a confident result is accepted
- Could be gated behind a "Fast mode" toggle in the web UI

---

## 6. Implementation Notes (for future planning)

- All provider changes must be reflected in `docs/cross-repo-sync.md` since response shapes may affect `oceano-player-ios`.
- New provider fields in `config.json` must have backward-compatible defaults so existing installs are not broken.
- The recognition coordinator in `internal/recognition/` should be extended, not rewritten, to follow the "behavior-preserving refactors first" principle in `AGENTS.md`.
