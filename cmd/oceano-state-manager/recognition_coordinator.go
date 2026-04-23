package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

type recognitionCoordinator struct {
	mgr        *mgr
	rec        Recognizer
	confirmRec Recognizer
	shazamRec  Recognizer
	lib        *internallibrary.Library
}

func newRecognitionCoordinator(m *mgr, rec Recognizer, confirmRec Recognizer, shazamRec Recognizer, lib *internallibrary.Library) *recognitionCoordinator {
	return &recognitionCoordinator{
		mgr:        m,
		rec:        rec,
		confirmRec: confirmRec,
		shazamRec:  shazamRec,
		lib:        lib,
	}
}

func (c *recognitionCoordinator) primaryRecognizer() Recognizer {
	if chain, ok := c.rec.(*ChainRecognizer); ok && chain.Primary() != nil {
		return chain.Primary()
	}
	return c.rec
}

func isPhysicalFormat(format string) bool {
	f := strings.ToLower(strings.TrimSpace(format))
	return f == "cd" || f == "vinyl"
}

func isNewTrackCandidate(result *RecognitionResult, currentACRID, currentShazamID string) bool {
	if result == nil {
		return false
	}
	if result.ACRID != "" {
		return result.ACRID != currentACRID
	}
	if result.ShazamID != "" {
		return result.ShazamID != currentShazamID
	}
	return currentACRID == "" && currentShazamID == ""
}

func shouldBypassBackoff(isBoundaryTrigger, backoffRateLimited bool) bool {
	return isBoundaryTrigger && !backoffRateLimited
}

func shouldSkipRecognitionAttempt(isPhysical, isAirPlay, isBluetooth bool) bool {
	return !isPhysical || isAirPlay || isBluetooth
}

func recognitionLogFields(result *RecognitionResult) (string, string) {
	source := "unknown"
	score := fmt.Sprintf("score=%d", result.Score)
	if result.ACRID != "" {
		source = "acrcloud"
	} else if result.ShazamID != "" {
		source = "shazam"
		if result.Score == 0 {
			score = "score=n/a"
		}
	}
	return source, score
}

func cloneRecognitionResult(r *RecognitionResult) *RecognitionResult {
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}

func mergeMissingProviderIDs(dst, src *RecognitionResult) bool {
	if dst == nil || src == nil {
		return false
	}
	changed := false
	if dst.ACRID == "" && src.ACRID != "" {
		dst.ACRID = src.ACRID
		changed = true
	}
	if dst.ShazamID == "" && src.ShazamID != "" {
		dst.ShazamID = src.ShazamID
		changed = true
	}
	return changed
}

func computeRecognizedSeekMS(isBoundaryTrigger bool, captureStartedAt, now, lastBoundaryForSeek, physStartedAt time.Time, previousResult, newResult *RecognitionResult) (int64, bool) {
	if isBoundaryTrigger {
		seekMS := now.Sub(captureStartedAt).Milliseconds()
		if seekMS < 0 {
			seekMS = 0
		}
		if !lastBoundaryForSeek.IsZero() {
			if better := now.Sub(lastBoundaryForSeek).Milliseconds(); better > seekMS {
				seekMS = better
			}
		}
		return seekMS, true
	}

	seekMS := now.Sub(captureStartedAt).Milliseconds()
	if seekMS < 0 {
		seekMS = 0
	}

	// Use physStartedAt to account for recognition delay (capture + network latency)
	// when re-confirming the same track OR on the first recognition of a new session
	// (no previous result). In both cases the session anchor is valid.
	// For a genuinely different track found without a boundary event, only use the
	// capture elapsed time so the new track does not inherit the old session's elapsed.
	isSameTrack := sameTrackForStateContinuity(previousResult, newResult)
	isFirstRecognition := previousResult == nil
	if !physStartedAt.IsZero() && (isSameTrack || isFirstRecognition) {
		if better := now.Sub(physStartedAt).Milliseconds(); better > seekMS {
			seekMS = better
		}
		return seekMS, !isSameTrack
	}

	return seekMS, true
}

func (c *recognitionCoordinator) drainPendingTriggers() int {
	drained := 0
	for {
		select {
		case <-c.mgr.recognizeTrigger:
			drained++
		default:
			return drained
		}
	}
}

