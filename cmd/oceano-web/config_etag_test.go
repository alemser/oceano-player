package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAPIGetConfig_ETag_NotModified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rr := httptest.NewRecorder()
	apiGetConfig(rr, req, path)
	if rr.Code != http.StatusOK {
		t.Fatalf("first GET: %d", rr.Code)
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req2.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	apiGetConfig(rr2, req2, path)
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("conditional GET: got %d want 304", rr2.Code)
	}
	if rr2.Body.Len() != 0 {
		t.Fatalf("304 body should be empty, got %q", rr2.Body.String())
	}
}

func TestConfigETagMatches(t *testing.T) {
	etag := `"aabbccdd"`
	if !configETagMatches(`W/"aabbccdd"`, etag) {
		t.Fatal("weak form should match")
	}
	if !configETagMatches(`"aabbccdd"`, etag) {
		t.Fatal("strong form should match")
	}
	if configETagMatches(`"deadbeef"`, etag) {
		t.Fatal("different hash should not match")
	}
	if configETagMatches("", etag) {
		t.Fatal("empty If-None-Match should not match")
	}
}
