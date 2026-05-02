# Recognition: Flexible Providers, AcoustID Default, and Secret Handling

## Purpose

This plan describes how to evolve Oceano Player’s recognition stack from the current **ACRCloud-first / Shazam-optional** model (fixed chain enums in `recognition_setup.go`) toward a **flexible, ordered provider list** with **AcoustID (Chromaprint + MusicBrainz)** as the **default** bundled path, optional **ACRCloud** and **Shazam** when the user supplies credentials, and a **clear security model** for those credentials—especially the goal of **not persisting commercial API secrets on the Pi** when the user prefers the **iOS companion app** as the trust anchor.

It complements `docs/plans/recognition-provider-chain-improvement.md` (roles, quotas, parallel mode) and should be read together with `docs/cross-repo-sync.md` for any API or config contract changes affecting `oceano-player-ios`.

---

## Current state (codebase snapshot)

| Area | Today |
|------|--------|
| Capture | `captureFromPCMSocket` writes **S16_LE stereo 44100 Hz** WAV (`cmd/oceano-state-manager/recognizer.go`). |
| Chain | `RecognizerChain` string enum: `acrcloud_first`, `shazam_first`, `acrcloud_only`, `shazam_only` (`recognition_setup.go`). |
| Interface | `internal/recognition.Recognizer`: `Name()`, `Recognize(ctx, wavPath)` (`types.go`). |
| AcoustID | Mentioned in older docs/scripts; **no in-tree AcoustID recognizer** in `internal/recognition/` yet. |
| Credentials | ACRCloud host/key/secret live in **`/etc/oceano/config.json`** (web UI). Shazam uses a Python venv path, not API keys in config. |
| **Continuity monitor** | **`runShazamContinuityMonitor`** (`main.go`) periodically captures short audio and runs **Shazam only**, independent of `RecognizerChain`. It detects **gapless** or **soft** track changes (weak VU boundaries), calibrates against the current result, and can **suppress** VU-driven boundaries when “continuity is ready”. Tuning lives under `ShazamContinuity*` and `Continuity*` in `config_types.go` / `oceano-web/config.go`. |

---

## Product goals

1. **Default identification path**: **AcoustID** (free tier, user or app-supplied **client API key** from acoustid.org; low sensitivity compared to paid APIs).
2. **Bundled optional providers**: **ACRCloud** and **Shazam** ship with the product but are **off until configured** (keys for ACRCloud; Shazam may remain env-based or gain explicit API usage per upstream constraints).
3. **User-defined order**: Replace fixed enums with an **ordered list** of enabled providers. **AcoustID is only the default for new installs**—after the user adds or configures another provider, they may **reorder** entries (e.g. ACRCloud first), **turn AcoustID off entirely**, or keep AcoustID but **not** as the first hop.
4. **Per-provider roles**: Each enabled provider can participate as **`primary`** (identification chain, order = list order among primaries), **`confirmer`** (secondary pass / cross-check after a primary candidate—aligned with existing `ConfirmationDelay` behaviour), and/or **`arbitration`** (extra recognition pass **only when primaries disagree** or `merge_policy` cannot pick a winner). The same provider may appear in more than one role where it makes sense (e.g. AcoustID as both primary and confirmer is allowed; the coordinator defines call order).
5. **Security**: For paid or sensitive keys, prefer **storage on the phone** so the **Pi backend never holds plaintext secrets**—with explicit tradeoffs (see below).
6. **Multi-provider outcomes**: When more than one provider is enabled, the user should control whether the system **merges** answers into a single “best” result, **requires agreement**, runs **arbitration** providers on mismatch, or **surfaces a choice** when providers disagree—not only “first non-empty wins” as in `ChainRecognizer` today.

---

## Multi-provider aggregation vs Shazam-only continuity

### Why Shazam continuity exists today

Physical playback often has **gapless or low-silence** transitions. The main recognizer is driven by **VU / source boundaries** and timers; those can **miss** a side-B→side-A style change or a CD index jump with little RMS dip. The **Shazam continuity** loop is a **parallel channel**: cheap periodic captures compare Shazam’s answer to the **currently displayed** track and, after confirmation rules, fire a **re-recognition** when they diverge. It is **hard-wired to Shazam** (`RecognitionPlan.Continuity` is always the Shazam instance in `recognition_setup.go`).

