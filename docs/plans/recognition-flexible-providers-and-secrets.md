# Recognition: Flexible Providers and Secret Handling

## Purpose

This plan describes how to evolve Oceano Player’s recognition stack from the current **ACRCloud-first / optional `shazamio`** model (fixed chain enums in `recognition_setup.go`) toward a **flexible, ordered provider list** with optional **ACRCloud**, optional **Python `shazamio`** (see **Third-party clarity: shazamio** below—it is **not** an official Shazam API), and additional **snippet-friendly** REST providers (e.g. **AudD**), plus a **clear security model** for credentials—especially **not persisting commercial API secrets on the Pi** when the user prefers the **iOS companion app** as the trust anchor.

**AcoustID is not a product target:** the service and Chromaprint pipeline are oriented toward **full-track / library-scale** audio, while Oceano captures **short WAV segments** (typically ~7–20 s) from REC OUT. Requiring **whole-track** capture for reliable AcoustID matches would be **slow, heavy on storage/CPU, and a poor fit** for real-time vinyl/CD use. The POC and FAQ-aligned empty results on short clips confirmed this; see **Rationale: AcoustID deferred** below.

It complements `docs/plans/recognition-provider-chain-improvement.md` (roles, quotas, parallel mode), `docs/plans/recognition-shazamio-deferral-continuity-and-extensibility.md` (defer Shazamio for product scale, continuity-by-provider roadmap, safe custom-provider patterns), `docs/metrics-snapshots/README.md` (optional `GET /api/recognition/stats` snapshots and SQL to reset Shazamio-only `recognition_summary` rows), and should be read together with `docs/cross-repo-sync.md` for any API or config contract changes affecting `oceano-player-ios`.

---

## Client apps vs oceano-web (product direction)

**`oceano-web` is transitional.** The target experience is **native apps**: **`oceano-player-ios` first**, then **Android**. User-facing configuration, discovery, and recognition settings should converge on the **HTTP/SSE contract** (`GET/POST /api/config`, `/api/stream`, and related routes) that **mobile clients** implement—not on an ever-growing bespoke web UI. Treat **`oceano-web`** as **bootstrap**, **LAN admin**, and **ops** (service restarts, device pickers during setup) until native parity exists; **avoid new web-only product features** that would duplicate or fight iOS/Android.

---

## Current state (codebase snapshot)

| Area | Today |
|------|--------|
| Capture | `captureFromPCMSocket` writes **S16_LE stereo 44100 Hz** WAV (`cmd/oceano-state-manager/recognizer.go`). |
| Chain | `recognizer_chain` in JSON is **deprecated** for runtime ordering; **`recognition.providers[]`** drives the plan (`recognition_setup.go`). |
| Interface | `internal/recognition.Recognizer`: `Name()`, `Recognize(ctx, wavPath)` (`types.go`). |
| AcoustID | **Not implemented**; `acoustid_client_key` was **removed** from the config schema and services (historical POC only under `scripts/poc_acoustid.py`). |
| Credentials | ACRCloud host/key/secret live in **`/etc/oceano/config.json`** (edited via **`oceano-player-ios`**, `POST /api/config`, or **`sudo oceano-setup`**). The optional **shazamio** path uses a **Python venv + `shazamio`** (no Shazam API key in config). |
| **Continuity monitor** | **`runShazamioContinuityMonitor`** (`main.go`) periodically captures short audio and runs the **`shazamio`** subprocess at the **fixed** venv path when **`shazam`** is enabled in `recognition.providers` (and `shazam_recognizer_enabled` is not `false`). It detects **gapless** or **soft** track changes (weak VU boundaries), calibrates against the current result, and can **suppress** VU-driven boundaries when “continuity is ready”. Tuning lives under `ShazamioContinuity*` and `Continuity*` in `config_types.go` / `oceano-web/config.go`. When dedicated provider **roles** (e.g. continuity-only) ship, this rule may narrow. |

---

## Quiet program starts, live fades, and capture gain (planning only)

**Field report:** Some releases—especially **live** mixes and long **fade-ins** (e.g. low-level openings before the band enters)—stay **very quiet on REC OUT** for many seconds. Operators may need to **raise USB capture gain** (`amixer`, see `README.md` troubleshooting) so that (1) the **source detector** and **VU** paths see enough RMS above the silence threshold, and (2) the **first recognition capture** contains enough **spectral energy** for ACRCloud / AudD / `shazamio` within the configured window (typically ~7 s).

