## Oceano Player

Minimal “receiver-only” audio stack for **Raspberry Pi 5 -> USB -> Magnat MR 780**.

### Goals

- **Minimal**: no UI required
- **Receiver focused**: AirPlay first, then UPnP + Bluetooth
- **Config-driven**: works out-of-box for your hardware, others bring their own config
- **Integrates with your SPI “now playing”**: you can plug your existing screen app into the same playback events later

### Why this approach

- **AirPlay** support is best handled by the battle-tested `shairport-sync` daemon (widely used in audiophile Pi setups).
- Oceano Player stays small: it just **configures/launches/supervises** protocol daemons (starting with AirPlay) and later can expose a tiny local API for your SPI now-playing program.

### Language choice

This repo uses **Go** for the controller daemon because it is:

- **Simple to deploy** (single static-ish binary for ARM64)
- **Low overhead** (good for always-on services)
- **Great with systemd and process supervision**

Rust would also be an excellent choice. Python is fine too, but packaging and long-term service management tends to be more fiddly on appliance-style systems.

### What's included today

- `install.sh` / `update.sh`: plug-and-play scripts around system `shairport-sync`
- `oceano-analog-identify.service`: optional analog input identifier that writes
  now-playing snapshots to `/run/oceano-player/analog-now-playing.json`
- (legacy/optional) `cmd/oceano-player`: original wrapper daemon, no longer required for default install

### Install (on the Pi)

You’ll do this on Raspberry Pi OS (64-bit recommended).

1. Install (plug-and-play):

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

This configures:

- AirPlay name: `Triangle AirPlay`
- USB target match: `M780` (auto-detected from ALSA devices)
- metadata pipe: `/tmp/shairport-sync-metadata`
- analog snapshot file: `/run/oceano-player/analog-now-playing.json`
- persistent user config file: `/opt/oceano-player/config.env`

2. Verify service:

```bash
sudo systemctl status shairport-sync.service
journalctl -u shairport-sync.service -f
```

3. Update later:

```bash
curl -fsSL -o update.sh https://raw.githubusercontent.com/alemser/oceano-player/main/update.sh
chmod +x update.sh
sudo ./update.sh
```

`install.sh` and `update.sh` both use source in `/opt/oceano-player/src` (git clone/pull), then apply the same `shairport-sync` configuration.

If you want a single command to deploy a branch on Raspberry Pi, use:

```bash
sudo ./update-pr.sh --branch deadling-with-disconection
```

Important: if your local scripts are older and do not show newer options in
`--help`, switch branch manually first, then run `update.sh`:

```bash
cd /opt/oceano-player/src
git fetch origin
git checkout analog-source
git pull --ff-only origin analog-source
sudo chmod +x ./update.sh ./update-pr.sh
```

You can pass update options through it as well:

```bash
sudo ./update-pr.sh --branch deadling-with-disconection --output-strategy loopback --preplay-wait-seconds 8
```

### Change configuration (easy mode)

Edit one file and apply:

```bash
sudo nano /opt/oceano-player/config.env
sudo ./update.sh
```

`/opt/oceano-player/config.env` uses:

```bash
AIRPLAY_NAME="Triangle AirPlay"
USB_MATCH="M780"
ALSA_DEVICE="plughw:CARD=M780,DEV=0"
PREPLAY_WAIT_SECONDS="8"
OUTPUT_STRATEGY="loopback"
ANALOG_INPUT_ENABLED="true"
ANALOG_INPUT_DEVICE="plughw:CARD=USBADC,DEV=0"
ANALOG_IDENTIFY_INTERVAL_SECONDS="45"
ANALOG_METADATA_FILE="/run/oceano-player/analog-now-playing.json"
```

Sensitive credentials are stored separately in a root-only profile file:

```bash
/opt/oceano-player/.oceano-player
```

This file is loaded by `oceano-analog-identify.service` and created with `0600`
permissions.

Tip: set `ALSA_DEVICE` explicitly for stable AirPlay output and `ANALOG_INPUT_DEVICE` explicitly for stable analog capture.
The scripts also auto-set a compatible ALSA `mixer_device` when using `plughw`.
`PREPLAY_WAIT_SECONDS` lets AirPlay wait briefly for DAC/amp wake-up from standby before playback starts.
- `OUTPUT_STRATEGY="loopback"` keeps AirPlay connected to a virtual sink while the real DAC is unavailable. When the DAC comes back from standby, a background watchdog automatically reconnects the audio stream. This is the **recommended setting** for equipment with standby modes.
- `OUTPUT_STRATEGY="direct"` disables loopback bridging and outputs directly to the DAC (no standby resilience). On some ALSA builds/devices this may be less tolerant than loopback mode.
- `ANALOG_INPUT_ENABLED="true"` runs analog input identification as a separate source extension.
- Set the AcoustID key with `--acoustid-api-key` so it is stored in `/opt/oceano-player/.oceano-player`.

### Clean reinstall

If you want to reset and install from scratch:

