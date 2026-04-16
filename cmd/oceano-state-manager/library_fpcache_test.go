package main

// ── Fingerprint cache correctness tests ──────────────────────────────────────
//
// These tests prove that the in-memory fingerprint cache in Library is
// consistent with the underlying database across all mutation paths:
//
//   - buildFPCache at Open loads existing rows
//   - confirmedOnly filter respects user_confirmed / title / artist
//   - rebuildFPCache is called after SaveFingerprints, PruneStub,
//     PromoteStubFingerprints, PruneMatchingStubs, PruneRecentStubs
//   - RebuildFPCache makes direct-SQL user_confirmed changes visible

import (
	"path/filepath"
	"testing"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

// reopenLib closes lib and reopens the same on-disk database file.
// Simulates a service restart; exercises buildFPCache at Open.
func reopenLib(t *testing.T, lib *internallibrary.Library, path string) *internallibrary.Library {
	t.Helper()
	if err := lib.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lib2, err := internallibrary.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { lib2.Close() })
	return lib2
}

// TestFPCache_LoadedAtOpen proves that buildFPCache runs at Open and makes
// previously saved fingerprints visible without any subsequent SaveFingerprints
// call.  If buildFPCache were missing from Open, FindByFingerprints would scan
// an empty cache and return nil.
func TestFPCache_LoadedAtOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	lib, err := internallibrary.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	result := &RecognitionResult{ACRID: "acr-reopen", Title: "Reopen Track", Artist: "Artist"}
	id, err := lib.RecordPlay(result, "")
	if err != nil {
		t.Fatalf("RecordPlay: %v", err)
	}
	fp := makeFingerprint(0xABCD1234, 50)
	if err := lib.SaveFingerprints(id, []Fingerprint{fp}); err != nil {
		t.Fatalf("SaveFingerprints: %v", err)
	}

	// Reopen — new Library instance must rebuild cache from DB.
	lib2 := reopenLib(t, lib, dbPath)

	entry, err := lib2.FindByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints after reopen: %v", err)
	}
	if entry == nil {
		t.Fatal("cache not loaded at Open: FindByFingerprints returned nil after reopen")
	}
	if entry.ACRID != "acr-reopen" {
		t.Errorf("expected acr-reopen, got %q", entry.ACRID)
	}
}

// TestFindConfirmedByFingerprints_ExcludesUnconfirmed proves that the
// confirmedOnly filter in the cache scan rejects entries where user_confirmed=0,
// even when their fingerprints are an exact match.
func TestFindConfirmedByFingerprints_ExcludesUnconfirmed(t *testing.T) {
	lib := openTestLibrary(t)

	// RecordPlay creates entries with user_confirmed=0.
	result := &RecognitionResult{ACRID: "acr-unconf", Title: "Unconfirmed", Artist: "Artist"}
	id, _ := lib.RecordPlay(result, "")
	fp := makeFingerprint(0x11223344, 50)
	lib.SaveFingerprints(id, []Fingerprint{fp}) //nolint:errcheck

	entry, err := lib.FindConfirmedByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindConfirmedByFingerprints: %v", err)
	}
	if entry != nil {
		t.Errorf("unconfirmed entry must not be returned by FindConfirmedByFingerprints, got id=%d", entry.ID)
	}

	// Full (unfiltered) scan should still find it.
	entry2, err := lib.FindByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints: %v", err)
	}
	if entry2 == nil {
		t.Fatal("FindByFingerprints should find the unconfirmed entry")
	}
}

// TestFindConfirmedByFingerprints_ReturnsAfterRebuild proves that after a
// direct-SQL user_confirmed update (bypassing the library API), calling
// RebuildFPCache makes the entry visible to FindConfirmedByFingerprints.
// This mirrors what the web UI does when a user confirms a stub.
func TestFindConfirmedByFingerprints_ReturnsAfterRebuild(t *testing.T) {
	lib := openTestLibrary(t)

	fp := makeFingerprint(0xDEAD1234, 50)
	stub, err := lib.UpsertStub([]Fingerprint{fp}, 0.35, 30)
	if err != nil || stub == nil {
		t.Fatalf("UpsertStub: err=%v stub=%v", err, stub)
	}

	// Confirm it via direct SQL, as the web UI would.
	if _, err := lib.DB().Exec(
		`UPDATE collection SET title='Confirmed', artist='Artist', user_confirmed=1 WHERE id=?`,
		stub.ID,
	); err != nil {
		t.Fatalf("direct confirm: %v", err)
	}

	// Without cache rebuild, FindConfirmedByFingerprints still sees confirmed=false.
	before, err := lib.FindConfirmedByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindConfirmedByFingerprints (before rebuild): %v", err)
	}
	if before != nil {
		t.Errorf("cache should be stale before rebuild: expected nil, got id=%d", before.ID)
	}

	lib.RebuildFPCache()

	after, err := lib.FindConfirmedByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindConfirmedByFingerprints (after rebuild): %v", err)
	}
	if after == nil {
		t.Fatal("FindConfirmedByFingerprints returned nil after RebuildFPCache")
	}
	if after.Title != "Confirmed" {
		t.Errorf("unexpected title after rebuild: %q", after.Title)
	}
}

