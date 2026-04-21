package main

import (
	"testing"
	"time"
)

func TestVUBoundaryDetector_HysteresisStabilizesSilenceState(t *testing.T) {
	cfg := defaultVUBoundaryDetectorConfig(0.01, 2, 2)
	cfg.silenceEnterThreshold = 0.010
	cfg.silenceExitThreshold = 0.012
	d := newVUBoundaryDetector(cfg)
	now := time.Now()

	if out := d.Feed(0.0098, now); out.enteredSilence {
		t.Fatal("unexpected silence enter on first frame")
	}
	if out := d.Feed(0.0097, now.Add(50*time.Millisecond)); !out.enteredSilence {
		t.Fatal("expected silence enter after sustained low frames")
	}
	// Still below exit threshold: remain in silence.
	if out := d.Feed(0.0110, now.Add(100*time.Millisecond)); out.resumedFromSilence {
		t.Fatal("unexpected resume while still below silence exit threshold")
	}
	// Needs two active frames above exit threshold to resume.
	if out := d.Feed(0.0125, now.Add(150*time.Millisecond)); out.resumedFromSilence {
		t.Fatal("unexpected resume on first active frame")
	}
	if out := d.Feed(0.0128, now.Add(200*time.Millisecond)); !out.resumedFromSilence {
		t.Fatal("expected resume after sustained active frames")
	}
}

func TestVUBoundaryDetector_CalibratedTransitionTriggersEnergyBoundary(t *testing.T) {
	cfg := defaultVUBoundaryDetectorConfig(0.0095, 2, 2)
	cfg.energyWarmupFrames = 1
	cfg.energyChangeCooldown = 0
	cfg.energySlowAlpha = 0.05
	cfg.energyFastAlpha = 0.80
	cfg.energyDipRatio = 0.90
	cfg.energyRecoverRatio = 0.95
	cfg.energyDipMinFrames = 3
	cfg.energyDipMaxFrames = 20
	cfg.transitionGapRMS = 0.040
	cfg.transitionMinMusicRMS = 0.055
	d := newVUBoundaryDetector(cfg)
	now := time.Now()

	frames := []float32{0.080, 0.078, 0.076, 0.043, 0.042, 0.041, 0.040, 0.082}
	var boundary bool
	for i, v := range frames {
		out := d.Feed(v, now.Add(time.Duration(i)*50*time.Millisecond))
		if out.boundary && out.boundaryType == "energy-change" {
			boundary = true
		}
	}
	if !boundary {
		t.Fatal("expected calibrated energy-change boundary trigger")
	}
}

func TestVUBoundaryDetector_RejectsOverlongDipAsQuietPassage(t *testing.T) {
	cfg := defaultVUBoundaryDetectorConfig(0.0095, 2, 2)
	cfg.energyWarmupFrames = 1
	cfg.energyChangeCooldown = 0
	cfg.energySlowAlpha = 0.01
	cfg.energyFastAlpha = 1.00
	cfg.energyDipRatio = 0.95
	cfg.energyRecoverRatio = 0.95
	cfg.energyDipMinFrames = 3
	cfg.energyDipMaxFrames = 5
	cfg.transitionGapRMS = 0.040
	cfg.transitionMinMusicRMS = 0.055
	d := newVUBoundaryDetector(cfg)
	now := time.Now()

	frames := []float32{0.080, 0.078, 0.076, 0.038, 0.038, 0.038, 0.038, 0.038, 0.038, 0.085}
	for i, v := range frames {
		out := d.Feed(v, now.Add(time.Duration(i)*50*time.Millisecond))
		if out.boundary {
			t.Fatal("did not expect boundary for overlong low-energy passage")
		}
	}
}
