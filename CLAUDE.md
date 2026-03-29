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
| Physical media | Implemented (presence detection only — `Physical` or `None`) |
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

Track metadata for physical media (Vinyl/CD) is identified via Chromaprint + AcoustID fingerprinting.

## Planned architecture

PipeWire is the target audio routing layer. It allows multiple consumers (VU meter, detector, recording) to tap the same audio stream simultaneously without blocking each other.

```
PipeWire monitor tap
       │
       ▼
oceano-backend (Go)
  ├── source detector (Vinyl/CD vs None)
  ├── VU meter computation
  ├── metadata aggregator (shairport-sync pipe, BlueZ D-Bus, UPnP events)
  └── WebSocket server → UI
```

Current state uses `arecord` directly against the ALSA device, which is a single-reader model. Migration to PipeWire is the next architectural step.

## Repository layout

```
cmd/
  oceano-source-detector/   # Go: Vinyl/CD/None detector (runs as systemd service)
calibration/                # Python: capture and analyse calibration sessions
install.sh                  # Installer: AirPlay stack (shairport-sync + bridge + watchdog)
install-source-detector.sh  # Installer: builds and installs the Go detector
config.yaml                 # ALSA device + AirPlay name
```

## Source detector

The detector (`cmd/oceano-source-detector/main.go`) classifies audio as `Vinyl`, `CD`, or `None` by:

1. Computing RMS to gate silence
2. Applying a Hann window and FFT on each audio buffer
3. Computing the **sub-bass energy ratio** (15–40 Hz / total) — vinyl motor/arm resonance lives below musical content; CD has near-zero energy there
4. Using a **majority vote** over a rolling window of N detections to commit a source change

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

# On the Pi — source detector
sudo ./install-source-detector.sh \
  --vinyl-ratio-threshold 0.02 \
  --min-vinyl-rms 0.010

# Monitor detector logs
journalctl -u oceano-source-detector.service -f
```

## What NOT to do

- Do not block the ALSA device with multiple `arecord` calls — use PipeWire monitor taps when adding new consumers
- Do not add UI rendering code here — this is a backend-only repository
- Do not hardcode thresholds without calibration data — always note they are estimates and point to the calibration workflow
