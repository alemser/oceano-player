package main

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// stubRecognizer is defined in shazam_test.go (same package).

// --- canonicalProviderID ---

func TestCanonicalProviderID(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"ACRCloud", "acrcloud"},
		{"acrcloud", "acrcloud"},
		{"Shazam", "shazam"},
		{"Shazamio", "shazam"},
		{"ShazamioContinuity", "shazam"},
		{"AudD", "audd"},
		{"AUDD", "audd"},
		{"Unknown", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := canonicalProviderID(tc.name)
		if got != tc.want {
			t.Errorf("canonicalProviderID(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// --- recognizerCanonicalIDs ---

func TestRecognizerCanonicalIDs(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"ACRCloud", []string{"acrcloud"}},
		{"Shazam", []string{"shazam"}},
		{"ACRCloud→Shazam", []string{"acrcloud", "shazam"}},
		{"ACRCloud→AudD", []string{"acrcloud", "audd"}},
		{"Unknown", nil},
		{"ACRCloud→Unknown", []string{"acrcloud"}},
	}
	for _, tc := range cases {
		got := recognizerCanonicalIDs(&stubRecognizer{name: tc.name})
		if len(got) != len(tc.want) {
			t.Errorf("recognizerCanonicalIDs(%q) = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i, id := range got {
			if id != tc.want[i] {
				t.Errorf("recognizerCanonicalIDs(%q)[%d] = %q, want %q", tc.name, i, id, tc.want[i])
			}
		}
	}
}

// --- rateLimitedCanonicalIDs ---

func TestRateLimitedCanonicalIDs_singleRecognizer(t *testing.T) {
	rec := &stubRecognizer{name: "ACRCloud"}
	got := rateLimitedCanonicalIDs(rec)
	if len(got) != 1 || got[0] != "acrcloud" {
		t.Errorf("expected [acrcloud], got %v", got)
	}
}

func TestRateLimitedCanonicalIDs_onlyFallbackRateLimited(t *testing.T) {
	// ACRCloud returns no-match; Shazam returns ErrRateLimit.
	// Only "shazam" should be marked, not "acrcloud".
	primary := &stubRecognizer{name: "ACRCloud", result: nil, err: nil}
	fallback := &stubRecognizer{name: "Shazam", err: ErrRateLimit}
	chain := NewChainRecognizer(primary, fallback).(*ChainRecognizer)
	_, _ = chain.Recognize(t.Context(), "")

	got := rateLimitedCanonicalIDs(chain)
	if len(got) != 1 || got[0] != "shazam" {
		t.Errorf("expected [shazam] only (not acrcloud), got %v", got)
	}
}

func TestRateLimitedCanonicalIDs_bothRateLimited(t *testing.T) {
	// Both providers rate-limited: lastRateLimitedName = "Shazam" (last one hit).
	primary := &stubRecognizer{name: "ACRCloud", err: ErrRateLimit}
	fallback := &stubRecognizer{name: "Shazam", err: ErrRateLimit}
	chain := NewChainRecognizer(primary, fallback).(*ChainRecognizer)
	_, _ = chain.Recognize(t.Context(), "")

	got := rateLimitedCanonicalIDs(chain)
	if len(got) != 1 || got[0] != "shazam" {
		t.Errorf("expected [shazam] (last culprit), got %v", got)
	}
}

func TestRateLimitedCanonicalIDs_noRateLimit(t *testing.T) {
	primary := &stubRecognizer{name: "ACRCloud", result: &RecognitionResult{Title: "Track"}}
	fallback := &stubRecognizer{name: "Shazam"}
	chain := NewChainRecognizer(primary, fallback).(*ChainRecognizer)
	_, _ = chain.Recognize(t.Context(), "")

	got := rateLimitedCanonicalIDs(chain)
	if len(got) != 0 {
		t.Errorf("expected empty when no rate limit, got %v", got)
	}
}

// --- collectRateLimitedProvidersLocked ---

func TestCollectRateLimitedProvidersLocked_filtersExpiredAndSorts(t *testing.T) {
	m := &mgr{mu: sync.Mutex{}}
	now := time.Now()

	m.providerBackoffExpires = map[string]time.Time{
		"shazam":   now.Add(5 * time.Minute), // active
		"acrcloud": now.Add(-1 * time.Second), // expired
		"audd":     now.Add(10 * time.Minute), // active
	}

	ids, expires := m.collectRateLimitedProvidersLocked()

	if len(ids) != 2 {
		t.Fatalf("expected 2 active providers, got %v", ids)
	}
	// Must be sorted: "audd" < "shazam"
	if ids[0] != "audd" || ids[1] != "shazam" {
		t.Errorf("expected [audd shazam], got %v", ids)
	}
	if _, found := expires["acrcloud"]; found {
		t.Error("expired provider must not appear in backoff_expires map")
	}
	if expires["audd"] == 0 || expires["shazam"] == 0 {
		t.Error("active providers must have non-zero epoch in backoff_expires")
	}
}

func TestCollectRateLimitedProvidersLocked_emptyMap(t *testing.T) {
	m := &mgr{mu: sync.Mutex{}}
	ids, expires := m.collectRateLimitedProvidersLocked()
	if ids != nil || expires != nil {
		t.Errorf("expected nil,nil for empty map, got %v %v", ids, expires)
	}
}

// --- RecognitionStatus JSON serialisation ---

func TestRecognitionStatus_RateLimitFieldsOmitWhenEmpty(t *testing.T) {
	s := RecognitionStatus{Phase: "matched", Provider: "acrcloud"}
	data, _ := json.Marshal(s)
	var raw map[string]interface{}
	_ = json.Unmarshal(data, &raw)

	if _, ok := raw["rate_limited_providers"]; ok {
		t.Error("rate_limited_providers must be omitted when nil")
	}
	if _, ok := raw["backoff_expires"]; ok {
		t.Error("backoff_expires must be omitted when nil")
	}
}

func TestRecognitionStatus_RateLimitFieldsPresentWhenSet(t *testing.T) {
	until := time.Now().Add(5 * time.Minute).Unix()
	s := RecognitionStatus{
		Phase:                "no_match",
		RateLimitedProviders: []string{"shazam"},
		BackoffExpires:       map[string]int64{"shazam": until},
	}
	data, _ := json.Marshal(s)
	var raw map[string]interface{}
	_ = json.Unmarshal(data, &raw)

	providers, ok := raw["rate_limited_providers"].([]interface{})
	if !ok || len(providers) != 1 || providers[0] != "shazam" {
		t.Errorf("unexpected rate_limited_providers: %v", raw["rate_limited_providers"])
	}
	expires, ok := raw["backoff_expires"].(map[string]interface{})
	if !ok || expires["shazam"] == nil {
		t.Errorf("unexpected backoff_expires: %v", raw["backoff_expires"])
	}
}