**This is not a provider-ordering problem.** It is a **signal-chain / trigger / capture-length** problem shared by `oceano-source-detector` (RMS, PCM relay) and `oceano-state-manager` (when to capture, how long, optional skip semantics—see **B4** below).

### Constraints (why “auto gain everywhere” is not obvious)

- **Fingerprinting and noise:** Aggressive **AGC**, heavy **digital gain**, or **wideband compression** on the audio sent to cloud recognizers can **raise the noise floor**, alter dynamics, and **hurt match quality**; sudden **crescendos** after a quiet intro can **clip** if gain was boosted for the intro.
- **Split concerns:** **Source / VU** classification uses the **same** PCM tap as recognition. Boosting **only** the WAV written for providers does **not** help if **Physical** vs **None** or **VU boundaries** still see “silence” for too long—unless product explicitly separates **display gating** from **recognition branch** processing (larger architecture change).
- **Hardware variance:** USB capture cards expose **different mixer controls**; any **ALSA-level automation** is **card-specific**, needs **caps**, **hysteresis**, and an **opt-in** policy so installs without that card do not regress.

### Improvement directions (ranked by increasing product + engineering risk)

1. **Operator + docs (today, strongest ROI):** Treat **quiet sides and live fades** like **RMS calibration**: set capture gain so that **typical** quiet-but-musical passages land in the documented **0.05–0.25 RMS** band; accept that **extreme** pressings may need **manual** gain or **Advanced** tuning (`silence_threshold`, debounce, capture duration). Cross-link `README.md` and **Listening Metrics** / **R3** nudges where helpful.
2. **Trigger and capture policy (software, medium scope):** Optional **longer first capture** after a **Physical** edge or after **no_match** when **post-edge RMS** stays below a configurable percentile for *N* seconds; optional **shorter or zero skip** for “soft start” cohorts (related to **B4 — RMS-aware capture skip / LP run-in tuning**). Ships behind **flags**; validate on **live** and **classical** LPs so **false triggers** do not increase.
3. **Recognition-only digital gain (software, medium–high risk):** Apply a **bounded linear gain** (or gentle **normalize-to-target-RMS**) **only** to the **WAV** sent to providers, with a **hard ceiling** and **only when** measured pre-capture RMS is below a threshold. **Does not** change ALSA; may still interact badly with **noisy** phono stages—**telemetry first** (first-window RMS vs match / no_match).
4. **Dynamic ALSA capture gain (hardware integration, highest risk):** A privileged helper or narrow **`oceano-state-manager`** hook that adjusts **known-safe** mixer controls when **sustained** low RMS is observed during **Physical**, with **slow slew**, **max delta**, **cooldown**, and explicit **opt-in** in config; persist with **`alsactl store`** only if product commits to that model. **Defer** until (2)+(3) are ruled insufficient and **card matrix** is defined.

### Recommendation

- **Near term:** Improve **documentation** and **Advanced** copy for “quiet intros / live albums”; keep **manual gain** as the **primary** fix for extreme material.
- **Product path:** Pursue **(2)** under **B4** with telemetry (`recognition-enhancement.md` Listening Metrics contract) before **(3)** or **(4)**.
- **Explicit non-goal for now:** No commitment to **full automatic AGC** on the live REC OUT tap without a **dedicated spike** on fingerprint quality and **false-boundary** rates.

---

## Product goals

1. **Default identification path**: **ACRCloud** when configured (today’s primary **documented** API path), with **AudD** (or similar) as optional **snippet-friendly** REST providers, and **`shazamio`** only as an **optional, unofficial** integration (see below).
2. **Bundled optional providers**: **ACRCloud** is **off until configured** (BYOK keys). **`shazamio`** is **optional** via `install-shazam.sh` + venv path—**not** the same as a first-party **Shazam** developer API. **AudD** (or peers) as optional BYOK additions with public docs.
3. **User-defined order**: Replace fixed enums with an **ordered list** of providers. **iOS** should present this as a **modern card list**: **drag-and-drop** defines sequence; **toggles** enable one or more; the Pi runs the chain in **that order** for **`first_success`** (and similar policies). Collapsed cards keep the screen simple; expanding (chevron) reveals credentials, limits, and advanced options (see **iOS settings UX: provider cards**).
4. **Per-provider roles**: Each enabled provider can participate as **`primary`**, **`confirmer`**, and/or **`arbitration`** (same semantics as before; coordinator defines call order).
5. **Security**: For paid or sensitive keys, prefer **storage on the phone** so the **Pi backend never holds plaintext secrets**—with explicit tradeoffs (see below).
6. **Multi-provider outcomes**: User-controlled **merge** behaviour (`first_success`, `best_score`, `require_agreement`, `arbitrate`, optional `user_picks_on_conflict` later)—beyond today’s **sequential first match** (`ChainRecognizer`; see *Primary order, confidence, and parallel execution*).
7. **Per-provider usage limits**: Optional **daily / monthly / rolling** caps and **warn thresholds** so BYOK users do not exceed paid API plans; continuity calls metered separately where configured (see **Per-provider usage limits** below). Include an explicit **reset** control so users can clear local counters and **unblock** recognition after a mistaken cap or after upgrading a vendor plan (does not change vendor-side quota).

