# Recognition System — Findings & Improvement Opportunities

*Reviewed: 2026-04-15. Based on direct code inspection of
`cmd/oceano-state-manager/` and `internal/recognition/`.*

---

## 1. How the system works today

Recognition runs as a single coordinator loop (`recognitionCoordinator.run`)
fed by a buffered channel (`recognizeTrigger`, capacity 1). Two sources
generate triggers:

- **Boundary triggers** — VU monitor silence→audio transitions, or SIGUSR1 (manual/web UI)
- **Fallback timer** — periodic re-check at `RecognizerMaxInterval` (default 5 min);
  an accelerated `RecognizerRefreshInterval` fires sooner when a track is already
  recognised and the timer simply wants to confirm continuity

Each cycle:

1. Pre-capture source guards (3×: before skip delay, after skip, after capture+recognition)
2. PCM capture from `/tmp/oceano-pcm.sock`
3. Chromaprint fingerprint generation (configurable windows)
4. Optional local-first fingerprint short-circuit (confirmed library entries only,
   stricter BER threshold, skipped on boundary triggers)
5. Provider chain: `ChainRecognizer` → ACRCloud → Shazam (sequential, first match wins)
6. Optional confirmation step (second capture, second provider run, configurable delay)
7. Result applied to state + library; Shazam alignment goroutine spawned
8. Pending triggers drained

A parallel goroutine (`runShazamContinuityMonitor`) polls every ~8 s to detect
track changes between VU-monitor boundaries, using Shazam as a lightweight
fingerprinter.

---

## 2. What works well

- **Multi-layer source guards**: three defensive checks prevent a streaming result
  (AirPlay/Bluetooth active during capture) from being written to the physical library.
- **Local-first fingerprint short-circuit**: avoids a remote API call when the
  currently playing track is already in the library and the fingerprint match is
  confident. Correctly skipped on boundary triggers.
- **Confirmation step**: optional second capture + cross-provider validation before
  accepting a new track. Correctly skipped for boundary triggers and high-confidence
  scores where the round-trip latency is not worth the delay.
- **Boundary bypass of backoff**: real track-change events skip the no-match/error
  backoff so the UI shows "Identifying" immediately rather than waiting 15–30 s.
- **Atomic state writes**: all state updates under mutex + rename-on-write to JSON.
- **Stats recording**: every recogniser call is wrapped with library event tracking,
  giving visibility into provider success/failure rates over time.
- **Pending-stub enrichment**: repeated no-match retries on the same boundary append
  fingerprints to one stub row instead of creating duplicates.

---

## 3. Issues found — by severity

### 3.1 Shazam subprocess architecture (High impact)

**File:** `internal/recognition/shazam.go`

Every Shazam recognition call:
1. Creates a temp `.py` file on disk
2. Spawns a new Python interpreter process
3. Imports `shazamio` (full async event loop setup)
4. Sends the audio, waits for result
5. Deserialises JSON from stdout

This is the most fragile part of the system. Concrete problems:

- **Latency**: cold Python startup + `shazamio` import takes 1–3 s before any audio
  is even sent. The continuity monitor (already a parallel goroutine) absorbs this,
  but every alignment check and every continuity poll pays this tax.
- **Silent init failure**: `NewShazamRecognizer` returns `nil` if the Python binary
  is missing or `import shazamio` fails. There is no warning in the logs. The entire
  continuity system is disabled without any visible signal.
- **No score**: Shazam results carry no confidence score. The confirmation logic
  cannot use score-based bypass (`ConfirmationBypassScore`) for Shazam-only results.
- **Path safety**: `wavPath` is passed directly to `exec.CommandContext` as an
  argument — safe as-is because `/tmp` paths don't have spaces, but fragile if the
  temp dir is ever changed.
- **No retry / no timeout**: if the subprocess hangs, the calling context timeout
  is the only protection (`ctx` is passed to `CommandContext`).

See section 4 for the full Shazam HTTP alternative evaluation.

---

### 3.2 Global mutex held during library I/O (Medium impact)

**File:** `cmd/oceano-state-manager/main.go:223`, `recognition_coordinator.go`

A single `sync.Mutex` protects all manager state. Several code paths hold the lock
while calling SQLite:

- `applyRecognizedResult` → `lib.UpsertTrack`, `lib.SaveFingerprints`,
  `lib.RecordPlay` — can take 50–150 ms on a loaded Pi SD card.
- `findByFingerprintsWithFilter` (in `FindByFingerprints`, `FindConfirmedByFingerprints`)
  is called from the coordinator loop, not under the lock directly, but the coordinator
  sets `recognizerBusyUntil` under the lock and then does I/O outside it.

While these heavy calls are in progress, other goroutines that try to acquire the lock
(VU monitor, Bluetooth monitor, source watcher) are blocked. The recognizeTrigger
channel (capacity 1) means a boundary trigger that arrives while the lock is held for
DB work can be silently dropped if the previous trigger is still in the channel.

