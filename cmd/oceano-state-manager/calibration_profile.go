package main

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

// minCalibrationOffOnGap is the minimum discriminative delta between calibrated
// "off" and "on" RMS samples. Smaller gaps hug the noise floor and are treated
// as invalid for profile-derived VU silence thresholds (R2c).
const minCalibrationOffOnGap = float32(0.002)

// minSilenceThresholdHysteresisGap separates enter vs exit after floor clamp or
// when raw derived exit would equal enter (must match clamp behavior).
const minSilenceThresholdHysteresisGap = float32(0.0005)

const minDerivedEnterSilenceRMS = float32(0.001)

type calibrationProfileSample struct {
	AvgRMS float64 `json:"avg_rms"`
}

type calibrationVinylTransition struct {
	TailAvgRMS      float64 `json:"tail_avg_rms"`
	GapAvgRMS       float64 `json:"gap_avg_rms"`
	AttackAvgRMS    float64 `json:"attack_avg_rms"`
	GapDurationSecs float64 `json:"gap_duration_secs"`
	SamplesPerSec   float64 `json:"samples_per_sec"`
}

type calibrationProfile struct {
	Off             *calibrationProfileSample   `json:"off"`
	On              *calibrationProfileSample   `json:"on"`
	VinylTransition *calibrationVinylTransition `json:"vinyl_transition"`
}

// telemetryNudgesJSON mirrors advanced.r3_telemetry_nudges in config.json (R3).
type telemetryNudgesJSON struct {
	Enabled                        bool    `json:"enabled"`
	LookbackDays                   int     `json:"lookback_days"`
	MinFollowupPairs               int     `json:"min_followup_pairs"`
	BaselineFalsePositiveRatio     float64 `json:"baseline_false_positive_ratio"`
	MaxSilenceThresholdDelta       float64 `json:"max_silence_threshold_delta"`
	MaxDurationPessimismDelta      float64 `json:"max_duration_pessimism_delta"`
	EarlyTrackProgressP75Threshold float64 `json:"early_track_progress_p75_threshold"`
	EarlyTrackExtraSilenceDelta    float64 `json:"early_track_extra_silence_delta"`
}

type calibrationConfigSnapshot struct {
	Advanced struct {
		CalibrationProfiles map[string]calibrationProfile `json:"calibration_profiles"`
		TelemetryNudges   *telemetryNudgesJSON        `json:"r3_telemetry_nudges,omitempty"`
	} `json:"advanced"`
	Amplifier struct {
		Inputs []struct {
			ID          string `json:"id"`
			LogicalName string `json:"logical_name"`
		} `json:"inputs"`
	} `json:"amplifier"`
	AmplifierRuntime struct {
		LastKnownInputID string `json:"last_known_input_id"`
	} `json:"amplifier_runtime"`
}

type boundaryCalibrationModel struct {
	profileID string

	enterSilenceThreshold float32
	exitSilenceThreshold  float32

	transitionGapRMS      float32
	transitionMinMusicRMS float32
	transitionGapDuration time.Duration
	transitionSamplesHz   float32
}

// telemetryNudgesConfig is the resolved R3 settings after merging JSON with defaults.
type telemetryNudgesConfig struct {
	Enabled                        bool
	LookbackDays                   int
	MinFollowupPairs               int
	BaselineFalsePositiveRatio     float64
	MaxSilenceThresholdDelta       float64
	MaxDurationPessimismDelta      float64
	EarlyTrackProgressP75Threshold float64
	EarlyTrackExtraSilenceDelta    float64
}

func defaultTelemetryNudgesConfig() telemetryNudgesConfig {
	return telemetryNudgesConfig{
		LookbackDays:                   14,
		MinFollowupPairs:               25,
		BaselineFalsePositiveRatio:     0.10,
		MaxSilenceThresholdDelta:       0.004,
		MaxDurationPessimismDelta:      0.06,
		EarlyTrackProgressP75Threshold: 0.18,
		EarlyTrackExtraSilenceDelta:    0.001,
	}
}

