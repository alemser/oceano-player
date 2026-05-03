package main

import (
	"log"
	"strings"

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

type recognitionInstances struct {
	acr                Recognizer
	audd               Recognizer
	shazamio           Recognizer // Explicit provider list: id "shazam" maps to Shazamio community client; reserve distinct ids for future official Shazam API.
	shazamioContinuity Recognizer
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

func buildRecognitionInstances(cfg Config, lib *internallibrary.Library) recognitionInstances {
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

	var shazamioRec Recognizer
	var shazamioContinuityRec Recognizer
	if cfg.ShazamioPythonBin != "" {
		if s, err := NewShazamioRecognizer(cfg.ShazamioPythonBin); err != nil {
			log.Printf("recognizer: Shazamio unavailable — %v", err)
		} else {
			shazamioRec = wrapWithStats(s, lib)
			shazamioContinuityRec = wrapWithStatsAs(s, lib, "ShazamioContinuity")
			log.Printf("recognizer: Shazamio enabled (python=%s)", cfg.ShazamioPythonBin)
		}
	}

	return recognitionInstances{
		acr:                acrRec,
		audd:               auddRec,
		shazamio:           shazamioRec,
		shazamioContinuity: shazamioContinuityRec,
	}
}

func knownRecognitionProviderID(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "acrcloud", "audd", "shazam":
		return true
	default:
		return false
	}
}

func recognizerForProviderID(id string, inst recognitionInstances) Recognizer {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "acrcloud":
		return inst.acr
	case "audd":
		return inst.audd
	case "shazam":
		return inst.shazamio
	default:
		return nil
	}
}

func specHasRole(spec RecognitionProviderSpec, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, r := range spec.Roles {
		if strings.ToLower(strings.TrimSpace(r)) == want {
			return true
		}
	}
	return false
}

func buildRecognitionPlanFromProviders(specs []RecognitionProviderSpec, inst recognitionInstances) RecognitionPlan {
	var ordered []Recognizer
	for _, spec := range specs {
		if !spec.Enabled || len(spec.Roles) == 0 {
			continue
		}
		if !specHasRole(spec, "primary") {
			continue
		}
		rec := recognizerForProviderID(spec.ID, inst)
		if rec == nil {
			if !knownRecognitionProviderID(spec.ID) {
				log.Printf("recognizer: unknown provider id=%q in recognition.providers — skipped", spec.ID)
			} else {
				log.Printf("recognizer: provider id=%q has primary role but is not available (credentials / install) — skipped", spec.ID)
			}
			continue
		}
		ordered = append(ordered, rec)
	}

	var confirmer Recognizer
	for _, spec := range specs {
		if !spec.Enabled || len(spec.Roles) == 0 {
			continue
		}
		if !specHasRole(spec, "confirmer") {
			continue
		}
		rec := recognizerForProviderID(spec.ID, inst)
		if rec != nil {
			confirmer = rec
			break
		}
	}
	if confirmer == nil && len(ordered) == 2 {
		confirmer = ordered[1]
	}

	if len(ordered) == 0 {
		log.Printf("recognizer: recognition.providers resolved to no available primary providers — recognition disabled")
	}

	return RecognitionPlan{
		Ordered:    ordered,
		Confirmer:  confirmer,
		Continuity: inst.shazamioContinuity,
	}
}

func buildRecognitionPlanFromChain(chain string, inst recognitionInstances) RecognitionPlan {
	var ordered []Recognizer
	switch chain {
	case "shazam_first":
		ordered = appendRecognizers(ordered, inst.shazamio, inst.acr, inst.audd)
	case "acrcloud_only":
		ordered = appendRecognizers(ordered, inst.acr)
	case "shazam_only":
		ordered = appendRecognizers(ordered, inst.shazamio)
	case "audd_only":
		ordered = appendRecognizers(ordered, inst.audd)
	case "audd_first":
		ordered = appendRecognizers(ordered, inst.audd, inst.acr, inst.shazamio)
	default: // "acrcloud_first"
		ordered = appendRecognizers(ordered, inst.acr, inst.audd, inst.shazamio)
	}

	if len(ordered) == 0 {
		log.Printf("recognizer: chain policy=%s resolved to no available providers — recognition disabled", chain)
	}

	var confirmer Recognizer
	if len(ordered) == 2 {
		confirmer = ordered[1]
	}

	return RecognitionPlan{
		Ordered:    ordered,
		Confirmer:  confirmer,
		Continuity: inst.shazamioContinuity,
	}
}

func buildRecognitionComponents(cfg Config, lib *internallibrary.Library) recognitionComponents {
	inst := buildRecognitionInstances(cfg, lib)

	if len(cfg.RecognitionProviders) > 0 {
		mp := strings.ToLower(strings.TrimSpace(cfg.RecognitionMergePolicy))
		if mp != "" && mp != "first_success" {
			log.Printf("recognizer: merge_policy=%q not implemented yet — using first_success", cfg.RecognitionMergePolicy)
		}
		log.Printf("recognizer: using recognition.providers from %s (%d entries, merge_policy=%s)",
			cfg.CalibrationConfigPath, len(cfg.RecognitionProviders), strings.TrimSpace(cfg.RecognitionMergePolicy))
		plan := buildRecognitionPlanFromProviders(cfg.RecognitionProviders, inst)
		return newRecognitionComponents(plan)
	}

	chain := normalizeRecognizerChain(cfg.RecognizerChain)
	log.Printf("recognizer: chain policy=%s", chain)
	plan := buildRecognitionPlanFromChain(chain, inst)
	return newRecognitionComponents(plan)
}
