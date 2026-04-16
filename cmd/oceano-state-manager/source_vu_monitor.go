package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"math"
	"net"
	"os"
	"time"
)

// runSourceWatcher polls the source detector JSON file every 2 seconds.
// Changes are rare (user switches from vinyl to CD), so polling is sufficient.
func (m *mgr) runSourceWatcher(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pollSourceFile()
		}
	}
}

func (m *mgr) pollSourceFile() {
	data, err := os.ReadFile(m.cfg.SourceFile)
	if err != nil {
		return
	}
	var det detectorOutput
	if err := json.Unmarshal(data, &det); err != nil {
		return
	}
	src := det.Source
	if src == "" {
		src = "None"
	}

	m.mu.Lock()
	changed := m.physicalSource != src
	newSession := false
	if src == "Physical" {
		// A new session is one where we've been silent longer than the idle delay —
		// meaning the user stopped the record and started a new one, not just a
		// gap between tracks or brief oscillation around the threshold.
		if m.lastPhysicalAt.IsZero() || time.Since(m.lastPhysicalAt) > m.cfg.IdleDelay {
			newSession = true
		}
		m.lastPhysicalAt = time.Now()
	}
	if newSession {
		m.recognitionResult = nil
		m.physicalArtworkPath = ""
		m.physicalFormat = ""
		m.pendingStubID = 0
		m.shazamContinuityReady = false
		m.lastContinuityMismatchAt = time.Time{}
		m.lastContinuityMismatchFrom = ""
		m.lastContinuityMismatchTo = ""
		m.physicalStartedAt = time.Now()
	}
	needsTrigger := src == "Physical" && m.recognitionResult == nil
	m.physicalSource = src
	m.mu.Unlock()

	if changed {
		log.Printf("physical source: %s", src)
		m.markDirty()
	} else if src == "Physical" {
		// Even without a state change, mark dirty so the idle screen wakes
		// promptly if the writer missed a previous Physical transition.
		m.markDirty()
	}

	if needsTrigger {
		select {
		case m.recognizeTrigger <- recognizeTrigger{isBoundary: false}:
		default:
		}
	}
}

// runVUMonitor subscribes to the VU socket from oceano-source-detector and detects
// silence→audio transitions that signal a new track starting. On each transition it
// sends to m.recognizeTrigger so the recognizer runs at the right moment rather than
// on a blind timer.
//
// VU frames are 8 bytes each (float32 L + float32 R, little-endian) at ~21.5 Hz.
// A track boundary is: avg RMS below silenceThreshold for silenceFrames consecutive
// frames, followed by avg RMS above silenceThreshold for activeFrames frames.
func (m *mgr) runVUMonitor(ctx context.Context) {
	const (
		silenceThreshold = float32(0.01)
		silenceFrames    = 22 // ~1 s of silence (vinyl inter-track gaps can be < 2 s)
		activeFrames     = 11 // ~0.5 s of audio resumption
		retryDelay       = 5 * time.Second
	)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.Dial("unix", m.cfg.VUSocket)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}

		log.Printf("VU monitor: connected to %s", m.cfg.VUSocket)
		m.readVUFrames(ctx, conn, silenceThreshold, silenceFrames, activeFrames)
		conn.Close()

		if ctx.Err() != nil {
			return
		}
		log.Printf("VU monitor: disconnected — reconnecting in %s", retryDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
	}
}

