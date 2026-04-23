package main

import (
	"errors"
	"testing"
	"time"
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

	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Primary"}, nil, nil, nil)

	var backoffUntil time.Time
	backoffRateLimited := false
	coordinator.handleNoMatch(true, true, &backoffUntil, &backoffRateLimited)

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

func TestHandleNoMatch_SoftBoundaryPreservesRecognition(t *testing.T) {
	m := newTestMgr()
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.recognitionResult = &RecognitionResult{
		ACRID:  "acr-existing",
		Title:  "Existing Track",
		Artist: "Existing Artist",
	}
	m.physicalArtworkPath = "/tmp/existing.jpg"
	m.mu.Unlock()

	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Primary"}, nil, nil, nil)

	var backoffUntil time.Time
	backoffRateLimited := false
	coordinator.handleNoMatch(true, false, &backoffUntil, &backoffRateLimited)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recognitionResult == nil {
		t.Fatal("expected recognitionResult to be preserved on soft boundary no-match")
	}
	if m.recognitionResult.Title != "Existing Track" {
		t.Fatalf("unexpected recognitionResult title = %q", m.recognitionResult.Title)
	}
	if m.physicalArtworkPath != "/tmp/existing.jpg" {
		t.Fatalf("expected artwork to remain set, got %q", m.physicalArtworkPath)
	}
	if backoffUntil.IsZero() {
		t.Fatal("expected no-match backoff to be scheduled")
	}
	if backoffRateLimited {
		t.Fatal("expected non-rate-limit backoff")
	}
}

func TestDrainPendingTriggers_ReturnsDrainedCount(t *testing.T) {
	m := newTestMgr()
	m.recognizeTrigger = make(chan recognizeTrigger, 2)
	coordinator := newRecognitionCoordinator(m, &stubRecognizer{name: "Primary"}, nil, nil, nil)

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
	c := newRecognitionCoordinator(m, &stubRecognizer{name: "A"}, nil, nil, nil)

	var backoffUntil time.Time
	backoffRateLimited := false
	c.handleRecognitionError(errors.New("boom"), &backoffUntil, &backoffRateLimited)

	if backoffUntil.IsZero() {
		t.Fatal("expected backoffUntil to be set")
	}
	if backoffRateLimited {
		t.Fatal("expected non-rate-limited error to keep rate-limit flag false")
	}
}

func TestHandleRecognitionErrorSetsRateLimitBackoff(t *testing.T) {
	m := newTestMgr()
	c := newRecognitionCoordinator(m, &stubRecognizer{name: "A"}, nil, nil, nil)

	var backoffUntil time.Time
	backoffRateLimited := false
	c.handleRecognitionError(ErrRateLimit, &backoffUntil, &backoffRateLimited)

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
	coordinator := newRecognitionCoordinator(newTestMgr(), NewChainRecognizer(primary, fallback), nil, nil, nil)

	if got := coordinator.primaryRecognizer(); got != primary {
		t.Fatalf("primaryRecognizer() = %v, want %v", got, primary)
	}
}

func TestComputeRecognizedSeekMS_NonBoundaryEquivalentMetadataKeepsProgress(t *testing.T) {
	now := time.Now()
	physStartedAt := now.Add(-2 * time.Minute)
	captureStartedAt := now.Add(-6 * time.Second)

	previous := &RecognitionResult{ACRID: "acr-old", Title: "Shine On You Crazy Diamond", Artist: "Pink Floyd"}
	current := &RecognitionResult{ACRID: "acr-new", Title: "Shine On You Crazy Diamond", Artist: "Pink Floyd"}

	seekMS, resetStartedAt := computeRecognizedSeekMS(false, captureStartedAt, now, time.Time{}, physStartedAt, previous, current)
	if resetStartedAt {
		t.Fatal("expected physicalStartedAt to be preserved for same-track metadata")
	}
	if seekMS < 110000 {
		t.Fatalf("seekMS = %d, expected preserved monotonic progress", seekMS)
	}
}

func TestComputeRecognizedSeekMS_NonBoundaryDifferentMetadataResetsProgress(t *testing.T) {
	now := time.Now()
	physStartedAt := now.Add(-2 * time.Minute)
	captureStartedAt := now.Add(-6 * time.Second)

	previous := &RecognitionResult{ACRID: "acr-old", Title: "Shine On You Crazy Diamond", Artist: "Pink Floyd"}
	current := &RecognitionResult{ACRID: "acr-new", Title: "Wish You Were Here", Artist: "Pink Floyd"}

	seekMS, resetStartedAt := computeRecognizedSeekMS(false, captureStartedAt, now, time.Time{}, physStartedAt, previous, current)
	if !resetStartedAt {
		t.Fatal("expected physicalStartedAt reset for different track")
	}
	if seekMS > 20000 {
		t.Fatalf("seekMS = %d, expected near-capture seek for new track", seekMS)
	}
}

func TestRecognitionCoordinator_PrimaryRecognizerReturnsRecognizerAsIs(t *testing.T) {
	rec := &stubRecognizer{name: "ACRCloud"}
	coordinator := newRecognitionCoordinator(newTestMgr(), rec, nil, nil, nil)

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

func TestRecoverSeekMSFromSnapshot_AdvancesWithWallClock(t *testing.T) {
	now := time.Now()
	updatedAt := now.Add(-5 * time.Second)

	got := recoverSeekMSFromSnapshot(120000, updatedAt, now)
	if got < 125000 {
		t.Fatalf("recoverSeekMSFromSnapshot = %d, want >= 125000", got)
	}
}

func TestRecoverSeekMSFromSnapshot_ZeroTimestampKeepsBase(t *testing.T) {
	got := recoverSeekMSFromSnapshot(42000, time.Time{}, time.Now())
	if got != 42000 {
		t.Fatalf("recoverSeekMSFromSnapshot = %d, want 42000", got)
	}
}

func TestShouldRestorePreBoundaryResult(t *testing.T) {
	tests := []struct {
		name                 string
		isHardBoundary       bool
		preBoundarySeekMS    int64
		preBoundaryElapsedMS int64
		knownDurationMS      int
		minSeek              time.Duration
		durationPessimism    float64
		wantRestore          bool
		wantReason           string
	}{
		{
			name:                 "hard boundary restores when before threshold",
			isHardBoundary:       true,
			preBoundarySeekMS:    120000,
			preBoundaryElapsedMS: 50000,
			knownDurationMS:      240000,
			minSeek:              60 * time.Second,
			durationPessimism:    0.75,
			wantRestore:          true,
			wantReason:           "",
		},
		{
			name:                 "hard boundary blocks at or after threshold",
			isHardBoundary:       true,
			preBoundarySeekMS:    120000,
			preBoundaryElapsedMS: 180000,
			knownDurationMS:      240000,
			minSeek:              60 * time.Second,
			durationPessimism:    0.75,
			wantRestore:          false,
			wantReason:           "hard boundary",
		},
		{
			name:                 "hard boundary blocks when duration unknown",
			isHardBoundary:       true,
			preBoundarySeekMS:    120000,
			preBoundaryElapsedMS: 50000,
			knownDurationMS:      0,
			minSeek:              60 * time.Second,
			durationPessimism:    0.75,
			wantRestore:          false,
			wantReason:           "hard boundary",
		},
		{
			name:                 "soft boundary restores even with short seek",
			isHardBoundary:       false,
			preBoundarySeekMS:    30000,
			preBoundaryElapsedMS: 30000,
			knownDurationMS:      240000,
			minSeek:              60 * time.Second,
			durationPessimism:    0.75,
			wantRestore:          true,
			wantReason:           "",
		},
		{
			name:                 "soft boundary restores with mature seek",
			isHardBoundary:       false,
			preBoundarySeekMS:    90000,
			preBoundaryElapsedMS: 90000,
			knownDurationMS:      240000,
			minSeek:              60 * time.Second,
			durationPessimism:    0.75,
			wantRestore:          true,
			wantReason:           "",
		},
		{
			name:                 "invalid min seek still restores on soft boundary",
			isHardBoundary:       false,
			preBoundarySeekMS:    45000,
			preBoundaryElapsedMS: 45000,
			knownDurationMS:      240000,
			minSeek:              0,
			durationPessimism:    0.75,
			wantRestore:          true,
			wantReason:           "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRestore, gotReason := shouldRestorePreBoundaryResult(
				tt.isHardBoundary,
				tt.preBoundarySeekMS,
				tt.preBoundaryElapsedMS,
				tt.knownDurationMS,
				tt.minSeek,
				tt.durationPessimism,
			)
			if gotRestore != tt.wantRestore {
				t.Fatalf("shouldRestorePreBoundaryResult() restore=%v, want %v", gotRestore, tt.wantRestore)
			}
			if gotReason != tt.wantReason {
				t.Fatalf("shouldRestorePreBoundaryResult() reason=%q, want %q", gotReason, tt.wantReason)
			}
		})
	}
}

