package library

import (
	"path/filepath"
	"testing"

	"github.com/alemser/oceano-player/internal/recognition"
)

func TestUpdateDiscogsEnrichment_AdditiveNoOverwrite(t *testing.T) {
	lib, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()

	id, err := lib.RecordPlay(&recognition.Result{
		ACRID:  "acr-discogs-1",
		Title:  "Track",
		Artist: "Artist",
	}, "")
	if err != nil {
		t.Fatalf("RecordPlay: %v", err)
	}

	if err := lib.UpdateDiscogsEnrichment(id, "https://api.discogs.com/releases/999", "Album A", "Label A", "1984"); err != nil {
		t.Fatalf("UpdateDiscogsEnrichment(first): %v", err)
	}
	entry, err := lib.GetByID(id)
	if err != nil || entry == nil {
		t.Fatalf("GetByID after first update: err=%v entry=%v", err, entry)
	}
	if entry.DiscogsURL != "https://api.discogs.com/releases/999" || entry.Album != "Album A" || entry.Label != "Label A" || entry.Released != "1984" {
		t.Fatalf("unexpected first update values: %+v", entry)
	}

	// Additive policy: second update should not overwrite existing non-empty fields.
	if err := lib.UpdateDiscogsEnrichment(id, "https://api.discogs.com/releases/other", "Album B", "Label B", "2001"); err != nil {
		t.Fatalf("UpdateDiscogsEnrichment(second): %v", err)
	}
	entry, err = lib.GetByID(id)
	if err != nil || entry == nil {
		t.Fatalf("GetByID after second update: err=%v entry=%v", err, entry)
	}
	if entry.DiscogsURL != "https://api.discogs.com/releases/999" {
		t.Fatalf("discogs_url overwritten: %q", entry.DiscogsURL)
	}
	if entry.Album != "Album A" || entry.Label != "Label A" || entry.Released != "1984" {
		t.Fatalf("metadata overwritten unexpectedly: album=%q label=%q released=%q", entry.Album, entry.Label, entry.Released)
	}
}

