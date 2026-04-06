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
			if err := runStream(ctx, cfg, dev, hub, pcm); err != nil {
				log.Printf("stream error on %s: %v — restarting in 2s", dev, err)
				time.Sleep(2 * time.Second)
			}
		}
	}
}

func runStream(ctx context.Context, cfg Config, device string, hub *vuHub, pcm *pcmHub) error {
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

		var detected Source
		if rms >= cfg.SilenceThreshold {
			detected = SourcePhysical
		} else {
			detected = SourceNone
		}

		voteWindow[voteIdx%cfg.DebounceWindows] = detected
		voteIdx++

		if voteIdx < cfg.DebounceWindows {
			if cfg.Verbose {
				log.Printf("warming up: rms=%.5f det=%s (%d/%d)", rms, detected, voteIdx, cfg.DebounceWindows)
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
			log.Printf("rms=%.5f det=%s votes(none=%d physical=%d) curr=%s",
				rms, detected, noneVotes, physicalVotes, current)
		} else if now := time.Now(); now.Sub(lastHeartbeat) >= time.Minute {
			log.Printf("heartbeat: source=%s rms=%.5f", current, rms)
			lastHeartbeat = now
		}

		if winner != current {
			log.Printf("SOURCE: %s → %s (rms=%.5f)", current, winner, rms)
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
