package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearAnalogEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"ANALOG_INPUT_ENABLED",
		"ANALOG_INPUT_DEVICE",
		"ALSA_DEVICE",
		"ANALOG_METADATA_FILE",
		"ANALOG_CACHE_FILE",
		"ANALOG_INPUT_THRESHOLD",
		"ANALOG_SILENCE_SECONDS",
		"ANALOG_CAPTURE_SECONDS",
		"ANALOG_IDENTIFY_INTERVAL_SECONDS",
		"ANALOG_CONFIDENCE_THRESHOLD",
		"ANALOG_CACHE_TTL_SECONDS",
		"ACOUSTID_API_KEY",
	}
	for _, key := range keys {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv(%q) error = %v", key, err)
		}
	}
}

func TestEnvBool(t *testing.T) {
	t.Run("truthy values", func(t *testing.T) {
		for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
			t.Setenv("TEST_BOOL", value)
			if !envBool("TEST_BOOL", false) {
				t.Fatalf("expected %q to be truthy", value)
			}
		}
	})

	t.Run("fallback when unset", func(t *testing.T) {
		_ = os.Unsetenv("TEST_BOOL")
		if !envBool("TEST_BOOL", true) {
			t.Fatal("expected fallback when env is unset")
		}
	})

	t.Run("false for unknown values", func(t *testing.T) {
		t.Setenv("TEST_BOOL", "maybe")
		if envBool("TEST_BOOL", true) {
			t.Fatal("expected unknown value to be false")
		}
	})
}

