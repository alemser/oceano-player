package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"errors"

	internallibrary "github.com/alemser/oceano-player/internal/library"
	internalmetadata "github.com/alemser/oceano-player/internal/metadata"
)

type recognitionCoordinator struct {
	mgr           *mgr
	rec           Recognizer
	confirmRec    Recognizer
	shazamioRec   Recognizer
	lib           *internallibrary.Library
	metadataChain *internalmetadata.Chain
}

func newRecognitionCoordinator(m *mgr, rec Recognizer, confirmRec Recognizer, shazamioRec Recognizer, lib *internallibrary.Library) *recognitionCoordinator {
	return &recognitionCoordinator{
		mgr:         m,
		rec:         rec,
		confirmRec:  confirmRec,
		shazamioRec: shazamioRec,
		lib:         lib,
	}
}

// enrichWithMetadataChainAsync runs the configured metadata chain (text fields)
// and then resolves album artwork asynchronously via the iTunes Search API when
// enabled — matching the historical fetchArtwork / fetchArtworkFromSong behaviour
// without blocking applyRecognizedResult.
func (c *recognitionCoordinator) enrichWithMetadataChainAsync(result *RecognitionResult) {
	if result == nil {
		return
	}
	snapshot := cloneRecognitionResult(result)
	if strings.TrimSpace(snapshot.Title) == "" || strings.TrimSpace(snapshot.Artist) == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		artDir := strings.TrimSpace(c.mgr.cfg.ArtworkDir)

		c.mgr.mu.Lock()
		existingArtPath := strings.TrimSpace(c.mgr.physicalArtworkPath)
		c.mgr.mu.Unlock()

		baseReq := internalmetadata.Request{
			Title:       snapshot.Title,
			Artist:      snapshot.Artist,
			Album:       snapshot.Album,
			Label:       snapshot.Label,
			Released:    snapshot.Released,
			TrackNumber: snapshot.TrackNumber,
			DiscogsURL:  snapshot.DiscogsURL,
			Format:      snapshot.Format,
			ACRID:       snapshot.ACRID,
			ShazamID:    snapshot.ShazamID,
			WantArtwork: false,
		}

		var patch *internalmetadata.Patch
		if c.metadataChain != nil {
			var err error
			patch, err = c.metadataChain.Run(ctx, baseReq, nil)
			if err != nil {
				log.Printf("metadata chain: enrichment error for %s — %s: %v", snapshot.Artist, snapshot.Title, err)
				return
			}
		}

		if artDir != "" && existingArtPath == "" &&
			(strings.TrimSpace(snapshot.Album) != "" || strings.TrimSpace(snapshot.Title) != "") {
			artReq := baseReq
			artReq.WantArtwork = true
			artReq.ArtworkDir = artDir
			var artPatch *internalmetadata.Patch
			var artErr error
			if c.metadataChain != nil {
				artPatch, artErr = c.metadataChain.RunForArtwork(ctx, artReq)
			} else {
				it := internalmetadata.NewItunesProvider()
				artPatch, artErr = it.Enrich(ctx, artReq)
			}
			if artErr != nil {
				log.Printf("metadata enrichment: artwork error for %s — %s: %v", snapshot.Artist, snapshot.Title, artErr)
			} else {
				patch = internalmetadata.MergeArtworkOnly(patch, artPatch)
			}
		}

		if patch == nil || patch.Empty() {
			return
		}

		c.mgr.mu.Lock()
		current := c.mgr.recognitionResult
		if current == nil || !sameTrackByProviderIDs(current, snapshot) {
			c.mgr.mu.Unlock()
			return
		}
		changed := false
		if current.Album == "" && patch.Album != "" {
			current.Album = patch.Album
			changed = true
		}
		if current.Label == "" && patch.Label != "" {
			current.Label = patch.Label
			changed = true
		}
		if current.Released == "" && patch.Released != "" {
			current.Released = patch.Released
			changed = true
		}
		if current.TrackNumber == "" && patch.TrackNumber != "" {
			current.TrackNumber = patch.TrackNumber
			changed = true
		}
		if current.DiscogsURL == "" && patch.DiscogsURL != "" {
			current.DiscogsURL = patch.DiscogsURL
			changed = true
		}
		if patch.Artwork != nil && strings.TrimSpace(patch.Artwork.Path) != "" &&
			strings.TrimSpace(c.mgr.physicalArtworkPath) == "" {
			c.mgr.physicalArtworkPath = strings.TrimSpace(patch.Artwork.Path)
			changed = true
		}
		libraryID := c.mgr.physicalLibraryEntryID
		c.mgr.mu.Unlock()

		if changed {
			log.Printf("metadata enrichment: applied patch for %s — %s (provider=%s)", snapshot.Artist, snapshot.Title, patch.Provider)
			c.mgr.markDirty()
		}

		if c.lib != nil && libraryID > 0 {
			artPath := ""
			if patch.Artwork != nil {
				artPath = strings.TrimSpace(patch.Artwork.Path)
			}
			if dbErr := c.lib.UpdateEnrichmentPatch(libraryID, patch.DiscogsURL, patch.Album, patch.Label, patch.Released, patch.Provider, artPath); dbErr != nil {
				log.Printf("metadata chain: db persist error: %v", dbErr)
			}
		}
	}()
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

