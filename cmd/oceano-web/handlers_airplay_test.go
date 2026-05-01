package main

import (
	"bytes"
	"context"
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
	stateJSON := `{"source":"AirPlay","state":"playing","airplay_transport":{"available":true,"session_state":"ready","active_remote":"123456789","dacp_id":"0011223344556677","client_ip":"127.0.0.1"}}`
	if err := os.WriteFile(statePath, []byte(stateJSON), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)

	req := httptest.NewRequest(http.MethodGet, "/api/airplay/transport-capabilities", nil)
	rr := httptest.NewRecorder()
	handleAirPlayTransportCapabilities(configPath).ServeHTTP(rr, req)
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
	handleAirPlayTransportCapabilities(configPath).ServeHTTP(rr, req)
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

func TestAirPlayTransportCapabilities_AmpOff(t *testing.T) {
	origAmpStateFn := airplayTransportAmpPowerStateFn
	airplayTransportAmpPowerStateFn = func() string { return "standby" }
	t.Cleanup(func() { airplayTransportAmpPowerStateFn = origAmpStateFn })

	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "state.json")
	stateJSON := `{"source":"AirPlay","state":"playing","airplay_transport":{"available":true,"session_state":"ready","active_remote":"123","dacp_id":"abc","client_ip":"127.0.0.1"}}`
	if err := os.WriteFile(statePath, []byte(stateJSON), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)

	req := httptest.NewRequest(http.MethodGet, "/api/airplay/transport-capabilities", nil)
	rr := httptest.NewRecorder()
	handleAirPlayTransportCapabilities(configPath).ServeHTTP(rr, req)
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
	if resp.Available || resp.SessionState != "amp_off" || resp.Reason != "amp_off" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAirPlayTransport_ActionInvalid(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"source":"AirPlay","airplay_transport":{"available":true,"session_state":"ready","active_remote":"123","dacp_id":"abc","client_ip":"127.0.0.1"}}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)

	req := httptest.NewRequest(http.MethodPost, "/api/airplay/transport", bytes.NewBufferString(`{"action":"skip"}`))
	rr := httptest.NewRecorder()
	handleAirPlayTransport(configPath, nil).ServeHTTP(rr, req)
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
	handleAirPlayTransport(configPath, nil).ServeHTTP(rr, req)
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
	origResolver := airplayTransportServiceResolver
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
	airplayTransportServiceResolver = staticAirplayTransportResolver{
		host:   "127.0.0.1",
		port:   3689,
		source: "test",
	}
	defer func() {
		airplayTransportHTTPClient = orig
		airplayTransportServiceResolver = origResolver
	}()

	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"source":"AirPlay","airplay_transport":{"available":true,"session_state":"ready","active_remote":"9999","dacp_id":"aa","client_ip":"127.0.0.1"}}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)

	req := httptest.NewRequest(http.MethodPost, "/api/airplay/transport", bytes.NewBufferString(`{"action":"next"}`))
	rr := httptest.NewRecorder()
	handleAirPlayTransport(configPath, nil).ServeHTTP(rr, req)
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

func TestAirPlayTransport_RateLimited(t *testing.T) {
	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"source":"AirPlay","airplay_transport":{"available":true,"session_state":"ready","active_remote":"9999","dacp_id":"aa","client_ip":"127.0.0.1"}}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	configPath := writeTestConfigWithStateFile(t, statePath)
	limiter := newAirplayTransportRateLimiter(30 * time.Second)
	origResolver := airplayTransportServiceResolver
	airplayTransportServiceResolver = staticAirplayTransportResolver{
		host:   "127.0.0.1",
		port:   3689,
		source: "test",
	}
	defer func() { airplayTransportServiceResolver = origResolver }()

	req1 := httptest.NewRequest(http.MethodPost, "/api/airplay/transport", bytes.NewBufferString(`{"action":"next"}`))
	rr1 := httptest.NewRecorder()
	handleAirPlayTransport(configPath, limiter).ServeHTTP(rr1, req1)

	req2 := httptest.NewRequest(http.MethodPost, "/api/airplay/transport", bytes.NewBufferString(`{"action":"next"}`))
	rr2 := httptest.NewRecorder()
	handleAirPlayTransport(configPath, limiter).ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d: %s", rr2.Code, rr2.Body.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type staticAirplayTransportResolver struct {
	host   string
	port   int
	source string
	err    error
}

func (s staticAirplayTransportResolver) Resolve(_ context.Context, _ string, _ string) (string, int, string, error) {
	if s.err != nil {
		return "", 0, "", s.err
	}
	return s.host, s.port, s.source, nil
}

func (s staticAirplayTransportResolver) WarmUp(_ string, _ string) {}
