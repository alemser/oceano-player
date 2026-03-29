# README — Oceano Source Detector

## Purpose

Detects whether physical media (vinyl, CD, or any analog source) is playing via the
amplifier REC-OUT → USB capture card path, and writes the result to `/tmp/oceano-source.json`.

The detector outputs only `Physical` or `None` — it does not distinguish between Vinyl and CD.
Source type disambiguation (Vinyl vs CD) is a future capability pending reliable calibration data.

The result is consumed by `oceano-state-manager`. When a streaming source (AirPlay, Bluetooth,
UPnP) is active, the state manager ignores the detector output entirely — there is no need to
monitor the microphone if streaming is already confirmed.

---

## How it works

Each audio buffer (~186 ms at 44.1 kHz) is processed as follows:

1. **RMS** — root mean square of the buffer samples
2. **Silence gate** — if `rms < silence-threshold`, the window votes `None`; otherwise `Physical`
3. **Majority vote** over the last N windows — source changes only when one label exceeds N/2 votes,
   preventing transient noise from triggering false detections

---

## Installation

```bash
sudo ./install-source-detector.sh
```

With explicit silence threshold (tune from your noise floor):

```bash
sudo ./install-source-detector.sh --silence-threshold 0.010
```

---

## CLI flags

| Flag | Default | Description |
|---|---|---|
| `--device` | `plughw:2,0` | ALSA capture device (DIGITNOW USB on card 2) |
| `--output` | `/tmp/oceano-source.json` | Output JSON file |
| `--silence-threshold` | `0.008` | RMS below this = no physical source |
| `--debounce` | `10` | Majority vote window size |
| `--verbose` | `false` | Log per-window RMS and vote counts |

---

## Output format

```json
{
  "source": "Physical",
  "updated_at": "2026-03-29T20:14:47Z"
}
```

`source` is one of: `Physical` | `None`

---

## Tuning the silence threshold

With `--verbose`, the log shows the RMS for each window:

```
rms=0.00312 det=None votes(none=10 physical=0) curr=None
rms=0.08420 det=Physical votes(none=0 physical=10) curr=Physical
```

Set `--silence-threshold` to a value comfortably above your noise floor (typically `0.006`–`0.012`).

---

## Monitoring

```bash
journalctl -u oceano-source-detector.service -f
```
