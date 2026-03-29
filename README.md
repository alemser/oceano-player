# Oceano Player

Audio backend for **Raspberry Pi 5 → USB DAC → Magnat MR 780**.

Provides a unified playback state file (`/tmp/oceano-state.json`) and a real-time
VU meter socket (`/tmp/oceano-vu.sock`) that the UI reads, regardless of the
active source (AirPlay, physical media, or silence).

## Services

| Service | Install script | Role |
|---|---|---|
| `shairport-sync.service` | `install.sh` | AirPlay receiver |
| `oceano-airplay-bridge.service` | `install.sh` | Routes loopback audio to DAC |
| `oceano-bridge-watchdog.service` | `install.sh` | Reconnects bridge when DAC wakes from standby |
| `oceano-source-detector.service` | `install-source-detector.sh` | Captures REC-OUT via USB capture card; detects physical media presence; publishes VU frames |
| `oceano-state-manager.service` | `install-source-manager.sh` | Merges all sources into `/tmp/oceano-state.json` |

---

## Installation (on the Pi)

Raspberry Pi OS 64-bit (Bookworm) recommended.

### Step 1 — AirPlay stack

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

This installs and starts `shairport-sync`, the loopback bridge, and the watchdog.

Verify:
```bash
sudo systemctl status shairport-sync.service
journalctl -u shairport-sync.service -f
```

### Step 2 — Source detector

Reads the amplifier REC-OUT via a USB capture card. Detects whether physical
media is playing (any source routed through the amplifier: vinyl, CD, tuner, etc.)
and publishes real-time stereo RMS levels to the VU meter socket.

The capture card is auto-detected by name — if the ALSA card number changes
after a reboot, the service recovers automatically.

```bash
sudo ./install-source-detector.sh
```

Verify:
```bash
sudo systemctl status oceano-source-detector.service
journalctl -u oceano-source-detector.service -f
cat /tmp/oceano-source.json
```

### Step 3 — State manager

Merges AirPlay metadata and physical source detection into a single state file.
AirPlay takes priority — physical detection is ignored when streaming is active.

```bash
sudo ./install-source-manager.sh
```

Verify:
```bash
sudo systemctl status oceano-state-manager.service
journalctl -u oceano-state-manager.service -f
cat /tmp/oceano-state.json
```

---

## Output: `/tmp/oceano-state.json`

Written atomically whenever state changes. The UI polls or watches this file.

```json
{
  "source": "AirPlay",
  "state": "playing",
  "track": {
    "title": "So What",
    "artist": "Miles Davis",
    "album": "Kind of Blue",
    "duration_ms": 562000,
    "seek_ms": 12400,
    "seek_updated_at": "2026-03-29T20:30:00Z",
    "samplerate": "44.1 kHz",
    "bitdepth": "16 bit",
    "artwork_path": "/tmp/oceano-artwork-a1b2c3d4.jpg"
  },
  "updated_at": "2026-03-29T20:30:05Z"
}
```

`source` values: `AirPlay` | `Physical` | `None`

`track` is `null` when `source` is `Physical` (metadata identification is a future
feature) or `None`.

**UI progress interpolation** — to avoid polling for seek position:
```
current_position_ms = seek_ms + (now - seek_updated_at) * 1000
```

---

## VU meter socket

`/tmp/oceano-vu.sock` — Unix stream socket published by `oceano-source-detector`.

The REC-OUT of the Magnat MR 780 is always active regardless of the selected
input (AirPlay, vinyl, CD, tuner), so this socket provides a consistent stereo
signal for all sources.

| Field | Type | Description |
|---|---|---|
| Left RMS | `float32` LE | Left channel level [0.0, 1.0] |
| Right RMS | `float32` LE | Right channel level [0.0, 1.0] |

Each frame is 8 bytes. Multiple consumers can connect simultaneously; frames are
dropped silently if a consumer falls behind — the audio loop is never blocked.

Frame rate: ~22 fps (2048-sample buffer at 44.1 kHz ≈ 46 ms per frame).

---

## Update

Re-run any install script to pull the latest code and restart the service:

```bash
sudo ./install.sh
sudo ./install-source-detector.sh
sudo ./install-source-manager.sh
```

To deploy a specific branch:

```bash
sudo ./install.sh --branch my-branch
sudo ./install-source-detector.sh --branch my-branch
sudo ./install-source-manager.sh --branch my-branch
```

---

## Configuration reference

### `install.sh`

| Option | Default | Description |
|---|---|---|
| `--airplay-name` | `Triangle AirPlay` | AirPlay receiver name |
| `--usb-match` | `M780` | Text to match USB DAC in ALSA device list |
| `--alsa-device` | *(auto-detected)* | Explicit ALSA device string |
| `--preplay-wait-seconds` | `8` | Seconds to wait for DAC wake-up before playback |
| `--output-strategy` | `loopback` | `loopback` or `direct` |

Persistent config at `/opt/oceano-player/config.env` — edit and re-run to apply.

### `install-source-detector.sh`

| Option | Default | Description |
|---|---|---|
| `--device-match` | `USB Microphone` | Substring to match in `/proc/asound/cards` (auto-detects card number) |
| `--device` | *(none)* | Explicit ALSA fallback device if match fails |
| `--silence-threshold` | `0.008` | RMS below this = no physical source |
| `--debounce` | `10` | Majority vote window size |
| `--vu-socket` | `/tmp/oceano-vu.sock` | Unix socket for VU meter frames |

### `install-source-manager.sh`

| Option | Default | Description |
|---|---|---|
| `--metadata-pipe` | `/tmp/shairport-sync-metadata` | shairport-sync metadata FIFO |
| `--source-file` | `/tmp/oceano-source.json` | Source detector output |
| `--output` | `/tmp/oceano-state.json` | Unified state output |
| `--artwork-dir` | `/tmp` | Directory for artwork cache files |

---

## Clean reinstall

```bash
sudo systemctl disable --now shairport-sync.service oceano-airplay-bridge.service \
  oceano-bridge-watchdog.service oceano-source-detector.service \
  oceano-state-manager.service 2>/dev/null || true
sudo rm -rf /opt/oceano-player
sudo systemctl daemon-reload

curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
sudo ./install-source-detector.sh
sudo ./install-source-manager.sh
```

---

## Developer

### Run tests

```bash
go test ./...
```

### Enable pre-commit hook (runs tests on every commit)

```bash
git config core.hooksPath .githooks
```

### Output strategy

- **`loopback`** *(recommended)* — shairport-sync plays to a virtual ALSA loopback sink. The bridge forwards audio to the real DAC; the watchdog reconnects when the DAC wakes from standby.
- **`direct`** — outputs directly to the DAC. Simpler, but no standby resilience.

---

## Next steps

- Bluetooth receiver (BlueZ + PipeWire)
- UPnP/OpenHome (`upmpdcli` / `gmrender-resurrect`)
- HTTP + SSE server in state manager (real-time push to UI, replaces file polling)
- Track identification for physical media (Chromaprint + AcoustID)
- Configuration UI for device settings (ALSA device, thresholds, display mode)
