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
	var ordered []Recognizer
	var acrRec Recognizer
	if cfg.ACRCloudHost != "" && cfg.ACRCloudAccessKey != "" && cfg.ACRCloudSecretKey != "" {
		acrRec = NewACRCloudRecognizer(ACRCloudConfig{
			Host:      cfg.ACRCloudHost,
			AccessKey: cfg.ACRCloudAccessKey,
			SecretKey: cfg.ACRCloudSecretKey,
		})
		ordered = append(ordered, acrRec)
		log.Printf("recognizer: ACRCloud enabled (host=%s)", cfg.ACRCloudHost)
	}

	var shazamRec Recognizer
	if cfg.ShazamPythonBin != "" {
		if s := NewShazamRecognizer(cfg.ShazamPythonBin); s != nil {
			shazamRec = s
			ordered = append(ordered, shazamRec)
			log.Printf("recognizer: Shazam enabled (python=%s)", cfg.ShazamPythonBin)
		} else {
			log.Printf("recognizer: Shazam unavailable — %s not found or shazamio not installed", cfg.ShazamPythonBin)
		}
	}

	plan := RecognitionPlan{Ordered: ordered}
	if acrRec != nil && shazamRec != nil {
		plan.Confirmer = shazamRec
	}
	plan.Continuity = shazamRec

	return newRecognitionComponents(plan, newFingerprinter())
}
