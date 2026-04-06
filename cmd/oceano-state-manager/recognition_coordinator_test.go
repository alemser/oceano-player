package main

import (
	"testing"
	"time"
)

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
