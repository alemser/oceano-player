# Configuration & Onboarding — Improvement Plan

This document describes how first-time configuration feels today, why it is hard, and a phased plan to make setup **guided, physical-media-first, minimal by default, and opt-in** for advanced features.

### At a glance

- **Hub + wizard + roles:** [Configuration UI](#configuration-ui-cards-hub-layout-and-navigation) → [Amplifier setup wizard](#amplifier-setup-wizard-proposal) → [Device roles](#device-roles-connected-equipment--input-usage) → [Stylus wear](#stylus-needle-wear--product-differentiator--onboarding) → [Now Playing amp line](#now-playing-amplifier-line--kiosk--mobile-parity--touch-input-switch).
- **Concrete implementation order:** [Proposed work (phased)](#proposed-work-phased) — Phases **1–7** (sequential numbering; no `5b`).
- **Skim / backlog bullets:** [Editorial](#editorial-what-we-would-add-remove-or-defer) at the end intentionally mirrors roadmap intent for readers who jump to the bottom; **normative detail** remains in the body sections above.

---

## Product positioning & primary audience

**Primary audience:** People who care about **physical media** — vinyl, CD, and other line-level sources routed through a real amplifier. They want reliable **now playing** identification, sensible **track boundaries**, and a clear picture of **what is wired where**, without becoming AV installers.

**Hero experience (invest here):**

- **Track recognition** — credentials, capture level, chain choice, and understandable retry/backoff behaviour.
- **Per-setup calibration** — noise floor and (where relevant) vinyl gap detection, scoped to the inputs that actually carry physical sources.
- **Device topology** — “this box is my turntable on Phono”, “this is the CD deck on CD”, with optional IR where it helps.
- **Stylus (needle) wear observability** — **already shipped:** cumulative **vinyl hours** (and wear vs manufacturer-rated life) are hard to track from memory alone; few consumer tools expose this. Oceano ties listening to **Physical + Vinyl** sessions, persists profiles in SQLite, exposes **`GET /PUT /api/stylus`**, **`POST /api/stylus/replace`**, and the **Listening Metrics** “Stylus tracking” card (`history.html` / `history.js`) plus the stylus block on **`amplifier.html`** (`amplifier_page.js`). Onboarding must **surface** this when a **Phono / vinyl** path exists so the feature is not buried.

**Streaming (AirPlay / Bluetooth):** Important for day-to-day use, but **many products already excel** at discovery, pairing, and multi-room streaming. Oceano should ship **only what is needed** for a working stack (name, DAC/sink wiring, resilience hooks via `oceano-setup`) and **defer** streaming depth (fancy BT device management, UPnP, etc.) behind progressive disclosure or later releases.

**Implication for onboarding:** The **first-run narrative** should read: *“Make your records and CDs shine on the wall display; streaming is here when you want it.”* Checklist order and copy should reflect that priority.

---

## Goals

- A new user should know **what to do first** for **physical playback + recognition**, **what is optional**, and **what order** makes sense without reading the whole README.
- **No pre-configured amplifier** in a fresh install — amplifier identity, inputs, Broadlink, IR, and connected devices should be **introduced only inside a dedicated flow** (wizard or explicit “Set up amplifier”), not via baked-in `defaultConfig()` rows that look like “your amp is already a Magnat MR 780”.
- Surface **contextual hints** (“you have not configured X”) in the main UI and config drawer, not only deep in sub-pages.
- Complement **`oceano-setup`** (CLI on the Pi) with a **web-first path** tuned to **physical media** completion (capture, recognition, optional amp IR, optional calibration, **optional stylus setup** when vinyl is in use).

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
4. **Device roles drive calibration scope** — see below; avoids asking a vinyl listener to “calibrate USB streaming” unless they want to.

**Implementation note (not a principle):** first-run checklist completion and hub status text should be driven by **`GET /api/setup-status`** — see **Phase 5** and the **draft JSON contract** below for field names.

---

## Configuration UI: cards, hub layout, and navigation

### What exists today

The main config surface is a **side drawer** (`index.html` + `index.css`) with **stacked blocks**. Each block uses `.section`, which already looks **card-like** (surface background, border, radius, padding). However:

- The drawer **forces a single column** (`.config-drawer .sections { grid-template-columns: 1fr }`), so users get a **long vertical scroll** of dense form fields.
- **Deep work** (recognition chain, calibration, amplifier IR) mostly lives on **separate pages**, reached via **small text links** (“Configure … ↗”), which read as secondary despite being important.

So the product already has “cards” visually, but not a **hub mental model**: everything feels like one big form rather than “pick an area, go deep, come back.”

### Opinion: yes — a hub of large cards would help

**Recommendation:** treat the configuration entry point as a **hub** first, then **detail views** (existing or new routes).

| Hub card (example) | Shows on card | Tap / primary action |
|--------------------|---------------|------------------------|
| **Physical media** | Capture status, “ACRCloud: set / missing”, mic gain hint; **when a vinyl path exists** (Phono + `physical_format: vinyl` or equivalent), a **one-line stylus summary** (“~12h / 500h rated — OK”) or **“Configure stylus tracking”** so wear observability is visible from the hub without opening three pages | → `recognition.html` (or split: capture vs providers); **stylus** → `amplifier.html` stylus block (same APIs as today) |
| **Amplifier & IR** | “Not set up” / “Broadlink OK, 4/8 IR learned” | → amplifier wizard or `amplifier.html` |
| **Streaming basics** | AirPlay name, BT on/off summary | → inline quick fields *or* lightweight sub-page |
| **Display & idle** | Now playing / weather summary | → existing sections or `nowplaying`-related UI |
| **Advanced** | “Sockets, paths, library” | → `advanced.html` |

**Large icons (or simple illustrations)** on each card are worthwhile **if**:

- Every card has a **visible title and short subtitle** (never icon-only — accessibility and clarity on a Pi browser at arm’s length).
- **Status text** is fed from real state (`/api/setup-status` or a slim summary endpoint), not static copy — e.g. “Capture: USB Audio OK” vs “Missing ACRCloud host”.
- The hub stays **scannable in under five seconds**; icons support recognition, they do not replace explanations.

**What to avoid**

- **Duplicating** every field from `recognition.html` / `amplifier.html` on the hub — double maintenance and overwhelming first screen.
- **More than ~5–7 hub tiles** without grouping — use a **“Physical media”** group and a **“Everything else”** collapsed region to preserve the audience-first story.
- **Relying on the narrow drawer** for a rich 2×2 card grid on desktop — the drawer is ~520px wide; a **dedicated `/config` hub page** (full width) or widening the drawer on large breakpoints may be needed for comfortable large tiles.

### Hub rollout steps (non-prescriptive — **not** the same numbers as [Proposed work (phased)](#proposed-work-phased))

These three steps are **UI sequencing** for the hub only; do not confuse them with roadmap **Phase 1–7**.

1. **Hub step 1 — Content-only:** reorder existing `.section` blocks inside the drawer (physical first, streaming collapsed) — low effort, partial win.
2. **Hub step 2 — Hub layer:** replace or precede the long form with **click-through cards**; keep **quick save** for users who only change one global field, or move “Save & restart” to a sticky footer visible from hub and detail pages.
3. **Hub step 3 — Responsive:** on wide viewports, **two columns of hub cards**; on mobile, single column; optional full-page hub when opened from first-run checklist.

This aligns with the **physical-first onboarding** narrative and with **progressive disclosure**: the hub answers “where do I go?”; detail pages answer “how do I tune it?”

---

## Now Playing: amplifier line — kiosk / mobile parity + touch input switch

### Display scope (this plan)

**HDMI only:** kiosk and now playing UX below assume a **local panel attached via HDMI** (e.g. 7" 1024×600), which is what has been validated in testing. **DSI** (and other connectors) may work with the same installer stack but are **out of scope** for this plan until separately tested — do not infer parity or touch behaviour for DSI from this document.

### Today (baseline)

In `cmd/oceano-web/static/nowplaying.css`, **`#top-controls`** (which contains **`#amp-indicator`**) is hidden under **`@media not (pointer: coarse)`** together with **`#input-selector`**. The stylesheet comment mentions *non-touch displays* broadly; **this plan** focuses on the observed **HDMI kiosk** case: a fine pointer (mouse / trackpad) **does not show** the amplifier name chip, while a **phone** with coarse pointer often **does**. The playing UI therefore **under-communicates** “which amp / which input” on the very screen people watch from the sofa.

### Target UX

1. **Parity** — Show a **single compact line** (or pill cluster) on **both** touch-first and non-touch / **HDMI kiosk** layouts: **amplifier identity** (maker + model, or a user label) and **one resolved input line** that uses the **same labelling rules as the remote input dropdown** (`index.amplifier.js` / `renderAmpInputSelect`): when a connected device maps to **a single** amplifier input, **the device name replaces the logical input** (e.g. *Rega Planar* instead of duplicating *Phono*); when a device spans **multiple** inputs, show **`Device — logical`** (e.g. *Streamer — USB Audio*). If no device is mapped, show the **logical input** only (e.g. *Phono*). Same information architecture on mobile and on **1024×600** (or similar) **HDMI**; only **density** and **interaction** differ.
2. **Touch only for switching** — When the runtime reports **touch-capable** interaction (`pointer: coarse` and/or `maxTouchPoints` — same signals already used for hiding non-touch chrome), **tap the amplifier cluster** opens a **small, elegant** surface (dropdown anchored to the pill, or a slim sheet): list **visible inputs** from the same source as the main amp widget (`/api/amplifier/...`), current selection highlighted, dismiss on outside tap or timeout. Keep hit targets and motion **subtle** so the wall display does not feel like a tablet game.
3. **Non-touch kiosk** — Still render the **read-only** line (amp + input + device). **No** mandatory dropdown under mouse hover at 2–3 m viewing distance; optional future: long-path to config UI only if product wants it.

### Data

Requires **stable strings** in unified state and/or **`GET /api/amplifier/state`** (and related) for: resolved maker/model, **active input logical name**, and **resolved connected device label** for that input. If any piece is missing today, extend the contract once — the **wizard’s connected devices** model is the natural source of device names.

### Fit in this plan

Yes — this belongs here as **cross-cutting display + configuration outcome**: users invest in the amplifier wizard / topology so the **living room screen** should reflect it **everywhere**, not only on coarse pointers. Track implementation under **`nowplaying.html` / `nowplaying.css` / `nowplaying/main.js`** and small API/state extensions as needed.

---

## Device roles (connected equipment → input usage)

When the user defines a **connected device** (name + amplifier input IDs), they should optionally classify **what that device is for**:

| Role | Meaning | Calibration wizard | Notes |
|------|---------|-------------------|--------|
| **Physical media** | Turntable, CD player, tape, etc. — sources you want to **identify** and boundary-detect | **Offer** noise-floor calibration for those inputs; offer **vinyl transition** step when **`physical_format` is `vinyl`** (or logical input is Phono and user confirms vinyl — see below) | Primary Oceano value |
| **Streaming** | PC/USB DAC, streamer, “Bluetooth” input on amp, etc. | **Skip by default** (no per-input noise floor required for recognition of **files** on that path — recognition is REC OUT–driven for physical). User can still override “calibrate anyway” for odd setups. | Keeps wizard short |
| **Other** | Tuner, HDMI, unknown | **Skip** unless user opts in | Copy: “Skip unless this input carries a physical source you want to recognise” |

**Config shape:**

- **`role`** on each `connected_devices[]` row — JSON string enum: **`physical_media`**, **`streaming`**, **`other`** (snake_case).
- **`physical_format`** (optional, only meaningful when `role === physical_media`) — **`vinyl`**, **`cd`**, **`tape`**, **`mixed`**, or **`unspecified`** (default when absent). This is the **user’s statement of intent** (“this box is my turntable on Phono”), not a runtime detector: it drives **vinyl gap** copy, **Now Playing** format chips, and **stylus hour accumulation** (which already keys off **Physical + Vinyl** in the running system).

**Migration (decided):** if `role` is **absent**, treat as **`physical_media`**. If `physical_format` is **absent**, treat as **`unspecified`** — UI may **nudge** once: “Is this device vinyl, CD, or tape?” with emphasis when the mapped **logical input label** is **Phono** (strong prior for vinyl). Do not auto-write `vinyl` without user confirmation.

**Calibration wizard behaviour:** calibratable inputs = union of input IDs on `physical_media` devices, plus manual “always calibrate”. **Vinyl gap** sub-step only when **`physical_format === vinyl`** (or product-approved equivalent).

**Stylus onboarding gating:** show **“Configure stylus tracking”** in the **first-run checklist**, **hub Physical media card**, and optionally a **wizard sub-step** when **`physical_format === vinyl`** *or* when the device is mapped to a **Phono** input and the user has confirmed vinyl — not for CD-only or `unspecified` unless the user opts in.

**State manager / recognition:** roles and `physical_format` are primarily **UX and scoping**; backend continues to use **source**, **format**, and **input ID**–keyed calibration. Align config with what **`/api/stylus`** and session logic already expect for **Vinyl** play time.

---

## Stylus (needle) wear — product differentiator & onboarding

### Why this matters in the spec

Most listeners **cannot estimate** how many **vinyl hours** they have put on a stylus; manufacturer ratings (e.g. ~500 h) are easy to forget, and subjective “it still sounds fine” arrives **late**. Oceano already **accumulates listening time** when playback is classified as **physical vinyl** and surfaces it in **Listening Metrics** and the **amplifier** stylus UI — that is a **differentiating** story for the physical-media audience and should be **explicit in onboarding**, not only in a roadmap footnote.

### What exists today (baseline — do not re-spec from scratch)

- **HTTP API:** `GET` / `PUT` `/api/stylus`, `GET` `/api/stylus/catalog`, `POST` `/api/stylus/replace` (see `cmd/oceano-web/stylus_api.go` and tests).
- **UI:** **Listening Metrics** (`history.html` / `history.js`) — “Stylus tracking” card with hours and wear context; **`amplifier.html`** / `amplifier_page.js` — catalog profile, rated hours, replace flow.
- **Semantics:** counters are meaningful when sessions are **Vinyl** + **physical** listening; CD-only users should not see nagging stylus copy.

### Onboarding & configuration (target)

1. **Gate on intent, not on IR** — Stylus setup must remain available when **`amplifier.enabled === false`** (no Broadlink): topology + `physical_format: vinyl` is enough to show the CTA. If the current UI ties stylus visibility to “amplifier enabled”, consider **relaxing** that for vinyl-first users (implementation detail — product call).
2. **Wizard / connected devices** — After naming a deck and assigning **Phono**, prompt: **“Is this turntable (vinyl)?”** → sets `physical_format: vinyl` → unlocks checklist row **“Optional: set stylus model & rated life”** with deep link to existing stylus controls.
3. **Checklist & hub** — Add **`stylus_tracking_recommended`** / **`stylus_profile_configured`** (names TBD) to **`GET /api/setup-status`** so the Physical media card can show **progress**, not a dead link.
4. **Copy discipline** — Frame as **observability** (“hours vs your chosen rated life”), not medical or guaranteed wear science.

### Improvements beyond today’s UI (prioritised ideas)

| Idea | Rationale |
|------|-----------|
| **Compact stylus chip on Now Playing** (read-only, HDMI-safe) | Sofa-distance reminder without opening Metrics or amplifier page |
| **Threshold banner** | When usage crosses a user-defined % of rated life, one-line banner on hub or header (optional dismiss) |
| **Export / backup** | CSV or JSON of stylus history for users who archive gear notes |
| **Replace flow from checklist** | After mounting a new stylus, deep-link **`POST /api/stylus/replace`** preflight from onboarding success screen |
| **Clarify CD vs vinyl in one place** | Single sentence in wizard: “Stylus hours apply only when playback is detected as **Vinyl**” — reduces support confusion |

---

## Amplifier setup wizard (proposal)

**Yes — a dedicated amplifier wizard is worthwhile**, because it bundles decisions that today span `index.html` → `amplifier.html` → `pair.html` and repeats Broadlink context.

**Suggested flow (high level):**

1. **Intent** — “Do you want IR control of your amplifier?” → **No:** set `amplifier.enabled=false`, **skip Broadlink and IR learning**, but **still offer** identity (maker/model or built-in profile for **input map / IDs**) and the **connected devices** step (name, input IDs, **role**: physical media / streaming / other) so calibration scoping, UI labels, and recognition context stay correct. **Yes:** continue with full IR path.
2. **Identity** — Maker / model (free text) **or** “Pick a built-in profile” (e.g. Magnat MR 780) to pre-fill inputs, warm-up, standby, USB-reset timings.
3. **Inputs** — Confirm visible inputs and labels (editable); match real amp front panel.
4. **Broadlink pairing (required for IR)** — If the user chose IR control, this step is **mandatory** before any IR learning: the RM4 Mini must be reachable and paired (token/device id persisted). **Implementation is flexible:** the pairing flow can stay as today’s **standalone** `pair.html` wizard (open in same tab, new tab, or embedded iframe) — separate wizards are fine. The amplifier wizard must still **surface this as an explicit gated step** (“Complete Broadlink pairing → Continue”) so nobody lands on IR learn with an unpaired bridge.
   - **Gate mechanism:** the **Next** control that advances from Broadlink → IR learn stays **disabled** until persisted credentials exist (`broadlink.host` + non-empty `token` / `device_id` as today’s save path requires). Returning users see **Already paired — continue** when the config already satisfies the gate. (The disposable HTML prototype models this with an explicit “Paired — next” affordance.)
5. **IR codes** — Guided learn sequence for `power_on`, `power_off`, `volume_up/down`, `next_input`, … with skip only where unsafe; show “learned ✓” per row. **Blocked** until step 4 is satisfied.
6. **Connected devices** — For each box: name, **which input(s)** it uses, **role** (physical / streaming / other), and when **physical**: **`physical_format`** (`vinyl` | `cd` | `tape` | `mixed` | `unspecified`). If the chosen input is **Phono** and format is still unspecified, use a **single follow-up** (“Turntable / vinyl?”) to set **`vinyl`** without extra clutter. Optional “has IR remote” → second pass of IR learn for transport codes.
7. **Optional — Stylus** — When **`physical_format === vinyl`** (or confirmed Phono turntable), show a short step: **“Track needle hours”** with link to the existing **`amplifier.html`** stylus block (or inline embed). Skip entirely for streaming-only or CD-only topology.
8. **Review + Save & restart** — single commit point.

**Relationship to existing pages:** Implement as a **new route** (e.g. `/amplifier-wizard.html`) or a modal sequence that **reuses** the same APIs as `amplifier.html` and the existing **Broadlink pairing** endpoints/UI (`pair.html`). Deprioritise requiring users to discover three unrelated URLs **without** a checklist — reusing `pair.html` as its own wizard is acceptable; the amplifier wizard **orchestrates order** and **blocks** IR steps until pairing is done.

### Returning users: new amplifier or rewiring (e.g. owner swaps hardware)

The same wizard should remain useful **after** the first day — not only for strangers. For **you** changing amp or rearranging inputs:

- **Entry point:** a clear **“Change amplifier / re-run setup”** (or re-open wizard) so you do not hunt through `amplifier.html` fields from memory.
- **IR:** almost always **re-learn** amp commands when the model changes; Broadlink credentials often **stay** (same RM4) — the doc’s **gated Broadlink step** can show “already paired — continue” when token/host are valid.
- **Calibration:** `advanced.calibration_profiles` are keyed by **amplifier input ID**. If the new amp or profile uses **different IDs**, stale rows should be **surfaced** (“Phono used to be `20`, now it is `10` — re-run calibration for Phono”) instead of silently wrong behaviour. Optional: **export calibration** before swap, or a one-click “clear profiles for IDs no longer present”.
- **Connected devices + roles:** re-attach turntable/CD to the **new** input labels in the wizard; keeps the **physical-first calibration filter** accurate.

This closes the loop for **repeat configuration** as a first-class story, not only first boot.

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
4. **Optional — Amplifier wizard** — identity → **Broadlink pairing (required before IR)** → IR learn → connected devices + **roles** + **`physical_format`** where relevant (pairing may jump to the dedicated `pair.html` wizard; order stays explicit in the amp wizard).
5. **Optional — Calibration** — only **physical_media** (and vinyl gap where **`physical_format === vinyl`**).
6. **Optional — Stylus (vinyl path only)** — Shown when **`stylus_tracking_recommended`** is true (e.g. vinyl topology present but stylus profile not completed). Row text: **“Track stylus hours”** → existing **`amplifier.html`** / Metrics flows; **dismiss** or **complete** updates **`stylus_profile_configured`** (or equivalent) in **`/api/setup-status`**.

Streaming basics can appear as **step 2b** or a compact row: “AirPlay / Bluetooth (optional)”.

**Checklist “done” rules (implementation):** each row is **complete** when the corresponding booleans from **`GET /api/setup-status`** are true — use the **field names in the draft contract** below (`capture_configured`, `recognition_credentials_set`, `amplifier_topology_complete`, `calibration_physical_complete`, `stylus_profile_configured`, etc.). **Dismissed checklist** can remain a **client** flag (`localStorage`) so we do not invent new server state unless product wants sync across browsers.

### Phase 3 — Amplifier wizard implementation

- As specified in the previous section; **gate** IR-learning steps on successful Broadlink pairing (reuse `pair.html` and/or same APIs — **separate pairing wizard is OK**). Persist atomically at end (or per major milestone with explicit “saved draft” if needed).

### Phase 4 — Calibration scoped by device role (+ physical format)

- Extend config + UI for **`role`** on `connected_devices` (`physical_media` | `streaming` | `other`); **migration:** missing `role` → **`physical_media`** (see [Device roles](#device-roles-connected-equipment--input-usage)).
- Add **`physical_format`** on `physical_media` rows (`vinyl` | `cd` | `tape` | `mixed` | `unspecified`); **migration:** missing → **`unspecified`** with optional one-time UI nudge (stronger when input is **Phono**).
- Filter calibration wizard input list; update `recognition_page.js` / `calibration-wizard.js` copy to explain **why** some inputs are hidden.
- Ensure **state manager** still receives calibration keyed by input ID (no breaking change unless we add aliases).

### Phase 5 — Contextual hints & API

- Implement **`GET /api/setup-status`** (or extend an existing aggregate) returning the booleans needed for the hub, checklist, and header chips (“Missing ACRCloud”, “Amplifier not configured”, “Calibrate Phono for best vinyl boundaries”, etc.).
- Config drawer **reorders** sections based on incomplete flags.

#### `GET /api/setup-status` — draft JSON contract

Single JSON object (HTTP 200). **`schema_version`** lets clients evolve without silent breakage.

| Field | Type | Meaning |
|-------|------|--------|
| `schema_version` | `int` | Start at **1**; bump when fields are added or semantics change. |
| `oceano_setup_acknowledged` | `bool` | User confirmed CLI wizard (optional; may stay `false` forever if product skips). |
| `capture_configured` | `bool` | Non-empty capture device resolution (explicit ALSA or successful `device_match`). |
| `recognition_credentials_set` | `bool` | ACRCloud host + key + secret present (or whichever provider is “required”). |
| `amplifier_topology_complete` | `bool` | At least one input map **or** wizard marked “inputs OK” (exact rule TBD when empty amp is valid). |
| `amplifier_ir_enabled` | `bool` | `amplifier.enabled === true`. |
| `broadlink_paired` | `bool` | Host + token/device id present when IR enabled. |
| `calibration_physical_recommended` | `bool` | At least one `connected_devices[].role === physical_media` and no calibration row for that input yet. |
| `calibration_physical_complete` | `bool` | All **recommended** physical inputs have usable `calibration_profiles` entries (product-defined “complete”). |
| `vinyl_topology_present` | `bool` | At least one `connected_devices[]` row has `role === physical_media` and `physical_format === vinyl`, **or** product-defined equivalent (e.g. Phono + user-confirmed turntable flag). |
| `stylus_tracking_recommended` | `bool` | `vinyl_topology_present` and the user has **not** yet completed stylus onboarding (e.g. no active stylus profile / rated hours — exact rule should mirror what the Metrics card considers “not set up”). |
| `stylus_profile_configured` | `bool` | Stylus settings sufficient for wear display (e.g. active profile + rated hours in config/DB — align with `GET /api/stylus` success semantics). |
| `services_healthy` | `object` | Map service id → bool (optional; sourced from systemd or last heartbeat). |

**Example (fresh install):**

```json
{
  "schema_version": 1,
  "oceano_setup_acknowledged": false,
  "capture_configured": false,
  "recognition_credentials_set": false,
  "amplifier_topology_complete": false,
  "amplifier_ir_enabled": false,
  "broadlink_paired": false,
  "calibration_physical_recommended": false,
  "calibration_physical_complete": false,
  "vinyl_topology_present": false,
  "stylus_tracking_recommended": false,
  "stylus_profile_configured": false,
  "services_healthy": {
    "oceano_source_detector": true,
    "oceano_state_manager": true,
    "oceano_web": true
  }
}
```

**Checklist row mapping (suggested):** row 1 ↔ `oceano_setup_acknowledged`; row 2 ↔ `capture_configured`; row 3 ↔ `recognition_credentials_set`; row 4 (optional amp) ↔ `amplifier_topology_complete` **or** explicit skip flag if added; row 5 ↔ `calibration_physical_complete` **or** “skipped” boolean if product adds it; row 6 (optional stylus) ↔ `stylus_profile_configured` **or** dismiss when `stylus_tracking_recommended` is false.

### Phase 6 — Now Playing amplifier line (see dedicated section above)

- **CSS:** stop tying **visibility** of the whole `#top-controls` / amp cluster only to `pointer: coarse` for **read-only** identity; reserve `not (pointer: coarse)` for hiding **touch-only** chrome (e.g. full `#input-selector` sheet) if still desired, or split selectors so **HDMI kiosk** always shows amp + input + device text (validate on **HDMI** first; revisit after DSI testing if needed).
- **API/state:** expose logical input + connected device label for the now playing page.
- **Touch:** tap amp cluster → compact input list; reuse existing amp APIs where possible.

### Phase 7 — CLI ↔ web bridge

- **`oceano-setup`:** closing screen points to web checklist; emphasise **physical media** finish line.

---

## Success metrics (qualitative)

- A vinyl/CD listener can complete **capture + recognition** without touching amplifier IR.
- No fresh install JSON implies a **specific commercial amplifier**.
- Calibration wizard **does not nag** for USB/streaming-only inputs unless the user opts in.
- Streaming users can still get **minimal** AirPlay/BT from setup + one web subsection.
- **HDMI kiosk and phone** both show **amplifier + input + device** when configured; touch surfaces can change input without opening the full config UI.
- A **vinyl-first** user who maps **Phono** sees a **clear path** to **stylus tracking** (checklist + hub) without discovering `history.html` by accident.

### Quantifiable targets (engineering “done” hints)

These complement the qualitative list; tune thresholds during implementation.

- **`/api/setup-status`:** returns 200 with all **required** booleans true within **N s** after a valid `config.json` + services healthy (define `N` per environment).
- **First-run checklist:** ≥ **90%** of checklist rows derive from server booleans (not only `localStorage`), so a second browser session agrees with the first.
- **Calibration wizard:** with only `streaming` / `other` roles on all devices, **zero** per-input calibration slots offered (manual override excluded) — covered by an automated UI test or integration assertion where feasible.

---

## Editorial: what we would add, remove, or defer

*Skim-oriented duplicate of roadmap intent; **authoritative** acceptance criteria live in phased sections and in [Success metrics](#success-metrics-qualitative) above.*

**Add**

- **Config hub with large navigational cards** (status line + icon + deep link) — documented in **Configuration UI: cards, hub layout, and navigation** (same document).
- **Amplifier wizard** as the primary path for IR topology (your suggestion aligns with reducing cognitive load).
- **Device role** (`physical_media` / `streaming` / `other`) plus **`physical_format`** on physical rows — high leverage for calibration UX, **vinyl gap** gating, and **stylus** onboarding.
- **Physical-first checklist** copy and ordering in all first-run surfaces.
- **Explicit Broadlink step** inside the amplifier wizard: **required** before IR commands; may **launch** the existing `pair.html` wizard rather than duplicating UI (separate wizards are fine — sequencing and copy matter).
- **Now Playing amplifier line** — kiosk/mobile parity, optional touch-only input dropdown (**Now Playing: amplifier line** section + **Phase 6**).
- **Stylus wear** as a **promoted** checklist + hub + **`setup-status`** story — the **feature is already shipped**; remaining work is **discovery, gating on vinyl topology, and polish** (Now Playing chip, thresholds, export) per [Stylus section](#stylus-needle-wear--product-differentiator--onboarding).

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

**Risk (resolved in body):** missing `role` → **`physical_media`**; optional banner to refine roles.

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
- Architecture: `docs/architecture/amplifier-device-architecture.md`, `docs/plans/distribution-and-setup-improvements-plan.md`
- Recognition roadmap (low-score **carousel of alternatives**): `docs/plans/recognition-enhancement-plan.md` → section **Roadmap: low-confidence matches — primary pick + alternative carousel**; PR **R9** in the same document.
- Now playing layout: `cmd/oceano-web/static/nowplaying.html`, `nowplaying.css` (search `pointer: coarse`, `#top-controls`, `#amp-indicator`, `#input-selector`), `nowplaying/main.js` (`loadAmpPowerState`).
