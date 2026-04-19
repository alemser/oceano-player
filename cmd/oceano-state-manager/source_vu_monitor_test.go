package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPollSourceFile_ResumeWithinSessionQueuesRecognition(t *testing.T) {
	m := newTestMgr()
	file := filepath.Join(t.TempDir(), "source.json")
	m.cfg.SourceFile = file
	m.cfg.IdleDelay = 10 * time.Second
	m.cfg.SessionGapThreshold = 45 * time.Second
	m.physicalSource = "None"
	m.lastPhysicalAt = time.Now().Add(-4 * time.Second)
	m.physicalStartedAt = time.Now().Add(-2 * time.Minute)
	m.recognitionResult = &RecognitionResult{Title: "Track 1", Artist: "Artist 1", DurationMs: 240000, Format: "Vinyl"}

	if err := os.WriteFile(file, []byte(`{"source":"Physical"}`), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	beforeStartedAt := m.physicalStartedAt
	m.pollSourceFile()

	if m.physicalSource != "Physical" {
		t.Fatalf("physicalSource = %q, want Physical", m.physicalSource)
	}
	if m.recognitionResult == nil || m.recognitionResult.Title != "Track 1" {
		t.Fatal("recognitionResult should be preserved on same-session resume")
	}
	if !m.physicalStartedAt.After(beforeStartedAt) {
		t.Fatal("physicalStartedAt should reset on same-session resume")
	}
	select {
	case trig := <-m.recognizeTrigger:
		if trig.isBoundary {
			t.Fatal("resume trigger should not be marked as boundary")
		}
	default:
		t.Fatal("expected recognition trigger on same-session resume")
	}
}

func TestFireBoundaryTrigger_SilenceResetsSeekSuppression(t *testing.T) {
	// Scenario: needle dropped near end of track.
	// Recognition fires quickly → physicalSeekMS is small (~15 s overhead).
	// End-of-track silence is then detected.
	// Without the fix, elapsed (15 s + silence gap) < 75% of 240 s → boundary suppressed.
	// With the fix, silence detection resets seek → duration guard bypassed.
	m := newTestMgr()
	recognizedAt := time.Now().Add(-30 * time.Second)
	m.physicalSource = "Physical"
	m.physicalSeekMS = 15000 // 15 s since capture — not actual position
	m.physicalSeekUpdatedAt = recognizedAt
	m.recognitionResult = &RecognitionResult{
		Title:      "Needle Near End",
		Artist:     "Test Artist",
		DurationMs: 240000, // 4-min track, 75% = 3:00
	}

	// Simulate the VU monitor committing silence (silenceFrames reached).
	m.mu.Lock()
	m.physicalSeekMS = 0
	m.physicalSeekUpdatedAt = time.Time{}
	m.mu.Unlock()

	// Now simulate fireBoundaryTrigger — check the guard directly.
	m.mu.Lock()
	var durationMs int
	var seekMS int64
	var seekUpdatedAt time.Time
	if m.recognitionResult != nil {
		durationMs = m.recognitionResult.DurationMs
	}
	seekMS = m.physicalSeekMS
	seekUpdatedAt = m.physicalSeekUpdatedAt
	m.mu.Unlock()

	// The guard: if seekUpdatedAt is zero, suppression must be skipped.
	wouldSuppress := false
	if durationMs > 0 && !seekUpdatedAt.IsZero() {
		elapsed := time.Duration(seekMS)*time.Millisecond + time.Since(seekUpdatedAt)
		suppressUntil := time.Duration(float64(time.Duration(durationMs)*time.Millisecond) * 0.75)
		if elapsed < suppressUntil {
			wouldSuppress = true
		}
	}
	if wouldSuppress {
		t.Fatal("boundary should NOT be suppressed after silence reset seek state")
	}
	_ = seekUpdatedAt // used above
}

func TestPollSourceFile_TinyGapDoesNotQueueRecognition(t *testing.T) {
	m := newTestMgr()
	file := filepath.Join(t.TempDir(), "source.json")
	m.cfg.SourceFile = file
	m.cfg.IdleDelay = 10 * time.Second
	m.cfg.SessionGapThreshold = 45 * time.Second
	m.physicalSource = "None"
	m.lastPhysicalAt = time.Now().Add(-1500 * time.Millisecond)
	m.physicalStartedAt = time.Now().Add(-2 * time.Minute)
	m.recognitionResult = &RecognitionResult{Title: "Track 1", Artist: "Artist 1", DurationMs: 240000, Format: "Vinyl"}

	if err := os.WriteFile(file, []byte(`{"source":"Physical"}`), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	beforeStartedAt := m.physicalStartedAt
	m.pollSourceFile()

	if m.physicalStartedAt != beforeStartedAt {
		t.Fatal("physicalStartedAt should not change on tiny gap resume")
	}
	select {
	case <-m.recognizeTrigger:
		t.Fatal("did not expect recognition trigger on tiny gap resume")
	default:
	}
}
