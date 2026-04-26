# Configuration & Onboarding — Improvement Plan

This document describes how first-time configuration feels today, why it is hard, and a phased plan to make setup **guided, minimal by default, and opt-in** for advanced features (amplifier IR, per-input calibration, weather, etc.).

## Goals

- A new user should know **what to do first**, **what is optional**, and **what order** makes sense without reading the whole README.
- **Defaults should not imply a specific amplifier** (e.g. Magnat MR 780) until the user explicitly chooses hardware that matches a built-in profile.
- Surface **contextual hints** (“you have not configured X”) in the main UI and config drawer, not only deep in sub-pages.
- Complement existing **`oceano-setup`** (CLI on the Pi) with a **web-first welcome path** for credentials, devices, and “nice to have” options.

---

## First-user journey today (pain points)

### Split across many surfaces

| Surface | Role |
|--------|------|
| `sudo oceano-setup` | AirPlay name, Bluetooth, ALSA devices, PipeWire resilience, optional display |
| Main config drawer (`index.html`) | Streaming, capture, amplifier toggle + profile link, advanced link |
| `recognition.html` | ACRCloud / chain / calibration wizards / mic gain |
| `amplifier.html` | Broadlink host, pairing, IR learning, inputs, USB reset, connected devices |
| `pair.html` | Broadlink pairing wizard |
| `advanced.html` | Sockets, paths, library DB |

A first user opening **Configuration** sees many sections and external links (“Configure Recognition ↗”, “Configure Amplifier ↗”) without a **single ordered checklist** or “you are 2/5 steps done” model.

### Amplifier defaults feel “already decided”

Fresh `defaultConfig()` in `cmd/oceano-web/config.go` sets:

- `amplifier.profile_id`: `magnat_mr780`
- `maker` / `model`: Magnat MR 780
- Full input list and timing aligned with that product

`amplifier.enabled` is `false` (zero value), so IR control is off — but the **UI still presents an active Magnat-oriented profile** in “Active Profile”. That reads as “this system is for a Magnat MR 780” even when the user has different hardware or does not want IR at all.

**Desired behaviour:** built-in Magnat remains **available in the profile picker**, but **no hardware-specific profile is pre-selected** for a brand-new install. The user explicitly picks “I use Magnat MR 780 (built-in)” or “Generic / other”.

### Broadlink is a separate mental step

Copying is roughly: enable amplifier → choose profile → open amplifier page → enter RM IP → open pairing wizard in another tab → learn IR commands → save → restart services.

There is good copy on `amplifier.html` and `pair.html`, but nothing in the **main header or status area** that says “Broadlink not paired” vs “IR ready”.

### Calibration and recognition are advanced but look mandatory

Calibration wizards (`recognition.html` + `calibration-wizard.js`) are powerful but buried behind “Physical Sources → Configure Recognition”. A new user may not understand **when** calibration is needed vs **optional** presets (“Standard — no calibration needed”).

### Other defaults that look “wrong” for many users

Examples worth revisiting in the same simplification pass:

- **Weather**: defaults to Dublin with weather **enabled** in `defaultConfig()` — surprising on a fresh Pi in another country.
- **Advanced calibration profiles** in `advanced.calibration_profiles`: ship with sample numeric data keyed by input IDs; useful for regression/dev but **opaque** to a first user (“where did these numbers come from?”).

---

## Design principles

1. **Progressive disclosure** — core path: devices + recognition credentials + save/restart. Amplifier IR, calibration, weather, SPI: clearly optional.
2. **Explicit opt-in for hardware identity** — no implied Magnat (or any) model until chosen.
3. **One checklist, many deep links** — welcome wizard or dashboard row: each item deep-links to the right page with `?from=onboarding` if needed.
4. **Server-side “completeness”** — `GET /api/setup-status` (or extend `/api/status`) returning booleans: `has_capture_device`, `has_acrcloud`, `amplifier_ir_ready`, `calibration_any`, etc., so the UI does not duplicate logic.
5. **Preserve power user flows** — do not remove `amplifier.html` / `recognition.html`; add a thin **orchestration layer** on top.

