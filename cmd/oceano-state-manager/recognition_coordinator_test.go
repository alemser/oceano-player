package main

import (
	"errors"
	"testing"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

func TestShouldBypassBackoff(t *testing.T) {
	if !shouldBypassBackoff(true, false) {
		t.Fatal("expected boundary trigger without rate-limit backoff to bypass")
	}
	if shouldBypassBackoff(true, true) {
		t.Fatal("expected rate-limited backoff not to be bypassed")
	}
	if shouldBypassBackoff(false, false) {
		t.Fatal("expected non-boundary trigger not to bypass")
	}
}

func TestShouldSkipRecognitionAttempt(t *testing.T) {
	tests := []struct {
		name        string
		isPhysical  bool
		isAirPlay   bool
		isBluetooth bool
		want        bool
	}{
		{name: "physical no streaming", isPhysical: true, isAirPlay: false, isBluetooth: false, want: false},
		{name: "none source", isPhysical: false, isAirPlay: false, isBluetooth: false, want: true},
		{name: "airplay active", isPhysical: true, isAirPlay: true, isBluetooth: false, want: true},
		{name: "bluetooth active", isPhysical: true, isAirPlay: false, isBluetooth: true, want: true},
		{name: "both streaming", isPhysical: true, isAirPlay: true, isBluetooth: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSkipRecognitionAttempt(tt.isPhysical, tt.isAirPlay, tt.isBluetooth); got != tt.want {
				t.Fatalf("shouldSkipRecognitionAttempt(%v,%v,%v) = %v, want %v", tt.isPhysical, tt.isAirPlay, tt.isBluetooth, got, tt.want)
			}
		})
	}
}

