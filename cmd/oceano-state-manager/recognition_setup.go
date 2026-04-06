package main

import "log"

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

func buildRecognitionComponents(cfg Config) recognitionComponents {
	// Always try to create both recognizers so continuity can always use Shazam
	// regardless of which providers are in the identification chain.
	var acrRec Recognizer
	if cfg.ACRCloudHost != "" && cfg.ACRCloudAccessKey != "" && cfg.ACRCloudSecretKey != "" {
		acrRec = NewACRCloudRecognizer(ACRCloudConfig{
			Host:      cfg.ACRCloudHost,
			AccessKey: cfg.ACRCloudAccessKey,
			SecretKey: cfg.ACRCloudSecretKey,
		})
		log.Printf("recognizer: ACRCloud enabled (host=%s)", cfg.ACRCloudHost)
	}

	var shazamRec Recognizer
	if cfg.ShazamPythonBin != "" {
		if s := NewShazamRecognizer(cfg.ShazamPythonBin); s != nil {
			shazamRec = s
			log.Printf("recognizer: Shazam enabled (python=%s)", cfg.ShazamPythonBin)
		} else {
			log.Printf("recognizer: Shazam unavailable — %s not found or shazamio not installed", cfg.ShazamPythonBin)
		}
	}

	// Build chain order from the configured policy.
	var ordered []Recognizer
	switch cfg.RecognizerChain {
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
		ordered = append(ordered, localOnlyRecognizer{})
	default: // "acrcloud_first" or unset
		if acrRec != nil {
			ordered = append(ordered, acrRec)
		}
		if shazamRec != nil {
			ordered = append(ordered, shazamRec)
		}
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
		Continuity: shazamRec, // always Shazam for continuity — independent of chain setting
	}

	return newRecognitionComponents(plan, newFingerprinter())
}
