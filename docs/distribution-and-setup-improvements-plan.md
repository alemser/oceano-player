# Distribution and first-time setup — improvement plan

This document assesses how easy it is to distribute Oceano Player today, identifies gaps (especially microphone capture calibration and HDMI/DSI kiosk setup), and proposes a phased plan to make installs predictable for end users.

---

## Current distribution model

### Debian package (primary path for Pi users)

- **Build**: `Makefile` target `make release` cross-compiles `arm64` Linux binaries and runs `nfpm` (`nfpm.yaml`) to produce `dist/oceano-player_*_arm64.deb`.
- **CI vs releases**: GitHub **CI** (`.github/workflows/ci.yml`) runs `go test`, `go vet`, and a cross-compile **smoke build** — it does **not** produce a `.deb`. The **Release** workflow (`.github/workflows/release.yml`) builds and uploads the `.deb` only when a **version tag** `v*` is pushed.
- **Contents**: Core binaries (`oceano-source-detector`, `oceano-state-manager`, `oceano-web`, `oceano-setup`), systemd units, default config template, Broadlink bridge script, directory layout. **Not** packaged today: `oceano-display-check` / `oceano-display-launch` from `install-oceano-display.sh` (those are installed only when that script has been run).

### Source install (`install.sh`)

- Heavier path: clones/builds on device or uses release artifacts depending on script version; wires AirPlay bridge, PipeWire/ALSA stack, and all services. Good for developers and custom branches; higher surface area than the `.deb`.

### Post-install messaging

- **`packaging/postinst`** (after Phase 1) prints the web UI URL and directs users
  to **`sudo oceano-setup`** for AirPlay, Bluetooth, devices, and optional kiosk;
  it no longer references `./install-oceano-display.sh` (which is not on disk for
  `.deb`-only installs).

### Interactive wizard (`oceano-setup`)

- Covers: AirPlay name + `shairport-sync` config, ALSA output/capture **device selection**, Bluetooth name/agent, optional **display** enablement.
- Does **not** cover: capture **gain** (ALSA mixer), **silence threshold** tuning, ACRCloud credentials (by design — web UI), or the full **graphical stack** parity with `install-oceano-display.sh`.

---

## Why microphone and screen setup are “fundamental”

### Capture (microphone / REC OUT) calibration

Recognition and `Physical` vs `None` detection depend on:

- Sensible **capture level** (RMS in logs, typically ~0.05–0.25 during playback per README/CLAUDE).
- **`silence_threshold`** in `/etc/oceano/config.json` relative to the noise floor.

Today this is **documented** in README (manual `journalctl` + `amixer` + `alsactl store`) but **not guided** in `oceano-setup` or a dedicated tool. Users must discover troubleshooting docs after failures.

### Screen (HDMI/DSI kiosk)

Two different approaches exist in the repo:

| Aspect | `install-oceano-display.sh` | `oceano-setup` → `configureDisplay()` |
|--------|-----------------------------|----------------------------------------|
| Stack | Installs Xorg-related packages, Chromium detection, **Xvfb**-based launch script, optional LightDM autologin / xsession | Writes minimal `oceano-display-check`, `oceano-display-launch`, systemd unit; tries `apt-get install chromium-browser` |
| Chromium flags / URL | Richer flags (`--app=`, window size, hide cursor, etc.) | Simpler `--kiosk` + URL |
| Display detection | Filters HDMI/DSI/DP in DRM status loop | Any `connected` DRM connector |
| Operational risk | Complex (Xvfb + LightDM + systemd story must stay consistent) | **Running Chromium from a systemd service** often needs a **real user graphical session** or the same Xvfb pattern — current Go path may **fail silently** or behave differently than the bash installer |

This **duplication and drift** is a likely reason a previous attempt “did not work well”: users may follow `postinst` → non-existent script, or `oceano-setup` → incomplete display stack vs `install-oceano-display.sh`.

---

## Goals (what “good” looks like)

1. **One obvious path** after `apt install ./oceano-player_…_arm64.deb`: documented steps that always refer to **commands present on disk** (`oceano-setup`, optional `/usr/local/share/oceano/...` scripts).
2. **Guided capture calibration** in the wizard or web UI: measurable signal (RMS), suggested mixer control, optional persist (`alsactl store`), and optional **silence threshold** suggestion from a short noise sample.
3. **Single source of truth for kiosk**: either ship display helper scripts in the `.deb` and have both `postinst` hints and `oceano-setup` call them, or **delete** the divergent Go implementation and shell out to one maintained script installed with the package.
4. **Optional CI artifact**: e.g. build `.deb` on `main` as an **unpublished workflow artifact** or pre-release, so regressions in packaging are caught without tagging.

