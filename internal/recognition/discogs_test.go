package recognition

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiscogsClient_EnrichTrack_SelectsBestCandidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/database/search") {
			prefix := "http://" + r.Host
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
  "results": [
    {"title":"Miles Davis - Kind of Blue","year":1959,"label":["Columbia"],"resource_url":"` + prefix + `/releases/1","format":["Vinyl"]},
    {"title":"Another Artist - Other Album","year":2010,"label":["X"],"resource_url":"` + prefix + `/releases/2","format":["CD"]}
  ]
}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/releases/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
  "tracklist": [
    {"position":"A1","type_":"heading","title":"Side A"},
    {"position":"1","type_":"track","title":"So What"},
    {"position":"2","type_":"track","title":"Freddie Freeloader"}
  ]
}`))
			return
		}
		http.NotFound(w, r)
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

func TestDiscogsClient_EnrichTrack_YearAsString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/database/search") {
			prefix := "http://" + r.Host
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
  "results": [
    {"title":"Miles Davis - Kind of Blue","year":"1959","label":["Columbia"],"resource_url":"` + prefix + `/releases/1","format":["Vinyl"]}
  ]
}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/releases/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tracklist":[{"position":"1","type_":"track","title":"So What"}]}`))
			return
		}
		http.NotFound(w, r)
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
	if got.Released != "1959" {
		t.Fatalf("released=%q want 1959", got.Released)
	}
}