### How the product may change

A richer provider story (AcoustID default + optional ACRCloud/Shazam) makes **“always poll Shazam in the background”** less attractive:

| Factor | Implication |
|--------|-------------|
| **Cost / CPU** | Periodic Shazam (`shazamio` Python) is heavier than fingerprint + HTTP to AcoustID. |
| **Provider parity** | Users who disable Shazam should still get sensible behaviour; continuity must not be **Shazam-exclusive** in the long term. |
| **Redundancy** | If the main chain already runs **two providers** and a **merge policy** (see below), part of the value of continuity (catching false “same track” state) may overlap—**needs product/analysis** on real vinyl/CD sessions. |

**Direction:** Treat **continuous / periodic track-equality monitoring** as an **optional, explicitly configured** feature—not an implicit default tied to Shazam. When enabled, the **default engine** should prefer **AcoustID** (same fingerprint / same MusicBrainz recording id) for “is this still the same recording?” checks, with **Shazam** (or another provider) only if the user opts in for that probe.

### User-configurable strategies when multiple providers run

Move beyond a fixed `ChainRecognizer` “first match wins” for the **final** displayed metadata (implementation can still call providers in series or parallel internally):

| Mode | Behaviour | UX / headless notes |
|------|-----------|---------------------|
| **`first_success`** | Current mental model: first provider in order returns a match → use it (after optional minimum confidence). | Simple; no UI conflict. |
| **`best_score`** | Run enabled providers (sequential or parallel); pick the result with the **highest declared confidence** or provider-specific score, with tie-breakers (e.g. prefer MusicBrainz id stability). | Fully automatic. |
| **`require_agreement`** | Accept only if **N** providers agree on **normalised** artist+title (or on shared ISRC / MB recording id when available). | Reduces false positives; may yield **no** result until agreement—needs timeout fallback policy. |
| **`prefer_provider`** | User ranks providers for **truth** when scores tie or metadata conflicts. | Complements ordered list. |
| **`user_picks_on_conflict`** | If two top candidates disagree beyond a threshold, expose **both** in state (e.g. `track_candidates[]`) for **Now Playing / iOS** to show a picker; Pi applies the user’s choice until the next boundary. | Requires **state contract** and UI work (`docs/cross-repo-sync.md`); optional for v1. |
| **`arbitrate`** (or flag on `require_agreement`) | When two+ **primary** results conflict, run **`arbitration`**-role providers in order (often **AcoustID**—cheap fingerprint—or Shazam) to **break ties**: e.g. accept the primary whose metadata is **consistent with** the arbiter, or use arbiter as tie-break score. | Keeps headless operation without UI; must define deterministic tie-break rules in docs. |

**Recommendation:** Ship **`first_success`** + **`best_score`** + **`require_agreement`** + **`arbitrate`** (with per-provider **`arbitration`** role) as machine-local modes in config; treat **`user_picks_on_conflict`** as a later phase once unified state and mobile UI can carry candidate lists without breaking existing consumers.

### Continuity monitor: analysis checklist (keep vs simplify)

Before deleting or shrinking continuity, validate on hardware:

1. **Gapless album** on CD or ripped sequence: does VU-only + main chain miss track changes that continuity currently catches?
2. **Vinyl side change** with audible run-out groove: is a boundary always emitted?
3. **Shazam disabled**: is the experience acceptable if continuity is off or AcoustID-only?

**If continuity stays:** make **`continuity.enabled`**, **`continuity.provider`** (`acoustid` default, `shazam` optional), **`continuity.interval`**, and **`continuity.capture_duration`** first-class config; deprecate Shazam-prefixed flag names over time with migration mapping. **If continuity goes:** document that **gapless detection** relies on **stronger main-chain policies** (`require_agreement`, refresh timers, optional parallel double-shot) and tune VU / refresh defaults accordingly.

---

