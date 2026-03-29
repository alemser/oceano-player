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

Track metadata for physical media (Vinyl/CD) is identified via ACRCloud audio fingerprinting.

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
  └── writes /tmp/oceano-state.json      (unified state for UI)
```

**Source priority**: if physical audio is detected on the REC OUT capture card, the amplifier
is routing a physical source. The Magnat MR 780 requires manual input switching, so physical
detection takes priority over any concurrently active AirPlay stream.

**Recognition flow**:
1. `pollSourceFile` detects `Physical` → fires trigger immediately
2. `runVUMonitor` watches VU frames for silence gaps between tracks → fires trigger on audio resumption
3. `runRecognizer` waits for triggers, reads PCM from the socket, calls ACRCloud, updates state
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
- **Device picker** — `/api/devices` scans `/proc/asound/cards` and returns ALSA card names so
  the user can pick a device without knowing the card number.

The static UI (`cmd/oceano-web/static/index.html`) is embedded into the binary at compile time via
`//go:embed static`, so a single binary is deployed.

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

## Repository layout

```
cmd/
  oceano-source-detector/   # Go: Physical/None detector + VU + PCM relay (systemd service)
  oceano-state-manager/     # Go: unified state aggregator + ACRCloud recognition (systemd service)
  oceano-web/               # Go: configuration web UI + status API (systemd service, port 8080)
calibration/                # Python: capture and analyse calibration sessions
scripts/
  test-acoustid.sh          # Standalone ACRCloud recognition test (stop detector first)
install.sh                  # Installer: AirPlay stack (shairport-sync + bridge + watchdog)
install-source-detector.sh  # Installer: builds and installs the Go detector
install-source-manager.sh   # Installer: builds and installs the Go state manager
install-oceano-web.sh       # Installer: builds and installs the web UI
config.yaml                 # ALSA device + AirPlay name
```

## Source detector

The detector (`cmd/oceano-source-detector/main.go`) classifies audio as `Physical` or `None` by:

1. Computing RMS to gate silence — majority vote over a rolling window commits source changes
2. Publishing VU frames (float32 L+R) to `/tmp/oceano-vu.sock` for the UI and state manager
3. Relaying raw PCM chunks to `/tmp/oceano-pcm.sock` so the state manager can capture audio
   for recognition without opening the ALSA device a second time

Output: `/tmp/oceano-source.json`

### Calibration workflow

```bash
cd calibration
./capture-session.sh silence  # record baseline
./capture-session.sh cd       # record CD playing
./capture-session.sh vinyl    # record vinyl playing
./analyse-session.sh          # prints suggested thresholds
```

Then pass the suggested `--vinyl-ratio-threshold` to `install-source-detector.sh`.

## Hardware

- **Raspberry Pi 5**
- **Magnat MR 780** amplifier (USB DAC via `plughw:2,0`)
- **DIGITNOW USB capture card** on card 2 — captures REC OUT from amplifier

## Code conventions

- All code and documentation in **English**
- Go: standard library only where possible, no heavy frameworks
- Shell scripts: `bash`, `set -euo pipefail`, no external deps beyond standard Pi OS packages
- Systemd for process supervision — no custom daemons or init scripts
- Output state as atomic JSON file writes (`write tmp → rename`)

## Deployment

```bash
# On the Pi — AirPlay stack
sudo ./install.sh

# On the Pi — source detector (exposes VU + PCM sockets)
sudo ./install-source-detector.sh \
  --vinyl-ratio-threshold 0.02 \
  --min-vinyl-rms 0.010

# On the Pi — state manager (with ACRCloud recognition)
sudo ./install-source-manager.sh \
  --acrcloud-host identify-eu-west-1.acrcloud.com \
  --acrcloud-access-key <key> \
  --acrcloud-secret-key <secret>

# On the Pi — web configuration UI (accessible at http://<pi-ip>:8080)
sudo ./install-oceano-web.sh

# Monitor logs
journalctl -u oceano-source-detector.service -f
journalctl -u oceano-state-manager.service -f
journalctl -u oceano-web.service -f
```

## Troubleshooting

### ACRCloud not recognising tracks / recognition fails silently

**Symptom:** `no match` in state manager logs, or `network is unreachable`.

1. **Check RMS level** — target is 0.05–0.25 during playback:
   ```bash
   journalctl -u oceano-source-detector.service -f
   # look at: heartbeat: source=Physical rms=X
   ```

2. **RMS too high (> 0.40)** — REC OUT is overdriving the capture card, causing clipping that corrupts the fingerprint. Reduce capture volume:
   ```bash
   amixer -c 3 sset 'Mic' 3      # start here; adjust up/down until RMS ≈ 0.15
   alsactl store                  # persist across reboots
   ```
   The working value on the Magnat MR 780 + DIGITNOW card is **level 3 / 53% → RMS ≈ 0.19**.

3. **Network unreachable (IPv6)** — the ACRCloud client forces IPv4 since Pi networks often lack IPv6 routing. If this error appears, confirm IPv4 connectivity:
   ```bash
   curl -4 https://identify-eu-west-1.acrcloud.com
   ```

4. **Album art shows wrong album** — expected when playing a compilation (e.g. a "Best Of"). ACRCloud identifies by audio fingerprint and returns the best-known release for that recording. No workaround without manual input.

---

### Source oscillating rapidly between Physical and None

**Symptom:** logs show `None → Physical → None → Physical` several times per second.

The silence threshold is too close to the noise floor at the current capture volume. Raise it:

```bash
sudo ./install-source-detector.sh \
  --branch music-recognition \
  --silence-threshold 0.025
```

The default threshold (0.008) is calibrated for higher capture volumes. After reducing capture volume to level 3, use **0.025** as the silence threshold.

---

### Track info stays on screen after record is changed or side is flipped

If the phono stage has residual hum, the source may remain `Physical` during record changes. The VU monitor clears the recognition result after ~2 s of silence, but only if RMS drops below the silence threshold. If hum keeps RMS above the threshold, the old track persists.

Options:
- Raise `--silence-threshold` slightly so phono hum is treated as silence
- The `--recognizer-max-interval` (default 5 min) will eventually trigger a new recognition

---

## What NOT to do

- Do not block the ALSA device with multiple `arecord` calls — consume the PCM socket from `oceano-source-detector` instead; PipeWire monitor taps are the long-term replacement
- Do not add UI rendering code here — this is a backend-only repository
- Do not hardcode thresholds without calibration data — always note they are estimates and point to the calibration workflow
