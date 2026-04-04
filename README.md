# Oceano Player

Audio backend for making the **Raspberry Pi** a piece of your HI-FI equipament.

---

## Installation (on the Pi)

Raspberry Pi OS 64-bit (Bookworm) recommended.

**Before running the installer**, make sure your USB DAC / amplifier is powered
on and connected via USB — the device only appears in the ALSA device list when
active.

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

This single command installs all services and dependencies (including Go,
`shairport-sync`, `alsa-utils`, and `fpcalc` via `libchromaprint-tools`):

| Service | Role |
|---|---|
| `shairport-sync` | AirPlay receiver |
| `oceano-airplay-bridge` | Routes loopback audio to DAC |
| `oceano-bridge-watchdog` | Reconnects bridge when DAC wakes from standby |
| `oceano-source-detector` | Captures REC-OUT, detects physical media, publishes VU + PCM |
| `oceano-state-manager` | Merges all sources into `/tmp/oceano-state.json` |
| `oceano-web` | Configuration UI at `http://<pi-ip>:8080` |

---

## First-time setup after install

After installation, open `http://<pi-ip>:8080` in your browser and configure:

### 1. Audio capture level (required for track recognition)

The USB capture card volume must be set so that RMS stays between **0.05–0.25**
during playback. Check the current level:

```bash
journalctl -u oceano-source-detector.service -f
# look for: heartbeat: source=Physical rms=X
```

If RMS is too high (> 0.40) or too low (< 0.05), adjust the capture volume.
First find your capture card number:
```bash
arecord -l   # note the card number for your USB capture device
```

Then reduce or increase the mic level (replace `N` with your card number):
```bash
amixer -c N sset 'Mic' 50%   # start here; adjust until RMS ≈ 0.15–0.20
alsactl store                  # persist across reboots
```

### 2. ACRCloud credentials (required for track recognition)

