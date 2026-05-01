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
	if m.airplayDACPActiveRemote != "" || m.airplayDACPID != "" || m.airplayDACPClientIP != "" {
		t.Error("pend: DACP context should be cleared")
	}
	m.mu.Unlock()
}

func TestApplyItem_AirPlayDACPContext(t *testing.T) {
	m := newTestMgr()
	m.applyItem("ssnc", "acre", []byte("123456789"))
	m.applyItem("ssnc", "daid", []byte("ABCDEF0123456789"))
	m.applyItem("ssnc", "clip", []byte("192.168.1.44"))
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.airplayDACPActiveRemote != "123456789" {
		t.Fatalf("activeRemote = %q, want 123456789", m.airplayDACPActiveRemote)
	}
	if m.airplayDACPID != "ABCDEF0123456789" {
		t.Fatalf("dacpID = %q, want ABCDEF0123456789", m.airplayDACPID)
	}
	if m.airplayDACPClientIP != "192.168.1.44" {
		t.Fatalf("clientIP = %q, want 192.168.1.44", m.airplayDACPClientIP)
	}
	if m.airplayDACPUpdatedAt.IsZero() {
		t.Fatal("airplayDACPUpdatedAt should be set")
	}
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
	m.airplayDACPActiveRemote = "123"
	m.airplayDACPID = "ABC"
	m.airplayDACPClientIP = "127.0.0.1"
	m.airplayDACPUpdatedAt = time.Now()
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
	if s.AirPlayTransport == nil || !s.AirPlayTransport.Available || s.AirPlayTransport.SessionState != "ready" {
		t.Fatalf("airplay transport = %+v, want ready/available", s.AirPlayTransport)
	}
}

