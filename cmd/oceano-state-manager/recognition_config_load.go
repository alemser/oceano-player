package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"
)

// recognitionConfigFragment is the minimal JSON shape read from CalibrationConfigPath
// to load recognition.providers / merge_policy (explicit provider list).
type recognitionConfigFragment struct {
	Recognition struct {
		Providers   []RecognitionProviderSpec `json:"providers"`
		MergePolicy string                    `json:"merge_policy"`
	} `json:"recognition"`
}

// applyRecognitionProvidersFromConfigFile populates cfg.RecognitionProviders and
// cfg.RecognitionMergePolicy when recognition.providers is a non-empty array.
// On read/parse errors or absent providers, it leaves those fields zero and the
// legacy RecognizerChain path applies.
func applyRecognitionProvidersFromConfigFile(cfg *Config) {
	path := strings.TrimSpace(cfg.CalibrationConfigPath)
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("recognizer: recognition.providers not loaded — cannot read %s: %v", path, err)
		return
	}
	var frag recognitionConfigFragment
	if err := json.Unmarshal(data, &frag); err != nil {
		log.Printf("recognizer: recognition.providers not loaded — parse %s: %v", path, err)
		return
	}
	if len(frag.Recognition.Providers) == 0 {
		return
	}
	mp := strings.TrimSpace(frag.Recognition.MergePolicy)
	if mp == "" {
		mp = "first_success"
	}
	cfg.RecognitionMergePolicy = mp
	cfg.RecognitionProviders = frag.Recognition.Providers
}
