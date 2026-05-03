package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShazamParticipatesInProviders(t *testing.T) {
	tests := []struct {
		name string
		spec []RecognitionProviderSpec
		want bool
	}{
		{"empty", nil, false},
		{"no shazam", []RecognitionProviderSpec{
			{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		}, false},
		{"shazam disabled", []RecognitionProviderSpec{
			{ID: "shazam", Enabled: false, Roles: []string{"primary"}},
		}, false},
		{"shazam no roles", []RecognitionProviderSpec{
			{ID: "shazam", Enabled: true, Roles: nil},
		}, false},
		{"shazam enabled primary", []RecognitionProviderSpec{
			{ID: "shazam", Enabled: true, Roles: []string{"primary"}},
		}, true},
		{"shazam id case", []RecognitionProviderSpec{
			{ID: "SHAZAM", Enabled: true, Roles: []string{"primary"}},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shazamParticipatesInProviders(tt.spec); got != tt.want {
				t.Fatalf("shazamParticipatesInProviders() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildRecognitionPlanFromProviders_PrimaryOrder(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	b := &stubRecognizer{name: "B"}
	c := &stubRecognizer{name: "C"}
	inst := recognitionInstances{acr: a, audd: b, shazamio: c}
	specs := []RecognitionProviderSpec{
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		{ID: "audd", Enabled: true, Roles: []string{"primary"}},
		{ID: "shazam", Enabled: true, Roles: []string{"primary"}},
	}
	plan := buildRecognitionPlanFromProviders(specs, inst)
	if len(plan.Ordered) != 3 {
		t.Fatalf("ordered len=%d want 3", len(plan.Ordered))
	}
	if plan.Ordered[0] != a || plan.Ordered[1] != b || plan.Ordered[2] != c {
		t.Fatalf("unexpected order")
	}
	if plan.Confirmer != nil {
		t.Fatalf("confirmer should be nil with three primaries (no explicit confirmer role), got %v", plan.Confirmer)
	}
}

func TestBuildRecognitionPlanFromProviders_TwoPrimaryConfirmerFallback(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	b := &stubRecognizer{name: "B"}
	inst := recognitionInstances{acr: a, audd: b}
	specs := []RecognitionProviderSpec{
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		{ID: "audd", Enabled: true, Roles: []string{"primary"}},
	}
	plan := buildRecognitionPlanFromProviders(specs, inst)
	if len(plan.Ordered) != 2 {
		t.Fatalf("ordered len=%d", len(plan.Ordered))
	}
	if plan.Confirmer != b {
		t.Fatalf("want second primary as confirmer when none declared, got %v", plan.Confirmer)
	}
}

func TestBuildRecognitionPlanFromProviders_EmptyRolesSkipped(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	inst := recognitionInstances{acr: a}
	specs := []RecognitionProviderSpec{
		{ID: "acrcloud", Enabled: true, Roles: []string{}},
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
	}
	plan := buildRecognitionPlanFromProviders(specs, inst)
	if len(plan.Ordered) != 1 || plan.Ordered[0] != a {
		t.Fatalf("got %+v", plan.Ordered)
	}
}

func TestBuildRecognitionPlanFromProviders_ConfirmerRole(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	b := &stubRecognizer{name: "B"}
	inst := recognitionInstances{acr: a, audd: b}
	specs := []RecognitionProviderSpec{
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		{ID: "audd", Enabled: true, Roles: []string{"confirmer"}},
	}
	plan := buildRecognitionPlanFromProviders(specs, inst)
	if len(plan.Ordered) != 1 || plan.Ordered[0] != a {
		t.Fatalf("ordered=%v", plan.Ordered)
	}
	if plan.Confirmer != b {
		t.Fatalf("want AudD confirmer, got %v", plan.Confirmer)
	}
}

func TestBuildRecognitionPlanFromChain_MatchesAcrCloudFirstOrdering(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	b := &stubRecognizer{name: "B"}
	c := &stubRecognizer{name: "C"}
	inst := recognitionInstances{acr: a, audd: b, shazamio: c}
	planChain := buildRecognitionPlanFromChain("acrcloud_first", inst)
	planProv := buildRecognitionPlanFromProviders([]RecognitionProviderSpec{
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		{ID: "audd", Enabled: true, Roles: []string{"primary"}},
		{ID: "shazam", Enabled: true, Roles: []string{"primary"}},
	}, inst)
	if len(planChain.Ordered) != len(planProv.Ordered) {
		t.Fatalf("len chain=%d providers=%d", len(planChain.Ordered), len(planProv.Ordered))
	}
	for i := range planChain.Ordered {
		if planChain.Ordered[i] != planProv.Ordered[i] {
			t.Fatalf("index %d chain=%v providers=%v", i, planChain.Ordered[i], planProv.Ordered[i])
		}
	}
}

func TestApplyRecognitionProvidersFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	payload := `{
  "recognition": {
    "merge_policy": "first_success",
    "providers": [
      {"id": "acrcloud", "enabled": true, "roles": ["primary"]},
      {"id": "audd", "enabled": false, "roles": ["primary"]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.CalibrationConfigPath = path
	applyRecognitionProvidersFromConfigFile(&cfg)
	if len(cfg.RecognitionProviders) != 2 {
		t.Fatalf("providers len=%d", len(cfg.RecognitionProviders))
	}
	if cfg.RecognitionMergePolicy != "first_success" {
		t.Fatalf("merge_policy=%q", cfg.RecognitionMergePolicy)
	}
}

func TestApplyRecognitionProvidersFromConfigFile_DefaultMergePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	payload := `{"recognition":{"providers":[{"id":"acrcloud","enabled":true,"roles":["primary"]}]}}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.CalibrationConfigPath = path
	applyRecognitionProvidersFromConfigFile(&cfg)
	if cfg.RecognitionMergePolicy != "first_success" {
		t.Fatalf("merge_policy=%q", cfg.RecognitionMergePolicy)
	}
}

func TestApplyRecognitionProvidersFromConfigFile_MissingRecognitionKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	payload := `{"audio_input":{"device_match":""}}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.CalibrationConfigPath = path
	applyRecognitionProvidersFromConfigFile(&cfg)
	if cfg.RecognitionProviders != nil {
		t.Fatalf("want nil providers, got len=%d", len(cfg.RecognitionProviders))
	}
}

func TestApplyRecognitionProvidersFromConfigFile_ShazamRecognizerDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	payload := `{
  "recognition": {
    "shazam_recognizer_enabled": false,
    "providers": [
      {"id": "acrcloud", "enabled": true, "roles": ["primary"]},
      {"id": "shazam", "enabled": true, "roles": ["primary"]}
    ]
  }
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.CalibrationConfigPath = path
	applyRecognitionProvidersFromConfigFile(&cfg)
	if len(cfg.RecognitionProviders) != 2 {
		t.Fatalf("providers len=%d", len(cfg.RecognitionProviders))
	}
	for _, p := range cfg.RecognitionProviders {
		if strings.EqualFold(p.ID, "shazam") && p.Enabled {
			t.Fatal("want shazam provider forced off when shazam_recognizer_enabled is false")
		}
	}
}

func TestApplyRecognitionProvidersFromConfigFile_EmptyProvidersArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	payload := `{"recognition":{"providers":[],"merge_policy":"first_success"}}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.CalibrationConfigPath = path
	applyRecognitionProvidersFromConfigFile(&cfg)
	if len(cfg.RecognitionProviders) != 0 {
		t.Fatalf("len=%d", len(cfg.RecognitionProviders))
	}
}

func TestBuildRecognitionPlanFromProviders_ContinuityWhenShazamioClientPresentEvenWithoutPrimaries(t *testing.T) {
	shz := &stubRecognizer{name: "Shazamio"}
	inst := recognitionInstances{shazamio: shz, shazamioContinuity: shz}
	specs := []RecognitionProviderSpec{
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
	}
	plan := buildRecognitionPlanFromProviders(specs, inst)
	if len(plan.Ordered) != 0 {
		t.Fatalf("expected no primaries without ACR instance")
	}
	// Shazamio is dual-purpose today: continuity runs whenever the subprocess client
	// exists, even if no primary chain is runnable (e.g. missing ACR credentials).
	if plan.Continuity != shz {
		t.Fatalf("want Shazamio continuity when client is present, got %v", plan.Continuity)
	}
}

func TestBuildRecognitionPlanFromProviders_ContinuityWhenShazamioClientPresentWithoutShazamPrimary(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	shz := &stubRecognizer{name: "Shazamio"}
	cont := &stubRecognizer{name: "ShazamioCont"}
	inst := recognitionInstances{acr: a, shazamio: shz, shazamioContinuity: cont}
	specs := []RecognitionProviderSpec{
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
	}
	plan := buildRecognitionPlanFromProviders(specs, inst)
	if len(plan.Ordered) != 1 || plan.Ordered[0] != a {
		t.Fatalf("ordered=%v", plan.Ordered)
	}
	if plan.Continuity != cont {
		t.Fatalf("want Shazamio continuity whenever the continuity wrapper is built (ACR-only chain), got %v", plan.Continuity)
	}
}

func TestBuildRecognitionPlanFromProviders_NoContinuityWithoutShazamioClient(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	shz := &stubRecognizer{name: "Shazamio"}
	inst := recognitionInstances{acr: a, shazamio: shz, shazamioContinuity: nil}
	specs := []RecognitionProviderSpec{
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		{ID: "shazam", Enabled: true, Roles: []string{"primary"}},
	}
	plan := buildRecognitionPlanFromProviders(specs, inst)
	if plan.Continuity != nil {
		t.Fatalf("continuity should be nil when shazamioContinuity wrapper was not constructed, got %v", plan.Continuity)
	}
}

func TestBuildRecognitionPlanFromProviders_ContinuityWhenShazamPrimary(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	shz := &stubRecognizer{name: "Shazamio"}
	cont := &stubRecognizer{name: "ShazamioCont"}
	inst := recognitionInstances{acr: a, shazamio: shz, shazamioContinuity: cont}
	specs := []RecognitionProviderSpec{
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		{ID: "shazam", Enabled: true, Roles: []string{"primary"}},
	}
	plan := buildRecognitionPlanFromProviders(specs, inst)
	if plan.Continuity != cont {
		t.Fatalf("want Shazamio continuity when shazam primary is enabled, got %v", plan.Continuity)
	}
}

func TestBuildRecognitionPlanFromChain_ContinuityWhenShazamioClientPresentAcrCloudOnly(t *testing.T) {
	acr := &stubRecognizer{name: "ACRCloud"}
	shz := &stubRecognizer{name: "Shazamio"}
	cont := &stubRecognizer{name: "ShazamioCont"}
	inst := recognitionInstances{acr: acr, shazamio: shz, shazamioContinuity: cont}
	plan := buildRecognitionPlanFromChain("acrcloud_only", inst)
	if plan.Continuity != cont {
		t.Fatalf("acrcloud_only still attaches Shazamio continuity when the client is installed (dual behaviour), got %v", plan.Continuity)
	}
}

func TestBuildRecognitionPlanFromChain_ContinuityWhenPolicyIncludesShazam(t *testing.T) {
	acr := &stubRecognizer{name: "ACRCloud"}
	shz := &stubRecognizer{name: "Shazamio"}
	cont := &stubRecognizer{name: "ShazamioCont"}
	inst := recognitionInstances{acr: acr, shazamio: shz, shazamioContinuity: cont}
	plan := buildRecognitionPlanFromChain("acrcloud_first", inst)
	if plan.Continuity != cont {
		t.Fatalf("want continuity for acrcloud_first when Shazamio is installed, got %v", plan.Continuity)
	}
}