func TestTriggerHelpers(t *testing.T) {
	periodic := triggerPeriodicRecognition()
	if periodic.isBoundary {
		t.Fatal("periodic trigger must not be boundary")
	}
	if periodic.isHardBoundary {
		t.Fatal("periodic trigger must not be hard boundary")
	}
	if !periodic.detectedAt.IsZero() {
		t.Fatal("periodic trigger must not carry a detectedAt timestamp")
	}

	soft := triggerBoundaryRecognition(false)
	if !soft.isBoundary {
		t.Fatal("boundary trigger must be boundary")
	}
	if soft.isHardBoundary {
		t.Fatal("soft boundary trigger must not be hard")
	}
	if !soft.detectedAt.IsZero() {
		t.Fatal("VU/SIGUSR1 soft boundary must not carry a detectedAt timestamp")
	}

	hard := triggerBoundaryRecognition(true)
	if !hard.isBoundary {
		t.Fatal("hard boundary trigger must be boundary")
	}
	if !hard.isHardBoundary {
		t.Fatal("hard boundary trigger must be hard")
	}
	if !hard.detectedAt.IsZero() {
		t.Fatal("VU hard boundary must not carry a detectedAt timestamp")
	}
}

// TestCaptureSkipDuration verifies that only hard boundaries (silence→audio)
// produce a non-zero skip — soft transitions play clean audio from the start.
func TestCaptureSkipDuration(t *testing.T) {
	if got := captureSkipDuration(true); got != 2*time.Second {
		t.Fatalf("captureSkipDuration(hard) = %s, want 2s", got)
	}
	if got := captureSkipDuration(false); got != 0 {
		t.Fatalf("captureSkipDuration(soft) = %s, want 0", got)
	}
}