func TestDiscogsClient_EnrichTrack_YearNonNumericString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/database/search") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[{"title":"Artist - Album","year":"unknown","label":["Label"],"resource_url":"` + "http://" + r.Host + `/releases/1","format":["Vinyl"]}]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/releases/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tracklist":[{"position":"A1","type_":"track","title":"Track One"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewDiscogsClient(DiscogsClientConfig{Token: "tok", Timeout: 2 * time.Second, MaxRetries: 1, BaseURL: srv.URL})
	got, err := client.EnrichTrack(context.Background(), "Artist", "Track One", "Album", "Vinyl")
	if err != nil {
		t.Fatalf("EnrichTrack error: %v", err)
	}
	if got == nil {
		t.Fatal("expected enrichment result even with non-numeric year")
	}
	if got.Released != "" {
		t.Fatalf("released=%q: want empty string for non-numeric year", got.Released)
	}
}

func TestScoreDiscogsCandidate_Penalties(t *testing.T) {
	base := discogsSearchItem{
		Title:  "Miles Davis - Kind of Blue",
		Year:   1959,
		Format: []string{"Vinyl", "Album"},
		Style:  []string{"Jazz"},
	}
	compilation := discogsSearchItem{
		Title:  "Miles Davis - The Best of Miles Davis",
		Year:   1990,
		Format: []string{"Vinyl", "Compilation"},
		Style:  []string{"Jazz"},
	}
	live := discogsSearchItem{
		Title:  "Miles Davis - Live at Carnegie Hall",
		Year:   1962,
		Format: []string{"Vinyl", "Album"},
		Style:  []string{"Jazz", "Live"},
	}
	liveTitle := discogsSearchItem{
		Title:  "Miles Davis - Live in Berlin",
		Year:   1964,
		Format: []string{"Vinyl", "Album"},
		Style:  []string{"Jazz"},
	}
	bootleg := discogsSearchItem{
		Title:  "Miles Davis - Kind of Blue",
		Year:   1959,
		Format: []string{"Vinyl", "Unofficial Release"},
		Style:  []string{"Jazz"},
	}

	scoreBase := scoreDiscogsCandidate(base, "Miles Davis", "So What", "Kind of Blue", "Vinyl")
	scoreComp := scoreDiscogsCandidate(compilation, "Miles Davis", "So What", "Kind of Blue", "Vinyl")
	scoreLive := scoreDiscogsCandidate(live, "Miles Davis", "So What", "Kind of Blue", "Vinyl")
	scoreLiveTitle := scoreDiscogsCandidate(liveTitle, "Miles Davis", "So What", "Kind of Blue", "Vinyl")
	scoreBootleg := scoreDiscogsCandidate(bootleg, "Miles Davis", "So What", "Kind of Blue", "Vinyl")

	if scoreComp >= scoreBase {
		t.Errorf("compilation score %d should be lower than studio %d", scoreComp, scoreBase)
	}
	if scoreLive >= scoreBase {
		t.Errorf("live (style) score %d should be lower than studio %d", scoreLive, scoreBase)
	}
	if scoreLiveTitle >= scoreBase {
		t.Errorf("live (title) score %d should be lower than studio %d", scoreLiveTitle, scoreBase)
	}
	if scoreBootleg >= scoreBase {
		t.Errorf("bootleg score %d should be lower than studio %d", scoreBootleg, scoreBase)
	}
}

func TestPickBestDiscogsResult_PrefersStudioOverCompilation(t *testing.T) {
	studio := discogsSearchItem{
		Title:  "Miles Davis - Kind of Blue",
		Year:   1959,
		Format: []string{"Vinyl", "Album"},
		Style:  []string{"Jazz"},
	}
	comp := discogsSearchItem{
		Title:  "Miles Davis - The Best of Miles Davis",
		Year:   1990,
		Format: []string{"Vinyl", "Compilation"},
		Style:  []string{"Jazz"},
	}
	liveAlbum := discogsSearchItem{
		Title:  "Miles Davis - Live at the Plugged Nickel",
		Year:   1965,
		Format: []string{"Vinyl", "Album"},
		Style:  []string{"Jazz", "Live"},
	}

	got := pickBestDiscogsResult(
		[]discogsSearchItem{liveAlbum, comp, studio},
		"Miles Davis", "So What", "Kind of Blue", "Vinyl",
	)
	if got == nil {
		t.Fatal("expected a result")
	}
	if got.Album != "Kind of Blue" {
		t.Errorf("album=%q want %q (studio should beat compilation and live)", got.Album, "Kind of Blue")
	}
}

func TestMatchDiscogsTracklistPosition_VinylSides(t *testing.T) {
	rows := []discogsTracklistItem{
		{Position: "A1", Type_: "track", Title: "Blue in Green"},
		{Position: "A2", Type_: "track", Title: "So What"},
	}
	if g := matchDiscogsTracklistPosition(rows, "So What"); g != "A2" {
		t.Fatalf("got %q want A2", g)
	}
}

func TestMatchDiscogsTracklistPosition_SkipsHeadings(t *testing.T) {
	rows := []discogsTracklistItem{
		{Position: "A", Type_: "heading", Title: "Part One"},
		{Position: "1", Type_: "track", Title: "So What"},
	}
	if g := matchDiscogsTracklistPosition(rows, "So What"); g != "1" {
		t.Fatalf("got %q want 1", g)
	}
}

func TestNormalizeDiscogsTrackTitle_FeatStripped(t *testing.T) {
	if g := normalizeDiscogsTrackTitle("So What (feat. John Coltrane)"); g != "so what" {
		t.Fatalf("got %q", g)
	}
}

func TestCanonicalDiscogsTrackPosition(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"1", "1"},
		{" 12 ", "12"},
		{"a2", "A2"},
		{"A-2", "A2"},
		{"B.3", "B3"},
		{"2A", "2A"},
		{"3d", "3D"},
		{"12-A", "12A"},
		{"CD1-3", "CD1-3"},
		{"cd2-11", "cd2-11"}, // HasPrefix CD → passthrough (case preserved in Fields join — actually we use Fields on original "cd2-11" → "cd2-11")
	}
	for _, tc := range tests {
		if g := canonicalDiscogsTrackPosition(tc.in); g != tc.want {
			t.Fatalf("canonicalDiscogsTrackPosition(%q) = %q want %q", tc.in, g, tc.want)
		}
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