func TestEnvIntAndFloat(t *testing.T) {
	t.Setenv("TEST_INT", "123")
	if got := envInt("TEST_INT", 7); got != 123 {
		t.Fatalf("envInt() = %d, want 123", got)
	}

	t.Setenv("TEST_INT", "bad")
	if got := envInt("TEST_INT", 7); got != 7 {
		t.Fatalf("envInt() invalid = %d, want fallback 7", got)
	}

	t.Setenv("TEST_FLOAT", "1.25")
	if got := envFloat("TEST_FLOAT", 0.5); got != 1.25 {
		t.Fatalf("envFloat() = %v, want 1.25", got)
	}

	t.Setenv("TEST_FLOAT", "bad")
	if got := envFloat("TEST_FLOAT", 0.5); got != 0.5 {
		t.Fatalf("envFloat() invalid = %v, want fallback 0.5", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("   ", "", " value ", "other"); got != "value" {
		t.Fatalf("firstNonEmpty() = %q, want %q", got, "value")
	}

	if got := firstNonEmpty("", "   "); got != "" {
		t.Fatalf("firstNonEmpty() = %q, want empty string", got)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	clearAnalogEnv(t)

	cfg := loadConfig()

	if !cfg.Enabled {
		t.Fatal("expected analog input enabled by default")
	}
	if cfg.Device != "hw:1,0" {
		t.Fatalf("Device = %q, want hw:1,0", cfg.Device)
	}
	if cfg.StateFile != "/run/oceano-player/analog-now-playing.json" {
		t.Fatalf("StateFile = %q", cfg.StateFile)
	}
	if cfg.CacheFile != "/var/lib/oceano-player/analog-cache.json" {
		t.Fatalf("CacheFile = %q", cfg.CacheFile)
	}
	if cfg.SignalThreshold != 0.01 {
		t.Fatalf("SignalThreshold = %v, want 0.01", cfg.SignalThreshold)
	}
	if cfg.SilenceSeconds != 6 {
		t.Fatalf("SilenceSeconds = %d, want 6", cfg.SilenceSeconds)
	}
	if cfg.CaptureSeconds != 12 {
		t.Fatalf("CaptureSeconds = %d, want 12", cfg.CaptureSeconds)
	}
	if cfg.IdentifyIntervalSeconds != 45 {
		t.Fatalf("IdentifyIntervalSeconds = %d, want 45", cfg.IdentifyIntervalSeconds)
	}
	if cfg.ConfidenceThreshold != 0.80 {
		t.Fatalf("ConfidenceThreshold = %v, want 0.80", cfg.ConfidenceThreshold)
	}
	if cfg.CacheTTLSeconds != 86400 {
		t.Fatalf("CacheTTLSeconds = %d, want 86400", cfg.CacheTTLSeconds)
	}
}

func TestLoadConfigEnvOverridesAndMinimums(t *testing.T) {
	clearAnalogEnv(t)
	t.Setenv("ANALOG_INPUT_ENABLED", "false")
	t.Setenv("ALSA_DEVICE", "hw:9,9")
	t.Setenv("ANALOG_INPUT_DEVICE", "plughw:CARD=USBADC,DEV=0")
	t.Setenv("ANALOG_METADATA_FILE", "/tmp/state.json")
	t.Setenv("ANALOG_CACHE_FILE", "/tmp/cache.json")
	t.Setenv("ANALOG_INPUT_THRESHOLD", "0.25")
	t.Setenv("ANALOG_SILENCE_SECONDS", "9")
	t.Setenv("ANALOG_CAPTURE_SECONDS", "2")
	t.Setenv("ANALOG_IDENTIFY_INTERVAL_SECONDS", "3")
	t.Setenv("ANALOG_CONFIDENCE_THRESHOLD", "0.91")
	t.Setenv("ANALOG_CACHE_TTL_SECONDS", "12")
	t.Setenv("ACOUSTID_API_KEY", " key ")

	cfg := loadConfig()

	if cfg.Enabled {
		t.Fatal("expected analog input to be disabled")
	}
	if cfg.Device != "plughw:CARD=USBADC,DEV=0" {
		t.Fatalf("Device = %q", cfg.Device)
	}
	if cfg.StateFile != "/tmp/state.json" {
		t.Fatalf("StateFile = %q", cfg.StateFile)
	}
	if cfg.CacheFile != "/tmp/cache.json" {
		t.Fatalf("CacheFile = %q", cfg.CacheFile)
	}
	if cfg.SignalThreshold != 0.25 {
		t.Fatalf("SignalThreshold = %v", cfg.SignalThreshold)
	}
	if cfg.SilenceSeconds != 9 {
		t.Fatalf("SilenceSeconds = %d", cfg.SilenceSeconds)
	}
	if cfg.CaptureSeconds != 6 {
		t.Fatalf("CaptureSeconds = %d, want minimum 6", cfg.CaptureSeconds)
	}
	if cfg.IdentifyIntervalSeconds != 20 {
		t.Fatalf("IdentifyIntervalSeconds = %d, want minimum 20", cfg.IdentifyIntervalSeconds)
	}
	if cfg.ConfidenceThreshold != 0.91 {
		t.Fatalf("ConfidenceThreshold = %v", cfg.ConfidenceThreshold)
	}
	if cfg.CacheTTLSeconds != 60 {
		t.Fatalf("CacheTTLSeconds = %d, want minimum 60", cfg.CacheTTLSeconds)
	}
	if cfg.AcoustIDAPIKey != "key" {
		t.Fatalf("AcoustIDAPIKey = %q, want trimmed key", cfg.AcoustIDAPIKey)
	}
}

func TestAtomicWriteJSON(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "state.json")
	payload := statePayload{Source: "analog", Status: "idle", Title: "Unknown"}

	if err := atomicWriteJSON(path, payload); err != nil {
		t.Fatalf("atomicWriteJSON() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var got statePayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Source != "analog" || got.Status != "idle" {
		t.Fatalf("unexpected state payload: %+v", got)
	}
}

func TestLoadCacheHandlesMissingAndInvalidFiles(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	if got := loadCache(missing); len(got) != 0 {
		t.Fatalf("loadCache(missing) length = %d, want 0", len(got))
	}

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if got := loadCache(invalid); len(got) != 0 {
		t.Fatalf("loadCache(invalid) length = %d, want 0", len(got))
	}
}

func TestSaveAndLoadCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	payload := map[string]cacheEntry{
		"abc": {
			CachedAt: 123,
			Metadata: metadata{Title: "Song", Artist: "Artist", Album: "Album", Confidence: 0.9},
		},
	}

	saveCache(path, payload)
	got := loadCache(path)

	entry, ok := got["abc"]
	if !ok {
		t.Fatal("expected cache entry to exist")
	}
	if entry.Metadata.Title != "Song" || entry.Metadata.Confidence != 0.9 {
		t.Fatalf("unexpected cache entry: %+v", entry)
	}
}

func TestSetError(t *testing.T) {
	state := statePayload{}
	setError(&state, "lookup_failed")
	if state.Error == nil || *state.Error != "lookup_failed" {
		t.Fatalf("Error = %v, want lookup_failed", state.Error)
	}

	setError(&state, "  ")
	if state.Error != nil {
		t.Fatalf("Error = %v, want nil", state.Error)
	}
}

func TestAcoustIDLookupSelectsBestRecording(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("client"); got != "test-key" {
			t.Fatalf("client query = %q, want test-key", got)
		}
		if got := r.URL.Query().Get("fingerprint"); got != "fp123" {
			t.Fatalf("fingerprint query = %q, want fp123", got)
		}
		if got := r.URL.Query().Get("duration"); got != "120" {
			t.Fatalf("duration query = %q, want 120", got)
		}
		_, _ = w.Write([]byte(`{
			"status":"ok",
			"results":[
				{
					"score":0.41,
					"recordings":[{"title":"Lower","artists":[{"name":"M83"}],"releasegroups":[{"title":"Before the Dawn"}],"releases":[{"id":"rel-1"}]}]
				},
				{
					"score":0.93,
					"recordings":[{"title":"Best Match","artists":[{"name":"Best Artist"}],"releasegroups":[{"title":"Best Album"}],"releases":[{"id":"rel-2"}]}]
				}
			]
		}`))
	}))
	defer server.Close()

	oldURL := acoustIDLookupURL
	acoustIDLookupURL = server.URL
	defer func() { acoustIDLookupURL = oldURL }()

	got, err := acoustIDLookup("test-key", "fp123", 120)
	if err != nil {
		t.Fatalf("acoustIDLookup() error = %v", err)
	}
	if got.Title != "Best Match" || got.Artist != "Best Artist" || got.Album != "Best Album" {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	if got.ReleaseID == nil || *got.ReleaseID != "rel-2" {
		t.Fatalf("ReleaseID = %v, want rel-2", got.ReleaseID)
	}
	if got.Confidence != 0.93 {
		t.Fatalf("Confidence = %v, want 0.93", got.Confidence)
	}
}

