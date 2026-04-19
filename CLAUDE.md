# CLAUDE.md — Oceano Player

## Project overview

Oceano Player is an audio backend for a **Raspberry Pi 5** connected to a **Magnat MR 780** amplifier via USB. It runs headlessly on the Pi and exposes audio state to a separate UI process.

The long-term goal is a **unified backend** that the UI queries regardless of the active audio source.

## Intended sources

| Source | Status |
|---|---|
| AirPlay | Implemented (shairport-sync) |
| Bluetooth | Planned |
| UPnP | Planned |
| Physical media | Implemented (`Physical` / `None` detection + ACRCloud track identification) |
| Vinyl vs CD distinction | Future (requires reliable calibration data) |

## Intended UI data contract

The backend must expose a single stream (WebSocket or SSE) that the UI consumes:

```json
{
  "source": "AirPlay | Bluetooth | UPnP | Physical | None",
  "track": { "title": "", "artist": "", "album": "", "artwork_url": "" },
  "vu": { "left": 0.0, "right": 0.0 },
  "state": "playing | stopped | idle"
}
```

Track metadata for physical media (Vinyl/CD) is identified via the configured online recognizer chain.

## Architecture

```
ALSA device (arecord)
       │
       ▼
oceano-source-detector
  ├── RMS / majority-vote → /tmp/oceano-source.json   (Physical | None)
  ├── VU frames → /tmp/oceano-vu.sock                 (float32 L+R at ~21.5 Hz)
  └── raw PCM   → /tmp/oceano-pcm.sock                (S16_LE stereo 44100 Hz)

oceano-state-manager
  ├── reads /tmp/oceano-source.json      (physical source polling)
  ├── reads /tmp/oceano-vu.sock          (VU monitor: silence→audio = track boundary trigger)
  ├── reads /tmp/oceano-pcm.sock         (recognition capture — no second arecord needed)
  ├── reads shairport-sync metadata pipe (AirPlay metadata)
  ├── internal/recognition               (provider clients + chain orchestration)
  ├── internal/library                   (SQLite collection and artwork paths)
  ├── recognition coordinator            (trigger loop + confirmation + persistence policies)
  └── writes /tmp/oceano-state.json      (unified state for UI)
```

**Source priority**: if physical audio is detected on the REC OUT capture card, the amplifier
is routing a physical source. The Magnat MR 780 requires manual input switching, so physical
detection takes priority over any concurrently active AirPlay stream.

**Recognition flow**:
1. `pollSourceFile` detects `Physical` → fires trigger immediately
2. `runVUMonitor` watches VU frames for silence gaps between tracks → fires trigger on audio resumption
3. `runRecognizer` delegates to the recognition coordinator, which waits for triggers,
  captures PCM, runs the recognizer chain, applies confirmation/fallback policies,
  and persists track/artwork updates
4. On rate limit: backs off 5 min. On no match: retries after 90 s. Fallback: re-runs every `RecognizerMaxInterval` (default 5 min) even without a track boundary event.

**PipeWire migration**: once PipeWire replaces `arecord`, the PCM and VU sockets become PipeWire
monitor taps. Only `oceano-source-detector/main.go` changes; the state manager is unaffected.

## Web configuration UI

`cmd/oceano-web` is a self-contained HTTP server (default `:8080`) that provides:

- **Config editor** — reads/writes `/etc/oceano/config.json`, the single source of truth for all
  service parameters (audio devices, ACRCloud credentials, thresholds, socket paths).
- **Service restarter** — on save, rewrites the systemd unit files for `oceano-source-detector`
  and `oceano-state-manager` and restarts them via `systemctl`.
- **Status bar** — polls `/api/status` (proxies `/tmp/oceano-state.json`) to show live playback state.
- **Real-time stream** — `/api/stream` is a Server-Sent Events endpoint that pushes state changes
  whenever `/tmp/oceano-state.json` is modified (500 ms poll, `: ping` keepalive every 15 s).
- **Now Playing UI** — `/nowplaying.html` is a full-screen display page for 5"–7" HDMI/DSI screens
  (optimised for 1024×600). It connects to `/api/stream` and renders artwork, track metadata,
  source logos, and format-specific info (sample rate, bit depth, CD track, vinyl side/track).
- **Device picker** — `/api/devices` scans `/proc/asound/cards` and returns ALSA card names so
  the user can pick a device without knowing the card number.

