package main

import (
	"path/filepath"
	"testing"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

// openTestLibrary creates an in-memory (":memory:") SQLite library for tests.
func openTestLibrary(t *testing.T) *internallibrary.Library {
	t.Helper()
	lib, err := internallibrary.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { lib.Close() })
	return lib
}

// ── Open / migrate ────────────────────────────────────────────────────────────

func TestOpen_CreatesSchema(t *testing.T) {
	lib := openTestLibrary(t)
	// A simple query proves the collection table exists.
	var count int
	if err := lib.DB().QueryRow(`SELECT COUNT(*) FROM collection`).Scan(&count); err != nil {
		t.Fatalf("collection table missing after Open: %v", err)
	}
}

func TestOpen_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lib.db")
	lib1, err := internallibrary.Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	lib1.Close()
	lib2, err := internallibrary.Open(path)
	if err != nil {
		t.Fatalf("second Open (re-open existing db): %v", err)
	}
	lib2.Close()
}

// ── RecordPlay ────────────────────────────────────────────────────────────────

func TestRecordPlay_InsertsNewTrack(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{
		ACRID:  "acr-001",
		Title:  "So What",
		Artist: "Miles Davis",
		Album:  "Kind of Blue",
		Score:  100,
	}
	id, err := lib.RecordPlay(result, "")
	if err != nil {
		t.Fatalf("RecordPlay: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero entry ID")
	}

	entry, err := lib.Lookup("acr-001")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if entry == nil {
		t.Fatal("entry not found after RecordPlay")
	}
	if entry.Title != "So What" {
		t.Errorf("title = %q, want %q", entry.Title, "So What")
	}
	if entry.PlayCount != 1 {
		t.Errorf("play_count = %d, want 1", entry.PlayCount)
	}
	if entry.UserConfirmed {
		t.Error("user_confirmed should be false for new tracks (allow manual association)")
	}
}

func TestRecordPlay_IncrementsPlayCount(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{ACRID: "acr-002", Title: "Blue in Green", Artist: "Miles Davis"}
	lib.RecordPlay(result, "") //nolint:errcheck
	lib.RecordPlay(result, "") //nolint:errcheck

	entry, _ := lib.Lookup("acr-002")
	if entry.PlayCount != 2 {
		t.Errorf("play_count = %d, want 2", entry.PlayCount)
	}
}

func TestRecordPlay_PreservesHigherScore(t *testing.T) {
	lib := openTestLibrary(t)
	r1 := &RecognitionResult{ACRID: "acr-003", Title: "Original", Artist: "Artist A", Score: 80}
	r2 := &RecognitionResult{ACRID: "acr-003", Title: "Better Match", Artist: "Artist A", Score: 95}

	lib.RecordPlay(r1, "") //nolint:errcheck
	lib.RecordPlay(r2, "") //nolint:errcheck

	entry, _ := lib.Lookup("acr-003")
	if entry.Title != "Better Match" {
		t.Errorf("title = %q, want %q (higher score should win)", entry.Title, "Better Match")
	}
	if entry.Score != 95 {
		t.Errorf("score = %d, want 95", entry.Score)
	}
}

func TestRecordPlay_DoesNotOverwriteArtwork(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{ACRID: "acr-004", Title: "Track", Artist: "Artist"}
	lib.RecordPlay(result, "/art/cover.jpg") //nolint:errcheck
	// Second play with no artwork — existing path should be preserved.
	lib.RecordPlay(result, "") //nolint:errcheck

	entry, _ := lib.Lookup("acr-004")
	if entry.ArtworkPath != "/art/cover.jpg" {
		t.Errorf("artwork_path = %q, want %q", entry.ArtworkPath, "/art/cover.jpg")
	}
}

