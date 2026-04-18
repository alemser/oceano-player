package main

import "testing"

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
