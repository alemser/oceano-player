package main

import (
	"path/filepath"
	"testing"

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
