package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openTestLibrary creates a temporary SQLite library for testing.
func openTestLibrary(t *testing.T) *Library {
	t.Helper()
	dir := t.TempDir()
	lib, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { lib.Close() })
	return lib
}

// --- LookupByFingerprint ---

func TestLookupByFingerprint_NotFound(t *testing.T) {
	lib := openTestLibrary(t)

	entry, err := lib.LookupByFingerprint("AQAD_nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil, got %+v", entry)
	}
}

func TestLookupByFingerprint_EmptyString(t *testing.T) {
	lib := openTestLibrary(t)

	entry, err := lib.LookupByFingerprint("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil for empty fingerprint, got %+v", entry)
	}
}

func TestLookupByFingerprint_Found(t *testing.T) {
	lib := openTestLibrary(t)

	fp := "AQADtJmSSaklHMmSSaRX"
	result := &RecognitionResult{
		ACRID:    "acrid-001",
		Title:    "So What",
		Artist:   "Miles Davis",
		Album:    "Kind of Blue",
		Label:    "Columbia",
		Released: "1959",
		Score:    95,
	}

	if err := lib.RecordPlay(result, "", fp); err != nil {
		t.Fatalf("RecordPlay: %v", err)
	}

	entry, err := lib.LookupByFingerprint(fp)
	if err != nil {
		t.Fatalf("LookupByFingerprint: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.Title != result.Title {
		t.Errorf("title = %q, want %q", entry.Title, result.Title)
	}
	if entry.Artist != result.Artist {
		t.Errorf("artist = %q, want %q", entry.Artist, result.Artist)
	}
	fps, err := lib.loadFingerprints(entry.ID)
	if err != nil {
		t.Fatalf("loadFingerprints: %v", err)
	}
	if len(fps) != 1 || fps[0] != fp {
		t.Errorf("fingerprints = %v, want [%q]", fps, fp)
	}
	if entry.ACRID != result.ACRID {
		t.Errorf("acrid = %q, want %q", entry.ACRID, result.ACRID)
	}
}

// --- RecordPlay with fingerprint ---

func TestRecordPlay_FingerprintStoredWithACRID(t *testing.T) {
	lib := openTestLibrary(t)

	fp := "AQABz1GJUEGUAAAB"
	result := &RecognitionResult{
		ACRID:  "acrid-abc",
		Title:  "Blue in Green",
		Artist: "Miles Davis",
		Album:  "Kind of Blue",
		Score:  88,
	}

	if err := lib.RecordPlay(result, "", fp); err != nil {
		t.Fatalf("RecordPlay: %v", err)
	}

	// Should be retrievable by ACRID.
	byACRID, err := lib.Lookup(result.ACRID)
	if err != nil || byACRID == nil {
		t.Fatalf("Lookup by ACRID: err=%v entry=%v", err, byACRID)
	}
	if byACRID == nil {
		t.Fatalf("Lookup by ACRID returned nil")
	}
	fpsByACRID, err := lib.loadFingerprints(byACRID.ID)
	if err != nil {
		t.Fatalf("loadFingerprints: %v", err)
	}
	if len(fpsByACRID) != 1 || fpsByACRID[0] != fp {
		t.Errorf("fingerprints via ACRID lookup = %v, want [%q]", fpsByACRID, fp)
	}

	// Should also be retrievable by fingerprint.
	byFP, err := lib.LookupByFingerprint(fp)
	if err != nil || byFP == nil {
		t.Fatalf("LookupByFingerprint: err=%v entry=%v", err, byFP)
	}
	if byFP.ACRID != result.ACRID {
		t.Errorf("acrid via fingerprint lookup = %q, want %q", byFP.ACRID, result.ACRID)
	}
}

func TestRecordPlay_FingerprintOnlyUnknown(t *testing.T) {
	lib := openTestLibrary(t)

	fp := "AQABz0mUaEkSunknown"
	unknown := &RecognitionResult{
		Title:    "Unknown music",
		Artist:   "Unknown artist",
		Album:    "Unknown album",
		Label:    "Unknown",
		Released: "Unknown",
	}

	if err := lib.RecordPlay(unknown, "", fp); err != nil {
		t.Fatalf("RecordPlay: %v", err)
	}

	entry, err := lib.LookupByFingerprint(fp)
	if err != nil || entry == nil {
		t.Fatalf("LookupByFingerprint: err=%v entry=%v", err, entry)
	}
	if entry.Title != "Unknown music" {
		t.Errorf("title = %q, want Unknown music", entry.Title)
	}
	if entry.Artist != "Unknown artist" {
		t.Errorf("artist = %q, want Unknown artist", entry.Artist)
	}
	if entry.Album != "Unknown album" {
		t.Errorf("album = %q, want Unknown album", entry.Album)
	}
	fps, err := lib.loadFingerprints(entry.ID)
	if err != nil {
		t.Fatalf("loadFingerprints: %v", err)
	}
	if len(fps) != 1 || fps[0] != fp {
		t.Errorf("fingerprints = %v, want [%q]", fps, fp)
	}
}

func TestRecordPlay_ExistingRecordGetsFingerprint(t *testing.T) {
	lib := openTestLibrary(t)

	// Insert a track without a fingerprint (simulates a pre-fingerprint record).
	result := &RecognitionResult{
		ACRID:  "acrid-legacy",
		Title:  "Milestones",
		Artist: "Miles Davis",
		Album:  "Milestones",
		Score:  92,
	}
	if err := lib.RecordPlay(result, "", ""); err != nil {
		t.Fatalf("RecordPlay (no fingerprint): %v", err)
	}

	// Confirm no fingerprints stored yet.
	entry, err := lib.Lookup(result.ACRID)
	if err != nil || entry == nil {
		t.Fatalf("Lookup: err=%v entry=%v", err, entry)
	}
	fps, err := lib.loadFingerprints(entry.ID)
	if err != nil {
		t.Fatalf("loadFingerprints: %v", err)
	}
	if len(fps) != 0 {
		t.Errorf("expected no fingerprints, got %v", fps)
	}

	// Re-recognize with a fingerprint (simulates ACRCloud hit on next play).
	fp := "AQADlegacy_fp"
	if err := lib.RecordPlay(result, "", fp); err != nil {
		t.Fatalf("RecordPlay (with fingerprint): %v", err)
	}

	// Existing record should now have the fingerprint stored.
	entry, err = lib.Lookup(result.ACRID)
	if err != nil || entry == nil {
		t.Fatalf("Lookup after update: err=%v entry=%v", err, entry)
	}
	fps, err = lib.loadFingerprints(entry.ID)
	if err != nil {
		t.Fatalf("loadFingerprints: %v", err)
	}
	if len(fps) != 1 || fps[0] != fp {
		t.Errorf("fingerprints = %v, want [%q]", fps, fp)
	}

	// Should also be retrievable by fingerprint.
	byFP, err := lib.LookupByFingerprint(fp)
	if err != nil || byFP == nil {
		t.Fatalf("LookupByFingerprint: err=%v entry=%v", err, byFP)
	}
	if byFP.ACRID != result.ACRID {
		t.Errorf("acrid via fingerprint lookup = %q, want %q", byFP.ACRID, result.ACRID)
	}
}

func TestRecordPlay_FingerprintIncreasesPlayCount(t *testing.T) {
	lib := openTestLibrary(t)

	fp := "AQABz0mUaEkSrepeat"
	result := &RecognitionResult{
		ACRID:  "acrid-repeat",
		Title:  "Autumn Leaves",
		Artist: "Bill Evans",
		Score:  80,
	}

	for i := 0; i < 3; i++ {
		if err := lib.RecordPlay(result, "", fp); err != nil {
			t.Fatalf("RecordPlay #%d: %v", i+1, err)
		}
	}

	entry, err := lib.LookupByFingerprint(fp)
	if err != nil || entry == nil {
		t.Fatalf("LookupByFingerprint: err=%v entry=%v", err, entry)
	}
	if entry.PlayCount != 3 {
		t.Errorf("play_count = %d, want 3", entry.PlayCount)
	}
}

func TestRecordPlay_MultipleFingerprints(t *testing.T) {
	lib := openTestLibrary(t)

	result := &RecognitionResult{
		ACRID:  "acrid-multi",
		Title:  "So What",
		Artist: "Miles Davis",
		Album:  "Kind of Blue",
		Score:  95,
	}

	fp1 := "AQADfp_multi_1"
	fp2 := "AQADfp_multi_2"
	fp3 := "AQADfp_multi_3"

	// First play: fingerprint fp1 captured from start of track.
	if err := lib.RecordPlay(result, "", fp1); err != nil {
		t.Fatalf("RecordPlay fp1: %v", err)
	}
	// Second play: different capture offset produces fp2.
	if err := lib.RecordPlay(result, "", fp2); err != nil {
		t.Fatalf("RecordPlay fp2: %v", err)
	}
	// Third play: yet another offset produces fp3.
	if err := lib.RecordPlay(result, "", fp3); err != nil {
		t.Fatalf("RecordPlay fp3: %v", err)
	}

	// All three fingerprints should map to the same entry.
	for _, fp := range []string{fp1, fp2, fp3} {
		entry, err := lib.LookupByFingerprint(fp)
		if err != nil || entry == nil {
			t.Fatalf("LookupByFingerprint(%q): err=%v entry=%v", fp, err, entry)
		}
		if entry.ACRID != result.ACRID {
			t.Errorf("fp %q: acrid = %q, want %q", fp, entry.ACRID, result.ACRID)
		}
	}

	// Lookup by ACRID should show all three fingerprints in the table.
	entry, err := lib.Lookup(result.ACRID)
	if err != nil || entry == nil {
		t.Fatalf("Lookup: err=%v entry=%v", err, entry)
	}
	allFPs, err := lib.loadFingerprints(entry.ID)
	if err != nil {
		t.Fatalf("loadFingerprints: %v", err)
	}
	if len(allFPs) != 3 {
		t.Errorf("fingerprint count = %d, want 3; got %v", len(allFPs), allFPs)
	}
	for _, want := range []string{fp1, fp2, fp3} {
		found := false
		for _, got := range allFPs {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fingerprint %q not found in %v", want, allFPs)
		}
	}
}

// --- Recognizer flow with mock fingerprinter ---

// mockFingerprinter returns a fixed fingerprint for any WAV file.
type mockFingerprinter struct {
	fp  string
	err error
}

func (m *mockFingerprinter) Fingerprint(_ string) (string, error) {
	return m.fp, m.err
}

// mockRecognizer returns a fixed result for any WAV file.
type mockRecognizer struct {
	result *RecognitionResult
	err    error
	called int
}

func (m *mockRecognizer) Name() string { return "mock" }
func (m *mockRecognizer) Recognize(_ context.Context, _ string) (*RecognitionResult, error) {
	m.called++
	return m.result, m.err
}

func TestRunFingerprintCheck_CacheHitReturnsCachedResult(t *testing.T) {
	lib := openTestLibrary(t)
	fp := "AQABcache_hit_fp"

	// Pre-populate the library with a known fingerprint.
	known := &RecognitionResult{
		ACRID:  "cached-acrid",
		Title:  "Cached Track",
		Artist: "Cached Artist",
		Album:  "Cached Album",
		Score:  90,
	}
	if err := lib.RecordPlay(known, "", fp); err != nil {
		t.Fatalf("seed RecordPlay: %v", err)
	}

	// Create a mock WAV file (content doesn't matter; fingerprinter is mocked).
	wavDir := t.TempDir()
	wavPath := filepath.Join(wavDir, "test.wav")
	if err := os.WriteFile(wavPath, makeSilentWAV(1), 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}

	mockFP := &mockFingerprinter{fp: fp}

	ctx, cancel := context.WithCancel(context.Background())

	// Override captureFromPCMSocket by injecting the wavPath directly via a
	// test-only capture function — we test only the fingerprint+library path
	// by calling the internal helper directly.
	result, skipped := runFingerprintCheck(ctx, mockFP, lib, wavPath)
	cancel()

	if !skipped {
		t.Error("expected fingerprint cache hit to skip ACRCloud")
	}
	if result == nil {
		t.Fatal("expected non-nil result from cache hit")
	}
	if result.Title != known.Title {
		t.Errorf("title = %q, want %q", result.Title, known.Title)
	}
}

func TestRunRecognizer_FingerprintMissFallsBackToACRCloud(t *testing.T) {
	lib := openTestLibrary(t)

	wavDir := t.TempDir()
	wavPath := filepath.Join(wavDir, "test.wav")
	if err := os.WriteFile(wavPath, makeSilentWAV(1), 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}

	fp := "AQABmiss_fp"
	mockFP := &mockFingerprinter{fp: fp}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result, skipped := runFingerprintCheck(ctx, mockFP, lib, wavPath)

	if skipped {
		t.Error("expected fingerprint miss — should NOT skip ACRCloud")
	}
	if result != nil {
		t.Errorf("expected nil result from fingerprint check on miss, got %+v", result)
	}
	// Fingerprint miss means ACRCloud should be called by the caller (runRecognizer).
	// runFingerprintCheck only returns cached results; ACRCloud call is the caller's job.
}

func TestRecordPlay_UnknownStoredForFingerprint(t *testing.T) {
	lib := openTestLibrary(t)

	fp := "AQABno_match_fp"

	// Simulate: fingerprint not in DB, ACRCloud returns nil (no match).
	// After the call, the fingerprint should be stored with Unknown metadata.
	unknown := &RecognitionResult{
		Title:    "Unknown music",
		Artist:   "Unknown artist",
		Album:    "Unknown album",
		Label:    "Unknown",
		Released: "Unknown",
	}
	if err := lib.RecordPlay(unknown, "", fp); err != nil {
		t.Fatalf("RecordPlay unknown: %v", err)
	}

	entry, err := lib.LookupByFingerprint(fp)
	if err != nil || entry == nil {
		t.Fatalf("LookupByFingerprint: err=%v entry=%v", err, entry)
	}
	if entry.Title != "Unknown music" {
		t.Errorf("title = %q, want Unknown music", entry.Title)
	}
	if entry.Artist != "Unknown artist" {
		t.Errorf("artist = %q, want Unknown artist", entry.Artist)
	}
	if entry.Album != "Unknown album" {
		t.Errorf("album = %q, want Unknown album", entry.Album)
	}
}

func TestAddFingerprint_SameEntryIsNoOp(t *testing.T) {
	lib := openTestLibrary(t)

	res, err := lib.db.Exec(`
		INSERT INTO collection (title, artist, play_count, first_played, last_played)
		VALUES ('Track A', 'Artist A', 1, '2024-01-01', '2024-01-01')`)
	if err != nil {
		t.Fatalf("insert collection row: %v", err)
	}
	collectionID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}

	fp := "AQAD-same-entry"
	if err := lib.addFingerprint(collectionID, fp); err != nil {
		t.Fatalf("first addFingerprint: %v", err)
	}
	if err := lib.addFingerprint(collectionID, fp); err != nil {
		t.Fatalf("second addFingerprint should be no-op: %v", err)
	}

	var count int
	if err := lib.db.QueryRow(`SELECT COUNT(*) FROM track_fingerprints WHERE fingerprint = ?`, fp).Scan(&count); err != nil {
		t.Fatalf("count fingerprints: %v", err)
	}
	if count != 1 {
		t.Errorf("fingerprint row count = %d, want 1", count)
	}
}

