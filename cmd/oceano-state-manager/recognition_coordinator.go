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

// preBoundarySnapshot captures the recognition state at the moment a boundary
// trigger fires, so it can be restored if the recognition confirms the same track
// or returns no-match while the track is still within its expected duration.
type preBoundarySnapshot struct {
	result         *RecognitionResult
	seekMS         int64
	seekUpdatedAt  time.Time
	libraryEntryID int64
	artworkPath    string
}

// backoffState groups the two backoff variables that persist across trigger
// iterations so processOneTrigger can modify them and run() sees the updates.
type backoffState struct {
	until       time.Time
	rateLimited bool
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
	// recognizerRunning is cleared by the caller's deferred clearRecognizerRunning.

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
			if strings.ToLower(result.Format) != strings.ToLower(c.mgr.physicalFormat) && c.mgr.cfg.FormatHintFile != "" {
				hintPath, newFmt := c.mgr.cfg.FormatHintFile, result.Format
				go func() { writeFormatHint(hintPath, newFmt) }()
			}
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
		if strings.ToLower(result.Format) != strings.ToLower(c.mgr.physicalFormat) && c.mgr.cfg.FormatHintFile != "" {
			hintPath, newFmt := c.mgr.cfg.FormatHintFile, result.Format
			go func() { writeFormatHint(hintPath, newFmt) }()
		}
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
	// recognizerRunning is cleared by the caller's deferred clearRecognizerRunning.
}

// clearRecognizerRunning atomically clears the recognizerRunning flag and, if it
// was set, notifies the writer so the UI spinner disappears immediately. Used as
// a deferred call in processOneTrigger to guarantee cleanup on all exit paths.
func (c *recognitionCoordinator) clearRecognizerRunning() {
	c.mgr.mu.Lock()
	wasRunning := c.mgr.recognizerRunning
	c.mgr.recognizerRunning = false
	c.mgr.mu.Unlock()
	if wasRunning {
		c.mgr.markDirty()
	}
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

// awaitTrigger blocks until a recognition trigger is ready, handling both the
// explicit trigger channel and the fallback timer. Returns false only when ctx
// is cancelled (caller should exit). Skips timer fires when recognition is not
// due (source not physical, or refresh interval not elapsed).
func (c *recognitionCoordinator) awaitTrigger(ctx context.Context, timer *time.Timer, bo *backoffState) (recognizeTrigger, bool) {
	for {
		select {
		case <-ctx.Done():
			return recognizeTrigger{}, false
		case trig := <-c.mgr.recognizeTrigger:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.mgr.cfg.RecognizerMaxInterval)
			return trig, true
		case <-timer.C:
			c.mgr.mu.Lock()
			isPhysical := c.mgr.physicalSource == "Physical"
			hasRecognition := c.mgr.recognitionResult != nil
			lastRecogAt := c.mgr.lastRecognizedAt
			isAirPlay := c.mgr.airplayPlaying
			isBluetooth := c.mgr.bluetoothPlaying
			c.mgr.mu.Unlock()
			if !isPhysical || isAirPlay || isBluetooth {
				timer.Reset(c.mgr.cfg.RecognizerMaxInterval)
				continue
			}
			if hasRecognition {
				refresh := resolvedRefreshInterval(c.mgr.cfg.RecognizerRefreshInterval, c.mgr.cfg.RecognizerMaxInterval)
				if time.Since(lastRecogAt) < refresh {
					timer.Reset(refresh - time.Since(lastRecogAt))
					continue
				}
				timer.Reset(refresh)
			} else {
				timer.Reset(c.mgr.cfg.RecognizerMaxInterval)
			}
			return recognizeTrigger{}, true // synthetic fallback trigger
		}
	}
}

