package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildRecognitionPlanFromProviders_PrimaryOrder(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	b := &stubRecognizer{name: "B"}
	c := &stubRecognizer{name: "C"}
	inst := recognitionInstances{acr: a, audd: b, shazam: c}
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
	inst := recognitionInstances{acr: a, audd: b, shazam: c}
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
