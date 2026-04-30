# Findings and proposed improvements (critical review)

This document consolidates **findings** from recent product and code reviews (AirPlay output reliability, amplifier **cycle** input mode, USB DAC **identification** semantics, companion iOS behaviour) and a **second-pass** critique: assumptions, failure modes, severity, and concrete improvement directions.

**Scope:** Primarily `oceano-player` (Go backend / `oceano-web`) and cross-cutting behaviour of `oceano-player-ios` where referenced. iOS-only backlog items are listed under a dedicated section.

**Audience:** Maintainers and future agents implementing changes.

---

## 1. AirPlay output, DAC presence, and silent fallback (Option B)

### Findings

1. **Resolved behaviour (implemented):** When `audio_output.device_match` is set and the matched USB DAC is absent from `/proc/asound/cards`, `oceano-web` reconciles `shairport-sync` to ALSA **`null`** (silent sink) instead of an ambiguous `default` device. When the DAC reappears, output returns to `plughw:N,0` and the service is restarted. A periodic guardian in `oceano-web` drives this reconciliation.
2. **Operational implication:** AirPlay can remain **connectable** with **no audible output** while the DAC is missing — this is intentional for stability, not a regression.
3. **Documentation:** `README.md` and `CLAUDE.md` should stay aligned with this contract whenever behaviour or troubleshooting steps change.

### Second-pass critique

| Risk | Mitigation / note |
|------|---------------------|
| **Restart churn** | Guardian should only rewrite/restart when the **resolved device string** changes (avoid log noise and unnecessary session drops). Verify in code reviews that no path compares unstable strings. |
| **Explicit `audio_output.device`** | If the user pins an explicit `plughw:N,0` and unplugs the card, `N` may shift on replug; `device_match` is more robust for hotplug. Document that trade-off. |
| **Shairport failure modes** | Silent fallback does not fix mDNS, wrong `shairport-sync` unit, or Avahi issues — keep troubleshooting separate. |

### Proposed improvements (optional)

- Expose a **read-only status** field (e.g. on `/api/status` or amplifier-adjacent diagnostics) for “AirPlay output target: `dac` \| `null` \| `unknown`” so UIs can show *why* there is no sound without SSH.
- Add a **single integration test** or scripted smoke check that stubs ALSA card lists (where feasible) to prevent regression of `null` vs `default` resolution.

---

## 2. Amplifier input mode `cycle` — behaviour and intermittent failures

### Findings

1. **`POST /api/amplifier/select-input`** (used by topology picker / “go to input”) calls `selectInputForward`. In **`cycle`** mode it implements a **two-phase** behaviour:
   - If no input navigation occurred within **1200 ms** (`selectionActiveWindow`), it sends an extra **`NextInput`** pulse to **arm / open** the selector, waits `firstStepSettle` (from `amplifier.usb_reset.first_step_settle_ms`, with fallback), then sends the remaining pulses for the computed `steps`, with `stepAdvanceWait` between consecutive pulses (except after the last).
   - If navigation **is** considered active inside that window, it **skips** the arming pulse and only sends `steps` pulses.
2. **`POST /api/amplifier/next-input` and `prev-input`** send **exactly one** IR command each and update the same “last nav” timestamp. They **do not** invoke `selectInputForward` — so they **do not** apply cycle arming or inter-pulse delays.
3. **Consequence:** User experience depends on **which control path** was used (picker vs single-step buttons). That can look **random** when users mix flows.
4. **`last_known_input_id`** in `config.json` drives the **assumed** current input for clients that compute `steps` as forward distance in the **ordered** `amplifier.inputs` list. Any **drift** (physical remote, another client, failed IR) makes `steps` wrong even if arming logic is correct.
5. **Ordering assumption:** Forward `steps` math assumes `amplifier.inputs` order matches the **physical cyclic order** of the amplifier’s INPUT control. If topology order diverges from hardware order, navigation will be systematically wrong (may appear “intermittent” when combined with drift).

### Second-pass critique

| Issue | Severity | Notes |
|-------|----------|--------|
| **Dual code paths** (`select-input` vs `next`/`prev`) | High (UX) | Single-tap “next” from cold state may only arm UI on some amps; picker path may work. |
| **1.2 s active window** | Medium | Rapid mixed actions from multiple UIs can skip arming when the amp already left selection mode. |
| **IR reliability** | Medium–High | Broadlink + environment: lost pulses desynchronise without feedback from the amp. |
| **No closed-loop confirmation** | High | System does not read back actual input from hardware; all logic is open-loop over IR + stored state. |

### Proposed improvements

1. [x] **Unify semantics (backend):** `next-input` / `prev-input` now route through the same cycle state machine as `select-input` in `cycle` mode (`prev` uses mirrored arming + settle + pacing).
2. [ ] **Telemetry (follow-up PR):** Log structured events (`input_nav`, `mode`, `steps`, `armed`, `settle_ms`) at **info** level behind a flag or rate limit to support field diagnosis.
3. **Client contract:** After failed `select-input` (non-2xx), clients must **not** advance assumed index or persist `last_known_input_id` (see iOS §4.1).
4. **Topology guardrails:** Wizard or docs should state explicitly: **input list order must match hardware cycle order** for `cycle` mode.

