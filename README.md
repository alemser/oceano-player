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
- `docs/spi-now-playing-integration.md`: integration notes for now-playing data/artwork
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
- USB target match: `MR 780` (auto-detected from `aplay -l`)
- metadata pipe: `/tmp/shairport-sync-metadata` (for future SPI now playing integration)

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
sudo ./install.sh --airplay-name "Triangle AirPlay" --alsa-device "hw:1,0"
sudo ./update.sh --airplay-name "Triangle AirPlay" --alsa-device "hw:1,0"
```

Or keep auto ALSA selection but change match text:

```bash
sudo ./install.sh --usb-match "MR 780"
```

### Manual install (optional)

1. Install AirPlay daemon:

```bash
sudo apt update
sudo apt install -y shairport-sync
```

2. Identify your USB ALSA device:

```bash
aplay -l
```

Update `audio.alsa_device` in `config.yaml` (example: `hw:1,0`).

3. Build and run:

```bash
go build -o bin/oceano-player ./cmd/oceano-player
./bin/oceano-player -config ./config.yaml
```

### Next steps

- **Systemd unit + installer** (so it starts on boot)
- Add **AirPlay 2 validation** + recommended `shairport-sync` config path for distros where CLI flags differ
- Add future protocol managers:
  - **UPnP/OpenHome** (likely `upmpdcli` / `gmrender-resurrect` ecosystem, depending on goals)
  - **Bluetooth receiver** (BlueZ + `bluealsa`/`pipewire` options)
- Event output for your SPI app (simple JSON over a UNIX socket or HTTP)