func (c *recognitionCoordinator) handleRecognitionError(err error, backoffUntil *time.Time, backoffRateLimited *bool) bool {
	log.Printf("recognizer [%s]: error: %v", c.rec.Name(), err)
	if errors.Is(err, ErrRateLimit) {
		const rateLimitBackoff = 5 * time.Minute
		log.Printf("recognizer [%s]: rate limited — backing off %s", c.rec.Name(), rateLimitBackoff)
		*backoffUntil = time.Now().Add(rateLimitBackoff)
		*backoffRateLimited = true
		return true
	}
	const errorBackoff = 30 * time.Second
	*backoffUntil = time.Now().Add(errorBackoff)
	*backoffRateLimited = false
	return true
}

func (c *recognitionCoordinator) handleNoMatch(isBoundaryTrigger bool, isHardBoundaryTrigger bool, backoffUntil *time.Time, backoffRateLimited *bool) {
	noMatchBackoff := c.mgr.cfg.NoMatchBackoff
	if noMatchBackoff <= 0 {
		noMatchBackoff = 15 * time.Second
	}
	log.Printf("recognizer [%s]: no match — retrying in %s", c.rec.Name(), noMatchBackoff)

	if isBoundaryTrigger && isHardBoundaryTrigger {
		c.mgr.mu.Lock()
		c.mgr.recognitionResult = nil
		c.mgr.physicalArtworkPath = ""
		c.mgr.physicalLibraryEntryID = 0
		c.mgr.shazamContinuityReady = false
		c.mgr.shazamContinuityAbandoned = false
		c.mgr.mu.Unlock()
	}

	*backoffUntil = time.Now().Add(noMatchBackoff)
	*backoffRateLimited = false
}

func (c *recognitionCoordinator) maybeConfirmCandidate(ctx context.Context, result *RecognitionResult, isBoundaryTrigger bool) (bool, bool) {
	if c.mgr.cfg.ConfirmationDelay <= 0 {
		return false, false
	}

	c.mgr.mu.Lock()
	currentACRID := ""
	currentShazamID := ""
	if c.mgr.recognitionResult != nil {
		currentACRID = c.mgr.recognitionResult.ACRID
		currentShazamID = c.mgr.recognitionResult.ShazamID
	}
	c.mgr.mu.Unlock()

	if !isNewTrackCandidate(result, currentACRID, currentShazamID) {
		return false, false
	}

	if c.mgr.cfg.ConfirmationBypassScore > 0 && result.Score >= c.mgr.cfg.ConfirmationBypassScore {
		if c.mgr.cfg.Verbose {
			log.Printf("recognizer [%s]: high-confidence match (score=%d) — skipping confirmation", c.rec.Name(), result.Score)
		}
		return false, false
	}
	if isBoundaryTrigger {
		if c.mgr.cfg.Verbose {
			log.Printf("recognizer [%s]: boundary-triggered recognition — skipping confirmation delay", c.rec.Name())
		}
		return false, false
	}

	log.Printf("recognizer [%s]: new track candidate — confirming in %s", c.rec.Name(), c.mgr.cfg.ConfirmationDelay)
	select {
	case <-ctx.Done():
		return false, true
	case <-time.After(c.mgr.cfg.ConfirmationDelay):
	}

	confirmer := c.confirmRec
	if confirmer == nil {
		confirmer = c.rec
	}

	confDur := c.mgr.cfg.ConfirmationCaptureDuration
	if confDur <= 0 {
		confDur = c.mgr.cfg.RecognizerCaptureDuration
	}
	confCtx, confCancel := context.WithTimeout(ctx, confDur+10*time.Second)
	confWav, confErr := captureFromPCMSocket(confCtx, c.mgr.cfg.PCMSocket, confDur, 0, os.TempDir())
	confCancel()

	if confErr != nil {
		if ctx.Err() != nil {
			return false, true
		}
		log.Printf("recognizer [%s]: confirmation capture error — accepting original result: %v", c.rec.Name(), confErr)
		return false, false
	}

	confCtx2, confCancel2 := context.WithTimeout(ctx, confDur+10*time.Second)
	var conf *RecognitionResult
	var confRecErr error
	confProviderName := confirmer.Name()
	if c.confirmRec != nil && c.confirmRec != c.rec {
		type recOut struct {
			res *RecognitionResult
			err error
		}
		primaryRec := c.primaryRecognizer()
		pCh := make(chan recOut, 1)
		sCh := make(chan recOut, 1)
		go func() {
			r, e := primaryRec.Recognize(confCtx2, confWav)
			pCh <- recOut{res: r, err: e}
		}()
		go func() {
			r, e := c.confirmRec.Recognize(confCtx2, confWav)
			sCh <- recOut{res: r, err: e}
		}()
		pOut := <-pCh
		sOut := <-sCh
		conf, confRecErr, confProviderName = chooseConfirmationResult(
			primaryRec.Name(), pOut.res, pOut.err,
			c.confirmRec.Name(), sOut.res, sOut.err,
		)
	} else {
		conf, confRecErr = confirmer.Recognize(confCtx2, confWav)
	}
	confCancel2()
	os.Remove(confWav)
	if ctx.Err() != nil {
		return false, true
	}
	if confRecErr != nil {
		log.Printf("recognizer [%s]: confirmation (%s) error — accepting original result: %v", c.rec.Name(), confProviderName, confRecErr)
		return false, false
	}
	if conf == nil {
		log.Printf("recognizer [%s]: confirmation (%s) returned no match — keeping original candidate %s — %s",
			c.rec.Name(), confProviderName, result.Artist, result.Title)
		return false, false
	}

	sameTrack := confProviderName == c.rec.Name() && conf.ACRID != "" && conf.ACRID == result.ACRID
	if !sameTrack {
		sameTrack = tracksEquivalent(conf.Title, conf.Artist, result.Title, result.Artist)
	}
	if !sameTrack {
		log.Printf("recognizer [%s]: confirmation (%s) disagrees (got %s — %s) — keeping original candidate %s — %s",
			c.rec.Name(), confProviderName, conf.Artist, conf.Title, result.Artist, result.Title)
		return false, false
	}

	log.Printf("recognizer [%s]: confirmed by %s — %s — %s", c.rec.Name(), confProviderName, result.Artist, result.Title)
	if c.shazamRec != nil && confProviderName == c.shazamRec.Name() {
		if result.ShazamID == "" {
			result.ShazamID = conf.ShazamID
		}
		return true, false
	}
	return false, false
}