// TestFPCache_InvalidatedAfterPruneStub proves that after PruneStub removes a
// stub's collection row (and cascades to fingerprints), FindByFingerprints no
// longer returns the deleted entry.
func TestFPCache_InvalidatedAfterPruneStub(t *testing.T) {
	lib := openTestLibrary(t)

	fp := makeFingerprint(0xCAFEBABE, 50)
	stub, _ := lib.UpsertStub([]Fingerprint{fp}, 0.35, 30)

	// Confirm it's in the cache before pruning.
	before, _ := lib.FindByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if before == nil {
		t.Fatal("fingerprint should be found before PruneStub")
	}

	if err := lib.PruneStub(stub.ID); err != nil {
		t.Fatalf("PruneStub: %v", err)
	}

	after, err := lib.FindByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints after PruneStub: %v", err)
	}
	if after != nil {
		t.Errorf("cache not invalidated after PruneStub: still found id=%d", after.ID)
	}
}

// TestFPCache_InvalidatedAfterPromoteStubFingerprints proves that after moving
// fingerprints from a stub to a confirmed entry, FindByFingerprints finds them
// under the new entry ID, not the deleted stub.
func TestFPCache_InvalidatedAfterPromoteStubFingerprints(t *testing.T) {
	lib := openTestLibrary(t)

	fp := makeFingerprint(0x77777777, 50)
	stub, _ := lib.UpsertStub([]Fingerprint{fp}, 0.35, 30)

	confirmed := &RecognitionResult{ACRID: "acr-prom", Title: "Promoted", Artist: "Artist"}
	confirmedID, _ := lib.RecordPlay(confirmed, "")

	if err := lib.PromoteStubFingerprints(stub.ID, confirmedID); err != nil {
		t.Fatalf("PromoteStubFingerprints: %v", err)
	}

	entry, err := lib.FindByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints after Promote: %v", err)
	}
	if entry == nil {
		t.Fatal("fingerprints not found under new entry after PromoteStubFingerprints")
	}
	if entry.ACRID != "acr-prom" {
		t.Errorf("expected promoted entry acr-prom, got %q", entry.ACRID)
	}

	// Old stub should no longer be returned.
	if entry.ID == stub.ID {
		t.Errorf("fingerprints still attributed to deleted stub id=%d", stub.ID)
	}
}

// TestFPCache_InvalidatedAfterPruneMatchingStubs proves that stubs deleted by
// PruneMatchingStubs are removed from the cache.
func TestFPCache_InvalidatedAfterPruneMatchingStubs(t *testing.T) {
	lib := openTestLibrary(t)

	fp := makeFingerprint(0x00FF00FF, 50)
	stub, _ := lib.UpsertStub([]Fingerprint{fp}, 0.35, 30)

	confirmed := &RecognitionResult{ACRID: "acr-prune-match", Title: "Found", Artist: "A"}
	keepID, _ := lib.RecordPlay(confirmed, "")

	lib.PruneMatchingStubs([]Fingerprint{fp}, 0.35, 30, keepID)

	// Stub row should be gone.
	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub.ID).Scan(&count)
	if count != 0 {
		t.Fatal("PruneMatchingStubs did not delete the stub from the DB")
	}

	// Cache should no longer contain the stub's fingerprint.
	entry, err := lib.FindByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints after PruneMatchingStubs: %v", err)
	}
	if entry != nil && entry.ID == stub.ID {
		t.Errorf("cache not invalidated after PruneMatchingStubs: stub id=%d still matched", stub.ID)
	}
}

// TestFPCache_InvalidatedAfterPruneRecentStubs proves that stubs deleted by
// PruneRecentStubs are removed from the cache.
func TestFPCache_InvalidatedAfterPruneRecentStubs(t *testing.T) {
	lib := openTestLibrary(t)

	boundary := time.Now().Add(-5 * time.Second)
	fp := makeFingerprint(0xFF00FF00, 50)
	stub, _ := lib.UpsertStub([]Fingerprint{fp}, 0.35, 30)

	lib.PruneRecentStubs(boundary, 0)

	var count int
	lib.DB().QueryRow(`SELECT COUNT(*) FROM collection WHERE id=?`, stub.ID).Scan(&count)
	if count != 0 {
		t.Fatal("PruneRecentStubs did not delete the stub from the DB")
	}

	entry, err := lib.FindByFingerprints([]Fingerprint{fp}, 0.35, 30)
	if err != nil {
		t.Fatalf("FindByFingerprints after PruneRecentStubs: %v", err)
	}
	if entry != nil {
		t.Errorf("cache not invalidated after PruneRecentStubs: still matched id=%d", entry.ID)
	}
}