---

## Minimum executable install (from zero)

**Intent:** Define what “Oceano works” means on a **fresh install** before any flexible-provider or iOS-heavy work lands. This keeps scope honest for a **single-operator** or **early-adopter** setup: recognition config evolution (**explicit provider list and later phases**) is **not** a prerequisite for a usable appliance.

### What must run

| Requirement | Notes |
|-------------|--------|
| **`oceano-source-detector`** + **`oceano-state-manager`** | Core path: capture → source classification, VU, unified state. |
| **Unified state output** | `/tmp/oceano-state.json` with `source`, `vu`, `state`; `track` may be empty or streaming-derived. |
| **Optional but practical: `oceano-web`** | Serves **HTTP APIs** (e.g. `POST /api/config`) and **`/nowplaying.html`** for the local display; use **iOS** or **`oceano-setup`** for operator-facing configuration—see **Client apps vs oceano-web**. |

### What is explicitly optional at first boot

| Optional | Notes |
|----------|--------|
| **Track recognition (physical)** | Requires **non-empty `recognition.providers`** with at least one **enabled** primary backed by credentials/install; otherwise recognition is disabled (`README.md`, `docs/cross-repo-sync.md`). **Physical / None** and AirPlay/BT metadata paths still behave as configured. |
| **`recognition.providers[]` (explicit list)** | **Required** for physical recognition; `recognizer_chain` is **deprecated** for runtime ordering (kept only for compatibility in older JSON/systemd flags). |
| **iOS companion** | Not required for a bare-minimum Pi; becomes the **primary** operator UI. Until then: `sudo oceano-setup`, hand-edited `config.json`, or `curl` to `POST /api/config`. |

### Green-path checklist (documentation target)

Use this as the **README / first-boot** bar; mirror in `README.md` when user-visible install docs are updated:

1. Install `.deb` or `install.sh` stack as documented; enable core systemd units.
2. Confirm `oceano-source-detector` and `oceano-state-manager` are **active** (`journalctl` clean of fatal errors).
3. Confirm `/tmp/oceano-state.json` updates (source + VU) during playback or silence as expected.
4. **If** recognition is desired: add **`recognition.providers`** (non-empty) **and** credentials per enabled slot (or `shazamio` path when using that id); until then, expect **no** ACR/AudD/Shazam calls.
5. **If** you change service-affecting fields in `config.json` by hand: run **`sudo systemctl restart oceano-web`** (or `POST /api/config` once from a client) so systemd units for detector/manager stay aligned with JSON.

**Planning implication:** **`recognition.providers[]`** is the **only** supported way to enable physical recognition ordering; **`recognizer_chain`** is not used to infer providers at runtime. Prioritize **iOS** (or `POST /api/config`) emitting a concrete `providers` array on every save.

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

## Primary order, confidence, and parallel execution (clarification)

**Shipped runtime (`merge_policy: first_success` + `ChainRecognizer`):** Providers with the **`primary`** role are ordered **exactly as they appear** in `recognition.providers[]`. For one capture WAV, the chain calls the **first** primary, then the **second**, and so on **only** when the previous call yields **no usable match** (`nil` / no match) or an error that allows advancing (see `internal/recognition/chain.go`). The **first non-nil successful result ends the chain**. A **low confidence score on an otherwise successful match does not** automatically try the next primary—doing that needs new coordinator logic or a different **`merge_policy`** (planned below).

**`confirmer` / `arbitration`:** These roles are **separate** from the sequential “try next on failure” rule: the coordinator uses them for **second-pass validation**, tie-breaks, or dispute resolution—not as “fallback because score was low” unless product explicitly adds that rule.

**iOS / operator copy:** Describe list order as **priority**: “try the first provider for this excerpt; if it cannot identify the track, try the next.” Do **not** promise score-based fallback or ranked alternatives until the backend implements them.

