package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamWantsVU(t *testing.T) {
	if streamWantsVU(httptest.NewRequest(http.MethodGet, "/api/stream", nil)) {
		t.Fatal("default should not want VU")
	}
	if !streamWantsVU(httptest.NewRequest(http.MethodGet, "/api/stream?vu=1", nil)) {
		t.Fatal("vu=1 should enable VU")
	}
	if !streamWantsVU(httptest.NewRequest(http.MethodGet, "/api/stream?foo=x&vu=1", nil)) {
		t.Fatal("vu=1 with other params should enable VU")
	}
}

func TestRewriteStateJSONForClient_stripsVU(t *testing.T) {
	raw := []byte(`{"source":"None","vu":{"left":0.1,"right":0.2},"state":"idle"}`)
	out, err := rewriteStateJSONForClient(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) == string(raw) {
		t.Fatal("expected different JSON after strip")
	}
	if string(out) == "" {
		t.Fatal("empty output")
	}
	out2, err := rewriteStateJSONForClient(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(out2) != string(raw) {
		t.Fatalf("includeVU should preserve bytes, got %q", out2)
	}
}
