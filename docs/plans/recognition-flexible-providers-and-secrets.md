# Recognition: Flexible Providers and Secret Handling

## Purpose

This plan describes how to evolve Oceano Player’s recognition stack from the current **ACRCloud-first / optional `shazamio`** model (fixed chain enums in `recognition_setup.go`) toward a **flexible, ordered provider list** with optional **ACRCloud**, optional **Python `shazamio`** (see **Third-party clarity: shazamio** below—it is **not** an official Shazam API), and additional **snippet-friendly** REST providers (e.g. **AudD**), plus a **clear security model** for credentials—especially **not persisting commercial API secrets on the Pi** when the user prefers the **iOS companion app** as the trust anchor.

**AcoustID is not a product target:** the service and Chromaprint pipeline are oriented toward **full-track / library-scale** audio, while Oceano captures **short WAV segments** (typically ~7–20 s) from REC OUT. Requiring **whole-track** capture for reliable AcoustID matches would be **slow, heavy on storage/CPU, and a poor fit** for real-time vinyl/CD use. The POC and FAQ-aligned empty results on short clips confirmed this; see **Rationale: AcoustID deferred** below.

It complements `docs/plans/recognition-provider-chain-improvement.md` (roles, quotas, parallel mode) and should be read together with `docs/cross-repo-sync.md` for any API or config contract changes affecting `oceano-player-ios`.

---

## Current state (codebase snapshot)

| Area | Today |
|------|--------|
| Capture | `captureFromPCMSocket` writes **S16_LE stereo 44100 Hz** WAV (`cmd/oceano-state-manager/recognizer.go`). |
| Chain | `RecognizerChain` string enum: `acrcloud_first`, `shazam_first`, `acrcloud_only`, `shazam_only` (`recognition_setup.go`). |
| Interface | `internal/recognition.Recognizer`: `Name()`, `Recognize(ctx, wavPath)` (`types.go`). |
| AcoustID | **Not implemented** as a `Recognizer`. `acoustid_client_key` may exist in config for historical/additive reasons but **does not enable** any in-process provider. |
| Credentials | ACRCloud host/key/secret live in **`/etc/oceano/config.json`** (today also editable via bundled web UI; **product converges to iOS-only** for user-facing config). The optional **shazamio** path uses a **Python venv + `shazamio`** (no Shazam API key in config). |
| **Continuity monitor** | **`runShazamContinuityMonitor`** (`main.go`) periodically captures short audio and runs the **`shazamio`** subprocess path only, independent of `RecognizerChain`. It detects **gapless** or **soft** track changes (weak VU boundaries), calibrates against the current result, and can **suppress** VU-driven boundaries when “continuity is ready”. Tuning lives under `ShazamContinuity*` and `Continuity*` in `config_types.go` / `oceano-web/config.go`. |

---

## Product goals

1. **Default identification path**: **ACRCloud** when configured (today’s primary **documented** API path), with **AudD** (or similar) as optional **snippet-friendly** REST providers, and **`shazamio`** only as an **optional, unofficial** integration (see below).
2. **Bundled optional providers**: **ACRCloud** is **off until configured** (BYOK keys). **`shazamio`** is **optional** via `install-shazam.sh` + venv path—**not** the same as a first-party **Shazam** developer API. **AudD** (or peers) as optional BYOK additions with public docs.
3. **User-defined order**: Replace fixed enums with an **ordered list** of enabled providers. New installs may default to **ACRCloud-first** when credentials exist, otherwise explicit user choice in **iOS**.
4. **Per-provider roles**: Each enabled provider can participate as **`primary`**, **`confirmer`**, and/or **`arbitration`** (same semantics as before; coordinator defines call order).
5. **Security**: For paid or sensitive keys, prefer **storage on the phone** so the **Pi backend never holds plaintext secrets**—with explicit tradeoffs (see below).
6. **Multi-provider outcomes**: User-controlled **merge** behaviour (`first_success`, `best_score`, `require_agreement`, `arbitrate`, optional `user_picks_on_conflict` later)—not only “first non-empty wins” as in `ChainRecognizer` today.

