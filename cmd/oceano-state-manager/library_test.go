package main

import (
	"path/filepath"
	"testing"
	"time"
)

// openTestLibrary creates an in-memory (":memory:") SQLite library for tests.
func openTestLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { lib.Close() })
	return lib
}

// makeFingerprint builds a deterministic Fingerprint of length n whose values
// are all set to v. Used to construct fingerprints that are identical (BER=0)
// or completely different (BER=1.0) in a controlled way.
func makeFingerprint(v uint32, n int) Fingerprint {
	fp := make(Fingerprint, n)
	for i := range fp {
		fp[i] = v
	}
	return fp
}

// ── Open / migrate ────────────────────────────────────────────────────────────

func TestOpen_CreatesSchema(t *testing.T) {
	lib := openTestLibrary(t)
	// A simple query proves the collection table exists.
	var count int
	if err := lib.DB().QueryRow(`SELECT COUNT(*) FROM collection`).Scan(&count); err != nil {
		t.Fatalf("collection table missing after Open: %v", err)
	}
	if err := lib.DB().QueryRow(`SELECT COUNT(*) FROM fingerprints`).Scan(&count); err != nil {
		t.Fatalf("fingerprints table missing after Open: %v", err)
	}
}

func TestOpen_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lib.db")
	lib1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	lib1.Close()
	lib2, err := Open(path)
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
	if !entry.UserConfirmed {
		t.Error("user_confirmed should be true after ACRCloud recognition")
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

// ── HasFingerprints ───────────────────────────────────────────────────────────

func TestHasFingerprints_FalseWhenNone(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{ACRID: "acr-hfp-01", Title: "Track", Artist: "Artist"}
	id, _ := lib.RecordPlay(result, "")
	if lib.HasFingerprints(id) {
		t.Error("HasFingerprints should be false for a new entry with no fingerprints")
	}
}

func TestHasFingerprints_TrueAfterSave(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{ACRID: "acr-hfp-02", Title: "Track", Artist: "Artist"}
	id, _ := lib.RecordPlay(result, "")
	lib.SaveFingerprints(id, []Fingerprint{makeFingerprint(0x11223344, 50)}) //nolint:errcheck
	if !lib.HasFingerprints(id) {
		t.Error("HasFingerprints should be true after SaveFingerprints")
	}
}

func TestHasFingerprints_FirstPlayOnlySemantics(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{ACRID: "acr-hfp-03", Title: "Track", Artist: "Artist"}
	id, _ := lib.RecordPlay(result, "")
	fp := []Fingerprint{makeFingerprint(0xAABBCCDD, 50)}

	// First play: no fingerprints yet → should save.
	if lib.HasFingerprints(id) {
		t.Fatal("should have no fingerprints before first save")
	}
	lib.SaveFingerprints(id, fp) //nolint:errcheck

	// Second play: fingerprints exist → caller should skip SaveFingerprints.
	if !lib.HasFingerprints(id) {
		t.Error("should have fingerprints after first save")
	}

	// Verify count stays at len(fp) — caller is responsible for gating,
	// but confirm the DB state is correct.
	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM fingerprints WHERE entry_id=?`, id).Scan(&count)
	if count != len(fp) {
		t.Errorf("fingerprint count = %d, want %d", count, len(fp))
	}
}

// ── SaveFingerprints / FindByFingerprints ─────────────────────────────────────

func TestFindByFingerprints_NoMatch(t *testing.T) {
	lib := openTestLibrary(t)
	// Store fingerprints for one entry.
	result := &RecognitionResult{ACRID: "acr-fp-01", Title: "Track A", Artist: "Artist"}
	id, _ := lib.RecordPlay(result, "")
	stored := makeFingerprint(0xAAAAAAAA, 50)
	lib.SaveFingerprints(id, []Fingerprint{stored}) //nolint:errcheck

	// Query with a completely different fingerprint.
	query := []Fingerprint{makeFingerprint(0x55555555, 50)}
	entry, err := lib.FindByFingerprints(query, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints: %v", err)
	}
	if entry != nil {
		t.Errorf("expected no match for completely different fingerprint, got id=%d", entry.ID)
	}
}

func TestFindByFingerprints_MatchBelowThreshold(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{ACRID: "acr-fp-02", Title: "Track B", Artist: "Artist"}
	id, _ := lib.RecordPlay(result, "")
	// Store an identical fingerprint.
	fp := makeFingerprint(0xDEADBEEF, 50)
	lib.SaveFingerprints(id, []Fingerprint{fp}) //nolint:errcheck

	entry, err := lib.FindByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints: %v", err)
	}
	if entry == nil {
		t.Fatal("expected match for identical fingerprint")
	}
	if entry.ACRID != "acr-fp-02" {
		t.Errorf("matched wrong entry: %q", entry.ACRID)
	}
}

func TestFindByFingerprints_ReturnsClosestMatch(t *testing.T) {
	lib := openTestLibrary(t)

	// Entry A: identical to query (BER=0).
	rA := &RecognitionResult{ACRID: "acr-close", Title: "Close", Artist: "A"}
	idA, _ := lib.RecordPlay(rA, "")
	fpA := makeFingerprint(0x00000000, 50)
	lib.SaveFingerprints(idA, []Fingerprint{fpA}) //nolint:errcheck

	// Entry B: all bits differ (BER=1.0).
	rB := &RecognitionResult{ACRID: "acr-far", Title: "Far", Artist: "B"}
	idB, _ := lib.RecordPlay(rB, "")
	fpB := makeFingerprint(0xFFFFFFFF, 50)
	lib.SaveFingerprints(idB, []Fingerprint{fpB}) //nolint:errcheck

	query := []Fingerprint{makeFingerprint(0x00000000, 50)}
	entry, err := lib.FindByFingerprints(query, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints: %v", err)
	}
	if entry == nil {
		t.Fatal("expected a match")
	}
	if entry.ACRID != "acr-close" {
		t.Errorf("matched %q, want %q", entry.ACRID, "acr-close")
	}
}

func TestFindByFingerprints_EmptyLibrary(t *testing.T) {
	lib := openTestLibrary(t)
	query := []Fingerprint{makeFingerprint(0xDEADBEEF, 50)}
	entry, err := lib.FindByFingerprints(query, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints on empty library: %v", err)
	}
	if entry != nil {
		t.Errorf("empty library should return nil, got %+v", entry)
	}
}

func TestFindByFingerprints_EmptyQuery(t *testing.T) {
	lib := openTestLibrary(t)
	entry, err := lib.FindByFingerprints(nil, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints(nil): %v", err)
	}
	if entry != nil {
		t.Error("nil query should return nil")
	}
}

func TestFindByFingerprints_RequiresAllQueryWindowsToMatchSameEntry(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{ACRID: "acr-fp-03", Title: "Track C", Artist: "Artist"}
	id, _ := lib.RecordPlay(result, "")

	matching := makeFingerprint(0x12345678, 50)
	lib.SaveFingerprints(id, []Fingerprint{matching}) //nolint:errcheck

	// First query window matches perfectly, second is completely different.
	// Old logic used the global minimum BER and would incorrectly accept this.
	query := []Fingerprint{
		matching,
		makeFingerprint(0xFFFFFFFF, 50),
	}

	entry, err := lib.FindByFingerprints(query, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints: %v", err)
	}
	if entry != nil {
		t.Fatalf("expected no match when only one query window aligns, got id=%d", entry.ID)
	}
}

func TestFindByFingerprints_PrefersEntryMatchingAllWindows(t *testing.T) {
	lib := openTestLibrary(t)

	// Entry A matches both windows.
	rA := &RecognitionResult{ACRID: "acr-fp-all", Title: "All", Artist: "A"}
	idA, _ := lib.RecordPlay(rA, "")
	fpA1 := makeFingerprint(0x11111111, 50)
	fpA2 := makeFingerprint(0x22222222, 50)
	lib.SaveFingerprints(idA, []Fingerprint{fpA1, fpA2}) //nolint:errcheck

	// Entry B matches only the first window.
	rB := &RecognitionResult{ACRID: "acr-fp-partial", Title: "Partial", Artist: "B"}
	idB, _ := lib.RecordPlay(rB, "")
	lib.SaveFingerprints(idB, []Fingerprint{fpA1}) //nolint:errcheck

	query := []Fingerprint{fpA1, fpA2}
	entry, err := lib.FindByFingerprints(query, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints: %v", err)
	}
	if entry == nil {
		t.Fatal("expected a match")
	}
	if entry.ACRID != "acr-fp-all" {
		t.Fatalf("matched %q, want %q", entry.ACRID, "acr-fp-all")
	}
}

// ── UpsertStub ────────────────────────────────────────────────────────────────

func TestUpsertStub_CreatesStub(t *testing.T) {
	lib := openTestLibrary(t)
	fps := []Fingerprint{makeFingerprint(0x11223344, 50)}
	stub, err := lib.UpsertStub(fps, 0.35, 30)
	if err != nil {
		t.Fatalf("UpsertStub: %v", err)
	}
	if stub == nil || stub.ID == 0 {
		t.Fatal("expected stub with non-zero ID")
	}
	if stub.Title != "" || stub.Artist != "" {
		t.Errorf("stub should have empty title/artist, got %q/%q", stub.Title, stub.Artist)
	}
	if stub.PlayCount != 1 {
		t.Errorf("play_count = %d, want 1", stub.PlayCount)
	}
}

func TestUpsertStub_ReusesExistingStubByFingerprint(t *testing.T) {
	lib := openTestLibrary(t)
	fp := makeFingerprint(0xAABBCCDD, 50)
	fps := []Fingerprint{fp}

	stub1, _ := lib.UpsertStub(fps, 0.35, 30)
	stub2, _ := lib.UpsertStub(fps, 0.35, 30)

	if stub1.ID != stub2.ID {
		t.Errorf("second UpsertStub should reuse id=%d, got id=%d", stub1.ID, stub2.ID)
	}

	// Re-read to check play_count was incremented.
	var playCount int
	lib.DB().QueryRow(`SELECT play_count FROM collection WHERE id=?`, stub1.ID).Scan(&playCount)
	if playCount != 2 {
		t.Errorf("play_count = %d after two upserts, want 2", playCount)
	}
}

func TestUpsertStub_DifferentFingerprintCreatesNewStub(t *testing.T) {
	lib := openTestLibrary(t)
	fp1 := []Fingerprint{makeFingerprint(0x00000000, 50)}
	fp2 := []Fingerprint{makeFingerprint(0xFFFFFFFF, 50)}

	stub1, _ := lib.UpsertStub(fp1, 0.35, 30)
	stub2, _ := lib.UpsertStub(fp2, 0.35, 30)

	if stub1.ID == stub2.ID {
		t.Error("different fingerprints should create distinct stubs")
	}
}

func TestUpsertStub_NoFingerprintsErrors(t *testing.T) {
	lib := openTestLibrary(t)
	_, err := lib.UpsertStub(nil, 0.35, 30)
	if err == nil {
		t.Error("UpsertStub(nil) should return an error")
	}
}

// ── PruneStub ─────────────────────────────────────────────────────────────────

func TestPruneStub_RemovesStub(t *testing.T) {
	lib := openTestLibrary(t)
	fps := []Fingerprint{makeFingerprint(0xCAFEBABE, 50)}
	stub, _ := lib.UpsertStub(fps, 0.35, 30)

	if err := lib.PruneStub(stub.ID); err != nil {
		t.Fatalf("PruneStub: %v", err)
	}
	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub.ID).Scan(&count)
	if count != 0 {
		t.Error("stub should have been deleted")
	}
}

func TestPruneStub_DoesNotDeleteConfirmedEntry(t *testing.T) {
	lib := openTestLibrary(t)
	result := &RecognitionResult{ACRID: "acr-safe", Title: "Safe", Artist: "Artist"}
	id, _ := lib.RecordPlay(result, "")

	lib.PruneStub(id) //nolint:errcheck

	entry, _ := lib.Lookup("acr-safe")
	if entry == nil {
		t.Error("PruneStub should not delete user_confirmed=1 entries")
	}
}

// ── PruneRecentStubs ──────────────────────────────────────────────────────────

func TestPruneRecentStubs_DeletesStubsAfterBoundary(t *testing.T) {
	lib := openTestLibrary(t)

	boundary := time.Now().Add(-5 * time.Second)

	// Stub created after boundary — should be pruned.
	stub1, _ := lib.UpsertStub([]Fingerprint{makeFingerprint(0x11111111, 50)}, 0.35, 30)

	// Confirmed entry — must NOT be pruned.
	result := &RecognitionResult{ACRID: "acr-keep", Title: "Keep", Artist: "Artist"}
	keepID, _ := lib.RecordPlay(result, "")

	lib.PruneRecentStubs(boundary, keepID)

	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub1.ID).Scan(&count)
	if count != 0 {
		t.Errorf("stub created after boundary should be pruned, still found id=%d", stub1.ID)
	}

	// keepID must still exist.
	entry, _ := lib.Lookup("acr-keep")
	if entry == nil {
		t.Error("confirmed entry should not be pruned")
	}
}

func TestPruneRecentStubs_SparesBoundaryExcludeID(t *testing.T) {
	lib := openTestLibrary(t)

	boundary := time.Now().Add(-5 * time.Second)
	stub, _ := lib.UpsertStub([]Fingerprint{makeFingerprint(0x22222222, 50)}, 0.35, 30)

	// excludeID = stub.ID → should NOT be deleted even though it matches the time window.
	lib.PruneRecentStubs(boundary, stub.ID)

	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub.ID).Scan(&count)
	if count == 0 {
		t.Error("stub matching excludeID should be spared by PruneRecentStubs")
	}
}

func TestPruneRecentStubs_SparesStubsBeforeBoundary(t *testing.T) {
	lib := openTestLibrary(t)

	// Create stub, then set its first_played to before the boundary.
	stub, _ := lib.UpsertStub([]Fingerprint{makeFingerprint(0x33333333, 50)}, 0.35, 30)
	pastTime := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	lib.DB().Exec(`UPDATE collection SET first_played=? WHERE id=?`, pastTime, stub.ID)

	boundary := time.Now().Add(-1 * time.Minute) // 1 min ago — after pastTime above
	lib.PruneRecentStubs(boundary, 0)

	// Stub was created before boundary → spared.
	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub.ID).Scan(&count)
	if count == 0 {
		t.Error("stub created before boundary should not be pruned")
	}
}

// ── PruneMatchingStubs ────────────────────────────────────────────────────────

func TestPruneMatchingStubs_DeletesMatchingStub(t *testing.T) {
	lib := openTestLibrary(t)

	fp := makeFingerprint(0x00000001, 50)
	stub, _ := lib.UpsertStub([]Fingerprint{fp}, 0.35, 30)

	// Simulate recognition of the same track — different entry.
	result := &RecognitionResult{ACRID: "acr-match", Title: "Found", Artist: "Artist"}
	keepID, _ := lib.RecordPlay(result, "")

	lib.PruneMatchingStubs([]Fingerprint{fp}, 0.35, 30, keepID)

	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub.ID).Scan(&count)
	if count != 0 {
		t.Errorf("stub with matching fingerprint should be pruned, found id=%d", stub.ID)
	}
}

func TestPruneMatchingStubs_SparesNonMatchingStub(t *testing.T) {
	lib := openTestLibrary(t)

	stub, _ := lib.UpsertStub([]Fingerprint{makeFingerprint(0xFFFFFFFF, 50)}, 0.35, 30)

	result := &RecognitionResult{ACRID: "acr-other", Title: "Other", Artist: "Artist"}
	keepID, _ := lib.RecordPlay(result, "")

	// Query with completely different fingerprint → no match → stub spared.
	lib.PruneMatchingStubs([]Fingerprint{makeFingerprint(0x00000000, 50)}, 0.35, 30, keepID)

	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub.ID).Scan(&count)
	if count == 0 {
		t.Error("stub with non-matching fingerprint should NOT be pruned")
	}
}

func TestPruneMatchingStubs_SparesExcludeID(t *testing.T) {
	lib := openTestLibrary(t)
	fp := makeFingerprint(0x12345678, 50)
	stub, _ := lib.UpsertStub([]Fingerprint{fp}, 0.35, 30)

	// excludeID == stub.ID → spared even though fingerprint matches.
	lib.PruneMatchingStubs([]Fingerprint{fp}, 0.35, 30, stub.ID)

	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub.ID).Scan(&count)
	if count == 0 {
		t.Error("PruneMatchingStubs should spare the excludeID entry")
	}
}
