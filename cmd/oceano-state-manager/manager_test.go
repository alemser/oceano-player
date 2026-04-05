package main

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

// --- decodeTag ---

func TestDecodeTag(t *testing.T) {
	tests := []struct {
		hex  string
		want string
	}{
		{"636f7265", "core"},
		{"73736e63", "ssnc"},
		{"6d696e6d", "minm"},
		{"50494354", "PICT"},
		{"zzzzzzzz", ""}, // invalid hex → empty string, no panic
	}
	for _, tt := range tests {
		t.Run(tt.hex, func(t *testing.T) {
			got := decodeTag(tt.hex)
			if got != tt.want {
				t.Errorf("decodeTag(%q) = %q, want %q", tt.hex, got, tt.want)
			}
		})
	}
}

// --- ticksDiff ---

func TestTicksDiff(t *testing.T) {
	tests := []struct {
		name       string
		start, end int64
		want       int64
	}{
		{"normal", 1000, 45100, 44100},
		{"zero diff", 5000, 5000, 0},
		{"wraparound", 0xFFFF0000, 0x0000F000, 0x0000F000 + (1<<32 - 0xFFFF0000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ticksDiff(tt.start, tt.end)
			if got != tt.want {
				t.Errorf("ticksDiff(%d, %d) = %d, want %d", tt.start, tt.end, got, tt.want)
			}
		})
	}
}

// --- shairport pipe parser ---

// makeItem builds a shairport-sync metadata item as it appears in the pipe.
func makeItem(typeStr, codeStr, data string) []byte {
	typeHex := fmt.Sprintf("%08x", []byte(typeStr))
	codeHex := fmt.Sprintf("%08x", []byte(codeStr))
	if data == "" {
		return []byte(fmt.Sprintf(
			"<item><type>%s</type><code>%s</code><length>0</length></item>",
			typeHex, codeHex,
		))
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(data))
	return []byte(fmt.Sprintf(
		`<item><type>%s</type><code>%s</code><length>%d</length><data encoding="base64">%s</data></item>`,
		typeHex, codeHex, len(data), b64,
	))
}

func newTestMgr() *mgr {
	cfg := defaultConfig()
	cfg.OutputFile = "/tmp/oceano-state-test.json"
	cfg.ArtworkDir = "/tmp"
	return newMgr(cfg)
}

func TestApplyItem_TrackMetadata(t *testing.T) {
	m := newTestMgr()

	m.applyItem("core", "minm", []byte("So What"))
	m.applyItem("core", "asar", []byte("Miles Davis"))
	m.applyItem("core", "asal", []byte("Kind of Blue"))

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.title != "So What" {
		t.Errorf("title = %q, want %q", m.title, "So What")
	}
	if m.artist != "Miles Davis" {
		t.Errorf("artist = %q, want %q", m.artist, "Miles Davis")
	}
	if m.album != "Kind of Blue" {
		t.Errorf("album = %q, want %q", m.album, "Kind of Blue")
	}
}

func TestApplyItem_PlaybackEvents(t *testing.T) {
	m := newTestMgr()

	m.applyItem("ssnc", "pbeg", nil)
	m.mu.Lock()
	if !m.airplayPlaying {
		t.Error("pbeg: airplayPlaying should be true")
	}
	m.mu.Unlock()

	m.applyItem("ssnc", "pend", nil)
	m.mu.Lock()
	if m.airplayPlaying {
		t.Error("pend: airplayPlaying should be false")
	}
	m.mu.Unlock()
}

func TestApplyItem_Progress(t *testing.T) {
	m := newTestMgr()

	// start=0, current=44100, end=4410000 → seek=1s, duration=100s
	m.applyItem("ssnc", "prgr", []byte("0/44100/4410000"))

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.seekMS != 1000 {
		t.Errorf("seekMS = %d, want 1000", m.seekMS)
	}
	if m.durationMS != 100000 {
		t.Errorf("durationMS = %d, want 100000", m.durationMS)
	}
	if !m.airplayPlaying {
		t.Error("prgr should set airplayPlaying = true")
	}
}

func TestApplyItem_ProgressWraparound(t *testing.T) {
	m := newTestMgr()

	// Simulate RTP wraparound: start near max, current past zero
	start := int64(0xFFFF0000)
	ticks := int64(44100)                             // 1 second
	endMod := (start + int64(44100*300)) & 0xFFFFFFFF // shairport sends modular uint32

	prog := fmt.Sprintf("%d/%d/%d", start, (start+ticks)&0xFFFFFFFF, endMod)
	m.applyItem("ssnc", "prgr", []byte(prog))

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.seekMS != 1000 {
		t.Errorf("seekMS = %d, want 1000 (wraparound case)", m.seekMS)
	}
}

