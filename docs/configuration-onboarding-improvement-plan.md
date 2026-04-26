# Configuration & Onboarding — Improvement Plan

This document describes how first-time configuration feels today, why it is hard, and a phased plan to make setup **guided, physical-media-first, minimal by default, and opt-in** for advanced features.

---

## Product positioning & primary audience

**Primary audience:** People who care about **physical media** — vinyl, CD, and other line-level sources routed through a real amplifier. They want reliable **now playing** identification, sensible **track boundaries**, and a clear picture of **what is wired where**, without becoming AV installers.

**Hero experience (invest here):**

- **Track recognition** — credentials, capture level, chain choice, and understandable retry/backoff behaviour.
- **Per-setup calibration** — noise floor and (where relevant) vinyl gap detection, scoped to the inputs that actually carry physical sources.
- **Device topology** — “this box is my turntable on Phono”, “this is the CD deck on CD”, with optional IR where it helps.

**Streaming (AirPlay / Bluetooth):** Important for day-to-day use, but **many products already excel** at discovery, pairing, and multi-room streaming. Oceano should ship **only what is needed** for a working stack (name, DAC/sink wiring, resilience hooks via `oceano-setup`) and **defer** streaming depth (fancy BT device management, UPnP, etc.) behind progressive disclosure or later releases.

**Implication for onboarding:** The **first-run narrative** should read: *“Make your records and CDs shine on the wall display; streaming is here when you want it.”* Checklist order and copy should reflect that priority.

---

## Goals

- A new user should know **what to do first** for **physical playback + recognition**, **what is optional**, and **what order** makes sense without reading the whole README.
- **No pre-configured amplifier** in a fresh install — amplifier identity, inputs, Broadlink, IR, and connected devices should be **introduced only inside a dedicated flow** (wizard or explicit “Set up amplifier”), not via baked-in `defaultConfig()` rows that look like “your amp is already a Magnat MR 780”.
- Surface **contextual hints** (“you have not configured X”) in the main UI and config drawer, not only deep in sub-pages.
- Complement **`oceano-setup`** (CLI on the Pi) with a **web-first path** tuned to **physical media** completion (capture, recognition, optional amp IR, optional calibration).

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

A first user opening **Configuration** sees many sections and external links without a **single ordered checklist** or “you are 2/5 steps done” model. For a **vinyl-first** user, the mental model (“REC OUT → capture → identify my records”) is disconnected from “Amplifier Control” and “Broadlink” until they read hints.

### Amplifier defaults feel “already decided”

Fresh `defaultConfig()` in `cmd/oceano-web/config.go` currently seeds a **full Magnat-oriented** `amplifier` block (`profile_id`, maker/model, inputs, timings). `amplifier.enabled` is `false`, so IR is off — but the **UI still presents an active hardware profile**, which reads as “this product assumes my amp is X” even when the user only cares about turntable + capture.

**Desired behaviour:** On a **new** install, `amplifier` should look **unconfigured** (empty `profile_id`, no implied maker/model, empty or minimal `inputs` until the user runs setup). Built-in profiles (e.g. Magnat MR 780) remain **selectable inside the wizard**, not applied silently.

### Broadlink is a separate mental step

Rough flow today: enable amplifier → choose profile → amplifier page → RM IP → pairing wizard in another tab → learn IR → save → restart. Nothing in the **header / status** distinguishes “Broadlink not paired” from “IR ready”.

### Calibration vs every input

The calibration wizard is powerful but **lists inputs in a generic way**. Users with **USB + Phono + CD** may not know that calibration matters most for **physical line paths** (and vinyl gap step only for turntable). There is no link between **“device on this input”** and **“should I calibrate this input?”**

### Streaming section competes for attention

AirPlay/BT blocks are legitimate but **visually parallel** to physical sources; for the target audience, the UI should **tier** content: physical path first, streaming as “Basics” or collapsible.

### Other defaults that look “wrong” for many users

- **Weather**: Dublin + enabled by default — unrelated to physical-media onboarding.
- **`advanced.calibration_profiles`** in `defaultConfig()`: numeric fixtures are great for **tests/dev** but **opaque** on a fresh Pi (“where did these numbers come from?”).

---

## Design principles

1. **Physical-first progressive disclosure** — minimum path: capture + recognition credentials + save/restart. Then optional: amplifier IR, per-input calibration, weather, SPI.
2. **No implied hardware** — no amplifier profile, maker/model, or input map until the user confirms hardware (wizard or import).
3. **One orchestrated story, many deep pages** — welcome checklist or wizard steps may **embed** iframes / same-origin steps or deep-link with `?step=` / `?from=onboarding`; power pages (`amplifier.html`, `recognition.html`) stay for advanced edits.
4. **Server-side completeness** — e.g. `GET /api/setup-status`: `has_capture`, `has_recognition_credentials`, `amplifier_configured`, `broadlink_paired`, `calibration_physical_complete`, etc.
5. **Device roles drive calibration scope** — see below; avoids asking a vinyl listener to “calibrate USB streaming” unless they want to.

---

## Device roles (connected equipment → input usage)