**Future (this document’s merge modes):** Modes such as **`best_score`**, **parallel primary calls**, **`require_agreement`**, **`user_picks_on_conflict`**, and **`arbitrate`** (see tables under *User-configurable strategies*) cover **alternative candidates**, **confidence-based selection**, and **parallel execution**—they extend **`merge_policy`** and coordinator behaviour beyond today’s **sequential fallback on no-match/error only**.

---

## Multi-provider aggregation vs Shazam-only continuity

### Why Shazam continuity exists today

Physical playback often has **gapless or low-silence** transitions. The main recognizer is driven by **VU / source boundaries** and timers; those can **miss** a side-B→side-A style change or a CD index jump with little RMS dip. The **continuity** loop is a **parallel channel**: periodic captures run **`shazamio`** and compare its answer to the **currently displayed** track and, after confirmation rules, fire a **re-recognition** when they diverge. Today **`RecognitionPlan.Continuity`** is that subprocess instance in `recognition_setup.go` **when `shazam` participates in `recognition.providers`** (enabled row + roles); the interpreter path is **`BundledShazamioPythonBin`** unless a non-empty `--shazam-python` override is passed for debugging.

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
| **`first_success`** | Current shipped model: sequential chain; **first successful match wins**; next primary runs only after **no match / eligible error** on the previous (not “low score” on a match). | Simple; no UI conflict. Optional minimum-confidence gating is **not** implemented unless added explicitly. |
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

### iOS settings UX: provider cards (target design)

**Intent:** A single, discoverable screen that matches how the backend thinks: **ordered providers**, **enabled flags**, and **detail on demand**—no separate “chain enum” picker.

| Element | Behaviour |
|---------|-----------|
| **Card list** | One card per known provider type (`acrcloud`, `audd`, `shazam`, …). Cards are shown in **list order** = **`providers[]` order** in `POST /api/config`. |
| **Drag and drop** | User reorders cards; on save, the app writes the **same order** to JSON. The Pi’s primary chain follows this sequence (for each enabled entry with a **`primary`** role, or simplified model: **enabled** ⇒ participates in order—product can map “enabled + order” to roles in one step). |
| **Toggle** | Per-card **On/Off**. Off ⇒ `enabled: false` for that entry; it is **skipped** at runtime but **stays in the list** so the user does not lose ordering or credentials. At least one enabled primary (or enabled card, per final rules) required before save. |
| **Chevron / disclosure** | Collapsed by default: logo, short label, toggle, drag handle. Expanded: **credentials** (masked fields, Keychain-backed where applicable), **usage limits** (caps, warn threshold, **reset counters**), optional **roles** or **continuity** sub-sections if not moved to a global row. |
| **Visual polish** | Rounded cards, spacing, optional subtle reorder affordance (handle icon); **SF Symbols** chevron rotation; light haptics on drop (optional). Follow **Human Interface Guidelines** for edit mode vs always-on drag per platform version. |

**Why it is simpler:** One mental model—**“top to bottom is try order; toggles are who is in the race”**—instead of cross-referencing a chain preset with a separate provider list. Advanced users still get limits and keys without cluttering the collapsed state.

**Implementation notes (`oceano-player-ios`):** Use **`SwiftUI`** `List` + `.onMove` / drag delegates, or **`UICollectionView`** with compositional layout if richer card chrome is needed. Persist order as array indices only; avoid parallel “sort priority” integers that can drift. Document screenshots and behaviour in **`docs/cross-repo-sync.md`** when the settings contract ships.

**Web (`oceano-web`):** Optional parity later (sortable list + expando); not required for the product story if **iOS** is the primary editor.

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
| **`oceano-web`** | Ships in the `.deb` for setup/admin; **not** the long-term primary client—see **Client apps vs oceano-web**. |
| **iOS (`oceano-player-ios`)** | **First** native consumer of config/state APIs; primary UX: per-provider **enable**, **roles** (primary / confirmer / arbitration), **reorder** primaries, ACRCloud / AudD / **`shazamio`** (venv) fields; **mask** secrets; optional **connection test**; validate “at least one primary” before save. **Android** follows the same backend contract when introduced. |

---

## Execution order: backend first, then iOS (incremental testing)

**Principle:** Land **additive, testable** backend slices first. **iOS** follows once the **config contract** and (where needed) **HTTP APIs** are stable. Avoid breaking `oceano-player-ios` consumers of `GET/POST /api/config` or state JSON. A **fresh Pi** can satisfy **Minimum executable install (from zero)** with **no** `providers[]` and **no** recognition keys until the operator opts in (physical recognition stays **off** until `providers` is configured).

