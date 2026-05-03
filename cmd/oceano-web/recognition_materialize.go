package main

import "strings"

// materializeRecognitionProvidersIfEmpty normalizes recognition.providers and
// merge_policy for persistence. It does not synthesize providers from
// recognizer_chain (deprecated for runtime ordering); operators must configure
// recognition.providers explicitly (e.g. via oceano-player-ios or POST /api/config).
func materializeRecognitionProvidersIfEmpty(rec *RecognitionConfig) {
	if rec == nil {
		return
	}
	if len(rec.Providers) > 0 {
		if strings.TrimSpace(rec.MergePolicy) == "" {
			rec.MergePolicy = "first_success"
		}
		return
	}
	if rec.Providers == nil {
		rec.Providers = []RecognitionProviderConfig{}
	}
	if strings.TrimSpace(rec.MergePolicy) == "" {
		rec.MergePolicy = "first_success"
	}
}

func normalizeRecognizerChainValue(raw string) string {
	switch strings.TrimSpace(raw) {
	case "acrcloud_first", "shazam_first", "acrcloud_only", "shazam_only",
		"audd_first", "audd_only":
		return strings.TrimSpace(raw)
	case "":
		return "acrcloud_first"
	default:
		return "acrcloud_first"
	}
}

// buildRecognitionProvidersFromLegacyChain maps recognizer_chain + credential
// fields to an explicit provider list. Used by tests and tooling; runtime
// recognition is driven only by recognition.providers in config.json.
func buildRecognitionProvidersFromLegacyChain(chain string, rec *RecognitionConfig) []RecognitionProviderConfig {
	hasACR := strings.TrimSpace(rec.ACRCloudHost) != "" &&
		strings.TrimSpace(rec.ACRCloudAccessKey) != "" &&
		strings.TrimSpace(rec.ACRCloudSecretKey) != ""
	hasAudD := strings.TrimSpace(rec.AudDAPIToken) != ""
	shazamOn := rec.ShazamioRecognizerEnabled

	ids := providerIDsForChain(chain)
	out := make([]RecognitionProviderConfig, 0, len(ids))
	for _, id := range ids {
		en := false
		switch id {
		case "acrcloud":
			en = hasACR
		case "audd":
			en = hasAudD
		case "shazam":
			en = shazamOn
		}
		out = append(out, RecognitionProviderConfig{
			ID:      id,
			Enabled: en,
			Roles:   []string{"primary"},
		})
	}
	return out
}

func providerIDsForChain(chain string) []string {
	switch chain {
	case "shazam_first":
		return []string{"shazam", "acrcloud", "audd"}
	case "acrcloud_only":
		return []string{"acrcloud"}
	case "shazam_only":
		return []string{"shazam"}
	case "audd_only":
		return []string{"audd"}
	case "audd_first":
		return []string{"audd", "acrcloud", "shazam"}
	default: // acrcloud_first
		return []string{"acrcloud", "audd", "shazam"}
	}
}
