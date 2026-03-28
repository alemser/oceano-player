# README — Reliable Go CD/Vinyl Detector

## Purpose

This document describes the **runtime Go detector** that consumes live ALSA audio and classifies the current source as:

* `None`
* `CD`
* `Vinyl`

The thresholds should come from the calibration workflow documented in the main README.

---

## Recommended Threshold Inputs

Run the Python calibration first:

```bash
python3 analyze_sources.py
```

Use the generated values as detector flags:

```bash
./detector \
  -device plughw:CARD=Microphone,DEV=0 \
  -silence-threshold 0.008 \
  -vinyl-threshold 0.165 \
  -min-vinyl-rms 0.090
```

Replace the numbers with your measured values.

---

## Core Detection Logic

The detector should classify using 3 signals:

1. RMS level
2. low-frequency rumble ratio
3. silence persistence

### Recommended rumble band

Use:

* **15–140 Hz**

instead of 20–80 Hz.

This captures:

* platter rumble
* arm resonance
* floor vibration
* subsonic energy

with much better stability.

---

## Recommended `classify()`

```go
func classify(samples []float64, cfg Config) (Source, float64, float64) {
    rms := computeRMS(samples)

    if rms < cfg.SilenceThreshold {
        return SourceNone, rms, 0
    }

    spectrum := fft(samples)
    ratio := lowFrequencyRatio(spectrum, cfg.SampleRate, cfg.BufferSize)

    if rms >= cfg.MinVinylRMS && ratio >= cfg.VinylThreshold {
        return SourceVinyl, rms, ratio
    }

    return SourceCD, rms, ratio
}
```

---

## Recommended `lowFrequencyRatio()` band

```go
lowMin := int(15.0 / binHz)
lowMax := int(140.0 / binHz)
```

---

## Runtime Stability Best Practices

### 1) Keep long-running `arecord`

Do not reopen ALSA per window.

Your current streaming approach is correct.

### 2) Use sliding majority vote

Your debounce vote window is good.

Recommended:

* 5 windows for fast switching
* 7 windows for extra stability

### 3) Keep silence-gated transitions

Your rule requiring silence before CD↔Vinyl transitions is excellent.

This mirrors real amplifier source switching.

---

## Expected Runtime Behavior

### CD stopped

```json
{"source":"None"}
```

### Phono idle

Usually:

```json
{"source":"None"}
```

unless platter rumble exceeds the silence threshold.

### CD playing

```json
{"source":"CD"}
```

### Vinyl playing

```json
{"source":"Vinyl"}
```

---

## Reliability Strategy

The biggest reliability gain comes from using:

> calibrated thresholds from your exact hardware chain

instead of hardcoded defaults.

This makes the detector portable across:

* different amps
* phono stages
* USB grabbers
* Raspberry Pi models
* gain settings

---

## Final Recommendation

Treat the Python calibration as the **training phase** and the Go program as the **runtime inference engine**.

Whenever the hardware path changes, regenerate the CSVs and update the thresholds.