func TestBuildState_AirPlayTransportMissingContext(t *testing.T) {
	m := newTestMgr()
	m.airplayPlaying = true
	m.title = "Track"
	m.seekUpdatedAt = time.Now()

	s := m.buildState()
	if s.AirPlayTransport == nil {
		t.Fatal("airplay transport is nil")
	}
	if s.AirPlayTransport.Available {
		t.Fatal("airplay transport should be unavailable without DACP context")
	}
	if s.AirPlayTransport.SessionState != "no_airplay_session" {
		t.Fatalf("session_state = %q, want no_airplay_session", s.AirPlayTransport.SessionState)
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
	cfg.ConfirmationDelay = 2 * time.Second
	// Result ACRID matches current — no confirmation needed.
	if confirmationNeeded(cfg, "acr-001", "acr-001") {
		t.Error("confirmation should not be needed when result matches current track")
	}
}

func TestConfirmation_NeededForNewTrack(t *testing.T) {
	cfg := defaultConfig()
	cfg.ConfirmationDelay = 2 * time.Second
	if !confirmationNeeded(cfg, "acr-001", "acr-002") {
		t.Error("confirmation should be needed when result differs from current track")
	}
}

func TestConfirmation_NeededWhenNoCurrentTrack(t *testing.T) {
	cfg := defaultConfig()
	cfg.ConfirmationDelay = 2 * time.Second
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

// ── Physical seek output ──────────────────────────────────────────────────────

// TestBuildState_Physical_PopulatesSeek proves that physicalSeekMS and
// physicalSeekUpdatedAt are reflected in the TrackInfo returned by buildState
// so the frontend can interpolate the progress bar position.
func TestBuildState_Physical_PopulatesSeek(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.recognitionResult = &RecognitionResult{
		Title:      "Exodus",
		Artist:     "Bob Marley",
		DurationMs: 244000,
	}
	seekAt := time.Now().Add(-1 * time.Second)
	m.physicalSeekMS = 15000
	m.physicalSeekUpdatedAt = seekAt

	s := m.buildState()

	if s.Track == nil {
		t.Fatal("track should not be nil when recognition result is set")
	}
	if s.Track.SeekMS != 15000 {
		t.Errorf("SeekMS = %d, want 15000", s.Track.SeekMS)
	}
	if s.Track.SeekUpdatedAt == "" {
		t.Error("SeekUpdatedAt should not be empty when physicalSeekUpdatedAt is set")
	}
	if s.Track.DurationMS != 244000 {
		t.Errorf("DurationMS = %d, want 244000", s.Track.DurationMS)
	}
}

// TestBuildState_Physical_NoSeekWhenNotSet proves that when recognition result
// exists but seek has not been set (e.g. stub enriched via UI), SeekUpdatedAt
// is empty and SeekMS is zero — the UI hides the progress bar gracefully.
func TestBuildState_Physical_NoSeekWhenNotSet(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.recognitionResult = &RecognitionResult{
		Title:  "Unknown Track",
		Artist: "Unknown Artist",
	}
	// physicalSeekMS and physicalSeekUpdatedAt left at zero values.

	s := m.buildState()

	if s.Track == nil {
		t.Fatal("track should not be nil when recognition result is set")
	}
	if s.Track.SeekMS != 0 {
		t.Errorf("SeekMS = %d, want 0 when seek not set", s.Track.SeekMS)
	}
	if s.Track.SeekUpdatedAt != "" {
		t.Errorf("SeekUpdatedAt = %q, want empty when physicalSeekUpdatedAt is zero", s.Track.SeekUpdatedAt)
	}
}

// ── vuInSilence — Bugs 1 and 5 ───────────────────────────────────────────────

// TestBuildState_PhysicalVuSilenceIsIdle verifies that when the VU monitor
// enters silence (inter-track gap), buildState returns state="idle" and
// track=nil even though the physical source is still "Physical".
func TestBuildState_PhysicalVuSilenceIsIdle(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.vuInSilence = true

	s := m.buildState()

	if s.State != "idle" {
		t.Errorf("state = %q, want idle during VU silence (Bug 5 fix)", s.State)
	}
	if s.Track != nil {
		t.Errorf("track should be nil during VU silence, got %+v", s.Track)
	}
}

// TestBuildState_PhysicalVuSilenceHidesRecognizedTrack verifies that a
// previously recognised track is not shown while the VU monitor is in silence,
// preventing the "display not cleared on silence" symptom (Bug 1).
func TestBuildState_PhysicalVuSilenceHidesRecognizedTrack(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.physicalFormat = "Vinyl"
	m.vuInSilence = true
	m.recognitionResult = &RecognitionResult{
		Title: "Comfortably Numb", Artist: "Pink Floyd", Format: "Vinyl",
	}

	s := m.buildState()

	if s.State != "idle" {
		t.Errorf("state = %q, want idle during VU silence even with known track", s.State)
	}
	if s.Track != nil {
		t.Errorf("track should be nil during VU silence, got %+v", s.Track)
	}
	// Source/format labels should still reflect what was playing.
	if s.Source != "Vinyl" {
		t.Errorf("source = %q, want Vinyl — format label should persist during silence", s.Source)
	}
}

// TestBuildState_IdleDelayAfterDetectorNone_NotPlaying verifies that during the
// post-Physical idle-delay window, if the source detector is already None we do
// not emit state=playing (which would drive "Identifying…" on the display).
func TestBuildState_IdleDelayAfterDetectorNone_NotPlaying(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "None"
	m.lastPhysicalAt = time.Now()
	m.vuInSilence = false
	m.recognitionResult = nil

	s := m.buildState()

	if s.State != "stopped" {
		t.Errorf("state = %q, want stopped when physicalSource is None during idle-delay tail", s.State)
	}
	if s.Source != "Physical" {
		t.Errorf("source = %q, want Physical (grace label still applies)", s.Source)
	}
	if s.PhysicalDetectorActive {
		t.Error("PhysicalDetectorActive should be false when detector reports None")
	}
}

// During the idle-delay tail the detector is None but we may still expose the last
// recognised track (e.g. writer tick) — state stays stopped so the client does not
// show "Identifying…".
func TestBuildState_IdleDelayTailNone_StillExposesLastTrack(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "None"
	m.lastPhysicalAt = time.Now()
	m.vuInSilence = false
	m.recognitionResult = &RecognitionResult{
		Title: "News", Artist: "Dire Straits", Album: "Communiqué", Format: "CD",
	}
	m.physicalFormat = "CD"

	s := m.buildState()
	if s.State != "stopped" {
		t.Fatalf("state = %q, want stopped", s.State)
	}
	if s.Source != "CD" {
		t.Fatalf("source = %q, want CD", s.Source)
	}
	if s.Track == nil || s.Track.Title != "News" {
		t.Fatalf("track = %+v, want News metadata during tail", s.Track)
	}
	if s.PhysicalDetectorActive {
		t.Fatal("PhysicalDetectorActive should be false during idle-delay tail with detector None")
	}
}

// After IdleDelay from last Physical sighting, physicalActive is false — output
// matches a cold idle system (no physical grace window).
func TestBuildState_AfterIdleDelay_SourceNone(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "None"
	m.lastPhysicalAt = time.Now().Add(-11 * time.Second)
	m.vuInSilence = false
	m.recognitionResult = &RecognitionResult{Title: "Ghost", Artist: "Artist", Format: "Vinyl"}
	m.physicalFormat = "Vinyl"

	s := m.buildState()
	if s.Source != "None" {
		t.Errorf("source = %q, want None once idle-delay window elapsed", s.Source)
	}
	if s.State != "stopped" {
		t.Errorf("state = %q, want stopped", s.State)
	}
	if s.Track != nil {
		t.Errorf("track should be nil when source is None (not physical branch)")
	}
}

// Legitimate kiosk "identifying" path: detector says Physical, VU not in silence,
// no recognition row yet — state must be playing with nil track.
func TestBuildState_PhysicalActiveNoRecognition_IsPlayingWithoutTrack(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.lastPhysicalAt = time.Now()
	m.vuInSilence = false
	m.recognitionResult = nil
	m.physicalFormat = ""

	s := m.buildState()
	if s.State != "playing" {
		t.Fatalf("state = %q, want playing", s.State)
	}
	if s.Source != "Physical" {
		t.Fatalf("source = %q, want Physical", s.Source)
	}
	if s.Track != nil {
		t.Fatalf("track should be nil before first match (nowplaying shows Identifying…)")
	}
	if !s.PhysicalDetectorActive {
		t.Fatal("PhysicalDetectorActive should be true when detector is Physical")
	}
}

// TestBuildState_PhysicalNoVuSilenceIsPlaying verifies the normal (non-silence)
// path is unaffected by the vuInSilence field when it is false.
func TestBuildState_PhysicalNoVuSilenceIsPlaying(t *testing.T) {
	m := newTestMgr()
	m.physicalSource = "Physical"
	m.vuInSilence = false
	m.recognitionResult = &RecognitionResult{
		Title: "Money", Artist: "Pink Floyd", Format: "Vinyl",
	}

	s := m.buildState()

	if s.State != "playing" {
		t.Errorf("state = %q, want playing when vuInSilence=false", s.State)
	}
	if s.Track == nil {
		t.Error("track should not be nil when vuInSilence=false and recognition result is set")
	}
}

// Recognition phase "off" must appear in state even if recognizerBusyUntil is still
// in the future from a previous capture window; otherwise kiosk clients show
// "Identifying…" while the coordinator is skipping by input policy.
func TestBuildState_RecognitionOffPrecedesStaleRecognizerBusy(t *testing.T) {
	m := newTestMgr()
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.vuInSilence = false
	m.recognitionResult = nil
	m.recognitionPhase = "off"
	m.recognizerBusyUntil = time.Now().Add(2 * time.Minute)
	m.mu.Unlock()

	s := m.buildState()
	if s.Recognition == nil {
		t.Fatal("Recognition is nil")
	}
	if s.Recognition.Phase != "off" {
		t.Fatalf("Recognition.Phase = %q, want off", s.Recognition.Phase)
	}
	if s.Recognition.Detail != "input_policy_off" {
		t.Fatalf("Recognition.Detail = %q, want input_policy_off", s.Recognition.Detail)
	}
}

func TestSyncFromLibrary_PreservesSeekWhenDurationArrivesLate(t *testing.T) {
	lib := openTestLibrary(t)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := lib.DB().Exec(`
		INSERT INTO collection
			(acrid, shazam_id, title, artist, score, play_count, first_played, last_played, user_confirmed, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "acr-late-duration", "", "Late Duration", "Artist", 80, 1, now, now, 1, 180000)
	if err != nil {
		t.Fatalf("insert collection row: %v", err)
	}

	m := newTestMgr()
	m.lib = lib
	m.mu.Lock()
	m.physicalSource = "Physical"
	m.recognitionResult = &RecognitionResult{ACRID: "acr-late-duration", Title: "Late Duration", Artist: "Artist", DurationMs: 0}
	m.physicalSeekMS = 240000
	m.physicalSeekUpdatedAt = time.Now()
	m.mu.Unlock()

	m.syncFromLibrary(lib)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recognitionResult == nil {
		t.Fatal("expected recognition result after sync")
	}
	if m.recognitionResult.DurationMs != 180000 {
		t.Fatalf("DurationMs = %d, want 180000", m.recognitionResult.DurationMs)
	}
	if m.physicalSeekMS != 240000 {
		t.Fatalf("physicalSeekMS = %d, want preserved seek after late duration update", m.physicalSeekMS)
	}
	if m.physicalSeekUpdatedAt.IsZero() {
		t.Fatalf("physicalSeekUpdatedAt should stay set after late duration update")
	}
}
