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

## Repository layout

```
cmd/
  oceano-source-detector/   # Go: Physical/None detector + VU + PCM relay (systemd service)
  oceano-state-manager/     # Go: unified state aggregator + ACRCloud recognition (systemd service)
calibration/                # Python: capture and analyse calibration sessions
scripts/
  test-acoustid.sh          # Standalone ACRCloud recognition test (stop detector first)
install.sh                  # Installer: AirPlay stack (shairport-sync + bridge + watchdog)
install-source-detector.sh  # Installer: builds and installs the Go detector
install-source-manager.sh   # Installer: builds and installs the Go state manager
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

# Monitor logs
journalctl -u oceano-source-detector.service -f
journalctl -u oceano-state-manager.service -f
```

## What NOT to do

- Do not block the ALSA device with multiple `arecord` calls — consume the PCM socket from `oceano-source-detector` instead; PipeWire monitor taps are the long-term replacement
- Do not add UI rendering code here — this is a backend-only repository
- Do not hardcode thresholds without calibration data — always note they are estimates and point to the calibration workflow
