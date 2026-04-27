# Mobile companion app — product & delivery plan

This document proposes a **phone and tablet companion** for Oceano Player that **sits alongside** today’s **`oceano-web`** on the Raspberry Pi. It is **not** a replacement for the web stack: the Pi remains the single source of truth, configuration editor, kiosk **Now Playing** surface, and streaming stack entry point (AirPlay / Bluetooth / PipeWire as today).

**Principles**

1. **Physical media is the soul; streaming is the daily air** — the app must feel excellent for **vinyl / CD / physical** context (recognition, stylus, amp topology, calibration hints) **without** demoting **AirPlay / Bluetooth** to second-class status.
2. **LAN-first, optional cloud never** for v1 — the app talks to **`http(s)://<pi>:8080`** (or Bonjour-resolved host) the same way a laptop browser does today.
3. **Creative surface, boring integration** — prefer **reuse of existing HTTP + SSE contracts** over duplicating business logic on the device.

---

## Positioning

| Layer | Role |
|-------|------|
| **`oceano-web` (Pi)** | Canonical **config**, **static UIs** (drawer, `amplifier.html`, `recognition.html`, kiosk `nowplaying.html`), **REST + SSE**, future **`/api/setup-status`**. Keeps **zero-install** access from any browser on the LAN. |
| **Mobile app** | **Glanceable companion** from the sofa, kitchen, or garden: live state, quick actions, push-friendly surfaces, optional **widgets / Live Activities**. Deep-links into **full web** when the user needs power tools (e.g. full calibration chain editor). |
| **Streaming stack** | **Unchanged ownership** on the Pi (`oceano-setup`, shairport, BlueZ, PipeWire). The app **surfaces** streaming state from unified state + config; it does **not** re-implement discovery or audio routing. |

---

## What the app adds (beyond “Safari to the Pi”)

### Differentiation tied to Oceano strengths

1. **Physical pulse** — Large, calm **“Physical / Vinyl / CD / None”** readout with the same **track / VU / identifying** semantics as `/api/stream`, optimised for **arm’s-length** reading (typography, reduced chrome). Optional **haptic tick** on source transitions (respect user setting).
2. **Stylus story on the phone** — **Wear vs rated hours** and “time since last replace” in a **single card**, synced from existing **`/api/stylus`** + session logic; **one-tap** “I replaced the needle” → `POST /api/stylus/replace` with a confirmation sheet (no need to open `amplifier.html` on the Pi browser).
3. **Amplifier & topology from the pocket** — **Current amp + resolved input line** (same rules as **Now Playing** + remote dropdown: device name replaces redundant **Phono** when mapped). **Touch-first input picker** reusing `/api/amplifier/...` contracts; power / volume only where IR is enabled and safe.
4. **Recognition companion** — **Force recognise** (if/when exposed like the web status row), **last match / no match** hint, link to **Listening Metrics** (`history.html`) in embedded web view or Safari. Optional **low-confidence carousel** (future **R9**) becomes **swipeable** on phone first.
5. **Setup sidekick (non-blocking)** — Read-only **`/api/setup-status`** dashboard: “capture OK”, “ACRCloud missing”, “calibrate Phono”, “stylus not configured”. **Deep link** into the right **`oceano-web`** page for edits — the app does not rewrite `config.json` parsing in v1 unless a narrow **safe** API exists.

### Streaming stays first-class

6. **Stream bridge** — Dedicated **AirPlay / Bluetooth** strip: source, **playing / idle**, metadata when present (AVRCP parity with state file), **Bluetooth codec chip** when state exposes it (same mental model as **Now Playing** chips). No implication that streaming is “legacy”; copy treats **physical vs stream** as **equal lanes** with clear **priority story** only when both are active (mirror product rules from unified state).
7. **Multi-room of one** — Optional **widget** showing **whatever the Pi thinks is live** (physical or stream) so a **quick glance** does not require unlocking Safari bookmarks.

### Creative “delight” backlog (post-MVP)

8. **Live Activities (iOS) / rich ongoing notification (Android)** — “Now playing on Oceano” when the app is not foreground, fed by **background refresh** + last SSE snapshot (policy: **low frequency**, user opt-in, Pi LAN only).
9. **Siri Shortcuts / App Actions** — e.g. “What’s playing on Oceano?” → spoken summary from cached state (requires **local network** permission storytelling).
10. **Guest mode** — Read-only URL + optional PIN stored in app keychain (if product adds **lightweight gate** on Pi later); out of scope until security model is defined.

---

## Information architecture (suggested tabs)

| Tab | Primary content | Streaming |
|-----|-----------------|-----------|
| **Now** | Unified **now playing** card + chips (physical + stream) + **VU** if present in payload | Same card when source is AirPlay/BT/UPnP |
| **Physical** | Stylus card, **identifying** state, format (Vinyl/CD), optional **calibration nudge** | Collapsed or empty when not physical |
| **Amp** | Topology line, input picker, IR actions if enabled | N/A |
| **More** | Links to **full web** (config, recognition, metrics), **About**, **Pi URL** editor | Link to streaming basics in web drawer |

Tabs are **suggestions**; a **single-scroll “Oceano home”** with sections may test better on small phones.

---

## Technical approach