## Technical: AcoustID / Chromaprint (why past attempts may have failed)

Today the pipeline feeds recognizers a **stereo 44.1 kHz WAV**. Chromaprint’s reference tooling (`fpcalc`) and common Python stacks (`acoustid.fingerprint_file`) **normalize internally** (downmix, resample—often toward **mono ~11025 Hz**—and duration limits). If a **custom Go path** calls Chromaprint without that preprocessing, **match rates collapse**.

Recommendations for a **first-class AcoustID provider** in this repo:

| Topic | Guidance |
|-------|----------|
| **Implementation** | Prefer **calling `fpcalc`** (subprocess) or **official Chromaprint library** with the **same contract** as `fpcalc` (documented sample rate / channel layout). Avoid hand-rolled PCM unless it matches Chromaprint’s expectations. |
| **Input** | Either rely on **WAV → fpcalc** (simplest operationally) or resample/downmix in Go before native API—**must match Chromaprint’s expected format**. |
| **Duration** | Use **~10–20 s** of usable audio per attempt; align with existing `RecognizerCaptureDuration` defaults and coordinator triggers. |
| **Silence / LP needle** | If the first seconds are near-silence (needle drop, groove run-in), fingerprint quality is poor. Reuse or extend **VU/RMS-aware skip** (`skipDuration` in `captureFromPCMSocket`) so capture starts after **meaningful signal** (threshold configurable). |
| **Dependency** | Pi images must ship **`fpcalc`** or link **libchromaprint**; document in README / `.deb` dependencies. |
| **Lookup** | Fingerprint → **AcoustID API** → MusicBrainz recording IDs → metadata; **artwork** via **Cover Art Archive** or existing library artwork pipeline (no artwork from AcoustID alone). |
| **Rate limits** | Map HTTP 429 / AcoustID errors to existing **`ErrRateLimit`** and chain backoff behavior. |

---

## Architecture: flexible provider registry

### AcoustID as default, not mandatory

| Scenario | Example |
|----------|---------|
| **Out-of-box** | Single enabled provider: **AcoustID** first in list, `roles: ["primary"]`. |
| **Paid provider added** | User enables ACRCloud and **drags it above** AcoustID, or disables AcoustID and runs **ACR-only**. |
| **AcoustID as confirmer only** | Primaries: `[acrcloud]`; AcoustID has `roles: ["confirmer"]` (and optionally `["arbitration"]`) so free fingerprint validates commercial API output. |
| **Disagreement handling** | `merge_policy: "arbitrate"`; Shazam and/or AcoustID marked `arbitration`—invoked only when two primaries (e.g. ACR vs parallel Shazam) **do not match** under normalisation rules. |

Validation rules (implementation): **at least one** enabled provider must cover **`primary`** or recognition is disabled with a clear log + web UI warning. **`confirmer`** / **`arbitration`** without any **primary** is invalid.

### Config shape (conceptual)

Move from a single `recognizer_chain` enum to something like:

```json
"recognition": {
  "providers": [
    {
      "id": "acoustid",
      "enabled": true,
      "roles": ["primary", "arbitration"],
      "credential_ref": "config:acoustid"
    },
    {
      "id": "acrcloud",
      "enabled": true,
      "roles": ["primary", "confirmer"],
      "credential_ref": "ios:acrcloud"
    },
    { "id": "shazam", "enabled": false, "roles": ["primary"] }
  ],
  "merge_policy": "first_success",
  "continuity": {
    "enabled": false,
    "provider": "acoustid",
    "interval_secs": 12,
    "capture_duration_secs": 4
  },
  "acoustid_client_key": "…"
}
```

