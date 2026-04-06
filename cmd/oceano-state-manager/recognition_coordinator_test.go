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
		name       string
		isPhysical bool
		isAirPlay  bool
		want       bool
	}{
		{name: "physical no airplay", isPhysical: true, isAirPlay: false, want: false},
		{name: "none source", isPhysical: false, isAirPlay: false, want: true},
		{name: "airplay active", isPhysical: true, isAirPlay: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSkipRecognitionAttempt(tt.isPhysical, tt.isAirPlay); got != tt.want {
				t.Fatalf("shouldSkipRecognitionAttempt(%v,%v) = %v, want %v", tt.isPhysical, tt.isAirPlay, got, tt.want)
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

func TestHandleRecognitionErrorSetsBackoff(t *testing.T) {
	m := newTestMgr()
	c := newRecognitionCoordinator(m, &stubRecognizer{name: "A"}, nil, nil, nil, nil)

	var backoffUntil time.Time
	backoffRateLimited := false
	c.handleRecognitionError(errors.New("boom"), nil, &backoffUntil, &backoffRateLimited)

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
	c.handleRecognitionError(ErrRateLimit, nil, &backoffUntil, &backoffRateLimited)

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
	entry := &CollectionEntry{
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
	coordinator.applyLocalFallbackEntry(entry)

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

func TestRecognitionCoordinator_ApplyLocalFallbackEntryLeavesFormatUnsetForNonPhysicalMedia(t *testing.T) {
	m := newTestMgr()
	coordinator := newRecognitionCoordinator(m, nil, nil, nil, nil, nil)
	entry := &CollectionEntry{
		Title:       "Track",
		Artist:      "Artist",
		Format:      "Cassette",
		ArtworkPath: "/tmp/track.jpg",
	}

	coordinator.applyLocalFallbackEntry(entry)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.physicalFormat != "" {
		t.Fatalf("physicalFormat = %q, want empty", m.physicalFormat)
	}
	if m.physicalArtworkPath != entry.ArtworkPath {
		t.Fatalf("physicalArtworkPath = %q, want %q", m.physicalArtworkPath, entry.ArtworkPath)
	}
}
