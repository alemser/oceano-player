package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"
)

// applyRecognitionProvidersFromConfigFile loads cfg.RecognitionProviders and
// cfg.RecognitionMergePolicy from the `recognition` object in CalibrationConfigPath.
//
// Rules:
//   - Missing `recognition` key or unreadable file → RecognitionProviders cleared (nil).
//   - Present `recognition` with empty or omitted `providers` → RecognitionProviders is
//     an empty slice (physical recognition disabled until the operator adds providers).
//   - merge_policy defaults to first_success when recognition is present.
func applyRecognitionProvidersFromConfigFile(cfg *Config) {
	path := strings.TrimSpace(cfg.CalibrationConfigPath)
	if path == "" {
		cfg.RecognitionProviders = nil
		cfg.RecognitionMergePolicy = ""
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("recognizer: cannot read %s for recognition.providers: %v", path, err)
		cfg.RecognitionProviders = nil
		cfg.RecognitionMergePolicy = ""
		return
	}
	var top struct {
		Recognition *struct {
			Providers                 []RecognitionProviderSpec `json:"providers"`
			MergePolicy               string                    `json:"merge_policy"`
			ShazamRecognizerEnabled   *bool                     `json:"shazam_recognizer_enabled"`
		} `json:"recognition"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		log.Printf("recognizer: parse %s: %v", path, err)
		cfg.RecognitionProviders = nil
		cfg.RecognitionMergePolicy = ""
		return
	}
	if top.Recognition == nil {
		cfg.RecognitionProviders = nil
		cfg.RecognitionMergePolicy = ""
		return
	}
	mp := strings.TrimSpace(top.Recognition.MergePolicy)
	if mp == "" {
		mp = "first_success"
	}
	cfg.RecognitionMergePolicy = mp
	cfg.RecognitionProviders = append([]RecognitionProviderSpec(nil), top.Recognition.Providers...)

	// recognition.shazam_recognizer_enabled: when explicitly false, treat shazam as off
	// in the provider list so the subprocess is not started (iOS / web toggle).
	if top.Recognition.ShazamRecognizerEnabled != nil && !*top.Recognition.ShazamRecognizerEnabled {
		for i := range cfg.RecognitionProviders {
			if strings.EqualFold(strings.TrimSpace(cfg.RecognitionProviders[i].ID), "shazam") {
				cfg.RecognitionProviders[i].Enabled = false
			}
		}
	}
}