func (c *recognitionCoordinator) applyRecognizedResult(result *RecognitionResult, isBoundaryTrigger bool, isShazamFallback bool, shazamMatchedACR bool, captureStartedAt time.Time) {
	if c.lib != nil {
		artworkPath := ""
		if entry, lookupErr := c.lib.LookupByIDs(result.ACRID, result.ShazamID); lookupErr != nil {
			log.Printf("recognizer: library lookup error: %v", lookupErr)
		} else if entry != nil {
			if c.mgr.cfg.Verbose {
				log.Printf("recognizer: known track (plays: %d) — using saved metadata", entry.PlayCount)
			}
			result.Title = entry.Title
			result.Artist = entry.Artist
			result.Album = entry.Album
			result.Format = entry.Format
			if result.ShazamID == "" {
				result.ShazamID = entry.ShazamID
			}
			if entry.DurationMs > 0 {
				result.DurationMs = entry.DurationMs
			}
			artworkPath = entry.ArtworkPath
		}

		if artworkPath == "" && result.Album != "" {
			if ap, artErr := fetchArtwork(result.Artist, result.Album, c.mgr.cfg.ArtworkDir); artErr != nil {
				log.Printf("recognizer: artwork fetch error: %v", artErr)
			} else if ap != "" {
				log.Printf("recognizer: artwork saved at %s", ap)
				artworkPath = ap
			}
		}

		entryID, recErr := c.lib.RecordPlay(result, artworkPath)
		if recErr != nil {
			log.Printf("recognizer: library record error: %v", recErr)
		} else if entryID > 0 {
			// Read back the final entry after RecordPlay to pick up any library
			// metadata applied by equivalent-metadata merge (e.g. when ACRCloud
			// returns a different ACRID for a track the user already edited).
			// This prevents a brief flash of provider data before syncFromLibrary runs.
			if finalEntry, _ := c.lib.GetByID(entryID); finalEntry != nil {
				if finalEntry.Title != "" {
					result.Title = finalEntry.Title
				}
				if finalEntry.Artist != "" {
					result.Artist = finalEntry.Artist
				}
				result.Album = finalEntry.Album
				result.Format = finalEntry.Format
				if finalEntry.ShazamID != "" {
					result.ShazamID = finalEntry.ShazamID
				}
				if finalEntry.DurationMs > 0 {
					result.DurationMs = finalEntry.DurationMs
				}
				if finalEntry.ArtworkPath != "" {
					artworkPath = finalEntry.ArtworkPath
				}
			}
		}

		now := time.Now()
		c.mgr.mu.Lock()
		lastBoundaryForSeek := c.mgr.lastBoundaryAt
		physStartedAt := c.mgr.physicalStartedAt
		previousResult := cloneRecognitionResult(c.mgr.recognitionResult)
		c.mgr.mu.Unlock()
		seekMS, shouldResetPhysicalStartedAt := computeRecognizedSeekMS(isBoundaryTrigger, captureStartedAt, now, lastBoundaryForSeek, physStartedAt, previousResult, result)

		c.mgr.mu.Lock()
		c.mgr.recognitionResult = result
		c.mgr.lastRecognizedAt = now
		c.mgr.physicalLibraryEntryID = entryID
		c.mgr.shazamContinuityReady = isShazamFallback || shazamMatchedACR || result.ShazamID != ""
		c.mgr.shazamContinuityAbandoned = false
		if isPhysicalFormat(result.Format) {
			c.mgr.physicalFormat = result.Format
		}
		c.mgr.physicalArtworkPath = artworkPath
		c.mgr.physicalSeekMS = seekMS
		c.mgr.physicalSeekUpdatedAt = now
		if isBoundaryTrigger && !lastBoundaryForSeek.IsZero() {
			c.mgr.physicalStartedAt = lastBoundaryForSeek
		} else if shouldResetPhysicalStartedAt {
			c.mgr.physicalStartedAt = captureStartedAt
		}
		c.mgr.mu.Unlock()
		return
	}

	// No-library path.
	now := time.Now()
	c.mgr.mu.Lock()
	lastBoundaryForSeek := c.mgr.lastBoundaryAt
	physStartedAt := c.mgr.physicalStartedAt
	previousResult := cloneRecognitionResult(c.mgr.recognitionResult)
	c.mgr.mu.Unlock()
	seekMS, shouldResetPhysicalStartedAt := computeRecognizedSeekMS(isBoundaryTrigger, captureStartedAt, now, lastBoundaryForSeek, physStartedAt, previousResult, result)

	c.mgr.mu.Lock()
	c.mgr.recognitionResult = result
	c.mgr.lastRecognizedAt = now
	c.mgr.shazamContinuityReady = isShazamFallback || shazamMatchedACR || result.ShazamID != ""
	c.mgr.shazamContinuityAbandoned = false
	if isPhysicalFormat(result.Format) {
		c.mgr.physicalFormat = result.Format
	}
	c.mgr.physicalSeekMS = seekMS
	c.mgr.physicalSeekUpdatedAt = now
	if isBoundaryTrigger && !lastBoundaryForSeek.IsZero() {
		c.mgr.physicalStartedAt = lastBoundaryForSeek
	} else if shouldResetPhysicalStartedAt {
		c.mgr.physicalStartedAt = captureStartedAt
	}
	c.mgr.mu.Unlock()
}