func (m *mgr) readVUFrames(ctx context.Context, conn net.Conn, silenceThreshold float32, silenceFrames, activeFrames int) {
	const (
		// Energy-change detection for track boundaries without silence gaps
		// (e.g. vinyl with residual hum, CD crossfades).
		//
		// Two EMAs track the signal energy: a slow baseline (~9 s time constant)
		// and a fast current level (~0.3 s). When the fast EMA dips well below the
		// baseline and then recovers, it indicates a track transition.
		energySlowAlpha      = float32(0.005) // ~200-frame / ~9 s time constant
		energyFastAlpha      = float32(0.15)  // ~7-frame  / ~0.3 s time constant
		energyDipRatio       = float32(0.45)  // fast < slow*0.45 → dip in progress
		energyRecoverRatio   = float32(0.75)  // fast > slow*0.75 after dip → boundary
		energyDipMinFrames   = 43             // dip must sustain ~2 s before committing
		energyWarmupFrames   = 200            // frames before detection is active (~9 s)
		energyChangeCooldown = 30 * time.Second
	)

	buf := make([]byte, 8)
	silenceCount := 0
	activeCount := 0
	inSilence := false

	// Energy-change detection state.
	var slowEMA, fastEMA float32
	energyFrameCount := 0 // counts only non-silent frames; resets after any boundary
	dipCount := 0
	hadDip := false
	lastEnergyTrigger := time.Time{}

	fireBoundaryTrigger := func(reason string) {
		// Pessimistic duration guard: suppress boundary triggers while the track is
		// likely still playing, to avoid re-recognition on silent passages.
		//
		// Base strategy:
		//   - Treat the reported duration as 75% reliable: allow triggers only
		//     after 75% of the track has elapsed. No hard cap — long tracks such
		//     as 14-min epics need the full window (10:40 with 75% of 14:14).
		//   - elapsed is computed from physicalSeekMS, which is set to the actual
		//     time since track start (including any recognition overhead), so the
		//     comparison is accurate even when recognition took multiple attempts.
		//
		// Learned adjustment (self-calibration):
		//   - If a false-positive was previously recorded for this track
		//     (recognition returned the same track after a boundary trigger),
		//     extend the threshold to learnedElapsed + 30 s.
		//   - Never suppress past 95% of the reported duration so the real end
		//     can always be detected.
		//
		// Examples (no calibration):
		//   4-min track  → checks active from 3:00
		//   14-min track → checks active from 10:40
		const (
			durationPessimism   = 0.75            // base: 75% of reported duration
			learnedMargin       = 30 * time.Second // buffer added after last known false-positive
			maxSuppressFraction = 0.95            // never suppress past 95% of track
		)
		m.mu.Lock()
		var durationMs int
		var seekMS int64
		var seekUpdatedAt time.Time
		var learnedElapsedMs int64
		if m.recognitionResult != nil {
			durationMs = m.recognitionResult.DurationMs
		}
		seekMS = m.physicalSeekMS
		seekUpdatedAt = m.physicalSeekUpdatedAt
		learnedElapsedMs = m.physicalDurationFPElapsedMs
		m.mu.Unlock()

		if durationMs > 0 && !seekUpdatedAt.IsZero() {
			elapsed := time.Duration(seekMS)*time.Millisecond + time.Since(seekUpdatedAt)
			trackDuration := time.Duration(durationMs) * time.Millisecond

			// Pessimistic threshold: 75% of reported duration, no hard cap.
			suppressUntil := time.Duration(float64(trackDuration) * durationPessimism)

			// Learned adjustment: extend past the last known false-positive point.
			if learnedElapsedMs > 0 {
				learned := time.Duration(learnedElapsedMs)*time.Millisecond + learnedMargin
				if learned > suppressUntil {
					suppressUntil = learned
				}
				// Safety: never suppress past 95% of the reported duration.
				maxAllowed := time.Duration(float64(trackDuration) * maxSuppressFraction)
				if suppressUntil > maxAllowed {
					suppressUntil = maxAllowed
				}
			}

			if elapsed < suppressUntil {
				log.Printf("VU monitor: boundary suppressed (%s) — %s elapsed, checks active from %s (track %s)",
					reason, elapsed.Round(time.Second), suppressUntil.Round(time.Second), trackDuration.Round(time.Second))
				return
			}
		}

		// Do not clear current metadata/artwork here: boundary detection can fire
		// on false positives, and clearing preemptively causes UI flicker/regressions.
		// State is cleared later only when a boundary-triggered recognition confirms no match.
		log.Printf("VU monitor: track boundary detected (%s) — triggering recognition", reason)
		select {
		case m.recognizeTrigger <- recognizeTrigger{isBoundary: true}:
		default:
		}
	}

	go func() {
		<-ctx.Done()
		conn.SetDeadline(time.Now())
	}()

	for {
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		left := math.Float32frombits(binary.LittleEndian.Uint32(buf[0:4]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(buf[4:8]))
		avg := (left + right) / 2

		// --- Silence-based boundary detection ---
		if avg < silenceThreshold {
			silenceCount++
			activeCount = 0
			if silenceCount >= silenceFrames && !inSilence {
				inSilence = true
				log.Printf("VU monitor: silence detected")
				// Freeze energy model during silence; it will restart fresh on resumption.
				energyFrameCount = 0
				dipCount = 0
				hadDip = false
			}
		} else {
			activeCount++
			silenceCount = 0
			if inSilence && activeCount >= activeFrames {
				inSilence = false
				fireBoundaryTrigger("silence→audio")
				lastEnergyTrigger = time.Now()
			}
		}

		// --- Energy-change detection (seamless transitions without silence) ---
		// Don't run during silence or while still in the active-count phase after silence.
		if avg < silenceThreshold || inSilence {
			continue
		}

		energyFrameCount++
		if energyFrameCount == 1 {
			slowEMA = avg
			fastEMA = avg
		} else {
			slowEMA = energySlowAlpha*avg + (1-energySlowAlpha)*slowEMA
			fastEMA = energyFastAlpha*avg + (1-energyFastAlpha)*fastEMA
		}

		if energyFrameCount < energyWarmupFrames {
			continue // EMA not yet converged; avoid false positives on startup
		}

		if fastEMA < slowEMA*energyDipRatio {
			dipCount++
			if dipCount >= energyDipMinFrames {
				hadDip = true
			}
		} else {
			dipCount = 0
			if hadDip && fastEMA > slowEMA*energyRecoverRatio {
				hadDip = false
				if time.Since(lastEnergyTrigger) >= energyChangeCooldown {
					lastEnergyTrigger = time.Now()
					energyFrameCount = 0 // restart model for the new track
					fireBoundaryTrigger("energy-change")
				} else {
					log.Printf("VU monitor: energy-change boundary suppressed (cooldown active)")
				}
			}
		}
	}
}
