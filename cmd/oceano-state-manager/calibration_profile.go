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
	Enabled                        bool     `json:"enabled"`
	LookbackDays                   *int     `json:"lookback_days,omitempty"`
	MinFollowupPairs               *int     `json:"min_followup_pairs,omitempty"`
	BaselineFalsePositiveRatio     *float64 `json:"baseline_false_positive_ratio,omitempty"`
	MaxSilenceThresholdDelta       *float64 `json:"max_silence_threshold_delta,omitempty"`
	MaxDurationPessimismDelta      *float64 `json:"max_duration_pessimism_delta,omitempty"`
	EarlyTrackProgressP75Threshold *float64 `json:"early_track_progress_p75_threshold,omitempty"`
	EarlyTrackExtraSilenceDelta    *float64 `json:"early_track_extra_silence_delta,omitempty"`
}

type autonomousCalibrationJSON struct {
	Enabled bool `json:"enabled"`
}

type rmsPercentileLearningJSON struct {
	Enabled               *bool    `json:"enabled,omitempty"`
	AutonomousApply       bool     `json:"autonomous_apply"`
	MinSilenceSamples     *int     `json:"min_silence_samples,omitempty"`
	MinMusicSamples       *int     `json:"min_music_samples,omitempty"`
	PersistIntervalSecs   *int     `json:"persist_interval_secs,omitempty"`
	HistogramBins         *int     `json:"histogram_bins,omitempty"`
	HistogramMaxRMS       *float64 `json:"histogram_max_rms,omitempty"`
}

type calibrationConfigSnapshot struct {
	Advanced struct {
		CalibrationProfiles     map[string]calibrationProfile   `json:"calibration_profiles"`
		TelemetryNudges         *telemetryNudgesJSON            `json:"r3_telemetry_nudges,omitempty"`
		AutonomousCalibration   *autonomousCalibrationJSON      `json:"autonomous_calibration,omitempty"`
		RMSPercentileLearning   *rmsPercentileLearningJSON     `json:"rms_percentile_learning,omitempty"`
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

// rmsLearningRuntimeConfig controls autonomous RMS histogram learning (library DB).
type rmsLearningRuntimeConfig struct {
	Enabled             bool
	AutonomousApply     bool
	MinSilenceSamples   int
	MinMusicSamples     int
	PersistInterval     time.Duration
	Bins                int
	MaxRMS              float32
}

func defaultRMSLearningRuntimeConfig() rmsLearningRuntimeConfig {
	return rmsLearningRuntimeConfig{
		Enabled:           true, // collect by default; AutonomousApply stays false until explicitly set
		MinSilenceSamples: 400,
		MinMusicSamples:   400,
		PersistInterval:   2 * time.Minute,
		Bins:              80,
		MaxRMS:            0.25,
	}
}

func mergeRMSLearningConfig(raw *rmsPercentileLearningJSON) rmsLearningRuntimeConfig {
	out := defaultRMSLearningRuntimeConfig()
	if raw == nil {
		return out
	}
	if raw.Enabled != nil {
		out.Enabled = *raw.Enabled
	}
	out.AutonomousApply = raw.AutonomousApply
	if raw.MinSilenceSamples != nil {
		out.MinSilenceSamples = *raw.MinSilenceSamples
	}
	if raw.MinMusicSamples != nil {
		out.MinMusicSamples = *raw.MinMusicSamples
	}
	if raw.PersistIntervalSecs != nil && *raw.PersistIntervalSecs > 0 {
		out.PersistInterval = time.Duration(*raw.PersistIntervalSecs) * time.Second
	}
	if raw.HistogramBins != nil && *raw.HistogramBins >= 16 && *raw.HistogramBins <= 256 {
		out.Bins = *raw.HistogramBins
	}
	if raw.HistogramMaxRMS != nil && *raw.HistogramMaxRMS > 0.02 && *raw.HistogramMaxRMS <= 0.6 {
		out.MaxRMS = float32(*raw.HistogramMaxRMS)
	}
	if out.MinSilenceSamples < 50 {
		out.MinSilenceSamples = 50
	}
	if out.MinMusicSamples < 50 {
		out.MinMusicSamples = 50
	}
	return out
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
	if raw.LookbackDays != nil {
		out.LookbackDays = *raw.LookbackDays
	}
	if raw.MinFollowupPairs != nil {
		out.MinFollowupPairs = *raw.MinFollowupPairs
	}
	if raw.BaselineFalsePositiveRatio != nil {
		out.BaselineFalsePositiveRatio = *raw.BaselineFalsePositiveRatio
	}
	if raw.MaxSilenceThresholdDelta != nil {
		out.MaxSilenceThresholdDelta = *raw.MaxSilenceThresholdDelta
	}
	if raw.MaxDurationPessimismDelta != nil {
		out.MaxDurationPessimismDelta = *raw.MaxDurationPessimismDelta
	}
	if raw.EarlyTrackProgressP75Threshold != nil {
		out.EarlyTrackProgressP75Threshold = *raw.EarlyTrackProgressP75Threshold
	}
	if raw.EarlyTrackExtraSilenceDelta != nil {
		out.EarlyTrackExtraSilenceDelta = *raw.EarlyTrackExtraSilenceDelta
	}
	return out
}

func loadBoundaryCalibrationModel(path string, fallbackSilenceThreshold float32, preferredMediaFormat string) (boundaryCalibrationModel, telemetryNudgesConfig, rmsLearningRuntimeConfig) {
	telemetryCfg := defaultTelemetryNudgesConfig()
	rmsCfg := defaultRMSLearningRuntimeConfig()
	model := boundaryCalibrationModel{
		enterSilenceThreshold: fallbackSilenceThreshold,
		exitSilenceThreshold:  fallbackSilenceThreshold,
	}
	if path == "" {
		return model, telemetryCfg, rmsCfg
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return model, mergeTelemetryNudgesConfig(nil), mergeRMSLearningConfig(nil)
	}

	var snap calibrationConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return model, mergeTelemetryNudgesConfig(nil), mergeRMSLearningConfig(nil)
	}
	telemetryCfg = mergeTelemetryNudgesConfig(snap.Advanced.TelemetryNudges)
	rmsCfg = mergeRMSLearningConfig(snap.Advanced.RMSPercentileLearning)
	if snap.Advanced.AutonomousCalibration != nil && snap.Advanced.AutonomousCalibration.Enabled {
		telemetryCfg.Enabled = true
	}

	if len(snap.Advanced.CalibrationProfiles) == 0 {
		return model, telemetryCfg, rmsCfg
	}

	profileID, profile, ok := chooseCalibrationProfile(snap, preferredMediaFormat)
	if !ok {
		return model, telemetryCfg, rmsCfg
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
		default:
			log.Printf("calibration profile %s: invalid off/on samples (off=%.6f on=%.6f); using global VU silence thresholds",
				model.profileID, off, on)
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
	return model, telemetryCfg, rmsCfg
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
