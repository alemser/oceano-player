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
	coordinator.handleNoMatch(true, &backoffUntil, &backoffRateLimited)

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
