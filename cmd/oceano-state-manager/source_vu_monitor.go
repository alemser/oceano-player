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

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

const physicalResumeRecognitionGap = 2 * time.Second

const (
	// Keep this conservative to clear stale metadata after a real stop/needle-lift
	// while avoiding clears during typical inter-track pauses on physical media.
	staleSilenceKnownTrackClear          = 25 * time.Second
	staleSilenceKnownTrackProgressFactor = 0.90
	// Allow duration-guard bypass only near track start so a quick needle re-drop
	// can trigger recognition, but mid-track false positives still remain guarded.
	earlyBypassGuardWindow = 45 * time.Second
	// Source-detector restarts can briefly emit None while the capture device
	// reconnects; keep Physical stable for a short window to avoid idle flicker.
	transientNoneIgnoreWindow = 8 * time.Second
)

// effectiveDurationPessimism applies a conservative boost when the current library
// row is marked boundary-sensitive (quiet passages mistaken for track changes).
func effectiveDurationPessimism(base float64, boundarySensitive bool) float64 {
	const boost = 0.12
	const capVal = 0.98
	if base <= 0 || base > 1 {
		base = 0.75
	}
	if !boundarySensitive {
		return base
	}
	v := base + boost
	if v > capVal {
		return capVal
	}
	return v
}

func shouldSuppressBoundary(durationMs int, seekMS int64, seekUpdatedAt, bypassUntil, now time.Time, durationPessimism float64) bool {
	if durationMs <= 0 || seekUpdatedAt.IsZero() {
		return false
	}
	if durationPessimism <= 0 || durationPessimism > 1 {
		durationPessimism = 0.75 // fallback to default if invalid
	}
	elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
	if now.Before(bypassUntil) && elapsed <= earlyBypassGuardWindow {
		return false
	}
	trackDuration := time.Duration(durationMs) * time.Millisecond
	suppressUntil := time.Duration(float64(trackDuration) * durationPessimism)
	return elapsed < suppressUntil
}

func shouldIgnoreBoundaryAtMatureProgress(durationMs int, seekMS int64, seekUpdatedAt, now time.Time, durationPessimism float64) bool {
	if durationMs <= 0 || seekUpdatedAt.IsZero() {
		return false
	}
	if durationPessimism <= 0 || durationPessimism > 1 {
		durationPessimism = 0.75
	}
	elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
	trackDuration := time.Duration(durationMs) * time.Millisecond
	// Once elapsed passes the full track duration the track is definitively over;
	// stop suppressing so VU boundaries and the duration-exceeded trigger can fire.
	if elapsed >= trackDuration {
		return false
	}
	suppressUntil := time.Duration(float64(trackDuration) * durationPessimism)
	return elapsed >= suppressUntil
}

func shouldClearStaleRecognitionOnSilence(durationMs int, seekMS int64, seekUpdatedAt, now time.Time, silenceElapsed time.Duration) bool {
	if silenceElapsed <= 0 {
		return false
	}
	if durationMs > 0 && !seekUpdatedAt.IsZero() {
		if seekMS < 0 {
			seekMS = 0
		}
		delta := now.Sub(seekUpdatedAt).Milliseconds()
		if delta < 0 {
			delta = 0
		}
		elapsedMS := seekMS + delta
		minProgressMS := int64(float64(durationMs) * staleSilenceKnownTrackProgressFactor)
		if elapsedMS < minProgressMS {
			return false
		}
		return silenceElapsed >= staleSilenceKnownTrackClear
	}
	// Without duration+seek we cannot distinguish side flip from a quiet passage.
	// Keep the current track to avoid aggressive false clears.
	return false
}

func shouldSuppressBoundarySensitiveBoundary(durationMs int, seekMS int64, seekUpdatedAt, now time.Time, boundarySensitive bool, reason string) bool {
	if !boundarySensitive || durationMs <= 0 || seekUpdatedAt.IsZero() {
		return false
	}
	if reason == "duration-exceeded" {
		return false
	}
	elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
	trackDuration := time.Duration(durationMs) * time.Millisecond
	// Boundary-sensitive tracks with known duration stay locked until the known
	// track end so quiet passages do not trigger mid-track re-recognition.
	return elapsed < trackDuration
}

