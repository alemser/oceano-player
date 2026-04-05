package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
)

// ── ChainRecognizer ───────────────────────────────────────────────────────────

type stubRecognizer struct {
	name   string
	result *RecognitionResult
	err    error
	calls  int
}

func (s *stubRecognizer) Name() string { return s.name }
func (s *stubRecognizer) Recognize(_ context.Context, _ string) (*RecognitionResult, error) {
	s.calls++
	return s.result, s.err
}

func TestChainRecognizer_Name(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	b := &stubRecognizer{name: "B"}
	chain := NewChainRecognizer(a, b).(*ChainRecognizer)
	if chain.Name() != "A→B" {
		t.Errorf("Name() = %q, want %q", chain.Name(), "A→B")
	}
}

func TestChainRecognizer_SingleProvider_NotWrapped(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	rec := NewChainRecognizer(a)
	// Single provider → returned as-is, not wrapped in ChainRecognizer.
	if _, ok := rec.(*ChainRecognizer); ok {
		t.Error("single provider should not be wrapped in ChainRecognizer")
	}
}

func TestChainRecognizer_NilProviders_ReturnsNil(t *testing.T) {
	if NewChainRecognizer(nil, nil) != nil {
		t.Error("all-nil providers should return nil")
	}
}

func TestChainRecognizer_SkipsNilProviders(t *testing.T) {
	a := &stubRecognizer{name: "A", result: &RecognitionResult{Title: "Track A"}}
	rec := NewChainRecognizer(nil, a, nil)
	result, err := rec.Recognize(context.Background(), "test.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Title != "Track A" {
		t.Errorf("expected Track A, got %v", result)
	}
}

func TestChainRecognizer_FirstMatchReturned(t *testing.T) {
	a := &stubRecognizer{name: "A", result: &RecognitionResult{Title: "Track A", ACRID: "acr-a"}}
	b := &stubRecognizer{name: "B", result: &RecognitionResult{Title: "Track B", ACRID: "acr-b"}}
	chain := NewChainRecognizer(a, b)

	result, err := chain.Recognize(context.Background(), "test.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.ACRID != "acr-a" {
		t.Errorf("expected first provider result, got %v", result)
	}
	if b.calls != 0 {
		t.Errorf("second provider should not be called when first matches, got %d calls", b.calls)
	}
}

func TestChainRecognizer_FallsThrough_OnNoMatch(t *testing.T) {
	a := &stubRecognizer{name: "A", result: nil} // no match
	b := &stubRecognizer{name: "B", result: &RecognitionResult{Title: "Track B"}}
	chain := NewChainRecognizer(a, b)

	result, err := chain.Recognize(context.Background(), "test.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Title != "Track B" {
		t.Errorf("expected fallback to second provider, got %v", result)
	}
	if a.calls != 1 {
		t.Errorf("first provider should be called once, got %d", a.calls)
	}
	if b.calls != 1 {
		t.Errorf("second provider should be called once, got %d", b.calls)
	}
}

func TestChainRecognizer_LogsShazamFallbackMatch(t *testing.T) {
	var buf bytes.Buffer
	prevWriter := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer log.SetOutput(prevWriter)
	defer log.SetFlags(prevFlags)

	a := &stubRecognizer{name: "ACRCloud", result: nil}
	b := &stubRecognizer{name: "Shazam", result: &RecognitionResult{Title: "Exodus", Artist: "Bob Marley"}}
	chain := NewChainRecognizer(a, b)

	result, err := chain.Recognize(context.Background(), "test.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected Shazam fallback result")
	}

	got := buf.String()
	if !strings.Contains(got, "recognizer chain: Shazam: fallback match Bob Marley — Exodus") {
		t.Fatalf("expected Shazam fallback match log, got %q", got)
	}
}

func TestChainRecognizer_FallsThrough_OnError(t *testing.T) {
	a := &stubRecognizer{name: "A", err: errors.New("network error")}
	b := &stubRecognizer{name: "B", result: &RecognitionResult{Title: "Track B"}}
	chain := NewChainRecognizer(a, b)

	result, err := chain.Recognize(context.Background(), "test.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Title != "Track B" {
		t.Errorf("expected fallback after error, got %v", result)
	}
}

func TestChainRecognizer_FallsThrough_OnRateLimit(t *testing.T) {
	a := &stubRecognizer{name: "ACRCloud", err: ErrRateLimit}
	b := &stubRecognizer{name: "Shazam", result: &RecognitionResult{Title: "Track B", Artist: "Artist B"}}
	chain := NewChainRecognizer(a, b)

	result, err := chain.Recognize(context.Background(), "test.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Title != "Track B" {
		t.Fatalf("expected Shazam fallback match after ACRCloud rate limit, got %v", result)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Fatalf("expected one call per provider (a=%d b=%d)", a.calls, b.calls)
	}
}

