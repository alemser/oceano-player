# On-Device ML & Stylus Life — Honest Evaluation

**Companion to:** `recognition-master-plan.md`  
**Date:** 2026-05-04  
**Context:** User explored three ideas with ChatGPT — stylus life monitoring, on-device ML boundary/live detection using existing RMS telemetry, 7-second WAV classification on a Pi with 1 GB RAM in Go.

---

## Short verdict

The recognition master plan's existing posture is **correct and conservative**. The ChatGPT conversation surfaces three genuinely interesting directions, but the "Sim / Yes" answers understate the complexity of each. Below is an honest breakdown.

---

## 1. On-device ML: boundary detection, live vs. studio, music start/end

### What the plan already covers
- **T15 (R6/R6b):** "ML-lite boundary classifier + metrics" — explicitly planned but gated behind 1B (percentile/rolling aggregates).
- **"Live / gapless boundary policy":** "On-device ML for transitions: long-term research; prefer export features → sidecar over hot-path inference until validated."
- The sequencing is right: **telemetry → offline analysis → sidecar → bounded on-device**.

### Reality check on each sub-question

**"When does a track start / end?"**
The existing VU monitor (silence→audio, duration-exceeded, R2b floor clamp) already does this without ML. ML would add value mainly for gapless records or live sets where silence never appears. T15 is the right long-term slot for this.

**"Live vs. studio?"**
This is a real audio ML problem. Short-clip live detection works reasonably well with:
- Crowd noise / reverb tail in inter-song gaps
- Audience applause spectral signature
- Dynamic range (live tends to be more compressed in peak-to-average)

But 7-second clips are short — you may capture only the track itself, not the gap. Reliability on a 7 s window is unclear without empirical testing. This belongs in a research spike, not the main backlog.

**"Can this run on Pi 1 GB?"**
Yes, with severe constraints:
- **TensorFlow Lite** or **ONNX Runtime** have Go/C bindings; a small classifier (< 5 MB) can infer in < 100 ms on Pi 5.
- But these add CGo complexity to a Go codebase that currently has no CGo.
- The plan's sidecar model ("export features → sidecar process") is better: keep Go clean, run Python/Rust inference process with bounded memory, communicate via socket.
- A 200 KB ONNX model doing 7 s FFT features + logistic regression is feasible at ~50 MB peak RAM.
- A full neural network (MobileNet-scale) for audio classification uses 150–300 MB — tight on 1 GB with everything else running.

**"Does 7 s WAV work as input?"**
For fingerprinting (ACRCloud, AudD): yes, that is exactly their design.  
For an internal classifier: 7 s × 44100 Hz × 2 ch × 2 bytes = ~1.2 MB raw, ~86 KB after log-mel spectrogram at standard params. That fits. The question is whether 7 s gives enough context for the specific task.

### What ChatGPT got right (and wrong)
**Right:** The existing RMS / noise floor data IS the right feature space. It is not wasted. `recognition_attempts.wav_rms` + session context are exactly what a boundary quality model would need as labels.

**Wrong (by omission):** Saying "Sim" without the qualifier that you need **labeled data first**. The model cannot be trained without knowing which 7 s captures led to correct vs. wrong recognition outcomes — which is exactly what `recognition_attempts` is now building. The ML work cannot start until you have 1 000+ labeled attempt rows with outcomes.

---

## 3. Influence on the master plan

The ChatGPT conversation does not invalidate anything in the plan. It does suggest two clarifications worth adding:

### 3a. Clarify T15 ML architecture choice
The plan says "ML-lite" but does not specify what that means on a Pi in Go. Add:
- Preferred architecture: **feature extraction in Go** (RMS, silence ratio, energy delta, zero-crossing rate) → **ONNX sidecar** (separate process, bounded RAM, socket IPC) → result injected into coordinator.
- CGo in the main binary is explicitly **not** the goal.
- Pi memory budget for sidecar: < 100 MB peak including model load.

### 3b. Live/studio detection — add as explicit research spike
Not in the current matrix. Could be added as:
- **T23 (research spike):** Live vs. studio detection from 7 s WAV — spectral + dynamic features → small classifier. Gate on: ≥ 500 labeled Vinyl attempt rows with format confirmed; offline validation first.

---

## 4. What is genuinely promising here

1. **`recognition_attempts` is already the training set** for everything else. The investment in T22 pays off for all future ML work.
2. The plan's sequencing (telemetry → offline → lite sidecar → on-device) is the right order. ChatGPT's enthusiasm does not change that sequencing — it validates why the telemetry foundation matters.
3. Gapless/live boundary detection is the hardest remaining question. Do not start it before you have 6+ months of real attempt rows.

---

## 5. Summary table

| Idea | Feasibility on Pi 1 GB | In current plan? | Recommended next step |
|------|----------------------|-----------------|----------------------|
| Live vs. studio detection | Feasible (small ONNX sidecar) | Not explicit | Add as T23 research spike |
| Music boundary ML | Feasible with sidecar | T15 (gated correctly) | Wait for labeled `recognition_attempts` data |
| 7 s WAV feature extraction | Yes, in Go without CGo | N/A | Prerequisite for all of the above |

---

*This evaluation is intentionally critical. "Sim" is not a plan.*
