package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNoiseFloorRejectsTooLowRMSThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noise-floor.json")

	// RMS threshold = RMS + StdDev*4 = 0.0016 + 0.0001*4 = 0.0020 (too low)
	nf := NoiseFloor{
		RMS:    0.0016,
		StdDev: 0.0001,
	}
	data, err := json.Marshal(nf)
	if err != nil {
		t.Fatalf("marshal noise floor: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write noise floor: %v", err)
	}

	got, ok := loadNoiseFloor(path)
	if ok {
		t.Fatalf("expected loadNoiseFloor to reject too-low rms threshold")
	}

	def := defaultNoiseFloor()
	if got.RMS != def.RMS || got.StdDev != def.StdDev {
		t.Fatalf("expected default noise floor %+v, got %+v", def, got)
	}
}