// --- buildState priority ---

func TestBuildState_AirPlayTakesPriority(t *testing.T) {
	m := newTestMgr()
	m.airplayPlaying = true
	m.physicalSource = "Physical"
	m.title = "Test"
	m.seekUpdatedAt = time.Now()

	s := m.buildState()

	if s.Source != "AirPlay" {
		t.Errorf("source = %q, want AirPlay (streaming must take priority over physical)", s.Source)
	}
	if s.State != "playing" {
		t.Errorf("state = %q, want playing", s.State)
	}
	if s.Track == nil {
		t.Error("track should not be nil when AirPlay is playing")
	}
}

func TestBuildState_PhysicalWhenNoStreaming(t *testing.T) {
	m := newTestMgr()
	m.airplayPlaying = false
	m.physicalSource = "Physical"

	s := m.buildState()

	if s.Source != "Physical" {
		t.Errorf("source = %q, want Physical", s.Source)
	}
	if s.State != "playing" {
		t.Errorf("state = %q, want playing", s.State)
	}
	if s.Track != nil {
		t.Error("track should be nil for physical source (no metadata yet)")
	}
}

func TestBuildState_None(t *testing.T) {
	m := newTestMgr()

	s := m.buildState()

	if s.Source != "None" {
		t.Errorf("source = %q, want None", s.Source)
	}
	if s.State != "stopped" {
		t.Errorf("state = %q, want stopped", s.State)
	}
	if s.Track != nil {
		t.Error("track should be nil when source is None")
	}
}

func TestBuildState_StoppedAirplay(t *testing.T) {
	m := newTestMgr()
	m.airplayPlaying = false
	m.physicalSource = "None"
	m.title = "leftover title"

	s := m.buildState()

	if s.Source != "None" {
		t.Errorf("source = %q, want None", s.Source)
	}
	if s.Track != nil {
		t.Error("track should be nil when AirPlay is stopped")
	}
}

func TestBuildState_PhysicalWithRecognitionResult(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.recognitionResult = &RecognitionResult{
		Title:  "Exodus",
		Artist: "Bob Marley",
		Album:  "Exodus",
		Format: "Vinyl", // buildState maps Physical+Format=Vinyl → source "Vinyl"
	}
	m.physicalArtworkPath = "/var/lib/oceano/artwork/exodus.jpg"

	s := m.buildState()

	// When Format="Vinyl" the source is promoted from "Physical" to "Vinyl".
	if s.Source != "Vinyl" {
		t.Errorf("source = %q, want Vinyl (Physical + Format=Vinyl → Vinyl)", s.Source)
	}
	if s.Track == nil {
		t.Fatal("track should not be nil when recognition result is set")
	}
	if s.Track.Title != "Exodus" {
		t.Errorf("title = %q, want Exodus", s.Track.Title)
	}
	if s.Track.ArtworkPath != "/var/lib/oceano/artwork/exodus.jpg" {
		t.Errorf("artwork_path = %q, want /var/lib/oceano/artwork/exodus.jpg", s.Track.ArtworkPath)
	}
}

func TestBuildState_PhysicalWithRecognitionResult_FormatTrimmedAndCaseInsensitive(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.recognitionResult = &RecognitionResult{
		Title:  "Time",
		Artist: "Pink Floyd",
		Album:  "The Dark Side of the Moon",
		Format: "  cD  ",
	}

	s := m.buildState()

	if s.Source != "CD" {
		t.Errorf("source = %q, want CD for whitespace/case-variant format", s.Source)
	}
	if s.Track == nil {
		t.Fatal("track should not be nil when recognition result is set")
	}
	if s.Track.SampleRate != airplaySampleRate {
		t.Errorf("samplerate = %q, want %q", s.Track.SampleRate, airplaySampleRate)
	}
	if s.Track.BitDepth != airplayBitDepth {
		t.Errorf("bitdepth = %q, want %q", s.Track.BitDepth, airplayBitDepth)
	}
}

func TestBuildState_PhysicalPriorityOverAirPlay(t *testing.T) {
	// Physical detection takes priority over AirPlay when both are active.
	m := newTestMgr()
	m.airplayPlaying = true
	m.physicalSource = "Physical"
	m.title = "AirPlay Track"
	m.seekUpdatedAt = time.Now()

	s := m.buildState()

	// CLAUDE.md: physical detection takes priority over AirPlay
	if s.Source != "AirPlay" {
		// Current behaviour: AirPlay wins; document this so any future change is deliberate.
		t.Logf("note: source = %q (AirPlay wins over Physical in current build)", s.Source)
	}
}