### How to interleave work

| Step | Layer | What ships | How you test before iOS |
|------|--------|------------|-------------------------|
| **1** | Backend (**shipped 2026-05**) | **`recognition.providers[]` required** for physical recognition; **`recognizer_chain` deprecated** for runtime plan build; **`oceano-web`** does **not** materialize `providers` from chain on save (`docs/cross-repo-sync.md`). | Missing/empty `providers` → recognition disabled + `recognition.phase: not_configured` in state JSON. |
| **2** | Backend | **`merge_policy`** + coordinator (`first_success` only = current semantics). | Toggle policy in JSON; verify logs and `oceano-state.json`. |
| **3** | Backend | Continuity refactor flags (`continuity.enabled`, `continuity.provider`) behind defaults matching today. | Flip in JSON; gapless CD session on hardware. |
| **4** | iOS | Settings screens: edit `recognition` (providers, roles, keys), call existing **`POST /api/config`** — **must persist non-empty `providers`** when any recognition slot is enabled. | Device-only UX; Pi already understands payload. |
| **5** | iOS + Backend | **Option A** delegated recognition (new channel) only after steps 1–4 are stable. | Contract in `docs/cross-repo-sync.md`; staged feature flag. |

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

## Per-provider usage limits (BYOK billing protection)

**Goal:** Let users align Oceano’s call volume with **each vendor’s plan** (credits, daily/monthly caps, burst limits) so the Pi does not **silently overspend** API quota after noisy vinyl sessions, aggressive refresh intervals, or continuity polling.

This complements `docs/plans/recognition-provider-chain-improvement.md` (quotas at coordinator level); here the emphasis is **per-provider, user-configurable ceilings** surfaced in **config + UI + optional state hints**.

### What counts as “one use”

| Call path | Should count toward provider limits? | Notes |
|-----------|--------------------------------------|--------|
| **Primary chain** `Recognize` (boundary / timer / manual trigger) | **Yes** — one increment **per successful HTTP attempt** to that provider (or per subprocess invocation for **`shazamio`**). | If the chain tries ACR then AudD on the same WAV, **each** provider that is actually called increments **its** counter. |
| **Confirmer / arbitration** second pass | **Yes** for the provider that runs the confirmation call. | Same boundary can therefore consume **two** units for one provider if design is “ACR then ACR confirm” (rare); document behaviour. |
| **Retries** after transport error | **Product choice:** default **do not** count failed network attempts; **do** count HTTP **4xx/5xx** where the vendor may still bill (configurable `count_failed_requests`). | Avoid punishing users for flaky Wi-Fi while still respecting provider billing docs. |
| **Continuity / periodic monitor** | **Yes**, against a **separate budget** (recommended) or the **same** counter with a lower cap — user choice. | Continuity can dominate volume if interval is short; default UX should warn when enabling continuity without a continuity-specific cap. |
| **Delegated recognition (Option A, iOS relay)** | Count on **Pi** when the job is **dispatched** (or when iOS acknowledges receipt) so local metering matches user expectation even if HTTP executes on the phone. | iOS may additionally show vendor dashboards; Pi remains source of truth for “stop firing jobs.” |

### Limit dimensions (configurable per provider)

| Dimension | Purpose |
|-----------|---------|
| **`max_calls_per_calendar_day`** (optional) | Hard ceiling in **UTC calendar day** (simple to explain and reset); good for vendors that bill “per day.” |
| **`max_calls_per_rolling_24h`** (optional) | Sliding window; better for burst-heavy plans. **Either** calendar **or** rolling per counter family — avoid double-counting the same calls in two windows unless product explicitly supports “whichever is stricter wins.” |
| **`max_calls_per_calendar_month`** (optional) | Protects monthly credit packs (AudD-style credits, ACR tier caps). |
| **`warn_threshold_ratio`** (e.g. `0.8`) | When **used ≥ ratio × limit** for the active window, emit **log + machine-readable state** so **iOS / web** can show “approaching limit.” |
| **`on_limit`** | **`block`** (skip calls, recognition may fall through to next provider or show “quota exhausted”) vs **`allow_overrun`** (log only; **not** default for commercial UX). |

**Default policy for new installs:** limits **unset** = **no local cap** (current behaviour), preserving backward compatibility. Power users and commercial SKUs opt in explicitly.

