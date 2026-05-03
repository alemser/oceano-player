package main

import (
	"strings"
	"testing"
)

// TestBuildRecognitionPlanFromChain_matrix locks the expected provider order for
// every supported recognizer_chain value when ACR, AudD, and Shazamio are all
// available. Run on any machine: go test ./cmd/oceano-state-manager -run TestBuildRecognitionPlanFromChain_matrix
func TestBuildRecognitionPlanFromChain_matrix(t *testing.T) {
	acr := &stubRecognizer{name: "ACRCloud"}
	audd := &stubRecognizer{name: "AudD"}
	shazam := &stubRecognizer{name: "Shazamio"}
	inst := recognitionInstances{acr: acr, audd: audd, shazamio: shazam}

	tests := []struct {
		chain string
		want  string // joined primary order
	}{
		{"acrcloud_first", "ACRCloud,AudD,Shazamio"},
		{"shazam_first", "Shazamio,ACRCloud,AudD"},
		{"audd_first", "AudD,ACRCloud,Shazamio"},
		{"acrcloud_only", "ACRCloud"},
		{"shazam_only", "Shazamio"},
		{"audd_only", "AudD"},
		{"", "ACRCloud,AudD,Shazamio"}, // normalizeRecognizerChain default
		{"unknown_chain_xyz", "ACRCloud,AudD,Shazamio"},
	}

	for _, tt := range tests {
		chain := normalizeRecognizerChain(tt.chain)
		plan := buildRecognitionPlanFromChain(chain, inst)
		got := joinStubOrderedNames(t, plan.Ordered)
		if got != tt.want {
			t.Fatalf("chain=%q (normalized=%q): got %q want %q", tt.chain, chain, got, tt.want)
		}
	}
}

// When only ACR is configured, chains that list Shazamio/AudD first still skip nils
// and end up with whatever recognizers remain (ChainRecognizer drops nil entries).
func TestBuildRecognitionPlanFromChain_partialInstances(t *testing.T) {
	acr := &stubRecognizer{name: "ACRCloud"}
	inst := recognitionInstances{acr: acr, audd: nil, shazamio: nil}

	plan := buildRecognitionPlanFromChain("shazam_first", inst)
	got := joinStubOrderedNames(t, plan.Ordered)
	if got != "ACRCloud" {
		t.Fatalf("shazam_first with only ACR: got %q want ACRCloud", got)
	}
}

func joinStubOrderedNames(t *testing.T, rs []Recognizer) string {
	t.Helper()
	var names []string
	for _, r := range rs {
		s, ok := r.(*stubRecognizer)
		if !ok {
			t.Fatalf("expected *stubRecognizer in ordered list, got %T", r)
		}
		names = append(names, s.name)
	}
	return strings.Join(names, ",")
}
