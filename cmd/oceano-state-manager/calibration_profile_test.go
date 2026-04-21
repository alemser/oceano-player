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