// takePreBoundarySnapshot captures the current recognition state so it can be
// restored if the next recognition confirms the same track or returns no-match
// while the track is still within its expected duration.
func (c *recognitionCoordinator) takePreBoundarySnapshot(trig recognizeTrigger) preBoundarySnapshot {
	if !trig.isBoundary {
		return preBoundarySnapshot{}
	}
	c.mgr.mu.Lock()
	defer c.mgr.mu.Unlock()
	return preBoundarySnapshot{
		result:         cloneRecognitionResult(c.mgr.recognitionResult),
		seekMS:         c.mgr.physicalSeekMS,
		seekUpdatedAt:  c.mgr.physicalSeekUpdatedAt,
		libraryEntryID: c.mgr.physicalLibraryEntryID,
		artworkPath:    c.mgr.physicalArtworkPath,
	}
}

// processOneTrigger executes a single recognition attempt end-to-end.
// The deferred clearRecognizerRunning guarantees recognizerRunning is reset and
// the UI is notified on every exit path — no manual cleanup needed in callers.
func (c *recognitionCoordinator) processOneTrigger(ctx context.Context, trig recognizeTrigger, bo *backoffState) {
	const errorBackoff = 30 * time.Second
	defer c.clearRecognizerRunning()

	// Wait out any active backoff (boundary triggers bypass no-match backoff).
	if wait := time.Until(bo.until); wait > 0 {
		if shouldBypassBackoff(trig.isBoundary, bo.rateLimited) {
			log.Printf("recognizer [%s]: boundary trigger bypasses no-match/error backoff (%s remaining)", c.rec.Name(), wait)
		} else {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}

	// Pre-capture source guard.
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
		return
	}

	// Record the boundary timestamp before capture begins.
	if trig.isBoundary {
		c.mgr.mu.Lock()
		if !trig.detectedAt.IsZero() {
			c.mgr.lastBoundaryAt = trig.detectedAt
		} else {
			c.mgr.lastBoundaryAt = time.Now()
		}
		c.mgr.mu.Unlock()
	}

	// Defensive double-check: BT/AirPlay may have become active between the
	// pre-guard above and the boundary timestamp write.
	c.mgr.mu.Lock()
	isPhysicalNow := c.mgr.physicalSource == "Physical"
	isAirPlayNow := c.mgr.airplayPlaying
	isBluetoothNow := c.mgr.bluetoothPlaying
	c.mgr.mu.Unlock()
	if !isPhysicalNow || isAirPlayNow || isBluetoothNow {
		log.Printf("recognizer [%s]: ABORTING recognition — source changed: isPhysical=%v isAirPlay=%v isBluetooth=%v",
			c.rec.Name(), isPhysicalNow, isAirPlayNow, isBluetoothNow)
		return
	}

	// Snapshot recognition state before capture so it can be restored on
	// same-track confirmation or no-match within the known track duration.
	// Recognition runs silently: the current track stays visible with a spinner.
	snap := c.takePreBoundarySnapshot(trig)

	if c.lib != nil {
		if trig.isBoundary {
			c.lib.RecordRecognitionEvent("Trigger", "boundary")
		} else {
			c.lib.RecordRecognitionEvent("Trigger", "fallback_timer")
		}
	}

	skip := captureSkipDuration(trig.isHardBoundary)
	if c.mgr.cfg.Verbose {
		log.Printf("recognizer [%s]: capturing %s from %s (skip=%s)",
			c.rec.Name(), c.mgr.cfg.RecognizerCaptureDuration, c.mgr.cfg.PCMSocket, skip)
	}
	c.mgr.mu.Lock()
	c.mgr.recognizerBusyUntil = time.Now().Add(skip + c.mgr.cfg.RecognizerCaptureDuration + 12*time.Second)
	c.mgr.recognizerRunning = true
	c.mgr.mu.Unlock()
	c.mgr.markDirty()

	captureStartedAt := time.Now()
	captureCtx, cancel := context.WithTimeout(ctx, skip+c.mgr.cfg.RecognizerCaptureDuration+10*time.Second)
	wavPath, err := captureFromPCMSocket(captureCtx, c.mgr.cfg.PCMSocket, c.mgr.cfg.RecognizerCaptureDuration, skip, os.TempDir())
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("recognizer [%s]: capture error: %v", c.rec.Name(), err)
		bo.until = time.Now().Add(errorBackoff)
		bo.rateLimited = false
		return
	}

	result, recErr := c.rec.Recognize(ctx, wavPath)
	os.Remove(wavPath)
	if ctx.Err() != nil {
		return
	}
	if recErr != nil {
		c.handleRecognitionError(recErr, &bo.until, &bo.rateLimited)
		return
	}

	// Final source guard: BT or AirPlay may have become active during the
	// capture/recognition window (10–20 s). Discard rather than polluting the library.
	c.mgr.mu.Lock()
	isPhysicalFinal := c.mgr.physicalSource == "Physical"
	isAirPlayFinal := c.mgr.airplayPlaying
	isBluetoothFinal := c.mgr.bluetoothPlaying
	c.mgr.mu.Unlock()
	if !isPhysicalFinal || isAirPlayFinal || isBluetoothFinal {
		log.Printf("recognizer [%s]: discarding result — source changed during capture/recognition (isPhysical=%v isAirPlay=%v isBluetooth=%v)",
			c.rec.Name(), isPhysicalFinal, isAirPlayFinal, isBluetoothFinal)
		return
	}

	bo.until = time.Time{}
	bo.rateLimited = false
	c.dispatchResult(ctx, result, trig, snap, captureStartedAt, bo)
}

