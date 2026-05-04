# Oceano Player

Audio backend for making the **Raspberry Pi** a piece of your HI-FI equipament.

---

## Companion clients and cross-repo sync

This repository is the **backend contract owner** for the Oceano ecosystem.

A separate iOS app repository, **`oceano-player-ios`**, depends directly on this project's API shape and behavior (for example: `/api/status`, `/api/stream`, `/api/config`, `/api/stylus`, `/api/history/stats`, `/api/recognition/*`, amplifier endpoints, and related JSON fields/semantics).

When backend behavior changes here, the iOS app will often require updates in lockstep.

**If you change any of the following, assume `oceano-player-ios` must be reviewed immediately:**

- endpoint paths, methods, response codes, or payload fields
- state semantics (source priority, idle/playing transitions, fallback behavior)
- config keys and default values persisted by `/api/config`
- stylus/recognition/history metrics fields and thresholds
- amplifier/topology/IR payload shape

Before shipping backend changes, run the short checklist in
[`docs/cross-repo-sync.md`](docs/cross-repo-sync.md).

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
2. Configure **`/etc/oceano/config.json`** via **`oceano-player-ios`** or `POST /api/config`
   (same fields as before): **Track Recognition** using **your own** API accounts
   ([BYOK](#third-party-recognition-byok); default path **ACRCloud**), plus **Audio Input**
   (device, silence threshold) if you need to fine-tune beyond the wizard.
3. Calibrate **USB capture gain** (RMS in logs) for reliable recognition — see
   [§1 Audio capture level](#1-audio-capture-level-required-for-track-recognition)
   below.

If you **cloned** this repository, you can also run
`sudo ./install-oceano-display.sh` — it is equivalent in behaviour to the display
options in `oceano-setup` and is not shipped in the `.deb` (handy for scripts/CI
from a checkout only). For **Bluetooth → DAC** routing when you have more than
one USB sound device, the full `sudo oceano-setup` run (ALSA **output** step) is
still recommended — it installs the PipeWire default-sink helper the display
script does not include.

### Resilience (what `sudo oceano-setup` applies for you)

Run it **after all USB devices are connected** (amplifier on, capture card, etc.),
and **again** if you add or reorder USB hardware. The wizard and the matching
`install.sh` path install safeguards so a stock Pi is usable without reading long
troubleshooting notes:

| Area | Handled by setup / installer |
|------|------------------------------|
| **Raspberry Pi OS + LightDM** | `oceano-kiosk` X11 session instead of Wayland `rpd-labwc`: `lightdm.conf.d/zz-…` **and** the **main** `/etc/lightdm/lightdm.conf` `user-session` / `autologin-session` (the main file overrides drop-ins; see [Troubleshooting](#troubleshooting)). |
| **AirPlay (shairport)** | **ALSA** output to the chosen DAC (the `shairport-sync` system user cannot use the login user’s PipeWire). `oceano-web` migrates legacy `output_backend=pa` on startup. **`avahi-daemon`** (mDNS/Bonjour) so iPhones can discover the receiver. |
| **Bluetooth audio** | Default **PipeWire sink** = same DAC (script + user systemd oneshot, **linger** so it can run at boot; BlueZ **codec** list for AAC/LDAC/… when WirePlumber ≥ 0.5). |
| **Multiple USB sound cards** | Warning in the wizard; `device_match` in `config.json` from the chosen `plughw:CARD=…`; do **not** point output at a capture dongle. |
| **Kiosk / HDMI** | No `xrandr --auto` in the launch script (avoids a **black** screen on some panels). Kiosk setup also writes anti-blanking defaults (`xset s off`, `xset -dpms`, `xset s noblank`) via `~/.xprofile` and launch-time guards. Use `/boot/firmware/config.txt` or `raspi-config` for HDMI mode if needed. |
| **Bluetooth discoverable** | `bluetoothctl` + `main.conf` after the Bluetooth step. |

**`oceano-web`** on port **8080** exposes **HTTP APIs** (including `GET /api/config` with optional
**`If-None-Match`** → **`304 Not Modified`**, `GET /api/player/summary` and **`GET /api/library`** with the same pattern,
**`GET /api/library/changes`**, `POST /api/config`, and **`/api/stream`** / **`/api/status`** where stereo **`vu`** meters are
**omitted by default** and enabled with **`?vu=1`**) and the **`/nowplaying.html`** local display; successful config writes
still rewrite systemd units and shairport when needed. LAN client contracts are summarized in
[`docs/reference/http-lightweight-clients.md`](docs/reference/http-lightweight-clients.md). Deeper failure modes (RMS, ACRCloud, …) are in [Troubleshooting](#troubleshooting) below.

### AirPlay DAC auto-return (silent fallback mode)

When `audio_output.device_match` is configured, `oceano-web` continuously reconciles
`shairport-sync` output with currently detected ALSA cards:

- If the matching USB DAC is present, output is pinned to `plughw:N,0`.
- If the DAC disappears (power off/unplug), output switches to ALSA `null`
  (silent sink) so AirPlay sessions can still connect reliably.
- As soon as the DAC returns, output is switched back automatically to `plughw:N,0`
  and `shairport-sync` is restarted.

This avoids “visible but fails to connect” behavior when a fixed DAC path is absent,
without routing audio to an unexpected physical default sink.

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
| `oceano-web` | HTTP API + `/nowplaying.html` at `http://<pi-ip>:8080` |

---

## First-time setup after install

After installation, use **`oceano-player-ios`**, `POST /api/config`, or **`sudo oceano-setup`**
to edit `/etc/oceano/config.json`. Open **`http://<pi-ip>:8080/nowplaying.html`** only for the
local HDMI/DSI display (optional).

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

### Third-party recognition (BYOK)

Optional track identification calls **external** recognition services using
**credentials you obtain** from each provider (bring your own key / account).
You are responsible for **their** developer terms, **pricing**, quotas, and
acceptable use. **Oceano Player only ships client integration**—it does not
resell API access, bundle unlimited third-party quota, or act as your contractual
counterparty with those services. For product positioning and compliance notes,
see [`docs/plans/recognition-master-plan.md`](docs/plans/recognition-master-plan.md)
(§ **Product & compliance** and **Third-party clarity: shazamio**).

**Optional `shazamio` path:** the installer sets up a Python venv with the community
**[`shazamio`](https://pypi.org/project/shazamio/)** package. That is **not** a
first-party **Shazam** developer API integration. It can break without notice and
carries **extra commercial / ToS risk** for sold products—prefer **documented**
providers (e.g. ACRCloud) for a default retail story. See the plan section
*Third-party clarity: shazamio*.

**Optional AudD:** [AudD](https://docs.audd.io/) is a **documented** REST API (BYOK token from [dashboard.audd.io](https://dashboard.audd.io/)). Configure `recognition.audd_api_token` and include **AudD** in `recognition.providers` (ordered list) when you want it in the chain. Short captures (~7–20 s WAV) are within their standard endpoint limits.

### 2. ACRCloud credentials (required for track recognition)

Track identification (artist, title, album) for physical media (vinyl, CD) is
powered by [ACRCloud](https://www.acrcloud.com) when you enable it under **Track
Recognition**. The state manager runs physical recognition **only** when
`recognition.providers` in `/etc/oceano/config.json` is a **non-empty** array with
at least one **enabled** primary provider that has credentials (or Shazamio
installed, if you enable that slot). The legacy `recognizer_chain` field is **not**
used to infer providers anymore — save once from the **iOS app** or include
`providers` explicitly in `POST /api/config`. Without credentials **and** without
a usable provider entry, `track` stays `null` for physical sources.

Set recognition credentials (iOS app or `POST /api/config`):
- **ACRCloud Host** — e.g. `identify-eu-west-1.acrcloud.com`
- **Access Key**
- **Secret Key**

After saving config, services restart automatically when fields that affect them change. Confirm recognition is active:
```bash
journalctl -u oceano-state-manager.service -f | grep recognizer
# should show: recognizer [ACRCloud]: capturing ...
```

### 3. Audio input device (if auto-detection fails)

Set the capture path after install: `sudo oceano-setup` or **`audio_input`** in `config.json` (via iOS / `POST /api/config`) — use **Auto-detect name** with a **substring** that appears in your card’s line in `/proc/asound/cards` (e.g. `Device`, `UAC2`), or an explicit `plughw:N,0` / `plughw:CARD=Name,DEV=0`. The default is not tied to a fixed product name.

To list available capture devices:
```bash
arecord -l
```

### 4. Listening metrics and boundary telemetry (optional)

Use **`GET /api/history/stats`**, **`GET /api/recognition/stats`**, **`GET /api/recognition/attempts`** (per-provider attempt log with capture RMS, latency, and error class), and related JSON APIs (or the iOS app) for **listening metrics**. The same services and recognition logic run from the first boot; there is **no separate “learning phase”** you must enable. What starts empty is **historical data**: play history, recognition counters, and **VU boundary telemetry** only appear after you have actually played physical media (and gone through track changes or silence transitions the monitor can see).

On a **fresh install**, expect **low or zero counts** for the first hours or days. After a **service upgrade**, boundary rows are recorded from the **new process start onward** (events before the restart are not backfilled into the database). Totals become more useful after **a week or two** of normal listening if you want to compare suppression rates or tune calibration—but the system is fully operational before then.

When you correct a library entry’s format (**Vinyl**, **CD**, or **Unknown**), existing **boundary telemetry** rows linked to that entry receive **`format_resolved`** / **`format_resolved_at`** so aggregates can favour the corrected label without rewriting historical **`format_at_event`** values.

After each **fired** boundary, the state manager records **post-recognition follow-up** columns on the same row (outcome, optional IDs, **early-boundary** cohort flag). **Listening Metrics** shows linked counts under the boundary summary when data exists.

**RMS percentile learning** (optional, `advanced.rms_percentile_learning` in `config.json`): while **Physical** is active, the state manager accumulates **stable-silence vs stable-music** RMS histograms per format into the library database table **`rms_learning`**. With **Autonomous apply** enabled, derived silence enter/exit can **replace** calibration OFF/ON thresholds for VU boundaries once enough samples exist. Disable learning or autonomous apply if you change capture gain or hardware.

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
    "artwork_path": "/tmp/oceano-artwork-a1b2c3d4.jpg",
    "discogs_url": "https://api.discogs.com/releases/12345"
  },
  "updated_at": "2026-03-29T20:30:05Z"
}
```

`source` values: `AirPlay` | `Physical` | `None`

`track` is `null` when source is `Physical` and no recognition result is available yet, or when source is `None`.

`track.discogs_url` is optional and appears only when Discogs enrichment is enabled and a confident release match is found.

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

- **mDNS / Avahi missing (very common on minimal installs)** — iOS uses Bonjour to find AirPlay. You need **`avahi-daemon` running**:
  ```bash
  sudo apt install -y avahi-daemon
  sudo systemctl enable --now avahi-daemon.service
  avahi-browse -a 2>/dev/null | head   # or: avahi-browse -t _raop._tcp  (if avahi-utils installed)
  ```
  Installs with `sudo oceano-setup` and current `.deb` / `install.sh` add this; older installs: install Avahi, then `sudo systemctl restart shairport-sync`.

- **iPhone and Pi on the same L2 / Wi-Fi** — guest networks and **AP/client isolation** block mDNS. Put both on the same LAN, disable “isolate clients” for that SSID, or test Ethernet on the Pi.

0. **shairport-sync is `failed` (PulseAudio / “Connection refused”)** — the service runs as
   `shairport-sync` and **cannot** use the logged-in user’s PipeWire. Current releases use the
   **ALSA** backend to your DAC. Open the web config and **Save** (or re-run
   `sudo oceano-setup` so it rewrites `shairport-sync.conf`), then
   `sudo systemctl restart oceano-web shairport-sync` and check
   `journalctl -u shairport-sync -n 20 --no-pager`. **Fresh `.deb` or newer `oceano-web`**
   migrates a legacy `output_backend = "pa"` on startup automatically.

1. **Amplifier input not set to USB** — shairport-sync needs the DAC to be present at startup. Set the input to USB and re-run `sudo ./install.sh`.

2. **AirPlay connects but no sound while DAC is off** — expected with silent fallback mode.
   With `audio_output.device_match`, Oceano intentionally routes to ALSA `null` when
   the matched DAC is missing, then auto-returns to `plughw:N,0` once the DAC is detected.
   Keep the DAC powered if you want immediate audible playback.

3. **shairport-sync not running**:
   ```bash
   sudo systemctl status shairport-sync.service
   journalctl -u shairport-sync.service -n 30 --no-pager
   ```

4. **Loopback module not loaded**:
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

### HDMI: wrong or stretched resolution (kiosk or desktop)

Oceano only opens a full-screen **browser**; it does not set the **video mode**. If the image is
letterboxed, scaled wrong, or 4:3 on a 16:9 TV, set the mode on the Pi side:

- **`sudo raspi-config` → Display Options** (or the Display menu on your OS image) — pick a mode that
  matches the panel, then reboot.
- **Manual:** edit [`/boot/firmware/config.txt`](https://www.raspberrypi.com/documentation/computers/config-txt.html#video-options)
  (older images: `/boot/config.txt`) — e.g. `hdmi_group`, `hdmi_mode`, or `hdmi_cvt` for custom timings.
- After a valid mode, **X11 and Chromium** follow; avoid forcing `xrandr` in startup scripts (can black
  some panels — see the kiosk/HDMI table above).

### HDMI/DSI: screen goes black after a few minutes

If the panel works on boot and later goes black, this is usually X11 screen blanking / DPMS.
Current `oceano-setup` and `install-oceano-display.sh` disable both automatically (`xset s off`,
`xset -dpms`, `xset s noblank`) via `~/.xprofile` and launch-time guards.

To verify on the Pi:

```bash
DISPLAY=:0 XAUTHORITY=~/.Xauthority xset -q
```

Expected: `DPMS is Disabled` and `timeout: 0`.

---

### Track recognition not working (`track: null` for physical source)

1. **Missing `recognition.providers` or empty array** — after upgrades, open **Physical Media** in the iOS app and **Save** so a non-empty `providers` list is written, or add the array by hand (see `docs/reference/recognition.md`). Credentials alone are not enough.
2. **ACRCloud credentials not configured** — set **`recognition`** fields in **`/etc/oceano/config.json`** (iOS app or `POST /api/config`) and ensure services restarted.

3. **RMS too high (> 0.40)** — clipping degrades recognition quality. Find your capture card number with `arecord -l`, then reduce the level:
   ```bash
   amixer -c N sset 'Mic' 50%   # replace N with your card number
   alsactl store
   ```
   Target: **RMS ≈ 0.15–0.20** during normal playback.

4. **Source detector showing `None`** — the capture card may not be detected:
   ```bash
   journalctl -u oceano-source-detector.service -f
   # look for: heartbeat: source=Physical rms=X
   # if source=None while music is playing, check audio_input.device_match / device in config.json
   ```

5. **Network unreachable (IPv6)** — ACRCloud client forces IPv4. Confirm connectivity:
   ```bash
   curl -4 https://identify-eu-west-1.acrcloud.com
   ```

6. **Library reuse by provider ID** — once a physical track is recognized, oceano-state-manager reuses saved metadata/artwork from the local library by either **ACRCloud ACRID** or the **track id** returned by the optional **`shazamio`** integration (stored as `shazam_id` in the library). This keeps user-edited metadata and artwork stable even when one provider misses and the other one matches.

---

### Source oscillating rapidly between Physical and None

The silence threshold is too close to the noise floor at the current capture volume. Raise **`audio_input.silence_threshold`** in `config.json` (e.g. to `0.025`).

### Legacy `streaming_usb_guard_enabled` in config

`advanced.streaming_usb_guard_enabled` was used by the removed Streaming USB Guard feature.
If this key still exists in `/etc/oceano/config.json` from older installs, it is ignored.

---

### Track info stays on screen after record is changed

The last recognized track is shown for 60 seconds after audio stops (configurable via `--idle-delay`). If the phono stage has residual hum keeping RMS above the threshold, the source stays `Physical` and the old track persists until the next recognition cycle.

Options:
- Raise **`audio_input.silence_threshold`** slightly in `config.json` so phono hum is treated as silence
- The recognizer retries at every track boundary (silence → audio transition)

---

### Album art shows wrong album

Expected when playing a compilation or "Best Of". ACRCloud returns the best-known release for the recording it matched, which may be credited to "Various Artists" in music databases, causing the artwork lookup to fail.

---

## Configuration reference

All service parameters live in **`/etc/oceano/config.json`**, edited with **`oceano-player-ios`**,
`POST /api/config`, or **`sudo oceano-setup`** (wizard). **`oceano-web`** applies changes that
require systemd or shairport updates when config is saved via the API.

**Recognition capture length:** `recognition.capture_duration_secs` is the seconds of audio written to each WAV for ACRCloud and the optional **`shazamio`** path (one capture per attempt). A config save that touches recognition regenerates `oceano-state-manager.service` with a matching `--recognizer-capture-duration` flag. The Go binary’s built-in default is the same value (7s) so ad-hoc runs without systemd flags match fresh `config.json` installs.

**Discogs enrichment (optional):** `recognition.discogs` controls post-recognition metadata enrichment (disabled by default). Keys:
- `enabled` (bool)
- `token` (BYOK Discogs token)
- `timeout_secs` (default `6`)
- `max_retries` (default `2`)
- `cache_ttl_hours` (default `72`, reserved for cache/backfill stages)

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

After reinstall, ACRCloud credentials are preserved if `/etc/oceano/config.json` was not deleted. If it was, re-configure credentials via the iOS app or `POST /api/config` and re-run `amixer` to set the capture level.

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
- Local media library — SQLite cache of recognized tracks enriched with MusicBrainz metadata and Cover Art Archive artwork; editable via library HTTP APIs / iOS
.