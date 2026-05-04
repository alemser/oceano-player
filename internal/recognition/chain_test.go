package recognition

import (
	"context"
	"errors"
	"testing"
)

type stubRecognizer struct {
	name   string
	result *Result
	err    error
}

func (s *stubRecognizer) Name() string { return s.name }
func (s *stubRecognizer) Recognize(_ context.Context, _ string) (*Result, error) {
	return s.result, s.err
}

func TestChainRecognizer_RateLimitedProviderName_primaryRateLimits(t *testing.T) {
	primary := &stubRecognizer{name: "ACRCloud", err: ErrRateLimit}
	fallback := &stubRecognizer{name: "Shazam", err: ErrRateLimit}
	chain := NewChainRecognizer(primary, fallback).(*ChainRecognizer)

	_, err := chain.Recognize(context.Background(), "")
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}
	// lastRateLimitedName should be the last one that rate-limited
	if chain.RateLimitedProviderName() != "Shazam" {
		t.Errorf("expected Shazam, got %q", chain.RateLimitedProviderName())
	}
}

func TestChainRecognizer_RateLimitedProviderName_onlyFallbackRateLimits(t *testing.T) {
	primary := &stubRecognizer{name: "ACRCloud", result: nil, err: nil} // no-match
	fallback := &stubRecognizer{name: "Shazam", err: ErrRateLimit}
	chain := NewChainRecognizer(primary, fallback).(*ChainRecognizer)

	_, err := chain.Recognize(context.Background(), "")
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}
	if chain.RateLimitedProviderName() != "Shazam" {
		t.Errorf("expected Shazam, got %q", chain.RateLimitedProviderName())
	}
}

func TestChainRecognizer_RateLimitedProviderName_primaryRateLimitsFallbackNoMatch(t *testing.T) {
	// Primary rate-limits, fallback cleans up with no-match → chain returns (nil,nil).
	// No ErrRateLimit surfaces; lastRateLimitedName should still reflect ACRCloud but
	// the caller won't call handleRecognitionError so it doesn't matter. Verify reset.
	primary := &stubRecognizer{name: "ACRCloud", err: ErrRateLimit}
	fallback := &stubRecognizer{name: "Shazam", result: nil, err: nil}
	chain := NewChainRecognizer(primary, fallback).(*ChainRecognizer)

	res, err := chain.Recognize(context.Background(), "")
	if err != nil || res != nil {
		t.Fatalf("expected (nil, nil), got (%v, %v)", res, err)
	}
	// Even though ACRCloud rate-limited, the chain returned clean no-match.
	// The coordinator will NOT call handleRecognitionError in this path,
	// so whatever lastRateLimitedName contains is irrelevant — but the field
	// must be reset before each call.
	// Verify second call resets it.
	primary.err = nil
	_, _ = chain.Recognize(context.Background(), "")
	if chain.RateLimitedProviderName() != "" {
		t.Errorf("expected empty after non-rate-limit call, got %q", chain.RateLimitedProviderName())
	}
}

func TestChainRecognizer_RateLimitedProviderName_noRateLimit(t *testing.T) {
	// NewChainRecognizer with two providers returns a *ChainRecognizer.
	primary := &stubRecognizer{name: "ACRCloud", result: &Result{Title: "Track"}}
	fallback := &stubRecognizer{name: "Shazam", result: nil}
	chain := NewChainRecognizer(primary, fallback).(*ChainRecognizer)

	_, _ = chain.Recognize(context.Background(), "")
	if chain.RateLimitedProviderName() != "" {
		t.Errorf("expected empty when no rate limit, got %q", chain.RateLimitedProviderName())
	}
}