// dispatchResult applies a recognition outcome: successful match, same-track
// confirmation, or no-match. It is called from processOneTrigger after all
// source guards have passed, with a guaranteed clearRecognizerRunning defer
// active in the parent frame.
func (c *recognitionCoordinator) dispatchResult(ctx context.Context, result *RecognitionResult, trig recognizeTrigger, snap preBoundarySnapshot, captureStartedAt time.Time, bo *backoffState) {
	if result != nil {
		source, score := recognitionLogFields(result)
		log.Printf("recognizer [%s]: %s source=%s ids(acr=%q shazam=%q)  %s — %s",
			c.rec.Name(), score, source, result.ACRID, result.ShazamID, result.Artist, result.Title)
		isShazamFallback := result.ShazamID != "" && result.ACRID == ""

		c.mgr.mu.Lock()
		currentResult := c.mgr.recognitionResult
		c.mgr.mu.Unlock()
		compareResult := currentResult
		if compareResult == nil && trig.isBoundary {
			compareResult = snap.result
		}

		if compareResult != nil && sameTrackForStateContinuity(compareResult, result) {
			preBoundaryElapsedMS := recoverSeekMSFromSnapshot(snap.seekMS, snap.seekUpdatedAt, time.Now())
			knownDurationMS := 0
			if snap.result != nil {
				knownDurationMS = snap.result.DurationMs
			}
			thresholdMS := restoreThresholdMS(knownDurationMS, c.mgr.cfg.DurationPessimism)
			elapsedPct := elapsedPercentOfDuration(preBoundaryElapsedMS, knownDurationMS)
			canRestore, restoreBlockedReason := shouldRestorePreBoundaryResult(
				trig.isHardBoundary,
				snap.seekMS,
				preBoundaryElapsedMS,
				knownDurationMS,
				c.mgr.cfg.BoundaryRestoreMinSeek,
				c.mgr.cfg.DurationPessimism,
			)

			if canRestore {
				log.Printf("recognizer [%s]: same track confirmed — keeping pre-boundary result (%s — %s, seek=%ds elapsed=%ds duration=%ds threshold=%ds elapsed_pct=%.1f)",
					c.rec.Name(), result.Artist, result.Title,
					snap.seekMS/1000, preBoundaryElapsedMS/1000,
					knownDurationMS/1000, thresholdMS/1000, elapsedPct)
				now := time.Now()
				c.mgr.mu.Lock()
				c.mgr.lastRecognizedAt = now
				if c.mgr.recognitionResult == nil && trig.isBoundary && snap.result != nil {
					c.mgr.recognitionResult = cloneRecognitionResult(snap.result)
					c.mgr.physicalLibraryEntryID = snap.libraryEntryID
					c.mgr.physicalArtworkPath = snap.artworkPath
				}
				if trig.isBoundary {
					c.mgr.physicalSeekMS = recoverSeekMSFromSnapshot(snap.seekMS, snap.seekUpdatedAt, now)
					c.mgr.physicalSeekUpdatedAt = now
				}
				if c.mgr.recognitionResult != nil {
					mergeMissingProviderIDs(c.mgr.recognitionResult, result)
				}
				c.mgr.mu.Unlock()
				return // defer fires: recognizerRunning=false + markDirty
			}
			log.Printf("recognizer [%s]: same track re-confirmed but restore blocked (%s, seek=%ds elapsed=%ds duration=%ds threshold=%ds elapsed_pct=%.1f min=%ds) — applying fresh result (%s — %s)",
				c.rec.Name(), restoreBlockedReason,
				snap.seekMS/1000, preBoundaryElapsedMS/1000,
				knownDurationMS/1000, thresholdMS/1000, elapsedPct,
				int(c.mgr.cfg.BoundaryRestoreMinSeek.Seconds()),
				result.Artist, result.Title)
		}

		shazamMatchedACR, stop := c.maybeConfirmCandidate(ctx, result, trig.isBoundary)
		if stop {
			return // ctx cancelled; defer fires cleanup
		}

		c.applyRecognizedResult(result, trig.isBoundary, isShazamFallback, shazamMatchedACR, captureStartedAt)

		if c.shazamRec != nil && result.ACRID != "" && !shazamMatchedACR {
			go c.mgr.tryEnableShazamContinuity(ctx, c.shazamRec, result)
		}

		// Drain any triggers queued during the long capture+recognition window so
		// we don't re-recognise the same track immediately after a successful match.
		for {
			select {
			case <-c.mgr.recognizeTrigger:
			default:
				return // defer fires: recognizerRunning=false + markDirty
			}
		}
	}

	// No-match path.
	// Elapsed-time guard: if the track cannot have ended yet, the boundary was a
	// false positive (a-cappella breath, quiet passage, CD inter-track gap). Keep
	// showing the previous track and retry after a short backoff.
	if trig.isBoundary && trig.isHardBoundary && snap.result != nil && snap.result.DurationMs > 0 {
		now := time.Now()
		elapsed := recoverSeekMSFromSnapshot(snap.seekMS, snap.seekUpdatedAt, now)
		if elapsed < int64(snap.result.DurationMs) {
			noMatchBackoff := c.mgr.cfg.NoMatchBackoff
			if noMatchBackoff <= 0 {
				noMatchBackoff = 15 * time.Second
			}
			log.Printf("recognizer [%s]: no match — elapsed %ds within track duration %ds, keeping %s — %s (retry in %s)",
				c.rec.Name(), elapsed/1000, snap.result.DurationMs/1000,
				snap.result.Artist, snap.result.Title, noMatchBackoff)
			c.mgr.mu.Lock()
			if c.mgr.recognitionResult == nil {
				// Safe fallback: result was cleared by a concurrent path.
				c.mgr.recognitionResult = cloneRecognitionResult(snap.result)
				c.mgr.physicalArtworkPath = snap.artworkPath
				c.mgr.physicalLibraryEntryID = snap.libraryEntryID
			}
			// Refresh seek so the progress bar stays accurate after the recognition delay.
			c.mgr.physicalSeekMS = elapsed
			c.mgr.physicalSeekUpdatedAt = now
			c.mgr.mu.Unlock()
			bo.until = time.Now().Add(noMatchBackoff)
			bo.rateLimited = false
			return // defer fires: recognizerRunning=false + markDirty
		}
	}
	c.handleNoMatch(trig.isBoundary, trig.isHardBoundary, &bo.until, &bo.rateLimited)
	// defer fires: recognizerRunning=false + markDirty
}

func (c *recognitionCoordinator) run(ctx context.Context) {
	if c.rec == nil {
		return
	}

	fallbackTimer := time.NewTimer(c.mgr.cfg.RecognizerMaxInterval)
	defer fallbackTimer.Stop()

	var bo backoffState
	for {
		trig, ok := c.awaitTrigger(ctx, fallbackTimer, &bo)
		if !ok {
			return
		}
		c.processOneTrigger(ctx, trig, &bo)
	}
}
