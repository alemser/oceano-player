package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"
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
		cfg.RecognitionCaptureAutoGain = defaultRecognitionCaptureAutoGainConfig()
		cfg.Discogs = defaultConfig().Discogs
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("recognizer: cannot read %s for recognition.providers: %v", path, err)
		cfg.RecognitionProviders = nil
		cfg.RecognitionMergePolicy = ""
		cfg.RecognitionCaptureAutoGain = defaultRecognitionCaptureAutoGainConfig()
		cfg.Discogs = defaultConfig().Discogs
		return
	}
	var top struct {
		Recognition *struct {
			Providers               []RecognitionProviderSpec       `json:"providers"`
			MergePolicy             string                          `json:"merge_policy"`
			ShazamRecognizerEnabled *bool                           `json:"shazam_recognizer_enabled"`
			CaptureAutoGain         RecognitionCaptureAutoGainConfig `json:"capture_auto_gain"`
			Discogs                 struct {
				Enabled       bool   `json:"enabled"`
				Token         string `json:"token"`
				TimeoutSecs   int    `json:"timeout_secs"`
				MaxRetries    int    `json:"max_retries"`
				CacheTTLHours int    `json:"cache_ttl_hours"`
			} `json:"discogs"`
		} `json:"recognition"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		log.Printf("recognizer: parse %s: %v", path, err)
		cfg.RecognitionProviders = nil
		cfg.RecognitionMergePolicy = ""
		cfg.RecognitionCaptureAutoGain = defaultRecognitionCaptureAutoGainConfig()
		cfg.Discogs = defaultConfig().Discogs
		return
	}
	if top.Recognition == nil {
		cfg.RecognitionProviders = nil
		cfg.RecognitionMergePolicy = ""
		cfg.RecognitionCaptureAutoGain = defaultRecognitionCaptureAutoGainConfig()
		cfg.Discogs = defaultConfig().Discogs
		return
	}
	mp := strings.TrimSpace(top.Recognition.MergePolicy)
	if mp == "" {
		mp = "first_success"
	}
	cfg.RecognitionMergePolicy = mp
	cfg.RecognitionProviders = append([]RecognitionProviderSpec(nil), top.Recognition.Providers...)
	cfg.RecognitionCaptureAutoGain = normalizeRecognitionCaptureAutoGainConfig(top.Recognition.CaptureAutoGain)
	cfg.Discogs = normalizeDiscogsConfig(top.Recognition.Discogs)

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

func normalizeDiscogsConfig(raw struct {
	Enabled       bool   `json:"enabled"`
	Token         string `json:"token"`
	TimeoutSecs   int    `json:"timeout_secs"`
	MaxRetries    int    `json:"max_retries"`
	CacheTTLHours int    `json:"cache_ttl_hours"`
}) DiscogsConfig {
	def := defaultConfig().Discogs
	out := DiscogsConfig{
		Enabled:    raw.Enabled,
		Token:      strings.TrimSpace(raw.Token),
		MaxRetries: raw.MaxRetries,
	}
	if raw.TimeoutSecs <= 0 {
		out.Timeout = def.Timeout
	} else {
		out.Timeout = time.Duration(raw.TimeoutSecs) * time.Second
	}
	if out.MaxRetries <= 0 {
		out.MaxRetries = def.MaxRetries
	}
	if raw.CacheTTLHours <= 0 {
		out.CacheTTL = def.CacheTTL
	} else {
		out.CacheTTL = time.Duration(raw.CacheTTLHours) * time.Hour
	}
	return out
}