### Config shape (conceptual extension)

Extend each entry in `recognition.providers[]` (or a parallel map keyed by provider `id`) with optional **`usage_limits`**:

```json
{
  "id": "audd",
  "enabled": true,
  "roles": ["primary"],
  "credential_ref": "config:audd",
  "usage_limits": {
    "max_calls_per_calendar_day": 500,
    "max_calls_per_calendar_month": 10000,
    "warn_threshold_ratio": 0.85,
    "on_limit": "block",
    "count_failed_requests": false,
    "continuity_budget": {
      "max_calls_per_calendar_day": 200,
      "share_main_counter": false
    }
  }
}
```

- **`continuity_budget`**: when `share_main_counter` is `true`, periodic continuity uses the **same** counters as the main chain (simplest mental model, easiest to exhaust accidentally).
- **Global fallback** (optional): `recognition.usage_limits_defaults` applied when a provider omits `usage_limits`, so iOS can ship presets (“AudD hobby tier template”).

### Enforcement architecture

1. **Single choke-point** before every `Recognizer.Recognize` (and before enqueueing **Option A** jobs): `UsageLimiter.Allow(ctx, providerID, callKind)` where `callKind` is `primary | confirmer | arbitration | continuity`.
2. **Persistence:** durable counters in **`library.db`** (or a small sidecar SQLite table `recognition_usage`) with **atomic increment + window key** (`audd:2026-05-02`, `audd:2026-05`) to survive restarts; avoid `/tmp`-only state.
3. **On block:** coordinator skips that provider for this attempt; logs `recognition: provider=audd limit=day reason=max_calls_per_calendar_day`; optional **`recognition`** subtree in `oceano-state.json` with `phase: "limit_reached"` and `provider` so **iOS** can toast without scraping logs.
4. **Clock skew:** document that limits use **Pi system clock**; NTP recommended on the appliance.

### UX and cross-repo

| Surface | Behaviour |
|---------|-----------|
| **`oceano-player-ios`** | Per-provider settings: show **used / limit** per window, **warn** banner, link to vendor billing page; validate save when limits are logically impossible (e.g. continuity cap > main cap with shared counter). **Reset counters** action (see below). |
| **`oceano-web`** (if retained) | Same fields for Pi-local admins; `GET /api/recognition/usage` (read usage); **`POST /api/recognition/usage/reset`** (or equivalent) for admin reset; optional **Recognition** page control: “Reset usage counters” with confirmation. |
| **Docs** | README: “Usage limits are enforced **on-device** to protect your API plan; they are **not** a substitute for vendor-side dashboards.” Explain that **reset** only clears **local** bookkeeping on the Pi. |

### Counter reset (unblock after local limit)

Users who hit **`on_limit: block`** need a **deliberate way to clear enforcement** without waiting for the next calendar window or editing SQLite by hand.

| Topic | Specification |
|--------|----------------|
| **Semantics** | Reset **only** Oceano’s **persisted counters** (SQLite). It does **not** increase vendor API quota, refund credits, or undo HTTP **429** throttling on the provider side. Copy in UI: short disclaimer before confirm. |
| **Scopes** | At minimum: **`provider_id`** (e.g. `audd`, `acrcloud`) + optional **`call_kind`** (`primary \| continuity \| all`). Optional **`windows`**: `["day","month","rolling"]` or **`all`** to wipe every stored bucket for that provider (simplest “unblock me now” button). |
| **API** | e.g. `POST /api/recognition/usage/reset` with JSON body `{ "provider": "audd", "scope": "all" }` (exact shape in `docs/cross-repo-sync.md`). Same **auth / CSRF** posture as `POST /api/config` (local-trust LAN model today). |
| **State** | After reset, coordinator clears **`limit_reached`** (or equivalent) on the next successful `Allow`; optionally bump a **`usage_counters_reset_at`** timestamp in state for debugging. |
| **Audit** | Log: `recognition: usage counters reset provider=audd scope=all requested_by=web` (or session id); helps support without silent circumvention. |
| **CLI / support** | Optional: `oceano-state-manager` or small `oceano-usage-reset` helper invoking the same library function — only if product wants headless SSH recovery without the web UI. |

**UX pattern:** On the recognition / metrics screen, per provider show **Used: N / limit** with a secondary control **“Reset local counters”** → confirm dialog → success toast. A single **global** “Reset all providers” remains **secondary** (dangerous) or hidden under **Advanced**.

