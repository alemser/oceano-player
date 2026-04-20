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
		t.Fatal("expected no suppression while bypass window is active")
	}
}

func TestShouldSuppressBoundary_SuppressesWithoutBypass(t *testing.T) {
	now := time.Now()
	recognizedAt := now.Add(-30 * time.Second)

	if got := shouldSuppressBoundary(240000, 15000, recognizedAt, time.Time{}, now, 0.75); !got {
		t.Fatal("expected suppression when elapsed is below 75% and no bypass is active")
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
