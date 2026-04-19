package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTickPlayHistory_UpgradesPhysicalPlaceholderInPlace(t *testing.T) {
	lib := openTestLibrary(t)
	m := newTestMgr()
	m.lib = lib
	m.physicalSource = "Physical"
	m.physicalFormat = "Vinyl"

	m.tickPlayHistory()

	placeholderID := m.currentPlayHistoryID
	if placeholderID <= 0 {
		t.Fatal("expected placeholder play_history row to be opened")
	}
	if m.currentPlayKey != physicalUnknownHistoryKey {
		t.Fatalf("currentPlayKey = %q, want placeholder key", m.currentPlayKey)
	}

	recognizedAt := time.Now().UTC()
	m.recognitionResult = &RecognitionResult{
		Title:       "So What",
		Artist:      "Miles Davis",
		Album:       "Kind of Blue",
		TrackNumber: "A1",
		DurationMs:  545000,
	}
	m.physicalSeekUpdatedAt = recognizedAt
	m.physicalSeekMS = 12000

	m.tickPlayHistory()

	if m.currentPlayHistoryID != placeholderID {
		t.Fatalf("currentPlayHistoryID = %d, want %d", m.currentPlayHistoryID, placeholderID)
	}
	if m.currentPlayKey != "Physical\x00So What\x00Miles Davis" {
		t.Fatalf("currentPlayKey = %q, want recognized key", m.currentPlayKey)
	}

	entries, total, err := lib.ListPlayHistory(10, 0)
	if err != nil {
		t.Fatalf("ListPlayHistory: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("history rows = total:%d len:%d, want 1 row", total, len(entries))
	}
	entry := entries[0]
	if entry.ID != placeholderID {
		t.Fatalf("entry.ID = %d, want %d", entry.ID, placeholderID)
	}
	if entry.Title != "So What" || entry.Artist != "Miles Davis" {
		t.Fatalf("entry metadata = %q / %q, want recognized values", entry.Title, entry.Artist)
	}
	if entry.MediaFormat != "Vinyl" || entry.VinylSide != "A" {
		t.Fatalf("format/side = %q / %q, want Vinyl / A", entry.MediaFormat, entry.VinylSide)
	}
	if entry.EndedAt != "" {
		t.Fatalf("entry.EndedAt = %q, want open row", entry.EndedAt)
	}
	if entry.StartedAt == "" {
		t.Fatal("entry.StartedAt should be backdated on recognition upgrade")
	}
}

func TestPollSourceFile_ResumeAfterIdleQueuesRecognition(t *testing.T) {
	m := newTestMgr()
	file := filepath.Join(t.TempDir(), "source.json")
	m.cfg.SourceFile = file
	m.cfg.IdleDelay = 10 * time.Second
	m.cfg.SessionGapThreshold = 45 * time.Second
	m.physicalSource = "None"
	m.lastPhysicalAt = time.Now().Add(-15 * time.Second)
	m.physicalStartedAt = time.Now().Add(-1 * time.Minute)
	m.recognitionResult = &RecognitionResult{Title: "Old Track", Artist: "Old Artist", Format: "Vinyl"}

	if err := os.WriteFile(file, []byte(`{"source":"Physical"}`), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	beforeStartedAt := m.physicalStartedAt
	m.pollSourceFile()

	if m.physicalSource != "Physical" {
		t.Fatalf("physicalSource = %q, want Physical", m.physicalSource)
	}
	if m.recognitionResult == nil || m.recognitionResult.Title != "Old Track" {
		t.Fatal("recognitionResult should be preserved when resuming within session gap threshold")
	}
	if !m.physicalStartedAt.After(beforeStartedAt) {
		t.Fatal("physicalStartedAt should reset on resume after idle")
	}
	select {
	case trig := <-m.recognizeTrigger:
		if trig.isBoundary {
			t.Fatal("resume-after-idle trigger should not be marked as boundary")
		}
	default:
		t.Fatal("expected recognition trigger on physical resume after idle")
	}
}
