package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

func approxEq32(a, b float32) bool {
	return math.Abs(float64(a-b)) < 1e-5
}

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

	model, _, _ := loadBoundaryCalibrationModel(cfgPath, 0.0095, "")
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

func TestLoadBoundaryCalibrationModel_AutonomousCalibrationEnablesTelemetryNudges(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	json := `{
	  "advanced": {
	    "calibration_profiles": {
	      "20": {
	        "off": {"avg_rms": 0.0100},
	        "on": {"avg_rms": 0.0200}
	      }
	    },
	    "r3_telemetry_nudges": { "enabled": false },
	    "autonomous_calibration": { "enabled": true }
	  },
	  "amplifier_runtime": { "last_known_input_id": "20" }
	}`
	if err := os.WriteFile(cfgPath, []byte(json), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, tel, _ := loadBoundaryCalibrationModel(cfgPath, 0.0095, "")
	if !tel.Enabled {
		t.Fatal("expected telemetry nudges enabled when autonomous_calibration.enabled is true")
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

	model, _, _ := loadBoundaryCalibrationModel(cfgPath, 0.0095, "")
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

	model, _, _ := loadBoundaryCalibrationModel(cfgPath, 0.0095, "Vinyl")
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

	model, _, _ := loadBoundaryCalibrationModel(cfgPath, 0.0095, "CD")
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
	model, _, _ := loadBoundaryCalibrationModel(cfgPath, floor, "")
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
	model, _, _ := loadBoundaryCalibrationModel(cfgPath, fallback, "")
	if model.profileID != "20" {
		t.Fatalf("profileID = %q, want 20", model.profileID)
	}
	if model.enterSilenceThreshold != fallback {
		t.Fatalf("enter=%f want %f (profile-derived skipped)", model.enterSilenceThreshold, fallback)
	}
	if model.exitSilenceThreshold != fallback+minSilenceThresholdHysteresisGap {
		t.Fatalf("exit=%f want %f after hysteresis preserve", model.exitSilenceThreshold, fallback+minSilenceThresholdHysteresisGap)
	}
}

func TestDeriveVUThresholdsFromCalibratedOffOn_EpsilonBoundary(t *testing.T) {
	t.Run("gap_below_epsilon_not_derived", func(t *testing.T) {
		_, _, gap, derived := deriveVUThresholdsFromCalibratedOffOn(0.010, 0.011)
		if derived || !approxEq32(gap, 0.001) {
			t.Fatalf("want gap≈0.001 derived=false, got gap=%f derived=%v", gap, derived)
		}
	})
	t.Run("gap_equal_epsilon_derived", func(t *testing.T) {
		enter, exit, gap, derived := deriveVUThresholdsFromCalibratedOffOn(0.010, 0.012)
		if !derived || !approxEq32(gap, minCalibrationOffOnGap) {
			t.Fatalf("want derived at gap≈epsilon, got gap=%f derived=%v", gap, derived)
		}
		if exit <= enter {
			t.Fatalf("exit=%f must exceed enter=%f", exit, enter)
		}
	})
	t.Run("invalid_pair", func(t *testing.T) {
		_, _, _, derived := deriveVUThresholdsFromCalibratedOffOn(0.02, 0.01)
		if derived {
			t.Fatal("want derived=false when on<=off")
		}
	})
}

func TestClampSilenceThresholdsToFloor_Table(t *testing.T) {
	tests := []struct {
		name        string
		enter, exit float32
		floor       float32
		wantEnter   float32
		wantExit    float32
	}{
		{
			name: "both_above_floor_unchanged", enter: 0.015, exit: 0.016, floor: 0.0095,
			wantEnter: 0.015, wantExit: 0.016,
		},
		{
			name: "floor_zero_no_change", enter: 0.01, exit: 0.02, floor: 0,
			wantEnter: 0.01, wantExit: 0.02,
		},
		{
			name: "both_below_floor", enter: 0.01, exit: 0.011, floor: 0.02,
			wantEnter: 0.02, wantExit: 0.0205,
		},
		{
			name: "equal_after_floor_need_hysteresis", enter: 0.015, exit: 0.015, floor: 0.02,
			wantEnter: 0.02, wantExit: 0.0205,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotE, gotX := clampSilenceThresholdsToFloor(tt.enter, tt.exit, tt.floor)
			if !approxEq32(gotE, tt.wantEnter) || !approxEq32(gotX, tt.wantExit) {
				t.Fatalf("clamp(%f,%f,%f) = (%f,%f), want (%f,%f)",
					tt.enter, tt.exit, tt.floor, gotE, gotX, tt.wantEnter, tt.wantExit)
			}
		})
	}
}

func TestLoadBoundaryCalibrationModel_GapEqualEpsilonUsesDerivedThresholds(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	json := `{
	  "advanced": {
	    "calibration_profiles": {
	      "20": {
	        "off": {"avg_rms": 0.0100},
	        "on": {"avg_rms": 0.0120}
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

	wantEnter, wantExit, _, derived := deriveVUThresholdsFromCalibratedOffOn(0.010, 0.012)
	if !derived {
		t.Fatal("fixture must derive")
	}
	model, _, _ := loadBoundaryCalibrationModel(cfgPath, 0.0095, "")
	if !approxEq32(model.enterSilenceThreshold, wantEnter) || !approxEq32(model.exitSilenceThreshold, wantExit) {
		t.Fatalf("enter=%f exit=%f want enter=%f exit=%f",
			model.enterSilenceThreshold, model.exitSilenceThreshold, wantEnter, wantExit)
	}
}

func TestMergeTelemetryNudgesConfig_AllowsExplicitZeroValues(t *testing.T) {
	raw := &telemetryNudgesJSON{
		Enabled:                    true,
		LookbackDays:               intPtr(0),
		MinFollowupPairs:           intPtr(0),
		BaselineFalsePositiveRatio: floatPtr(0),
		MaxSilenceThresholdDelta:   floatPtr(0),
		MaxDurationPessimismDelta:  floatPtr(0),
	}
	cfg := mergeTelemetryNudgesConfig(raw)
	if !cfg.Enabled {
		t.Fatal("enabled should be true")
	}
	if cfg.LookbackDays != 0 || cfg.MinFollowupPairs != 0 {
		t.Fatalf("expected explicit zero ints to be preserved, got lookback=%d minPairs=%d", cfg.LookbackDays, cfg.MinFollowupPairs)
	}
	if cfg.BaselineFalsePositiveRatio != 0 || cfg.MaxSilenceThresholdDelta != 0 || cfg.MaxDurationPessimismDelta != 0 {
		t.Fatalf("expected explicit zero floats to be preserved, got baseline=%f maxSil=%f maxPess=%f",
			cfg.BaselineFalsePositiveRatio, cfg.MaxSilenceThresholdDelta, cfg.MaxDurationPessimismDelta)
	}
}
