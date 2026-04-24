package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// readFormatHint reads the format hint file written by oceano-state-manager.
// Returns the lowercase format string ("vinyl", "cd") or "" if absent/unreadable.
func readFormatHint(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var h struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(data, &h); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(h.Format))
}

// selectCalibrationFile derives a per-format calibration file path from the
// base path. "vinyl" → "noise-floor-vinyl.json", "cd" → "noise-floor-cd.json".
// Returns basePath unchanged when format is unrecognised or empty.
func selectCalibrationFile(basePath, format string) string {
	if basePath == "" {
		return basePath
	}
	switch format {
	case "vinyl", "cd":
		ext := filepath.Ext(basePath)
		return basePath[:len(basePath)-len(ext)] + "-" + format + ext
	}
	return basePath
}

func run(ctx context.Context, cfg Config) error {
	_ = os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755)
	_ = writeState(cfg.OutputFile, SourceNone)

	hub := newVUHub()
	go hub.run(ctx)
	go listenVU(ctx, cfg.VUSocket, hub)

	pcm := newPCMHub()
	go pcm.run(ctx)
	go listenPCM(ctx, cfg.PCMSocket, pcm)

	calFile := selectCalibrationFile(cfg.CalibrationFile, readFormatHint(cfg.FormatHintFile))
	if calFile != cfg.CalibrationFile {
		log.Printf("noise floor: using format-specific calibration file %s", calFile)
	}
	learner := newNoiseFloorLearner(calFile)
	defer learner.flush()

	for {
		select {
		case <-ctx.Done():
			_ = writeState(cfg.OutputFile, SourceNone)
			return nil
		default:
			dev, err := resolveDevice(cfg)
			if err != nil {
				log.Printf("device error: %v — retrying in 5s", err)
				time.Sleep(5 * time.Second)
				continue
			}
			if err := runStream(ctx, cfg, dev, hub, pcm, learner); err != nil {
				log.Printf("stream error on %s: %v — restarting in 2s", dev, err)
				time.Sleep(2 * time.Second)
			}
		}
	}
}

