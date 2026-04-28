package main

import (
	"path/filepath"
	"testing"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

func TestClampDurationPessimismScalar(t *testing.T) {
	if clampDurationPessimismScalar(0.2) != 0.5 {
		t.Fatalf("low clamp")
	}
	if clampDurationPessimismScalar(0.85) != 0.85 {
		t.Fatalf("mid")
	}
	if clampDurationPessimismScalar(1.5) != 0.98 {
		t.Fatalf("high clamp")
	}
}

func TestComputeTelemetryCalibrationNudges_Disabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib.db")
	lib, err := internallibrary.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	cfg := defaultTelemetryNudgesConfig()
	s, p, sum := computeTelemetryCalibrationNudges(lib, cfg, "Vinyl")
	if s != 0 || p != 0 || sum != "" {
		t.Fatalf("want zeros when disabled, got silence=%v pess=%v sum=%q", s, p, sum)
	}
	cfg.Enabled = true
	s, p, sum = computeTelemetryCalibrationNudges(lib, cfg, "Vinyl")
	if s != 0 || p != 0 {
		t.Fatalf("empty db should not nudge")
	}
	if sum == "" || sum[:12] != "insufficient" {
		t.Fatalf("expected insufficient pairs message, got %q", sum)
	}
}

func TestComputeTelemetryCalibrationNudges_WithTelemetryData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib.db")
	lib, err := internallibrary.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()

	cfg := defaultTelemetryNudgesConfig()
	cfg.Enabled = true
	cfg.MinFollowupPairs = 20

	cut := time.Now().UTC().Add(-6 * time.Hour).Format(time.RFC3339Nano)
	// 30 pairs total: 15 same_track_restored + 15 matched => fp_ratio=0.5.
	// This should produce non-zero (positive) silence/pessimism nudges.
	for i := 0; i < 15; i++ {
		_, err = lib.DB().Exec(`
			INSERT INTO boundary_events (
				occurred_at, outcome, boundary_type, is_hard, physical_source, format_at_event,
				duration_ms, seek_ms, followup_outcome
			) VALUES (?,?,?,?,?,?,?,?,?)`,
			cut, internallibrary.BoundaryOutcomeFired, "silence->audio", 1, "Physical", "Vinyl",
			200000, 25000, internallibrary.FollowupOutcomeSameTrackRestored,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 15; i++ {
		seek := 30000 + (i * 1500)
		_, err = lib.DB().Exec(`
			INSERT INTO boundary_events (
				occurred_at, outcome, boundary_type, is_hard, physical_source, format_at_event,
				duration_ms, seek_ms, followup_outcome
			) VALUES (?,?,?,?,?,?,?,?,?)`,
			cut, internallibrary.BoundaryOutcomeFired, "silence->audio", 1, "Physical", "Vinyl",
			200000, seek, internallibrary.FollowupOutcomeMatched,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	s, p, summary := computeTelemetryCalibrationNudges(lib, cfg, "Vinyl")
	if s <= 0 {
		t.Fatalf("expected positive silence delta, got %v", s)
	}
	if p <= 0 {
		t.Fatalf("expected positive pessimism delta, got %v", p)
	}
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
}

func TestComputeTelemetryCalibrationNudges_RespectsConfiguredZeroCaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lib.db")
	lib, err := internallibrary.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()

	cfg := defaultTelemetryNudgesConfig()
	cfg.Enabled = true
	cfg.MinFollowupPairs = 1
	cfg.MaxSilenceThresholdDelta = 0
	cfg.MaxDurationPessimismDelta = 0

	cut := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	for i := 0; i < 4; i++ {
		outcome := internallibrary.FollowupOutcomeMatched
		if i%2 == 0 {
			outcome = internallibrary.FollowupOutcomeSameTrackRestored
		}
		_, err = lib.DB().Exec(`
			INSERT INTO boundary_events (
				occurred_at, outcome, boundary_type, is_hard, physical_source, format_at_event,
				duration_ms, seek_ms, followup_outcome
			) VALUES (?,?,?,?,?,?,?,?,?)`,
			cut, internallibrary.BoundaryOutcomeFired, "silence->audio", 1, "Physical", "Vinyl",
			180000, 35000+i*1000, outcome,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	s, p, _ := computeTelemetryCalibrationNudges(lib, cfg, "Vinyl")
	if s != 0 || p != 0 {
		t.Fatalf("configured zero caps should disable nudges, got silence=%v pessimism=%v", s, p)
	}
}
