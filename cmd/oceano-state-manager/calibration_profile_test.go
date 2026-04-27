package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBoundaryCalibrationModel_UsesLastKnownInputProfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	json := `{
	  "advanced": {
	    "calibration_profiles": {
	      "20": {
	        "off": {"avg_rms": 0.0100},
	        "on": {"avg_rms": 0.0200}
	      },
	      "30": {
	        "off": {"avg_rms": 0.0050},
	        "on": {"avg_rms": 0.0120},
	        "vinyl_transition": {
	          "tail_avg_rms": 0.055,
	          "gap_avg_rms": 0.040,
	          "attack_avg_rms": 0.080,
	          "gap_duration_secs": 2.5,
	          "samples_per_sec": 21.5
	        }
	      }
	    }
	  },
	  "amplifier_runtime": {
	    "last_known_input_id": "20"
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(json), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	model := loadBoundaryCalibrationModel(cfgPath, 0.0095, "")
	if model.profileID != "20" {
		t.Fatalf("profileID = %q, want 20", model.profileID)
	}
	if model.enterSilenceThreshold <= 0.0100 || model.exitSilenceThreshold <= model.enterSilenceThreshold {
		t.Fatalf("unexpected thresholds enter=%.4f exit=%.4f", model.enterSilenceThreshold, model.exitSilenceThreshold)
	}
	if model.transitionGapRMS != 0 {
		t.Fatalf("expected no transition model for profile 20, got %.4f", model.transitionGapRMS)
	}
}

func TestLoadBoundaryCalibrationModel_FallsBackToVinylProfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	json := `{
	  "advanced": {
	    "calibration_profiles": {
	      "20": {
	        "off": {"avg_rms": 0.0100},
	        "on": {"avg_rms": 0.0200}
	      },
	      "30": {
	        "off": {"avg_rms": 0.0050},
	        "on": {"avg_rms": 0.0120},
	        "vinyl_transition": {
	          "tail_avg_rms": 0.055,
	          "gap_avg_rms": 0.040,
	          "attack_avg_rms": 0.080,
	          "gap_duration_secs": 2.5,
	          "samples_per_sec": 21.5
	        }
	      }
	    }
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(json), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	model := loadBoundaryCalibrationModel(cfgPath, 0.0095, "")
	if model.profileID != "30" {
		t.Fatalf("profileID = %q, want 30", model.profileID)
	}
	if model.transitionGapRMS <= 0 {
		t.Fatal("expected transition gap RMS from vinyl profile")
	}
}

func TestLoadBoundaryCalibrationModel_PrefersMediaFormatOverLastKnownInput(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	json := `{
	  "advanced": {
	    "calibration_profiles": {
	      "10": {
	        "off": {"avg_rms": 0.0060},
	        "on": {"avg_rms": 0.0120},
	        "vinyl_transition": {
	          "tail_avg_rms": 0.055,
	          "gap_avg_rms": 0.040,
	          "attack_avg_rms": 0.080,
	          "gap_duration_secs": 2.5,
	          "samples_per_sec": 21.5
	        }
	      },
	      "20": {
	        "off": {"avg_rms": 0.0100},
	        "on": {"avg_rms": 0.0200}
	      }
	    }
	  },
	  "amplifier": {
	    "inputs": [
	      {"id":"10", "logical_name":"Phono"},
	      {"id":"20", "logical_name":"CD"}
	    ]
	  },
	  "amplifier_runtime": {
	    "last_known_input_id": "20"
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(json), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	model := loadBoundaryCalibrationModel(cfgPath, 0.0095, "Vinyl")
	if model.profileID != "10" {
		t.Fatalf("profileID = %q, want 10 for vinyl", model.profileID)
	}
	if model.transitionGapRMS <= 0 {
		t.Fatal("expected vinyl transition data for vinyl preference")
	}
}

func TestLoadBoundaryCalibrationModel_CDFormatFallsBackToInputSelection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	json := `{
	  "advanced": {
	    "calibration_profiles": {
	      "20": {
	        "off": {"avg_rms": 0.0100},
	        "on": {"avg_rms": 0.0200}
	      },
	      "30": {
	        "off": {"avg_rms": 0.0060},
	        "on": {"avg_rms": 0.0120},
	        "vinyl_transition": {
	          "tail_avg_rms": 0.055,
	          "gap_avg_rms": 0.040,
	          "attack_avg_rms": 0.080,
	          "gap_duration_secs": 2.5,
	          "samples_per_sec": 21.5
	        }
	      }
	    }
	  },
	  "amplifier": {
	    "inputs": [
	      {"id":"20", "logical_name":"CD"},
	      {"id":"30", "logical_name":"Phono"}
	    ]
	  },
	  "amplifier_runtime": {
	    "last_known_input_id": "30"
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(json), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	model := loadBoundaryCalibrationModel(cfgPath, 0.0095, "CD")
	if model.profileID != "20" {
		t.Fatalf("profileID = %q, want 20 for CD", model.profileID)
	}
}

func TestLoadBoundaryCalibrationModel_ClampsDerivedThresholdsToGlobalFloor(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	// Gap above R2c ε but profile-derived enter/exit still below global floor (R2b clamp).
	json := `{
	  "advanced": {
	    "calibration_profiles": {
	      "20": {
	        "off": {"avg_rms": 0.0180},
	        "on": {"avg_rms": 0.0210}
	      }
	    }
	  },
	  "amplifier_runtime": {
	    "last_known_input_id": "20"
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(json), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	const floor float32 = 0.0220
	model := loadBoundaryCalibrationModel(cfgPath, floor, "")
	if model.enterSilenceThreshold < floor {
		t.Fatalf("enter=%f should be >= floor %f", model.enterSilenceThreshold, floor)
	}
	if model.exitSilenceThreshold < floor {
		t.Fatalf("exit=%f should be >= floor %f", model.exitSilenceThreshold, floor)
	}
	if model.exitSilenceThreshold <= model.enterSilenceThreshold {
		t.Fatalf("exit=%f must be > enter=%f after clamp", model.exitSilenceThreshold, model.enterSilenceThreshold)
	}
}

func TestClampSilenceThresholdsToFloor_NoOpWhenDerivedAboveFloor(t *testing.T) {
	e, x := clampSilenceThresholdsToFloor(0.015, 0.016, 0.0095)
	if e != 0.015 || x != 0.016 {
		t.Fatalf("unexpected clamp: enter=%f exit=%f", e, x)
	}
}

func TestLoadBoundaryCalibrationModel_SkipsDerivedThresholdsWhenOffOnGapTooSmall(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	json := `{
	  "advanced": {
	    "calibration_profiles": {
	      "20": {
	        "off": {"avg_rms": 0.0100},
	        "on": {"avg_rms": 0.0110}
	      }
	    }
	  },
	  "amplifier_runtime": {
	    "last_known_input_id": "20"
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(json), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	const fallback float32 = 0.0095
	model := loadBoundaryCalibrationModel(cfgPath, fallback, "")
	if model.profileID != "20" {
		t.Fatalf("profileID = %q, want 20", model.profileID)
	}
	if model.enterSilenceThreshold != fallback {
		t.Fatalf("enter=%f want %f (profile-derived skipped)", model.enterSilenceThreshold, fallback)
	}
	const minHysteresisGap float32 = 0.0005
	if model.exitSilenceThreshold != fallback+minHysteresisGap {
		t.Fatalf("exit=%f want %f after hysteresis preserve", model.exitSilenceThreshold, fallback+minHysteresisGap)
	}
}