---

## Recommended plan (phased)

### Phase 1 — Documentation and messaging (low risk, immediate) — **done**

- Fix **`postinst`** (and README if needed) so kiosk instructions never reference `./install-oceano-display.sh` unless that file is **actually installed** by the package. Prefer: “Run `sudo oceano-setup` and enable the display step” **or** “see README — Display”.
- Add a short **`docs/` or README** “Debian install checklist”: install → `oceano-setup` → web UI (ACRCloud + devices) → optional display → reboot.
- Clarify in README that **`.deb` artifacts appear on GitHub Releases (tags)**, not on every CI run.

Implemented in branch `install-improvements`: `packaging/postinst` now points to
`sudo oceano-setup` and the README; README Option A documents Releases vs CI and
adds the post-install checklist (with a note that `install-oceano-display.sh`
applies to git clones only). `CLAUDE.md` Deployment and Now Playing display
sections match the same split (`.deb` + `oceano-setup` vs repo + `install-oceano-display.sh`).

### Phase 2 — Unify kiosk installation (high impact)

- **Decision**: Pick **one** implementation — recommend **bash `install-oceano-display.sh`** as canonical (already handles packages, Chromium path, Xvfb/LightDM wiring) and:
  - **Vendor it into the `.deb`** (e.g. `/usr/local/lib/oceano/scripts/install-oceano-display.sh` or `/usr/share/oceano/...`) with `755`, **or** inline equivalent logic in `postinst` only if you want zero second script (usually worse).
  - Change **`oceano-setup`** to run the shipped script (non-interactive flags if feasible, or `exec` into it) instead of maintaining a second `configureDisplay()` path — **or** reimplement the bash script’s behavior in Go only if you commit to feature parity and tests.
- Add **`oceano-display.service`** to `nfpm.yaml` only if the unit file is stable and matches the shipped launch path (today the service is created by the display installer, not the core `.deb`).
- **Smoke test on Pi**: fresh Bookworm image → `.deb` → setup → reboot → `nowplaying.html` visible on HDMI.

### Phase 3 — Microphone / capture calibration wizard (medium effort, high UX)

- **CLI extension (`oceano-setup` or subcommand)**:
  - After capture device is chosen, optionally run a **“Calibration”** step:
    - Start or reuse `oceano-source-detector` heartbeat (or read last lines from journal) and print **live RMS** with a target band.
    - Enumerate **`amixer`** controls for the selected card and let the user adjust (interactive) or print exact commands to run.
    - On success, prompt to **`alsactl store`** and optionally bump **`silence_threshold`** in `config.json` with a short **silence** instruction (“pause playback 10s”) to estimate noise floor — document that this is heuristic.
- **Web UI (optional, later)**:
  - A small “Capture health” panel: last RMS, link to metrics/history, copy-paste `amixer` lines — avoids SSH for users who only use the browser (requires a safe read-only path from state or logs).

### Phase 4 — Packaging and CI hardening

- **Workflow**: Add a job that runs `make release` (or `make package`) on `pull_request` / `push` to `main` **without** uploading to GitHub Releases, storing the `.deb` as an **artifact** for manual download and regression testing.
- **`recommends` vs `depends`**: Review `chromium-browser` vs `chromium` on Bookworm; align package names with what `install-oceano-display.sh` and `findChromium()` expect.
- **Versioning**: Ensure `nfpm` version matches `git describe` so support logs are actionable.

### Phase 5 — Polish and telemetry (optional)

- First-boot **`systemd` one-shot** that prints a **motd** or journal notice pointing to `oceano-setup` until `config.json` marks “initial setup done” (careful not to annoy headless users).
- Capture **structured log line** after calibration (threshold, card id) for support without exposing secrets.

---

## Risks and constraints

- **Hardware variance**: USB capture cards expose different mixer control names; calibration must degrade gracefully (“run `alsamixer -c N` manually”).
- **Display stack on Pi OS**: Bookworm moves between X11/Wayland/Labwc depending on image; any LightDM/X11 assumption should be **documented** and tested on the **same image class** you recommend in README.
- **Behavior-preserving rule**: Until Phase 2 is complete, treat **`install-oceano-display.sh`** as the reference for working kiosks; avoid expanding the Go `configureDisplay()` path without parity.

---

## Summary

Distribution via **`.deb` on tagged releases** is already a strong baseline; the main weaknesses are **inconsistent post-install instructions**, **split brain between two kiosk installers**, and **manual-only capture calibration**. Addressing messaging first, then **consolidating kiosk into one shipped path**, then **adding a guided RMS/mixer step**, yields the largest improvement for real users with controlled risk.
