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
		{"zzzzzzzz", ""},  // invalid hex → empty string, no panic
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
	ticks := int64(44100) // 1 second
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
