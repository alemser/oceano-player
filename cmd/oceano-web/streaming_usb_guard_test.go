package main

import (
	"testing"
	"time"
)

func TestShouldEnsureUSBForStreamingPlayback(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		playback     string
		wantDecision bool
	}{
		{name: "airplay playing", source: "AirPlay", playback: "playing", wantDecision: true},
		{name: "bluetooth playing", source: "Bluetooth", playback: "playing", wantDecision: true},
		{name: "airplay stopped", source: "AirPlay", playback: "stopped", wantDecision: false},
		{name: "bluetooth idle", source: "Bluetooth", playback: "idle", wantDecision: false},
		{name: "physical playing", source: "Physical", playback: "playing", wantDecision: false},
		{name: "none playing", source: "None", playback: "playing", wantDecision: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldEnsureUSBForStreamingPlayback(tt.source, tt.playback)
			if got != tt.wantDecision {
				t.Fatalf("shouldEnsureUSBForStreamingPlayback(%q, %q) = %v, want %v", tt.source, tt.playback, got, tt.wantDecision)
			}
		})
	}
}

func TestIsStreamingStateFresh(t *testing.T) {
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		modAge    time.Duration
		updatedAt string
		wantFresh bool
	}{
		{name: "fresh modtime no updated_at", modAge: 5 * time.Second, updatedAt: "", wantFresh: true},
		{name: "stale modtime stale updated_at", modAge: 40 * time.Second, updatedAt: now.Add(-40 * time.Second).Format(time.RFC3339), wantFresh: false},
		{name: "stale modtime but fresh updated_at", modAge: 40 * time.Second, updatedAt: now.Add(-5 * time.Second).Format(time.RFC3339), wantFresh: true},
		{name: "stale modtime invalid updated_at", modAge: 40 * time.Second, updatedAt: "not-a-time", wantFresh: false},
		{name: "future updated_at", modAge: 40 * time.Second, updatedAt: now.Add(10 * time.Second).Format(time.RFC3339), wantFresh: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modTime := now.Add(-tt.modAge)
			got := isStreamingStateFresh(modTime, tt.updatedAt, now)
			if got != tt.wantFresh {
				t.Fatalf("isStreamingStateFresh(modAge=%s, updatedAt=%q) = %v, want %v", tt.modAge, tt.updatedAt, got, tt.wantFresh)
			}
		})
	}
}