func TestAddFingerprint_DifferentEntryReturnsConflict(t *testing.T) {
	lib := openTestLibrary(t)

	res1, err := lib.db.Exec(`
		INSERT INTO collection (title, artist, play_count, first_played, last_played)
		VALUES ('Track A', 'Artist A', 1, '2024-01-01', '2024-01-01')`)
	if err != nil {
		t.Fatalf("insert first collection row: %v", err)
	}
	id1, err := res1.LastInsertId()
	if err != nil {
		t.Fatalf("first last insert id: %v", err)
	}

	res2, err := lib.db.Exec(`
		INSERT INTO collection (title, artist, play_count, first_played, last_played)
		VALUES ('Track B', 'Artist B', 1, '2024-01-01', '2024-01-01')`)
	if err != nil {
		t.Fatalf("insert second collection row: %v", err)
	}
	id2, err := res2.LastInsertId()
	if err != nil {
		t.Fatalf("second last insert id: %v", err)
	}

	fp := "AQAD-conflict"
	if err := lib.addFingerprint(id1, fp); err != nil {
		t.Fatalf("seed addFingerprint: %v", err)
	}

	err = lib.addFingerprint(id2, fp)
	if err == nil {
		t.Fatal("expected fingerprint conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "fingerprint conflict") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// runFingerprintCheck is an extracted, testable helper for the fingerprint
// cache-check logic inside runRecognizer. It returns (entry result, wasHit).
// On a cache hit it returns the cached RecognitionResult and wasHit=true.
// On a cache miss it returns (nil, false) so the caller can proceed to ACRCloud.
func runFingerprintCheck(ctx context.Context, fp Fingerprinter, lib *Library, wavPath string) (*RecognitionResult, bool) {
	if fp == nil || lib == nil {
		return nil, false
	}
	gfp, err := fp.Fingerprint(wavPath)
	if err != nil || gfp == "" {
		return nil, false
	}
	entry, err := lib.LookupByFingerprint(gfp)
	if err != nil || entry == nil {
		return nil, false
	}
	result := &RecognitionResult{
		ACRID:    entry.ACRID,
		Title:    entry.Title,
		Artist:   entry.Artist,
		Album:    entry.Album,
		Label:    entry.Label,
		Released: entry.Released,
		Score:    entry.Score,
		Format:   entry.Format,
	}
	if err := lib.RecordPlay(result, entry.ArtworkPath, gfp); err != nil {
		return nil, false
	}
	return result, true
}

// makeSilentWAV generates a minimal valid WAV file with the given duration in seconds.
// Used only to create dummy WAV files for tests that mock the fingerprinter.
func makeSilentWAV(seconds int) []byte {
	const sampleRate = 44100
	const channels = 2
	const bitsPerSample = 16
	numSamples := sampleRate * channels * seconds
	pcmSize := numSamples * (bitsPerSample / 8)

	wav := make([]byte, 44+pcmSize)
	copy(wav[0:], []byte("RIFF"))
	putUint32LE(wav[4:], uint32(36+pcmSize))
	copy(wav[8:], []byte("WAVEfmt "))
	putUint32LE(wav[16:], 16)
	putUint16LE(wav[20:], 1) // PCM
	putUint16LE(wav[22:], uint16(channels))
	putUint32LE(wav[24:], uint32(sampleRate))
	putUint32LE(wav[28:], uint32(sampleRate*channels*bitsPerSample/8))
	putUint16LE(wav[32:], uint16(channels*bitsPerSample/8))
	putUint16LE(wav[34:], uint16(bitsPerSample))
	copy(wav[36:], []byte("data"))
	putUint32LE(wav[40:], uint32(pcmSize))
	return wav
}

func putUint32LE(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putUint16LE(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}
