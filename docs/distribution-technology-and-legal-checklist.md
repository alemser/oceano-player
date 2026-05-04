# Distribution technology stack and legal compliance checklist

This document inventories **software and hardware components** touched by Oceano Player when deployed on a **Raspberry Pi** (typical path) or on **custom Linux hardware** with a reduced stack. It is meant as an **internal engineering and product checklist**, not legal advice. **Consult qualified counsel** before shipping a commercial product, especially where **AirPlay compatibility**, **Bluetooth codecs**, **audio fingerprints**, or **branding** (Raspberry Pi, Apple, etc.) are involved.

## How to use this document

1. **Decide your distribution model** — e.g. Debian package on Raspberry Pi OS, custom image, or appliance with only required packages.
2. For each **row**, verify **licenses**, **API terms**, **export/patent** implications for your jurisdiction, and **privacy** (what user or playback data leaves the device).
3. Treat the **policy assessment** column as a **non-legal, high-level risk note** from an engineering perspective; it does not replace counsel.

---

## 1. Core platform and distribution

| Component | Role in Oceano | Verify when distributing | Policies and primary references | Assessment (non-legal) |
|-----------|----------------|----------------------------|-----------------------------------|-------------------------|
| **Raspberry Pi hardware** | Reference target (Pi 5); GPIO/HDMI/DSI optional | Trademark and “Raspberry Pi” naming on packaging/marketing; reseller rules if you sell kits | [Raspberry Pi trademark rules](https://www.raspberrypi.com/trademark-rules/) | Use official naming guidelines; do not imply endorsement by Raspberry Pi Ltd. |
| **Raspberry Pi OS / Debian packages** | Base OS and `apt` dependencies | Compliance with Debian [DFSG](https://wiki.debian.org/DebianFreeSoftwareGuidelines) mix vs non-free; redistribution of modified packages | [Debian legal information](https://www.debian.org/legal/) | Mostly mature FOSS stack; watch **non-free firmware** and **codec** packages if you ship a curated image. |
| **systemd** | Service supervision for detector, state manager, web, display, shairport | LGPL v2+ obligations if you link against libsystemd in a proprietary binary (Oceano’s Go binaries typically invoke systemd via CLI) | [systemd README / license](https://github.com/systemd/systemd) | Low friction for typical “install our `.deb` / scripts” distribution. |
| **Go toolchain** (build time) | Compiling Oceano binaries; `install.sh` may download a Go tarball | BSD-style license for compiler; your **product license** for distributed binaries is separate | [Go license](https://go.dev/LICENSE) | Straightforward for building; ensure you satisfy **dependencies’** licenses (see libraries). |

---

## 2. Oceano application runtime (this repository)

| Component | Role in Oceano | Verify when distributing | Policies and primary references | Assessment (non-legal) |
|-----------|----------------|----------------------------|-----------------------------------|-------------------------|
| **Oceano binaries** (`oceano-source-detector`, `oceano-state-manager`, `oceano-web`, `oceano-setup`) | Main product logic | Choose and publish a **SPDX license** for your distribution; honor **NOTICE** files for transitive deps | Repository `LICENSE` (if present) + dependency licenses below | You own distribution terms for your build; keep **third-party attribution** visible (e.g. in image or `/usr/share/doc`). |
| **modernc.org/sqlite** | Embedded SQLite for library / local data | [SQLite blessing](https://www.sqlite.org/purchase/license) (public domain–style) + modernc wrapper terms | [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) | Generally permissive; still document in notices. |
| **grandcat/zeroconf** | mDNS / DNS-SD client code in tree | Apache 2.0 attribution | [zeroconf](https://github.com/grandcat/zeroconf) | Standard Apache 2.0 hygiene. |
| **golang.org/x/*** (e.g. `net`, `crypto`) | TLS, networking | BSD-style; Google’s PATENTS file historically debated — review current `LICENSE` in module | [Go extended libraries](https://cs.opensource.google/go/x) | Widely used; include license text in compliance bundle. |

---

## 3. Audio path: capture, AirPlay, Bluetooth, ALSA, PipeWire

| Component | Role in Oceano | Verify when distributing | Policies and primary references | Assessment (non-legal) |
|-----------|----------------|----------------------------|-----------------------------------|-------------------------|
| **Linux kernel + ALSA** | USB capture for physical detection/recognition; DAC playback | GPL v2 for kernel; driver firmware licenses | Kernel [COPYING](https://www.kernel.org/doc/html/latest/process/license-rules.html) | Standard embedded Linux topic; capture/playback is local until you send audio to third parties. |
| **shairport-sync** | AirPlay **receiver** (RAOP) | **High commercial sensitivity:** Apple does not offer a public license for third-party AirPlay receivers; shairport-sync is a **community reimplementation**. Distributors must assess **patent, trademark, and ToS** risk in each market. | [shairport-sync](https://github.com/mikebrady/shairport-sync) (GPL); Apple: [AirPlay overview](https://www.apple.com/airplay/) (marketing; not a license grant) | **Primary legal hotspot** for a consumer “AirPlay speaker” story. Many hobby projects use shairport; **commercial bundling** needs counsel. |
| **Avahi (`avahi-daemon`)** | mDNS for AirPlay discovery from iOS | LGPL | [Avahi](https://github.com/avahi/avahi) | Common dependency; service discovery only. |
| **BlueZ + D-Bus** | Bluetooth A2DP sink, AVRCP metadata | GPL/LGPL stack; Bluetooth SIG qualification if you ship branded Bluetooth hardware | [BlueZ](http://www.bluez.org/) | Software stack is standard; **hardware** may need **Bluetooth SIG** listing and **QDID** path. |
| **PipeWire / WirePlumber** (optional paths) | Bluetooth → DAC routing; codec plugins | Mostly MIT/ LGPL mix; **codec binaries** may pull patent-encumbered builds | [PipeWire](https://pipewire.org/), [WirePlumber](https://pipewire.pages.freedesktop.org/wireplumber/) | Verify which **codec plugins** ship (AAC, LDAC, AptX, etc.) — **patent pools** and **binary redistribution** rules apply to hardware products. |
| **FDK-AAC / `libfdk-aac`** (optional build in `install.sh`) | Bluetooth AAC plugin build path | Fraunhofer FDK license is **not compatible with GPL** in some combinations; FFmpeg project documents FDK nuances | [FFmpeg legal](https://ffmpeg.org/legal.html); Fraunhofer FDK-AAC licensing | **Do not assume “apt install = OK for my appliance.”** AAC encode/decode often needs **patent licensing** for commercial hardware. |
| **ffmpeg** (package; optional Shazam path) | Installed with `install-shazam.sh` / general tooling | LGPL/GPL depending on build flags; patent claims on codecs | [FFmpeg legal](https://ffmpeg.org/legal.html) | If you only distribute **your Go binaries** and rely on user-installed `ffmpeg`, responsibility shifts; **bundled ffmpeg** needs a license audit. |
| **ALSA `alsa-utils`** | Device listing, `amixer`, etc. | LGPL/GPL parts | [ALSA](https://www.alsa-project.org/) | Low drama for typical Pi use. |

---

## 4. Track recognition providers (online, BYOK)

| Component | Role in Oceano | Verify when distributing | Policies and primary references | Assessment (non-legal) |
|-----------|----------------|----------------------------|-----------------------------------|-------------------------|
| **ACRCloud** | Primary fingerprint identification for physical media | Account terms, data processing, retention, **EU/US** transfer, rate limits, **redistribution** of metadata/art URLs | [ACRCloud](https://www.acrcloud.com/) — use **Terms**, **Privacy**, and developer agreement linked from their site | **Contractual relationship** is between **you/the operator** and ACRCloud; disclose to end users if fingerprints leave the device. |
| **AudD** | Optional REST recognition (BYOK token) | API terms, acceptable use, quotas | [AudD docs](https://docs.audd.io/); [AudD site terms](https://audd.io/) (verify current legal pages) | Clear **documented API** model; still need **privacy notice** for audio snippets sent to AudD. |
| **shazamio (Python)** | Optional community recognizer via subprocess; **not** Apple’s official API | PyPI package license; **unofficial** use of Shazam-like services may violate **third-party ToS** even if the library is open source | Project docs in repo: `install-shazam.sh`; [shazamio on PyPI](https://pypi.org/project/shazamio/) | **High ToS uncertainty** vs Apple/Shazam; treat as **best-effort optional** feature, not a retail guarantee. |

---

## 5. Metadata enrichment and artwork (online)

| Component | Role in Oceano | Verify when distributing | Policies and primary references | Assessment (non-legal) |
|-----------|----------------|----------------------------|-----------------------------------|-------------------------|
| **Discogs API** | Release-level metadata enrichment (when configured) | [Discogs API terms](https://www.discogs.com/developers/) — attribution, images, rate limits | [Discogs Terms of Use](https://www.discogs.com/help/doc/terms-of-use) | Generally **developer-friendly** with explicit API rules; cache/store fields per their terms. |
| **Apple iTunes Search API** | Lookup for artwork / catalog hints (HTTP `itunes.apple.com`) | Apple **standard EULA** for Internet Services; attribution requirements | [Apple iTunes Search API EULA](https://www.apple.com/legal/internet-services/itunes/dev/stdeula/) | **Read attribution clause carefully**; artwork URLs often point to **Apple CDN** (`mzstatic.com`) — usage is governed by Apple rules, not only your code. |
| **Apple CDN (`mzstatic.com`)** | Artwork image bytes | Same as Apple media / affiliate rules for caching and display | Linked from Apple developer / iTunes API docs above | Treat artwork like **third-party content**; do not assume unlimited **commercial merchandising** rights. |
| **MusicBrainz (via AudD `return` fields)** | Optional structured IDs in AudD responses | MusicBrainz data license if you persist/republish database excerpts | [MusicBrainz doc data license](https://musicbrainz.org/doc/MusicBrainz_Documentation/License) | Usually fine for **normal app metadata**; mind **bulk redistribution** vs live API use. |

---

## 6. UI, kiosk, and local HTTP

| Component | Role in Oceano | Verify when distributing | Policies and primary references | Assessment (non-legal) |
|-----------|----------------|----------------------------|-----------------------------------|-------------------------|
| **Embedded static Now Playing UI** | Served by `oceano-web` (HTML/JS/CSS) | Your license for shipped assets; any **third-party fonts/icons** in static bundle | Check `static/` provenance in repo | Audit **icon/font** licenses if not all in-house. |
| **Chromium** (kiosk) | Renders `/nowplaying.html` | Chromium license + branding; auto-update channel policies if you fork | [Chromium](https://www.chromium.org/Home/); [Chromium license](https://chromium.googlesource.com/chromium/src/+/HEAD/LICENSE) | **Chromium ≠ Chrome**; branding and **Widevine** (if ever added) are separate topics. |
| **Xorg / Xvfb / LightDM** | Display session for kiosk | Per-package licenses (mostly MIT/X11/GPL mix) | Distribution-specific (`apt` source) | Low risk legally; **security** hardening matters for exposed kiosk. |
| **`oceano-web` HTTP API** | LAN configuration and SSE | Your responsibility for **TLS**, **auth**, and **GDPR** if you log IPs/metadata | N/A (your deployment) | Default is **local network** — still document what is stored in SQLite and logs. |

---

## 7. Hardware beyond the Pi (typical Oceano setup)

| Component | Role in Oceano | Verify when distributing | Policies and primary references | Assessment (non-legal) |
|-----------|----------------|----------------------------|-----------------------------------|-------------------------|
| **USB DAC / amplifier** (e.g. class-compliant USB audio) | Playback output | USB-IF trademark if you use USB logos; vendor firmware | [USB-IF trademark guidelines](https://www.usb.org/getting-the-usb-logo) | Usually **vendor’s hardware** compliance; your product brochure should not misuse logos. |
| **USB capture interface** | REC OUT → PCM for detection/recognition | Same as above | Vendor docs | **Electrical safety** and **EMC** are certification topics, not only software. |
| **Bluetooth radio module** (if custom PCB) | BT audio | Regulatory (FCC, CE, UKCA, etc.) + **Bluetooth SIG** | [Bluetooth SIG](https://www.bluetooth.com/develop-with-bluetooth/) | **Hardware certification** domain; software stack alone is insufficient. |

---

## 8. Companion and downstream (not shipped by this repo)

| Component | Role | Verify | Policies | Assessment (non-legal) |
|-----------|------|--------|------------|-------------------------|
| **`oceano-player-ios` app** | Remote control / config consumer of this backend | Apple App Store Review Guidelines; privacy nutrition labels | [Apple Developer Program License Agreement](https://developer.apple.com/support/terms/) | Separate **distribution pipeline** from Pi backend; keep **API contract** in sync (see `docs/ai-cross-repo-sync.md`). |

---

## 9. Suggested “minimum custom hardware” software set

If you strip the stack to **only what Oceano needs** on custom Linux hardware, you still typically retain:

- **Kernel + ALSA** (or PipeWire if you standardize on it)
- **BlueZ** (if Bluetooth is required)
- **shairport-sync + Avahi** (if AirPlay is required) — **see AirPlay risk row**
- **Oceano Go services + SQLite**
- **One or more** of: **ACRCloud**, **AudD**, **shazamio** (optional)
- **Optional**: **Discogs**, **iTunes Search API** for enrichment
- **Optional kiosk**: **Chromium + X**

Everything removed (e.g. no shairport) reduces **legal surface** but changes **product claims** (“AirPlay receiver” must not be advertised if removed).

---

## 10. Maintainer note

When **adding a new network dependency** (provider, CDN, or installer download URL), extend this table in the **same pull request** so distribution and privacy reviews stay traceable.

**Last updated:** 2026-05-04 (covers components referenced in `install.sh`, `install-shazam.sh`, `install-oceano-display.sh`, `go.mod`, and `internal/metadata` / `internal/recognition` as of that date).