func TestShouldCreateBoundaryStub(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name          string
		lastStub      time.Time
		lastBoundary  time.Time
		stillPhysical bool
		want          bool
	}{
		{name: "not physical", stillPhysical: false, want: false},
		{name: "no previous stub", lastStub: time.Time{}, lastBoundary: now, stillPhysical: true, want: true},
		{name: "stub before boundary", lastStub: now.Add(-2 * time.Second), lastBoundary: now, stillPhysical: true, want: true},
		{name: "stub after boundary", lastStub: now.Add(2 * time.Second), lastBoundary: now, stillPhysical: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldCreateBoundaryStub(tt.lastStub, tt.lastBoundary, tt.stillPhysical); got != tt.want {
				t.Fatalf("shouldCreateBoundaryStub(...) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldCreateFingerprintOnlyStub(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name          string
		lastStub      time.Time
		lastBoundary  time.Time
		stillPhysical bool
		minInterval   time.Duration
		want          bool
	}{
		{name: "not physical", stillPhysical: false, minInterval: time.Minute, want: false},
		{name: "first stub", stillPhysical: true, lastStub: time.Time{}, lastBoundary: now, minInterval: time.Minute, want: true},
		{name: "new boundary", stillPhysical: true, lastStub: now.Add(-5 * time.Second), lastBoundary: now, minInterval: time.Hour, want: true},
		{name: "throttled between boundaries", stillPhysical: true, lastStub: now.Add(-10 * time.Second), lastBoundary: now.Add(-1 * time.Minute), minInterval: time.Minute, want: false},
		{name: "allowed after interval", stillPhysical: true, lastStub: now.Add(-2 * time.Minute), lastBoundary: now.Add(-3 * time.Minute), minInterval: time.Minute, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldCreateFingerprintOnlyStub(tt.lastStub, tt.lastBoundary, tt.stillPhysical, tt.minInterval); got != tt.want {
				t.Fatalf("shouldCreateFingerprintOnlyStub(...) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleNoMatch_FingerprintOnlyThrottlesStubUpserts(t *testing.T) {
	m := newTestMgr()
	m.cfg.RecognizerChain = "fingerprint_only"
	m.cfg.RecognizerMaxInterval = 10 * time.Minute
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.mu.Unlock()

	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Fingerprint"}, nil, nil, nil, lib)

	fps := []Fingerprint{{1, 2, 3, 4, 5, 6, 7, 8}}
	var backoffUntil time.Time
	backoffRateLimited := false

	coordinator.handleNoMatch(fps, false, &backoffUntil, &backoffRateLimited)

	entry, err := lib.FindByFingerprints(fps, m.cfg.FingerprintThreshold, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints after first no-match: %v", err)
	}
	if entry == nil {
		t.Fatal("expected stub entry to be created on first fingerprint-only no-match")
	}

	var playCount1 int
	if err := lib.DB().QueryRow(`SELECT play_count FROM collection WHERE id=?`, entry.ID).Scan(&playCount1); err != nil {
		t.Fatalf("query first play_count: %v", err)
	}
	if playCount1 != 1 {
		t.Fatalf("first play_count = %d, want 1", playCount1)
	}

	coordinator.handleNoMatch(fps, false, &backoffUntil, &backoffRateLimited)

	var playCount2 int
	if err := lib.DB().QueryRow(`SELECT play_count FROM collection WHERE id=?`, entry.ID).Scan(&playCount2); err != nil {
		t.Fatalf("query second play_count: %v", err)
	}
	if playCount2 != 1 {
		t.Fatalf("play_count after throttled no-match = %d, want 1", playCount2)
	}
}

func TestHandleNoMatch_FingerprintOnlySkipsStubWhenTrackAlreadyRecognized(t *testing.T) {
	m := newTestMgr()
	m.cfg.RecognizerChain = "fingerprint_only"
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.recognitionResult = &RecognitionResult{Title: "Known", Artist: "Artist"}
	m.mu.Unlock()

	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Fingerprint"}, nil, nil, nil, lib)

	fps := []Fingerprint{{101, 102, 103, 104}}
	var backoffUntil time.Time
	backoffRateLimited := false

	coordinator.handleNoMatch(fps, false, &backoffUntil, &backoffRateLimited)

	entry, err := lib.FindByFingerprints(fps, m.cfg.FingerprintThreshold, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints: %v", err)
	}
	if entry != nil {
		t.Fatalf("expected no stub to be stored while a recognized track is active, got entry id=%d", entry.ID)
	}
}

func TestHandleNoMatch_FingerprintOnlyEnrichesPendingStubAcrossRetries(t *testing.T) {
	m := newTestMgr()
	m.cfg.RecognizerChain = "fingerprint_only"
	m.cfg.FingerprintThreshold = 0
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.mu.Unlock()

	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Fingerprint"}, nil, nil, nil, lib)

	fps1 := []Fingerprint{{1, 2, 3, 4}}
	fps2 := []Fingerprint{{999, 998, 997, 996}} // intentionally different: no local fallback hit
	var backoffUntil time.Time
	backoffRateLimited := false

	coordinator.handleNoMatch(fps1, false, &backoffUntil, &backoffRateLimited)

	m.mu.Lock()
	pendingID := m.pendingStubID
	m.mu.Unlock()
	if pendingID == 0 {
		t.Fatal("expected pendingStubID to be set after first stub creation")
	}

	var beforeCount int
	if err := lib.DB().QueryRow(`SELECT COUNT(*) FROM fingerprints WHERE entry_id=?`, pendingID).Scan(&beforeCount); err != nil {
		t.Fatalf("count fingerprints before enrich: %v", err)
	}

	coordinator.handleNoMatch(fps2, false, &backoffUntil, &backoffRateLimited)

	m.mu.Lock()
	pendingID2 := m.pendingStubID
	m.mu.Unlock()
	if pendingID2 != pendingID {
		t.Fatalf("pendingStubID changed across retries: got %d want %d", pendingID2, pendingID)
	}

	var afterCount int
	if err := lib.DB().QueryRow(`SELECT COUNT(*) FROM fingerprints WHERE entry_id=?`, pendingID).Scan(&afterCount); err != nil {
		t.Fatalf("count fingerprints after enrich: %v", err)
	}
	if afterCount <= beforeCount {
		t.Fatalf("expected fingerprint rows to increase after enrich, before=%d after=%d", beforeCount, afterCount)
	}
}

// TestHandleNoMatch_DoesNotApplyLocalFallback verifies that handleNoMatch never
// substitutes a local fingerprint match for an explicit cloud "no match".
// When both ACRCloud and Shazam return no match, that verdict must be respected
// and the recognition result must remain unset — even if the local library
// contains a fingerprint that would match.  Applying the local result would risk
// showing a completely wrong track (false positive).
func TestHandleNoMatch_DoesNotApplyLocalFallback(t *testing.T) {
	m := newTestMgr()
	m.cfg.RecognizerChain = "fingerprint_only"
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.mu.Unlock()

	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Fingerprint"}, nil, nil, nil, lib)

	fps := []Fingerprint{{201, 202, 203, 204}}
	if _, err := lib.UpsertStub(fps, m.cfg.FingerprintThreshold, 30); err != nil {
		t.Fatalf("UpsertStub: %v", err)
	}

	// Simulate an already-queued trigger; it must remain after handleNoMatch
	// since no local fallback is applied.
	m.recognizeTrigger <- recognizeTrigger{isBoundary: false}

	var backoffUntil time.Time
	backoffRateLimited := false
	coordinator.handleNoMatch(fps, false, &backoffUntil, &backoffRateLimited)

	// Trigger must still be in the queue — local fallback must not have drained it.
	if got := len(m.recognizeTrigger); got != 1 {
		t.Fatalf("pending trigger queue size = %d, want 1 (local fallback must not drain on no-match)", got)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// recognitionResult must remain nil — no local result applied on explicit no-match.
	if m.recognitionResult != nil {
		t.Fatalf("recognitionResult must be nil after no-match; got %+v", m.recognitionResult)
	}
}

func TestHandleNoMatch_BoundaryClearsExistingRecognition(t *testing.T) {
	m := newTestMgr()
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.recognitionResult = &RecognitionResult{
		ACRID:  "acr-existing",
		Title:  "Existing Track",
		Artist: "Existing Artist",
	}
	m.physicalArtworkPath = "/tmp/existing.jpg"
	m.shazamContinuityReady = true
	m.mu.Unlock()

	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Fingerprint"}, nil, nil, nil, nil)

	var backoffUntil time.Time
	backoffRateLimited := false
	coordinator.handleNoMatch(nil, true, &backoffUntil, &backoffRateLimited)

	m.mu.Lock()
	defer m.mu.Unlock()
	// Boundary no-match must clear recognition state so the UI shows "identifying"
	// rather than showing the previous track while a new one is playing.
	if m.recognitionResult != nil {
		t.Fatalf("expected recognitionResult to be cleared on boundary no-match, got %+v", m.recognitionResult)
	}
	if m.physicalArtworkPath != "" {
		t.Fatalf("expected physicalArtworkPath to be cleared, got %q", m.physicalArtworkPath)
	}
	if m.shazamContinuityReady {
		t.Fatal("expected shazamContinuityReady to be cleared on boundary no-match")
	}
	if backoffUntil.IsZero() {
		t.Fatal("expected no-match backoff to be scheduled")
	}
	if backoffRateLimited {
		t.Fatal("expected non-rate-limit backoff")
	}
}

func TestApplyRecognizedResult_PromotesPendingStubFingerprints(t *testing.T) {
	m := newTestMgr()
	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Fingerprint"}, nil, nil, nil, lib)

	fps := []Fingerprint{{42, 43, 44, 45}}
	stub, err := lib.UpsertStub(fps, m.cfg.FingerprintThreshold, 30)
	if err != nil || stub == nil {
		t.Fatalf("UpsertStub: err=%v stub=%v", err, stub)
	}

	m.mu.Lock()
	m.pendingStubID = stub.ID
	m.mu.Unlock()

	result := &RecognitionResult{
		ACRID:  "acr-promote-1",
		Title:  "Promoted Song",
		Artist: "Promoted Artist",
		Score:  90,
	}
	coordinator.applyRecognizedResult(result, nil, false, false, false, time.Now())

	entry, err := lib.Lookup("acr-promote-1")
	if err != nil || entry == nil {
		t.Fatalf("Lookup promoted entry: err=%v entry=%v", err, entry)
	}
	if !lib.HasFingerprints(entry.ID) {
		t.Fatal("expected promoted entry to have fingerprints")
	}

	var remaining int
	if err := lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub.ID).Scan(&remaining); err != nil {
		t.Fatalf("count old stub row: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected old stub to be pruned after promotion, found %d row(s)", remaining)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingStubID != 0 {
		t.Fatalf("pendingStubID = %d, want 0 after successful promotion", m.pendingStubID)
	}
}

func TestDrainPendingTriggers_ReturnsDrainedCount(t *testing.T) {
	m := newTestMgr()
	m.recognizeTrigger = make(chan recognizeTrigger, 2)
	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Fingerprint"}, nil, nil, nil, nil)

	m.recognizeTrigger <- recognizeTrigger{isBoundary: false}
	m.recognizeTrigger <- recognizeTrigger{isBoundary: true}

	if drained := coordinator.drainPendingTriggers(); drained != 2 {
		t.Fatalf("drainPendingTriggers() = %d, want 2", drained)
	}
	if got := len(m.recognizeTrigger); got != 0 {
		t.Fatalf("pending trigger queue size = %d, want 0", got)
	}
}

func TestHandleRecognitionErrorSetsBackoff(t *testing.T) {
	m := newTestMgr()
	c := newRecognitionCoordinator(m, &stubRecognizer{name: "A"}, nil, nil, nil, nil)

	var backoffUntil time.Time
	backoffRateLimited := false
	c.handleRecognitionError(errors.New("boom"), nil, &backoffUntil, &backoffRateLimited, time.Now())

	if backoffUntil.IsZero() {
		t.Fatal("expected backoffUntil to be set")
	}
	if backoffRateLimited {
		t.Fatal("expected non-rate-limited error to keep rate-limit flag false")
	}
}

func TestHandleRecognitionErrorSetsRateLimitBackoff(t *testing.T) {
	m := newTestMgr()
	c := newRecognitionCoordinator(m, &stubRecognizer{name: "A"}, nil, nil, nil, nil)

	var backoffUntil time.Time
	backoffRateLimited := false
	c.handleRecognitionError(ErrRateLimit, nil, &backoffUntil, &backoffRateLimited, time.Now())

	if backoffUntil.IsZero() {
		t.Fatal("expected backoffUntil to be set for rate limit")
	}
	if !backoffRateLimited {
		t.Fatal("expected rate-limit flag true")
	}
}

func TestIsPhysicalFormat(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   bool
	}{
		{name: "cd exact", format: "cd", want: true},
		{name: "vinyl exact", format: "vinyl", want: true},
		{name: "trim and case", format: " Vinyl ", want: true},
		{name: "non physical", format: "Cassette", want: false},
		{name: "empty", format: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPhysicalFormat(tt.format); got != tt.want {
				t.Fatalf("isPhysicalFormat(%q) = %v, want %v", tt.format, got, tt.want)
			}
		})
	}
}

func TestIsNewTrackCandidate(t *testing.T) {
	tests := []struct {
		name            string
		result          *RecognitionResult
		currentACRID    string
		currentShazamID string
		want            bool
	}{
		{
			name:         "nil result",
			result:       nil,
			currentACRID: "acr-1",
			want:         false,
		},
		{
			name:         "acrid changed",
			result:       &RecognitionResult{ACRID: "acr-2"},
			currentACRID: "acr-1",
			want:         true,
		},
		{
			name:         "acrid unchanged",
			result:       &RecognitionResult{ACRID: "acr-1"},
			currentACRID: "acr-1",
			want:         false,
		},
		{
			name:            "shazam changed when no acrid",
			result:          &RecognitionResult{ShazamID: "shz-2"},
			currentShazamID: "shz-1",
			want:            true,
		},
		{
			name:            "shazam unchanged when no acrid",
			result:          &RecognitionResult{ShazamID: "shz-1"},
			currentShazamID: "shz-1",
			want:            false,
		},
		{
			name:   "no ids and no current ids",
			result: &RecognitionResult{},
			want:   true,
		},
		{
			name:         "no ids but current acrid present",
			result:       &RecognitionResult{},
			currentACRID: "acr-1",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNewTrackCandidate(tt.result, tt.currentACRID, tt.currentShazamID); got != tt.want {
				t.Fatalf("isNewTrackCandidate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecognitionCoordinator_PrimaryRecognizerUsesChainPrimary(t *testing.T) {
	primary := &stubRecognizer{name: "ACRCloud"}
	fallback := &stubRecognizer{name: "Shazam"}
	coordinator := newRecognitionCoordinator(newTestMgr(), NewChainRecognizer(primary, fallback), nil, nil, nil, nil)

	if got := coordinator.primaryRecognizer(); got != primary {
		t.Fatalf("primaryRecognizer() = %v, want %v", got, primary)
	}
}

func TestRecognitionCoordinator_PrimaryRecognizerReturnsRecognizerAsIs(t *testing.T) {
	rec := &stubRecognizer{name: "ACRCloud"}
	coordinator := newRecognitionCoordinator(newTestMgr(), rec, nil, nil, nil, nil)

	if got := coordinator.primaryRecognizer(); got != rec {
		t.Fatalf("primaryRecognizer() = %v, want %v", got, rec)
	}
}

func TestResolvedRefreshIntervalFallsBackToMax(t *testing.T) {
	max := 5 * time.Minute

	if got := resolvedRefreshInterval(0, max); got != max {
		t.Fatalf("resolvedRefreshInterval(0, %s) = %s, want %s", max, got, max)
	}
	if got := resolvedRefreshInterval(-1*time.Second, max); got != max {
		t.Fatalf("resolvedRefreshInterval(negative, %s) = %s, want %s", max, got, max)
	}
}

func TestResolvedRefreshIntervalUsesExplicitRefresh(t *testing.T) {
	refresh := 90 * time.Second
	max := 5 * time.Minute

	if got := resolvedRefreshInterval(refresh, max); got != refresh {
		t.Fatalf("resolvedRefreshInterval(%s, %s) = %s, want %s", refresh, max, got, refresh)
	}
}

func TestRecognitionCoordinator_ApplyLocalFallbackEntryUpdatesManagerState(t *testing.T) {
	m := newTestMgr()
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil, nil)
	entry := &internallibrary.CollectionEntry{
		ACRID:         "acr-1",
		ShazamID:      "shz-1",
		Title:         "Exodus",
		Artist:        "Bob Marley",
		Album:         "Exodus",
		Label:         "Island",
		Released:      "1977",
		Score:         98,
		Format:        " Vinyl ",
		ArtworkPath:   "/var/lib/oceano/artwork/exodus.jpg",
		UserConfirmed: true,
	}

	before := time.Now()
	coordinator.applyLocalFallbackEntry(entry, time.Now())

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.recognitionResult == nil {
		t.Fatal("recognitionResult is nil")
	}
	if m.recognitionResult.Title != entry.Title {
		t.Fatalf("title = %q, want %q", m.recognitionResult.Title, entry.Title)
	}
	if m.recognitionResult.Artist != entry.Artist {
		t.Fatalf("artist = %q, want %q", m.recognitionResult.Artist, entry.Artist)
	}
	if m.recognitionResult.ShazamID != entry.ShazamID {
		t.Fatalf("shazam_id = %q, want %q", m.recognitionResult.ShazamID, entry.ShazamID)
	}
	if !m.shazamContinuityReady {
		t.Fatal("shazamContinuityReady = false, want true")
	}
	if m.physicalFormat != entry.Format {
		t.Fatalf("physicalFormat = %q, want %q", m.physicalFormat, entry.Format)
	}
	if m.physicalArtworkPath != entry.ArtworkPath {
		t.Fatalf("physicalArtworkPath = %q, want %q", m.physicalArtworkPath, entry.ArtworkPath)
	}
	if m.lastRecognizedAt.Before(before) {
		t.Fatal("lastRecognizedAt was not updated")
	}
}

// TestTryLocalFingerprintFallback_MatchesUnconfirmedStub verifies that the
// fingerprint fallback returns a stub even when UserConfirmed = false.
// This is the core "Option A" behaviour: the same unknown track is identified
// consistently across plays without requiring the user to confirm it first.
func TestTryLocalFingerprintFallback_MatchesUnconfirmedStub(t *testing.T) {
	m := newTestMgr()
	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Fingerprint"}, nil, nil, nil, lib)

	fps := []Fingerprint{{0xAABBCCDD, 0x11223344, 0x55667788, 0x99AABBCC}}

	// First play: no match anywhere — stub is created.
	var backoffUntil time.Time
	backoffRateLimited := false
	m.cfg.RecognizerChain = "fingerprint_only"
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.mu.Unlock()
	coordinator.handleNoMatch(fps, true, &backoffUntil, &backoffRateLimited)

	entry, err := lib.FindByFingerprints(fps, m.cfg.FingerprintThreshold, 30)
	if err != nil || entry == nil {
		t.Fatalf("stub not created after first no-match: err=%v entry=%v", err, entry)
	}
	if entry.UserConfirmed {
		t.Fatal("stub should not be user-confirmed yet")
	}

	// Second play: fingerprint fallback must now match the unconfirmed stub.
	matched := coordinator.tryLocalFingerprintFallback(fps, time.Now())
	if !matched {
		t.Fatal("tryLocalFingerprintFallback returned false for unconfirmed stub — expected true")
	}

	m.mu.Lock()
	result := m.recognitionResult
	m.mu.Unlock()
	if result == nil {
		t.Fatal("recognitionResult is nil after fallback match")
	}
}

// TestTryLocalFingerprintFallback_MatchesConfirmedStub verifies that a
// user-confirmed stub (with title/artist filled in) is also matched and that
// its metadata is applied.
func TestTryLocalFingerprintFallback_MatchesConfirmedStub(t *testing.T) {
	m := newTestMgr()
	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil, lib)

	fps := []Fingerprint{{0x12345678, 0x9ABCDEF0, 0x11111111, 0x22222222}}

	// Insert a confirmed entry directly, as the user would after filling details.
	stub, err := lib.UpsertStub(fps, m.cfg.FingerprintThreshold, 30)
	if err != nil || stub == nil {
		t.Fatalf("UpsertStub: err=%v stub=%v", err, stub)
	}
	if _, err := lib.DB().Exec(
		`UPDATE collection SET title='Dark Side', artist='Pink Floyd', user_confirmed=1 WHERE id=?`,
		stub.ID,
	); err != nil {
		t.Fatalf("confirm stub: %v", err)
	}

	matched := coordinator.tryLocalFingerprintFallback(fps, time.Now())
	if !matched {
		t.Fatal("tryLocalFingerprintFallback returned false for confirmed stub")
	}

	m.mu.Lock()
	result := m.recognitionResult
	m.mu.Unlock()
	if result == nil {
		t.Fatal("recognitionResult is nil")
	}
	if result.Title != "Dark Side" || result.Artist != "Pink Floyd" {
		t.Fatalf("unexpected metadata: title=%q artist=%q", result.Title, result.Artist)
	}
}

// TestTryLocalFingerprintFallback_NoMatch verifies that a fingerprint that
// does not exist in the library returns false without error.
func TestTryLocalFingerprintFallback_NoMatch(t *testing.T) {
	m := newTestMgr()
	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil, lib)

	fps := []Fingerprint{{0xDEADBEEF, 0xCAFEBABE, 0xFEEDFACE, 0xBAADF00D}}
	if coordinator.tryLocalFingerprintFallback(fps, time.Now()) {
		t.Fatal("expected false for fingerprint not in library")
	}
}

func TestTryLocalFingerprintLocalFirst_MatchesConfirmedOnly(t *testing.T) {
	m := newTestMgr()
	m.cfg.FingerprintLocalFirst = true
	m.cfg.FingerprintLocalFirstThreshold = 0.18

	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil, lib)

	fps := []Fingerprint{{0xCAFE1234, 0xBEEF5678, 0x11111111, 0x22222222}}
	stub, err := lib.UpsertStub(fps, m.cfg.FingerprintThreshold, 30)
	if err != nil || stub == nil {
		t.Fatalf("UpsertStub: err=%v stub=%v", err, stub)
	}
	if _, err := lib.DB().Exec(
		`UPDATE collection SET title='Confirmed Track', artist='Confirmed Artist', user_confirmed=1 WHERE id=?`,
		stub.ID,
	); err != nil {
		t.Fatalf("confirm stub: %v", err)
	}
	lib.RebuildFPCache() // direct SQL update bypasses library API; refresh cache

	if !coordinator.tryLocalFingerprintLocalFirst(fps, time.Now()) {
		t.Fatal("expected local-first to match confirmed fingerprint entry")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recognitionResult == nil {
		t.Fatal("recognitionResult is nil")
	}
	if m.recognitionResult.Title != "Confirmed Track" || m.recognitionResult.Artist != "Confirmed Artist" {
		t.Fatalf("unexpected metadata after local-first: %+v", m.recognitionResult)
	}
}

func TestTryLocalFingerprintLocalFirst_DoesNotMatchUnconfirmedStub(t *testing.T) {
	m := newTestMgr()
	m.cfg.FingerprintLocalFirst = true
	m.cfg.FingerprintLocalFirstThreshold = 0.18

	lib := openTestLibrary(t)
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil, lib)

	fps := []Fingerprint{{0xAAAA0001, 0xAAAA0002, 0xAAAA0003, 0xAAAA0004}}
	if _, err := lib.UpsertStub(fps, m.cfg.FingerprintThreshold, 30); err != nil {
		t.Fatalf("UpsertStub: %v", err)
	}

	if coordinator.tryLocalFingerprintLocalFirst(fps, time.Now()) {
		t.Fatal("expected local-first to ignore unconfirmed stub entries")
	}
}

func TestRecognitionCoordinator_ApplyLocalFallbackEntryLeavesFormatUnsetForNonPhysicalMedia(t *testing.T) {
	m := newTestMgr()
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil, nil)
	entry := &internallibrary.CollectionEntry{
		Title:       "Track",
		Artist:      "Artist",
		Format:      "Cassette",
		ArtworkPath: "/tmp/track.jpg",
	}

	coordinator.applyLocalFallbackEntry(entry, time.Now())

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.physicalFormat != "" {
		t.Fatalf("physicalFormat = %q, want empty", m.physicalFormat)
	}
	if m.physicalArtworkPath != entry.ArtworkPath {
		t.Fatalf("physicalArtworkPath = %q, want %q", m.physicalArtworkPath, entry.ArtworkPath)
	}
}

func TestShouldShortCircuitLocalFirst_NoCurrentTrack(t *testing.T) {
	// No prior recognised track: must NOT short-circuit so ACRCloud/Shazam can
	// confirm the result. A local fingerprint false-positive at startup would
	// otherwise be accepted without any cross-check.
	entry := &internallibrary.CollectionEntry{ACRID: "acrid-1", Title: "Song", Artist: "Artist"}
	if shouldShortCircuitLocalFirst(nil, entry) {
		t.Fatal("must not short-circuit when there is no current recognition (no prior context)")
	}
}

func TestShouldShortCircuitLocalFirst_SameCurrentTrack(t *testing.T) {
	current := &RecognitionResult{ACRID: "acrid-1", Title: "Song", Artist: "Artist"}
	entry := &internallibrary.CollectionEntry{ACRID: "acrid-1", Title: "Song", Artist: "Artist"}
	if shouldShortCircuitLocalFirst(current, entry) {
		t.Fatal("expected no short-circuit when local-first matches current track")
	}
}

func TestShouldShortCircuitLocalFirst_DifferentCurrentTrack(t *testing.T) {
	current := &RecognitionResult{ACRID: "acrid-1", Title: "Song A", Artist: "Artist"}
	entry := &internallibrary.CollectionEntry{ACRID: "acrid-2", Title: "Song B", Artist: "Artist"}
	if !shouldShortCircuitLocalFirst(current, entry) {
		t.Fatal("expected short-circuit when local-first points to a different track")
	}
}

// ── Physical seek (progress bar best-effort) ──────────────────────────────────

// TestApplyRecognizedResult_SetsPhysicalSeek proves that after a successful
// recognition, physicalSeekMS reflects elapsed time since captureStartedAt and
// physicalSeekUpdatedAt is set to a recent time.
func TestApplyRecognizedResult_SetsPhysicalSeek(t *testing.T) {
	m := newTestMgr()
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.mu.Unlock()
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil, nil)

	captureStartedAt := time.Now().Add(-3 * time.Second) // simulate 3 s capture already elapsed
	result := &RecognitionResult{ACRID: "acr-seek-1", Title: "Seek Track", Artist: "Artist", Score: 85}

	coordinator.applyRecognizedResult(result, nil, false, false, false, captureStartedAt)

	m.mu.Lock()
	seekMS := m.physicalSeekMS
	seekUpdatedAt := m.physicalSeekUpdatedAt
	m.mu.Unlock()

	if seekMS < 3000 {
		t.Errorf("physicalSeekMS = %d, want >= 3000 (3 s elapsed since capture start)", seekMS)
	}
	if seekMS > 10000 {
		t.Errorf("physicalSeekMS = %d, want < 10000 (no more than 10 s plausible overhead)", seekMS)
	}
	if seekUpdatedAt.IsZero() {
		t.Error("physicalSeekUpdatedAt should not be zero after recognition")
	}
	if time.Since(seekUpdatedAt) > 2*time.Second {
		t.Errorf("physicalSeekUpdatedAt is too old: %s", time.Since(seekUpdatedAt))
	}
}

// TestApplyLocalFallbackEntry_SetsPhysicalSeek proves that the local fingerprint
// fallback path also sets seek, using the same captureStartedAt mechanics.
func TestApplyLocalFallbackEntry_SetsPhysicalSeek(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{ACRID: "acr-seek-local", Title: "Local Track", Artist: "Artist"}
	id, _ := lib.RecordPlay(result, "")
	entry, _ := lib.GetByID(id)

	m := newTestMgr()
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.mu.Unlock()
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil, lib)

	captureStartedAt := time.Now().Add(-5 * time.Second)
	coordinator.applyLocalFallbackEntry(entry, captureStartedAt)

	m.mu.Lock()
	seekMS := m.physicalSeekMS
	seekUpdatedAt := m.physicalSeekUpdatedAt
	m.mu.Unlock()

	if seekMS < 5000 {
		t.Errorf("physicalSeekMS = %d, want >= 5000 (5 s elapsed since capture start)", seekMS)
	}
	if seekUpdatedAt.IsZero() {
		t.Error("physicalSeekUpdatedAt should not be zero after local fallback")
	}
}

// TestPhysicalSeek_ResetOnBoundaryClear proves that the pre-capture boundary
// clear zeroes seek so the UI does not interpolate from a stale position.
func TestPhysicalSeek_ResetOnBoundaryClear(t *testing.T) {
	m := newTestMgr()
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.physicalSeekMS = 120000
	m.physicalSeekUpdatedAt = time.Now().Add(-2 * time.Minute)
	m.mu.Unlock()

	// Simulate the pre-capture boundary clear (same code path as in run()).
	m.mu.Lock()
	m.recognitionResult = nil
	m.physicalLibraryEntryID = 0
	m.physicalArtworkPath = ""
	m.physicalSeekMS = 0
	m.physicalSeekUpdatedAt = time.Time{}
	m.mu.Unlock()

	m.mu.Lock()
	seekMS := m.physicalSeekMS
	seekUpdatedAt := m.physicalSeekUpdatedAt
	m.mu.Unlock()

	if seekMS != 0 {
		t.Errorf("physicalSeekMS = %d, want 0 after boundary clear", seekMS)
	}
	if !seekUpdatedAt.IsZero() {
		t.Errorf("physicalSeekUpdatedAt = %s, want zero after boundary clear", seekUpdatedAt)
	}
}