func mergeTelemetryNudgesConfig(raw *telemetryNudgesJSON) telemetryNudgesConfig {
	out := defaultTelemetryNudgesConfig()
	if raw == nil {
		return out
	}
	out.Enabled = raw.Enabled
	if raw.LookbackDays > 0 {
		out.LookbackDays = raw.LookbackDays
	}
	if raw.MinFollowupPairs > 0 {
		out.MinFollowupPairs = raw.MinFollowupPairs
	}
	if raw.BaselineFalsePositiveRatio > 0 {
		out.BaselineFalsePositiveRatio = raw.BaselineFalsePositiveRatio
	}
	if raw.MaxSilenceThresholdDelta > 0 {
		out.MaxSilenceThresholdDelta = raw.MaxSilenceThresholdDelta
	}
	if raw.MaxDurationPessimismDelta > 0 {
		out.MaxDurationPessimismDelta = raw.MaxDurationPessimismDelta
	}
	if raw.EarlyTrackProgressP75Threshold > 0 {
		out.EarlyTrackProgressP75Threshold = raw.EarlyTrackProgressP75Threshold
	}
	if raw.EarlyTrackExtraSilenceDelta > 0 {
		out.EarlyTrackExtraSilenceDelta = raw.EarlyTrackExtraSilenceDelta
	}
	return out
}

func loadBoundaryCalibrationModel(path string, fallbackSilenceThreshold float32, preferredMediaFormat string) (boundaryCalibrationModel, telemetryNudgesConfig) {
	telemetryCfg := defaultTelemetryNudgesConfig()
	model := boundaryCalibrationModel{
		enterSilenceThreshold: fallbackSilenceThreshold,
		exitSilenceThreshold:  fallbackSilenceThreshold,
	}
	if path == "" {
		return model, telemetryCfg
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return model, mergeTelemetryNudgesConfig(nil)
	}

	var snap calibrationConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return model, mergeTelemetryNudgesConfig(nil)
	}
	telemetryCfg = mergeTelemetryNudgesConfig(snap.Advanced.TelemetryNudges)

	if len(snap.Advanced.CalibrationProfiles) == 0 {
		return model, telemetryCfg
	}

	profileID, profile, ok := chooseCalibrationProfile(snap, preferredMediaFormat)
	if !ok {
		return model, telemetryCfg
	}
	model.profileID = profileID

	if profile.Off != nil && profile.On != nil {
		off := float32(profile.Off.AvgRMS)
		on := float32(profile.On.AvgRMS)
		enter, exit, gap, derived := deriveVUThresholdsFromCalibratedOffOn(off, on)
		switch {
		case derived:
			model.enterSilenceThreshold = enter
			model.exitSilenceThreshold = exit
		case on > off && off > 0 && gap < minCalibrationOffOnGap:
			log.Printf("calibration profile %s: off→on gap %.6f below minimum %.6f; using global VU silence thresholds",
				model.profileID, gap, minCalibrationOffOnGap)
		}
	}

	if vt := profile.VinylTransition; vt != nil {
		if vt.GapAvgRMS > 0 {
			model.transitionGapRMS = float32(vt.GapAvgRMS)
		}
		tail := float32(vt.TailAvgRMS)
		attack := float32(vt.AttackAvgRMS)
		if tail > 0 && attack > 0 {
			if tail < attack {
				model.transitionMinMusicRMS = tail
			} else {
				model.transitionMinMusicRMS = attack
			}
		}
		if vt.GapDurationSecs > 0 {
			model.transitionGapDuration = time.Duration(vt.GapDurationSecs * float64(time.Second))
		}
		if vt.SamplesPerSec > 0 {
			model.transitionSamplesHz = float32(vt.SamplesPerSec)
		}
	}

	model.enterSilenceThreshold, model.exitSilenceThreshold = clampSilenceThresholdsToFloor(
		model.enterSilenceThreshold, model.exitSilenceThreshold, fallbackSilenceThreshold,
	)
	return model, telemetryCfg
}