---

## Rationale: AcoustID deferred (not viable for Oceano’s capture model)

| Constraint | Implication |
|------------|-------------|
| **AcoustID / Chromaprint** | Documented as targeting **full files**, not short snippets ([AcoustID FAQ](https://acoustid.org/faq); [chromaprint#146](https://github.com/acoustid/chromaprint/issues/146)). |
| **Oceano capture** | Short **WAV** windows (~7–20 s) for responsiveness and disk; extending to **full track** means **longer RAM/disk use**, worse UX on track boundaries, and no guarantee of match on **analog** REC OUT vs digital fingerprints in the DB. |
| **Product decision** | **Do not** pursue AcoustID as a first-class provider. **MusicBrainz / Cover Art Archive** remain valid as **enrichment** after another provider returns a recording id or strong artist/title (e.g. from ACRCloud or AudD). |

Historical reference: `scripts/poc_acoustid.py` and plan text about `fpcalc` remain for **experimentation only**, not the shipping architecture.

---

## Third-party clarity: `shazamio` (not an official Shazam API)

Oceano’s optional “Shazam-class” recognition uses the **Python package [`shazamio`](https://pypi.org/project/shazamio/)** (and similar community stacks), invoked from **`oceano-state-manager`** via a configured interpreter/venv. That is **not** integration with a **documented, contractual Shazam / Apple developer API** (no API key flow endorsed by Shazam for this use case in-tree).

| Topic | Implication |
|--------|-------------|
| **Naming** | Docs and UX should say **`shazamio`** (or “optional community Shazam client”) where precision matters; avoid implying **“official Shazam API”** or unlimited entitlement to Shazam’s service. |
| **Terms of service** | End users may still be bound by **Shazam / Apple** terms for the **underlying service** if `shazamio` talks to Shazam backends; **you are not** the API reseller, but **commercial products** should disclose that this path is **unofficial** and may **break or be blocked** without notice. |
| **Commercial / retail positioning** | For **sold** hardware or App Store–distributed apps, relying on **`shazamio`** carries **higher legal, compliance, and continuity risk** than **documented** providers (ACRCloud, AudD, etc.). Prefer those for the **default commercial story**; treat **`shazamio`** as **power-user / optional** and document the tradeoff in README and privacy/disclosure copy. |
| **Engineering** | Continuity and chain code may still refer internally to **Shazam IDs** (provider-specific track identifiers returned by the library)—that is **metadata naming**, not a claim of official API partnership. |

**Recommendation:** Keep **`shazamio`** install **optional**; in product copy, separate **“ACRCloud / AudD (BYOK, documented APIs)”** from **“optional shazamio (community client; use at your own risk for commercial deployments).”**

---

## Multi-provider aggregation vs Shazam-only continuity

### Why Shazam continuity exists today

Physical playback often has **gapless or low-silence** transitions. The main recognizer is driven by **VU / source boundaries** and timers; those can **miss** a side-B→side-A style change or a CD index jump with little RMS dip. The **continuity** loop is a **parallel channel**: periodic captures run **`shazamio`** and compare its answer to the **currently displayed** track and, after confirmation rules, fire a **re-recognition** when they diverge. It is **hard-wired to the `shazamio` path** (`RecognitionPlan.Continuity` is always that instance in `recognition_setup.go`).

### How the product may change

A richer provider story (optional ACRCloud / AudD / **`shazamio`**) makes **“always poll `shazamio` in the background”** less attractive for some users:

| Factor | Implication |
|--------|-------------|
| **Cost / CPU** | Periodic Shazam (`shazamio` Python) is heavier than a single REST upload per boundary. |
| **Provider parity** | Users who omit **`shazamio`** should still get sensible behaviour; continuity must not be **`shazamio`-exclusive** in the long term. |
| **Redundancy** | If the main chain already runs **two providers** and a **merge policy**, part of the value of continuity may overlap—**needs product/analysis** on real vinyl/CD sessions. |

**Direction:** Treat **continuous / periodic track-equality monitoring** as an **optional, explicitly configured** feature—not an implicit default tied to **`shazamio`**. When enabled, **`continuity.provider`** should default to **`shazam`** (meaning the existing **`shazamio`** subprocess) for backward compatibility, with future options such as **same as primary** or **acrcloud** where APIs support comparable “still the same track?” probes (product-specific).

### User-configurable strategies when multiple providers run

Move beyond a fixed `ChainRecognizer` “first match wins” for the **final** displayed metadata (implementation can still call providers in series or parallel internally):

| Mode | Behaviour | UX / headless notes |
|------|-----------|---------------------|
| **`first_success`** | Current mental model: first provider in order returns a match → use it (after optional minimum confidence). | Simple; no UI conflict. |
| **`best_score`** | Run enabled providers (sequential or parallel); pick the result with the **highest declared confidence** or provider-specific score, with tie-breakers (e.g. prefer stable ids when available). | Fully automatic. |
| **`require_agreement`** | Accept only if **N** providers agree on **normalised** artist+title (or on shared ISRC / MB recording id when available). | Reduces false positives; may yield **no** result until agreement—needs timeout fallback policy. |
| **`prefer_provider`** | User ranks providers for **truth** when scores tie or metadata conflicts. | Complements ordered list. |
| **`user_picks_on_conflict`** | If two top candidates disagree beyond a threshold, expose **both** in state (e.g. `track_candidates[]`) for **Now Playing / iOS** to show a picker; Pi applies the user’s choice until the next boundary. | Requires **state contract** and UI work (`docs/cross-repo-sync.md`); optional for v1. |
| **`arbitrate`** (or flag on `require_agreement`) | When two+ **primary** results conflict, run **`arbitration`**-role providers in order (e.g. **`shazamio`** or a second **documented** API) to **break ties**. | Keeps headless operation without UI; must define deterministic tie-break rules in docs. |

**Recommendation:** Ship **`first_success`** + **`best_score`** + **`require_agreement`** + **`arbitrate`** (with per-provider **`arbitration`** role) as machine-local modes in config; treat **`user_picks_on_conflict`** as a later phase once unified state and mobile UI can carry candidate lists without breaking existing consumers.

### Continuity monitor: analysis checklist (keep vs simplify)

Before deleting or shrinking continuity, validate on hardware:

1. **Gapless album** on CD or ripped sequence: does VU-only + main chain miss track changes that continuity currently catches?
2. **Vinyl side change** with audible run-out groove: is a boundary always emitted?
3. **`shazamio` not installed**: is the experience acceptable if continuity is off?

**If continuity stays:** make **`continuity.enabled`**, **`continuity.provider`** (default **`shazam`** for migration), **`continuity.interval`**, and **`continuity.capture_duration`** first-class config; deprecate Shazam-prefixed flag names over time with migration mapping. **If continuity goes:** document that **gapless detection** relies on **stronger main-chain policies** (`require_agreement`, refresh timers, optional parallel double-shot) and tune VU / refresh defaults accordingly.

---

## Architecture: flexible provider registry

### Default scenarios (no AcoustID)

| Scenario | Example |
|----------|---------|
| **Out-of-box** | User configures **ACRCloud** in **iOS**; single primary **`acrcloud`**. |
| **Dual commercial** | **`audd`** then **`acrcloud`** (or reverse), with `merge_policy` and optional **`arbitration`** role on one. |
| **`shazamio` in the mix** | Primaries **`acrcloud`**; provider id **`shazam`** (implementation = **`shazamio`**) as **`confirmer`** or continuity-only. |
| **Disagreement handling** | `merge_policy: "arbitrate"`; arbitration provider invoked only when primaries disagree under normalisation rules. |

Validation rules (implementation): **at least one** enabled provider must cover **`primary`** or recognition is disabled with a clear **log** and a **machine-readable hint** in state or config validation for **iOS** to surface. **`confirmer`** / **`arbitration`** without any **primary** is invalid.

### Config shape (conceptual)

Move from a single `recognizer_chain` enum to something like:

```json
"recognition": {
  "providers": [
    {
      "id": "acrcloud",
      "enabled": true,
      "roles": ["primary", "confirmer"],
      "credential_ref": "ios:acrcloud"
    },
    {
      "id": "audd",
      "enabled": true,
      "roles": ["primary"],
      "credential_ref": "config:audd"
    },
    { "id": "shazam", "enabled": false, "roles": ["primary"] }
  ],
  "merge_policy": "first_success",
  "continuity": {
    "enabled": false,
    "provider": "shazam",
    "interval_secs": 12,
    "capture_duration_secs": 4
  }
}
```

- **`providers`**: **ordered list** for **`primary` chain order** and for the **iOS** settings UX—only entries with the **`primary`** role participate in the main ordered chain; the app persists this array (reorder / enable / disable).
- **`roles`**: subset of **`primary`**, **`confirmer`**, **`arbitration`**. Empty `roles` treated as disabled for all passes.
- **`merge_policy`**: one of the strategies in **Multi-provider aggregation**.
- **`continuity`**: optional periodic “same track?” monitor; **`provider`** defaults to **`shazam`** when migrating from legacy Shazam continuity flags.
- **`credential_ref`**: indirection for where secrets live (`config` vs `ios` vs future `keychain` service).
- **Backward compatibility**: map legacy `acrcloud_first` / `shazam_only` / … to the new list for one or two releases; log deprecation. Map `shazam_continuity_interval_secs` > 0 and Shazam available → `continuity.enabled: true`, `continuity.provider: shazam` until users migrate.

### Code structure

- Add optional providers under `internal/recognition/` (e.g. **`audd.go`**) matching `Recognize(ctx, wavPath)`.
- Extend `Result` with stable IDs where useful (e.g. **MusicBrainz Recording ID**, **ISRC**) for deduping and library correlation—keep JSON state fields backward compatible (`omitempty`).
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

Secrets in `config.json` with **filesystem permissions** (`root` / `oceano` user only), optional **LUKS** or OS-level full-disk encryption. **iOS** (or a one-off `curl` to `POST /api/config` during development) **writes** keys; fields should be **masked** in any on-device preview.

**Pros:** Works offline; matches today’s model; minimal moving parts.  
**Cons:** Not “phone-only”; anyone with root on the Pi can read keys.

### Recommendation

- Ship **Option C** as the **default supported path** for users who want simplicity (**iOS** is the primary editor; legacy `oceano-web` pages may remain for Pi-local debugging only).
- Document and implement **Option A** as the **privacy-first / split-trust** mode for users who pair with iOS, with explicit **“iOS must be reachable”** UX.

---

## Packaging and distribution

| Component | Notes |
|-----------|--------|
| **Debian package** | No **chromaprint-tools** requirement for recognition (AcoustID not shipped). |
| **Optional providers** | ACRCloud: HTTP client only (already). **`shazamio`**: keep Python venv path optional; disclose unofficial nature in product copy. AudD: token + multipart WAV when implemented. |
| **iOS (`oceano-player-ios`)** | Primary UX: per-provider **enable**, **roles** (primary / confirmer / arbitration), **reorder** primaries, ACRCloud / AudD / **`shazamio`** (venv) fields; **mask** secrets; optional **connection test**; validate “at least one primary” before save. |

---

## Execution order: backend first, then iOS (incremental testing)

**Principle:** Land **additive, testable** backend slices first. **iOS** follows once the **config contract** and (where needed) **HTTP APIs** are stable. Avoid breaking `oceano-player-ios` consumers of `GET/POST /api/config` or state JSON.

### How to interleave work

| Step | Layer | What ships | How you test before iOS |
|------|--------|------------|-------------------------|
| **1** | Backend | **Parse `recognition.providers[]`** with **fallback**: if absent, derive the same runtime plan from legacy `recognizer_chain` + existing ACR/Shazam fields (**no behavior change** for old JSON). | Edit `/etc/oceano/config.json` by hand (or `curl POST /api/config`) with **only** legacy keys → logs + recognition identical to today. |
| **2** | Backend | Accept **optional** `providers` array in JSON; `oceano-web` `managerArgs` / reload unchanged until step 3. | Hand-craft minimal `providers` mirroring `acrcloud_first` → same behavior. |
| **3** | Backend | **`oceano-web`** writes `providers` on save (optional **dev bridge**) **or** skip and use manual JSON until iOS ready. | Save from web once → systemd args + recognition still work. |
| **4** | Backend | **`merge_policy`** + coordinator (start with `first_success` only = current semantics). | Toggle policy in JSON; verify logs and `oceano-state.json`. |
| **5** | Backend | Continuity refactor flags (`continuity.enabled`, `continuity.provider`) behind defaults matching today. | Flip in JSON; gapless CD session on hardware. |
| **6** | iOS | Settings screens: edit `recognition` (providers, roles, keys), call existing **`POST /api/config`**. | Device-only UX; Pi already understands payload. |
| **7** | iOS + Backend | **Option A** delegated recognition (new channel) only after steps 1–6 are stable. | Contract in `docs/cross-repo-sync.md`; staged feature flag. |

**Rule of thumb:** after each backend step, **`go test ./...`** (or affected packages) + **one Pi smoke test** (physical play + journalctl). iOS work **blocks** on no **breaking** `config.json` shape—only **additive** keys until a deliberate major version.

---

## Commercial product fit: BYOK, liability, and third-party terms

**Disclaimer:** This section is **product and compliance-oriented engineering context**, not legal advice. Ship text through counsel before positioning a paid hardware or software product in your jurisdiction.

### Bring-your-own-key (BYOK) — viability

A **very common** model for integrator products (media servers, automation hubs, dev tools) is:

- The **product** (Oceano Player: `.deb`, Pi image, optional iOS companion) **ships code that can call** third-party APIs when configured.
- The **end user** creates accounts with **AudD, ACRCloud, TheAudioDB**, etc., accepts those vendors’ **Terms of Service / Acceptable Use**, pays **their** invoices, and pastes **their own** API keys into config or the phone app.

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

- **What** is sent (e.g. short WAV clip vs fingerprint only).
- **Where** it goes (vendor name, region if relevant).
- That the user controls enablement and keys (**BYOK**).

This supports **GDPR-style transparency**, consumer trust, and **App Store**-style privacy questionnaires for `oceano-player-ios` if it participates in recognition.

### Higher-risk integration paths (commercial lens)

| Path | Commercial note |
|------|------------------|
| **Official documented APIs** (ACRCloud, AudD, Houndify with license) | **Lower risk** if UX is BYOK and docs link to vendor ToS. |
| **Unofficial / reverse-engineered** paths (e.g. **`shazamio`** and other community clients that target Shazam backends) | **Higher risk** for a **sold product**: possible ToS / CFAA-style issues depending on jurisdiction; vendor may block clients; harder to explain to enterprise or retail buyers. Prefer **documented** APIs (ACRCloud, AudD, …) for “default commercial story”; label **`shazamio`** explicitly as **optional / unofficial** in UX and legal-adjacent copy. |

### What to ship in-repo / in-product

- **README / `oceano-setup`**: “Recognition providers are optional; you need your own API account where applicable; see [vendor] terms.”
- **iOS app**: per-provider BYOK copy, links to vendor **pricing** and **developer ToS**, masked secret fields, validation when saving `recognition` to the Pi.
- **No implied warranty** that any provider will remain available or pricing-stable.

**Interim / dev:** `oceano-web` recognition pages may still exist for Pi-local testing until iOS parity; treat them as **non-authoritative** relative to this plan.

### Summary

| Question | Practical answer |
|----------|------------------|
| Can Oceano be sold as a product while users pay APIs themselves? | **Yes**, BYOK is standard and **fits** Oceano’s architecture. |
| Who pays license/API fees? | **End user**, for each provider they enable (as you intended). |
| Main product risks to manage? | **Shared embedded keys** (avoid without contracts), **misleading branding**, **undisclosed data flows**, and **reliance on unofficial APIs** for retail/commercial positioning. |

---

## Community provider evaluation (shortlist for assessment)

This table lists **widely discussed** services in maker / self-host / media-server communities. It separates **acoustic identification** (upload or fingerprint audio → track) from **metadata enrichment** (you already know artist/title or a MusicBrainz id).

**Fit for “seamless” integration** here means: **HTTP(S) from Go**, **single API key** or **OAuth-free** flow, and a natural match to **`Recognizer.Recognize(ctx, wavPath)`** (multipart file POST or fingerprint + GET/POST) on Raspberry Pi OS without heavy proprietary SDKs.

### A. Acoustic identification (primary `Recognizer` candidates)

| Provider | Auth / transport | Fit | Community / notes |
|----------|------------------|-----|---------------------|
| **AcoustID** (Chromaprint → acoustid.org) | Client API key; local fingerprint + HTTP lookup | **Not pursued** | Wrong fit: **full-file** orientation vs Oceano’s **short captures**; see **Rationale: AcoustID deferred**. |
| **ACRCloud** | Host + access key + **HMAC-style signing** (already in project) | **Excellent** | Very common in commercial integrations; good metadata and **ISRC**; already implemented in `internal/recognition/acrcloud.go`. |
| **[AudD](https://docs.audd.io/)** | Single **`api_token`**; `POST` `multipart/form-data` with **`file`** (or `url`) | **Excellent** | **Snippet-friendly** REST; good next provider after ACRCloud. |
| **`shazamio`** (Python; **not** a first-party Shazam API) | Venv + library; no in-tree Shazam API key | **Good (hobbyist / optional)** | Already wired in-repo; **commercial risk**: unofficial client to Shazam-like backends; may **break** anytime; **not** the same tier as ACRCloud/AudD for retail positioning. |
| **SoundHound** via **[Houndify](https://www.houndify.com/)** | Developer account; **voice / audio** APIs and SDKs | **Moderate** | Strong technology; less “drop one WAV in curl” than AudD. |
| **Gracenote** (Nielsen) | **GNSDK** / enterprise contracts | **Low** for seamless OSS Pi | Heavy SDK and licensing. |

### B. Metadata & artwork (not a substitute for acoustic ID)

Use these **after** a recording id or reliable **artist + title** (e.g. from ACRCloud / AudD), to improve **artwork, bios, or browse UX**—not as the first hop from raw PCM.

| Provider | Role | Fit | Notes |
|----------|------|-----|-------|
| **[TheAudioDB](https://www.theaudiodb.com/)** | JSON API: artist / album / track by **name** or **MusicBrainz id** | **Enrichment** | **Does not** identify audio from a mystery clip; useful for **artwork** once you have MBIDs or search keys. |
| **[MusicBrainz](https://musicbrainz.org/doc/MusicBrainz_API)** | Recording / release **lookup** | **Enrichment** | Respect **rate limits** and **User-Agent** policy. |
| **[Cover Art Archive](https://coverartarchive.org/)** | Artwork by **release MBID** | **Enrichment** | Standard companion to MusicBrainz. |

### C. Adjacent ideas (optional research)

| Idea | Fit | Notes |
|------|-----|-------|
| **Local fingerprint DB** (e.g. **dejavu**-style, custom corpus) | Niche | Useful for **private** collections; different ops model. |
| **Streaming radio APIs** (e.g. AudD **stream** monitoring) | Low for Oceano | Targets **URL-based** streams; Oceano’s input is **local capture**. |

### Suggested evaluation order for engineering spikes

1. **AudD** — smallest integration surface next to existing ACRCloud (token + multipart WAV).  
2. **TheAudioDB** — spike as **post-recognition artwork** resolver from **MusicBrainz recording / release** id, not as a `Recognizer`.  
3. **Houndify / SoundHound** — only if enterprise licensing is acceptable.  
4. **Gracenote** — defer unless a partner or volume deal appears.

---

## Phased implementation (summary)

### Backend (`oceano-player`) — do first

| Phase | Scope |
|-------|--------|
| **B0** | **Config model**: `recognition.providers[]` + `merge_policy` (default `first_success`) + migration from `recognizer_chain`; **runtime parity** with current enum-based chain when `providers` omitted. |
| **B1** | **`buildRecognitionComponents`** data-driven from `providers` + **roles**; confirmer / arbitration wiring; logs + validation hints for invalid configs. |
| **B1b** | Extend **`merge_policy`** (`best_score`, `require_agreement`, `arbitrate`) without changing default behavior until explicitly set. |
| **B2** | **AudD** — **shipped** in `internal/recognition/audd.go`; config `audd_api_token` + chain modes `audd_first` / `audd_only` + insertion into `acrcloud_first` / `shazam_first` when token set. Further REST providers reuse the same pattern. |
| **B3** | **Continuity refactor**: `continuity.enabled`, `continuity.provider`; migrate Shazam-prefixed keys; hardware validation. |
| **B4** | **RMS-aware** capture skip / LP run-in tuning. |
| **B5** | **Option A** Pi endpoint(s) or channel for **iOS-mediated** recognition jobs; security review. |

### iOS (`oceano-player-ios`) — after contract is stable

| Phase | Scope |
|-------|--------|
| **I1** | **Recognition settings**: edit `recognition` in `POST /api/config` — provider list, order, roles, ACRCloud / AudD / **`shazamio`** path; masking; validation; copy that **`shazamio`** is unofficial if exposed to end users. |
| **I2** | **Option A** client: subscribe to Pi job channel, run provider calls with Keychain secrets, return results. |
| **I3** | Optional: **`user_picks_on_conflict`** UI when state exposes `track_candidates[]`. |

### Deferred / parallel research

| Phase | Scope |
|-------|--------|
| **P*** | Parallel recognition, per-provider timeouts, quotas — see `recognition-provider-chain-improvement.md`. |

---

## Documentation and cross-repo

- Update **`README.md`**, **`CLAUDE.md` / `AGENTS.md`**, and **`docs/cross-repo-sync.md`** whenever:
  - `config.json` keys or semantics change;
  - unified state JSON gains fields;
  - a new **Pi ↔ iOS** recognition channel is added.
- Avoid silent renames of exported JSON fields used by iOS.

---

## Open questions

1. **`shazamio`**: keep **optional** forever vs **deprecate** for commercial SKUs in favour of AudD-only; any future **official** Shazam API would be a **separate** integration with its own ToS.
2. **Delegated recognition (Option A)**: acceptable **latency** for vinyl (10–20 s capture + phone round-trip)?
3. **Offline**: should the Pi **fall back** to a Pi-stored provider when iOS is unreachable and ACRCloud is configured as `ios:` only?
4. **Continuity default after migration**: is **`continuity.enabled: false`** acceptable out of the box when **`shazamio`** is not installed, or do we require an explicit opt-in?

---

## References (in-repo)

- `scripts/poc_acoustid.py` + `scripts/requirements-acoustid-poc.txt` — **historical POC** only (AcoustID not a shipping path).
- `cmd/oceano-state-manager/recognition_setup.go` — chain construction; `Continuity` recognizer wiring.
- `cmd/oceano-state-manager/main.go` — `runShazamContinuityMonitor`, `tryEnableShazamContinuity`.
- `cmd/oceano-state-manager/source_vu_monitor.go` — interaction when continuity is “ready” vs VU boundaries.
- `cmd/oceano-state-manager/recognizer.go` — WAV capture format and skip window.
- `internal/recognition/*` — recognizer interface and providers.
- `docs/plans/recognition-provider-chain-improvement.md` — roles, quotas, parallel recognition.
- `docs/plans/recognition-enhancement.md` — CPU / contention notes for fingerprint work on Pi.
