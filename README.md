## Oceano Player

Minimal “receiver-only” audio stack for **Raspberry Pi 5 → USB → Magnat MR 780**.

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

### What’s included today

- `cmd/oceano-player`: small daemon that reads `config.yaml` and runs `shairport-sync`
- `config.yaml`: default config tailored to Pi5 + USB amp DAC
- `install.sh` / `update.sh`: helper scripts for Pi setup

### Install (on the Pi)

You’ll do this on Raspberry Pi OS (64-bit recommended).

1. Install by cloning from git:

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/alemser/oceano-player/main/install.sh
chmod +x install.sh
sudo ./install.sh
```

2. Set your USB ALSA device:

```bash
aplay -l
sudo nano /opt/oceano-player/config.yaml
sudo systemctl restart oceano-player
```

3. Update later:

```bash
curl -fsSL -o update.sh https://raw.githubusercontent.com/alemser/oceano-player/main/update.sh
chmod +x update.sh
sudo ./update.sh
```

`install.sh` clones the repo into `/opt/oceano-player/src` and builds/deploys from there. `update.sh` pulls and rebuilds from that same directory and restarts the service.

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