// deriveVUThresholdsFromCalibratedOffOn maps calibrated off/on RMS samples to VU
// silence enter/exit. Returns derived=false when the pair is unusable (invalid
// ordering) or when the gap is below minCalibrationOffOnGap (R2c).
func deriveVUThresholdsFromCalibratedOffOn(off, on float32) (enter, exit, gap float32, derived bool) {
	if !(on > off && off > 0) {
		return 0, 0, 0, false
	}
	gap = on - off
	if gap < minCalibrationOffOnGap {
		return 0, 0, gap, false
	}
	enter = off + gap*0.35
	exit = off + gap*0.55
	if enter < minDerivedEnterSilenceRMS {
		enter = minDerivedEnterSilenceRMS
	}
	if exit <= enter {
		exit = enter + minSilenceThresholdHysteresisGap
	}
	return enter, exit, gap, true
}

// clampSilenceThresholdsToFloor ensures profile-derived VU silence enter/exit never
// sit below advanced.vu_silence_threshold (the fallback passed from the VU monitor).
// Preserves exit > enter after clamping.
func clampSilenceThresholdsToFloor(enter, exit, floor float32) (float32, float32) {
	if floor <= 0 {
		return enter, exit
	}
	if enter < floor {
		enter = floor
	}
	if exit < floor {
		exit = floor
	}
	if exit <= enter {
		exit = enter + minSilenceThresholdHysteresisGap
	}
	return enter, exit
}

func chooseCalibrationProfile(snap calibrationConfigSnapshot, preferredMediaFormat string) (string, calibrationProfile, bool) {
	profiles := snap.Advanced.CalibrationProfiles
	if len(profiles) == 0 {
		return "", calibrationProfile{}, false
	}

	fmtKey := strings.ToLower(strings.TrimSpace(preferredMediaFormat))
	if fmtKey == "cd" || fmtKey == "vinyl" {
		if id, profile, ok := chooseCalibrationProfileByFormat(snap, profiles, fmtKey); ok {
			return id, profile, true
		}
	}

	if id := snap.AmplifierRuntime.LastKnownInputID; id != "" {
		if p, ok := profiles[id]; ok {
			return id, p, true
		}
	}

	ids := make([]string, 0, len(profiles))
	for id := range profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if profiles[id].VinylTransition != nil {
			return id, profiles[id], true
		}
	}
	id := ids[0]
	return id, profiles[id], true
}

func chooseCalibrationProfileByFormat(snap calibrationConfigSnapshot, profiles map[string]calibrationProfile, format string) (string, calibrationProfile, bool) {
	inputIDs := make([]string, 0, len(snap.Amplifier.Inputs))
	for _, in := range snap.Amplifier.Inputs {
		id := strings.TrimSpace(in.ID)
		if id == "" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(in.LogicalName))
		if format == "vinyl" {
			// "vinil" is intentionally accepted to match user-configured input
			// labels in Portuguese while keeping code and docs in English.
			if strings.Contains(name, "phono") || strings.Contains(name, "vinyl") || strings.Contains(name, "vinil") {
				inputIDs = append(inputIDs, id)
			}
			continue
		}
		if strings.Contains(name, "cd") {
			inputIDs = append(inputIDs, id)
		}
	}
	for _, id := range inputIDs {
		if p, ok := profiles[id]; ok {
			return id, p, true
		}
	}

	ids := make([]string, 0, len(profiles))
	for id := range profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if format == "vinyl" {
		for _, id := range ids {
			if profiles[id].VinylTransition != nil {
				return id, profiles[id], true
			}
		}
		return "", calibrationProfile{}, false
	}

	for _, id := range ids {
		if profiles[id].VinylTransition == nil {
			return id, profiles[id], true
		}
	}
	return "", calibrationProfile{}, false
}
