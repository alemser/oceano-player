package metadata

import (
	"strings"
	"testing"
)

func TestUpgradeArtworkURL(t *testing.T) {
	in := "https://is1-ssl.mzstatic.com/image/thumb/100x100bb.jpg"
	got := upgradeArtworkURL(in)
	want := "https://is1-ssl.mzstatic.com/image/thumb/600x600bb.jpg"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBestItunesSongArtworkURL_exactTitle(t *testing.T) {
	results := []itunesSongResult{
		{ArtistName: "Other", TrackName: "Noise", ArtworkUrl100: "https://x/100x100bb.jpg"},
		{ArtistName: "Main Artist", TrackName: "Hit Song", ArtworkUrl100: "https://y/100x100bb.jpg"},
	}
	got := BestItunesSongArtworkURL(results, "Main Artist", "Hit Song")
	if !strings.Contains(got, "600x600bb") {
		t.Fatalf("expected 600 artwork, got %q", got)
	}
}

func TestBestItunesSongArtworkURL_substringTitleFallback(t *testing.T) {
	results := []itunesSongResult{
		{ArtistName: "Dire Straits", TrackName: "Once Upon a Time in the West", ArtworkUrl100: "http://west/100x100bb.jpg"},
	}
	got := BestItunesSongArtworkURL(results, "Dire Straits", "Once Upon a Time")
	if !strings.Contains(got, "http://west/") {
		t.Fatalf("got %q", got)
	}
}

func TestBestItunesSongArtworkURL_fallsBackToFirst(t *testing.T) {
	results := []itunesSongResult{
		{ArtistName: "Someone Else", TrackName: "Unrelated", ArtworkUrl100: "http://fallback/100x100bb.jpg"},
	}
	got := BestItunesSongArtworkURL(results, "Dire Straits", "Private Investigations")
	if !strings.Contains(got, "http://fallback/") {
		t.Fatalf("got %q", got)
	}
}

func TestBestItunesSongArtworkURL_fallsBackToFirstWhenArtistMatches(t *testing.T) {
	results := []itunesSongResult{
		{ArtistName: "Someone", TrackName: "Whatever", ArtworkUrl100: "https://z/100x100bb.jpg"},
	}
	got := BestItunesSongArtworkURL(results, "Someone", "Whatever")
	if got == "" || !strings.Contains(got, "600x600bb") {
		t.Fatalf("got %q", got)
	}
}

func TestBestItunesSongArtworkURL_empty(t *testing.T) {
	if got := BestItunesSongArtworkURL(nil, "A", "B"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := BestItunesSongArtworkURL([]itunesSongResult{{}}, "", "T"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