```bash
sudo systemctl disable --now oceano-player.service 2>/dev/null || true
sudo systemctl disable --now shairport-sync.service 2>/dev/null || true
sudo rm -f /etc/systemd/system/oceano-player.service
sudo rm -rf /opt/oceano-player
sudo systemctl daemon-reload

curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

### Custom overrides

If device detection fails, set values explicitly:

```bash
sudo ./install.sh --airplay-name "Triangle AirPlay" --alsa-device "plughw:CARD=M780,DEV=0"
sudo ./update.sh --airplay-name "Triangle AirPlay" --alsa-device "plughw:CARD=M780,DEV=0"

# Explicit capture device for analog identification:
sudo ./update.sh --analog-input-device "plughw:CARD=USBADC,DEV=0"

# Helpful when DAC/amp standby causes first-try connection drop:
sudo ./update.sh --preplay-wait-seconds 12

# Keep AirPlay always available; bridge audio to DAC only when DAC is awake:
sudo ./update.sh --output-strategy loopback

# Disable loopback bridging and output directly to DAC:
sudo ./update.sh --output-strategy direct

# If direct mode causes immediate disconnects, revert quickly to loopback:
sudo ./update.sh --output-strategy loopback --preplay-wait-seconds 0

# Disable analog identification service:
sudo ./update.sh --analog-input-enabled false

# Increase analog fingerprint interval to 90 seconds:
sudo ./update.sh --analog-identify-interval-seconds 90

# Store/update AcoustID key securely (root-only profile file):
sudo ./update.sh --acoustid-api-key "0cAcPUvHVU"
```

Or keep auto ALSA selection but change match text:

```bash
sudo ./install.sh --usb-match "M780"
```

### Notes

- This project now defaults to **system `shairport-sync` as single owner**.
- Legacy `oceano-player.service` wrapper is kept in the repo for reference, but not used by default install/update.
- In `loopback` strategy:
  - `shairport-sync` plays to ALSA loopback (`plughw:CARD=Loopback,DEV=0`)
  - A companion bridge service (`oceano-airplay-bridge.service`) forwards audio to the real DAC when available
  - A watchdog service (`oceano-bridge-watchdog.service`) monitors the DAC every 10 seconds and automatically restarts the bridge when the DAC comes back from standby
  - This ensures uninterrupted AirPlay playback and automatic audio re-routing when equipment wakes up

### Troubleshooting

Common issues observed on Raspberry Pi deployments and verified fixes:

1. `Unknown argument: --analog-input-enabled` (or missing newer options)

Cause: stale `update.sh` / `update-pr.sh` on the device.

Fix:

```bash
cd /opt/oceano-player/src
git fetch origin
git checkout analog-source
git pull --ff-only origin analog-source
sudo chmod +x ./update.sh ./update-pr.sh
```

2. `sudo: ./update.sh: command not found`

Cause: script lacks executable bit.

Fix:

```bash
sudo chmod +x ./update.sh
```

3. AirPlay connects then disconnects immediately

Recommended recovery path:

```bash
sudo ./update.sh \
  --analog-input-enabled false \
  --output-strategy loopback \
  --alsa-device "plughw:CARD=M780,DEV=0" \
  --preplay-wait-seconds 0