func TestChainRecognizer_ErrorThenNoMatch_ReturnsError(t *testing.T) {
	// This outcome drives runRecognizer's error path, where local fingerprint
	// fallback is attempted as a last resort.
	a := &stubRecognizer{name: "ACRCloud", err: errors.New("network error")}
	b := &stubRecognizer{name: "Shazam", result: nil}
	chain := NewChainRecognizer(a, b)

	result, err := chain.Recognize(context.Background(), "test.wav")
	if result != nil {
		t.Fatalf("expected nil result, got %v", result)
	}
	if err == nil {
		t.Fatal("expected non-nil error when first provider errors and second returns no match")
	}
}

func TestChainRecognizer_NoMatchThenNoMatch_ReturnsNilNil(t *testing.T) {
	// This outcome drives runRecognizer's no-match path, where local fingerprint
	// fallback is attempted as a last resort.
	a := &stubRecognizer{name: "ACRCloud", result: nil}
	b := &stubRecognizer{name: "Shazam", result: nil}
	chain := NewChainRecognizer(a, b)

	result, err := chain.Recognize(context.Background(), "test.wav")
	if err != nil {
		t.Fatalf("expected nil error when all providers no-match, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result when all providers no-match, got %v", result)
	}
}

func TestChainRecognizer_AllError_ReturnsLastError(t *testing.T) {
	errA := errors.New("error from A")
	errB := errors.New("error from B")
	a := &stubRecognizer{name: "A", err: errA}
	b := &stubRecognizer{name: "B", err: errB}
	chain := NewChainRecognizer(a, b)

	_, err := chain.Recognize(context.Background(), "test.wav")
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !errors.Is(err, errB) {
		t.Errorf("expected last error %v, got %v", errB, err)
	}
}

func TestChainRecognizer_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before any call

	a := &stubRecognizer{name: "A", result: &RecognitionResult{Title: "Track A"}}
	b := &stubRecognizer{name: "B", result: &RecognitionResult{Title: "Track B"}}
	chain := NewChainRecognizer(a, b)

	_, err := chain.Recognize(ctx, "test.wav")
	// ChainRecognizer checks ctx.Err() before each provider; both should be skipped.
	if err == nil {
		t.Error("expected context error")
	}
	if a.calls != 0 || b.calls != 0 {
		t.Errorf("no provider should be called with cancelled context (a=%d b=%d)", a.calls, b.calls)
	}
}

// ── Cross-service confirmation matching ───────────────────────────────────────

// crossServiceMatch encapsulates the title+artist comparison used in
// runRecognizer when confirmRec differs from rec (e.g. Shazam confirming ACRCloud).
func crossServiceMatch(result, conf *RecognitionResult, sameProvider bool) bool {
	if sameProvider && conf.ACRID != "" && conf.ACRID == result.ACRID {
		return true
	}
	return strings.EqualFold(conf.Title, result.Title) &&
		strings.EqualFold(conf.Artist, result.Artist)
}

func TestCrossServiceMatch_SameACRID(t *testing.T) {
	r := &RecognitionResult{ACRID: "acr-001", Title: "Track", Artist: "Artist"}
	c := &RecognitionResult{ACRID: "acr-001", Title: "Track", Artist: "Artist"}
	if !crossServiceMatch(r, c, true) {
		t.Error("same ACRID should match")
	}
}

func TestCrossServiceMatch_TitleArtist_CaseInsensitive(t *testing.T) {
	// ACRCloud returns "Forever Loving Jah", Shazam returns "Forever Loving Jah"
	// but different ACRID (different ID spaces).
	r := &RecognitionResult{ACRID: "acr-001", Title: "Forever Loving Jah", Artist: "Bob Marley & The Wailers"}
	c := &RecognitionResult{ACRID: "shz-999", Title: "forever loving jah", Artist: "BOB MARLEY & THE WAILERS"}
	if !crossServiceMatch(r, c, false) {
		t.Error("case-insensitive title+artist match should succeed across services")
	}
}

func TestTracksEquivalent_RemasterSuffix_Ignored(t *testing.T) {
	if !tracksEquivalent("Lovesong", "The Cure", "Lovesong (Remaster 2010)", "The Cure") {
		t.Fatal("remaster suffix should be ignored when matching tracks")
	}
}

func TestTracksEquivalent_ArtistSubsetVariant_Matches(t *testing.T) {
	if !tracksEquivalent("Strange Transmissions", "The Peter Malick Group", "Strange Transmissions", "Peter Malick") {
		t.Fatal("artist subset variant should still match the same track")
	}
}

func TestTracksEquivalent_BobMarleyVariant_Matches(t *testing.T) {
	if !tracksEquivalent("Forever Loving Jah", "Bob Marley & The Wailers", "Forever Loving Jah", "Bob Marley") {
		t.Fatal("artist token subset should match common band-name variants")
	}
}

