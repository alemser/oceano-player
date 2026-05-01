package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestConfigWithStateFile(t *testing.T, statePath string) string {
	t.Helper()
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	cfg := defaultConfig()
	cfg.Advanced.StateFile = statePath
	if err := saveConfig(configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	return configPath
}

func TestAirPlayTransportCapabilities_Ready(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "state.json")
	stateJSON := `{"source":"AirPlay","state":"playing"}`
	if err := os.WriteFile(statePath, []byte(stateJSON), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)
	reader := staticDACPContextReader{ctx: airplayDACPContext{
		ActiveRemote: "123456789",
		DACPID:       "0011223344556677",
		ClientIP:     "127.0.0.1",
		UpdatedAt:    time.Now(),
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/airplay/transport-capabilities", nil)
	rr := httptest.NewRecorder()
	handleAirPlayTransportCapabilities(configPath, reader).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Available    bool   `json:"available"`
		SessionState string `json:"session_state"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Available || resp.SessionState != "ready" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAirPlayTransportCapabilities_NoSession(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "state.json")
	stateJSON := `{"source":"None","state":"stopped"}`
	if err := os.WriteFile(statePath, []byte(stateJSON), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)

	req := httptest.NewRequest(http.MethodGet, "/api/airplay/transport-capabilities", nil)
	rr := httptest.NewRecorder()
	handleAirPlayTransportCapabilities(configPath, staticDACPContextReader{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Available    bool   `json:"available"`
		SessionState string `json:"session_state"`
		Reason       string `json:"reason"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Available || resp.SessionState != "no_airplay_session" || resp.Reason != "no_airplay_session" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAirPlayTransport_ActionInvalid(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"source":"AirPlay"}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)
	reader := staticDACPContextReader{ctx: airplayDACPContext{
		ActiveRemote: "123",
		DACPID:       "abc",
		ClientIP:     "127.0.0.1",
		UpdatedAt:    time.Now(),
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/airplay/transport", bytes.NewBufferString(`{"action":"skip"}`))
	rr := httptest.NewRecorder()
	handleAirPlayTransport(configPath, reader).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAirPlayTransport_NoSession(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"source":"None"}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)
	req := httptest.NewRequest(http.MethodPost, "/api/airplay/transport", bytes.NewBufferString(`{"action":"pause"}`))
	rr := httptest.NewRecorder()
	handleAirPlayTransport(configPath, staticDACPContextReader{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAirPlayTransport_ExecutesCommand(t *testing.T) {
	var gotPath string
	var gotActiveRemote string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotActiveRemote = r.Header.Get("Active-Remote")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	orig := airplayTransportHTTPClient
	transport := srv.Client().Transport
	airplayTransportHTTPClient = &http.Client{
		Timeout: 2 * time.Second,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			u := *req.URL
			u.Scheme = "http"
			u.Host = strings.TrimPrefix(srv.URL, "http://")
			clone := req.Clone(req.Context())
			clone.URL = &u
			return transport.RoundTrip(clone)
		}),
	}
	defer func() { airplayTransportHTTPClient = orig }()

	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"source":"AirPlay"}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)
	reader := staticDACPContextReader{ctx: airplayDACPContext{
		ActiveRemote: "9999",
		DACPID:       "aa",
		ClientIP:     "127.0.0.1",
		UpdatedAt:    time.Now(),
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/airplay/transport", bytes.NewBufferString(`{"action":"next"}`))
	rr := httptest.NewRecorder()
	handleAirPlayTransport(configPath, reader).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if gotPath != "/ctrl-int/1/nextitem" {
		t.Fatalf("unexpected DACP path: %q", gotPath)
	}
	if gotActiveRemote != "9999" {
		t.Fatalf("unexpected Active-Remote header: %q", gotActiveRemote)
	}
}

type staticDACPContextReader struct {
	ctx airplayDACPContext
}

func (s staticDACPContextReader) Snapshot() airplayDACPContext {
	return s.ctx
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
