package recognition

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDiscogsClient_EnrichTrack_SelectsBestCandidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
  "results": [
    {"title":"Miles Davis - Kind of Blue","year":1959,"label":["Columbia"],"resource_url":"https://api.discogs.com/releases/1","format":["Vinyl"]},
    {"title":"Another Artist - Other Album","year":2010,"label":["X"],"resource_url":"https://api.discogs.com/releases/2","format":["CD"]}
  ]
}`))
	}))
	defer srv.Close()

	client := NewDiscogsClient(DiscogsClientConfig{
		Token:      "tok",
		Timeout:    2 * time.Second,
		MaxRetries: 1,
		BaseURL:    srv.URL,
	})
	got, err := client.EnrichTrack(context.Background(), "Miles Davis", "So What", "Kind of Blue", "Vinyl")
	if err != nil {
		t.Fatalf("EnrichTrack error: %v", err)
	}
	if got == nil {
		t.Fatal("expected enrichment result")
	}
	if got.Label != "Columbia" {
		t.Fatalf("label=%q want Columbia", got.Label)
	}
	if got.Released != "1959" {
		t.Fatalf("released=%q want 1959", got.Released)
	}
}

func TestDiscogsClient_EnrichTrack_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := NewDiscogsClient(DiscogsClientConfig{
		Token:      "tok",
		Timeout:    2 * time.Second,
		MaxRetries: 2,
		BaseURL:    srv.URL,
	})
	_, err := client.EnrichTrack(context.Background(), "A", "B", "", "")
	if err != ErrRateLimit {
		t.Fatalf("err=%v want ErrRateLimit", err)
	}
}

