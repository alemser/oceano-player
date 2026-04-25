# Oceano Player

Audio backend for making the **Raspberry Pi** a piece of your HI-FI equipament.

---

## Installation (on the Pi)

Raspberry Pi OS 64-bit (Bookworm) recommended.

### Option A — Debian package (recommended)

Pre-built `arm64` binary, no compiler needed on the Pi. Downloads dependencies
automatically via `apt`.

**Where to get the `.deb`:** packages are attached to **GitHub Releases** when
a version tag (`v*`) is pushed. CI on the default branch runs tests and a
cross-compile check but **does not** publish a `.deb` on every run.

```bash
# Download the latest release
wget https://github.com/alemser/oceano-player/releases/latest/download/oceano-player_$(curl -s https://api.github.com/repos/alemser/oceano-player/releases/latest | grep -oP '"tag_name":\s*"v\K[^"]+')_arm64.deb

# Install (resolves apt dependencies automatically)
sudo apt install ./oceano-player_*_arm64.deb
```

Services are enabled and started automatically.

**Suggested order after `apt install`:**

1. Run **`sudo oceano-setup`** — AirPlay name, ALSA output and capture devices,
   Bluetooth, optional **HDMI/DSI kiosk** (Xvfb + `oceano-display` systemd, optional
   **LightDM** autologin to `oceano-kiosk` — the same flow as
   `install-oceano-display.sh` in this repo. On Raspberry Pi OS the wizard and script also
   adjust **`/etc/lightdm/lightdm.conf`** so `rpd-labwc` in the main file does not override the
   `oceano-kiosk` session; reboot after the display step if the panel is connected.
2. Open **`http://<pi-ip>:8080`** — ACRCloud credentials, **Audio Input** (device,
   silence threshold) if you need to fine-tune beyond the wizard.
3. Calibrate **USB capture gain** (RMS in logs) for reliable recognition — see
   [§1 Audio capture level](#1-audio-capture-level-required-for-track-recognition)
   below.

If you **cloned** this repository, you can also run
`sudo ./install-oceano-display.sh` — it is equivalent in behaviour to the display
options in `oceano-setup` and is not shipped in the `.deb` (handy for scripts/CI
from a checkout only).

### Option B — Install from source

Use this when you need to apply a specific branch or customise flags during
installation.

**Before running the installer**, make sure your USB DAC / amplifier is powered
on and connected via USB — the device only appears in the ALSA device list when
active.

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

This single command installs all services and dependencies (including Go,
`shairport-sync`, and `alsa-utils`):

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

Track identification (artist, title, album) for physical media (vinyl, CD) is
powered by [ACRCloud](https://www.acrcloud.com). Without credentials, `track`
will always be `null` for physical sources.

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

### 4. Listening metrics and boundary telemetry (optional)

Open **`http://<pi-ip>:8080/history.html`** (also linked from the main config page as **Metrics**). The same services and recognition logic run from the first boot; there is **no separate “learning phase”** you must enable. What starts empty is **historical data**: play history, recognition counters, and **VU boundary telemetry** only appear after you have actually played physical media (and gone through track changes or silence transitions the monitor can see).

On a **fresh install**, expect **low or zero counts** for the first hours or days. After a **service upgrade**, boundary rows are recorded from the **new process start onward** (events before the restart are not backfilled into the database). Totals become more useful after **a week or two** of normal listening if you want to compare suppression rates or tune calibration—but the system is fully operational before then.

---

## Update

### Debian package

```bash
wget https://github.com/alemser/oceano-player/releases/latest/download/oceano-player_$(curl -s https://api.github.com/repos/alemser/oceano-player/releases/latest | grep -oP '"tag_name":\s*"v\K[^"]+')_arm64.deb
sudo apt install ./oceano-player_*_arm64.deb
```

`/etc/oceano/config.json` is never overwritten on upgrade — your credentials and
device settings are preserved.

### From source

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

### HDMI/DSI: shows Pi desktop (labwc) or no Now Playing (not the kiosk)

On Raspberry Pi OS, `/etc/lightdm/lightdm.conf` can keep `user-session` / `autologin-session` set
to `rpd-labwc` (Wayland). That **overrides** a `lightdm.conf.d` drop-in, so the session you see is
`labwc`, not `oceano-kiosk` / X11. Re-run **`sudo oceano-setup`** and enable the **display** + **LightDM**
path (or run `install-oceano-display.sh` from a repo checkout). That updates the main `lightdm.conf`
as well. Then `sudo reboot`. To verify which session is active: `loginctl show-user "$(whoami)"` and
`loginctl show-session <n> -p Type -p Desktop` for the graphical seat session.

---

### Track recognition not working (`track: null` for physical source)

1. **ACRCloud credentials not configured** — the most common cause after a fresh install. Open `http://<pi-ip>:8080` → **Track Recognition** → fill in credentials → **Save & Restart Services**.

2. **RMS too high (> 0.40)** — clipping degrades recognition quality. Find your capture card number with `arecord -l`, then reduce the level:
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

5. **Library reuse by provider ID** — once a physical track is recognized, oceano-state-manager reuses saved metadata/artwork from the local library by either **ACRCloud ACRID** or **Shazam track ID**. This keeps user-edited metadata and artwork stable even when one provider misses and the other one matches.

---

### Source oscillating rapidly between Physical and None

The silence threshold is too close to the noise floor at the current capture volume. In the web UI under **Audio Input**, raise **Silence Threshold** to `0.025`.

### Legacy `streaming_usb_guard_enabled` in config

`advanced.streaming_usb_guard_enabled` was used by the removed Streaming USB Guard feature.
If this key still exists in `/etc/oceano/config.json` from older installs, it is ignored.

---

### Track info stays on screen after record is changed

The last recognized track is shown for 60 seconds after audio stops (configurable via `--idle-delay`). If the phono stage has residual hum keeping RMS above the threshold, the source stays `Physical` and the old track persists until the next recognition cycle.

Options:
- Raise **Silence Threshold** slightly in the web UI so phono hum is treated as silence
- The recognizer retries at every track boundary (silence → audio transition)

---

### Album art shows wrong album

Expected when playing a compilation or "Best Of". ACRCloud returns the best-known release for the recording it matched, which may be credited to "Various Artists" in music databases, causing the artwork lookup to fail.

---

## Configuration reference

All service parameters are managed through the web UI at `http://<pi-ip>:8080`.
The underlying config is stored at `/etc/oceano/config.json`.

**Recognition capture length:** `recognition.capture_duration_secs` is the seconds of audio written to each WAV for ACRCloud/Shazam (one capture per attempt). Saving the web UI regenerates `oceano-state-manager.service` with a matching `--recognizer-capture-duration` flag. The Go binary’s built-in default is the same value (7s) so ad-hoc runs without systemd flags match fresh `config.json` installs.

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
make test
# or: go test ./...
```

### Cross-compile for arm64

```bash
make build        # produces dist/oceano-{source-detector,state-manager,web}
make package      # produces dist/oceano-player_VERSION_arm64.deb  (requires nfpm)
make clean
```

Install `nfpm` once with `brew install nfpm` (macOS) or
`go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest`.

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
.