All static assets (`index.html`, `nowplaying.html`) are embedded into the binary at compile time
via `//go:embed static`, so a single binary is deployed.

Config sections mirror the service CLI flags:

| Section | Controls |
|---|---|
| Audio Input | capture device (auto-detect by name or explicit `plughw:N,0`), silence threshold, debounce window |
| Audio Output | AirPlay name, DAC device (auto-detect or explicit) |
| Track Recognition | ACRCloud host / key / secret, capture duration, max re-recognition interval |
| Advanced | socket paths, state/source file paths, artwork dir, metadata pipe |

Install:
```bash
sudo ./install-oceano-web.sh
# optional: --addr :9090  --branch my-branch
```

## Now Playing display (HDMI/DSI)

`/nowplaying.html` is served by `oceano-web` and targets 5"–7" displays connected via HDMI or DSI
(validated on the 7" 1024×600 IPS HDMI monitor). It is designed for living-room viewing distance.

**Features:**
- Source logos (AirPlay, Bluetooth, UPnP, CD, Vinyl, Physical) with smooth transitions
- Artwork with graceful placeholder for unidentified or artwork-less tracks
- Large track title, artist, album text
- Format chips: sample rate + bit depth (AirPlay/streaming), codec + sample rate + bit depth (Bluetooth, detected via PipeWire within ~6s of playback start), CD track number, vinyl side + track
- Identifying animation while ACRCloud/Shazam recognises a new track
- Idle clock screen when no source is active
- Reconnecting SSE client with exponential back-off

**Auto-launch on Pi boot:**
```bash
sudo ./install-oceano-display.sh
# optional: --web-addr http://localhost:8080  --user pi
```

This installs `oceano-display.service`, which:
1. Runs `oceano-display-check` to detect a connected HDMI or DSI panel via `/sys/class/drm`.
   If no display is found the service exits cleanly — safe for headless Pi deployments.
2. Launches Chromium in kiosk mode pointing at `http://localhost:8080/nowplaying.html`.
3. Restarts automatically on crash.

**Local development:** open `http://<pi-ip>:8080/nowplaying.html` in any browser. The SSE stream
works across the network, so you can see the live display from a laptop while the Pi is playing.

## Repository layout

```
cmd/
  oceano-source-detector/   # Go: Physical/None detector + VU + PCM relay (systemd service)
  oceano-state-manager/     # Go: unified state aggregator + ACRCloud recognition (systemd service)
  oceano-web/               # Go: config UI + /api/stream SSE + /nowplaying.html (port 8080)
    static/
      index.html            #   Configuration UI (all screen sizes)
      nowplaying.html       #   Full-screen now playing UI for 5"–7" HDMI/DSI displays
scripts/
  test-acoustid.sh          # Legacy standalone AcoustID experiment (not used by services)
install.sh                  # Installer: AirPlay stack (shairport-sync + bridge + watchdog)
install-source-detector.sh  # Installer: builds and installs the Go detector
install-source-manager.sh   # Installer: builds and installs the Go state manager
install-oceano-web.sh       # Installer: builds and installs the web UI
install-oceano-display.sh   # Installer: kiosk Chromium service for HDMI/DSI display
```

## Source detector

The detector (`cmd/oceano-source-detector/main.go`) classifies audio as `Physical` or `None` by:

1. Computing RMS to gate silence — majority vote over a rolling window commits source changes
2. Publishing VU frames (float32 L+R) to `/tmp/oceano-vu.sock` for the UI and state manager
3. Relaying raw PCM chunks to `/tmp/oceano-pcm.sock` so the state manager can capture audio
   for recognition without opening the ALSA device a second time

Output: `/tmp/oceano-source.json`

## Hardware

- **Raspberry Pi 5**
- USB DAC / amplifier — auto-detected by name in ALSA, or configured explicitly via `plughw:N,0`
- USB capture card — captures REC OUT from the amplifier for physical source detection and ACRCloud recognition

## Code conventions

- All code and documentation in **English**
- Go: standard library only where possible, no heavy frameworks
- Shell scripts: `bash`, `set -euo pipefail`, no external deps beyond standard Pi OS packages
- Systemd for process supervision — no custom daemons or init scripts
- Output state as atomic JSON file writes (`write tmp → rename`)

## Engineering principles valued in this repo

- **Behavior-preserving refactors first**: prefer incremental extraction over rewrites; avoid changing runtime semantics unless explicitly requested.
- **No-regression discipline**: run package and full-repo tests after structural changes; if tests fail, fix immediately before proceeding.
- **Cohesion over file size**: group code by responsibility (wiring, metadata ingest, monitoring, recognition, persistence, output), not by convenience.
- **Loose coupling at boundaries**: avoid hidden field/implementation coupling between components; use explicit interfaces and narrow contracts.
- **Configurable provider orchestration**: recognizers should be easy to enable/disable/reorder and assign to distinct roles (primary, confirmer, continuity).
- **Pragmatic simplicity**: avoid over-engineering; prefer same-package file splits before introducing deeper package trees.
- **Operational reliability on Raspberry Pi**: prioritize stable long-running behavior, predictable backoff/retry logic, and atomic state updates.
- **Documentation stays in sync**: when architecture/workflows change, update README/CLAUDE/install help in the same change set.

## Deployment

```bash
# On the Pi — installs everything (AirPlay stack + detector + state manager + web UI)
sudo ./install.sh

# Then open http://<pi-ip>:8080 to set ACRCloud credentials and audio devices.

# Install the now-playing kiosk display (HDMI/DSI screens):
sudo ./install-oceano-display.sh
# optional: --web-addr http://localhost:8080  --user pi

# Individual services can still be updated independently:
sudo ./install-source-detector.sh --branch my-branch
sudo ./install-source-manager.sh --branch my-branch
sudo ./install-oceano-web.sh --branch my-branch

# Monitor logs
journalctl -u oceano-source-detector.service -f
journalctl -u oceano-state-manager.service -f
journalctl -u oceano-web.service -f
journalctl -u oceano-display.service -f
```

## Troubleshooting

### ACRCloud not recognising tracks / recognition fails silently

**Symptom:** `no match` in state manager logs, or `network is unreachable`.

1. **Check RMS level** — target is 0.05–0.25 during playback:
   ```bash
   journalctl -u oceano-source-detector.service -f
   # look at: heartbeat: source=Physical rms=X
   ```

2. **RMS too high (> 0.40)** — REC OUT is overdriving the capture card, causing clipping that degrades recognition quality. Find your capture card number with `arecord -l`, then reduce the level:
   ```bash
   amixer -c N sset 'Mic' 50%    # replace N with your card number; adjust until RMS ≈ 0.15–0.20
   alsactl store                  # persist across reboots
   ```

3. **Network unreachable (IPv6)** — the ACRCloud client forces IPv4 since Pi networks often lack IPv6 routing. If this error appears, confirm IPv4 connectivity:
   ```bash
   curl -4 https://identify-eu-west-1.acrcloud.com
   ```

4. **Album art shows wrong album** — expected when playing a compilation (e.g. a "Best Of"). ACRCloud returns the best-known release for the recording it matched. No workaround without manual input.

---

### Source oscillating rapidly between Physical and None

**Symptom:** logs show `None → Physical → None → Physical` several times per second.

The silence threshold is too close to the noise floor at the current capture volume. Raise it:

```bash
sudo ./install-source-detector.sh --silence-threshold 0.035
```

The default threshold is **0.025**. If your capture card has a higher noise floor, raise it further via the web UI under **Audio Input → Silence Threshold**.

---

### Track info stays on screen after record is changed or side is flipped

If the phono stage has residual hum, the source may remain `Physical` during record changes. The VU monitor clears the recognition result after ~2 s of silence, but only if RMS drops below the silence threshold. If hum keeps RMS above the threshold, the old track persists.

Options:
- Raise `--silence-threshold` slightly so phono hum is treated as silence
- The `--recognizer-max-interval` (default 5 min) will eventually trigger a new recognition

---

## Documentation hygiene

Whenever a feature is added, a flag is changed, or a workflow changes:

- **README.md** — keep "Installation", "First-time setup", "Troubleshooting", and "Configuration reference" in sync. A user following the README from scratch should succeed without consulting the code.
- **CLAUDE.md** — keep Architecture, Deployment, and What NOT to do current. If a section describes something that no longer exists, update or remove it.
- **Install scripts** — `--help` output must match the flags actually accepted.

If you make a change and notice that any of these are stale, update them in the same commit.

## What NOT to do

- Do not block the ALSA device with multiple `arecord` calls — consume the PCM socket from `oceano-source-detector` instead; PipeWire monitor taps are the long-term replacement
- Do not add UI rendering code here — this is a backend-only repository
- Do not hardcode thresholds without empirical data — always note they are estimates and suggest adjusting via the web UI
