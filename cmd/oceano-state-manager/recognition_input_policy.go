package main

import (
	"encoding/json"
	"os"
	"strings"
)

type inputRecognitionPolicy string

const (
	inputRecognitionPolicyAuto        inputRecognitionPolicy = "auto"
	inputRecognitionPolicyLibrary     inputRecognitionPolicy = "library"
	inputRecognitionPolicyDisplayOnly inputRecognitionPolicy = "display_only"
	inputRecognitionPolicyOff         inputRecognitionPolicy = "off"
)

type recognitionInputPolicySnapshot struct {
	Amplifier struct {
		Inputs []struct {
			ID                string `json:"id"`
			LogicalName       string `json:"logical_name"`
			RecognitionPolicy string `json:"recognition_policy"`
		} `json:"inputs"`
		ConnectedDevices []struct {
			InputIDs []string `json:"input_ids"`
			Role     string   `json:"role"`
		} `json:"connected_devices"`
	} `json:"amplifier"`
	AmplifierRuntime struct {
		LastKnownInputID string `json:"last_known_input_id"`
	} `json:"amplifier_runtime"`
}

type resolvedRecognitionPolicy struct {
	Policy            inputRecognitionPolicy
	LastKnownInputID  string
	DerivedBy         string
}

func shouldRunRecognitionForInputPolicy(p inputRecognitionPolicy) bool {
	return p != inputRecognitionPolicyOff
}

func shouldPersistRecognitionForInputPolicy(p inputRecognitionPolicy) bool {
	return p == inputRecognitionPolicyLibrary
}

func normalizeInputRecognitionPolicy(v string) inputRecognitionPolicy {
	p := strings.ToLower(strings.TrimSpace(v))
	switch p {
	case string(inputRecognitionPolicyLibrary):
		return inputRecognitionPolicyLibrary
	case string(inputRecognitionPolicyDisplayOnly):
		return inputRecognitionPolicyDisplayOnly
	case string(inputRecognitionPolicyOff):
		return inputRecognitionPolicyOff
	default:
		return inputRecognitionPolicyAuto
	}
}

func normalizeConnectedRole(v string) string {
	role := strings.ToLower(strings.TrimSpace(v))
	switch role {
	case "streaming", "other":
		return role
	default:
		return "physical_media"
	}
}

func labelLooksPhysicalMedia(label string) bool {
	s := strings.ToLower(strings.TrimSpace(label))
	// Accept "vinil" for Portuguese user labels.
	return strings.Contains(s, "phono") || strings.Contains(s, "vinyl") || strings.Contains(s, "vinil") || strings.Contains(s, "cd")
}

func resolveRecognitionPolicyFromSnapshot(snap recognitionInputPolicySnapshot) resolvedRecognitionPolicy {
	lastID := strings.TrimSpace(snap.AmplifierRuntime.LastKnownInputID)
	out := resolvedRecognitionPolicy{
		Policy:           inputRecognitionPolicyOff,
		LastKnownInputID: lastID,
		DerivedBy:        "default_off",
	}
	if lastID == "" {
		return out
	}

	var inputPolicy inputRecognitionPolicy = inputRecognitionPolicyAuto
	var inputLabel string
	for _, in := range snap.Amplifier.Inputs {
		if strings.TrimSpace(in.ID) != lastID {
			continue
		}
		inputPolicy = normalizeInputRecognitionPolicy(in.RecognitionPolicy)
		inputLabel = in.LogicalName
		break
	}
	if inputPolicy == inputRecognitionPolicyLibrary || inputPolicy == inputRecognitionPolicyDisplayOnly || inputPolicy == inputRecognitionPolicyOff {
		out.Policy = inputPolicy
		out.DerivedBy = "explicit_input_policy"
		return out
	}

	if labelLooksPhysicalMedia(inputLabel) {
		out.Policy = inputRecognitionPolicyLibrary
		out.DerivedBy = "auto_physical_label"
		return out
	}

	for _, d := range snap.Amplifier.ConnectedDevices {
		if normalizeConnectedRole(d.Role) != "physical_media" {
			continue
		}
		for _, id := range d.InputIDs {
			if strings.TrimSpace(id) == lastID {
				out.Policy = inputRecognitionPolicyLibrary
				out.DerivedBy = "auto_physical_device_role"
				return out
			}
		}
	}
	return out
}

func resolveRecognitionPolicyFromConfigPath(path string) resolvedRecognitionPolicy {
	if strings.TrimSpace(path) == "" {
		return resolvedRecognitionPolicy{Policy: inputRecognitionPolicyOff, DerivedBy: "default_off"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return resolvedRecognitionPolicy{Policy: inputRecognitionPolicyOff, DerivedBy: "default_off"}
	}
	var snap recognitionInputPolicySnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return resolvedRecognitionPolicy{Policy: inputRecognitionPolicyOff, DerivedBy: "default_off"}
	}
	return resolveRecognitionPolicyFromSnapshot(snap)
}