When the user defines a **connected device** (name + amplifier input IDs), they should optionally classify **what that device is for**:

| Role | Meaning | Calibration wizard | Notes |
|------|---------|-------------------|--------|
| **Physical media** | Turntable, CD player, tape, etc. — sources you want to **identify** and boundary-detect | **Offer** noise-floor calibration for those inputs; offer **vinyl transition** step if format is vinyl/turntable | Primary Oceano value |
| **Streaming** | PC/USB DAC, streamer, “Bluetooth” input on amp, etc. | **Skip by default** (no per-input noise floor required for recognition of **files** on that path — recognition is REC OUT–driven for physical). User can still override “calibrate anyway” for odd setups. | Keeps wizard short |
| **Other** | Tuner, HDMI, unknown | **Skip** unless user opts in | Copy: “Skip unless this input carries a physical source you want to recognise” |

**Config shape (proposal):** extend `ConnectedDeviceConfig` with something like `role: "physical_media" | "streaming" | "other"` (exact enum TBD). **Migration:** omit → treat as `physical_media` for backward compatibility *or* `unknown` that prompts once in UI.

**Calibration wizard behaviour:** build the list of **calibratable inputs** as the union of input IDs attached to devices with `role = physical_media`, plus any input the user manually marks “always calibrate”. Hide or de-emphasise the rest. **Vinyl gap** sub-step only when at least one physical device is tagged as vinyl/turntable (either a sub-flag `format_hint: vinyl` on the device or a dedicated role `physical_vinyl`).

**State manager / recognition:** today much logic keys off **source** and **calibration profiles** keyed by amplifier **input ID**. Device roles are primarily a **UX and scoping** layer; backend can keep using input IDs once calibration slots exist. Longer term, roles could feed **defaults** (e.g. continuity / boundary tuning) per format — out of scope for this doc except as a hook.

---

## Stylus (needle) life & listening context (roadmap-friendly)

Physical-media enthusiasts often care about **stylus hours** and **record wear**. Oceano already has **play time**, **track boundaries**, and **format hints** (Vinyl vs CD) in the architecture conversation.

**Possible additions (not committed scope):**

- Soft **listening counters** per turntable device (increment while `Physical` + `Vinyl` + audio present).
- Optional **reminder** UI (“~N hours on this stylus — replace or inspect per manufacturer guidance”) with **user-settable** target hours, not medical/engineering claims.
- Tie reminders to **connected device** “turntable” entries from the amplifier wizard.

This plan **does not** require implementing needle tracking in Phase 1–2; it records **audience alignment** so onboarding copy and future features stay coherent.

---

## Amplifier setup wizard (proposal)

**Yes — a dedicated amplifier wizard is worthwhile**, because it bundles decisions that today span `index.html` → `amplifier.html` → `pair.html` and repeats Broadlink context.

**Suggested flow (high level):**

1. **Intent** — “Do you want IR control of your amplifier?” → No: skip entire block, leave `amplifier.enabled=false` and empty topology. Yes: continue.
2. **Identity** — Maker / model (free text) **or** “Pick a built-in profile” (e.g. Magnat MR 780) to pre-fill inputs, warm-up, standby, USB-reset timings.
3. **Inputs** — Confirm visible inputs and labels (editable); match real amp front panel.
4. **Broadlink pairing (required for IR)** — If the user chose IR control, this step is **mandatory** before any IR learning: the RM4 Mini must be reachable and paired (token/device id persisted). **Implementation is flexible:** the pairing flow can stay as today’s **standalone** `pair.html` wizard (open in same tab, new tab, or embedded iframe) — separate wizards are fine. The amplifier wizard must still **surface this as an explicit gated step** (“Complete Broadlink pairing → Continue”) so nobody lands on IR learn with an unpaired bridge.
5. **IR codes** — Guided learn sequence for `power_on`, `power_off`, `volume_up/down`, `next_input`, … with skip only where unsafe; show “learned ✓” per row. **Blocked** until step 4 is satisfied.
6. **Connected devices** — For each box: name, **which input(s)** it uses, **role** (physical / streaming / other), optional “has IR remote” → second pass of IR learn for transport codes.
7. **Review + Save & restart** — single commit point.

**Relationship to existing pages:** Implement as a **new route** (e.g. `/amplifier-wizard.html`) or a modal sequence that **reuses** the same APIs as `amplifier.html` and the existing **Broadlink pairing** endpoints/UI (`pair.html`). Deprioritise requiring users to discover three unrelated URLs **without** a checklist — reusing `pair.html` as its own wizard is acceptable; the amplifier wizard **orchestrates order** and **blocks** IR steps until pairing is done.

---

## Streaming: “basics only” in product and in UI

- **`oceano-setup`** remains the right place for **mDNS, PipeWire, Bluetooth codec, shairport ALSA backend** — one shot, expert-maintained.
- **Web UI:** keep **AirPlay name**, **Bluetooth on/off**, and **output device match** visible but **collapsed** under a “Streaming basics” subsection after physical-media steps in the checklist.
- **Defer:** rich BT device gallery, UPnP, multi-zone — document as **not differentiating** for v1 of this onboarding pass.

---

## Proposed work (phased)

