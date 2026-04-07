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
	fpr        Fingerprinter
	lib        *internallibrary.Library
}

func newRecognitionCoordinator(m *mgr, rec Recognizer, confirmRec Recognizer, shazamRec Recognizer, fpr Fingerprinter, lib *internallibrary.Library) *recognitionCoordinator {
	return &recognitionCoordinator{
		mgr:        m,
		rec:        rec,
		confirmRec: confirmRec,
		shazamRec:  shazamRec,
		fpr:        fpr,
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

func shouldSkipRecognitionAttempt(isPhysical, isAirPlay bool) bool {
	return !isPhysical || isAirPlay
}

func shouldCreateBoundaryStub(lastStub, lastBoundary time.Time, stillPhysical bool) bool {
	if !stillPhysical {
		return false
	}
	return lastStub.IsZero() || !lastStub.After(lastBoundary)
}

func shouldCreateFingerprintOnlyStub(lastStub, lastBoundary time.Time, stillPhysical bool, minInterval time.Duration) bool {
	if !stillPhysical {
		return false
	}
	if shouldCreateBoundaryStub(lastStub, lastBoundary, stillPhysical) {
		return true
	}
	if minInterval <= 0 {
		minInterval = 5 * time.Minute
	}
	return time.Since(lastStub) >= minInterval
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

func (c *recognitionCoordinator) applyLocalFallbackEntry(entry *internallibrary.CollectionEntry) {
	c.mgr.mu.Lock()
	c.mgr.recognitionResult = &RecognitionResult{
		ACRID:    entry.ACRID,
		ShazamID: entry.ShazamID,
		Title:    entry.Title,
		Artist:   entry.Artist,
		Album:    entry.Album,
		Label:    entry.Label,
		Released: entry.Released,
		Score:    entry.Score,
		Format:   entry.Format,
	}
	c.mgr.lastRecognizedAt = time.Now()
	c.mgr.pendingStubID = 0
	c.mgr.shazamContinuityReady = entry.ShazamID != ""
	if isPhysicalFormat(entry.Format) {
		c.mgr.physicalFormat = entry.Format
	}
	c.mgr.physicalArtworkPath = entry.ArtworkPath
	c.mgr.mu.Unlock()
	c.mgr.markDirty()
}

func (c *recognitionCoordinator) tryLocalFingerprintFallback(capturedFPs []Fingerprint) bool {
	if len(capturedFPs) == 0 || c.lib == nil {
		return false
	}
	localEntry, err := c.lib.FindByFingerprints(capturedFPs, c.mgr.cfg.FingerprintThreshold, 30)
	if err != nil {
		log.Printf("recognizer: fingerprint lookup error: %v", err)
		return false
	}
	if localEntry == nil {
		return false
	}
	log.Printf("recognizer: local fingerprint fallback match (id=%d confirmed=%v %s — %s)",
		localEntry.ID, localEntry.UserConfirmed, localEntry.Artist, localEntry.Title)
	c.applyLocalFallbackEntry(localEntry)
	return true
}

func (c *recognitionCoordinator) lookupLocalFingerprintLocalFirst(capturedFPs []Fingerprint) *internallibrary.CollectionEntry {
	if !c.mgr.cfg.FingerprintLocalFirst || len(capturedFPs) == 0 || c.lib == nil {
		return nil
	}
	threshold := c.mgr.cfg.FingerprintLocalFirstThreshold
	if threshold <= 0 {
		threshold = c.mgr.cfg.FingerprintThreshold
	}
	localEntry, err := c.lib.FindConfirmedByFingerprints(capturedFPs, threshold, 30)
	if err != nil {
		log.Printf("recognizer: local-first fingerprint lookup error: %v", err)
		return nil
	}
	return localEntry
}

func shouldShortCircuitLocalFirst(current *RecognitionResult, localEntry *internallibrary.CollectionEntry) bool {
	if localEntry == nil {
		return false
	}
	if current == nil {
		return true
	}
	candidate := &RecognitionResult{
		ACRID:    localEntry.ACRID,
		ShazamID: localEntry.ShazamID,
		Title:    localEntry.Title,
		Artist:   localEntry.Artist,
	}
	return !sameTrackByProviderIDs(current, candidate)
}

func (c *recognitionCoordinator) tryLocalFingerprintLocalFirst(capturedFPs []Fingerprint) bool {
	localEntry := c.lookupLocalFingerprintLocalFirst(capturedFPs)
	if localEntry == nil {
		return false
	}
	log.Printf("recognizer: local-first fingerprint match (id=%d %s — %s)",
		localEntry.ID, localEntry.Artist, localEntry.Title)
	c.applyLocalFallbackEntry(localEntry)
	return true
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

func (c *recognitionCoordinator) handleRecognitionError(err error, capturedFPs []Fingerprint, backoffUntil *time.Time, backoffRateLimited *bool) bool {
	log.Printf("recognizer [%s]: error: %v", c.rec.Name(), err)
	if c.tryLocalFingerprintFallback(capturedFPs) {
		drained := c.drainPendingTriggers()
		log.Printf("recognizer [%s]: local fallback matched; pending triggers drained=%d", c.rec.Name(), drained)
		*backoffUntil = time.Time{}
		return true
	}
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

func (c *recognitionCoordinator) handleNoMatch(capturedFPs []Fingerprint, isBoundaryTrigger bool, backoffUntil *time.Time, backoffRateLimited *bool) {
	noMatchBackoff := c.mgr.cfg.NoMatchBackoff
	if noMatchBackoff <= 0 {
		noMatchBackoff = 15 * time.Second
	}

	if c.tryLocalFingerprintFallback(capturedFPs) {
		drained := c.drainPendingTriggers()
		log.Printf("recognizer [%s]: local fallback matched; pending triggers drained=%d", c.rec.Name(), drained)
		*backoffUntil = time.Time{}
		return
	}

	log.Printf("recognizer [%s]: no match — retrying in %s", c.rec.Name(), noMatchBackoff)
	storeStubOnNoMatch := isBoundaryTrigger || c.mgr.cfg.RecognizerChain == "fingerprint_only"
	if len(capturedFPs) > 0 && c.lib != nil && storeStubOnNoMatch {
		c.mgr.mu.Lock()
		lastStub := c.mgr.lastStubAt
		lastBoundary := c.mgr.lastBoundaryAt
		stillPhysical := c.mgr.physicalSource == "Physical"
		hasRecognition := c.mgr.recognitionResult != nil
		pendingStubID := c.mgr.pendingStubID
		c.mgr.mu.Unlock()

		if c.mgr.cfg.RecognizerChain == "fingerprint_only" {
			minInterval := c.mgr.cfg.RecognizerMaxInterval
			if minInterval <= 0 {
				minInterval = noMatchBackoff
			}
			if !stillPhysical {
				log.Printf("recognizer: stub skipped — source is no longer Physical (run-out groove or disc removed)")
				c.mgr.mu.Lock()
				c.mgr.pendingStubID = 0
				c.mgr.mu.Unlock()
			} else if hasRecognition {
				log.Printf("recognizer: fingerprint-only stub skipped — already holding recognized track")
				c.mgr.mu.Lock()
				c.mgr.pendingStubID = 0
				c.mgr.mu.Unlock()
			} else if pendingStubID > 0 {
				if saveErr := c.lib.SaveFingerprints(pendingStubID, capturedFPs); saveErr != nil {
					log.Printf("recognizer: pending stub enrich error (id=%d): %v", pendingStubID, saveErr)
					c.mgr.mu.Lock()
					c.mgr.pendingStubID = 0
					c.mgr.mu.Unlock()
				} else {
					log.Printf("recognizer: fingerprint-only no-match enriched pending stub (id=%d)", pendingStubID)
				}
			} else if !shouldCreateFingerprintOnlyStub(lastStub, lastBoundary, stillPhysical, minInterval) {
				log.Printf("recognizer: fingerprint-only stub skipped — throttle active (lastStub=%s, minInterval=%s)", lastStub.Format(time.RFC3339), minInterval)
			} else if stub, stubErr := c.lib.UpsertStub(capturedFPs, c.mgr.cfg.FingerprintThreshold, 30); stubErr != nil {
				log.Printf("recognizer: stub upsert error: %v", stubErr)
			} else {
				log.Printf("recognizer: fingerprint-only no-match stub stored (id=%d)", stub.ID)
				c.mgr.mu.Lock()
				c.mgr.lastStubAt = time.Now()
				c.mgr.pendingStubID = stub.ID
				c.mgr.mu.Unlock()
			}
		} else if shouldCreateBoundaryStub(lastStub, lastBoundary, stillPhysical) {
			if stub, stubErr := c.lib.UpsertStub(capturedFPs, c.mgr.cfg.FingerprintThreshold, 30); stubErr != nil {
				log.Printf("recognizer: stub upsert error: %v", stubErr)
			} else {
				log.Printf("recognizer: fingerprint stub stored (id=%d)", stub.ID)
				c.mgr.mu.Lock()
				c.mgr.lastStubAt = time.Now()
				c.mgr.pendingStubID = stub.ID
				c.mgr.mu.Unlock()
			}
		} else if !stillPhysical {
			log.Printf("recognizer: stub skipped — source is no longer Physical (run-out groove or disc removed)")
			c.mgr.mu.Lock()
			c.mgr.pendingStubID = 0
			c.mgr.mu.Unlock()
		} else {
			log.Printf("recognizer: stub skipped — already created for this boundary (lastStub=%s)", lastStub.Format(time.RFC3339))
		}
	}

	if isBoundaryTrigger {
		log.Printf("recognizer [%s]: boundary no-match — keeping current track until replacement is identified", c.rec.Name())
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
		log.Printf("recognizer [%s]: high-confidence match (score=%d) — skipping confirmation", c.rec.Name(), result.Score)
		return false, false
	}
	if isBoundaryTrigger {
		log.Printf("recognizer [%s]: boundary-triggered recognition — skipping confirmation delay", c.rec.Name())
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

func (c *recognitionCoordinator) applyRecognizedResult(result *RecognitionResult, capturedFPs []Fingerprint, isBoundaryTrigger bool, isShazamFallback bool, shazamMatchedACR bool) {
	if c.lib != nil {
		c.mgr.mu.Lock()
		pendingStubID := c.mgr.pendingStubID
		c.mgr.mu.Unlock()

		artworkPath := ""
		if entry, lookupErr := c.lib.LookupByIDs(result.ACRID, result.ShazamID); lookupErr != nil {
			log.Printf("recognizer: library lookup error: %v", lookupErr)
		} else if entry != nil {
			log.Printf("recognizer: known track (plays: %d) — using saved metadata", entry.PlayCount)
			result.Title = entry.Title
			result.Artist = entry.Artist
			result.Album = entry.Album
			result.Format = entry.Format
			if result.ShazamID == "" {
				result.ShazamID = entry.ShazamID
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
			if pendingStubID > 0 && pendingStubID != entryID {
				if promoteErr := c.lib.PromoteStubFingerprints(pendingStubID, entryID); promoteErr != nil {
					log.Printf("recognizer: promote pending stub fingerprints error (stub=%d target=%d): %v", pendingStubID, entryID, promoteErr)
				} else {
					log.Printf("recognizer: promoted pending stub fingerprints (stub=%d target=%d)", pendingStubID, entryID)
				}
			}
			if len(capturedFPs) > 0 && isBoundaryTrigger && !c.lib.HasFingerprints(entryID) {
				if fpErr := c.lib.SaveFingerprints(entryID, capturedFPs); fpErr != nil {
					log.Printf("recognizer: save fingerprints error: %v", fpErr)
				}
			}
			c.mgr.mu.Lock()
			lastBoundary := c.mgr.lastBoundaryAt
			c.mgr.mu.Unlock()
			if !lastBoundary.IsZero() {
				c.lib.PruneRecentStubs(lastBoundary, entryID)
			}
		}

		c.mgr.mu.Lock()
		c.mgr.recognitionResult = result
		c.mgr.lastRecognizedAt = time.Now()
		c.mgr.pendingStubID = 0
		c.mgr.shazamContinuityReady = isShazamFallback || shazamMatchedACR || result.ShazamID != ""
		if isPhysicalFormat(result.Format) {
			c.mgr.physicalFormat = result.Format
		}
		if entry, _ := c.lib.LookupByIDs(result.ACRID, result.ShazamID); entry != nil && entry.ArtworkPath != "" {
			c.mgr.physicalArtworkPath = entry.ArtworkPath
		} else {
			c.mgr.physicalArtworkPath = artworkPath
		}
		c.mgr.mu.Unlock()
		return
	}

	c.mgr.mu.Lock()
	c.mgr.recognitionResult = result
	c.mgr.lastRecognizedAt = time.Now()
	c.mgr.shazamContinuityReady = isShazamFallback || shazamMatchedACR || result.ShazamID != ""
	if isPhysicalFormat(result.Format) {
		c.mgr.physicalFormat = result.Format
	}
	c.mgr.mu.Unlock()
}

func resolvedRefreshInterval(refresh, max time.Duration) time.Duration {
	if refresh <= 0 {
		return max
	}
	return refresh
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
		select {
		case <-ctx.Done():
			return
		case trig := <-c.mgr.recognizeTrigger:
			isBoundaryTrigger = trig.isBoundary
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
			c.mgr.mu.Unlock()
			if !isPhysical {
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
		c.mgr.mu.Unlock()
		if shouldSkipRecognitionAttempt(isPhysical, isAirPlay) {
			if isAirPlay {
				log.Printf("recognizer [%s]: skipping — AirPlay is active", c.rec.Name())
			}
			continue
		}

		var skip time.Duration
		if isBoundaryTrigger {
			skip = time.Duration(c.mgr.cfg.FingerprintBoundaryLeadSkipSecs) * time.Second
			c.mgr.mu.Lock()
			c.mgr.lastBoundaryAt = time.Now()
			c.mgr.pendingStubID = 0
			c.mgr.mu.Unlock()
		}

		log.Printf("recognizer [%s]: capturing %s from %s (skip=%s)",
			c.rec.Name(), c.mgr.cfg.RecognizerCaptureDuration, c.mgr.cfg.PCMSocket, skip)
		c.mgr.mu.Lock()
		c.mgr.recognizerBusyUntil = time.Now().Add(skip + c.mgr.cfg.RecognizerCaptureDuration + 12*time.Second)
		c.mgr.mu.Unlock()

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

		captureSec := int(c.mgr.cfg.RecognizerCaptureDuration.Seconds())
		capturedFPs := GenerateFingerprints(c.fpr, wavPath,
			c.mgr.cfg.FingerprintWindows, c.mgr.cfg.FingerprintStrideSec,
			c.mgr.cfg.FingerprintLengthSec, captureSec)

		if localEntry := c.lookupLocalFingerprintLocalFirst(capturedFPs); localEntry != nil {
			c.mgr.mu.Lock()
			currentResult := c.mgr.recognitionResult
			c.mgr.mu.Unlock()
			if shouldShortCircuitLocalFirst(currentResult, localEntry) {
				log.Printf("recognizer: local-first fingerprint match (id=%d %s — %s)",
					localEntry.ID, localEntry.Artist, localEntry.Title)
				c.applyLocalFallbackEntry(localEntry)
				drained := c.drainPendingTriggers()
				log.Printf("recognizer [%s]: local-first matched; pending triggers drained=%d", c.rec.Name(), drained)
				os.Remove(wavPath)
				backoffUntil = time.Time{}
				backoffRateLimited = false
				continue
			}
			log.Printf("recognizer [%s]: local-first matched current track — continuing provider chain", c.rec.Name())
		}

		result, err := c.rec.Recognize(ctx, wavPath)
		os.Remove(wavPath)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			if c.handleRecognitionError(err, capturedFPs, &backoffUntil, &backoffRateLimited) {
				continue
			}
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
			if currentResult != nil && sameTrackByProviderIDs(currentResult, result) {
				if isBoundaryTrigger {
					retry := c.mgr.cfg.NoMatchBackoff
					if retry <= 0 {
						retry = 15 * time.Second
					}
					log.Printf("recognizer [%s]: boundary trigger returned same track (%s — %s) — retrying in %s",
						c.rec.Name(), result.Artist, result.Title, retry)
					backoffUntil = time.Now().Add(retry)
					backoffRateLimited = false
					continue
				}

				log.Printf("recognizer [%s]: same track confirmed — no change (%s — %s)", c.rec.Name(), result.Artist, result.Title)
				shouldMarkDirty := false
				c.mgr.mu.Lock()
				c.mgr.lastRecognizedAt = time.Now()
				if c.mgr.recognitionResult != nil {
					if c.mgr.recognitionResult.ACRID == "" && result.ACRID != "" {
						c.mgr.recognitionResult.ACRID = result.ACRID
						shouldMarkDirty = true
					}
					if c.mgr.recognitionResult.ShazamID == "" && result.ShazamID != "" {
						c.mgr.recognitionResult.ShazamID = result.ShazamID
						shouldMarkDirty = true
					}
				}
				c.mgr.mu.Unlock()
				if shouldMarkDirty {
					c.mgr.markDirty()
				}
				continue
			}

			stop := false
			shazamMatchedACR, stop = c.maybeConfirmCandidate(ctx, result, isBoundaryTrigger)
			if stop {
				return
			}

			c.applyRecognizedResult(result, capturedFPs, isBoundaryTrigger, isShazamFallback, shazamMatchedACR)

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
			c.handleNoMatch(capturedFPs, isBoundaryTrigger, &backoffUntil, &backoffRateLimited)
			if backoffUntil.IsZero() {
				continue
			}
		}
		c.mgr.markDirty()
	}
}