func TestRecordPlay_MergesEquivalentShazamConfirmedTrackIntoExistingRow(t *testing.T) {
	lib := openTestLibrary(t)

	first := &RecognitionResult{
		ACRID:  "acr-old-001",
		Title:  "Merry Go Round",
		Artist: "The Replacements",
		Score:  80,
	}
	firstID, err := lib.RecordPlay(first, "")
	if err != nil {
		t.Fatalf("RecordPlay(first): %v", err)
	}

	second := &RecognitionResult{
		ACRID:    "acr-new-999",
		ShazamID: "shz-20058157",
		Title:    "Merry Go Round (2008 Remaster)",
		Artist:   "The Replacements",
		Score:    25,
	}
	secondID, err := lib.RecordPlay(second, "")
	if err != nil {
		t.Fatalf("RecordPlay(second): %v", err)
	}

	if secondID != firstID {
		t.Fatalf("expected equivalent confirmed track to reuse row id=%d, got id=%d", firstID, secondID)
	}

	entryByOldACR, err := lib.Lookup("acr-old-001")
	if err != nil {
		t.Fatalf("Lookup(old acr): %v", err)
	}
	if entryByOldACR == nil {
		t.Fatal("entry not found by old ACRID")
	}
	if entryByOldACR.PlayCount != 2 {
		t.Fatalf("play_count = %d, want 2", entryByOldACR.PlayCount)
	}
	if entryByOldACR.ShazamID != "shz-20058157" {
		t.Fatalf("shazam_id = %q, want %q", entryByOldACR.ShazamID, "shz-20058157")
	}

	entryByShazam, err := lib.LookupByShazamID("shz-20058157")
	if err != nil {
		t.Fatalf("LookupByShazamID: %v", err)
	}
	if entryByShazam == nil {
		t.Fatal("entry not found by shazam_id")
	}
	if entryByShazam.ID != firstID {
		t.Fatalf("LookupByShazamID id = %d, want %d", entryByShazam.ID, firstID)
	}

	var total int
	if err := lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE lower(artist)=lower(?) AND lower(title) LIKE lower(?)`, "The Replacements", "%merry%go%round%").Scan(&total); err != nil {
		t.Fatalf("count equivalent rows: %v", err)
	}
	if total != 1 {
		t.Fatalf("equivalent track rows = %d, want 1", total)
	}
}

func TestRecordPlay_MergesEquivalentHighConfidenceACRTrackWithoutShazam(t *testing.T) {
	lib := openTestLibrary(t)

	first := &RecognitionResult{
		ACRID:  "acr-first-111",
		Title:  "Merry Go Round",
		Artist: "The Replacements",
		Score:  85,
	}
	firstID, err := lib.RecordPlay(first, "")
	if err != nil {
		t.Fatalf("RecordPlay(first): %v", err)
	}

	second := &RecognitionResult{
		ACRID:  "acr-second-222",
		Title:  "Merry Go Round (2008 Remaster)",
		Artist: "The Replacements",
		Score:  100,
	}
	secondID, err := lib.RecordPlay(second, "")
	if err != nil {
		t.Fatalf("RecordPlay(second): %v", err)
	}

	if secondID != firstID {
		t.Fatalf("expected equivalent high-confidence track to reuse row id=%d, got id=%d", firstID, secondID)
	}

	entryByFirst, err := lib.Lookup("acr-first-111")
	if err != nil {
		t.Fatalf("Lookup(first acr): %v", err)
	}
	if entryByFirst == nil {
		t.Fatal("entry not found by first ACRID")
	}
	if entryByFirst.PlayCount != 2 {
		t.Fatalf("play_count = %d, want 2", entryByFirst.PlayCount)
	}

	entryBySecond, err := lib.Lookup("acr-second-222")
	if err != nil {
		t.Fatalf("Lookup(second acr): %v", err)
	}
	if entryBySecond != nil {
		t.Fatalf("unexpected duplicate row by second ACRID: %+v", entryBySecond)
	}

	var total int
	if err := lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE lower(artist)=lower(?) AND lower(title) LIKE lower(?)`, "The Replacements", "%merry%go%round%").Scan(&total); err != nil {
		t.Fatalf("count equivalent rows: %v", err)
	}
	if total != 1 {
		t.Fatalf("equivalent track rows = %d, want 1", total)
	}
}