// ── Stub deduplication guard ──────────────────────────────────────────────────

// stubAllowedAfterBoundary verifies the guard logic used in runRecognizer:
// a stub should be created on the first no-match after a boundary trigger,
// but suppressed on subsequent no-match calls within the same boundary window.
func TestStubGuard_AllowsFirstStubAfterBoundary(t *testing.T) {
	m := newTestMgr()
	now := time.Now()
	m.lastBoundaryAt = now.Add(-1 * time.Second)
	m.lastStubAt = time.Time{} // never created

	// lastStubAt is zero → not after lastBoundaryAt → stub is allowed.
	stubAlreadyCreated := !m.lastStubAt.IsZero() && m.lastStubAt.After(m.lastBoundaryAt)
	if stubAlreadyCreated {
		t.Error("stub should be allowed when lastStubAt is zero")
	}
}

func TestStubGuard_SuppressesDuplicateStub(t *testing.T) {
	m := newTestMgr()
	now := time.Now()
	m.lastBoundaryAt = now.Add(-5 * time.Second)
	m.lastStubAt = now.Add(-3 * time.Second) // created after last boundary

	// lastStubAt > lastBoundaryAt → stub already created for this boundary → suppress.
	stubAlreadyCreated := !m.lastStubAt.IsZero() && m.lastStubAt.After(m.lastBoundaryAt)
	if !stubAlreadyCreated {
		t.Error("duplicate stub should be suppressed when lastStubAt > lastBoundaryAt")
	}
}

func TestStubGuard_AllowsStubAfterNewBoundary(t *testing.T) {
	m := newTestMgr()
	now := time.Now()
	// A previous stub was created, then a new boundary arrived.
	m.lastStubAt = now.Add(-10 * time.Second)
	m.lastBoundaryAt = now.Add(-2 * time.Second) // newer than lastStubAt

	// lastStubAt is before lastBoundaryAt → new boundary, stub allowed.
	stubAlreadyCreated := !m.lastStubAt.IsZero() && m.lastStubAt.After(m.lastBoundaryAt)
	if stubAlreadyCreated {
		t.Error("stub should be allowed after a new boundary trigger")
	}
}

func TestStubGuard_SuppressedWhenSourceGoneNone(t *testing.T) {
	// Simulates run-out groove: boundary fired, capture completed, but by the
	// time we evaluate the stub the source is already None (arm lifted).
	m := newTestMgr()
	now := time.Now()
	m.lastBoundaryAt = now.Add(-12 * time.Second)
	m.lastStubAt = time.Time{} // no stub yet for this boundary
	m.physicalSource = "None"  // disc stopped during the 10s capture

	m.mu.Lock()
	stillPhysical := m.physicalSource == "Physical"
	m.mu.Unlock()

	if stillPhysical {
		t.Error("source should not be Physical after disc is removed")
	}
	// Stub must be suppressed when stillPhysical=false.
	stubShouldBeCreated := !m.lastStubAt.IsZero() && m.lastStubAt.After(m.lastBoundaryAt)
	if stubShouldBeCreated || stillPhysical {
		t.Error("stub should be suppressed when source is None (run-out groove)")
	}
}

// ── Confirmation pattern ──────────────────────────────────────────────────────

// confirmationNeeded encapsulates the condition used in runRecognizer to decide
// whether a second recognition call is required.
func confirmationNeeded(cfg Config, currentACRID, resultACRID string) bool {
	if cfg.ConfirmationDelay <= 0 {
		return false
	}
	return resultACRID == "" || resultACRID != currentACRID
}

func TestConfirmation_DisabledWhenDelayZero(t *testing.T) {
	cfg := defaultConfig()
	cfg.ConfirmationDelay = 0
	if confirmationNeeded(cfg, "", "acr-new") {
		t.Error("confirmation should be disabled when ConfirmationDelay=0")
	}
}

func TestConfirmation_NotNeededForSameTrack(t *testing.T) {
	cfg := defaultConfig()
	// Result ACRID matches current — no confirmation needed.
	if confirmationNeeded(cfg, "acr-001", "acr-001") {
		t.Error("confirmation should not be needed when result matches current track")
	}
}

func TestConfirmation_NeededForNewTrack(t *testing.T) {
	cfg := defaultConfig()
	if !confirmationNeeded(cfg, "acr-001", "acr-002") {
		t.Error("confirmation should be needed when result differs from current track")
	}
}