In practice this is unlikely to cause a visible bug because real track boundaries are
seconds apart. But on slow SD cards it is a latency risk.

---

### 3.3 Shazam continuity grace period has no success requirement (Medium impact)

**File:** `cmd/oceano-state-manager/main.go:432–440`

```go
const continuityGracePeriod = 30 * time.Second
if !ready && time.Since(lastRecognizedAt) < continuityGracePeriod {
    continue
}
```

After 30 s the monitor runs unconditionally, even if `tryEnableShazamContinuity`
failed because Shazam returned no match at all for this recording. The two-sighting
confirmation (added 2026-04-15) prevents a single false positive, but does not prevent
a scenario where Shazam consistently misidentifies the same recording as a different
track — in that case both sightings will agree on the wrong result and a spurious trigger
will fire every ~3 min.

**Root cause**: The grace period was added to fix the case where Shazam had a transient
error during alignment. It is too broad: it also activates for recordings that Shazam
genuinely cannot identify correctly.

**Better approach**: require at least one agreeing poll (Shazam returns same
title+artist as ACRCloud) before the monitor is considered calibrated. If Shazam never
agrees within, say, 5 min, disable the continuity monitor for this track and rely on
the VU monitor + fallback timer alone.

---

### 3.4 FindByFingerprints does a full table scan (Medium impact)

**File:** `internal/library/library.go:626–718`

`findByFingerprintsWithFilter` loads every fingerprint row joined to its collection
entry, with no WHERE clause that limits by approximate fingerprint hash or entry count.
On a library with many entries this is an O(N×M) comparison (N entries × M fingerprints
per entry × number of captured windows).

This runs:
- On every fallback-timer cycle (after no-match from remote providers)
- On every error recovery attempt
- On the local-first pre-check

On a Raspberry Pi 5 with hundreds of library entries the query itself may be fast
(SQLite in WAL mode is efficient), but the in-process BER comparison loop is CPU-bound
and grows linearly with library size.

---

### 3.5 Stubs have no creation timestamp / no aging policy (Low–Medium impact)

**File:** `internal/library/library.go` — `collection` table schema

Unconfirmed stubs (tracks that ACRCloud never matched) accumulate indefinitely. There
is no `created_at` column on stub rows, so it is impossible to automatically prune stubs
older than N days. Over time this:
- Slows `FindByFingerprints` (more rows to scan)
- Makes the library web UI harder to manage (stale entries)
- Complicates the "link stub to confirmed entry" feature (more orphaned rows to handle)

---

### 3.6 Confirmation disagrees → original result accepted without re-try (Low impact)

**File:** `recognition_coordinator.go:411–429`

When the confirmation capture fails (network error, capture error) or the confirmer
returns no-match, the original candidate is accepted as-is with a log line. This is
intentional ("fail open"), but means a capture error during confirmation silently
promotes an unconfirmed result. A one-line note in the log is the only signal.

Similarly, when primary and confirmer disagree (both find a track but different ones),
the original candidate is kept — there is no escalation or alert, and the disagreement
is not recorded in stats.

---

### 3.7 Chain swallows intermediate errors (Low impact)

**File:** `internal/recognition/chain.go:53–74`

`ChainRecognizer.Recognize` returns only `lastErr`, losing all intermediate errors.
If ACRCloud returns a rate-limit error and Shazam returns a network error, the caller
only sees the Shazam error. Stats tracking (via `stats_recognizer.go` wrappers) does
record per-provider outcomes, but the coordinator's log and error-handling logic see
only the final error, which affects whether `backoffRateLimited` is set correctly.

---

### 3.8 Continuity mismatch key is title+artist string (Low impact)

**File:** `cmd/oceano-state-manager/main.go:474–476`
(calls `canonicalTrackKey` on both `current` and `shRes`)

The two-sighting confirmation compares from→to pairs using canonical title+artist
strings. If the same underlying track returns with minor metadata differences between
two Shazam polls (e.g. "feat. X" in one, absent in another; or trailing whitespace),
the keys differ and the second sighting is not recognised as a confirmation — a new
first-sighting is recorded instead, effectively resetting the counter.

Using ShazamID for comparison would be more stable, but Shazam does not always return
an ID for every result.

---

## 4. Shazam HTTP alternative — full evaluation

### 4.1 What `shazamio` actually does

`shazamio` is an unofficial Python library that reverse-engineered the Shazam mobile
app's internal HTTP API. It:
1. Converts the audio to a Shazam signature (a proprietary fingerprint format derived
   from a spectrogram)
2. POSTs the signature to `https://amp.shazam.com/discovery/v5/...` with the same
   headers the iOS app sends
3. Parses the JSON response

This is what the current subprocess approach calls, wrapped in Python asyncio.

### 4.2 Calling the Shazam API directly from Go

**Option A — Unofficial Shazam endpoint (same as shazamio)**

A Go library exists: `github.com/lrmn7/go-shazam` and similar forks. The approach is
identical to `shazamio` but removes the Python subprocess entirely:

- No Python process spawning
- No temp file per call
- Full `context.Context` propagation (real cancellation, not just process kill)
- Score field potentially extractable from the response JSON
- Latency drops from ~2–4 s (Python cold start) to ~0.3–0.8 s (HTTP round-trip)

**Risks:**
- Unofficial API — no SLA, no versioning, no documentation
- Shazam has broken `shazamio` multiple times by rotating signing keys or changing
  request format. The same breakage would affect a Go port.
- Shazam signature algorithm (the audio fingerprinting step before the HTTP call) is
  not documented. Any Go implementation must port this algorithm, which is complex.
  `shazamio` depends on `acoustid` or a C extension for this step.
- If Shazam detects unusual traffic patterns (running on a Raspberry Pi IP, not a
  mobile device) it may block or rate-limit without notice.

**Option B — Apple/Shazam official API (ShazamKit)**

Apple released ShazamKit as an official SDK for iOS/macOS. There is no official
REST API or cross-platform SDK. ShazamKit is only available as a native framework on
Apple platforms and cannot be called from a Raspberry Pi.

**Option C — RapidAPI / third-party Shazam wrappers**

Several providers on RapidAPI expose Shazam-compatible endpoints:

- `shazam.p.rapidapi.com` — unofficial wrapper, marketed as the "Shazam API"
- Pricing: free tier ≈ 500 requests/month; paid plans start at ~$10–$20/month for
  5 000–10 000 requests
- Account required: RapidAPI account + credit card for paid tiers
- Same data quality as Shazam (same underlying database)
- REST endpoint: `POST /songs/detect` — sends base64-encoded audio, returns JSON
- **Advantage**: stable URL and versioned; if broken, provider is accountable
- **Disadvantage**: costs money beyond free tier; another account/key to manage;
  free tier is very limited for a system that runs a poll every ~8 s

At 8 s intervals with Vinyl playing 45 min per side:
- ~337 continuity checks per side
- Plus alignment calls at track start (~1–3 per track × ~10 tracks)
- Total: ~350–360 Shazam calls per album side
- Free tier (500/month) exhausted in ~1–2 album sides per month

**Option D — AcoustID (open-source, free)**

AcoustID is an open audio fingerprinting service backed by the same Chromaprint
algorithm already used for local library fingerprinting. It identifies recordings
using MusicBrainz IDs.

- Free, open-source, no account required beyond a one-time application key registration
- No per-call cost
- Chromaprint fingerprint generation is already done in the system (`fpcalc`)
- Go client available or easy to implement (simple HTTPS POST with form data)
- **Limitation**: database is community-sourced, less comprehensive than Shazam/ACRCloud
  for niche or regional releases
- **Best use case**: as an additional provider in the chain, or to replace the Shazam
  alignment/continuity check where real-time identification is less critical

### 4.3 Recommendation

| Option | Cost | Stability | Effort | Score |
|---|---|---|---|---|
| Keep Python subprocess (status quo) | Free | Fragile | None | Low |
| Go port of unofficial Shazam API | Free | Fragile | High | Medium |
| RapidAPI Shazam wrapper | $10–20/mo beyond ~1–2 album sides | Stable | Low | Medium |
| AcoustID (Chromaprint-based) | Free | Stable | Low–Medium | High |

**Short term**: replace the subprocess with a Go implementation of the unofficial
Shazam endpoint. The algorithm is already implemented in open-source Go forks; the main
risk is API instability. This eliminates cold-start latency and silent init failures
at zero cost.

**Medium term**: add AcoustID as an additional provider in the chain. Since Chromaprint
fingerprinting is already happening for local library matching, sending the same
fingerprint to AcoustID is low-overhead and free. It would reduce dependence on both
ACRCloud (paid, rate-limited) and the unofficial Shazam endpoint.

**Avoid**: RapidAPI Shazam for a personal always-on Raspberry Pi system. The volume
of continuity-monitor calls makes the economics poor for what is essentially a
duplicate cross-check — the marginal value does not justify the ongoing cost and
account management overhead.

---

## 5. Priority summary

| # | Finding | Effort | Impact |
|---|---|---|---|
| 1 | Replace Shazam subprocess with Go HTTP call | Medium | High — latency, reliability, silent failures |
| 2 | Grace period: require ≥1 agreeing poll before enabling continuity | Low | Medium — eliminates systematic false positives |
| 3 | Add AcoustID as free supplementary provider | Medium | Medium — reduces ACRCloud cost exposure, improves coverage |
| 4 | Stubs: add `created_at` + auto-prune policy | Low | Medium — library hygiene, FindByFingerprints performance |
| 5 | Log warning when Shazam init fails | Trivial | Low — observability |
| 6 | Continuity mismatch: prefer ShazamID over string key | Low | Low — prevents confirmation counter reset on metadata variants |
| 7 | FindByFingerprints: add index or tier-based pre-filter | Medium | Low now, Medium as library grows |
| 8 | Lock: separate DB writes from state lock | Medium | Low now, Medium on slow SD cards |