Record contract changes in `docs/cross-repo-sync.md` when `oceano-state.json`, `POST /api/config`, or **`POST /api/recognition/usage/reset`** gains fields or behaviour.

### Testing and telemetry

- **Unit tests:** boundary at 23:59:59 → 00:00:00 reset; rolling window eviction; `block` vs `allow_overrun`.
- **Integration:** dry-run mode or `OCEANO_USAGE_LIMIT_DRY_RUN` env (optional) logs **would-block** without incrementing — for support only, not default in production.

### Phasing (relative to other plan items)

| Phase | Scope |
|-------|--------|
| **B1c** | Introduce **`UsageLimiter`** + SQLite counters + wiring in coordinator; **no default limits** until JSON present; **`POST /api/recognition/usage/reset`** (or shared library entrypoint) + **Counter reset** UX on web/iOS. |
| **B2+** | When **AudD** / multi-provider is default, ship **example presets** in docs (not in image) for common vendor tiers. |
| **I1+** | iOS editors for `usage_limits` + usage readback + **Reset local counters** per provider (same contract as web reset API). |

Align with **`recognition-provider-chain-improvement.md`** so global “parallel mode” quotas and per-provider limits compose predictably (e.g. **stricter of local limit vs global coordinator cap** wins).

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
| **Explicit provider list** | **Shipped:** `recognition.providers[]` + `merge_policy` (default `first_success`); **non-empty `providers`** required for physical recognition; **`recognizer_chain` not used** for runtime ordering (see **Minimum executable install** — recognition optional until configured). |
| **B1** | **`buildRecognitionComponents`** data-driven from `providers` + **roles**; confirmer / arbitration wiring; logs + validation hints for invalid configs. |
| **B1b** | Extend **`merge_policy`** (`best_score`, `require_agreement`, `arbitrate`) without changing default behavior until explicitly set. |
| **B1c** | **Per-provider usage limits** — `UsageLimiter`, SQLite-backed counters, coordinator choke-point; optional `usage_limits` on each provider; defaults **off**; **reset** API + UI to clear local counters and unblock (see **Counter reset**). |
| **B2** | **AudD** — **shipped** in `internal/recognition/audd.go`; config `audd_api_token` + chain modes `audd_first` / `audd_only` + insertion into `acrcloud_first` / `shazam_first` when token set. Further REST providers reuse the same pattern. |
| **B3** | **Continuity refactor**: `continuity.enabled`, `continuity.provider`; migrate Shazam-prefixed keys; hardware validation. |
| **B4** | **RMS-aware** capture skip / LP run-in tuning; extends to **quiet program starts** and optional **first-window capture policy** (see **Quiet program starts, live fades, and capture gain**). |
| **B5** | **Option A** Pi endpoint(s) or channel for **iOS-mediated** recognition jobs; security review. |

### iOS (`oceano-player-ios`) — after contract is stable

| Phase | Scope |
|-------|--------|
| **I1** ✅ **(MVP UI shipped; contract follow-up)** | **Recognition settings** (`oceano-player-ios`): provider cards, reorder, toggles, credentials, masking, **`shazamio`** disclosure. **Backend now requires** persisted **`recognition.providers[]`** on every save that should enable physical recognition — do not rely on `recognizer_chain` alone (see **iOS I1 — completion status**). |
| **I2** | **Option A** client: subscribe to Pi job channel, run provider calls with Keychain secrets, return results. |
| **I3** | Optional: **`user_picks_on_conflict`** UI when state exposes `track_candidates[]`. |

#### iOS I1 — completion status (2026-05-02)

**MVP UI (2026-05-02)** in `oceano-player-ios`: Physical Media settings use **provider cards** with **drag-and-drop order**, per-card **toggle**, and **disclosure** for credentials (ACRCloud, AudD API token, shazamio path / flag); save goes through **`POST /api/config`** with `recognizer_chain` + credential fields derived from slot order/toggles via `RecognitionProviderCatalog` (client-side); **`acoustid_client_key`** is stripped on load/save. `PhysicalMediaConfigView.swift` / `PhysicalMediaConfigClient.swift`.

**Follow-up (2026-05-03 backend):** iOS must **always emit `recognition.providers`** (non-empty when recognition should run) on save — backend no longer infers providers from `recognizer_chain`. Surface **`not_configured`** / empty-provider states in UX if helpful.

**Still open vs full I1 scope:** per-provider **`usage_limits`** editors and **usage reset** actions, richer BYOK / unofficial **`shazamio`** copy — see **Deferred: Provider quota / rate-limit UX** (this doc).

