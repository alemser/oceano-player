package main

import (
	"reflect"
	"testing"
)

func TestMaterializeRecognitionProvidersIfEmpty_preservesNonEmpty(t *testing.T) {
	rec := &RecognitionConfig{
		RecognizerChain: "acrcloud_first",
		Providers: []RecognitionProviderConfig{
			{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
			{ID: "audd", Enabled: true, Roles: []string{"confirmer"}},
		},
		MergePolicy: "first_success",
	}
	materializeRecognitionProvidersIfEmpty(rec)
	if len(rec.Providers) != 2 {
		t.Fatalf("providers len: got %d want 2", len(rec.Providers))
	}
	if rec.Providers[1].ID != "audd" || rec.Providers[1].Roles[0] != "confirmer" {
		t.Fatalf("preserved confirmer: %+v", rec.Providers[1])
	}
}

func TestMaterializeRecognitionProvidersIfEmpty_setsMergePolicy(t *testing.T) {
	rec := &RecognitionConfig{
		RecognizerChain: "acrcloud_first",
		Providers: []RecognitionProviderConfig{
			{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		},
		MergePolicy: "",
	}
	materializeRecognitionProvidersIfEmpty(rec)
	if rec.MergePolicy != "first_success" {
		t.Fatalf("merge_policy: got %q", rec.MergePolicy)
	}
}

func TestBuildRecognitionProvidersFromLegacyChain_acrcloudFirst(t *testing.T) {
	rec := &RecognitionConfig{
		RecognizerChain:     "acrcloud_first",
		ACRCloudHost:        "h",
		ACRCloudAccessKey:   "k",
		ACRCloudSecretKey:   "s",
		AudDAPIToken:        "t",
		ShazamioRecognizerEnabled: true,
	}
	got := buildRecognitionProvidersFromLegacyChain("acrcloud_first", rec)
	want := []RecognitionProviderConfig{
		{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		{ID: "audd", Enabled: true, Roles: []string{"primary"}},
		{ID: "shazam", Enabled: true, Roles: []string{"primary"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestBuildRecognitionProvidersFromLegacyChain_flagsDisabled(t *testing.T) {
	rec := &RecognitionConfig{
		RecognizerChain:           "acrcloud_first",
		ACRCloudHost:              "h",
		ACRCloudAccessKey:         "",
		ACRCloudSecretKey:         "",
		AudDAPIToken:              "",
		ShazamioRecognizerEnabled: false,
	}
	got := buildRecognitionProvidersFromLegacyChain("acrcloud_first", rec)
	want := []RecognitionProviderConfig{
		{ID: "acrcloud", Enabled: false, Roles: []string{"primary"}},
		{ID: "audd", Enabled: false, Roles: []string{"primary"}},
		{ID: "shazam", Enabled: false, Roles: []string{"primary"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestBuildRecognitionProvidersFromLegacyChain_shazamFirstOrder(t *testing.T) {
	rec := &RecognitionConfig{
		ACRCloudHost:              "h",
		ACRCloudAccessKey:         "k",
		ACRCloudSecretKey:         "s",
		ShazamioRecognizerEnabled: true,
	}
	got := buildRecognitionProvidersFromLegacyChain("shazam_first", rec)
	ids := make([]string, len(got))
	for i := range got {
		ids[i] = got[i].ID
	}
	if !reflect.DeepEqual(ids, []string{"shazam", "acrcloud", "audd"}) {
		t.Fatalf("order: %v", ids)
	}
}

func TestBuildRecognitionProvidersFromLegacyChain_audOnly(t *testing.T) {
	rec := &RecognitionConfig{AudDAPIToken: "tok"}
	got := buildRecognitionProvidersFromLegacyChain("audd_only", rec)
	if len(got) != 1 || got[0].ID != "audd" || !got[0].Enabled {
		t.Fatalf("got %+v", got)
	}
}

func TestRecognitionRecognitionEqualForRestart_materializeNormalizesNoop(t *testing.T) {
	old := RecognitionConfig{
		RecognizerChain: "acrcloud_first",
		Providers:       nil,
		MergePolicy:     "",
		ACRCloudHost:    "identify-eu-west-1.acrcloud.com",
	}
	newer := old
	materializeRecognitionProvidersIfEmpty(&newer)
	if recognitionRecognitionEqualForRestart(old, newer) != true {
		t.Fatalf("expected equal after materialize normalization, old=%+v new=%+v", old, newer)
	}
}

func TestRecognitionRecognitionEqualForRestart_detectsProviderChange(t *testing.T) {
	a := RecognitionConfig{
		Providers: []RecognitionProviderConfig{
			{ID: "acrcloud", Enabled: true, Roles: []string{"primary"}},
		},
		MergePolicy: "first_success",
	}
	b := a
	b.Providers = append([]RecognitionProviderConfig(nil), a.Providers...)
	b.Providers[0].Enabled = false
	if recognitionRecognitionEqualForRestart(a, b) {
		t.Fatal("expected inequality when provider enabled flag changes")
	}
}

func TestMaterializeRecognitionProvidersIfEmpty_nilProvidersBecomesEmptySlice(t *testing.T) {
	rec := &RecognitionConfig{
		RecognizerChain: "audd_first",
		Providers:       nil,
		MergePolicy:     "",
	}
	materializeRecognitionProvidersIfEmpty(rec)
	if rec.Providers == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(rec.Providers) != 0 {
		t.Fatalf("len %d want 0", len(rec.Providers))
	}
	if rec.MergePolicy != "first_success" {
		t.Fatalf("merge %q", rec.MergePolicy)
	}
}