func (m *mgr) clearStalePhysicalRecognitionOnSilence(reason string, silenceElapsed time.Duration) bool {
	m.mu.Lock()
	if m.physicalSource != "Physical" || m.recognitionResult == nil {
		m.mu.Unlock()
		return false
	}
	artist := m.recognitionResult.Artist
	title := m.recognitionResult.Title
	m.recognitionResult = nil
	m.physicalArtworkPath = ""
	m.physicalLibraryEntryID = 0
	m.physicalBoundarySensitive = false
	m.shazamContinuityReady = false
	m.shazamContinuityAbandoned = false
	m.physicalSeekMS = 0
	m.physicalSeekUpdatedAt = time.Time{}
	m.mu.Unlock()

	log.Printf("VU monitor: cleared stale physical track after prolonged silence (%s, silence=%s, track=%s - %s)", reason, silenceElapsed.Round(time.Second), artist, title)
	m.markDirty()
	return true
}

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
	if src == "None" && m.physicalSource == "Physical" && !m.lastPhysicalAt.IsZero() {
		if sincePhysical := time.Since(m.lastPhysicalAt); sincePhysical >= 0 && sincePhysical <= transientNoneIgnoreWindow {
			m.mu.Unlock()
			return
		}
	}
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
		m.physicalBoundarySensitive = false
		m.physicalFormat = ""
		m.shazamContinuityReady = false
		m.lastContinuityMismatchAt = time.Time{}
		m.lastContinuityMismatchFrom = ""
		m.lastContinuityMismatchTo = ""
		m.lastContinuityMismatchCount = 0
		m.physicalStartedAt = time.Now()
	} else if resumedAfterIdle {
		m.recognitionResult = nil
		m.physicalArtworkPath = ""
		m.physicalBoundarySensitive = false
		m.physicalFormat = ""
		m.shazamContinuityReady = false
		m.lastContinuityMismatchAt = time.Time{}
		m.lastContinuityMismatchFrom = ""
		m.lastContinuityMismatchTo = ""
		m.lastContinuityMismatchCount = 0
		// The UI may have already gone idle during a longer pause. Reset the seek
		// anchor and queue a fresh recognition attempt on resume instead of waiting
		// solely for the VU boundary path to win the race.
		m.physicalStartedAt = time.Now()
		// Invalidate seek so the duration guard cannot suppress the first boundary
		// trigger after the needle is placed back on the record.
		m.physicalSeekMS = 0
		m.physicalSeekUpdatedAt = time.Time{}
	} else if resumedAfterSilence {
		// None→Physical within the same session (e.g. vinyl paused then CD started):
		// clear stale metadata so the UI does not keep the previous record while a
		// new capture runs. Needle-lift on the same album will briefly show empty
		// until the next match, which matches user expectation better than a wrong title.
		m.recognitionResult = nil
		m.physicalArtworkPath = ""
		m.physicalBoundarySensitive = false
		m.physicalFormat = ""
		m.shazamContinuityReady = false
		m.lastContinuityMismatchAt = time.Time{}
		m.lastContinuityMismatchFrom = ""
		m.lastContinuityMismatchTo = ""
		m.lastContinuityMismatchCount = 0
		m.physicalStartedAt = time.Now()
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
		case m.recognizeTrigger <- triggerPeriodicRecognition():
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
		silenceFrames = 22 // ~1 s of silence (vinyl inter-track gaps can be < 2 s)
		activeFrames  = 11 // ~0.5 s of audio resumption
		retryDelay    = 5 * time.Second
		// Rebuild detector settings periodically so telemetry nudges stay fresh
		// during long uptimes even when the VU socket remains connected.
		telemetryRefreshInterval = 24 * time.Hour
	)

	silenceThreshold := float32(m.cfg.VUSilenceThreshold)
	if silenceThreshold <= 0 {
		silenceThreshold = 0.01
	}

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
		log.Printf("VU monitor: thresholds silence=%.4f silenceFrames=%d activeFrames=%d", silenceThreshold, silenceFrames, activeFrames)
		durationGuardBypassWindow := m.cfg.DurationGuardBypassWindow
		if durationGuardBypassWindow <= 0 {
			durationGuardBypassWindow = 20 * time.Second
		}
		detectorCfg := defaultVUBoundaryDetectorConfig(silenceThreshold, silenceFrames, activeFrames)
		calModel, telemetryCfg, rmsLearn := loadBoundaryCalibrationModel(m.cfg.CalibrationConfigPath, silenceThreshold, m.currentPhysicalFormatForCalibration())
		calFormat := m.currentPhysicalFormatForCalibration()
		silNudge, pessNudge, telemetrySummary := computeTelemetryCalibrationNudges(m.lib, telemetryCfg, calFormat)
		m.mu.Lock()
		if !telemetryCfg.Enabled || m.lib == nil {
			m.telemetryDurationPessimismDelta = 0
		} else {
			m.telemetryDurationPessimismDelta = pessNudge
		}
		m.mu.Unlock()
		if telemetryCfg.Enabled && telemetrySummary != "" {
			log.Printf("VU monitor: R3 telemetry nudges — %s", telemetrySummary)
		}
		if silNudge != 0 {
			calModel.enterSilenceThreshold += silNudge
			calModel.exitSilenceThreshold += silNudge
			calModel.enterSilenceThreshold, calModel.exitSilenceThreshold = clampSilenceThresholdsToFloor(
				calModel.enterSilenceThreshold, calModel.exitSilenceThreshold, silenceThreshold,
			)
		}
		if calModel.enterSilenceThreshold > 0 {
			detectorCfg.silenceEnterThreshold = calModel.enterSilenceThreshold
		}
		if calModel.exitSilenceThreshold > 0 {
			detectorCfg.silenceExitThreshold = calModel.exitSilenceThreshold
		}
		if calModel.transitionGapRMS > 0 {
			detectorCfg.transitionGapRMS = calModel.transitionGapRMS
			detectorCfg.transitionMinMusicRMS = calModel.transitionMinMusicRMS
			if calModel.transitionGapDuration > 0 && calModel.transitionSamplesHz > 0 {
				expectedFrames := int(calModel.transitionGapDuration.Seconds() * float64(calModel.transitionSamplesHz))
				if expectedFrames > 0 {
					minFrames := int(float64(expectedFrames) * 0.5)
					if minFrames > detectorCfg.energyDipMinFrames {
						detectorCfg.energyDipMinFrames = minFrames
					}
					detectorCfg.energyDipMaxFrames = int(float64(expectedFrames)*2.2) + 4
				}
			}
		}
		if detectorCfg.energyDipMaxFrames > 0 && detectorCfg.energyDipMaxFrames < detectorCfg.energyDipMinFrames+4 {
			detectorCfg.energyDipMaxFrames = detectorCfg.energyDipMinFrames + 4
		}
		applyPersistedRMSLearningToDetector(m.lib, rmsLearn, silenceThreshold, calFormat, &detectorCfg)
		// Re-apply R3 silence nudge on top of profile- or RMS-learned enter/exit.
		if silNudge != 0 {
			detectorCfg.silenceEnterThreshold += silNudge
			detectorCfg.silenceExitThreshold += silNudge
			detectorCfg.silenceEnterThreshold, detectorCfg.silenceExitThreshold = clampSilenceThresholdsToFloor(
				detectorCfg.silenceEnterThreshold, detectorCfg.silenceExitThreshold, silenceThreshold,
			)
		}
		if calModel.profileID != "" {
			log.Printf("VU monitor: calibration profile=%s silenceEnter=%.4f silenceExit=%.4f gapRMS=%.4f gapDur=%s", calModel.profileID, detectorCfg.silenceEnterThreshold, detectorCfg.silenceExitThreshold, detectorCfg.transitionGapRMS, calModel.transitionGapDuration.Round(100*time.Millisecond))
		}
		var learner *rmsPercentileLearner
		if rmsLearn.Enabled && m.lib != nil {
			learner = newRMSPercentileLearner(m.lib, rmsLearn, silNudge)
		}
		refreshInterval := time.Duration(0)
		if telemetryCfg.Enabled {
			refreshInterval = telemetryRefreshInterval
		} else if rmsLearn.Enabled && m.lib != nil {
			// Reload calibration JSON + persisted RMS histograms periodically during long uptimes.
			refreshInterval = telemetryRefreshInterval
		}
		m.readVUFrames(ctx, conn, detectorCfg, durationGuardBypassWindow, m.effectiveDurationPessimismForPhysicalPolicy(), refreshInterval, learner, silenceThreshold)
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

