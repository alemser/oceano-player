# Feature: Debian Package + GitHub Actions CI/CD

## Goal

Replace the "compile-on-Pi" install scripts with a single `.deb` that the user installs with
`sudo apt install ./oceano-player_*.deb`. GitHub Actions cross-compiles for `arm64` and publishes
the package to GitHub Releases on every git tag.

The shell scripts remain intact for local development. The `.deb` is a parallel distribution
channel, not a replacement.

---

## User experience after this feature

```bash
# First install
wget https://github.com/alemser/oceano-player/releases/latest/download/oceano-player_1.x.x_arm64.deb
sudo apt install ./oceano-player_1.x.x_arm64.deb
# Open http://<pi-ip>:8080 → configure ACRCloud keys + audio devices → Save

# Upgrade
wget https://github.com/alemser/oceano-player/releases/latest/download/oceano-player_1.x.x_arm64.deb
sudo apt install ./oceano-player_1.x.x_arm64.deb   # upgrades in-place, keeps /etc/oceano/config.json

# Remove
sudo apt remove oceano-player
sudo apt purge oceano-player   # also removes /etc/oceano and /var/lib/oceano
```

---

## What the package handles vs. what stays manual

| Task | How |
|------|-----|
| Install Go binaries | Package contents |
| Install systemd units | Package contents (enabled by postinst) |
| Install broadlink Python script | Package contents |
| Create `/etc/oceano`, `/var/lib/oceano/artwork` | postinst |
| Write default `/etc/oceano/config.json` | postinst (only if file does not exist — preserves upgrades) |
| Set up Python venv for broadlink | postinst |
| Set up Python venv for Shazam | postinst (if `shazamio` optional dep is desired) |
| AirPlay name, ACRCloud keys, ALSA device | Web UI at `:8080` after install |
| `shairport-sync.conf` generation | `oceano-setup` helper script (see Phase 3) |
| Bluetooth configuration | `oceano-setup` helper script |
| Kiosk display | Separate `oceano-player-display` package (later) |

---

## Architecture of the package

```
oceano-player_1.x.x_arm64.deb
├── /usr/local/bin/
│   ├── oceano-source-detector
│   ├── oceano-state-manager
│   ├── oceano-web
│   └── oceano-setup                    ← new: interactive AirPlay/BT wizard
├── /usr/local/lib/oceano/
│   └── broadlink_bridge.py
├── /etc/systemd/system/
│   ├── oceano-source-detector.service
│   ├── oceano-state-manager.service
│   └── oceano-web.service
├── /etc/oceano/
│   └── config.json.default             ← template; postinst copies → config.json if absent
└── DEBIAN/
    ├── control                         ← metadata + apt dependencies
    ├── postinst                        ← enable services, create dirs, Python venv
    ├── prerm                           ← stop services
    └── postrm                          ← purge data dirs (only on --purge)
```

---

## New files to create

### 1. `Makefile`

Targets:

| Target | Action |
|--------|--------|
| `make build` | Cross-compile all three binaries for `linux/arm64` into `dist/` |
| `make package` | Run `nfpm package` → produces `dist/oceano-player_VERSION_arm64.deb` |
| `make release` | `build` + `package` (used by CI) |
| `make test` | `go test ./...` |
| `make clean` | Remove `dist/` |

Version is read from `git describe --tags --always` (e.g. `v1.2.0` → `1.2.0`).

---

### 2. `nfpm.yaml`