func resolvedRefreshInterval(refresh, max time.Duration) time.Duration {
	if refresh <= 0 {
		return max
	}
	return refresh
}

// captureSkipDuration returns how many seconds of PCM to discard before
// recording the recognition sample. Hard boundaries (silence→audio) need a
// 2 s flush to remove stylus-drop crackle and the previous-track buffer.
// Soft boundaries (energy-change, continuity monitor) are gapless — the new
// track is already playing cleanly so no skip is needed.
func captureSkipDuration(isHardBoundary bool) time.Duration {
	if isHardBoundary {
		return 2 * time.Second
	}
	return 0
}

func (c *recognitionCoordinator) run(ctx context.Context) {
	if c.rec == nil {
		return
	}

	const errorBackoff = 30 * time.Second

	var backoffUntil time.Time
	backoffRateLimited := false

	fallbackTimer := time.NewTimer(c.mgr.cfg.RecognizerMaxInterval)
	defer fallbackTimer.Stop()

	for {
		isBoundaryTrigger := false
		isHardBoundaryTrigger := false
		var boundaryDetectedAt time.Time
		var preBoundaryResult *RecognitionResult
		var preBoundarySeekMS int64
		var preBoundarySeekUpdatedAt time.Time
		var preBoundaryLibraryEntryID int64
		var preBoundaryArtworkPath string
		select {
		case <-ctx.Done():
			return
		case trig := <-c.mgr.recognizeTrigger:
			isBoundaryTrigger = trig.isBoundary
			isHardBoundaryTrigger = trig.isHardBoundary
			boundaryDetectedAt = trig.detectedAt
			if !fallbackTimer.Stop() {
				select {
				case <-fallbackTimer.C:
				default:
				}
			}
			fallbackTimer.Reset(c.mgr.cfg.RecognizerMaxInterval)
		case <-fallbackTimer.C:
			c.mgr.mu.Lock()
			isPhysical := c.mgr.physicalSource == "Physical"
			hasRecognition := c.mgr.recognitionResult != nil
			lastRecogAt := c.mgr.lastRecognizedAt
			fallbackBluetooth := c.mgr.bluetoothPlaying
			fallbackAirPlay := c.mgr.airplayPlaying
			c.mgr.mu.Unlock()
			if !isPhysical || fallbackAirPlay || fallbackBluetooth {
				fallbackTimer.Reset(c.mgr.cfg.RecognizerMaxInterval)
				continue
			}
			if hasRecognition {
				refresh := resolvedRefreshInterval(c.mgr.cfg.RecognizerRefreshInterval, c.mgr.cfg.RecognizerMaxInterval)
				if time.Since(lastRecogAt) < refresh {
					fallbackTimer.Reset(refresh - time.Since(lastRecogAt))
					continue
				}
				fallbackTimer.Reset(refresh)
			} else {
				fallbackTimer.Reset(c.mgr.cfg.RecognizerMaxInterval)
			}
		}

		if wait := time.Until(backoffUntil); wait > 0 {
			if shouldBypassBackoff(isBoundaryTrigger, backoffRateLimited) {
				log.Printf("recognizer [%s]: boundary trigger bypasses no-match/error backoff (%s remaining)", c.rec.Name(), wait)
			} else {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}

		c.mgr.mu.Lock()
		isPhysical := c.mgr.physicalSource == "Physical"
		isAirPlay := c.mgr.airplayPlaying
		isBluetooth := c.mgr.bluetoothPlaying
		c.mgr.mu.Unlock()
		if shouldSkipRecognitionAttempt(isPhysical, isAirPlay, isBluetooth) {
			if c.mgr.cfg.Verbose {
				switch {
				case isAirPlay:
					log.Printf("recognizer [%s]: skipping — AirPlay is active", c.rec.Name())
				case isBluetooth:
					log.Printf("recognizer [%s]: skipping — Bluetooth is active", c.rec.Name())
				}
			}
			continue
		}

		skip := captureSkipDuration(isHardBoundaryTrigger)
		if isBoundaryTrigger {
			c.mgr.mu.Lock()
			if !boundaryDetectedAt.IsZero() {
				// Continuity-monitor triggers carry the first-sighting time, which
				// is closer to the actual track change than time.Now().
				c.mgr.lastBoundaryAt = boundaryDetectedAt
			} else {
				c.mgr.lastBoundaryAt = time.Now()
			}
			c.mgr.mu.Unlock()
		}

		// Defensive check: abort if BT/AirPlay became active since the first
		// shouldSkipRecognitionAttempt check.
		c.mgr.mu.Lock()
		isPhysicalNow := c.mgr.physicalSource == "Physical"
		isAirPlayNow := c.mgr.airplayPlaying
		isBluetoothNow := c.mgr.bluetoothPlaying
		c.mgr.mu.Unlock()
		if !isPhysicalNow || isAirPlayNow || isBluetoothNow {
			log.Printf("recognizer [%s]: ABORTING recognition — source changed: isPhysical=%v isAirPlay=%v isBluetooth=%v", c.rec.Name(), isPhysicalNow, isAirPlayNow, isBluetoothNow)
			continue
		}

		// All pre-capture checks passed. Only hard boundaries clear recognition
		// state preemptively; soft boundaries keep current metadata to avoid
		// progress-bar flicker/resets on quiet passages.
		if isBoundaryTrigger && isHardBoundaryTrigger {
			c.mgr.mu.Lock()
			preBoundaryResult = cloneRecognitionResult(c.mgr.recognitionResult)
			preBoundarySeekMS = c.mgr.physicalSeekMS
			preBoundarySeekUpdatedAt = c.mgr.physicalSeekUpdatedAt
			preBoundaryLibraryEntryID = c.mgr.physicalLibraryEntryID
			preBoundaryArtworkPath = c.mgr.physicalArtworkPath
			c.mgr.recognitionResult = nil
			c.mgr.physicalLibraryEntryID = 0
			c.mgr.physicalArtworkPath = ""
			c.mgr.physicalSeekMS = 0
			c.mgr.physicalSeekUpdatedAt = time.Time{}
			c.mgr.mu.Unlock()
			c.mgr.markDirty()
		} else if isBoundaryTrigger {
			c.mgr.mu.Lock()
			preBoundaryResult = cloneRecognitionResult(c.mgr.recognitionResult)
			preBoundarySeekMS = c.mgr.physicalSeekMS
			preBoundarySeekUpdatedAt = c.mgr.physicalSeekUpdatedAt
			preBoundaryLibraryEntryID = c.mgr.physicalLibraryEntryID
			preBoundaryArtworkPath = c.mgr.physicalArtworkPath
			c.mgr.mu.Unlock()
		}

		if c.lib != nil {
			if isBoundaryTrigger {
				c.lib.RecordRecognitionEvent("Trigger", "boundary")
			} else {
				c.lib.RecordRecognitionEvent("Trigger", "fallback_timer")
			}
		}

		if c.mgr.cfg.Verbose {
			log.Printf("recognizer [%s]: capturing %s from %s (skip=%s)",
				c.rec.Name(), c.mgr.cfg.RecognizerCaptureDuration, c.mgr.cfg.PCMSocket, skip)
		}
		c.mgr.mu.Lock()
		c.mgr.recognizerBusyUntil = time.Now().Add(skip + c.mgr.cfg.RecognizerCaptureDuration + 12*time.Second)
		c.mgr.mu.Unlock()

		captureStartedAt := time.Now()
		captureCtx, cancel := context.WithTimeout(ctx, skip+c.mgr.cfg.RecognizerCaptureDuration+10*time.Second)
		wavPath, err := captureFromPCMSocket(captureCtx, c.mgr.cfg.PCMSocket, c.mgr.cfg.RecognizerCaptureDuration, skip, os.TempDir())
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("recognizer [%s]: capture error: %v", c.rec.Name(), err)
			backoffUntil = time.Now().Add(errorBackoff)
			backoffRateLimited = false
			continue
		}

		result, err := c.rec.Recognize(ctx, wavPath)
		os.Remove(wavPath)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			if c.handleRecognitionError(err, &backoffUntil, &backoffRateLimited) {
				continue
			}
			continue
		}

		// Final source guard: the capture + recognition window is 10-20 s.
		// BT or AirPlay may have become active during that window. Discard the
		// result rather than writing a streaming track into the physical library.
		c.mgr.mu.Lock()
		isPhysicalFinal := c.mgr.physicalSource == "Physical"
		isAirPlayFinal := c.mgr.airplayPlaying
		isBluetoothFinal := c.mgr.bluetoothPlaying
		c.mgr.mu.Unlock()
		if !isPhysicalFinal || isAirPlayFinal || isBluetoothFinal {
			log.Printf("recognizer [%s]: discarding result — source changed during capture/recognition (isPhysical=%v isAirPlay=%v isBluetooth=%v)",
				c.rec.Name(), isPhysicalFinal, isAirPlayFinal, isBluetoothFinal)
			continue
		}

		backoffUntil = time.Time{}
		backoffRateLimited = false

		if result != nil {
			source, score := recognitionLogFields(result)
			log.Printf("recognizer [%s]: %s source=%s ids(acr=%q shazam=%q)  %s — %s",
				c.rec.Name(), score, source, result.ACRID, result.ShazamID, result.Artist, result.Title)
			isShazamFallback := result.ShazamID != "" && result.ACRID == ""
			shazamMatchedACR := false

			c.mgr.mu.Lock()
			currentResult := c.mgr.recognitionResult
			c.mgr.mu.Unlock()
			compareResult := currentResult
			if compareResult == nil && isBoundaryTrigger {
				compareResult = preBoundaryResult
			}
			if compareResult != nil && sameTrackForStateContinuity(compareResult, result) {
				minSeekForRestore := c.mgr.cfg.BoundaryRestoreMinSeek
				preBoundaryElapsedMS := recoverSeekMSFromSnapshot(preBoundarySeekMS, preBoundarySeekUpdatedAt, time.Now())
				knownDurationMS := 0
				if preBoundaryResult != nil {
					knownDurationMS = preBoundaryResult.DurationMs
				}
				thresholdMS := restoreThresholdMS(knownDurationMS, c.mgr.cfg.DurationPessimism)
				elapsedPct := elapsedPercentOfDuration(preBoundaryElapsedMS, knownDurationMS)
				// Conservative policy:
				// 1) Soft boundaries: restore only when prior seek is mature.
				// 2) Hard boundaries: restore only when boundary happened before the
				//    duration suppression threshold (same-track false-positive guard).
				canRestore, restoreBlockedReason := shouldRestorePreBoundaryResult(
					isHardBoundaryTrigger,
					preBoundarySeekMS,
					preBoundaryElapsedMS,
					knownDurationMS,
					minSeekForRestore,
					c.mgr.cfg.DurationPessimism,
				)

				if canRestore {
					log.Printf("recognizer [%s]: same track confirmed — restoring pre-boundary result (%s — %s, seek=%ds elapsed=%ds duration=%ds threshold=%ds elapsed_pct=%.1f)",
						c.rec.Name(), result.Artist, result.Title,
						preBoundarySeekMS/1000,
						preBoundaryElapsedMS/1000,
						knownDurationMS/1000,
						thresholdMS/1000,
						elapsedPct,
					)
					shouldMarkDirty := false
					now := time.Now()
					c.mgr.mu.Lock()
					c.mgr.lastRecognizedAt = now
					if c.mgr.recognitionResult == nil && isBoundaryTrigger && preBoundaryResult != nil {
						restored := cloneRecognitionResult(preBoundaryResult)
						c.mgr.recognitionResult = restored
						c.mgr.physicalLibraryEntryID = preBoundaryLibraryEntryID
						c.mgr.physicalArtworkPath = preBoundaryArtworkPath
						c.mgr.physicalSeekMS = recoverSeekMSFromSnapshot(preBoundarySeekMS, preBoundarySeekUpdatedAt, now)
						c.mgr.physicalSeekUpdatedAt = now
						shouldMarkDirty = true
					}
					if c.mgr.recognitionResult != nil {
						if mergeMissingProviderIDs(c.mgr.recognitionResult, result) {
							shouldMarkDirty = true
						}
					}
					c.mgr.mu.Unlock()
					if shouldMarkDirty {
						c.mgr.markDirty()
					}
					continue
				}
				log.Printf("recognizer [%s]: same track re-confirmed but restore blocked (%s, seek=%ds elapsed=%ds duration=%ds threshold=%ds elapsed_pct=%.1f min=%ds) — applying fresh result (%s — %s)",
					c.rec.Name(), restoreBlockedReason,
					preBoundarySeekMS/1000,
					preBoundaryElapsedMS/1000,
					knownDurationMS/1000,
					thresholdMS/1000,
					elapsedPct,
					int(minSeekForRestore.Seconds()),
					result.Artist, result.Title,
				)
			}

			stop := false
			shazamMatchedACR, stop = c.maybeConfirmCandidate(ctx, result, isBoundaryTrigger)
			if stop {
				return
			}

			c.applyRecognizedResult(result, isBoundaryTrigger, isShazamFallback, shazamMatchedACR, captureStartedAt)

			// Detect false-positive boundary: the boundary trigger fired but
			if c.shazamRec != nil && result.ACRID != "" && !shazamMatchedACR {
				go c.mgr.tryEnableShazamContinuity(ctx, c.shazamRec, result)
			}

			for {
				select {
				case <-c.mgr.recognizeTrigger:
				default:
					goto drained
				}
			}
		drained:
		} else {
			c.handleNoMatch(isBoundaryTrigger, isHardBoundaryTrigger, &backoffUntil, &backoffRateLimited)
			if backoffUntil.IsZero() {
				continue
			}
		}
		c.mgr.markDirty()
	}
}
