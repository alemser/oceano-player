# Oceano Source Detector — Calibration Guide

## Why calibration is needed

The source detector classifies audio as **Vinyl**, **CD**, or **None** by analysing the frequency content of the signal coming from an amplifier REC-OUT into the microphone capture card.

The core metric is the **low-frequency energy ratio**: how much of the total signal energy sits in the 20–80 Hz band. Vinyl has a characteristic rumble in this range (motor vibration, stylus friction) that CD does not. The detector computes this ratio using an FFT on each audio window and compares it against a threshold.

The problem is that the right threshold depends entirely on your specific hardware:

- Every amplifier's REC-OUT level is different
- Every USB capture card has its own noise floor and frequency response
- The mic volume setting changes the signal level

If the thresholds are wrong, the detector either misclassifies sources or fails to switch when you change inputs. **Calibration finds the exact thresholds for your setup from real recordings.**

---

## What you need

- Raspberry Pi with the microphone USB capture card connected to the amplifier REC-OUT
- A CD and a vinyl record of the same album (Dire Straits — self-titled works well as it has varied dynamics)
- The four calibration scripts (all must be in the same directory):
  - `capture-session.sh` + `capture-analyse.py` — for recording sessions
  - `analyse-session.sh` + `analyse-session.py` — for analysing results
- numpy installed: `pip3 install numpy --break-system-packages`

---

## How it works

`capture-session.sh` pipes a continuous `arecord` stream into `capture-analyse.py`, which reads the audio window by window (8192 samples ≈ 186ms per window), computes RMS and low-frequency ratio using the **same FFT logic as the Go detector**, and writes the results to a CSV file.

This is important: because the calibration uses identical maths to the detector, the threshold values you get from the CSV are directly applicable to the running service.

`analyse-session.sh` is a thin wrapper that calls `analyse-session.py`, which reads all the CSV files, computes per-label statistics, checks whether the CD and Vinyl ratio ranges overlap, and prints the exact `--silence-threshold`, `--vinyl-threshold`, and `--min-vinyl-rms` values to use.

> **Note:** The scripts are split into `.sh` + `.py` pairs to avoid a bash limitation where heredocs and pipes conflict and cause the Python code to receive no audio data. All four files must be in the same directory.

---

## Setup

Make sure the shell scripts are in the same directory as their Python companions and are executable:

```bash
chmod +x capture-session.sh analyse-session.sh
```

Check that numpy is available:

```bash
python3 -c "import numpy; print(numpy.__version__)"
```

Check your mic volume (should be at `2` for REC-OUT input):

```bash
amixer -c 2 contents
```

If the `Mic Capture Volume` is above `4`, reduce it to avoid clipping:

```bash
amixer -c 2 set 'Mic Capture Volume' 2,2
```

---

## Capture sessions

Run the three captures in order without changing the amplifier volume between them.

### Step 1 — Silence

Turn the amplifier on. Do not select any source or mute all inputs. Run:

```bash
./capture-session.sh --label silence --duration 60
```

This captures the noise floor of your hardware — used to set `--silence-threshold`.

### Step 2 — CD

Put on the Dire Straits CD and start playing from track 1. Once it is playing, run:

```bash
./capture-session.sh --label cd --duration 2520
```

2520 seconds = 42 minutes (full album). You can stop early with `CTRL+C` — at least 5 minutes of varied music is enough for good statistics.

### Step 3 — Vinyl

Switch the amplifier to the vinyl input, drop the needle on Side A. Once it is playing, run:

```bash
./capture-session.sh --label vinyl --duration 1500
```

1500 seconds = 25 minutes (Side A). Let the needle run into the end-of-side groove — that silence with the stylus still in the groove is useful data as it shows the pure motor rumble signature.

### Live output

During capture you will see a line printed every ~2 seconds:

```
  [ 12%] w=  380  rms=0.0842 ████████            ratio=0.0312 ██
  [ 12%] w=  390  rms=0.1205 ██████████████      ratio=0.0287 ██
```

- `rms` goes up and down with the music volume
- `ratio` should be consistently low for CD and consistently higher for Vinyl
- If you see `⚠ CLIPPING`, stop and reduce mic volume: `amixer -c 2 set 'Mic Capture Volume' 1,1`

---

## Analyse