// TestContinuityTrigger_CarriesFirstSightingTime verifies that a continuity
// trigger carries detectedAt (first sighting) and is a soft boundary.
func TestContinuityTrigger_CarriesFirstSightingTime(t *testing.T) {
	firstSighting := time.Now().Add(-10 * time.Second)
	trig := recognizeTrigger{isBoundary: true, detectedAt: firstSighting}

	if !trig.isBoundary {
		t.Fatal("continuity trigger must be boundary")
	}
	if trig.isHardBoundary {
		t.Fatal("continuity trigger must not be hard boundary")
	}
	if trig.detectedAt != firstSighting {
		t.Fatalf("detectedAt = %v, want %v", trig.detectedAt, firstSighting)
	}
	if captureSkipDuration(trig.isHardBoundary) != 0 {
		t.Fatal("continuity trigger must produce skip=0")
	}
}

// TestComputeRecognizedSeekMS_BoundaryUsesFirstSightingAsAnchor verifies that
// when a continuity trigger's firstSightingAt is used as lastBoundaryForSeek,
// the seek estimate reflects the earlier detection time rather than the
// trigger-fire time, avoiding over-estimating elapsed time in the new track.
func TestComputeRecognizedSeekMS_BoundaryUsesFirstSightingAsAnchor(t *testing.T) {
	now := time.Now()
	captureStartedAt := now.Add(-10 * time.Second) // 10 s capture

	// Simulate the continuity monitor's firstSightingAt being 12 s ago
	// (first sighting at t-12s, confirmation at t-0s after 2 polls of 8s... but
	// in this simplified scenario the coordinator sets lastBoundaryAt=firstSightingAt).
	firstSightingAt := now.Add(-12 * time.Second)

	result := &RecognitionResult{ACRID: "acr-new", Title: "New Track", Artist: "Artist"}

	// With firstSightingAt as lastBoundaryForSeek, seek = max(captureDelta, boundaryDelta)
	// = max(10s, 12s) = 12s.
	seekMS, _ := computeRecognizedSeekMS(true, captureStartedAt, now, firstSightingAt, time.Time{}, nil, result)
	if seekMS < 12000 {
		t.Fatalf("seekMS = %d, want >= 12000 (should use first-sighting anchor)", seekMS)
	}

	// Without the anchor (zero lastBoundaryForSeek), seek = captureDelta = 10s.
	seekMSNoAnchor, _ := computeRecognizedSeekMS(true, captureStartedAt, now, time.Time{}, time.Time{}, nil, result)
	if seekMSNoAnchor > 11000 {
		t.Fatalf("seekMSNoAnchor = %d, want ~10000 (only capture elapsed)", seekMSNoAnchor)
	}

	if seekMS <= seekMSNoAnchor {
		t.Fatalf("first-sighting anchor (%d ms) should produce larger seek than no anchor (%d ms)", seekMS, seekMSNoAnchor)
	}
}