func TestRecordPlay_MergesEquivalentLowScoreACRTrackWithoutShazam(t *testing.T) {
	lib := openTestLibrary(t)

	first := &RecognitionResult{
		ACRID:  "acr-first-low-111",
		Title:  "Merry Go Round",
		Artist: "The Replacements",
		Score:  85,
	}
	firstID, err := lib.RecordPlay(first, "")
	if err != nil {
		t.Fatalf("RecordPlay(first): %v", err)
	}

	second := &RecognitionResult{
		ACRID:  "acr-second-low-222",
		Title:  "Merry Go Round (2008 Remaster)",
		Artist: "The Replacements",
		Score:  79,
	}
	secondID, err := lib.RecordPlay(second, "")
	if err != nil {
		t.Fatalf("RecordPlay(second): %v", err)
	}

	if secondID != firstID {
		t.Fatalf("expected equivalent low-score track to reuse row id=%d, got id=%d", firstID, secondID)
	}

	entryByFirst, err := lib.Lookup("acr-first-low-111")
	if err != nil {
		t.Fatalf("Lookup(first acr): %v", err)
	}
	if entryByFirst == nil {
		t.Fatal("entry not found by first ACRID")
	}
	if entryByFirst.PlayCount != 2 {
		t.Fatalf("play_count = %d, want 2", entryByFirst.PlayCount)
	}

	entryBySecond, err := lib.Lookup("acr-second-low-222")
	if err != nil {
		t.Fatalf("Lookup(second acr): %v", err)
	}
	if entryBySecond != nil {
		t.Fatalf("unexpected duplicate row by second ACRID: %+v", entryBySecond)
	}
}

// ── Lookup ────────────────────────────────────────────────────────────────────

func TestLookup_NotFound(t *testing.T) {
	lib := openTestLibrary(t)
	entry, err := lib.Lookup("does-not-exist")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil for unknown acrid, got %+v", entry)
	}
}

func TestLookup_EmptyACRID(t *testing.T) {
	lib := openTestLibrary(t)
	entry, err := lib.Lookup("")
	if err != nil {
		t.Fatalf("Lookup('') should not error: %v", err)
	}
	if entry != nil {
		t.Error("Lookup('') should return nil, not a row")
	}
}

func TestLookupByIDs_FallsBackToShazamID(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{
		ShazamID: "shz-001",
		Title:    "Exodus",
		Artist:   "Bob Marley",
		Album:    "Exodus",
		Score:    90,
	}
	if _, err := lib.RecordPlay(result, ""); err != nil {
		t.Fatalf("RecordPlay(shazam): %v", err)
	}

	entry, err := lib.LookupByIDs("", "shz-001")
	if err != nil {
		t.Fatalf("LookupByIDs: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry by shazam ID fallback")
	}
	if entry.ShazamID != "shz-001" {
		t.Fatalf("shazam_id = %q, want %q", entry.ShazamID, "shz-001")
	}
}

func TestLookupByIDs_PrefersEntryWithDurationWhenACRHasNone(t *testing.T) {
	lib := openTestLibrary(t)
	now := time.Now().UTC().Format(time.RFC3339)

	// Simulate a historical split where ACRID and Shazam IDs point to different
	// rows for the same musical work. ACR row has no duration; Shazam row does.
	if _, err := lib.DB().Exec(`
		INSERT INTO collection
			(acrid, title, artist, score, play_count, first_played, last_played, user_confirmed, duration_ms)
		VALUES (?, ?, ?, ?, 1, ?, ?, 0, ?)
	`, "acr-split-001", "Telegraph Road", "Dire Straits", 95, now, now, 0); err != nil {
		t.Fatalf("insert acr row: %v", err)
	}

	if _, err := lib.DB().Exec(`
		INSERT INTO collection
			(shazam_id, title, artist, score, play_count, first_played, last_played, user_confirmed, duration_ms)
		VALUES (?, ?, ?, ?, 1, ?, ?, 1, ?)
	`, "shz-split-001", "Telegraph Road", "Dire Straits", 80, now, now, 854000); err != nil {
		t.Fatalf("insert shazam row: %v", err)
	}

	entry, err := lib.LookupByIDs("acr-split-001", "shz-split-001")
	if err != nil {
		t.Fatalf("LookupByIDs: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry from LookupByIDs")
	}
	if entry.DurationMs != 854000 {
		t.Fatalf("DurationMs = %d, want %d", entry.DurationMs, 854000)
	}
	if entry.ShazamID != "shz-split-001" {
		t.Fatalf("expected selected row to be shazam-backed row, got shazam_id=%q", entry.ShazamID)
	}
}