- **`providers`**: **ordered list** for **UI and for `primary` chain order**—only entries with the **`primary`** role participate in the main ordered chain; drag-and-drop in the web config rewrites this array.
- **`roles`**: subset of **`primary`**, **`confirmer`**, **`arbitration`** (see **AcoustID as default, not mandatory**). Empty `roles` treated as disabled for all passes.
- **`merge_policy`**: one of the strategies in **Multi-provider aggregation** (`first_success`, `best_score`, `require_agreement`, `arbitrate`, …).
- **`continuity`**: optional periodic “same track?” monitor; **`provider`** defaults to **`acoustid`** when enabled; mirrors today’s Shazam continuity knobs without hard-coding Shazam.
- **`credential_ref`**: indirection for where secrets live (`config` vs `ios` vs future `keychain` service).
- **Backward compatibility**: map legacy `acrcloud_first` / `shazam_only` / … to the new list for one or two releases; log deprecation. Map `shazam_continuity_interval_secs` > 0 and Shazam available → `continuity.enabled: true`, `continuity.provider: shazam` until users migrate.

### Code structure

- Add `internal/recognition/acoustid.go` (or split client vs fingerprint).
- Extend `Result` with stable IDs where useful (e.g. **MusicBrainz Recording ID**) for deduping and library correlation—keep JSON state fields backward compatible (`omitempty`).
- `buildRecognitionComponents` becomes **data-driven**: parse **`roles`** → build **primary** `ChainRecognizer` (order preserved) → attach **confirmer** and **arbitration** hooks per `RecognitionPlan` / coordinator (replacing hard-coded “second in chain = confirmer” and Shazam-only continuity assumptions where appropriate).

---

## Security: storing ACRCloud (and similar) keys “on the phone”

**Constraint:** The Pi runs **headless** recognition on **local capture**. Any provider that needs a **server-side API secret** will eventually need that secret **available to something that can call the API**. If the secret never leaves the phone, **the phone (or a proxy the user controls) must perform the HTTP call** or **mint short-lived tokens** the Pi can use.

### Option A — Secrets only in iOS Keychain (strongest alignment with your preference)

**Flow (high level):**

1. User enters ACRCloud key/secret **only in `oceano-player-ios`**; stored in **Keychain**.
2. Pi reaches a recognition event and produces a **fingerprint or compact audio descriptor** (preferred: **fingerprint + duration**, not raw PCM, to reduce bandwidth and privacy risk).
3. **Transport**: iOS app, when on the same LAN/VPN, opens an **outbound** connection to the Pi (or uses existing WebSocket) and subscribes to “recognition jobs”; Pi sends **job ID + fingerprint payload**; iOS calls ACRCloud with user credentials; iOS returns **normalized `Result` JSON** to the Pi.
4. Pi merges result into `oceano-state.json` as today.

**Pros:** Pi never stores commercial secrets; rotation is on the phone.  
**Cons:** Requires **iOS online** for that provider; higher engineering cost; latency and pairing UX; must define a **versioned binary/JSON contract** between repos (`docs/cross-repo-sync.md`).

### Option B — User-operated relay / token broker (advanced)

User deploys a tiny relay (or Oceano-hosted opt-in service) where the phone registers secrets; Pi uses **short-lived OAuth-style tokens**. Pi still never sees the root secret.

**Pros:** Pi can call APIs without the phone being awake for every track (depending on token TTL).  
**Cons:** Operational complexity; trust model for the relay.

### Option C — Encrypted-at-rest on Pi (pragmatic baseline)

Secrets in `config.json` with **filesystem permissions** (`root` / `oceano` user only), optional **LUKS** or OS-level full-disk encryption. Web UI **masks** secrets; only writes on change.

**Pros:** Works offline; matches today’s model; minimal moving parts.  
**Cons:** Not “phone-only”; anyone with root on the Pi can read keys.

### Recommendation

- Ship **Option C** as the **default supported path** for users who want simplicity.
- Document and implement **Option A** as the **privacy-first / split-trust** mode for users who pair with iOS, with explicit **“iOS must be reachable”** UX.
- Treat **AcoustID client key** as **low sensitivity** (can default to a project key with limits, or require user registration—product decision).

---

## Packaging and distribution

| Component | Notes |
|-----------|--------|
| **Debian package** | Add **`chromaprint-tools`** (or equivalent providing `fpcalc`) as a **dependency** when AcoustID is default. |
| **Optional providers** | ACRCloud: HTTP client only (already). Shazam: keep Python path optional; document any new API-key flow if upstream requires it. |
| **Wizard / web** | Per-provider **enable**, **role** checkboxes (primary / confirmer / arbitration), **drag-and-drop order** for primaries, AcoustID key field, connection test; mask ACRCloud secret fields; validate “at least one primary”. |

