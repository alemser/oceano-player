package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"
)

const (
	streamingUSBGuardPollInterval = 500 * time.Millisecond
	streamingUSBGuardCooldown     = 20 * time.Second
	streamingUSBGuardResetTimeout = 45 * time.Second
)

type streamingStateSnapshot struct {
	Source string `json:"source"`
	State  string `json:"state"`
}

func shouldEnsureUSBForStreamingPlayback(source, playbackState string) bool {
	if playbackState != "playing" {
		return false
	}
	return source == "AirPlay" || source == "Bluetooth"
}

// startStreamingUSBGuard ensures the amp is routed to USB while AirPlay or
// Bluetooth playback is active. If the USB DAC is not currently detectable,
// it triggers the existing reset-to-USB flow.
func startStreamingUSBGuard(ctx context.Context, stateFile string, ampServer *amplifierServer) {
	if ampServer == nil || ampServer.amp == nil || stateFile == "" {
		return
	}

	go func() {
		ticker := time.NewTicker(streamingUSBGuardPollInterval)
		defer ticker.Stop()

		var lastMod time.Time
		var lastResetAttempt time.Time

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			info, err := os.Stat(stateFile)
			if err != nil {
				continue
			}
			if !info.ModTime().After(lastMod) {
				continue
			}
			lastMod = info.ModTime()

			data, err := os.ReadFile(stateFile)
			if err != nil {
				continue
			}

			var snap streamingStateSnapshot
			if err := json.Unmarshal(data, &snap); err != nil {
				continue
			}
			if !shouldEnsureUSBForStreamingPlayback(snap.Source, snap.State) {
				continue
			}

			now := time.Now()
			if !lastResetAttempt.IsZero() && now.Sub(lastResetAttempt) < streamingUSBGuardCooldown {
				continue
			}
			if ampServer.usbDACPresent(ctx) {
				continue
			}

			lastResetAttempt = now
			attemptCtx, cancel := context.WithTimeout(ctx, streamingUSBGuardResetTimeout)
			resp, err := ampServer.resetUSBInput(attemptCtx)
			cancel()
			if err != nil {
				log.Printf("streaming USB guard: reset failed: %v", err)
				continue
			}
			log.Printf("streaming USB guard: source=%s state=%s status=%s jumps=%d", snap.Source, snap.State, resp.Status, resp.Attempts)
		}
	}()
}
