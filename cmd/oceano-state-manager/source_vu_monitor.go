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

const (
	// Keep this conservative to clear stale metadata after a real stop/needle-lift
	// while avoiding clears during typical inter-track pauses on physical media.
	staleSilenceKnownTrackClear          = 25 * time.Second
	staleSilenceKnownTrackProgressFactor = 0.90
	// Allow duration-guard bypass only near track start so a quick needle re-drop
	// can trigger recognition, but mid-track false positives still remain guarded.
	earlyBypassGuardWindow = 45 * time.Second
)

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
	changed := m.physicalSource != src
	newSession := false
	resumedAfterIdle := false
	resumedAfterSilence := false

	// Always run session detection and update lastPhysicalAt when source is Physical,
	// regardless of whether a recent result exists. Skipping this would:
	//   1. Leave m.lastPhysicalAt stale → idle delay stops working after recognition
	//   2. Prevent newSession detection when medium changes (CD after vinyl)
	if src == "Physical" {
		now := time.Now()
		gap := time.Duration(0)
		if !m.lastPhysicalAt.IsZero() {
			gap = now.Sub(m.lastPhysicalAt)
		}
		if m.lastPhysicalAt.IsZero() || gap > m.cfg.SessionGapThreshold {
			newSession = true
		} else if gap > m.cfg.IdleDelay {
			resumedAfterIdle = true
		} else if changed && gap >= physicalResumeRecognitionGap {
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
		m.lastContinuityMismatchCount = 0
		m.physicalStartedAt = time.Now()
		m.physicalSeekMS = 0
		m.physicalSeekUpdatedAt = time.Time{}
	} else if resumedAfterIdle {
		m.recognitionResult = nil
		m.physicalArtworkPath = ""
		m.physicalFormat = ""
		m.shazamContinuityReady = false
		m.lastContinuityMismatchAt = time.Time{}
		m.lastContinuityMismatchFrom = ""
		m.lastContinuityMismatchTo = ""
		m.lastContinuityMismatchCount = 0
		m.physicalStartedAt = time.Now()
		m.physicalSeekMS = 0
		m.physicalSeekUpdatedAt = time.Time{}
	} else if resumedAfterSilence {
		m.physicalStartedAt = time.Now()
		// Do NOT clear physicalSeekMS/physicalSeekUpdatedAt here — clearing them
		// causes the duration guards to skip, firing a trigger on every a-cappella
		// breath or soft passage.
	}

	// Cooldown after a failed/discarded recognition attempt.
	const triggerCooldown = 20 * time.Second
	recentFailure := !m.lastRecognitionAttemptAt.IsZero() && time.Now().Before(m.lastRecognitionAttemptAt.Add(triggerCooldown))

	// Cooldown after a successful recognition — suppresses re-triggers during quiet
	// passages or brief pauses within the same track. Does NOT apply to new sessions
	// (medium change): those must always trigger regardless of recent success.
	const recentSuccessCooldown = 120 * time.Second
	recentSuccess := !m.lastRecognizedAt.IsZero() && time.Now().Before(m.lastRecognizedAt.Add(recentSuccessCooldown))

	needsTrigger := src == "Physical" &&
		(m.recognitionResult == nil || newSession || resumedAfterIdle || resumedAfterSilence) &&
		!recentFailure &&
		!(recentSuccess && !newSession) // new sessions always trigger even if recently recognized
	m.physicalSource = src
	m.mu.Unlock()

	if changed {
		log.Printf("physical source: %s", src)
		m.markDirty()
	} else if src == "Physical" {
		m.markDirty()
	}

	if needsTrigger {
		var trig recognizeTrigger
		if newSession || resumedAfterIdle {
			trig = triggerBoundaryRecognition(true)
		} else {
			trig = triggerPeriodicRecognition()
		}
		select {
		case m.recognizeTrigger <- trig:
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
	)

	// Use the same threshold as source-detector for consistency.
	// Previously used 0.01 which was higher than source-detector's 0.005,
	// causing false silence detection during quiet passages.
	silenceThreshold := float32(m.cfg.VUSilenceThreshold)
	// Try to get VU threshold from source-detector's learned noise floor.
	// If available, use its RMS threshold for consistency with source-detector.
	vuThresholdFromFile := m.loadNoiseFloorFromSourceDetector()
	if vuThresholdFromFile > 0 {
		silenceThreshold = vuThresholdFromFile
		log.Printf("VU monitor: using learned threshold from source-detector: %.5f", silenceThreshold)
	} else if silenceThreshold <= 0 {
		silenceThreshold = 0.005 // Match source-detector's default RMS threshold
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
		calModel := loadBoundaryCalibrationModel(m.cfg.CalibrationConfigPath, silenceThreshold, m.currentPhysicalFormatForCalibration())
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
		if calModel.profileID != "" {
			log.Printf("VU monitor: calibration profile=%s silenceEnter=%.4f silenceExit=%.4f gapRMS=%.4f gapDur=%s", calModel.profileID, detectorCfg.silenceEnterThreshold, detectorCfg.silenceExitThreshold, detectorCfg.transitionGapRMS, calModel.transitionGapDuration.Round(100*time.Millisecond))
		}
		m.readVUFrames(ctx, conn, detectorCfg, durationGuardBypassWindow, m.cfg.DurationPessimism)
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

// loadNoiseFloorFromSourceDetector reads the learned noise floor from
// source-detector's calibration file and extracts the RMS threshold.
// Returns 0 if the file is not found or invalid.
func (m *mgr) loadNoiseFloorFromSourceDetector() float32 {
	if m.cfg.CalibrationConfigPath == "" {
		return 0
	}
	data, err := os.ReadFile(m.cfg.CalibrationConfigPath)
	if err != nil {
		return 0
	}
	var nf struct {
		RMS   float64 `json:"rms"`
		StdDev float64 `json:"stddev"`
	}
	if err := json.Unmarshal(data, &nf); err != nil || nf.RMS <= 0 {
		return 0
	}
	// Use RMS + 4*StdDev to match source-detector's threshold formula
	const rmsMultiplier = 4.0
	threshold := nf.RMS + nf.StdDev*rmsMultiplier
	return float32(threshold)
}

func (m *mgr) readVUFrames(ctx context.Context, conn net.Conn, detectorCfg vuBoundaryDetectorConfig, durationGuardBypassWindow time.Duration, durationPessimism float64) {
	buf := make([]byte, 8)
	staleSilenceCleared := false
	detector := newVUBoundaryDetector(detectorCfg)
	durationGuardBypassUntil := time.Time{}
	// Keyed on physicalSeekUpdatedAt at the time of firing; auto-resets when a new
	// track is confirmed (recognition sets a fresh seekUpdatedAt). Avoids requiring
	// an explicit reset signal for fully gapless albums where no VU boundary fires.
	var durationExceededFiredForSeek time.Time

	fireBoundaryTrigger := func(reason string, isHardBoundary bool, detectedAt time.Time) {
		m.mu.Lock()
		var durationMs int
		var seekMS int64
		var seekUpdatedAt time.Time
		var lastRecognizedAt time.Time
		continuityReady := m.shazamContinuityReady
		if m.recognitionResult != nil {
			durationMs = m.recognitionResult.DurationMs
		}
		seekMS = m.physicalSeekMS
		seekUpdatedAt = m.physicalSeekUpdatedAt
		lastRecognizedAt = m.lastRecognizedAt
		m.mu.Unlock()

		// Do not allow the duration-guard bypass to override suppression while
		// the current track is identified and elapsed is inside the suppression
		// window. The bypass exists for quick needle re-drops (which arrive as
		// boundaries with a still-active previous track's seek state); it must
		// not fire for quiet sections (a cappella phrases, soft passages) that
		// fall within the first 45 s of an already-identified track.
		bypassUntil := durationGuardBypassUntil
		if durationMs > 0 && !seekUpdatedAt.IsZero() {
			suppressUntilMS := int64(float64(durationMs) * durationPessimism)
			elapsed := seekMS + int64(time.Since(seekUpdatedAt).Milliseconds())
			if elapsed < suppressUntilMS {
				bypassUntil = time.Time{}
			}
		}

		now := time.Now()
		if shouldSuppressBoundary(durationMs, seekMS, seekUpdatedAt, bypassUntil, now, durationPessimism) {
			elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
			trackDuration := time.Duration(durationMs) * time.Millisecond
			suppressUntil := time.Duration(float64(trackDuration) * durationPessimism)
			log.Printf("VU monitor: boundary suppressed (%s) — %s elapsed, checks active from %s (track %s)",
				reason, elapsed.Round(time.Second), suppressUntil.Round(time.Second), trackDuration.Round(time.Second))
			return
		}
		if shouldIgnoreBoundaryAtMatureProgress(durationMs, seekMS, seekUpdatedAt, now, durationPessimism) {
			elapsed := time.Duration(seekMS)*time.Millisecond + now.Sub(seekUpdatedAt)
			trackDuration := time.Duration(durationMs) * time.Millisecond
			suppressUntil := time.Duration(float64(trackDuration) * normalizedDurationPessimism(durationPessimism))
			if continuityReady {
				log.Printf("VU monitor: boundary ignored (%s) — continuity monitor preferred at mature progress (%s >= %s, track %s)",
					reason, elapsed.Round(time.Second), suppressUntil.Round(time.Second), trackDuration.Round(time.Second))
			} else {
				log.Printf("VU monitor: boundary ignored (%s) — mature progress guard active (%s >= %s, track %s)",
					reason, elapsed.Round(time.Second), suppressUntil.Round(time.Second), trackDuration.Round(time.Second))
			}
			return
		}

		// Fallback for tracks where the duration is not known (live recordings,
		// certain a cappella tracks). Without durationMs the time-based guards
		// above are disabled, so any VU boundary would trigger re-recognition.
		// Once a result exists, suppress VU boundaries for RecognizerMaxInterval
		// after the last successful recognition — the fallback timer provides the
		// periodic refresh without false positives from energy-change events or
		// quiet sections triggering the silence→audio detector.
		if durationMs == 0 && !seekUpdatedAt.IsZero() && !lastRecognizedAt.IsZero() {
			if sinceLastRecog := time.Since(lastRecognizedAt); sinceLastRecog < m.cfg.RecognizerMaxInterval {
				remaining := (m.cfg.RecognizerMaxInterval - sinceLastRecog).Round(time.Second)
				log.Printf("VU monitor: boundary suppressed (%s) — result held with unknown duration (re-recognition in %s)", reason, remaining)
				return
			}
		}

		durationGuardBypassUntil = time.Time{}

		const boundaryCooldown = 20 * time.Second
		m.mu.Lock()
		hasValidResult := m.recognitionResult != nil
		isSourcePhysical := m.physicalSource == "Physical"
		recentAttemptFailed := !m.lastRecognitionAttemptAt.IsZero() && time.Now().Before(m.lastRecognitionAttemptAt.Add(boundaryCooldown))
		m.mu.Unlock()

		// Never trigger recognition from VU boundaries when source is not Physical.
		// The VU socket publishes frames continuously regardless of source state, so
		// silence→audio transitions can fire even after the needle is lifted. Triggering
		// recognition when there is no physical source would discard the result (source
		// changed during capture) and set lastRecognitionAttemptAt, which blocks the
		// 20 s cooldown from firing on the real next-track boundary.
		if !isSourcePhysical {
			log.Printf("VU monitor: boundary suppressed (%s) — source is not Physical", reason)
			return
		}

		if recentAttemptFailed || (hasValidResult && !isHardBoundary) {
			if recentAttemptFailed {
				log.Printf("VU monitor: boundary suppressed (%s) — recognition cooldown active", reason)
			} else {
				log.Printf("VU monitor: boundary suppressed (%s) — already have result and not hard boundary", reason)
			}
			return
		}

		log.Printf("VU monitor: track boundary detected (%s hard=%v) — triggering recognition", reason, isHardBoundary)
		select {
		case m.recognizeTrigger <- recognizeTrigger{isBoundary: true, isHardBoundary: isHardBoundary, detectedAt: detectedAt}:
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
		now := time.Now()
		left := math.Float32frombits(binary.LittleEndian.Uint32(buf[0:4]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(buf[4:8]))
		avg := (left + right) / 2
		out := detector.Feed(avg, now)

		if out.armDurationBypass {
			durationGuardBypassUntil = now.Add(durationGuardBypassWindow)
		}
		if out.enteredSilence {
			log.Printf("VU monitor: silence detected")
		}
		if out.resumedFromSilence {
			staleSilenceCleared = false
		}
		if !out.inSilence {
			staleSilenceCleared = false
		}

		if out.inSilence && !staleSilenceCleared && out.silenceElapsed > 0 {
			m.mu.Lock()
			durationMS := 0
			seekMS := int64(0)
			seekUpdatedAt := time.Time{}
			if m.recognitionResult != nil {
				durationMS = m.recognitionResult.DurationMs
				seekMS = m.physicalSeekMS
				seekUpdatedAt = m.physicalSeekUpdatedAt
			}
			m.mu.Unlock()
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
		if !out.inSilence {
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