func TestRecordPlay_ACRIDUpsertPersistsShazamID(t *testing.T) {
	lib := openTestLibrary(t)

	first := &RecognitionResult{
		ACRID:  "acr-merge-001",
		Title:  "Track",
		Artist: "Artist",
		Score:  80,
	}
	if _, err := lib.RecordPlay(first, ""); err != nil {
		t.Fatalf("RecordPlay(first): %v", err)
	}

	second := &RecognitionResult{
		ACRID:    "acr-merge-001",
		ShazamID: "shz-merge-001",
		Title:    "Track",
		Artist:   "Artist",
		Score:    90,
	}
	if _, err := lib.RecordPlay(second, ""); err != nil {
		t.Fatalf("RecordPlay(second): %v", err)
	}

	entry, err := lib.LookupByIDs("", "shz-merge-001")
	if err != nil {
		t.Fatalf("LookupByIDs(shazam): %v", err)
	}
	if entry == nil {
		t.Fatal("expected lookup by shazam ID after ACRID upsert")
	}
	if entry.ACRID != "acr-merge-001" {
		t.Fatalf("acrid = %q, want %q", entry.ACRID, "acr-merge-001")
	}
}

// ── DurationMs persistence ────────────────────────────────────────────────────

// TestRecordPlay_PersistsDurationMs proves that the duration returned by a
// recognition provider survives the RecordPlay → GetByID round-trip for all
// three insert paths (ACRID, ShazamID-only, title+artist fallback).
func TestRecordPlay_PersistsDurationMs(t *testing.T) {
	tests := []struct {
		name   string
		result *RecognitionResult
	}{
		{
			name: "ACRID path",
			result: &RecognitionResult{
				ACRID: "acr-dur-001", Title: "So What", Artist: "Miles Davis",
				Score: 90, DurationMs: 561000,
			},
		},
		{
			name: "ShazamID path",
			result: &RecognitionResult{
				ShazamID: "shz-dur-001", Title: "Exodus", Artist: "Bob Marley",
				Score: 85, DurationMs: 244000,
			},
		},
		{
			name: "title+artist fallback path",
			result: &RecognitionResult{
				Title: "Unique Track XYZ", Artist: "Unique Artist XYZ",
				Score: 70, DurationMs: 185000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lib := openTestLibrary(t)
			id, err := lib.RecordPlay(tt.result, "")
			if err != nil {
				t.Fatalf("RecordPlay: %v", err)
			}
			entry, err := lib.GetByID(id)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if entry == nil {
				t.Fatal("entry not found after RecordPlay")
			}
			if entry.DurationMs != tt.result.DurationMs {
				t.Errorf("DurationMs = %d, want %d", entry.DurationMs, tt.result.DurationMs)
			}
		})
	}
}

// TestRecordPlay_DurationMs_UpdatedOnConflict proves that when the same track
// is played again with a non-zero DurationMs, the stored value is updated.
// And when the re-play carries DurationMs=0 (provider did not return a duration),
// the previously stored value is preserved.
func TestRecordPlay_DurationMs_UpdatedOnConflict(t *testing.T) {
	lib := openTestLibrary(t)

	// First play: no duration yet.
	r1 := &RecognitionResult{ACRID: "acr-dur-upd", Title: "Track", Artist: "Artist", Score: 80}
	id, _ := lib.RecordPlay(r1, "")

	entry, _ := lib.GetByID(id)
	if entry.DurationMs != 0 {
		t.Fatalf("DurationMs should be 0 on first play without duration, got %d", entry.DurationMs)
	}

	// Second play: provider now returns a duration.
	r2 := &RecognitionResult{ACRID: "acr-dur-upd", Title: "Track", Artist: "Artist", Score: 90, DurationMs: 210000}
	lib.RecordPlay(r2, "") //nolint:errcheck

	entry, _ = lib.GetByID(id)
	if entry.DurationMs != 210000 {
		t.Errorf("DurationMs = %d, want 210000 after update", entry.DurationMs)
	}

	// Third play: provider returns no duration — existing value must be preserved.
	r3 := &RecognitionResult{ACRID: "acr-dur-upd", Title: "Track", Artist: "Artist", Score: 95, DurationMs: 0}
	lib.RecordPlay(r3, "") //nolint:errcheck

	entry, _ = lib.GetByID(id)
	if entry.DurationMs != 210000 {
		t.Errorf("DurationMs = %d, want 210000 preserved when re-play carries 0", entry.DurationMs)
	}
}
