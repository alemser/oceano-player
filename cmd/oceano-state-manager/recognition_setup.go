package main

import (
	"log"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

func normalizeRecognizerChain(raw string) string {
	switch raw {
	case "acrcloud_first", "shazam_first", "acrcloud_only", "shazam_only",
		"audd_first", "audd_only":
		return raw
	case "":
		return "acrcloud_first"
	default:
		log.Printf("recognizer: unknown recognizer chain %q — falling back to acrcloud_first", raw)
		return "acrcloud_first"
	}
}

func appendRecognizers(dst []Recognizer, parts ...Recognizer) []Recognizer {
	for _, r := range parts {
		if r != nil {
			dst = append(dst, r)
		}
	}
	return dst
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
	chain      Recognizer
	confirmer  Recognizer
	continuity Recognizer
}

func newRecognitionComponents(plan RecognitionPlan) recognitionComponents {
	return recognitionComponents{
		chain:      NewChainRecognizer(plan.Ordered...),
		confirmer:  plan.Confirmer,
		continuity: plan.Continuity,
	}
}

func buildRecognitionComponents(cfg Config, lib *internallibrary.Library) recognitionComponents {
	var acrRec Recognizer
	if cfg.ACRCloudHost != "" && cfg.ACRCloudAccessKey != "" && cfg.ACRCloudSecretKey != "" {
		acrRec = wrapWithStats(NewACRCloudRecognizer(ACRCloudConfig{
			Host:      cfg.ACRCloudHost,
			AccessKey: cfg.ACRCloudAccessKey,
			SecretKey: cfg.ACRCloudSecretKey,
		}), lib)
		log.Printf("recognizer: ACRCloud enabled (host=%s)", cfg.ACRCloudHost)
	}

	var auddRec Recognizer
	if r := NewAudDRecognizer(AudDConfig{APIToken: cfg.AudDAPIToken}); r != nil {
		auddRec = wrapWithStats(r, lib)
		log.Printf("recognizer: AudD enabled (documented API)")
	}

	var shazamRec Recognizer          // used in the chain
	var shazamContinuityRec Recognizer // used by the continuity monitor
	if cfg.ShazamPythonBin != "" {
		if s, err := NewShazamRecognizer(cfg.ShazamPythonBin); err != nil {
			log.Printf("recognizer: Shazam unavailable — %v", err)
		} else {
			shazamRec = wrapWithStats(s, lib)
			shazamContinuityRec = wrapWithStatsAs(s, lib, "ShazamContinuity")
			log.Printf("recognizer: Shazam enabled (python=%s)", cfg.ShazamPythonBin)
		}
	}

	chain := normalizeRecognizerChain(cfg.RecognizerChain)
	log.Printf("recognizer: chain policy=%s", chain)

	var ordered []Recognizer
	switch chain {
	case "shazam_first":
		ordered = appendRecognizers(ordered, shazamRec, acrRec, auddRec)
	case "acrcloud_only":
		ordered = appendRecognizers(ordered, acrRec)
	case "shazam_only":
		ordered = appendRecognizers(ordered, shazamRec)
	case "audd_only":
		ordered = appendRecognizers(ordered, auddRec)
	case "audd_first":
		ordered = appendRecognizers(ordered, auddRec, acrRec, shazamRec)
	default: // "acrcloud_first"
		ordered = appendRecognizers(ordered, acrRec, auddRec, shazamRec)
	}

	if len(ordered) == 0 {
		log.Printf("recognizer: chain policy=%s resolved to no available providers — recognition disabled", chain)
	}

	var confirmer Recognizer
	if len(ordered) == 2 {
		confirmer = ordered[1]
	}

	plan := RecognitionPlan{
		Ordered:    ordered,
		Confirmer:  confirmer,
		Continuity: shazamContinuityRec,
	}

	return newRecognitionComponents(plan)
}