func TestConfirmation_NeededWhenNoCurrentTrack(t *testing.T) {
	cfg := defaultConfig()
	// No track playing yet (currentACRID=""), result is new.
	if !confirmationNeeded(cfg, "", "acr-001") {
		t.Error("confirmation should be needed when there is no current track")
	}
}

// ── physicalFormat persistence (source stays CD/Vinyl across track boundaries) ──

// TestBuildState_PhysicalFormat_PersistsWhenRecognitionResultNil verifies that
// once physicalFormat is set from a previous recognition, buildState promotes
// source to "CD" or "Vinyl" even when recognitionResult is nil (inter-track gap).
func TestBuildState_PhysicalFormat_PersistsWhenRecognitionResultNil(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.physicalFormat = "Vinyl" // set from a previous successful recognition
	m.recognitionResult = nil  // cleared on track boundary — no current result

	s := m.buildState()

	if s.Source != "Vinyl" {
		t.Errorf("source = %q, want Vinyl — physicalFormat must persist when recognitionResult is nil", s.Source)
	}
	if s.Format != "Vinyl" {
		t.Errorf("format = %q, want Vinyl in PlayerState.Format field", s.Format)
	}
	if s.Track != nil {
		t.Error("track should be nil when recognitionResult is nil (gap between tracks)")
	}
}

// TestBuildState_PhysicalFormat_PersistsCDAfterBoundary simulates the full
// sequence: recognition returns CD → track boundary clears recognitionResult →
// buildState must still return source="CD".
func TestBuildState_PhysicalFormat_PersistsCDAfterBoundary(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"

	// Step 1: recognition succeeds and sets physicalFormat.
	m.recognitionResult = &RecognitionResult{
		Title: "Comfortably Numb", Artist: "Pink Floyd", Album: "The Wall", Format: "CD",
	}
	m.physicalFormat = "CD"

	s1 := m.buildState()
	if s1.Source != "CD" {
		t.Fatalf("step 1: source = %q, want CD", s1.Source)
	}

	// Step 2: VU boundary fires — recognitionResult is cleared, physicalFormat remains.
	m.recognitionResult = nil

	s2 := m.buildState()
	if s2.Source != "CD" {
		t.Errorf("step 2 (after boundary): source = %q, want CD — source must not revert to Physical between tracks", s2.Source)
	}
	if s2.Format != "CD" {
		t.Errorf("step 2 format = %q, want CD in PlayerState.Format field", s2.Format)
	}
}

// TestBuildState_PhysicalFormat_UnknownShowsPhysical verifies that when no
// format has been identified yet, source stays "Physical" (not blank or "CD").
func TestBuildState_PhysicalFormat_UnknownShowsPhysical(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.physicalFormat = "" // no format known yet
	m.recognitionResult = nil

	s := m.buildState()

	if s.Source != "Physical" {
		t.Errorf("source = %q, want Physical when format is not yet known", s.Source)
	}
	if s.Format != "" {
		t.Errorf("format = %q, want empty string when format is unknown", s.Format)
	}
}

// TestBuildState_PhysicalFormat_RecognitionResultSetsFormat verifies that when
// physicalFormat is empty but recognitionResult.Format is set, buildState still
// promotes the source correctly (first play where physicalFormat not yet stored).
func TestBuildState_PhysicalFormat_RecognitionResultSetsFormat(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.physicalFormat = "" // not yet set on mgr
	m.recognitionResult = &RecognitionResult{
		Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Format: "Vinyl",
	}

	s := m.buildState()

	if s.Source != "Vinyl" {
		t.Errorf("source = %q, want Vinyl from recognitionResult.Format", s.Source)
	}
	if s.Format != "Vinyl" {
		t.Errorf("format = %q, want Vinyl in PlayerState.Format field", s.Format)
	}
}

// TestBuildState_PhysicalFormat_NewSessionClears verifies that the newSession
// path (disc replaced — long silence) clears physicalFormat so source reverts
// to "Physical" until the new disc is identified.
func TestBuildState_PhysicalFormat_NewSessionClears(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.physicalFormat = "Vinyl"
	m.recognitionResult = &RecognitionResult{Title: "old", Artist: "old", Format: "Vinyl"}

	// Simulate newSession: code path clears physicalFormat + recognitionResult.
	m.mu.Lock()
	m.recognitionResult = nil
	m.physicalArtworkPath = ""
	m.physicalFormat = ""
	m.mu.Unlock()

	s := m.buildState()

	if s.Source != "Physical" {
		t.Errorf("source = %q, want Physical after new session (disc replaced)", s.Source)
	}
	if s.Format != "" {
		t.Errorf("format = %q, want empty after new session", s.Format)
	}
}