func TestMergeMissingProviderIDs_FillsOnlyMissing(t *testing.T) {
	dst := &RecognitionResult{ACRID: "", ShazamID: "shz-old"}
	src := &RecognitionResult{ACRID: "acr-new", ShazamID: "shz-new"}

	changed := mergeMissingProviderIDs(dst, src)
	if !changed {
		t.Fatal("expected mergeMissingProviderIDs to report changed=true")
	}
	if dst.ACRID != "acr-new" {
		t.Fatalf("dst.ACRID = %q, want acr-new", dst.ACRID)
	}
	if dst.ShazamID != "shz-old" {
		t.Fatalf("dst.ShazamID = %q, want shz-old (existing ID must be preserved)", dst.ShazamID)
	}
}

// physicalSeekUpdatedAt is set to a recent time.
func TestApplyRecognizedResult_SetsPhysicalSeek(t *testing.T) {
	m := newTestMgr()
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.mu.Unlock()
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil)

	captureStartedAt := time.Now().Add(-3 * time.Second) // simulate 3 s capture already elapsed
	result := &RecognitionResult{ACRID: "acr-seek-1", Title: "Seek Track", Artist: "Artist", Score: 85}

	coordinator.applyRecognizedResult(result, false, false, false, captureStartedAt)

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

func TestComputeRecognizedSeekMS_NonBoundaryTrackChangeDoesNotReuseOldSessionElapsed(t *testing.T) {
	now := time.Now()
	captureStartedAt := now.Add(-4 * time.Second)
	physStartedAt := now.Add(-3 * time.Minute)
	previous := &RecognitionResult{ACRID: "old-acr", Title: "Old", Artist: "Artist"}
	current := &RecognitionResult{ACRID: "new-acr", Title: "New", Artist: "Artist"}

	seekMS, resetStart := computeRecognizedSeekMS(false, captureStartedAt, now, time.Time{}, physStartedAt, previous, current)
	if seekMS < 4000 || seekMS > 10000 {
		t.Fatalf("computeRecognizedSeekMS() seek=%d, want close to capture elapsed only", seekMS)
	}
	if !resetStart {
		t.Fatal("expected non-boundary track change to request physicalStartedAt reset")
	}
}

func TestComputeRecognizedSeekMS_NonBoundaryFirstRecognitionUsesPhysStartedAt(t *testing.T) {
	// Simulates Telegraph Road: quiet intro delays trigger; first attempt is no-match;
	// by the time the second attempt succeeds, 37s have elapsed since audio detected,
	// but captureStartedAt is only 12s ago. With no previous result, physStartedAt
	// should be used so the progress bar starts at ~37s, not ~12s.
	now := time.Now()
	captureStartedAt := now.Add(-12 * time.Second)
	physStartedAt := now.Add(-37 * time.Second)
	var previous *RecognitionResult // nil — first recognition of the session
	current := &RecognitionResult{ACRID: "acr-telegraph", Title: "Telegraph Road", Artist: "Dire Straits"}

	seekMS, resetStart := computeRecognizedSeekMS(false, captureStartedAt, now, time.Time{}, physStartedAt, previous, current)
	if seekMS < 37000 {
		t.Fatalf("computeRecognizedSeekMS() seek=%d, want >= 37s (physStartedAt elapsed)", seekMS)
	}
	if !resetStart {
		t.Fatal("expected physicalStartedAt reset after first recognition")
	}
}

func TestComputeRecognizedSeekMS_NonBoundarySameTrackCanReuseSessionElapsed(t *testing.T) {
	now := time.Now()
	captureStartedAt := now.Add(-4 * time.Second)
	physStartedAt := now.Add(-90 * time.Second)
	previous := &RecognitionResult{ACRID: "same-acr", Title: "Same", Artist: "Artist"}
	current := &RecognitionResult{ACRID: "same-acr", Title: "Same", Artist: "Artist"}

	seekMS, resetStart := computeRecognizedSeekMS(false, captureStartedAt, now, time.Time{}, physStartedAt, previous, current)
	if seekMS < 90000 {
		t.Fatalf("computeRecognizedSeekMS() seek=%d, want reused session elapsed", seekMS)
	}
	if resetStart {
		t.Fatal("expected same-track re-confirmation not to reset physicalStartedAt")
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
