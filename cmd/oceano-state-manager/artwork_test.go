package main

import (
	"strings"
	"testing"
)

func TestBestItunesSongArtworkURL_ExactArtistAndTitle(t *testing.T) {
	results := []itunesSongResult{
		{ArtistName: "Wrong", TrackName: "Noise", ArtworkUrl100: "http://wrong/100x100bb.jpg"},
		{ArtistName: "Dire Straits", TrackName: "News", ArtworkUrl100: "http://good/100x100bb.jpg"},
	}
	got := bestItunesSongArtworkURL(results, "Dire Straits", "News")
	if !strings.Contains(got, "600x600bb") || !strings.Contains(got, "http://good/") {
		t.Fatalf("bestItunesSongArtworkURL = %q", got)
	}
}

func TestBestItunesSongArtworkURL_SubstringTitleFallback(t *testing.T) {
	results := []itunesSongResult{
		{ArtistName: "Dire Straits", TrackName: "Once Upon a Time in the West", ArtworkUrl100: "http://west/100x100bb.jpg"},
	}
	got := bestItunesSongArtworkURL(results, "Dire Straits", "Once Upon a Time")
	if !strings.Contains(got, "http://west/") {
		t.Fatalf("bestItunesSongArtworkURL = %q", got)
	}
}

func TestBestItunesSongArtworkURL_FirstResultFallback(t *testing.T) {
	results := []itunesSongResult{
		{ArtistName: "Someone Else", TrackName: "Unrelated", ArtworkUrl100: "http://fallback/100x100bb.jpg"},
	}
	got := bestItunesSongArtworkURL(results, "Dire Straits", "Private Investigations")
	if !strings.Contains(got, "http://fallback/") {
		t.Fatalf("bestItunesSongArtworkURL = %q", got)
	}
}

func TestBestItunesSongArtworkURL_Empty(t *testing.T) {
	if got := bestItunesSongArtworkURL(nil, "A", "B"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := bestItunesSongArtworkURL([]itunesSongResult{{}}, "", "T"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
