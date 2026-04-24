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
	"time"
)

func run(ctx context.Context, cfg Config) error {
	_ = os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755)
	_ = writeState(cfg.OutputFile, SourceNone)

	hub := newVUHub()
	go hub.run(ctx)
	go listenVU(ctx, cfg.VUSocket, hub)

	pcm := newPCMHub()
	go pcm.run(ctx)
	go listenPCM(ctx, cfg.PCMSocket, pcm)

	learner := newNoiseFloorLearner(cfg.CalibrationFile)
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

		// Relay raw PCM to any connected consumers (e.g. recognizer).
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
		if cfg.SilenceThreshold > 0 {
			thresh.RMS = cfg.SilenceThreshold
		}
		if cfg.StdDevThreshold > 0 {
			thresh.StdDev = cfg.StdDevThreshold
		}

		// Asymmetric hysteresis detection:
		//
		//  None → Physical  (transition): requires BOTH RMS and StdDev above
		//    threshold. This filters CD-transport constant hum (RMS slightly
		//    elevated, variation ≈ 0) and vinyl inter-track groove noise.
		//
		//  Physical → None  (staying): only RMS is checked. Once music is
		//    confirmed, quiet sustained passages (low StdDev but still audible)
		//    stay Physical. Only genuine silence (RMS below threshold) ends the
		//    session. This matches the original single-threshold behaviour for
		//    in-track dynamics while keeping the false-positive guard on entry.
		detected := SourceNone
		if current == SourcePhysical {
			if windowRMS >= thresh.RMS {
				detected = SourcePhysical
			}
		} else {
			if windowRMS >= thresh.RMS && rollingStdDev >= thresh.StdDev {
				detected = SourcePhysical
			}
		}

		// Update the adaptive learner only when RMS is below the current
		// threshold — i.e. the signal is clearly silence. Using detected==None
		// as the gate was wrong: CD transport noise (elevated RMS, near-zero
		// variation) is classified as None but is not silence, and would
		// gradually push noiseRMS up, raising thresholds and causing
		// false-negative detection of steady music passages.
		if windowRMS < thresh.RMS {
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
