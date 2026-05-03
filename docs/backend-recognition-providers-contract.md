# Oceano Player iOS — recognition config contract (backend-aligned)

This document is the **downstream checklist** for `oceano-player-ios` against **`oceano-player` `main`** (post–2026-05 recognition work). The Go backend remains the source of truth; update this file when the contract changes.

**Primary backend references**

- `oceano-player/docs/cross-repo-sync.md` — logs **2026-05-02** (additive `providers`), **2026-05-03** (`providers` required; no server-side materialize from `recognizer_chain`), **Shazam / bundled path**, **VU / ACR 3003 backoff**.
- `oceano-player/docs/reference/recognition.md` — runtime plan from `recognition.providers`, Shazamio continuity, verification policy.
- `oceano-player/cmd/oceano-web/recognition_materialize.go` — `materializeRecognitionProvidersIfEmpty` (normalizes empty slice + `merge_policy` only; **does not** infer rows from `recognizer_chain`).

---

## Verdict on the short “agent checklist”

The original five non-negotiables are **directionally correct**. Gaps addressed below: **explicit JSON keys** (`shazam_recognizer_enabled`, deprecated `shazam_python_bin`), **runtime state / SSE phases**, **Shazam continuity** (server-side), and **optional UX** when ACR quota is exhausted.

---

## Non-negotiables (must implement / verify on device)

### 1. Persist `recognition.providers` on Save

- When physical recognition should run, **`POST /api/config` must include a non-empty `recognition.providers` array** (ordered list = try order for enabled primaries).
- Row shape (Physical Media provider cards / `POST` body):

  ```json
  { "id": "acrcloud" | "audd" | "shazam", "enabled": true|false, "roles": ["primary"] }
  ```

  - Use **`roles: ["primary"]`** only for rows that are actually active primaries; disabled rows should use **`roles: []`** (matches coordinator expectations for “skipped” entries).
  - Preserve unknown future provider objects from **`GET`** at the end of the array unchanged (including `credential_ref` if present).

### 2. `recognition.merge_policy`

- Send **`merge_policy": "first_success"`** whenever you send `providers` (backend defaults empty policy to `first_success` on save, but being explicit avoids ambiguity).

### 3. Do not rely on `recognizer_chain` for runtime

- `recognizer_chain` may remain in JSON for compatibility; **`oceano-state-manager` builds the recognition plan only from `recognition.providers`**.
- **`oceano-web` does not** repopulate `providers` from `recognizer_chain` on `POST /api/config`. Credentials alone are **not** enough if `providers` is missing or `[]`.

### 4. Upgrade / first-run path

- On **`GET /api/config`**, if `recognition.providers` is missing or empty: **pre-fill** the in-app card model from credentials + toggles + legacy `recognizer_chain`, then **prompt Save** so the next **`POST`** includes a concrete `providers` array (**client merge** — same wording as `cross-repo-sync.md`).

### 5. Shazamio on/off (no path from the client)

- Use **`recognition.shazam_recognizer_enabled`** (bool). When **`false`**, the backend forces any `providers` row with **`id == "shazam"`** to **`enabled: false`** on load so the subprocess is not started.
- **Do not** send a Python path for product configuration. Prefer **omitting** or **removing** deprecated **`shazam_python_bin`** on save; `oceano-web` migrates/clears legacy keys and always wires **`--shazam-python`** to the bundled interpreter for systemd.
- Stable wire id remains **`"shazam"`**; UI copy may say **Shazamio**.

### 6. State / SSE (`recognition` in unified state)

Handle at least:

| `phase` | `detail` (typical) | UX |
|--------|---------------------|-----|
| `not_configured` | `no_recognition_providers` | Physical recognition disabled until `providers` is saved (non-empty with runnable primary). |
| `matched` | — | Show provider + score when present. |
| `identifying` | e.g. `capturing` | Respect **`recognizerBusyUntil`** semantics on backend; terminal phases below win when applicable. |
| `no_match` | `no_match` | Terminal — must not stay stuck “identifying” when this is set. |
| `off` | `input_policy_off` | Terminal — recognition skipped for current input policy. |

### 7. Validation before Save

- Keep “at least one enabled primary with valid credentials for that id” before claiming recognition is on.
- When **all provider toggles are off**, sending **`providers: []`** is valid and matches “recognition off” after save.

---

## Backend guarantees (no iOS change required unless regressing)

- **Plan build:** `recognition.providers` only; empty / missing → no runnable chain.
- **Shazamio continuity:** Started when a Shazamio client exists and policy allows — **no need** to add a `"confirmer"` role from iOS for baseline continuity; future role UX may extend this.
- **ACRCloud HTTP 3003 / JSON 3003:** Treated as **rate limit** with **~5 min** coordinator backoff (reduces hammering); Shazamio may still answer in chain — expect more **Shazam-only** matches when ACR is in quota.
- **`GET /api/config` caching:** `oceano-web` returns a strong **`ETag`** (SHA-256 of the JSON body) and **`Cache-Control: private, no-cache`**. Clients MAY send **`If-None-Match`** with the previous ETag to receive **`304 Not Modified`** (empty body) when the file-backed config is unchanged — less LAN traffic and JSON parsing on refresh.

---

## Optional iOS improvements (quality / parity)

- **Quota UX:** When logs or support show ACR **3003**, surface a short in-app note (upgrade ACR plan or wait for backoff) — no new JSON fields today.
- **Release note:** After backend seek-anchoring changes, “time into track” on Now Playing may differ following long `no_match` streaks (`cross-repo-sync` 2026-05-03 VU / seek item).
- **Legacy GET:** If old Pis still return `shazam_python_bin`, treat it only as migration input for **`shazam_recognizer_enabled`**; never depend on a user-editable path.

---

## Backend change plan (only if client cannot comply)

**Default:** no backend contract change is required if iOS always sends non-empty `providers` when recognition should run.

**Fallback ideas (product decision — not implemented by default):**

1. **One-shot migration tool** (operator / support): script or `curl` using the same ordering rules as `buildRecognitionProvidersFromLegacyChain` in `recognition_materialize.go` to write `providers` once — **not** a permanent “server fabricates providers” mode (that would reintroduce drift).
2. **Observability only:** optional `GET /api/config` echo of `recognition_plan_summary` — would be additive; discuss in backend if support load is high.

---

## UX copy (English strings in app)

- Card order = **recognition try order** for enabled primaries under `merge_policy: first_success`. The Pi advances the chain on **no match** or certain errors, **not** solely on “low confidence” scores.
- Shazamio: clarify **optional fallback**, not an official Shazam API (already in card subtitles).

---

## Out of scope (do not block release)

- New `merge_policy` values (`best_score`, parallel primaries, user picks).
- Phone-mediated / delegated recognition (I2) until explicitly specified in backend.

---

## Verification matrix (before App Store / tagged release)

- [ ] Fresh Pi with credentials but **no** `providers` in `/etc/oceano/config.json` → app shows banner / `not_configured` after Save requirement; **Save** writes non-empty `providers`.
- [ ] Toggle all providers off → `providers: []` → state shows **`not_configured`** / recognition off.
- [ ] Shazam toggle off → `shazam_recognizer_enabled: false` and shazam rows disabled in `providers`.
- [ ] Round-trip: unknown `credential_ref` or future id preserved in `GET` → merged back on `POST`.
- [ ] ACR quota exhausted (3003): app remains stable; Shazamio can still identify or show `no_match` without crash loops.