---

## Proposed work (phased)

### Phase 1 — Neutral defaults & honest empty state (low risk)

- **Fresh config template**
  - Set `amplifier.profile_id` to empty (or a literal sentinel like `none` if JSON empty is ambiguous in the UI).
  - Clear `maker` / `model` or set to generic placeholders only shown when no profile is selected.
  - Keep built-in `magnat_mr780` **only** in the profile registry / “activate profile” API, not as the implicit default in `defaultConfig()`.
- **Migration**
  - Existing installs: **no change** on load (already have `config.json`). Optional one-time migration only if you introduce a new explicit `profile_id` value and need to map old files.
- **UI**
  - Profile `<select>` default option: “Choose amplifier profile…” with built-ins listed below.
  - Short hint: “Built-in profiles supply input maps and timings; IR codes still require learning on your amp.”

### Phase 2 — Web welcome checklist (“first 10 minutes”)

- After first load (or cookie / `localStorage` flag `onboarding_dismissed`), show a **compact checklist** (modal or top banner):
  1. System audio (link: note “already covered by `oceano-setup`” + button “I ran setup”).
  2. Capture device (device picker + link to Physical Sources).
  3. Track recognition credentials (deep link to recognition / ACRCloud section).
  4. *(Optional)* Enable amplifier control → Broadlink pairing → IR learn (ordered sub-steps).
  5. *(Optional)* Run calibration wizard when using vinyl / strict boundaries.
- **Dismiss** “Don’t show again” + restore entry under header (“Setup checklist”).

### Phase 3 — Contextual hints everywhere

- **Status bar / header**: chip or icon when `setup-status` reports missing ACRCloud or capture ambiguity (e.g. multiple USB cards).
- **Config drawer**: reorder sections so **incomplete** items float to the top when `setup-status` says so.
- **Amplifier widget**: only show when `amplifier.enabled`; when enabled but not paired, show “Pair Broadlink” instead of silent failures.

### Phase 4 — Bridge CLI setup and web setup

- **`oceano-setup`**: optional final screen “Open `http://<pi>:8080` to finish: ACRCloud, optional amplifier IR.”
- **Web**: link back to README section for resilience / Bluetooth only when relevant (already partially in header hint).

### Phase 5 — Weather & calibration template hygiene

- **Weather**: default `enabled: false` and neutral lat/lon or empty until user enables (avoids “why Dublin?”).
- **Calibration**: default `calibration_profiles` empty on fresh template; keep dev fixtures in test data or a `docs/examples/` JSON snippet rather than embedded defaults in production `defaultConfig()` (if that does not break tests — may require splitting “dev default” vs “shipping default”).

---

## Success metrics (qualitative)

- A new user can answer: “What is the **minimum** I must configure to get AirPlay + physical recognition?” without opening four pages.
- No UI copy implies ownership of a **specific commercial amplifier** until the user selects that profile.
- “Optional” features (IR, calibration, weather) are labeled consistently and discoverable from one checklist.

---

## Out of scope (for this plan)

- Replacing Broadlink with another IR stack.
- Merging all HTML into a single SPA (not required for onboarding; checklist + status API is enough).

---

## Related code / docs

- Default config: `cmd/oceano-web/config.go` (`defaultConfig`)
- Built-in profile resolution: `cmd/oceano-web/amplifier_profiles.go` (`builtInAmplifierProfile`, `resolveAmplifierConfig`)
- Pairing UI: `cmd/oceano-web/static/pair.html`
- Amplifier UI: `cmd/oceano-web/static/amplifier.html`
- Calibration: `cmd/oceano-web/static/recognition.html`, `static/recognition/calibration-wizard.js`
- CLI wizard: `cmd/oceano-setup/`
- Architecture reference: `docs/amplifier-device-architecture.md`, `docs/distribution-and-setup-improvements-plan.md`
