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

type detectionThresholds struct {
	rmsThreshold    float64
	stddevThreshold float64
}

func run(ctx context.Context, cfg Config) error {
	_ = os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755)
	_ = writeState(cfg.OutputFile, SourceNone)

	thresholds := computeThresholds(cfg)

	hub := newVUHub()
	go hub.run(ctx)
	go listenVU(ctx, cfg.VUSocket, hub)

	pcm := newPCMHub()
	go pcm.run(ctx)
	go listenPCM(ctx, cfg.PCMSocket, pcm)

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
			if err := runStream(ctx, cfg, dev, hub, pcm, thresholds); err != nil {
				log.Printf("stream error on %s: %v — restarting in 2s", dev, err)
				time.Sleep(2 * time.Second)
			}
		}
	}
}

func computeThresholds(cfg Config) detectionThresholds {
	// Try to load from calibration file first
	if nf, ok := loadNoiseFloor(cfg.CalibrationFile); ok {
		log.Printf("calibration: loaded from %s (rms=%.5f stddev=%.5f)", cfg.CalibrationFile, nf.RMS, nf.Stddev)
		return detectionThresholds{
			rmsThreshold:    nf.RMS + nf.Stddev*3.0,
			stddevThreshold: nf.Stddev * 2.5,
		}
	}

	// If calibration-duration is 0, fall back to manual threshold
	if cfg.CalibrationDuration <= 0 {
		log.Printf("calibration: no file, using silence-threshold=%.5f", cfg.SilenceThreshold)
		return detectionThresholds{
			rmsThreshold:    cfg.SilenceThreshold,
			stddevThreshold: cfg.StddevThreshold,
		}
	}

	// Need to measure - this will be done in runStream after device is opened
	return detectionThresholds{
		rmsThreshold:    cfg.SilenceThreshold,
		stddevThreshold: cfg.StddevThreshold,
	}
}

func runStream(ctx context.Context, cfg Config, device string, hub *vuHub, pcm *pcmHub, thresholds detectionThresholds) error {
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

	needsCalibration := cfg.CalibrationDuration > 0 && thresholds.rmsThreshold == cfg.SilenceThreshold

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

	rmsValues := make([]float64, 0, 1024)
	calibrationStart := time.Now()
	calibrating := needsCalibration

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
		// Copy the buffer since raw is reused on the next iteration.
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
		rms := (rmsL + rmsR) / 2.0

		// Publish VU levels regardless of silence state — the consumer decides
		// whether to display them.
		hub.publish(VUFrame{Left: float32(rmsL), Right: float32(rmsR)})

		// During calibration, collect RMS values
		if calibrating {
			rmsValues = append(rmsValues, rms)
			if time.Since(calibrationStart) >= time.Duration(cfg.CalibrationDuration)*time.Second {
				calibrating = false
				if len(rmsValues) > 10 {
					medianRMS := median(rmsValues)
					_, stddev := computeRMSStats(rmsValues)
					log.Printf("calibration: measured rms=%.5f stddev=%.5f (%d samples over %ds)",
						medianRMS, stddev, len(rmsValues), cfg.CalibrationDuration)
					nf := NoiseFloor{
						RMS:     medianRMS,
						Stddev:  stddev,
						Samples: len(rmsValues),
					}
					if err := saveNoiseFloor(cfg.CalibrationFile, nf); err != nil {
						log.Printf("calibration: failed to save: %v", err)
					} else {
						log.Printf("calibration: saved to %s", cfg.CalibrationFile)
					}
					thresholds.rmsThreshold = medianRMS + stddev*3.0
					thresholds.stddevThreshold = stddev * 2.5
					log.Printf("calibration: thresholds rms=%.5f stddev=%.5f", thresholds.rmsThreshold, thresholds.stddevThreshold)
				} else {
					log.Printf("calibration: insufficient samples (%d), using defaults", len(rmsValues))
				}
				rmsValues = nil
			}
			continue
		}

		// Compute stddev of current buffer for hybrid detection
		samples := make([]float64, cfg.BufferSize)
		for i := 0; i < cfg.BufferSize; i++ {
			samples[i] = (left[i] + right[i]) / 2.0
		}
		_, stddev := computeRMSStats(samples)

		var detected Source
		if rms >= thresholds.rmsThreshold || stddev >= thresholds.stddevThreshold {
			detected = SourcePhysical
		} else {
			detected = SourceNone
		}

		voteWindow[voteIdx%cfg.DebounceWindows] = detected
		voteIdx++

		if voteIdx < cfg.DebounceWindows {
			if cfg.Verbose {
				log.Printf("warming up: rms=%.5f stddev=%.5f det=%s (%d/%d)", rms, stddev, detected, voteIdx, cfg.DebounceWindows)
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
			log.Printf("rms=%.5f stddev=%.5f det=%s votes(none=%d physical=%d) curr=%s",
				rms, stddev, detected, noneVotes, physicalVotes, current)
		} else if now := time.Now(); now.Sub(lastHeartbeat) >= time.Minute {
			log.Printf("heartbeat: source=%s rms=%.5f stddev=%.5f", current, rms, stddev)
			lastHeartbeat = now
		}

		if winner != current {
			log.Printf("SOURCE: %s → %s (rms=%.5f stddev=%.5f)", current, winner, rms, stddev)
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