func isNewTrackCandidate(result *RecognitionResult, currentACRID, currentShazamioKey string) bool {
	if result == nil {
		return false
	}
	if result.ACRID != "" {
		return result.ACRID != currentACRID
	}
	if result.ShazamID != "" {
		return result.ShazamID != currentShazamioKey
	}
	return currentACRID == "" && currentShazamioKey == ""
}

func shouldBypassBackoff(isBoundaryTrigger, backoffRateLimited bool) bool {
	return isBoundaryTrigger && !backoffRateLimited
}

// canonicalProviderID maps a recognizer display name to the stable ID used in
// state.json and the provider-health endpoint.
func canonicalProviderID(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "acrcloud"):
		return "acrcloud"
	case strings.Contains(lower, "shazam"):
		return "shazam"
	case strings.Contains(lower, "audd"):
		return "audd"
	default:
		return ""
	}
}

// recognizerCanonicalIDs returns canonical provider IDs for ALL providers in a
// recognizer. For a ChainRecognizer whose Name() is "ACRCloud→Shazam" it splits
// on "→" and maps each segment, skipping unknown names. Used when clearing
// backoff after a successful recognition (all providers in the chain benefit).
func recognizerCanonicalIDs(rec Recognizer) []string {
	parts := strings.Split(rec.Name(), "→")
	ids := make([]string, 0, len(parts))
	for _, p := range parts {
		if id := canonicalProviderID(strings.TrimSpace(p)); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// rateLimitedCanonicalIDs returns the canonical provider ID of the specific
// provider that returned ErrRateLimit. For a ChainRecognizer it consults
// RateLimitedProviderName() so only the culprit is reported — not every
// provider in the chain. For a single (non-chain) recognizer it uses its
// own canonical ID, since it is by definition the one that rate-limited.
func rateLimitedCanonicalIDs(rec Recognizer) []string {
	if chain, ok := rec.(*ChainRecognizer); ok {
		name := chain.RateLimitedProviderName()
		if name == "" {
			return nil // chain recorded no rate-limit culprit
		}
		if id := canonicalProviderID(name); id != "" {
			return []string{id}
		}
		return nil
	}
	return recognizerCanonicalIDs(rec)
}

// setProviderBackoff records that a provider is rate-limited until the given
// time. Logs on the first entry into backoff (transition from not-limited).
func (m *mgr) setProviderBackoff(providerID string, until time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.providerBackoffExpires == nil {
		m.providerBackoffExpires = make(map[string]time.Time)
	}
	prev := m.providerBackoffExpires[providerID]
	m.providerBackoffExpires[providerID] = until
	if prev.IsZero() || time.Now().After(prev) {
		log.Printf("recognition: provider %s rate-limited until %s", providerID, until.UTC().Format(time.RFC3339))
	}
}

// clearProviderBackoff removes any active backoff entry for the given provider.
// Logs when clearing an entry that had not yet expired.
func (m *mgr) clearProviderBackoff(providerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.providerBackoffExpires == nil {
		return
	}
	prev, had := m.providerBackoffExpires[providerID]
	delete(m.providerBackoffExpires, providerID)
	if had && !prev.IsZero() && time.Now().Before(prev) {
		log.Printf("recognition: provider %s backoff cleared (was until %s)", providerID, prev.UTC().Format(time.RFC3339))
	}
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
	} else if strings.EqualFold(result.MatchSource, "audd") {
		source = "audd"
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

	// Non-boundary (periodic) trigger: use time elapsed since capture started.
	// physStartedAt is the session or last-reset anchor; on long sessions it can
	// be many minutes old, inflating seek far past the track duration (Bug 3).
	seekMS := now.Sub(captureStartedAt).Milliseconds()
	if seekMS < 0 {
		seekMS = 0
	}
	if sameTrackForStateContinuity(previousResult, newResult) && !physStartedAt.IsZero() {
		return seekMS, false // same track — preserve physicalStartedAt, keep seek conservative
	}

	// First successful ID with no prior in-memory result: wall time since the
	// physical session anchor (needle / resume) must count slow ACR retries and
	// confirmation, not only the last capture window (Bug: progress stuck near 0).
	if previousResult == nil && !physStartedAt.IsZero() {
		if wall := now.Sub(physStartedAt).Milliseconds(); wall > seekMS {
			seekMS = wall
		}
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
		until := time.Now().Add(rateLimitBackoff)
		log.Printf("recognizer [%s]: rate limited — backing off %s", c.rec.Name(), rateLimitBackoff)
		*backoffUntil = until
		*backoffRateLimited = true
		for _, id := range rateLimitedCanonicalIDs(c.rec) {
			c.mgr.setProviderBackoff(id, until)
		}
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
		boundaryAt := c.mgr.lastBoundaryAt
		c.mgr.recognitionResult = nil
		c.mgr.physicalArtworkPath = ""
		c.mgr.physicalLibraryEntryID = 0
		c.mgr.physicalBoundarySensitive = false
		c.mgr.shazamioContinuityReady = false
		c.mgr.shazamioContinuityAbandoned = false
		// Re-anchor playback clock for the next identification so a later periodic
		// match does not inherit wall time from before this (failed) track boundary.
		if !boundaryAt.IsZero() {
			c.mgr.physicalStartedAt = boundaryAt
		} else {
			c.mgr.physicalStartedAt = time.Now()
		}
		c.mgr.mu.Unlock()
	}

	c.mgr.mu.Lock()
	c.mgr.recognitionPhase = "no_match"
	c.mgr.mu.Unlock()
	c.mgr.markDirty()

	*backoffUntil = time.Now().Add(noMatchBackoff)
	*backoffRateLimited = false
}

// confirmationMatchesCandidate reports whether the confirmation pass agrees with the
// chain candidate (same ACR ID, matching Shazam IDs, or equivalent metadata).
func confirmationMatchesCandidate(confProviderName, primaryChainName string, conf, candidate *RecognitionResult) bool {
	if conf == nil || candidate == nil {
		return false
	}
	sameTrack := confProviderName == primaryChainName && conf.ACRID != "" && candidate.ACRID != "" && conf.ACRID == candidate.ACRID
	if !sameTrack && conf.ShazamID != "" && candidate.ShazamID != "" && conf.ShazamID == candidate.ShazamID {
		sameTrack = true
	}
	if !sameTrack {
		sameTrack = tracksEquivalent(conf.Title, conf.Artist, candidate.Title, candidate.Artist)
	}
	return sameTrack
}

// shazamioConfirmationFollowup merges ShazamID from the confirmer onto candidate when
// the confirmer is the chain client (Name "Shazamio"), not "ShazamioContinuity".
// Returns true when the coordinator should treat Shazamio as having aligned with ACR.
func shazamioConfirmationFollowup(confProviderName string, conf *RecognitionResult, candidate *RecognitionResult) bool {
	if confProviderName != "Shazamio" {
		return false
	}
	if candidate.ShazamID == "" && conf != nil && conf.ShazamID != "" {
		candidate.ShazamID = conf.ShazamID
	}
	return true
}

func (c *recognitionCoordinator) maybeConfirmCandidate(ctx context.Context, result *RecognitionResult, isBoundaryTrigger bool, attemptTel *internallibrary.RecognitionAttemptContext) (bool, bool) {
	if c.mgr.cfg.ConfirmationDelay <= 0 {
		return false, false
	}

	c.mgr.mu.Lock()
	currentACRID := ""
	currentShazamioKey := ""
	if c.mgr.recognitionResult != nil {
		currentACRID = c.mgr.recognitionResult.ACRID
		currentShazamioKey = c.mgr.recognitionResult.ShazamID
	}
	c.mgr.mu.Unlock()

	if !isNewTrackCandidate(result, currentACRID, currentShazamioKey) {
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
	if tel, gainErr := applyCaptureAutoGainOnWAVFile(confWav, c.mgr.cfg.RecognitionCaptureAutoGain); gainErr != nil {
		if c.mgr.cfg.Verbose {
			log.Printf("recognizer [%s]: confirmation capture auto-gain skipped: %v", c.rec.Name(), gainErr)
		}
	} else if tel.Applied {
		log.Printf("recognizer [%s]: confirmation capture auto-gain applied gain=%.2fx rms %.4f→%.4f peak %.4f→%.4f clipped=%d",
			c.rec.Name(), tel.Gain, tel.BeforeRMS, tel.AfterRMS, tel.BeforePeak, tel.AfterPeak, tel.Clipped)
	}

	confCtx2, confCancel2 := context.WithTimeout(ctx, confDur+10*time.Second)
	confRecCtx := confCtx2
	if attemptTel != nil {
		confCopy := *attemptTel
		confCopy.Phase = "confirmation"
		confCopy.SkipMs = 0
		confCopy.CaptureDurationMs = int(confDur.Milliseconds())
		if m, pk, err := wavPCMLevelStats(confWav); err == nil {
			confCopy.RMSMean, confCopy.RMSPeak = m, pk
		}
		confRecCtx = internallibrary.WithRecognitionAttemptContext(confCtx2, &confCopy)
	}
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
			r, e := primaryRec.Recognize(confRecCtx, confWav)
			pCh <- recOut{res: r, err: e}
		}()
		go func() {
			r, e := c.confirmRec.Recognize(confRecCtx, confWav)
			sCh <- recOut{res: r, err: e}
		}()
		pOut := <-pCh
		sOut := <-sCh
		conf, confRecErr, confProviderName = chooseConfirmationResult(
			primaryRec.Name(), pOut.res, pOut.err,
			c.confirmRec.Name(), sOut.res, sOut.err,
		)
	} else {
		conf, confRecErr = confirmer.Recognize(confRecCtx, confWav)
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

	if !confirmationMatchesCandidate(confProviderName, c.rec.Name(), conf, result) {
		log.Printf("recognizer [%s]: confirmation (%s) disagrees (got %s — %s) — keeping original candidate %s — %s",
			c.rec.Name(), confProviderName, conf.Artist, conf.Title, result.Artist, result.Title)
		return false, false
	}

	log.Printf("recognizer [%s]: confirmed by %s — %s — %s", c.rec.Name(), confProviderName, result.Artist, result.Title)
	if shazamioConfirmationFollowup(confProviderName, conf, result) {
		return true, false
	}
	return false, false
}

func (c *recognitionCoordinator) applyRecognizedResult(result *RecognitionResult, isBoundaryTrigger bool, isShazamioFallback bool, shazamioMatchedACR bool, captureStartedAt time.Time, persistToLibrary bool) {
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
				result.DurationMs = pickLongerDurationMs(result.DurationMs, entry.DurationMs)
			}
			if entry.DiscogsURL != "" {
				result.DiscogsURL = entry.DiscogsURL
			}
			artworkPath = entry.ArtworkPath
		}

		boundarySensitive := false
		entryID := int64(0)
		if persistToLibrary {
			var recErr error
			entryID, recErr = c.lib.RecordPlay(result, artworkPath)
			if recErr != nil {
				log.Printf("recognizer: library record error: %v", recErr)
			} else if entryID > 0 {
				// Read back the final entry after RecordPlay to pick up any library
				// metadata applied by equivalent-metadata merge (e.g. when ACRCloud
				// returns a different ACRID for a track the user already edited).
				// This prevents a brief flash of provider data before syncFromLibrary runs.
				if finalEntry, _ := c.lib.GetByID(entryID); finalEntry != nil {
					boundarySensitive = finalEntry.BoundarySensitive
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
						result.DurationMs = pickLongerDurationMs(result.DurationMs, finalEntry.DurationMs)
					}
					if finalEntry.ArtworkPath != "" {
						artworkPath = finalEntry.ArtworkPath
					}
					if finalEntry.DiscogsURL != "" {
						result.DiscogsURL = finalEntry.DiscogsURL
					}
				}
			}
		} else if c.mgr.cfg.Verbose {
			log.Printf("recognizer [%s]: display-only mode active for input=%q — skipping library persistence",
				c.rec.Name(), resolveRecognitionPolicyFromConfigPath(c.mgr.cfg.CalibrationConfigPath).LastKnownInputID)
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
		c.mgr.recognitionPhase = ""
		c.mgr.lastRecognizedAt = now
		c.mgr.physicalLibraryEntryID = entryID
		c.mgr.physicalBoundarySensitive = boundarySensitive
		c.mgr.shazamioContinuityReady = isShazamioFallback || shazamioMatchedACR || result.ShazamID != ""
		c.mgr.shazamioContinuityAbandoned = false
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
		c.enrichWithMetadataChainAsync(result)
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
	c.mgr.recognitionPhase = ""
	c.mgr.lastRecognizedAt = now
	c.mgr.physicalBoundarySensitive = false
	c.mgr.shazamioContinuityReady = isShazamioFallback || shazamioMatchedACR || result.ShazamID != ""
	c.mgr.shazamioContinuityAbandoned = false
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
	c.enrichWithMetadataChainAsync(result)
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
		var boundaryEventID int64
		isBoundaryTrigger := false
		isHardBoundaryTrigger := false
		var boundaryDetectedAt time.Time
		var preBoundaryResult *RecognitionResult
		var preBoundarySeekMS int64
		var preBoundarySeekUpdatedAt time.Time
		var preBoundaryLibraryEntryID int64
		var preBoundaryArtworkPath string
		var preBoundaryBoundarySensitive bool
		select {
		case <-ctx.Done():
			return
		case trig := <-c.mgr.recognizeTrigger:
			isBoundaryTrigger = trig.isBoundary
			isHardBoundaryTrigger = trig.isHardBoundary
			boundaryDetectedAt = trig.detectedAt
			boundaryEventID = trig.boundaryEventID
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
		vuInSilence := c.mgr.vuInSilence
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
			c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
				Outcome: internallibrary.FollowupOutcomeSkippedCoordinator,
			})
			continue
		}
		if vuInSilence {
			if c.mgr.cfg.Verbose {
				log.Printf("recognizer [%s]: skipping — VU in silence", c.rec.Name())
			}
			c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
				Outcome: internallibrary.FollowupOutcomeSkippedCoordinator,
			})
			continue
		}

		// Resolve per-input policy before any capture/provider call so "off"
		// truly disables recognition cost and telemetry noise for that input.
		policy := resolveRecognitionPolicyFromConfigPathCached(c.mgr.cfg.CalibrationConfigPath)
		if !shouldRunRecognitionForInputPolicy(policy.Policy) {
			if c.mgr.cfg.Verbose {
				log.Printf("recognizer [%s]: skipping by input policy=%q input=%q (%s)",
					c.rec.Name(), policy.Policy, policy.LastKnownInputID, policy.DerivedBy)
			}
			// Preserve existing hard-boundary behavior: clear stale physical
			// metadata so UI does not linger on an old track while recognition is off.
			if isBoundaryTrigger && isHardBoundaryTrigger {
				c.mgr.mu.Lock()
				c.mgr.recognitionResult = nil
				c.mgr.recognitionPhase = "off"
				c.mgr.recognizerBusyUntil = time.Time{}
				c.mgr.physicalArtworkPath = ""
				c.mgr.physicalLibraryEntryID = 0
				c.mgr.physicalBoundarySensitive = false
				c.mgr.shazamioContinuityReady = false
				c.mgr.shazamioContinuityAbandoned = false
				c.mgr.mu.Unlock()
				c.mgr.markDirty()
			} else {
				c.mgr.mu.Lock()
				c.mgr.recognitionPhase = "off"
				c.mgr.recognizerBusyUntil = time.Time{}
				c.mgr.mu.Unlock()
				c.mgr.markDirty()
			}
			c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
				Outcome: internallibrary.FollowupOutcomeSkippedCoordinator,
			})
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
			c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
				Outcome: internallibrary.FollowupOutcomeSkippedCoordinator,
			})
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
			preBoundaryBoundarySensitive = c.mgr.physicalBoundarySensitive
			c.mgr.recognitionResult = nil
			c.mgr.physicalLibraryEntryID = 0
			c.mgr.physicalBoundarySensitive = false
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
			preBoundaryBoundarySensitive = c.mgr.physicalBoundarySensitive
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
		c.mgr.recognitionPhase = ""
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
			c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
				Outcome: internallibrary.FollowupOutcomeCaptureError,
			})
			continue
		}
		if tel, gainErr := applyCaptureAutoGainOnWAVFile(wavPath, c.mgr.cfg.RecognitionCaptureAutoGain); gainErr != nil {
			if c.mgr.cfg.Verbose {
				log.Printf("recognizer [%s]: capture auto-gain skipped: %v", c.rec.Name(), gainErr)
			}
		} else if tel.Applied {
			log.Printf("recognizer [%s]: capture auto-gain applied gain=%.2fx rms %.4f→%.4f peak %.4f→%.4f clipped=%d",
				c.rec.Name(), tel.Gain, tel.BeforeRMS, tel.AfterRMS, tel.BeforePeak, tel.AfterPeak, tel.Clipped)
		}

		triggerStr := "fallback_timer"
		if isBoundaryTrigger {
			triggerStr = "boundary"
		}
		rmsMean, rmsPeak, rmsErr := wavPCMLevelStats(wavPath)
		if rmsErr != nil && c.mgr.cfg.Verbose {
			log.Printf("recognizer [%s]: wav RMS stats skipped: %v", c.rec.Name(), rmsErr)
		}
		tel := &internallibrary.RecognitionAttemptContext{
			Trigger:           triggerStr,
			BoundaryEventID:   boundaryEventID,
			IsHardBoundary:    isHardBoundaryTrigger,
			Phase:             "primary",
			SkipMs:            int(skip.Milliseconds()),
			CaptureDurationMs: int(c.mgr.cfg.RecognizerCaptureDuration.Milliseconds()),
			PhysicalFormat: internallibrary.NormalizeRMSLearningFormatKey(
				c.mgr.currentPhysicalFormatForCalibration()),
		}
		if rmsErr == nil {
			tel.RMSMean, tel.RMSPeak = rmsMean, rmsPeak
		}
		recCtx := internallibrary.WithRecognitionAttemptContext(ctx, tel)

		result, err := c.rec.Recognize(recCtx, wavPath)
		os.Remove(wavPath)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			_ = c.handleRecognitionError(err, &backoffUntil, &backoffRateLimited)
			c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
				Outcome: internallibrary.FollowupOutcomeRecognitionError,
			})
			continue
		}

		// Final source guard: capture + network recognition is usually on the order
		// of several seconds to tens of seconds depending on capture length and APIs.
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
			c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
				Outcome: internallibrary.FollowupOutcomeDiscarded,
			})
			continue
		}

		backoffUntil = time.Time{}
		backoffRateLimited = false
		for _, id := range recognizerCanonicalIDs(c.rec) {
			c.mgr.clearProviderBackoff(id)
		}

		if result != nil {
			source, score := recognitionLogFields(result)
			log.Printf("recognizer [%s]: %s source=%s ids(acr=%q shazamio_id=%q)  %s — %s",
				c.rec.Name(), score, source, result.ACRID, result.ShazamID, result.Artist, result.Title)
			isShazamioFallback := result.ShazamID != "" && result.ACRID == ""
			shazamioMatchedACR := false

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
				thresholdMS := restoreThresholdMS(knownDurationMS, c.mgr.effectiveDurationPessimismForPhysicalPolicy())
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
					c.mgr.effectiveDurationPessimismForPhysicalPolicy(),
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
						c.mgr.physicalBoundarySensitive = preBoundaryBoundarySensitive
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
					same := false
					c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
						Outcome:      internallibrary.FollowupOutcomeSameTrackRestored,
						NewRecording: &same,
					})
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
			shazamioMatchedACR, stop = c.maybeConfirmCandidate(ctx, result, isBoundaryTrigger, tel)
			if stop {
				return
			}

			c.applyRecognizedResult(
				result,
				isBoundaryTrigger,
				isShazamioFallback,
				shazamioMatchedACR,
				captureStartedAt,
				shouldPersistRecognitionForInputPolicy(policy.Policy),
			)

			var collID, phID int64
			c.mgr.mu.Lock()
			collID = c.mgr.physicalLibraryEntryID
			phID = c.mgr.currentPlayHistoryID
			c.mgr.mu.Unlock()
			c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
				Outcome:           internallibrary.FollowupOutcomeMatched,
				PostACRID:         result.ACRID,
				PostShazamID:      result.ShazamID,
				PostCollectionID:  collID,
				PostPlayHistoryID: phID,
				NewRecording:      recognitionFollowupNewRecording(preBoundaryResult, result),
			})

			// Detect false-positive boundary: the boundary trigger fired but
			if c.shazamioRec != nil && result.ACRID != "" && !shazamioMatchedACR {
				go c.mgr.tryEnableShazamioContinuity(ctx, c.shazamioRec, result)
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
			c.linkBoundaryFollowup(isBoundaryTrigger, boundaryEventID, internallibrary.BoundaryRecognitionFollowup{
				Outcome: internallibrary.FollowupOutcomeNoMatch,
			})
			if backoffUntil.IsZero() {
				continue
			}
		}
		c.mgr.markDirty()
	}
}
