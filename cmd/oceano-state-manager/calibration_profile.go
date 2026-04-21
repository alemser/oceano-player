package main

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

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

type calibrationConfigSnapshot struct {
	Advanced struct {
		CalibrationProfiles map[string]calibrationProfile `json:"calibration_profiles"`
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

func loadBoundaryCalibrationModel(path string, fallbackSilenceThreshold float32, preferredMediaFormat string) boundaryCalibrationModel {
	model := boundaryCalibrationModel{
		enterSilenceThreshold: fallbackSilenceThreshold,
		exitSilenceThreshold:  fallbackSilenceThreshold,
	}
	if path == "" {
		return model
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return model
	}

	var snap calibrationConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return model
	}
	if len(snap.Advanced.CalibrationProfiles) == 0 {
		return model
	}

	profileID, profile, ok := chooseCalibrationProfile(snap, preferredMediaFormat)
	if !ok {
		return model
	}
	model.profileID = profileID

	if profile.Off != nil && profile.On != nil {
		off := float32(profile.Off.AvgRMS)
		on := float32(profile.On.AvgRMS)
		if on > off && off > 0 {
			gap := on - off
			enter := off + gap*0.35
			exit := off + gap*0.55
			if enter < 0.001 {
				enter = 0.001
			}
			if exit <= enter {
				exit = enter + 0.0005
			}
			model.enterSilenceThreshold = enter
			model.exitSilenceThreshold = exit
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

	return model
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