---

## Commercial product fit: BYOK, liability, and third-party terms

**Disclaimer:** This section is **product and compliance-oriented engineering context**, not legal advice. Ship text through counsel before positioning a paid hardware or software product in your jurisdiction.

### Bring-your-own-key (BYOK) — viability

A **very common** model for integrator products (media servers, automation hubs, dev tools) is:

- The **product** (Oceano Player: `.deb`, Pi image, optional iOS companion) **ships code that can call** third-party APIs when configured.
- The **end user** creates accounts with **AudD, ACRCloud, AcoustID, TheAudioDB**, etc., accepts those vendors’ **Terms of Service / Acceptable Use**, pays **their** invoices, and pastes **their own** API keys into config or the phone app.

**Encaixe como produto:** This aligns well with Oceano: you sell **hardware integration + software**, not “unlimited Shazam inside the box.” Commercial viability is **high** for BYOK-style optional recognition, provided UX and docs make the boundary obvious (**user is the API customer**).

### Contractual chain

| Party | Typical relationship |
|-------|-------------------------|
| **Oceano (vendor / project)** | License to the **Oceano software** (e.g. open-source + optional paid support / hardware bundle). Not automatically party to AudD/ACRCloud contracts. |
| **End user** | **Direct** contractual relationship with each recognition provider they enable; responsible for **quota, fees, and ToS compliance**. |
| **Recognition provider** | Supplies API under **their** developer terms; may restrict **resale of API access**, **scraping**, or **embedding keys in redistributed apps**. |

Avoid **bundling a shared production API key** in the binary or image for all customers unless you have a **written** redistribution / OEM agreement and abuse controls—otherwise quota theft and ToS violations become **your** operational problem.

### Trademarks and positioning

- Refer to third-party services **by name** only where needed (**nominative use**): “Optional integration with ACRCloud when you provide credentials.”
- Do **not** imply **endorsement**, **sponsorship**, or **partnership** unless contractually true.
- UI copy: “Powered by X” should match **actual** contractual branding requirements from each vendor.

### Privacy and data flows (product disclosure)

When a user enables a commercial recognizer, **audio or derived fingerprints** leave the Pi (or the phone relay) to the provider’s servers. For a **commercial Oceano product**, document clearly:

- **What** is sent (e.g. short WAV clip vs Chromaprint fingerprint only).
- **Where** it goes (vendor name, region if relevant).
- That the user controls enablement and keys (**BYOK**).

This supports **GDPR-style transparency**, consumer trust, and **App Store**-style privacy questionnaires for `oceano-player-ios` if it participates in recognition.

### Higher-risk integration paths (commercial lens)

| Path | Commercial note |
|------|------------------|
| **Official documented APIs** (AcoustID, ACRCloud, AudD, Houndify with license) | **Lower risk** if UX is BYOK and docs link to vendor ToS. |
| **Unofficial / reverse-engineered** paths (e.g. community **Shazam** clients) | **Higher risk** for a **sold product**: possible ToS / CFAA-style issues depending on jurisdiction; vendor may block clients; harder to explain to enterprise or retail buyers. Prefer **documented** providers for “default commercial story.” |

### What to ship in-repo / in-product

- **README / setup wizard**: “Recognition providers are optional; you need your own API account where applicable; see [vendor] terms.”
- **Per-provider** short blurb + link to **pricing** and **developer ToS**.
- **No implied warranty** that any provider will remain available or pricing-stable.

**Implemented copy (baseline):** `README.md` first-time setup (Third-party recognition BYOK subsection + suggested-order step), `cmd/oceano-setup` completion hint, and `cmd/oceano-web/static/recognition.html` intro under Providers—all English, consistent with this section.

### Summary

