package library

import (
	"path/filepath"
	"testing"

	"github.com/alemser/oceano-player/internal/recognition"
)

func TestUpdateEnrichmentPatch_AdditiveNoOverwrite(t *testing.T) {
	lib, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()

	id, err := lib.RecordPlay(&recognition.Result{
		ACRID:  "acr-enrich-1",
		Title:  "Track",
		Artist: "Artist",
	}, "")
	if err != nil {
		t.Fatalf("RecordPlay: %v", err)
	}

	if err := lib.UpdateEnrichmentPatch(id, "https://api.discogs.com/releases/999", "Album A", "Label A", "1984", "discogs", "", ""); err != nil {
		t.Fatalf("UpdateEnrichmentPatch(first): %v", err)
	}
	entry, err := lib.GetByID(id)
	if err != nil || entry == nil {
		t.Fatalf("GetByID after first update: err=%v entry=%v", err, entry)
	}
	if entry.DiscogsURL != "https://api.discogs.com/releases/999" || entry.Album != "Album A" || entry.Label != "Label A" || entry.Released != "1984" {
		t.Fatalf("unexpected first update values: %+v", entry)
	}
	if entry.MetadataProvider != "discogs" {
		t.Fatalf("metadata_provider=%q want discogs", entry.MetadataProvider)
	}

	// Additive policy: second update must not overwrite existing non-empty fields.
	if err := lib.UpdateEnrichmentPatch(id, "https://api.discogs.com/releases/other", "Album B", "Label B", "2001", "itunes", "", ""); err != nil {
		t.Fatalf("UpdateEnrichmentPatch(second): %v", err)
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
	if entry.MetadataProvider != "discogs" {
		t.Fatalf("metadata_provider overwritten: %q", entry.MetadataProvider)
	}
}

func TestUpdateEnrichmentPatch_WritesProviderWhenEmpty(t *testing.T) {
	lib, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()

	id, err := lib.RecordPlay(&recognition.Result{
		ACRID:  "acr-enrich-2",
		Title:  "Song",
		Artist: "Band",
	}, "")
	if err != nil {
		t.Fatalf("RecordPlay: %v", err)
	}

	if err := lib.UpdateEnrichmentPatch(id, "", "Alb", "Lab", "1999", "itunes", "", ""); err != nil {
		t.Fatalf("UpdateEnrichmentPatch: %v", err)
	}
	entry, err := lib.GetByID(id)
	if err != nil || entry == nil {
		t.Fatalf("GetByID: err=%v entry=%v", err, entry)
	}
	if entry.MetadataProvider != "itunes" {
		t.Fatalf("metadata_provider=%q want itunes", entry.MetadataProvider)
	}
	if entry.DiscogsURL != "" {
		t.Fatalf("discogs_url should be empty, got %q", entry.DiscogsURL)
	}
}

func TestUpdateEnrichmentPatch_AdditiveArtworkPath(t *testing.T) {
	lib, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	defer lib.Close()

	id, err := lib.RecordPlay(&recognition.Result{
		ACRID:  "acr-art-1",
		Title:  "Song",
		Artist: "Band",
	}, "")
	if err != nil {
		t.Fatalf("RecordPlay: %v", err)
	}

	if err := lib.UpdateEnrichmentPatch(id, "", "", "", "", "", "/var/lib/oceano/artwork/cover.jpg", "discogs"); err != nil {
		t.Fatalf("UpdateEnrichmentPatch: %v", err)
	}
	entry, err := lib.GetByID(id)
	if err != nil || entry == nil {
		t.Fatalf("GetByID: err=%v entry=%v", err, entry)
	}
	if entry.ArtworkPath != "/var/lib/oceano/artwork/cover.jpg" {
		t.Fatalf("artwork_path=%q", entry.ArtworkPath)
	}
	if entry.ArtworkProvider != "discogs" {
		t.Fatalf("artwork_provider=%q want discogs", entry.ArtworkProvider)
	}

	if err := lib.UpdateEnrichmentPatch(id, "", "", "", "", "", "/other/never.jpg", "itunes"); err != nil {
		t.Fatalf("UpdateEnrichmentPatch second: %v", err)
	}
	entry, err = lib.GetByID(id)
	if err != nil || entry == nil {
		t.Fatalf("GetByID: err=%v entry=%v", err, entry)
	}
	if entry.ArtworkPath != "/var/lib/oceano/artwork/cover.jpg" {
		t.Fatalf("artwork_path overwritten: %q", entry.ArtworkPath)
	}
	if entry.ArtworkProvider != "discogs" {
		t.Fatalf("artwork_provider overwritten: %q", entry.ArtworkProvider)
	}
}
