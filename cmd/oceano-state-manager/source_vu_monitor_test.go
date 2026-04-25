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

func TestShouldSuppressBoundary_BypassWindowPreventsSuppression(t *testing.T) {
	now := time.Now()
	recognizedAt := now.Add(-30 * time.Second)

	if got := shouldSuppressBoundary(240000, 15000, recognizedAt, now.Add(10*time.Second), now, 0.75); got {
		t.Fatal("expected no suppression in early-track bypass window")
	}
}

func TestShouldSuppressBoundary_BypassWindowIgnoredAfterEarlyWindow(t *testing.T) {
	now := time.Now()
	recognizedAt := now.Add(-20 * time.Second)
	// elapsed ~= 80s, bypass should no longer disable suppression.
	if got := shouldSuppressBoundary(240000, 60000, recognizedAt, now.Add(10*time.Second), now, 0.75); !got {
		t.Fatal("expected suppression when bypass is active but elapsed is outside early window")
	}
}

func TestShouldSuppressBoundary_SuppressesWithoutBypass(t *testing.T) {
	now := time.Now()
	recognizedAt := now.Add(-30 * time.Second)

	if got := shouldSuppressBoundary(240000, 15000, recognizedAt, time.Time{}, now, 0.75); !got {
		t.Fatal("expected suppression when elapsed is below 75% and no bypass is active")
	}
}

func TestShouldIgnoreBoundaryAtMatureProgress_ThreeZones(t *testing.T) {
	now := time.Now()
	seekUpdatedAt := now.Add(-10 * time.Second)
	// durationMs = 240s, seekUpdatedAt = 10s ago
	// elapsed = seekMS + 10s

	// Zone 1: elapsed < 75% — not in mature zone, should NOT suppress
	if got := shouldIgnoreBoundaryAtMatureProgress(240000, 15000, seekUpdatedAt, now, 0.75); got {
		t.Fatal("zone 1 (<75%): expected false")
	}

	// Zone 2: 75% ≤ elapsed < 100% — should suppress quiet-passage boundaries
	// elapsed = 170s + 10s = 180s = 75% of 240s
	if got := shouldIgnoreBoundaryAtMatureProgress(240000, 170000, seekUpdatedAt, now, 0.75); !got {
		t.Fatal("zone 2 (75-100%): expected true")
	}

	// Zone 3: elapsed ≥ 100% — track is over, must NOT suppress so triggers can fire
	// elapsed = 235s + 10s = 245s > 240s
	if got := shouldIgnoreBoundaryAtMatureProgress(240000, 235000, seekUpdatedAt, now, 0.75); got {
		t.Fatal("zone 3 (>100%): expected false — track duration exceeded")
	}
}

func TestShouldClearStaleRecognitionOnSilence_KnownDurationBeforeProgressFloor(t *testing.T) {
	now := time.Now()
	seekUpdatedAt := now.Add(-5 * time.Second)
	// elapsed ~= 65s on a 240s track => 27%, below 70% floor.
	if got := shouldClearStaleRecognitionOnSilence(240000, 60000, seekUpdatedAt, now, 20*time.Second); got {
		t.Fatal("expected no stale clear before progress floor")
	}
}

func TestShouldClearStaleRecognitionOnSilence_KnownDurationAfterProgressFloor(t *testing.T) {
	now := time.Now()
	seekUpdatedAt := now.Add(-5 * time.Second)
	// elapsed ~= 224s on a 240s track => 93%, above floor.
	if got := shouldClearStaleRecognitionOnSilence(240000, 219000, seekUpdatedAt, now, 25*time.Second); !got {
		t.Fatal("expected stale clear after progress floor with prolonged silence")
	}
}

func TestShouldClearStaleRecognitionOnSilence_UnknownDurationDoesNotClear(t *testing.T) {
	now := time.Now()
	if got := shouldClearStaleRecognitionOnSilence(0, 0, time.Time{}, now, 60*time.Second); got {
		t.Fatal("expected no stale clear for unknown duration")
	}
}

func TestShouldCoalesceIntraTrackSilenceAudio(t *testing.T) {
	now := time.Now()
	seekAt := now.Add(-20 * time.Second)
	rec := &RecognitionResult{ACRID: "acr1", DurationMs: 106000}

	if shouldCoalesceIntraTrackSilenceAudio("energy-change", rec, 106000, 10000, seekAt, now) {
		t.Fatal("energy-change should not coalesce")
	}
	if shouldCoalesceIntraTrackSilenceAudio("silence->audio", nil, 106000, 10000, seekAt, now) {
		t.Fatal("nil rec should not coalesce")
	}
	if shouldCoalesceIntraTrackSilenceAudio("silence->audio", &RecognitionResult{DurationMs: 106000}, 106000, 10000, seekAt, now) {
		t.Fatal("rec without provider id should not coalesce")
	}
	if shouldCoalesceIntraTrackSilenceAudio("silence->audio", rec, 0, 10000, seekAt, now) {
		t.Fatal("unknown duration should not coalesce")
	}
	// elapsed = 10s + 20s = 30s, maxEarly ≈ min(100s, 58.3s) => coalesce
	if !shouldCoalesceIntraTrackSilenceAudio("silence->audio", rec, 106000, 10000, seekAt, now) {
		t.Fatal("expected coalesce in early segment of known track")
	}
	// elapsed < min floor (4s wall + 2s reported seek) — still "just started"
	earlyAnchor := now.Add(-2 * time.Second)
	if shouldCoalesceIntraTrackSilenceAudio("silence->audio", rec, 106000, 2000, earlyAnchor, now) {
		t.Fatal("expected no coalesce immediately after seek anchor")
	}
	// Very short track: maxEarly < 15s floor → no coalesce
	if shouldCoalesceIntraTrackSilenceAudio("silence->audio", rec, 20000, 5000, seekAt, now) {
		t.Fatal("expected no coalesce for very short track duration metadata")
	}
	// Late in track: elapsed past maxEarly
	lateSeek := now.Add(-70 * time.Second)
	if shouldCoalesceIntraTrackSilenceAudio("silence->audio", rec, 106000, 20000, lateSeek, now) {
		t.Fatal("expected no coalesce past early window")
	}
	shazamOnly := &RecognitionResult{ShazamID: "5912597", DurationMs: 106000}
	if !shouldCoalesceIntraTrackSilenceAudio("silence->audio", shazamOnly, 106000, 10000, seekAt, now) {
		t.Fatal("Shazam-only id should still enable coalesce")
	}
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