| Question | Practical answer |
|----------|------------------|
| Can Oceano be sold as a product while users pay APIs themselves? | **Yes**, BYOK is standard and **fits** Oceano’s architecture. |
| Who pays license/API fees? | **End user**, for each provider they enable (as you intended). |
| Main product risks to manage? | **Shared embedded keys** (avoid without contracts), **misleading branding**, **undisclosed data flows**, and **reliance on unofficial APIs** for retail/commercial positioning. |

---

## Community provider evaluation (shortlist for assessment)

This table lists **widely discussed** services in maker / self-host / media-server communities (e.g. around Plex, Kodi, MusicBrainz Picard, “Shazam alternative API” threads). It separates **acoustic identification** (upload or fingerprint audio → track) from **metadata enrichment** (you already know artist/title or a MusicBrainz id).

**Fit for “seamless” integration** here means: **HTTP(S) from Go**, **single API key** or **OAuth-free** flow, and a natural match to **`Recognizer.Recognize(ctx, wavPath)`** (multipart file POST or fingerprint + GET/POST) on Raspberry Pi OS without heavy proprietary SDKs.

### A. Acoustic identification (primary `Recognizer` candidates)

| Provider | Auth / transport | Fit | Community / notes |
|----------|------------------|-----|---------------------|
| **AcoustID** (Chromaprint → [acoustid.org](https://acoustid.org/) API) | Client API key; fingerprint computed **locally** (`fpcalc` / lib), then small HTTP lookup | **Excellent** | Default in this plan; open ecosystem; pairs with MusicBrainz; no raw-audio upload to a commercial vendor if desired. |
| **ACRCloud** | Host + access key + **HMAC-style signing** (already in project) | **Excellent** | Very common in commercial integrations; good metadata and **ISRC**; already implemented in `internal/recognition/acrcloud.go`. |
| **[AudD](https://docs.audd.io/)** | Single **`api_token`**; `POST` `multipart/form-data` with **`file`** (or `url`) to `https://api.audd.io/` | **Excellent** | Often cited as the most **documentation-friendly** “Shazam-class” REST API; optional `return=` for MusicBrainz / Spotify / Apple Music ids; standard tier ~12 s / size limits (enterprise endpoint for longer). Straightforward from Go `mime/multipart`. |
| **Shazam** (via **`shazamio`** / community libs) | No official public “upload this file” API for hobbyists; **unofficial** Python path | **Good (today)** | Already wired in-repo; **TOS / longevity** risk vs commercial APIs. |
| **SoundHound** via **[Houndify](https://www.houndify.com/)** | Developer account; **voice / audio** APIs and SDKs; music domain often **license / request** | **Moderate** | Strong technology; less “drop one WAV in curl” than AudD; better for products willing to use official SDKs or streaming voice requests. |
| **Gracenote** (Nielsen) | **GNSDK** / enterprise contracts | **Low** for seamless OSS Pi | Industry standard in cars / CE; heavy SDK and licensing—not a quick Debian + Go integration. |

### B. Metadata & artwork (not a substitute for acoustic ID)

Use these **after** a recording id or reliable **artist + title** (e.g. from AcoustID / ACRCloud / AudD), to improve **artwork, bios, or browse UX**—not as the first hop from raw PCM.

| Provider | Role | Fit | Notes |
|----------|------|-----|--------|
| **[TheAudioDB](https://www.theaudiodb.com/)** | JSON API: artist / album / track by **name** or **MusicBrainz id** | **Enrichment** | **Does not** identify audio from a mystery clip; very useful for **high-quality artwork** and extra fields once you have MBIDs or search keys. Simple HTTP GET; free tier + rate limits; v2 premium for production. |
| **[MusicBrainz](https://musicbrainz.org/doc/MusicBrainz_API)** | Recording / release **lookup** | **Enrichment** | Already in the AcoustID pipeline; respect **rate limits** and **User-Agent** policy. |
| **[Cover Art Archive](https://coverartarchive.org/)** | Artwork by **release MBID** | **Enrichment** | Standard companion to MusicBrainz. |

### C. Adjacent ideas (optional research)

| Idea | Fit | Notes |
|------|-----|--------|
| **Local fingerprint DB** (e.g. **dejavu**-style, custom corpus) | Niche | Useful for **private** collections; different ops model (index your own FLACs); not “identify any commercial track” out of the box. |
| **Streaming radio APIs** (e.g. AudD **stream** monitoring) | Low for Oceano | Targets **URL-based** continuous streams; Oceano’s input is **local capture**, not a radio URL. |

### Suggested evaluation order for engineering spikes

1. **AudD** — smallest integration surface next to existing ACRCloud (token + multipart WAV).  
2. **TheAudioDB** — spike as **post-recognition artwork** resolver from **MusicBrainz recording / release** id, not as a `Recognizer`.  
3. **Houndify / SoundHound** — only if enterprise licensing is acceptable; compare latency on Pi with official examples.  
4. **Gracenote** — defer unless a partner or volume deal appears.

---

## Phased implementation

| Phase | Scope |
|-------|--------|
| **P0** | AcoustID recognizer + `fpcalc`/lib dependency + MusicBrainz metadata mapping + chain integration; default order **AcoustID → (optional) ACRCloud → (optional) Shazam**; config migration from enums. |
| **P1** | Web UI: ordered providers, enable/disable, **roles** (primary / confirmer / arbitration), AcoustID key, masked secrets for Pi-stored ACRCloud. |
| **P1b** | **`merge_policy`** in config + coordinator logic (`first_success` / `best_score` / `require_agreement` / **`arbitrate`**); wire **confirmer** and **arbitration** passes from `roles`; document interaction with library persistence and confirmation delay. |
| **P2** | RMS-aware capture start / configurable skip to address silence and LP run-in. |
| **P2b** | **Continuity refactor**: `continuity.enabled` + `continuity.provider` (`acoustid` default); migrate Shazam-specific flags; **hardware validation** (gapless CD, vinyl) to decide default **on vs off** when Shazam is absent. |
| **P3** | **iOS-mediated recognition** (Option A): job channel, contract, pairing, and security review (TLS, replay, job size limits). |
| **P4** | Optional: **`user_picks_on_conflict`** + `track_candidates` in unified state; parallel recognition, per-provider timeouts, quotas (from existing chain-improvement doc). |

---

## Documentation and cross-repo

- Update **`README.md`**, **`CLAUDE.md` / `AGENTS.md`**, and **`docs/cross-repo-sync.md`** whenever:
  - `config.json` keys or semantics change;
  - unified state JSON gains fields;
  - a new **Pi ↔ iOS** recognition channel is added.
- Avoid silent renames of exported JSON fields used by iOS.

---

## Open questions

1. **Bundled AcoustID key**: shared project key vs mandatory user registration (abuse / quota)?
2. **Shazam**: remain **local Python** only, or align with any official API terms if keys are introduced?
3. **Delegated recognition (Option A)**: acceptable **latency** for vinyl (10–20 s capture + phone round-trip)?
4. **Offline**: should the Pi **fall back** to AcoustID-only when iOS is unreachable and ACRCloud is configured as `ios:` only?
5. **Continuity default after migration**: with AcoustID-only installs, is **`continuity.enabled: false`** acceptable out of the box, or do we enable **AcoustID continuity** at a conservative interval?
6. **AcoustID for continuity**: is comparing **recording IDs** from successive fingerprints stable enough on noisy vinyl captures, or does continuity still need **Shazam** (or ACRCloud) as an optional probe?

---

## References (in-repo)

- `cmd/oceano-state-manager/recognition_setup.go` — chain construction; `Continuity` recognizer wiring.
- `cmd/oceano-state-manager/main.go` — `runShazamContinuityMonitor`, `tryEnableShazamContinuity`.
- `cmd/oceano-state-manager/source_vu_monitor.go` — interaction when continuity is “ready” vs VU boundaries.
- `cmd/oceano-state-manager/recognizer.go` — WAV capture format and skip window.
- `internal/recognition/*` — recognizer interface and providers.
- `docs/plans/recognition-provider-chain-improvement.md` — roles, quotas, parallel recognition.
- `docs/plans/recognition-enhancement.md` — CPU / contention notes for fingerprint work on Pi.