### Stack (recommendation)

- **Default recommendation:** **React Native** (TypeScript) for **one team**, **shared types** with a small **OpenAPI or hand-written client** against existing routes. Add **native modules** only where needed (Bonjour, background tasks, widgets).
- **Escape hatch:** If **widgets / Live Activities / CarPlay** become the main bet, budget **Swift + Kotlin** for those surfaces only, still calling the same JSON APIs from a thin shared **Kotlin Multiplatform** or duplicated client — product decision when Phase C starts.

### Discovery & connectivity

- **Manual URL** (MVP): `http://192.168.x.x:8080` — matches today.
- **Bonjour / mDNS** (`_http._tcp` or dedicated **`_oceano._tcp`** if registered): reduces friction; **document** guest-Wi-Fi / AP isolation as a **support article**, not an app bug.
- **TLS:** Optional **self-signed** or **mkcert** on LAN — app must allow **user-trusted** cert or HTTP-only toggle with **clear risk copy** (v1 can stay **HTTP on trusted LAN** only).

### APIs to lean on (no new server required for MVP)

- **`GET /api/stream`** (SSE) or polling **`/tmp/oceano-state.json` proxy** — same as web; app implements **reconnect with backoff** like `nowplaying`.
- **`GET /api/config`** (read-only for dashboard), **`GET /api/amplifier/state`**, amplifier input APIs, **`/api/stylus`**, **`GET /api/setup-status`** when shipped.
- **Deep links:** `oceano://open?path=/recognition.html` **optional**; v1 can use **`SFSafariViewController` / Chrome Custom Tabs** to `http://pi:8080/...`.

---

## Phased delivery

### Phase A — Foundations (4–8 weeks, one engineer order-of-magnitude)

- Project bootstrap, **LAN client**, **SSE or poll**, **Pi URL** persistence, **single “Now” screen** with parity to core state JSON.
- **Error UX:** Pi unreachable, wrong URL, **401** if auth is added later.
- **Internal dogfood** on home Wi-Fi.

### Phase B — Physical + amp depth

- **Stylus** screen + **replace** flow; **Amp** tab with input line + picker; **Physical** tab with identifying / format chips.
- **Embed or link** to **Listening Metrics** for history-heavy views.

### Phase C — Streaming polish + system integration

- **Widget** (Android + iOS) with **last known state**; **Live Activity** spike on iOS.
- **Shortcuts** / **App Actions**; **localisation** (EN first, PT if product wants).

### Phase D — Distribution & trust

- **TestFlight** + **Play internal testing**; store listings emphasising **“companion to your Oceano Pi on your network”** (not a generic music player).
- **Privacy nutrition labels:** local network, no analytics until explicitly added.

---

## Distribution challenges (explicit)

| Challenge | Mitigation |
|-----------|------------|
| **Store review** | Clear copy: **local network device companion**, no copyrighted music playback claims; screenshots show **metadata from own hardware**. |
| **Update skew** | **Minimum app version** check against optional **`/api/version`** on Pi; friendly “update app” banner. |
| **mDNS blocked** | **Manual URL** + in-app **FAQ** (guest network, VLAN). |
| **HTTP cleartext** | Android **cleartext** manifest exception for user-entered hosts; iOS **ATS exception** for local IPs if needed — document **dev vs release** posture. |
| **Support surface** | **Two moving parts** (app version + Pi image); keep **release notes** cross-linked from README / GitHub Releases. |

---

## Non-goals (v1)

- **Replacing** `oceano-web` or **editing** full `config.json` graph from the app without the same validation as Save & restart on the Pi.
- **Cloud account**, **remote access** outside LAN, or **subscription** unless product strategy changes.
- **Playing audio on the phone** from the Pi stream (different product; huge scope).

---

## Success metrics (qualitative + light quantitative)

- **Daily opens** on same LAN as Pi (dogfood + beta).
- **% sessions** where user triggers **stylus replace** or **input change** from app vs web (proxy for value).
- **Support tickets** about “can’t connect” vs **mDNS** adoption (manual URL fallback success).
- **NPS** among beta users who use **both** vinyl and **AirPlay weekly** — product must not feel “vinyl-only”.

---

## Documentation & repo hygiene

- Keep this file as the **mobile roadmap**; when the first repo or binary exists, add a **`mobile/`** or external repo pointer here and a **Quick link** in [docs/README.md](../README.md).
- Any **new HTTP contract** the app needs should be added to **`oceano-web`** first with **tests**, same as other APIs — the app is a **client**, not a second backend.

---

## Open decisions (before Phase A kickoff)

1. **Single app vs two store listings** — one codebase (RN) still ships two binaries; branding can be unified **“Oceano”**.
2. **Auth model** — read-only MVP vs optional **PIN** on Pi (future).
3. **iPad** — treat as **first-class** layout (two-column) or phone-only v1.

---

## Summary

A mobile app should **amplify** what Oceano already does best — **physical media truth** on the REC path, **stylus observability**, **amplifier topology**, **recognition transparency** — while giving **streaming** the **same live dignity** in the UI. **`oceano-web` stays** as the Pi’s **operator console and kiosk**; the app is the **pocket layer** on top, with phased delivery and honest LAN-first distribution expectations.