### Phase 1 — Empty amplifier by default + neutral globals

- **`defaultConfig()`**
  - Remove baked-in Magnat profile and input list; `amplifier.profile_id` empty; `inputs` empty or a single placeholder only if the UI requires non-empty array (prefer empty + UI handles).
  - `amplifier.enabled` remains false until wizard completes or user toggles on manually.
- **Weather:** default off; empty or null location until enabled.
- **Calibration profiles:** empty map on fresh install; move numeric fixtures to **tests** or `docs/examples/*.json` if still needed for CI.
- **UI:** “Set up amplifier” CTA opens wizard; no pre-selected profile in `<select>`.

### Phase 2 — Physical-first welcome checklist

Ordered for the target audience:

1. **System foundation** — `oceano-setup` done (DAC, capture card present).
2. **Capture** — REC OUT card, device match, optional mic-gain wizard link.
3. **Recognition** — ACRCloud (and chain at **sensible defaults**).
4. **Optional — Amplifier wizard** — identity → **Broadlink pairing (required before IR)** → IR learn → connected devices + **roles** (pairing may jump to the dedicated `pair.html` wizard; order stays explicit in the amp wizard).
5. **Optional — Calibration** — only **physical_media** (and vinyl gap where applicable).

Streaming basics can appear as **step 2b** or a compact row: “AirPlay / Bluetooth (optional)”.

### Phase 3 — Amplifier wizard implementation

- As specified in the previous section; **gate** IR-learning steps on successful Broadlink pairing (reuse `pair.html` and/or same APIs — **separate pairing wizard is OK**). Persist atomically at end (or per major milestone with explicit “saved draft” if needed).

### Phase 4 — Calibration scoped by device role

- Extend config + UI for **device role**.
- Filter calibration wizard input list; update `recognition_page.js` / `calibration-wizard.js` copy to explain **why** some inputs are hidden.
- Ensure **state manager** still receives calibration keyed by input ID (no breaking change unless we add aliases).

### Phase 5 — Contextual hints & API

- `/api/setup-status` + header chips (“Missing ACRCloud”, “Amplifier not configured”, “Calibrate Phono for best vinyl boundaries”).
- Config drawer **reorders** sections based on incomplete flags.

### Phase 6 — CLI ↔ web bridge

- **`oceano-setup`:** closing screen points to web checklist; emphasise **physical media** finish line.

---

## Success metrics (qualitative)

- A vinyl/CD listener can complete **capture + recognition** without touching amplifier IR.
- No fresh install JSON implies a **specific commercial amplifier**.
- Calibration wizard **does not nag** for USB/streaming-only inputs unless the user opts in.
- Streaming users can still get **minimal** AirPlay/BT from setup + one web subsection.

---

## Editorial: what we would add, remove, or defer

**Add**

- **Amplifier wizard** as the primary path for IR topology (your suggestion aligns with reducing cognitive load).
- **Device role** (`physical_media` / `streaming` / `other`) on connected devices — high leverage for calibration UX and future features (needle hours, format-specific copy).
- **Physical-first checklist** copy and ordering in all first-run surfaces.
- **Explicit Broadlink step** inside the amplifier wizard: **required** before IR commands; may **launch** the existing `pair.html` wizard rather than duplicating UI (separate wizards are fine — sequencing and copy matter).
- **Roadmap note** for stylus/listening-time (audience-true even if shipped later).

**Remove or shrink**

- **Default Magnat profile** (and any other OEM-shaped defaults) from `defaultConfig()`.
- **Default calibration numbers** from shipping defaults (keep in tests/examples).
- **Default Dublin weather** as “on” — turn into explicit opt-in.

**Keep (do not delete)**

- Separate **advanced** pages for power users who outgrow the wizard.
- **`oceano-setup`** for OS-level streaming resilience — do not try to replace it with pure web.

**Defer / be cautious**

- **Monolithic single-page app** — not required if wizard + checklist + status API are coherent.
- **Automatic amp detection** without IR — usually impossible; avoid promising it.
- **Over-automating role inference** from maker names — default to **user choice** with smart suggestions later.

**Risk:** `role` migration must define behaviour for **existing** `connected_devices` with no role (treat as physical or prompt once). **Mitigation:** one-time banner “Tag your devices for smarter calibration” with safe default.

---

## Out of scope (for this plan document)

- Replacing Broadlink with another IR stack.
- Full vinyl-vs-CD **automatic** classification without calibration (see CLAUDE.md — still future-heavy).

---

## Related code / docs

- Default config: `cmd/oceano-web/config.go` (`defaultConfig`, `ConnectedDeviceConfig`)
- Built-in profile resolution: `cmd/oceano-web/amplifier_profiles.go`
- Pairing UI: `cmd/oceano-web/static/pair.html`
- Amplifier UI: `cmd/oceano-web/static/amplifier.html`
- Calibration: `cmd/oceano-web/static/recognition.html`, `static/recognition/calibration-wizard.js`
- CLI wizard: `cmd/oceano-setup/`
- Architecture: `docs/amplifier-device-architecture.md`, `docs/distribution-and-setup-improvements-plan.md`
