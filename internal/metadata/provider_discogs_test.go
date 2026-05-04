package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alemser/oceano-player/internal/recognition"
)

func newTestDiscogsClient(t *testing.T, handler http.HandlerFunc) (*recognition.DiscogsClient, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	c := recognition.NewDiscogsClient(recognition.DiscogsClientConfig{
		Token:      "test-token",
		Timeout:    2 * time.Second,
		MaxRetries: 1,
		BaseURL:    srv.URL,
	})
	return c, srv.Close
}

func TestDiscogsProvider_NilClient(t *testing.T) {
	p := NewDiscogsProvider(nil)
	if p != nil {
		t.Fatal("expected nil provider for nil client")
	}
}

func TestDiscogsProvider_EmptyArtistOrTitle(t *testing.T) {
	var called bool
	client, close := newTestDiscogsClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	defer close()

	p := NewDiscogsProvider(client)
	out, err := p.Enrich(context.Background(), Request{Artist: "", Title: "Something"})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("HTTP call must not be made when artist is empty")
	}
	if !out.Empty() {
		t.Fatalf("expected empty patch, got %+v", out)
	}
}

func TestDiscogsProvider_EnrichSuccess(t *testing.T) {
	client, close := newTestDiscogsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"results": [
				{"title":"Miles Davis - Kind of Blue","year":1959,"label":["Columbia"],"resource_url":"https://api.discogs.com/releases/1","format":["Vinyl"]}
			]
		}`))
	})
	defer close()

	p := NewDiscogsProvider(client)
	out, err := p.Enrich(context.Background(), Request{
		Artist: "Miles Davis",
		Title:  "So What",
		Album:  "Kind of Blue",
		Format: "Vinyl",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Label != "Columbia" {
		t.Fatalf("label=%q want Columbia", out.Label)
	}
	if out.Released != "1959" {
		t.Fatalf("released=%q want 1959", out.Released)
	}
	if out.DiscogsURL != "https://api.discogs.com/releases/1" {
		t.Fatalf("discogs_url=%q", out.DiscogsURL)
	}
	if out.Provider != providerDiscogsID {
		t.Fatalf("provider=%q want %q", out.Provider, providerDiscogsID)
	}
}

func TestDiscogsProvider_NoMatch(t *testing.T) {
	client, close := newTestDiscogsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	defer close()

	p := NewDiscogsProvider(client)
	out, err := p.Enrich(context.Background(), Request{Artist: "X", Title: "Y"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Empty() {
		t.Fatalf("expected empty patch on no match, got %+v", out)
	}
}

func TestDiscogsProvider_RateLimited(t *testing.T) {
	client, close := newTestDiscogsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer close()

	p := NewDiscogsProvider(client)
	out, err := p.Enrich(context.Background(), Request{Artist: "A", Title: "B"})
	if err != nil {
		t.Fatalf("rate-limit must not propagate as error, got: %v", err)
	}
	if !out.Empty() {
		t.Fatalf("expected empty patch on rate-limit, got %+v", out)
	}
}

func TestDiscogsProvider_Name(t *testing.T) {
	client, close := newTestDiscogsClient(t, func(w http.ResponseWriter, r *http.Request) {})
	defer close()

	p := NewDiscogsProvider(client)
	if p.Name() != providerDiscogsID {
		t.Fatalf("Name()=%q want %q", p.Name(), providerDiscogsID)
	}
}
