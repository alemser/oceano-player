---
name: pi-loopback-capture-sim
description: >-
  Simulates line-in / USB capture on a Raspberry Pi using ALSA snd-aloop and a
  WAV file so oceano-source-detector reports Physical audio and feeds PCM/VU to
  oceano-state-manager without a real amplifier REC OUT. Use when testing
  recognition, physical-source priority, or capture levels on Pi without
  hardware; pair with pi-access skill for SSH.
disable-model-invocation: true
---

# Pi capture simulation (ALSA loopback + WAV)

## Goal

Drive **`oceano-source-detector`** from a **known WAV** instead of the real USB capture dongle, so:

- `/tmp/oceano-source.json` can show **`Physical`**
- `/tmp/oceano-pcm.sock` carries **S16 LE stereo 44100 Hz** PCM (same contract as production)
- **`oceano-state-manager`** can run recognizer triggers end-to-end

This does **not** validate real REC OUT levels, phono hum, or USB capture hardware.

## Prerequisites

- Raspberry Pi with Oceano services installed (`oceano-source-detector`, `oceano-state-manager`).
- SSH access (see project skill **pi-access** if configured).
- `alsa-utils` (`aplay`, `arecord`) and optionally `ffmpeg` for format conversion.

## One-time kernel module

```bash
sudo modprobe snd-aloop
```

Optional persistence (Debian/Raspberry Pi OS):

```bash
echo snd-aloop | sudo tee /etc/modules-load.d/snd-aloop.conf
```

## Discover loopback ALSA names

```bash
aplay -l | grep -i loopback
arecord -l | grep -i loopback
```

Typical pattern for the first cable:

- **Playback client** (where `aplay` writes): `hw:CARD,0,0` or `plughw:CARD,0,0`
- **Capture client** (what `arecord` / Oceano capture reads): `hw:CARD,1,0` or `plughw:CARD,1,0`

`CARD` is the **card index** from `aplay -l` (not necessarily `0`).

## Prepare WAV (S16 LE stereo 44100 Hz)

On Mac or Pi:

```bash
ffmpeg -y -i input.wav -ac 2 -ar 44100 -sample_fmt s16 /tmp/oceano-loopback-test.wav
```

Copy to Pi if needed:

```bash
scp /tmp/oceano-loopback-test.wav pi@PI_HOST:/tmp/
```

## Point Oceano capture at loopback (temporary)

1. Open **oceano-web** → **Audio Input**.
2. Set capture device to the **loopback capture** device string, e.g. `plughw:CARD,1,0` (adjust `CARD`).
3. **Save & Restart Services** (or restart `oceano-source-detector` manually).

**Revert** after the test: restore the real capture `device_match` / `plughw:…` and save again.

## Feed audio into the loopback cable

In a separate SSH session (replace devices and path):

```bash
while true; do
  aplay -D plughw:CARD,0,0 -c 2 -r 44100 -f S16_LE /tmp/oceano-loopback-test.wav
done
```

Leave this running during the test. Stop with `Ctrl+C` when finished.

## Verify

```bash
journalctl -u oceano-source-detector.service -f
# Expect: source=Physical with non-trivial RMS while aplay is running

journalctl -u oceano-state-manager.service -f | grep -i recogniz
```

## Optional helper script

From a checkout of `oceano-player`:

```bash
bash .cursor/skills/pi-loopback-capture-sim/scripts/loopback-smoke.sh --help
```

The script only **prints** suggested commands and device hints; it does not rewrite `config.json` (avoid accidental production misconfiguration).

## Related: provider chain smoke (no audio)

To exercise **`recognizer_chain`** values on the Pi (restart + `SIGUSR1` + log grep) without editing config by hand:

```bash
sudo ./scripts/pi-recognition-provider-smoke.sh --dry-run   # print actions
sudo OCEANO_CONFIG=/etc/oceano/config.json ./scripts/pi-recognition-provider-smoke.sh
```

Off-device, lock provider **ordering** with Go tests:

```bash
go test ./cmd/oceano-state-manager -run TestBuildRecognitionPlanFromChain_matrix -count=1
```

## Safety

- Do not leave loopback as the production capture device.
- Loopback + loud WAV can still clip; keep RMS in a sane range for recognizer tests (same idea as real capture tuning).