func TestAcoustIDLookupErrors(t *testing.T) {
	t.Run("non-200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusBadGateway)
		}))
		defer server.Close()

		oldURL := acoustIDLookupURL
		acoustIDLookupURL = server.URL
		defer func() { acoustIDLookupURL = oldURL }()

		_, err := acoustIDLookup("test-key", "fp123", 120)
		if err == nil || !strings.Contains(err.Error(), "status 502") {
			t.Fatalf("expected 502 error, got %v", err)
		}
	})

	t.Run("non-ok payload", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"error"}`))
		}))
		defer server.Close()

		oldURL := acoustIDLookupURL
		acoustIDLookupURL = server.URL
		defer func() { acoustIDLookupURL = oldURL }()

		_, err := acoustIDLookup("test-key", "fp123", 120)
		if err == nil || !strings.Contains(err.Error(), "non-ok") {
			t.Fatalf("expected non-ok error, got %v", err)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"ok","results":[]}`))
		}))
		defer server.Close()

		oldURL := acoustIDLookupURL
		acoustIDLookupURL = server.URL
		defer func() { acoustIDLookupURL = oldURL }()

		_, err := acoustIDLookup("test-key", "fp123", 120)
		if err == nil || !strings.Contains(err.Error(), "no acoustid matches") {
			t.Fatalf("expected no matches error, got %v", err)
		}
	})
}

func TestWithArtworkURLUsesReleaseID(t *testing.T) {
	releaseID := "release/with spaces"
	in := &metadata{Title: "Song", Artist: "Artist", Album: "Album", ReleaseID: &releaseID}

	got := withArtworkURL(in)

	if got.ArtworkURL == nil {
		t.Fatal("expected ArtworkURL to be set")
	}
	if want := "https://coverartarchive.org/release/release%2Fwith%20spaces/front-250"; *got.ArtworkURL != want {
		t.Fatalf("ArtworkURL = %q, want %q", *got.ArtworkURL, want)
	}
}

func TestWithArtworkURLFallsBackToITunes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("term"); got != "Artist Album" {
			t.Fatalf("term query = %q, want Artist Album", got)
		}
		_, _ = w.Write([]byte(`{"results":[{"artworkUrl100":"https://example.test/100x100bb.jpg"}]}`))
	}))
	defer server.Close()

	oldURL := itunesSearchURL
	itunesSearchURL = server.URL
	defer func() { itunesSearchURL = oldURL }()

	got := withArtworkURL(&metadata{Artist: "Artist", Album: "Album"})
	if got.ArtworkURL == nil {
		t.Fatal("expected ArtworkURL to be set")
	}
	if want := "https://example.test/600x600bb.jpg"; *got.ArtworkURL != want {
		t.Fatalf("ArtworkURL = %q, want %q", *got.ArtworkURL, want)
	}
}

func TestWithArtworkURLReturnsInputWhenITunesFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	oldURL := itunesSearchURL
	itunesSearchURL = server.URL
	defer func() { itunesSearchURL = oldURL }()

	in := &metadata{Title: "Song", Artist: "Artist", Album: "Album"}
	got := withArtworkURL(in)
	if got == nil {
		t.Fatal("expected metadata")
	}
	if got.ArtworkURL != nil {
		t.Fatalf("ArtworkURL = %v, want nil", got.ArtworkURL)
	}
	if got.Title != in.Title || got.Artist != in.Artist || got.Album != in.Album {
		t.Fatalf("metadata changed unexpectedly: %+v", got)
	}
}

func TestPtrString(t *testing.T) {
	ptr := ptrString("value")
	if ptr == nil || *ptr != "value" {
		t.Fatalf("ptrString() = %v", ptr)
	}

	*ptr = "changed"
	if *ptr != "changed" {
		t.Fatal("expected pointer to be writable")
	}
}

func TestAtomicWriteJSONFailsForInvalidPath(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "parent-file")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := atomicWriteJSON(filepath.Join(filePath, "child.json"), map[string]string{"a": "b"})
	if err == nil {
		t.Fatal("expected atomicWriteJSON to fail when parent path is a file")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected PathError, got %T", err)
	}
}