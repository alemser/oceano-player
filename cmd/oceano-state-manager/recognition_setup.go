package main

import (
	"log"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

func normalizeRecognizerChain(raw string) string {
	switch raw {
	case "acrcloud_first", "shazam_first", "acrcloud_only", "shazam_only", "fingerprint_only":
		return raw
	case "":
		return "acrcloud_first"
	default:
		log.Printf("recognizer: unknown recognizer chain %q — falling back to acrcloud_first", raw)
		return "acrcloud_first"
	}
}

// RecognitionPlan defines provider order and provider roles independently.
// Ordered controls chain execution order. Confirmer and Continuity can point to
// any recognizer in Ordered, or be nil when the role is disabled.
type RecognitionPlan struct {
	Ordered    []Recognizer
	Confirmer  Recognizer
	Continuity Recognizer
}

type recognitionComponents struct {
	chain       Recognizer
	confirmer   Recognizer
	continuity  Recognizer
	fingerprint Fingerprinter
}

func newRecognitionComponents(plan RecognitionPlan, fingerprinter Fingerprinter) recognitionComponents {
	return recognitionComponents{
		chain:       NewChainRecognizer(plan.Ordered...),
		confirmer:   plan.Confirmer,
		continuity:  plan.Continuity,
		fingerprint: fingerprinter,
	}
}

func buildRecognitionComponents(cfg Config, lib *internallibrary.Library) recognitionComponents {
	// Always try to create both recognizers so continuity can always use Shazam
	// regardless of which providers are in the identification chain.
	var acrRec Recognizer
	if cfg.ACRCloudHost != "" && cfg.ACRCloudAccessKey != "" && cfg.ACRCloudSecretKey != "" {
		acrRec = wrapWithStats(NewACRCloudRecognizer(ACRCloudConfig{
			Host:      cfg.ACRCloudHost,
			AccessKey: cfg.ACRCloudAccessKey,
			SecretKey: cfg.ACRCloudSecretKey,
		}), lib)
		log.Printf("recognizer: ACRCloud enabled (host=%s)", cfg.ACRCloudHost)
	}

	// shazamRaw is the unwrapped recognizer shared by both the chain and continuity wrappers.
	// Each wrapper gets its own stats name so chain calls ("Shazam") and continuity polling
	// ("ShazamContinuity") are tracked separately.
	var shazamRec Recognizer          // used in the chain
	var shazamContinuityRec Recognizer // used by the continuity monitor
	if cfg.ShazamPythonBin != "" {
		if s := NewShazamRecognizer(cfg.ShazamPythonBin); s != nil {
			shazamRec = wrapWithStats(s, lib)
			shazamContinuityRec = wrapWithStatsAs(s, lib, "ShazamContinuity")
			log.Printf("recognizer: Shazam enabled (python=%s)", cfg.ShazamPythonBin)
		} else {
			log.Printf("recognizer: Shazam unavailable — %s not found or shazamio not installed", cfg.ShazamPythonBin)
		}
	}

	chain := normalizeRecognizerChain(cfg.RecognizerChain)
	log.Printf("recognizer: chain policy=%s", chain)

	// Build chain order from the configured policy.
	var ordered []Recognizer
	switch chain {
	case "shazam_first":
		if shazamRec != nil {
			ordered = append(ordered, shazamRec)
		}
		if acrRec != nil {
			ordered = append(ordered, acrRec)
		}
	case "acrcloud_only":
		if acrRec != nil {
			ordered = append(ordered, acrRec)
		}
	case "shazam_only":
		if shazamRec != nil {
			ordered = append(ordered, shazamRec)
		}
	case "fingerprint_only":
		ordered = append(ordered, wrapWithStats(localOnlyRecognizer{}, lib))
	default: // "acrcloud_first" or unset
		if acrRec != nil {
			ordered = append(ordered, acrRec)
		}
		if shazamRec != nil {
			ordered = append(ordered, shazamRec)
		}
	}

	if len(ordered) == 0 {
		log.Printf("recognizer: chain policy=%s resolved to no available providers — falling back to local fingerprint-only mode", chain)
		ordered = append(ordered, wrapWithStats(localOnlyRecognizer{}, lib))
	}

	// Confirmer is the secondary provider in the chain — used for cross-provider
	// confirmation when ConfirmationDelay > 0. Single-provider chains fall back
	// to same-provider second call.
	var confirmer Recognizer
	if len(ordered) == 2 {
		confirmer = ordered[1]
	}

	plan := RecognitionPlan{
		Ordered:    ordered,
		Confirmer:  confirmer,
		Continuity: shazamContinuityRec, // always Shazam for continuity — tracked separately from chain calls
	}

	return newRecognitionComponents(plan, newFingerprinter())
}