func (m *mgr) currentPhysicalFormatForCalibration() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.physicalFormat != "" {
		return m.physicalFormat
	}
	if m.recognitionResult != nil && m.recognitionResult.Format != "" {
		return m.recognitionResult.Format
	}
	return ""
}

func (m *mgr) readVUFrames(ctx context.Context, conn net.Conn, detectorCfg vuBoundaryDetectorConfig, durationGuardBypassWindow time.Duration, durationPessimism float64, refreshInterval time.Duration, learner *rmsPercentileLearner, floorSilence float32) {
	// Reset silence state on reconnect — without a live stream we have no
	// reliable VU state; clear it so the display doesn't stay frozen as idle.
	m.mu.Lock()
	m.vuInSilence = false
	m.mu.Unlock()

	buf := make([]byte, 8)
	staleSilenceCleared := false
	detector := newVUBoundaryDetector(detectorCfg)
	durationGuardBypassUntil := time.Time{}
	// Keyed on physicalSeekUpdatedAt at the time of firing; auto-resets when a new
	// track is confirmed (recognition sets a fresh seekUpdatedAt). Avoids requiring
	// an explicit reset signal for fully gapless albums where no VU boundary fires.
	var durationExceededFiredForSeek time.Time
	connectedAt := time.Now()

	fireBoundaryTrigger := func(reason string, isHardBoundary bool, detectedAt time.Time) {
		m.mu.Lock()
		var durationMs int
		var seekMS int64
		var seekUpdatedAt time.Time
		continuityReady := m.shazamContinuityReady
		boundarySensitive := m.physicalBoundarySensitive
		if m.recognitionResult != nil {
			durationMs = m.recognitionResult.DurationMs
		}
		seekMS = m.physicalSeekMS
		seekUpdatedAt = m.physicalSeekUpdatedAt
		m.mu.Unlock()

		effPess := effectiveDurationPessimism(durationPessimism, boundarySensitive)
		now := time.Now()
		if shouldSuppressBoundarySensitiveBoundary(durationMs, seekMS, seekUpdatedAt, now, boundarySensitive, reason) {
			elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
			trackDuration := time.Duration(durationMs) * time.Millisecond
			log.Printf("VU monitor: boundary suppressed (%s) — boundary-sensitive lock active (%s < %s)",
				reason, elapsed.Round(time.Second), trackDuration.Round(time.Second))
			m.recordBoundaryTelemetry(internallibrary.BoundaryOutcomeSuppressedDurationGuard, reason, isHardBoundary)
			return
		}
		if shouldSuppressBoundary(durationMs, seekMS, seekUpdatedAt, durationGuardBypassUntil, now, effPess) {
			elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
			trackDuration := time.Duration(durationMs) * time.Millisecond
			suppressUntil := time.Duration(float64(trackDuration) * effPess)
			log.Printf("VU monitor: boundary suppressed (%s) — %s elapsed, checks active from %s (track %s)",
				reason, elapsed.Round(time.Second), suppressUntil.Round(time.Second), trackDuration.Round(time.Second))
			m.recordBoundaryTelemetry(internallibrary.BoundaryOutcomeSuppressedDurationGuard, reason, isHardBoundary)
			return
		}
		if shouldIgnoreBoundaryAtMatureProgress(durationMs, seekMS, seekUpdatedAt, now, effPess) {
			elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
			trackDuration := time.Duration(durationMs) * time.Millisecond
			suppressUntil := time.Duration(float64(trackDuration) * normalizedDurationPessimism(effPess))
			if continuityReady {
				log.Printf("VU monitor: boundary ignored (%s) — continuity monitor preferred at mature progress (%s >= %s, track %s)",
					reason, elapsed.Round(time.Second), suppressUntil.Round(time.Second), trackDuration.Round(time.Second))
			} else {
				log.Printf("VU monitor: boundary ignored (%s) — mature progress guard active (%s >= %s, track %s)",
					reason, elapsed.Round(time.Second), suppressUntil.Round(time.Second), trackDuration.Round(time.Second))
			}
			m.recordBoundaryTelemetry(internallibrary.BoundaryOutcomeIgnoredMatureProgress, reason, isHardBoundary)
			return
		}

		durationGuardBypassUntil = time.Time{}

		// Suppress if source is not currently Physical. The VU socket publishes
		// frames regardless of source state, so silence→audio transitions can
		// fire after the needle is lifted or the CD stops. Triggering recognition
		// with no physical source wastes an API call and poisons the retry state.
		m.mu.Lock()
		isSourcePhysical := m.physicalSource == "Physical"
		m.mu.Unlock()
		if !isSourcePhysical {
			log.Printf("VU monitor: boundary suppressed (%s) — source is not Physical", reason)
			m.recordBoundaryTelemetry(internallibrary.BoundaryOutcomeSuppressedNotPhysical, reason, isHardBoundary)
			return
		}

		log.Printf("VU monitor: track boundary detected (%s hard=%v) — triggering recognition", reason, isHardBoundary)
		var evID int64
		if m.lib != nil {
			ev := m.buildBoundaryEvent(internallibrary.BoundaryOutcomeFired, reason, isHardBoundary)
			var err error
			evID, err = m.lib.RecordBoundaryEvent(ev)
			if err != nil {
				log.Printf("boundary telemetry: %v", err)
				evID = 0
			}
		}
		select {
		case m.recognizeTrigger <- recognizeTrigger{
			isBoundary: true, isHardBoundary: isHardBoundary, detectedAt: detectedAt,
			boundaryEventID: evID,
		}:
		default:
			if evID > 0 && m.lib != nil {
				if err := m.lib.ConvertBoundaryEventOutcome(evID,
					internallibrary.BoundaryOutcomeTriggerChannelFull, reason, isHardBoundary); err != nil {
					log.Printf("boundary telemetry: %v", err)
				}
			} else {
				m.recordBoundaryTelemetry(internallibrary.BoundaryOutcomeTriggerChannelFull, reason, isHardBoundary)
			}
		}
	}

	go func() {
		<-ctx.Done()
		conn.SetDeadline(time.Now())
	}()

	for {
		if refreshInterval > 0 && time.Since(connectedAt) >= refreshInterval {
			log.Printf("VU monitor: refreshing detector settings after %s uptime", refreshInterval)
			return
		}
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		now := time.Now()
		left := math.Float32frombits(binary.LittleEndian.Uint32(buf[0:4]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(buf[4:8]))
		avg := (left + right) / 2
		out := detector.Feed(avg, now)
		if learner != nil {
			learner.observe(m, avg, out, floorSilence, detector)
		}

		if out.armDurationBypass {
			durationGuardBypassUntil = now.Add(durationGuardBypassWindow)
		}
		if out.enteredSilence {
			log.Printf("VU monitor: silence detected")
			m.mu.Lock()
			m.vuInSilence = true
			m.mu.Unlock()
			m.markDirty()
		}
		if out.resumedFromSilence {
			staleSilenceCleared = false
			m.mu.Lock()
			m.vuInSilence = false
			m.mu.Unlock()
			m.markDirty()
		}
		if !out.inSilence {
			staleSilenceCleared = false
		}

		if out.inSilence && !staleSilenceCleared && out.silenceElapsed > 0 {
			m.mu.Lock()
			durationMS := 0
			seekMS := int64(0)
			seekUpdatedAt := time.Time{}
			boundarySensitive := false
			if m.recognitionResult != nil {
				durationMS = m.recognitionResult.DurationMs
				seekMS = m.physicalSeekMS
				seekUpdatedAt = m.physicalSeekUpdatedAt
				boundarySensitive = m.physicalBoundarySensitive
			}
			m.mu.Unlock()
			if boundarySensitive && durationMS > 0 && !seekUpdatedAt.IsZero() {
				elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
				trackDuration := time.Duration(durationMS) * time.Millisecond
				if elapsed < trackDuration {
					continue
				}
			}
			if shouldClearStaleRecognitionOnSilence(durationMS, seekMS, seekUpdatedAt, now, out.silenceElapsed) {
				if m.clearStalePhysicalRecognitionOnSilence("prolonged-silence", out.silenceElapsed) {
					staleSilenceCleared = true
				}
			}
		}

		if out.boundary {
			durationExceededFiredForSeek = time.Time{} // VU boundary = new track; reset so next track can fire too
			fireBoundaryTrigger(out.boundaryType, out.boundaryHard, time.Time{})
		}
		if out.energySuppressedByCooldown {
			log.Printf("VU monitor: energy-change boundary suppressed (cooldown active)")
			m.recordBoundaryTelemetry(internallibrary.BoundaryOutcomeEnergyChangeCooldown, "energy-change", false)
		}

		// Duration-exceeded trigger: when elapsed time passes the known track
		// duration (plus a grace margin), fire a hard recognition even if no
		// VU boundary was detected — handles gapless/live albums where tracks
		// blend without a clear silence or energy dip.
		//
		// The fired flag is keyed on physicalSeekUpdatedAt so it auto-resets when
		// a new track is recognised (recognition sets a fresh seekUpdatedAt), enabling
		// the trigger to fire for every subsequent gapless track without requiring a
		// VU boundary reset.
		//
		// Guard: skip in the same frame a VU boundary already fired — the boundary
		// just reset durationExceededFiredForSeek, so without this guard both triggers
		// fire simultaneously on every silence→audio transition when duration is exceeded.
		if !out.inSilence && !out.boundary {
			m.mu.Lock()
			var dxDurationMs int
			var dxSeekMS int64
			var dxSeekUpdatedAt time.Time
			if m.recognitionResult != nil {
				dxDurationMs = m.recognitionResult.DurationMs
				dxSeekMS = m.physicalSeekMS
				dxSeekUpdatedAt = m.physicalSeekUpdatedAt
			}
			m.mu.Unlock()

			if dxDurationMs > 0 && !dxSeekUpdatedAt.IsZero() && durationExceededFiredForSeek != dxSeekUpdatedAt {
				const durationExceededGrace = 10 * time.Second
				elapsed := time.Duration(dxSeekMS)*time.Millisecond + now.Sub(dxSeekUpdatedAt)
				trackDuration := time.Duration(dxDurationMs) * time.Millisecond
				if elapsed >= trackDuration+durationExceededGrace {
					overrun := (elapsed - trackDuration).Round(time.Second)
					log.Printf("VU monitor: track duration exceeded by %s — firing hard recognition trigger", overrun)
					durationExceededFiredForSeek = dxSeekUpdatedAt
					// Pass the theoretical track-end time as detectedAt so the
					// recognition coordinator anchors seek to the actual boundary,
					// not the moment the trigger was processed (~10 s later).
					trackEndTime := dxSeekUpdatedAt.Add(trackDuration - time.Duration(dxSeekMS)*time.Millisecond)
					fireBoundaryTrigger("duration-exceeded", true, trackEndTime)
				}
			}
		}
	}
}
