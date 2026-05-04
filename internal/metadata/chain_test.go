package metadata

import (
	"context"
	"testing"
)

type seqProvider struct {
	name    string
	patches []*Patch
}

func (s *seqProvider) Name() string { return s.name }

func (s *seqProvider) Enrich(context.Context, Request) (*Patch, error) {
	if len(s.patches) == 0 {
		return &Patch{}, nil
	}
	p := s.patches[0]
	s.patches = s.patches[1:]
	if p == nil {
		return &Patch{}, nil
	}
	return p, nil
}

func TestChainFillMissingThenStop_stopsWhenSeedComplete(t *testing.T) {
	var calls int
	noop := ProviderFunc(func(context.Context, Request) (*Patch, error) {
		calls++
		return &Patch{Provider: "noop", Album: "nope"}, nil
	})
	seed := &Patch{Album: "A", Label: "L", Released: "R", TrackNumber: "1", DiscogsURL: "http://d", Artwork: &ArtworkPatch{URL: "http://art"}}
	chain := NewChain([]Provider{NewPayloadProvider(), noop}, MergePolicyFillMissingThenStop)
	out, err := chain.Run(context.Background(), Request{}, seed)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("expected no provider runs when seed is complete, got %d calls", calls)
	}
	if out.Album != "A" || out.Artwork == nil || out.Artwork.URL != "http://art" {
		t.Fatalf("unexpected out: %+v", out)
	}
}

// ProviderFunc adapts a function to Provider for tests.
type ProviderFunc func(context.Context, Request) (*Patch, error)

func (f ProviderFunc) Name() string { return "func" }

func (f ProviderFunc) Enrich(ctx context.Context, req Request) (*Patch, error) {
	return f(ctx, req)
}

func TestChainFillMissingThenStop_earlyExit(t *testing.T) {
	first := &seqProvider{
		name: "first",
		patches: []*Patch{
			{Provider: "first", Album: "FromFirst"},
		},
	}
	var secondCalls int
	second := ProviderFunc(func(context.Context, Request) (*Patch, error) {
		secondCalls++
		return &Patch{Provider: "second", Label: "L2"}, nil
	})
	// Only album was missing relative to the full mask; once filled, remaining providers must not run.
	seed := &Patch{
		Label: "L0", Released: "R0", TrackNumber: "1", DiscogsURL: "http://d",
		Artwork: &ArtworkPatch{URL: "http://art"},
	}
	chain := NewChain([]Provider{first, second}, MergePolicyFillMissingThenStop)
	out, err := chain.Run(context.Background(), Request{}, seed)
	if err != nil {
		t.Fatal(err)
	}
	if secondCalls != 0 {
		t.Fatalf("expected second provider skipped once all seed-missing slots filled, got %d", secondCalls)
	}
	if out.Album != "FromFirst" || out.Label != "L0" {
		t.Fatalf("out: %+v", out)
	}
}

func TestChainFillMissingThenStop_mergesDisjointFields(t *testing.T) {
	a := &seqProvider{
		name: "a",
		patches: []*Patch{
			{Provider: "a", Album: "Alb"},
		},
	}
	b := &seqProvider{
		name: "b",
		patches: []*Patch{
			{Provider: "b", Label: "Lab"},
		},
	}
	chain := NewChain([]Provider{a, b}, MergePolicyFillMissingThenStop)
	out, err := chain.Run(context.Background(), Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Album != "Alb" || out.Label != "Lab" {
		t.Fatalf("out: %+v", out)
	}
}

func TestChainFirstSuccess_skipsFollowingProviders(t *testing.T) {
	var secondCalls int
	first := &seqProvider{
		name: "first",
		patches: []*Patch{
			{Provider: "first", Album: "Only"},
		},
	}
	second := ProviderFunc(func(context.Context, Request) (*Patch, error) {
		secondCalls++
		return &Patch{Provider: "second", Label: "L"}, nil
	})
	chain := NewChain([]Provider{first, second}, MergePolicyFirstSuccess)
	out, err := chain.Run(context.Background(), Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if secondCalls != 0 {
		t.Fatalf("second should not run: %d", secondCalls)
	}
	if out.Album != "Only" || out.Label != "" {
		t.Fatalf("out: %+v", out)
	}
}

func TestChainCollectAll_runsAllProviders(t *testing.T) {
	var calls int
	p := ProviderFunc(func(context.Context, Request) (*Patch, error) {
		calls++
		if calls == 1 {
			return &Patch{Provider: "p1", Album: "A1"}, nil
		}
		return &Patch{Provider: "p2", Label: "L2"}, nil
	})
	chain := NewChain([]Provider{p, p}, MergePolicyCollectAllBestEffort)
	out, err := chain.Run(context.Background(), Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls: %d", calls)
	}
	if out.Album != "A1" || out.Label != "L2" {
		t.Fatalf("out: %+v", out)
	}
}

func TestChainCollectAll_fillMissingDoesNotOverwrite(t *testing.T) {
	p := ProviderFunc(func(context.Context, Request) (*Patch, error) {
		return &Patch{Provider: "p1", Album: "Second"}, nil
	})
	seed := &Patch{Album: "First"}
	chain := NewChain([]Provider{p}, MergePolicyCollectAllBestEffort)
	out, err := chain.Run(context.Background(), Request{}, seed)
	if err != nil {
		t.Fatal(err)
	}
	if out.Album != "First" {
		t.Fatalf("got %q", out.Album)
	}
}

func TestPayloadProvider_Enrich(t *testing.T) {
	p := NewPayloadProvider()
	out, err := p.Enrich(context.Background(), Request{
		Album: "X", Label: "Y", Released: "2020", TrackNumber: "3",
		DiscogsURL: "https://example/d/1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Album != "X" || out.Label != "Y" || out.Released != "2020" || out.TrackNumber != "3" || out.DiscogsURL != "https://example/d/1" {
		t.Fatalf("patch: %+v", out)
	}
}
