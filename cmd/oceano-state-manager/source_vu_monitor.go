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

const physicalResumeRecognitionGap = 2 * time.Second

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
	resumedAfterIdle := false
	resumedAfterSilence := false
	if src == "Physical" {
		now := time.Now()
		gap := time.Duration(0)
		if !m.lastPhysicalAt.IsZero() {
			gap = now.Sub(m.lastPhysicalAt)
		}
		// A new session is one where the source has been None longer than
		// SessionGapThreshold — indicating the record stopped or the side was
		// flipped, not merely a normal inter-track silence gap.
		if m.lastPhysicalAt.IsZero() || gap > m.cfg.SessionGapThreshold {
			newSession = true
		} else if gap > m.cfg.IdleDelay {
			resumedAfterIdle = true
		} else if changed && gap >= physicalResumeRecognitionGap {
			// Manual stop/start within the same session (e.g. lifting the stylus and
			// placing it back at the beginning) should trigger recognition even though
			// the existing result is still present and duration-based boundary
			// suppression would otherwise block a fast re-start.
			resumedAfterSilence = true
		}
		m.lastPhysicalAt = now
	}
	if newSession {
		m.recognitionResult = nil
		m.physicalArtworkPath = ""
		m.physicalFormat = ""
		m.shazamContinuityReady = false
		m.lastContinuityMismatchAt = time.Time{}
		m.lastContinuityMismatchFrom = ""
		m.lastContinuityMismatchTo = ""
		m.physicalStartedAt = time.Now()
	} else if resumedAfterIdle {
		// The UI may have already gone idle during a longer pause. Reset the seek
		// anchor and queue a fresh recognition attempt on resume instead of waiting
		// solely for the VU boundary path to win the race.
		m.physicalStartedAt = time.Now()
		// Invalidate seek so the duration guard cannot suppress the first boundary
		// trigger after the needle is placed back on the record.
		m.physicalSeekMS = 0
		m.physicalSeekUpdatedAt = time.Time{}
	} else if resumedAfterSilence {
		m.physicalStartedAt = time.Now()
		// Same: invalidate seek on manual stop/start so the boundary guard is bypassed.
		m.physicalSeekMS = 0
		m.physicalSeekUpdatedAt = time.Time{}
	}
	needsTrigger := src == "Physical" && (m.recognitionResult == nil || resumedAfterIdle || resumedAfterSilence)
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
		// seekResetFrames is intentionally much smaller than silenceFrames:
		// we want to invalidate the seek position as soon as a brief silence appears
		// (indicating track end / needle lift) so the duration guard cannot suppress
		// the next silence→audio boundary trigger. The silence must still sustain for
		// silenceFrames before the boundary state machine commits inSilence=true.
		seekResetFrames = 5 // ~250 ms — enough to distinguish gap from glitch

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
		// likely still playing, to avoid re-recognition on silent passages within a track.
		// Triggers are allowed only after 75% of the reported duration has elapsed.
		// No hard cap — long tracks (e.g. 14-min epics) need the full window.
		const durationPessimism = 0.75
		m.mu.Lock()
		var durationMs int
		var seekMS int64
		var seekUpdatedAt time.Time
		if m.recognitionResult != nil {
			durationMs = m.recognitionResult.DurationMs
		}
		seekMS = m.physicalSeekMS
		seekUpdatedAt = m.physicalSeekUpdatedAt
		m.mu.Unlock()

		if durationMs > 0 && !seekUpdatedAt.IsZero() {
			elapsed := time.Duration(seekMS)*time.Millisecond + time.Since(seekUpdatedAt)
			trackDuration := time.Duration(durationMs) * time.Millisecond
			suppressUntil := time.Duration(float64(trackDuration) * durationPessimism)
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
			if silenceCount == seekResetFrames {
				// Reset seek suppression as soon as a brief silence is confirmed.
				// Inter-track gaps on vinyl can be shorter than silenceFrames (~1 s),
				// so we decouple the seek reset from the inSilence commit: 250 ms of
				// silence is sufficient to signal "current track ended"; we do not need
				// to wait for the full 1 s before disabling duration-based suppression.
				m.mu.Lock()
				m.physicalSeekMS = 0
				m.physicalSeekUpdatedAt = time.Time{}
				m.mu.Unlock()
			}
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
			if dipCount == seekResetFrames {
				// Energy dip confirmed (~250 ms) — reset seek suppression early,
				// just like we do at seekResetFrames consecutive silent frames.
				// This ensures the duration guard cannot suppress the boundary
				// trigger even if the dip is shorter than energyDipMinFrames.
				m.mu.Lock()
				m.physicalSeekMS = 0
				m.physicalSeekUpdatedAt = time.Time{}
				m.mu.Unlock()
			}
			if dipCount >= energyDipMinFrames && !hadDip {
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