---

## 3. USB DAC “identification” — utility split by use case

### Findings

1. **Audio routing (AirPlay / Bluetooth default sink):** Resolving **which ALSA card** is the intended DAC (via `device_match` or explicit `plughw`) remains **operationally necessary** on systems with **multiple** USB audio devices (e.g. capture dongle + amplifier DAC). Wrong card → wrong or silent output.
2. **Silent AirPlay fallback:** DAC detection via card scan supports **deterministic** fallback to `null` and **return** to `plughw` when the device reappears — this is distinct from “is the amplifier philosophically on?”.
3. **Power / inference use cases:** Using “DAC appears in `aplay -l` / card list” as a proxy for **amplifier power** is **fragile**: USB suspend, deep standby, hub power management, and boot order can make the DAC **disappear temporarily** while the user would still say the amp is “on” or “warm”.
4. **`ProbeWithInputCycling`:** Cycling IR until the DAC **reappears** is valuable mainly for **bootstrap** and **recovery** after mis-input; for stable installs it adds **complexity** and can feel like magic if USB enumeration is flaky.

### Second-pass critique

- Treat **routing identity** (which card is the DAC) and **power / UX inference** (is the amp usable right now) as **separate concerns** in documentation and UI copy. Conflating them increases perceived flakiness.
- Any feature that says “we detected your DAC” should clarify **detected for output routing**, not “your amplifier is fully ready”.

### Proposed improvements

1. **Product copy / setup:** De-emphasise DAC discovery in **post-setup** checklists; keep it prominent only where **routing** is configured (Audio Output, BT sink helpers).
2. **Optional degradation:** Allow advanced users to **disable** DAC-based power heuristics while keeping **explicit** output device strings (trade-off: more manual maintenance).
3. **Long-term:** If the amp exposes **reliable** state another way (future IP control, different hardware), prefer that for **power**; keep ALSA card match for **audio path** only.

---

## 4. Companion iOS app (`oceano-player-ios`)

### 4.1 Amplifier client — optimistic state without success signal

**Finding:** `AmplifierClient.post(_:body:)` catches errors and sets `lastError` but **does not** propagate failure to callers. Therefore `selectInput(fullIndex:)`, `nextInput()`, and `prevInput()` **always** update `currentInputIdx` and call `persistKnownInput` **even when the HTTP request failed**.

**Severity:** High (state drift vs physical amp).

**Proposal:** Make `post` return `Bool` or `throws`, and only update local index + persist when the server returns success.

### 4.2 Recognized track notifications (known backlog)

**Finding:** Deduplication for “already confirmed” tracks is not persisted across app relaunch; notifications can **re-fire** after cold start.

**Proposal:** Persist deduplication keys (e.g. fingerprint + confirmed flag) in `UserDefaults` or app group storage with bounded LRU eviction.

### 4.3 Bluetooth transport endpoint

**Context:** A backend `/api/bluetooth/transport` path was explored then rolled back per product decision; iOS may still reference endpoints depending on branch.

**Proposal:** Keep backend and iOS branches aligned; if transport is postponed, ensure **no** dangling client calls reference removed routes (build + contract tests).

---

## 5. Backend items already addressed (reference)

The following were implemented or fixed in recent work; listed here so this document does not re-open them as unknowns:

- **Built-in amplifier profile IR merge** on activate (defaults not wiped by empty learned map).
- **`oceano-state-manager` systemd `ExecStart` reconciliation** on `POST /api/config` so recognition provider flags do not drift from `config.json`.
- **AirPlay output resolution** with `device_match` and **`null`** fallback when DAC absent (`oceano-web` guardian).

---

## 6. Suggested prioritisation

| Priority | Item | Rationale |
|----------|------|-----------|
| P0 | iOS: only update `currentInputIdx` / persist after successful amplifier POST | Stops silent desync — root cause of many “random” navigation reports. |
| P1 | ~~Unify `next`/`prev` with `select-input` cycle machine **or** document + UI-disable~~ **Completed (backend unified)** | Removed dual-path ambiguity in backend cycle mode. |
| P1 | Persist notification deduplication | User trust / notification fatigue. |
| P2 | Product/docs: split “DAC for routing” vs “DAC for power inference” | Reduces perceived uselessness of DAC detection. |
| P2 | Optional diagnostics endpoint for AirPlay sink target | Faster support without SSH. |
| P3 | Structured logging for input navigation | Field debugging only; avoid log spam. **Pending follow-up PR**. |

---

## 7. iOS cross-repo checklist

When changing any of the following in **this** repository, follow `docs/ai-cross-repo-sync.md` and review **`oceano-player-ios`**:

- `/api/config` shape, `amplifier_runtime`, `last_known_input_id`
- Amplifier endpoints: `select-input`, `next-input`, `prev-input`, `last-known-input`
- AirPlay-related config keys or semantics surfaced to mobile

---

## Document history

- **2026-04-29:** Initial consolidation + second-pass technical critique (internal engineering doc; English per repository convention).
