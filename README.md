# Oceano Player

Minimal "receiver-only" audio stack for **Raspberry Pi 5 → USB → Magnat MR 780**.

## Goals

- **Minimal**: no UI required
- **Receiver focused**: AirPlay first, then UPnP + Bluetooth
- **Config-driven**: works out-of-box for your hardware, others bring their own config
- **Integrates with your SPI "now playing"**: plug your existing screen app into the same playback events later

## Why this approach

**AirPlay** support is best handled by the battle-tested `shairport-sync` daemon (widely used in audiophile Pi setups). Oceano Player stays small: it just **configures, launches, and supervises** protocol daemons (starting with AirPlay) and can later expose a tiny local API for your SPI now-playing program.

## Language choice

This repo uses **Go** for the controller daemon because it is:

- **Simple to deploy** (single static binary for ARM64)
- **Low overhead** (good for always-on services)
- **Great with systemd and process supervision**

Rust would also be an excellent choice. Python is fine too, but packaging and long-term service management tends to be more fiddly on appliance-style systems.

## What's included today

- `install.sh`: single script that handles both **first-time install** and **updates** — auto-detected based on existing state
- (legacy/optional) `cmd/oceano-player`: original wrapper daemon, no longer required for default install

## Install (on the Pi)

You'll do this on Raspberry Pi OS (64-bit recommended).

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

### 2. Verify service

```bash
sudo systemctl status shairport-sync.service
journalctl -u shairport-sync.service -f
```

### 3. Update later

Re-running `install.sh` on an already-configured system automatically runs in **update mode** — it pulls the latest code, re-applies configuration, and restarts services:

```bash
sudo ./install.sh
```

Or re-download and run in one go:

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

> ⚠️ The script will display a warning when running on a branch other than `main`. Do not use development branches in production without testing.

## Change configuration (easy mode)

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

**Tips:**
- Set `ALSA_DEVICE` explicitly for the most stable output.
- The script auto-sets a compatible ALSA `mixer_device` when using `plughw`.
- `PREPLAY_WAIT_SECONDS` lets AirPlay wait briefly for DAC/amp wake-up from standby before playback starts.
- `OUTPUT_STRATEGY="loopback"` keeps AirPlay connected to a virtual sink while the real DAC is unavailable. A background watchdog automatically reconnects the audio stream when the DAC wakes up. **Recommended for equipment with standby modes.**
- `OUTPUT_STRATEGY="direct"` outputs directly to the DAC with no standby resilience.

## Custom overrides

Pass options directly to `install.sh` to override config values without editing the file:

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

All options:

| Option | Default | Description |
|---|---|---|
| `--branch` | `main` | Git branch to install/update |
| `--airplay-name` | `Triangle AirPlay` | AirPlay receiver name |
| `--usb-match` | `M780` | Text to match USB DAC in ALSA device list |
| `--alsa-device` | *(auto-detected)* | Explicit ALSA device string |
| `--preplay-wait-seconds` | `8` | Seconds to wait for DAC wake-up before playback |
| `--output-strategy` | `loopback` | `loopback` or `direct` |

## Clean reinstall

To reset everything and start from scratch:

```bash
sudo systemctl disable --now shairport-sync.service 2>/dev/null || true
sudo systemctl disable --now oceano-airplay-bridge.service 2>/dev/null || true
sudo systemctl disable --now oceano-bridge-watchdog.service 2>/dev/null || true
sudo rm -f /etc/systemd/system/oceano-player.service
sudo rm -rf /opt/oceano-player
sudo systemctl daemon-reload

curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

## Notes

- This project defaults to **system `shairport-sync` as single owner**.
- Legacy `oceano-player.service` wrapper is kept in the repo for reference but not used by default.
- In `loopback` strategy:
  - `shairport-sync` plays to ALSA loopback (`plughw:CARD=Loopback,DEV=0`)
  - `oceano-airplay-bridge.service` forwards audio to the real DAC when available
  - `oceano-bridge-watchdog.service` monitors the DAC every 10 seconds and automatically restarts the bridge when the DAC comes back from standby

## Developer checks

To block pushes unless checks pass:

```bash
chmod +x scripts/test.sh .githooks/pre-push
git config core.hooksPath .githooks
```

Now every `git push` runs:

- Shell syntax checks for `install.sh`
- `go test ./...`

Manual run:

```bash
./scripts/test.sh
```

## Next steps

- Add **AirPlay 2 validation** + recommended `shairport-sync` config path for distros where CLI flags differ
- Add future protocol managers:
  - **UPnP/OpenHome** (`upmpdcli` / `gmrender-resurrect`)
  - **Bluetooth receiver** (BlueZ + `bluealsa`/`pipewire`)
- Event output for SPI app (JSON over a UNIX socket or HTTP)