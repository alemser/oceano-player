package main

import "testing"

func TestNormalizeInputRecognitionPolicy(t *testing.T) {
	cases := map[string]inputRecognitionPolicy{
		"":             inputRecognitionPolicyAuto,
		"auto":         inputRecognitionPolicyAuto,
		"library":      inputRecognitionPolicyLibrary,
		"display_only": inputRecognitionPolicyDisplayOnly,
		"off":          inputRecognitionPolicyOff,
		"invalid":      inputRecognitionPolicyAuto,
	}
	for in, want := range cases {
		if got := normalizeInputRecognitionPolicy(in); got != want {
			t.Fatalf("normalizeInputRecognitionPolicy(%q)=%q want %q", in, got, want)
		}
	}
}

func TestResolveRecognitionPolicyFromSnapshot_FallbackLibraryWithoutInput(t *testing.T) {
	s := recognitionInputPolicySnapshot{}
	got := resolveRecognitionPolicyFromSnapshot(s)
	if got.Policy != inputRecognitionPolicyLibrary {
		t.Fatalf("policy=%q want library", got.Policy)
	}
}

func TestResolveRecognitionPolicyFromSnapshot_ExplicitInputPolicyWins(t *testing.T) {
	var s recognitionInputPolicySnapshot
	s.AmplifierRuntime.LastKnownInputID = "30"
	s.Amplifier.Inputs = []struct {
		ID                string `json:"id"`
		LogicalName       string `json:"logical_name"`
		RecognitionPolicy string `json:"recognition_policy"`
	}{
		{ID: "30", LogicalName: "FM", RecognitionPolicy: "display_only"},
	}
	got := resolveRecognitionPolicyFromSnapshot(s)
	if got.Policy != inputRecognitionPolicyDisplayOnly {
		t.Fatalf("policy=%q want display_only", got.Policy)
	}
}

func TestResolveRecognitionPolicyFromSnapshot_AutoPhysicalLabel(t *testing.T) {
	var s recognitionInputPolicySnapshot
	s.AmplifierRuntime.LastKnownInputID = "20"
	s.Amplifier.Inputs = []struct {
		ID                string `json:"id"`
		LogicalName       string `json:"logical_name"`
		RecognitionPolicy string `json:"recognition_policy"`
	}{
		{ID: "20", LogicalName: "CD", RecognitionPolicy: "auto"},
	}
	got := resolveRecognitionPolicyFromSnapshot(s)
	if got.Policy != inputRecognitionPolicyLibrary {
		t.Fatalf("policy=%q want library", got.Policy)
	}
}

func TestResolveRecognitionPolicyFromSnapshot_AutoPhysicalRole(t *testing.T) {
	var s recognitionInputPolicySnapshot
	s.AmplifierRuntime.LastKnownInputID = "11"
	s.Amplifier.Inputs = []struct {
		ID                string `json:"id"`
		LogicalName       string `json:"logical_name"`
		RecognitionPolicy string `json:"recognition_policy"`
	}{
		{ID: "11", LogicalName: "AUX", RecognitionPolicy: "auto"},
	}
	s.Amplifier.ConnectedDevices = []struct {
		InputIDs []string `json:"input_ids"`
		Role     string   `json:"role"`
	}{
		{InputIDs: []string{"11"}, Role: "physical_media"},
	}
	got := resolveRecognitionPolicyFromSnapshot(s)
	if got.Policy != inputRecognitionPolicyLibrary {
		t.Fatalf("policy=%q want library", got.Policy)
	}
}

func TestResolveRecognitionPolicyFromSnapshot_AutoNonPhysicalDefaultsOff(t *testing.T) {
	var s recognitionInputPolicySnapshot
	s.AmplifierRuntime.LastKnownInputID = "11"
	s.Amplifier.Inputs = []struct {
		ID                string `json:"id"`
		LogicalName       string `json:"logical_name"`
		RecognitionPolicy string `json:"recognition_policy"`
	}{
		{ID: "11", LogicalName: "FM/DAB", RecognitionPolicy: "auto"},
	}
	s.Amplifier.ConnectedDevices = []struct {
		InputIDs []string `json:"input_ids"`
		Role     string   `json:"role"`
	}{
		{InputIDs: []string{"11"}, Role: "streaming"},
	}
	got := resolveRecognitionPolicyFromSnapshot(s)
	if got.Policy != inputRecognitionPolicyOff {
		t.Fatalf("policy=%q want off", got.Policy)
	}
}

func TestShouldRunRecognitionForInputPolicy(t *testing.T) {
	if shouldRunRecognitionForInputPolicy(inputRecognitionPolicyOff) {
		t.Fatal("off should disable recognition attempts")
	}
	if !shouldRunRecognitionForInputPolicy(inputRecognitionPolicyDisplayOnly) {
		t.Fatal("display_only should allow recognition attempts")
	}
	if !shouldRunRecognitionForInputPolicy(inputRecognitionPolicyLibrary) {
		t.Fatal("library should allow recognition attempts")
	}
}

func TestShouldPersistRecognitionForInputPolicy(t *testing.T) {
	if !shouldPersistRecognitionForInputPolicy(inputRecognitionPolicyLibrary) {
		t.Fatal("library should persist to library DB")
	}
	if shouldPersistRecognitionForInputPolicy(inputRecognitionPolicyDisplayOnly) {
		t.Fatal("display_only should not persist to library DB")
	}
	if shouldPersistRecognitionForInputPolicy(inputRecognitionPolicyOff) {
		t.Fatal("off should not persist to library DB")
	}
}
