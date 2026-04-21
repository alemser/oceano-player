# Feature: Detect Amplifier On/Off State

# Description

Today we can turn the amplifier on or off via Broadlink, but there is no way to know if it is
already on or off. This feature provides visual feedback about the amplifier's power state.

Amplifiers typically enter standby after a period of inactivity. When music is played via AirPlay,
for example, no sound will be heard and the DAC may not appear as a USB device. Without visual
feedback the user has no indication of the current state when operating from a distance.

Some amplifiers (e.g. tube amps like the Magnat MR 780) also have a warm-up period after power-on
(~30 seconds for the Magnat) before the DAC appears on USB and audio is ready.

# Detection Strategy

Detection is applied in order. Each step is only attempted if the previous one was inconclusive.

## Step 1 — USB DAC presence (most reliable)

Check whether the amplifier's USB DAC is visible as a USB audio device.

- USB present → amp is **on** (and the currently selected input is USB/DAC)
- USB absent → inconclusive; proceed to Step 2

> Note: the USB DAC only appears when the amp is powered on AND the USB Audio input is selected.
> Listening to CD, Vinyl, or FM means USB will be absent even when the amp is on.

## Step 2 — RMS via capture card (physical media / FM)

If a capture card is connected to the amplifier's REC OUT, monitor the RMS level.

- RMS consistently above silence threshold (≈ 0.015) → amp is **on** (physical source playing)
- RMS near zero for an extended period → inconclusive; proceed to Step 3

This step requires a capture card to be configured in the system.

## Step 3 — Input cycling to reach USB Audio (amp-specific, optional)

For amplifiers where Step 1 and Step 2 are inconclusive (no audio playing, USB absent), the system
can cycle through inputs using IR commands until the USB DAC appears or a timeout is reached.

**Conditions required before cycling:**
- RMS has been near zero for a sustained period (e.g. > 2 minutes) — avoids interrupting playback
- No active AirPlay stream
- Broadlink is configured
- Amp-specific cycling support is configured (see below)

**Cycling behaviour:**
- Send one input navigation IR command, wait a few seconds for the input to stabilise
- Check for USB DAC presence and/or RMS activity
- Repeat until USB is found (→ **on**) or the timeout is reached (→ **unknown**)

**Amp-specific configuration — Magnat MR 780:**
- Navigation command: left input button (cycles backwards through inputs)
- Wait per step: ~3 seconds
- Maximum cycles before giving up: configurable (default: 8)

Other amplifiers can be added with their own cycling commands and timing parameters.

## Step 4 — Infer from IR command history

If none of the above signals are available:

| Last IR command | Elapsed time | Inferred state |
|---|---|---|
| Power on | < warm-up timeout | `warming_up` |
| Power on | > warm-up timeout | `unknown` |
| Power off | any | `off` |
| None / unknown | any | `unknown` |

## State definitions

| State | Meaning |
|---|---|
| `on` | Amp is confirmed on (USB DAC present or RMS active) |
| `warming_up` | Power-on command sent; waiting for DAC to appear (within warm-up window) |
| `standby` | Amp entered standby automatically after inactivity timeout |
| `off` | Power-off command sent and confirmed (or inferred from history) |
| `unknown` | Cannot determine state; no signal and no reliable command history |

The standby timeout is configurable per amplifier profile so the system can infer `standby` after
the expected inactivity period without requiring the user to interact.

# UI Requirements

- Replace the single on/off toggle with two explicit buttons: **ON** and **OFF**
- Display the current state in the amplifier title bar (e.g. "Magnat MR 780 — On / Warming up / Standby / Off / Unknown")
- Show a visual indicator (icon or colour) matching the state
- When state is `unknown`, show an interrogation indicator — do not guess

# Prerequisites

Full detection (Steps 2 and 3) requires:
- A capture card connected to the amplifier's REC OUT, configured in the system
- Broadlink configured with the amplifier's IR codes

Step 1 (USB detection) works without any additional hardware.