func TestCrossServiceMatch_DifferentTitle(t *testing.T) {
	r := &RecognitionResult{Title: "Exodus", Artist: "Bob Marley"}
	c := &RecognitionResult{Title: "Jamming", Artist: "Bob Marley"}
	if crossServiceMatch(r, c, false) {
		t.Error("different title should not match")
	}
}

func TestCrossServiceMatch_DifferentArtist(t *testing.T) {
	r := &RecognitionResult{Title: "Redemption Song", Artist: "Bob Marley"}
	c := &RecognitionResult{Title: "Redemption Song", Artist: "Johnny Cash"}
	if crossServiceMatch(r, c, false) {
		t.Error("different artist should not match")
	}
}

func TestCrossServiceMatch_DisagreementMeansRejectCandidate(t *testing.T) {
	// Policy used by runRecognizer: if ACRCloud and Shazam disagree, do not
	// switch to either track. A two-provider confirmation must agree.
	r := &RecognitionResult{Title: "Pictures of You", Artist: "The Cure"}
	c := &RecognitionResult{Title: "Bend Down Low", Artist: "Bob Marley & The Wailers"}
	if crossServiceMatch(r, c, false) {
		t.Fatal("disagreement between ACRCloud and Shazam must reject the candidate")
	}
}

func TestCrossServiceMatch_DifferentACRID_SameProvider_NoTitleMatch(t *testing.T) {
	// Same provider but different ACRID and different title → no match.
	r := &RecognitionResult{ACRID: "acr-001", Title: "Track A", Artist: "Artist"}
	c := &RecognitionResult{ACRID: "acr-002", Title: "Track B", Artist: "Artist"}
	if crossServiceMatch(r, c, true) {
		t.Error("different ACRID and different title should not match")
	}
}

// ── Confirmation result selection ───────────────────────────────────────────

func TestChooseConfirmationResult_PrefersConfirmerMatch(t *testing.T) {
	primary := &RecognitionResult{ACRID: "acr-001", Title: "Track A", Artist: "Artist"}
	confirm := &RecognitionResult{ShazamID: "shz-001", Title: "Track A", Artist: "Artist"}

	res, err, provider := chooseConfirmationResult(
		"ACRCloud", primary, nil,
		"Shazam", confirm, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != confirm {
		t.Fatalf("expected confirmer result, got %#v", res)
	}
	if provider != "Shazam" {
		t.Fatalf("expected provider Shazam, got %q", provider)
	}
}

func TestChooseConfirmationResult_FallsBackToPrimaryWithoutClaimingShazam(t *testing.T) {
	primary := &RecognitionResult{ACRID: "acr-001", Title: "Track A", Artist: "Artist"}

	res, err, provider := chooseConfirmationResult(
		"ACRCloud", primary, nil,
		"Shazam", nil, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != primary {
		t.Fatalf("expected primary result, got %#v", res)
	}
	if provider != "ACRCloud" {
		t.Fatalf("expected provider ACRCloud, got %q", provider)
	}
}

func TestChooseConfirmationResult_ReturnsConfirmerErrorBeforePrimaryError(t *testing.T) {
	confirmErr := errors.New("shazam timeout")
	primaryErr := errors.New("acr timeout")

	res, err, provider := chooseConfirmationResult(
		"ACRCloud", nil, primaryErr,
		"Shazam", nil, confirmErr,
	)
	if res != nil {
		t.Fatalf("expected nil result, got %#v", res)
	}
	if !errors.Is(err, confirmErr) {
		t.Fatalf("expected confirmer error, got %v", err)
	}
	if provider != "Shazam" {
		t.Fatalf("expected provider Shazam, got %q", provider)
	}
}

func TestCanonicalTrackKey_PrefersProviderIDs(t *testing.T) {
	r := &RecognitionResult{ACRID: "acr-123", ShazamID: "shz-999", Title: "x", Artist: "y"}
	if got := canonicalTrackKey(r); got != "acrid:acr-123" {
		t.Fatalf("expected ACRID key, got %q", got)
	}

	r = &RecognitionResult{ShazamID: "shz-999", Title: "x", Artist: "y"}
	if got := canonicalTrackKey(r); got != "shazam:shz-999" {
		t.Fatalf("expected Shazam key, got %q", got)
	}
}

func TestCanonicalTrackKey_FallsBackToMetadata(t *testing.T) {
	r := &RecognitionResult{Title: "Lovesong (Remaster 2010)", Artist: "The Cure"}
	if got := canonicalTrackKey(r); got != "meta:lovesong|thecure" {
		t.Fatalf("unexpected metadata key: %q", got)
	}
}

// ── NewShazamRecognizer ───────────────────────────────────────────────────────

func TestNewShazamRecognizer_ReturnsNilWhenBinaryMissing(t *testing.T) {
	rec := NewShazamRecognizer("/nonexistent/python")
	if rec != nil {
		t.Error("NewShazamRecognizer should return nil when binary does not exist")
	}
}