### Deferred / parallel research

| Phase | Scope |
|-------|--------|
| **P*** | Parallel recognition, per-provider timeouts; compose with **per-provider usage limits** (`B1c`) and `recognition-provider-chain-improvement.md` coordinator quotas. |

---

## Deferred: Provider quota / rate-limit UX (not scheduled)

**Goal:** Surface **provider quota exhaustion** (e.g. ACRCloud **3003** *requests limit exceeded*) to the user in the **providers / recognition settings** flow, not only in Pi logs. Complement with **optional operator-defined budgets** so the app can **warn before** the vendor hard-stops.

**Backend backoff (implemented):** ACRCloud JSON status **3003** (and **4001**, **4003**) map to `ErrRateLimit` in `internal/recognition/acrcloud.go`, so the coordinator applies **`rateLimitBackoff` (5 min)** and **does not** bypass backoff on boundary triggers while `backoffRateLimited` is set — avoids ~30 s retry loops that hammer the API after quota exhaustion.

**Backend / contract (when implemented):**

- Expose a **stable snapshot** in unified state and/or **`GET /api/status`** (and SSE) — e.g. per-provider `last_error_code`, `rate_limited`, `rate_limited_message`, optional `rate_limited_until` — so clients do **not** need extra polling loops or heavy library calls.
- Optional **`config.json`** fields (per provider or global): **user-declared monthly cap** (estimated calls) and **reset day of month** (billing-aligned by user). The Pi maintains an **approximate local counter** (primary + confirmation + chain retries); label in UX as **device-estimated**, not a substitute for the vendor dashboard.
- **Source of truth** for “hard blocked” remains the **vendor HTTP response**; local limits are **advisory** only.

**iOS (`oceano-player-ios`):**

- Decide how to render **banner / badge / section** on the providers screen; consume the **same** state fields the app already loads for setup — **efficient**, no tight polling.
- Record follow-ups in **`docs/cross-repo-sync.md`** when fields ship.

**Non-goals for this deferral:**

- No **UI / state JSON / iOS** surfacing yet; no vendor-specific **preset** packages (pricing changes). Server-side **quota-class backoff** for ACR is in place (see above).

**Related:** Open question §5–§6 (*Usage limits*); `recognition-shazamio-deferral-continuity-and-extensibility.md` (live/gapless policy).

---

## Documentation and cross-repo

- After code changes to **`recognition.providers`** / **`merge_policy`** wiring, run the checks in **`docs/reference/recognition.md`** (section *Explicit provider list (mandatory verification)*) and **`.cursor/skills/pi-recognition-explicit-providers-smoke/SKILL.md`** (`go test` + `scripts/pi-recognition-provider-smoke.sh` on a Pi).
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
5. **Usage limits:** should vendor **429 / quota** responses automatically **tighten** local counters or only rely on pre-configured caps?
6. **Usage limits:** do we ship **vendor-named presets** (risky if pricing changes) vs **generic numeric templates** only in docs?
7. **Low-level program material:** Is **B4** (trigger/capture policy + telemetry) enough for most users, or do we need a **supported** path for **recognition-only digital gain** / **opt-in ALSA assist** (see **Quiet program starts, live fades, and capture gain**)?

---

## References (in-repo)

- `scripts/poc_acoustid.py` + `scripts/requirements-acoustid-poc.txt` — **historical POC** only (AcoustID not a shipping path).
- `cmd/oceano-state-manager/recognition_setup.go` — chain construction; `Continuity` recognizer wiring.
- `cmd/oceano-state-manager/main.go` — `runShazamContinuityMonitor`, `tryEnableShazamContinuity`.
- `cmd/oceano-state-manager/source_vu_monitor.go` — interaction when continuity is “ready” vs VU boundaries.
- `cmd/oceano-state-manager/recognizer.go` — WAV capture format and skip window.
- `internal/recognition/*` — recognizer interface and providers.
- `docs/plans/recognition-provider-chain-improvement.md` — roles, quotas, parallel recognition.
- `docs/plans/recognition-shazamio-deferral-continuity-and-extensibility.md` — Shazamio deferral, continuity provider choice, custom-provider security.
- `docs/metrics-snapshots/README.md` — recognition stats snapshots; example baseline JSON under `docs/metrics-snapshots/` (e.g. `baseline-pre-shazamio-summary-reset-2026-05-03.json`).
- `docs/plans/recognition-enhancement.md` — CPU / contention notes for fingerprint work on Pi.
