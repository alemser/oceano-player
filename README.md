# Oceano Player

Audio backend for **Raspberry Pi 5 → USB DAC → Magnat MR 780**.

Provides a unified playback state file (`/tmp/oceano-state.json`) that the UI reads,
regardless of the active source (AirPlay, physical media, or silence).

## Services

| Service | Install script | Role |
|---|---|---|
| `shairport-sync.service` | `install.sh` | AirPlay receiver |
| `oceano-airplay-bridge.service` | `install.sh` | Routes loopback audio to DAC |
| `oceano-bridge-watchdog.service` | `install.sh` | Reconnects bridge when DAC wakes from standby |
| `oceano-source-detector.service` | `install-source-detector.sh` | Detects physical media presence via USB capture card |
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

Detects whether physical media (vinyl, CD, or any analog source) is playing via the
amplifier REC-OUT → USB capture card (DIGITNOW on `plughw:2,0`).

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

`track` is `null` when `source` is `Physical` (metadata identification is a future feature)
or `None`.

**UI progress interpolation** — to avoid polling for seek position:
```
current_position_ms = seek_ms + (now - seek_updated_at) * 1000
```

---

## VU meter socket

The source detector also publishes real-time audio levels to `/tmp/oceano-vu.sock`
(Unix socket). Each frame is 8 bytes: `float32 left RMS` + `float32 right RMS`, little-endian.
Connect any number of consumers simultaneously. Frames are dropped silently if consumers
fall behind — the audio loop is never blocked.

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
| `--device` | `plughw:2,0` | ALSA capture device (USB capture card) |
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

- Bluetooth receiver (BlueZ + pipewire)
- UPnP/OpenHome (`upmpdcli` / `gmrender-resurrect`)
- HTTP + SSE server in state manager (real-time push to UI, replaces file polling)
- Track identification for physical media (Chromaprint + AcoustID)
