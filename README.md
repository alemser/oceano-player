# Oceano Player

Minimal "receiver-only" audio stack for **Raspberry Pi 5 → USB → Magnat MR 780**.

## Goals

- **Minimal**: no UI required
- **Receiver focused**: AirPlay first, then UPnP + Bluetooth
- **Config-driven**: works out-of-box for your hardware, others bring their own config
- **Integrates with your SPI "now playing"**: plug your existing screen app into the same playback events later

## How it works

Oceano Player is a **bash installer** that configures and supervises a set of systemd services around `shairport-sync`. There is no custom daemon — systemd handles process supervision, restarts, and boot behaviour directly.

In `loopback` mode (default), three services are installed:

| Service | Role |
|---|---|
| `shairport-sync.service` | AirPlay receiver |
| `oceano-airplay-bridge.service` | Forwards audio from loopback virtual device to real DAC |
| `oceano-bridge-watchdog.service` | Monitors DAC every 10s, restarts bridge when DAC wakes from standby |

In `direct` mode, only `shairport-sync.service` is installed.

## Why this approach

**AirPlay** support is best handled by the battle-tested `shairport-sync` daemon (widely used in audiophile Pi setups). Oceano Player stays small: it just **configures, launches, and supervises** protocol daemons via systemd, starting with AirPlay.

## Install (on the Pi)

Raspberry Pi OS 64-bit recommended.

### 1. Install

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

This configures:

- AirPlay name: `Triangle AirPlay`
- USB target match: `M780` (auto-detected from ALSA devices)
- Metadata pipe: `/tmp/shairport-sync-metadata`
- Persistent config file: `/opt/oceano-player/config.env`

### 2. Verify

```bash
sudo systemctl status shairport-sync.service
journalctl -u shairport-sync.service -f
```

### 3. Update

Re-running `install.sh` on an already-configured system automatically runs in **update mode** — pulls the latest code, re-applies configuration, and restarts services:

```bash
sudo ./install.sh
```

Or re-download and run:

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

### 4. Test a development branch

Use `--branch` to deploy a specific branch. If omitted, `main` is always used:

```bash
sudo ./install.sh --branch fix-disconnection
```

You can combine it with other options:

```bash
sudo ./install.sh --branch fix-disconnection --output-strategy loopback --preplay-wait-seconds 8
```

> ⚠️ The script displays a warning when running on a branch other than `main`. Do not use development branches in production without testing.

## Configuration

### Easy mode

Edit the config file and re-run:

```bash
sudo nano /opt/oceano-player/config.env
sudo ./install.sh
```

`/opt/oceano-player/config.env` format:

```bash
AIRPLAY_NAME="Triangle AirPlay"
USB_MATCH="M780"
ALSA_DEVICE="plughw:CARD=M780,DEV=0"
PREPLAY_WAIT_SECONDS="8"
OUTPUT_STRATEGY="loopback"
```

### Options reference

| Option | Default | Description |
|---|---|---|
| `--branch` | `main` | Git branch to install/update |
| `--airplay-name` | `Triangle AirPlay` | AirPlay receiver name |
| `--usb-match` | `M780` | Text to match USB DAC in ALSA device list |
| `--alsa-device` | *(auto-detected)* | Explicit ALSA device string |
| `--preplay-wait-seconds` | `8` | Seconds to wait for DAC wake-up before playback |
| `--output-strategy` | `loopback` | `loopback` or `direct` |

### Output strategy

- **`loopback`** *(recommended)* — `shairport-sync` plays to a virtual ALSA loopback sink. The bridge service forwards audio to the real DAC when available, and the watchdog automatically reconnects when the DAC wakes from standby. Best for equipment with standby modes.
- **`direct`** — outputs directly to the DAC. Simpler, but no standby resilience.

### Tips

- Set `ALSA_DEVICE` explicitly for the most stable output.
- The script auto-sets a compatible ALSA `mixer_device` when using `plughw`.
- `PREPLAY_WAIT_SECONDS` lets AirPlay wait briefly for DAC/amp wake-up from standby before playback starts.

## Custom overrides

Pass options directly to override config without editing the file:

```bash
# Set AirPlay name and ALSA device explicitly
sudo ./install.sh --airplay-name "Triangle AirPlay" --alsa-device "plughw:CARD=M780,DEV=0"

# Increase wait time for DAC/amp standby wake-up
sudo ./install.sh --preplay-wait-seconds 12

# Use loopback mode (recommended for standby-capable equipment)
sudo ./install.sh --output-strategy loopback

# Use direct output (no loopback bridging)
sudo ./install.sh --output-strategy direct

# Override USB auto-detection match string
sudo ./install.sh --usb-match "M780"
```

## Clean reinstall

```bash
sudo systemctl disable --now shairport-sync.service 2>/dev/null || true
sudo systemctl disable --now oceano-airplay-bridge.service 2>/dev/null || true
sudo systemctl disable --now oceano-bridge-watchdog.service 2>/dev/null || true
sudo rm -rf /opt/oceano-player
sudo systemctl daemon-reload

curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

## Developer checks

To block pushes unless checks pass:

```bash
chmod +x scripts/test.sh .githooks/pre-push
git config core.hooksPath .githooks
```

Now every `git push` runs a shell syntax check on `install.sh`.

Manual run:

```bash
./scripts/test.sh
```

## Next steps

- Add **AirPlay 2 validation** + recommended `shairport-sync` config path for distros where CLI flags differ
- Add future protocol managers:
  - **UPnP/OpenHome** (`upmpdcli` / `gmrender-resurrect`)
  - **Bluetooth receiver** (BlueZ + `bluealsa` / `pipewire`)
- Event output for SPI now-playing app (JSON over a UNIX socket or HTTP)