Once all three captures are done:

```bash
./analyse-session.sh
```

Example output:

```
========================================================================
  Label      Metric      Min       P5      P25     Mean      P75      P95
========================================================================
  silence    rms      0.00310  0.00312  0.00314  0.00316  0.00318  0.00321
  silence    ratio     0.0021   0.0024   0.0028   0.0031   0.0035   0.0042
  cd         rms      0.00283  0.01200  0.06500  0.09800  0.13200  0.15800
  cd         ratio     0.0041   0.0082   0.0120   0.0210   0.0310   0.0580
  vinyl      rms      0.00400  0.05200  0.09100  0.12400  0.16100  0.19200
  vinyl      ratio     0.0980   0.1420   0.1980   0.2640   0.3310   0.4120
------------------------------------------------------------------------

========================================================================
  Threshold suggestions
========================================================================

  silence-threshold : 0.00642
    silence p95 rms = 0.00321 × 2.0

  vinyl-threshold   : 0.1000     (CD p95=0.0580  Vinyl p5=0.1420  gap=0.0840)

  min-vinyl-rms     : 0.00963
    silence p95 rms = 0.00321 × 3.0

========================================================================
  Apply with:
========================================================================

  sudo ./install-source-detector.sh \
    --silence-threshold 0.00642 \
    --vinyl-threshold 0.1000 \
    --min-vinyl-rms 0.00963
```

### Reading the output

**`silence-threshold`** — RMS below this means nothing is playing (amp off, or source muted). Set to `silence p95 × 2` to give a comfortable margin above the noise floor.

**`vinyl-threshold`** — Low-freq ratio above this means Vinyl. Set to the midpoint between `CD p95 ratio` and `Vinyl p5 ratio`. The **gap** between these two values is the key number: a gap above `0.05` means clean separation and reliable detection. A gap below `0.02` means the ranges overlap and the detector will be unreliable at that mic volume.

**`min-vinyl-rms`** — RMS must also exceed this to trust a Vinyl classification. Prevents ambient noise (which can have a high ratio at very low amplitude) from being misclassified as Vinyl. Set to `silence p95 × 3`.

### If the gap is too small or negative

The analysis will show:

```
  vinyl-threshold    ⚠ CANNOT SUGGEST — CD and Vinyl ratio ranges OVERLAP
```

This means the detector cannot reliably separate the two sources at the current mic volume. The usual cause is the mic volume being too high, which causes clipping and distorts the frequency distribution. Reduce and re-capture:

```bash
amixer -c 2 set 'Mic Capture Volume' 1,1
./capture-session.sh --label cd    --duration 300
./capture-session.sh --label vinyl --duration 300
./analyse-session.sh
```

---

## Apply the thresholds

Copy the command from the analyser output and run it:

```bash
sudo ./install-source-detector.sh \
  --silence-threshold 0.00642 \
  --vinyl-threshold 0.1000 \
  --min-vinyl-rms 0.00963
```

This rebuilds the Go binary, rewrites the systemd service file with the new values, and restarts the service.

Verify it is working:

```bash
# Watch the service log
journalctl -u oceano-source-detector.service -f

# Check the output file
watch -n 1 cat /tmp/oceano-source.json
```

---

## Re-calibrating

You should re-calibrate if you:

- Change the mic volume (`amixer -c 2 set 'Mic Capture Volume'`)
- Change the REC-OUT cable or capture card
- Add a phono pre-amp or change the signal chain
- Find the detector is misclassifying sources consistently

The captures are stored in `./calibration-data/` with timestamps, so you can re-run `analyse-session.sh` at any time without recapturing.

---

## File reference

| File | Purpose |
|---|---|
| `capture-session.sh` | Shell wrapper — runs `arecord` and pipes audio to `capture-analyse.py` |
| `capture-analyse.py` | Python analyser — reads audio windows, computes RMS + FFT ratio, writes CSV |
| `analyse-session.sh` | Shell wrapper — calls `analyse-session.py` |
| `analyse-session.py` | Python analyser — reads CSVs, computes statistics, suggests thresholds |
| `calibration-data/` | Output directory for CSV files, named `TIMESTAMP_LABEL.csv` |

All four script files must be in the same directory. The `.sh` files locate their `.py` companions using their own path, so the directory name does not matter.