```

Why: loopback mode is generally the most stable path with standby-sensitive DAC/amp setups.

4. `unable to listen on ... port 5000` / `Address already in use`

Cause: more than one `shairport-sync` instance running (systemd unit + manual debug process).

Fix:

```bash
sudo systemctl stop shairport-sync.service
sudo pkill -f shairport-sync || true
sudo ss -ltnp | grep ':5000' || echo "port 5000 free"
```

Then run exactly one instance (either systemd service or manual `-vv`, not both).

5. `Unit oceano-analog-identify.service could not be found`

Expected when `ANALOG_INPUT_ENABLED=false`. In this mode, analog unit creation is intentionally skipped.

6. `Missing required command: fpcalc`

Cause: `fpcalc` is not installed. Package name can differ by distro release.

On Raspberry Pi OS / Debian trixie, install:

```bash
sudo apt-get update
sudo apt-get install -y libchromaprint-tools
```

Check:

```bash
which fpcalc
fpcalc -version
```

If package lookup differs on your image:

```bash
apt-cache search chromaprint
apt-cache search fpcalc
```

7. Verify generated preplay hook line after updates

```bash
grep -n "run_this_before_play_begins" /etc/shairport-sync.conf
```

Expected format:

```text
run_this_before_play_begins = "/usr/local/bin/oceano-airplay-preplay-wait.sh plughw:CARD=M780,DEV=0 0";
```

### Analog metadata snapshot

When enabled, `oceano-analog-identify.service` (Go binary) captures USB analog input,
attempts song recognition with `fpcalc` + AcoustID, and writes the latest state
to:

```text
/run/oceano-player/analog-now-playing.json
```

The snapshot is atomically replaced and intended for external consumers such as
`oceano-now-playing` configured with `MEDIA_PLAYER=analog_file`.

### Fingerprint test (AcoustID)

You can validate lookup immediately using the official AcoustID example
fingerprint (M83 sample):

```bash
curl -G "https://api.acoustid.org/v2/lookup" \
  --data-urlencode "client=0cAcPUvHVU" \
  --data-urlencode "meta=recordings+releasegroups+releases" \
  --data-urlencode "duration=641" \
  --data-urlencode "fingerprint=AQABz0qUkZK4oOfhL-CPc4e5C_wW2H2QH9uDL4cvoT8UNQ-eHtsE8cceeFJx-LiiHT-aPzhxoc-Opj_eI5d2hOFyMJRzfDk-QSsu7fBxqZDMHcfxPfDIoPWxv9C1o3yg44d_3Df2GJaUQeeR-cb2HfaPNsdxHj2PJnpwPMN3aPcEMzd-_MeB_Ej4D_CLP8ghHjkJv_jh_UDuQ8xnILwunPg6hF2R8HgzvLhxHVYP_ziJX0eKPnIE1UePMByDJyg7wz_6yELsB8n4oDmDa0Gv40hf6D3CE3_wH6HFaxCPUD9-hNeF5MfWEP3SCGym4-SxnXiGs0mRjEXD6fgl4LmKWrSChzzC33ge9PB3otyJMk-IVC6R8MTNwD9qKQ_CC8kPv4THzEGZS8GPI3x0iGVUxC1hRSizC5VzoamYDi-uR7iKPhGSI82PkiWeB_eHijvsaIWfBCWH5AjjCfVxZ1TQ3CvCTclGnEMfHbnZFA8pjD6KXwd__Cn-Y8e_I9cq6CR-4S9KLXqQcsxxoWh3eMxiHI6TIzyPv0M43YHz4yte-Cv-4D16Hv9F9C9SPUdyGtZRHV-OHEeeGD--BKcjVLOK_NCDXMfx44dzHEiOZ0Z44Rf6DH5R3uiPj4d_PKolJNyRJzyu4_CTD2WOvzjKH9GPb4cUP1Av9EuQd8fGCFee4JlRHi18xQh96NLxkCgfWFKOH6WGeoe4I3za4c5hTscTPEZTES1x8kE-9MQPjT8a8gh5fPgQZtqCFj9MDvp6fDx6NCd07bjx7MLR9AhtnFnQ70GjOcV0opmm4zpY3SOa7HiwdTtyHa6NC4e-HN-OfC5-OP_gLe2QDxfUCz_0w9l65HiPAz9-IaGOUA7-4MZ5CWFOlIfe4yUa6AiZGxf6w0fFxsjTOdC6Itbh4mGD63iPH9-RFy909XAMj7mC5_BvlDyO6kGTZKJxHUd4NDwuZUffw_5RMsde5CWkJAgXnDReNEaP6DTOQ65yaD88HoeX8fge-DSeHo9Qa8cTHc80I-_RoHxx_UHeBxrJw62Q34Kd7MEfpCcu6BLeB1ePw6OO4sOF_sHhmB504WWDZiEu8sKPpkcfCT9xfej0o0lr4T5yNJeOvjmu40w-TDmqHXmYgfFhFy_M7tD1o0cO_B2ms2j-ACEEQgQgAIwzTgAGmBIKIImNQAABwgQATAlhDGCCEIGIIM4BaBgwQBogEBIOESEIA8ARI5xAhxEFmAGAMCKAURKQQpQzRAAkCCBQEAKkQYIYIQQxCixCDADCABMAE0gpJIgyxhEDiCKCCIGAEIgJIQByAhFgGACCACMRQEyBAoxQiHiCBCFOECQFAIgAABR2QAgFjCDMA0AUMIoAIMChQghChASGEGeYEAIAIhgBSErnJPPEGWYAMgw05AhiiGHiBBBGGSCQcQgwRYJwhDDhgCSCSSEIQYwILoyAjAIigBFEUQK8gAYAQ5BCAAjkjCCAEEMZAUQAZQCjCCkpCgFMCCiIcVIAZZgilAQAiSHQECOcQAQIc4QClAHAjDDGkAGAMUoBgyhihgEChFCAAWEIEYwIJYwViAAlHCBIGEIEAEIQAoBwwgwiEBAEEEOoEwBY4wRwxAhBgAcKAESIQAwwIowRFhoBhAE"
```

Expected outcome includes `"status":"ok"` and M83 recording metadata.

### Developer checks

To block pushes unless checks pass:

```bash
chmod +x scripts/test.sh .githooks/pre-push
git config core.hooksPath .githooks
```

Now every `git push` runs:

- shell syntax checks for `install.sh` and `update.sh`
- `go test ./...`

Manual run:

```bash
./scripts/test.sh
```

### Next steps

- **Systemd unit + installer** (so it starts on boot)
- Add **AirPlay 2 validation** + recommended `shairport-sync` config path for distros where CLI flags differ
- Add future protocol managers:
  - **UPnP/OpenHome** (likely `upmpdcli` / `gmrender-resurrect` ecosystem, depending on goals)
  - **Bluetooth receiver** (BlueZ + `bluealsa`/`pipewire` options)
- Event output for your SPI app (simple JSON over a UNIX socket or HTTP)