Track identification for physical media (vinyl, CD) always generates a local
`fpcalc` fingerprint first and checks the library cache before falling back to
[ACRCloud](https://www.acrcloud.com). Without credentials, unmatched physical
tracks remain unidentified and `track` will be `null`.

In the web UI at `http://<pi-ip>:8080`, go to **Track Recognition** and fill in:
- **ACRCloud Host** — e.g. `identify-eu-west-1.acrcloud.com`
- **Access Key**
- **Secret Key**

Click **Save & Restart Services**. Confirm recognition is active:
```bash
journalctl -u oceano-state-manager.service -f | grep recognizer
# should show: recognizer [ACRCloud]: capturing ...
```

### 3. Audio input device (if auto-detection fails)

The capture card is auto-detected by name (`USB Microphone`). If it is not found,
set it explicitly in the web UI under **Audio Input → Device** (e.g. `plughw:3,0`).

To list available capture devices:
```bash
arecord -l
```

---

## Update

Re-run the main installer to update all services at once:

```bash
sudo ./install.sh
```

To deploy a specific branch:

```bash
sudo ./install.sh --branch my-branch
```

Individual services can still be updated independently:

```bash
sudo ./install-source-detector.sh --branch my-branch
sudo ./install-source-manager.sh --branch my-branch
sudo ./install-oceano-web.sh --branch my-branch
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

`track` is `null` when source is `Physical` and no recognition result is available yet, or when source is `None`.

**UI progress interpolation** — to avoid polling for seek position:
```
current_position_ms = seek_ms + (now - seek_updated_at) * 1000
```

---

## VU meter socket

`/tmp/oceano-vu.sock` — Unix stream socket published by `oceano-source-detector`.

The REC-OUT of the amplifier is captured by the USB capture card. This socket
provides the stereo signal for VU metering and ACRCloud recognition.

| Field | Type | Description |
|---|---|---|
| Left RMS | `float32` LE | Left channel level [0.0, 1.0] |
| Right RMS | `float32` LE | Right channel level [0.0, 1.0] |

Each frame is 8 bytes. Multiple consumers can connect simultaneously; frames are
dropped silently if a consumer falls behind — the audio loop is never blocked.

Frame rate: ~22 fps (2048-sample buffer at 44.1 kHz ≈ 46 ms per frame).

---

## Troubleshooting

### AirPlay not appearing on iPhone / devices

1. **Amplifier input not set to USB** — shairport-sync needs the DAC to be present at startup. Set the input to USB and re-run `sudo ./install.sh`.

2. **shairport-sync not running**:
   ```bash
   sudo systemctl status shairport-sync.service
   journalctl -u shairport-sync.service -n 30 --no-pager
   ```

3. **Loopback module not loaded**:
   ```bash
   lsmod | grep snd_aloop
   # if empty: sudo modprobe snd-aloop
   ```

---

### Track recognition not working (`track: null` for physical source)

1. **ACRCloud credentials not configured** — the most common cause after a fresh install. Open `http://<pi-ip>:8080` → **Track Recognition** → fill in credentials → **Save & Restart Services**.

2. **RMS too high (> 0.40)** — clipping corrupts the audio fingerprint. Find your capture card number with `arecord -l`, then reduce the level:
   ```bash
   amixer -c N sset 'Mic' 50%   # replace N with your card number
   alsactl store
   ```
   Target: **RMS ≈ 0.15–0.20** during normal playback.

3. **Source detector showing `None`** — the capture card may not be detected:
   ```bash
   journalctl -u oceano-source-detector.service -f
   # look for: heartbeat: source=Physical rms=X
   # if source=None while music is playing, check --device-match in the web UI
   ```

4. **Network unreachable (IPv6)** — ACRCloud client forces IPv4. Confirm connectivity:
   ```bash
   curl -4 https://identify-eu-west-1.acrcloud.com
   ```

---

### Source oscillating rapidly between Physical and None

The silence threshold is too close to the noise floor at the current capture volume. In the web UI under **Audio Input**, raise **Silence Threshold** to `0.025`.

---

### Track info stays on screen after record is changed

The last recognized track is shown for 60 seconds after audio stops (configurable via `--idle-delay`). If the phono stage has residual hum keeping RMS above the threshold, the source stays `Physical` and the old track persists until the next recognition cycle.

Options:
- Raise **Silence Threshold** slightly in the web UI so phono hum is treated as silence
- The recognizer retries at every track boundary (silence → audio transition)

---

### Album art shows wrong album

Expected when playing a compilation or "Best Of". ACRCloud identifies by audio fingerprint and returns the best-known release for that recording — which may be credited to "Various Artists" in music databases, causing the artwork lookup to fail.

---

## Configuration reference

All service parameters are managed through the web UI at `http://<pi-ip>:8080`.
The underlying config is stored at `/etc/oceano/config.json`.

### `install.sh` options

| Option | Default | Description |
|---|---|---|
| `--airplay-name` | `Oceano` | AirPlay receiver name |
| `--usb-match` | `M780` | Text to match USB DAC in ALSA device list |
| `--alsa-device` | *(auto-detected)* | Explicit ALSA device string |
| `--preplay-wait-seconds` | `8` | Seconds to wait for DAC wake-up before playback |
| `--output-strategy` | `loopback` | `loopback` or `direct` |
| `--branch` | `main` | Git branch to install |

Persistent config at `/opt/oceano-player/config.env`.

---

## Clean reinstall

```bash
sudo systemctl disable --now shairport-sync.service oceano-airplay-bridge.service \
  oceano-bridge-watchdog.service oceano-source-detector.service \
  oceano-state-manager.service oceano-web.service 2>/dev/null || true
sudo rm -rf /opt/oceano-player
sudo systemctl daemon-reload

curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

After reinstall, ACRCloud credentials are preserved if `/etc/oceano/config.json` was not deleted. If it was, re-configure credentials in the web UI and re-run `amixer` to set the capture level.

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

## Display UI

For a real-time display showing track metadata, artwork, and VU meters on an SPI-connected screen, install the companion project **[oceano-now-playing](https://github.com/alemser/oceano-now-playing)** — it reads `/tmp/oceano-state.json` and `/tmp/oceano-vu.sock` produced by this backend.

```bash
git clone https://github.com/alemser/oceano-now-playing.git
cd oceano-now-playing
./install.sh
```

Install oceano-player first (this project), then oceano-now-playing.

---

## Next steps

- Bluetooth receiver (BlueZ + PipeWire)
- UPnP/OpenHome (`upmpdcli` / `gmrender-resurrect`)
- Bluetooth receiver (BlueZ + PipeWire)
- UPnP/OpenHome (`upmpdcli` / `gmrender-resurrect`)
- PipeWire migration — replace `arecord` single-reader model with monitor taps
- Local media library — SQLite cache of recognized tracks enriched with MusicBrainz metadata and Cover Art Archive artwork; editable via web UI
