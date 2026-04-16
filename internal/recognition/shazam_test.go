package recognition

import "testing"

func TestParseShazamOutput_ExtractsDurationMs(t *testing.T) {
	data := []byte(`{"shazam_id":"123","title":"Exodus","artist":"Bob Marley","album":"Exodus","score":85,"duration_ms":244000}`)
	res, err := parseShazamOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.DurationMs != 244000 {
		t.Errorf("DurationMs = %d, want 244000", res.DurationMs)
	}
	if res.Score != 85 {
		t.Errorf("Score = %d, want 85", res.Score)
	}
}

func TestParseShazamOutput_ZeroDurationMs_WhenAbsent(t *testing.T) {
	// Daemon may omit duration_ms when shazamio does not return it.
	data := []byte(`{"shazam_id":"456","title":"Jamming","artist":"Bob Marley","album":"Exodus","score":72}`)
	res, err := parseShazamOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.DurationMs != 0 {
		t.Errorf("DurationMs = %d, want 0 when field absent", res.DurationMs)
	}
}

func TestParseShazamOutput_ZeroDurationMs_WhenExplicitZero(t *testing.T) {
	data := []byte(`{"shazam_id":"789","title":"Redemption Song","artist":"Bob Marley","album":"Uprising","score":90,"duration_ms":0}`)
	res, err := parseShazamOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.DurationMs != 0 {
		t.Errorf("DurationMs = %d, want 0 for explicit zero", res.DurationMs)
	}
}

func TestParseShazamOutput_NoMatchReturnsNil(t *testing.T) {
	// Empty title+artist → provider returned no match.
	data := []byte(`{}`)
	res, err := parseShazamOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Errorf("expected nil for empty payload, got %+v", res)
	}
}