[nfpm](https://nfpm.goreleaser.com/) is a single Go binary that reads this file and produces a
`.deb` (and optionally `.rpm`, `.apk`). No Debian build toolchain needed on the build machine.

Key sections:

```yaml
name: oceano-player
version: "${VERSION}"          # injected by Makefile / CI
arch: arm64
maintainer: "Alessandro Lemser <lemser.alessandro@gmail.com>"
description: "Headless audio backend for Raspberry Pi (AirPlay, Bluetooth, UPnP, Physical media)"
homepage: "https://github.com/alemser/oceano-player"

depends:
  - shairport-sync
  - alsa-utils
  - ffmpeg
  - bluez
  - bluez-tools
  - python3
  - python3-venv

recommends:
  - chromium-browser       # only needed for kiosk display

contents:
  # Binaries
  - src: dist/oceano-source-detector
    dst: /usr/local/bin/oceano-source-detector
    file_info: { mode: 0755 }

  - src: dist/oceano-state-manager
    dst: /usr/local/bin/oceano-state-manager
    file_info: { mode: 0755 }

  - src: dist/oceano-web
    dst: /usr/local/bin/oceano-web
    file_info: { mode: 0755 }

  - src: dist/oceano-setup
    dst: /usr/local/bin/oceano-setup
    file_info: { mode: 0755 }

  # Python bridge
  - src: scripts/broadlink_bridge.py
    dst: /usr/local/lib/oceano/broadlink_bridge.py
    file_info: { mode: 0644 }

  # Systemd units
  - src: packaging/systemd/oceano-source-detector.service
    dst: /etc/systemd/system/oceano-source-detector.service
    file_info: { mode: 0644 }

  - src: packaging/systemd/oceano-state-manager.service
    dst: /etc/systemd/system/oceano-state-manager.service
    file_info: { mode: 0644 }

  - src: packaging/systemd/oceano-web.service
    dst: /etc/systemd/system/oceano-web.service
    file_info: { mode: 0644 }

  # Config template
  - src: packaging/config.json.default
    dst: /etc/oceano/config.json.default
    file_info: { mode: 0644 }

scripts:
  postinstall: packaging/postinst
  preremove:   packaging/prerm
  postremove:  packaging/postrm
```

---

### 3. `packaging/systemd/*.service`

These are **static** unit files — no shell variable interpolation — using the defaults that the
web UI already reads from `/etc/oceano/config.json`. They replace the dynamically generated units
that `install-source-detector.sh` etc. write today.

All three units reference `/etc/oceano/config.json` via `--config` flags already supported by
the binaries. The services start with built-in defaults; the operator changes settings via the
web UI and saves (which restarts the relevant service).

Example — `oceano-web.service`:
```ini
[Unit]
Description=Oceano Player Web UI
After=network.target oceano-state-manager.service
Wants=oceano-state-manager.service

[Service]
ExecStart=/usr/local/bin/oceano-web --addr :8080 --config /etc/oceano/config.json --library-db /var/lib/oceano/library.db
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

---

### 4. `packaging/postinst`

```bash
#!/bin/bash
set -euo pipefail

# Create runtime directories
install -d -m 755 /etc/oceano
install -d -m 755 /var/lib/oceano/artwork

# Write default config only on first install (never overwrite on upgrade)
if [ ! -f /etc/oceano/config.json ]; then
    cp /etc/oceano/config.json.default /etc/oceano/config.json
fi

# Python venv for Broadlink bridge
if [ ! -d /opt/oceano-venv ]; then
    python3 -m venv /opt/oceano-venv
    /opt/oceano-venv/bin/pip install --quiet python-broadlink
fi

# Enable and start services
systemctl daemon-reload
systemctl enable --now oceano-source-detector.service
systemctl enable --now oceano-state-manager.service
systemctl enable --now oceano-web.service

echo ""
echo "Oceano Player installed. Open http://$(hostname -I | awk '{print $1}'):8080 to configure."
echo "To set up AirPlay and Bluetooth, run: sudo oceano-setup"
```

---

### 5. `packaging/prerm`

Stops services before files are removed.

```bash
#!/bin/bash
systemctl stop oceano-web.service oceano-state-manager.service oceano-source-detector.service || true
systemctl disable oceano-web.service oceano-state-manager.service oceano-source-detector.service || true
```

---

### 6. `packaging/postrm`

Only removes data on explicit `--purge`.

```bash
#!/bin/bash
if [ "$1" = "purge" ]; then
    rm -rf /etc/oceano /var/lib/oceano /opt/oceano-venv
    systemctl daemon-reload
fi
```

---

### 7. `packaging/config.json.default`

A copy of the default config with empty ACRCloud credentials and `device: ""` so the web UI
auto-detects the hardware. Identical to what `install.sh` writes today, extracted to a static
file.

---

### 8. `cmd/oceano-setup/main.go` — AirPlay + Bluetooth wizard

A new lightweight CLI binary (≈200 lines) that runs interactively on the Pi to:
1. Detect ALSA devices and let the user pick the capture card and DAC
2. Write `/etc/shairport-sync.conf` (mirrors what `install.sh` does today)
3. Configure Bluetooth discoverability and adapter name
4. Restart affected services

This separates the "hardware discovery" logic from the package install and makes it re-runnable
when the user changes hardware. Users who never need AirPlay or Bluetooth skip it entirely.

---

### 9. `.github/workflows/release.yml`

Triggered by a push of a `v*` tag (e.g. `git tag v1.2.0 && git push --tags`).

```yaml
on:
  push:
    tags: ["v*"]

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }          # needed for git describe

      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }

      - name: Install nfpm
        run: |
          curl -sfL https://install.goreleaser.com/github.com/goreleaser/nfpm.sh | sh -s -- -b /usr/local/bin

      - name: Build and package
        env:
          GOARCH: arm64
          GOOS: linux
          CGO_ENABLED: 0
        run: make release

      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          files: dist/oceano-player_*_arm64.deb
          generate_release_notes: true
```

`CGO_ENABLED: 0` is possible because `modernc.org/sqlite` is a pure-Go SQLite port — no C
toolchain needed for cross-compilation.

---

### 10. `.github/workflows/ci.yml`

Runs on every push and pull request to `main`:

```yaml
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: go test ./...
      - run: go vet ./...
```

---

## Implementation phases

### Phase 1 — Local build (no CI yet)

1. Create `Makefile` with `build`, `package`, `test`, `clean`
2. Install `nfpm` locally (`brew install nfpm` on Mac)
3. Create `packaging/` directory with `nfpm.yaml`, service files, `postinst`, `prerm`, `postrm`,
   `config.json.default`
4. Run `make package` → verify `.deb` builds cleanly
5. Test on Pi: `sudo apt install ./dist/oceano-player_*.deb`, confirm services start, web UI
   opens, config is preserved on reinstall

### Phase 2 — GitHub Actions CI

6. Create `.github/workflows/ci.yml` — test on every push
7. Create `.github/workflows/release.yml` — package + publish on tag
8. Push a `v0.1.0` tag, verify the `.deb` appears in GitHub Releases

### Phase 3 — `oceano-setup` wizard (optional but recommended)

9. Implement `cmd/oceano-setup/main.go`
10. Add to `nfpm.yaml` contents and `Makefile`
11. Document in README: "after install, run `sudo oceano-setup` to configure AirPlay and
    Bluetooth"

---

## What does NOT change

| Component | Status |
|-----------|--------|
| `install.sh` and all sub-installers | Remain intact for development use |
| `oceano-web` config UI | Unchanged — primary configuration surface |
| Service logic and binary flags | Unchanged |
| shairport-sync runtime behaviour | Unchanged |
| Python broadlink bridge protocol | Unchanged |