func runStream(ctx context.Context, cfg Config, device string, hub *vuHub, pcm *pcmHub, learner *noiseFloorLearner) error {
	log.Printf("opening capture device %s", device)
	cmd := exec.Command("arecord",
		"-D", device,
		"-f", "S16_LE",
		"-r", fmt.Sprintf("%d", cfg.SampleRate),
		"-c", "2",
		"-t", "raw",
		"--duration=0",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	current := SourceNone
	voteWindow := make([]Source, cfg.DebounceWindows)
	for i := range voteWindow {
		voteWindow[i] = SourceNone
	}
	voteIdx := 0
	lastHeartbeat := time.Now()

	// Frame-level counters for entry debounce and exit hold.
	// These operate before the majority-vote window to suppress brief
	// transients (vinyl pops) on entry and brief pauses (a cappella breaths)
	// on exit without touching the vote logic.
	entryTriggerFrames := 0
	exitSilenceFrames := 0

	raw := make([]byte, cfg.BufferSize*4)
	left := make([]float64, cfg.BufferSize)
	right := make([]float64, cfg.BufferSize)
	rolling := newRollingStats(rollingWindow)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if _, err := io.ReadFull(stdout, raw); err != nil {
			return err
		}

		chunk := make([]byte, len(raw))
		copy(chunk, raw)
		pcm.publish(chunk)

		for i := 0; i < cfg.BufferSize; i++ {
			l := int16(binary.LittleEndian.Uint16(raw[i*4:]))
			r := int16(binary.LittleEndian.Uint16(raw[i*4+2:]))
			left[i] = float64(l) / 32768.0
			right[i] = float64(r) / 32768.0
		}

		rmsL := computeRMS(left)
		rmsR := computeRMS(right)
		windowRMS := (rmsL + rmsR) / 2.0

		rolling.push(windowRMS)
		rollingStdDev := rolling.stddev()

		hub.publish(VUFrame{Left: float32(rmsL), Right: float32(rmsR)})

		thresh := learner.current.Thresholds()
		if cfg.SilenceThreshold > 0 && thresh.RMS > cfg.SilenceThreshold {
			// SilenceThreshold is an upper cap, not an override. When the adaptive
			// learner calibrates a lower value, use it (benefits quiet music like
			// a cappella). The cap prevents runaway calibration from pushing the
			// threshold above the configured limit.
			thresh.RMS = cfg.SilenceThreshold
		}
		if cfg.StdDevThreshold > 0 {
			thresh.StdDev = cfg.StdDevThreshold
		}

		// Asymmetric hysteresis:
		//
		//  Entry (None → Physical): RMS AND (StdDev OR high-RMS bypass).
		//    Filters CD transport noise and vinyl groove noise (elevated RMS,
		//    near-zero variation) from triggering a Physical classification.
		//    Requires 3 consecutive musical frames to confirm (~0.14 s).
		//
		//  Exit (Physical → None): RMS only (no StdDev gate).
		//    During music, even quiet sustained passages (a cappella, soft
		//    strings) maintain RMS above the calibrated threshold. Requires
		//    50 consecutive below-threshold frames (~2.3 s) to confirm exit,
		//    preventing brief pauses (breaths, fermatas) from flipping source.
		//
		// The asymmetry is intentional: a stricter entry filters noise while
		// a lenient exit protects quiet music. CD transport noise on the exit
		// path is handled by the SilenceThreshold cap — if transport noise
		// exceeds the cap the user should raise --silence-threshold.
		const (
			rmsHighBypassFactor   = 3.0
			entryTriggerThreshold = 3  // frames (~0.14 s) to confirm entry
			exitSilenceThreshold  = 50 // frames (~2.3 s) to confirm exit
		)

		detected := SourceNone
		if current == SourcePhysical {
			// EXIT: hold in Physical until sustained silence.
			if windowRMS >= thresh.RMS {
				exitSilenceFrames = 0
				detected = SourcePhysical
			} else {
				exitSilenceFrames++
				if exitSilenceFrames > exitSilenceThreshold {
					detected = SourceNone
				}
				// Else: still debouncing — brief pause, stay Physical.
			}
		} else {
			// ENTRY: require sustained musical signal.
			if windowRMS >= thresh.RMS && (rollingStdDev >= thresh.StdDev || windowRMS >= thresh.RMS*rmsHighBypassFactor) {
				entryTriggerFrames++
				if entryTriggerFrames >= entryTriggerThreshold {
					detected = SourcePhysical
				}
			} else {
				entryTriggerFrames = 0
			}
		}

		// Update the adaptive learner only during confirmed silence: RMS below
		// threshold AND variation below the StdDev threshold. The dual gate
		// prevents two contamination paths:
		//   • CD transport noise: RMS gate alone is sufficient (elevated RMS).
		//   • Vinyl groove noise: RMS may be below a freshly-calibrated threshold
		//     but StdDev is high (0.005–0.010). Without the StdDev gate it would
		//     be learned as silence, pulling rmsThreshold down over time and
		//     causing false-negative detection of quiet music in later tracks.
		if windowRMS < thresh.RMS && rollingStdDev < thresh.StdDev {
			learner.update(windowRMS, rollingStdDev)
		}

		voteWindow[voteIdx%cfg.DebounceWindows] = detected
		voteIdx++

		if voteIdx < cfg.DebounceWindows {
			if cfg.Verbose {
				log.Printf("warming up: rms=%.5f stddev=%.5f det=%s (%d/%d)",
					windowRMS, rollingStdDev, detected, voteIdx, cfg.DebounceWindows)
			}
			continue
		}

		noneVotes, physicalVotes := 0, 0
		for _, v := range voteWindow {
			if v == SourceNone {
				noneVotes++
			} else {
				physicalVotes++
			}
		}
		majority := cfg.DebounceWindows/2 + 1

		var winner Source
		switch {
		case physicalVotes >= majority:
			winner = SourcePhysical
		case noneVotes >= majority:
			winner = SourceNone
		default:
			winner = current
		}

		if cfg.Verbose {
			log.Printf("rms=%.5f stddev=%.5f (thresh rms=%.5f stddev=%.5f) det=%s votes(none=%d phys=%d) curr=%s",
				windowRMS, rollingStdDev, thresh.RMS, thresh.StdDev,
				detected, noneVotes, physicalVotes, current)
		} else if now := time.Now(); now.Sub(lastHeartbeat) >= time.Minute {
			log.Printf("heartbeat: source=%s rms=%.5f stddev=%.5f (thresh rms=%.5f stddev=%.5f)",
				current, windowRMS, rollingStdDev, thresh.RMS, thresh.StdDev)
			lastHeartbeat = now
		}

		if winner != current {
			log.Printf("SOURCE: %s → %s (rms=%.5f stddev=%.5f)", current, winner, windowRMS, rollingStdDev)
			current = winner
			_ = writeState(cfg.OutputFile, current)
		}
	}
}

func computeRMS(samples []float64) float64 {
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func writeState(path string, src Source) error {
	state := State{
		Source:    src,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	b, _ := json.MarshalIndent(state, "", "  ")
	tmp := path + ".tmp"
	_ = os.WriteFile(tmp, b, 0o644)
	return os.Rename(tmp, path)
}
