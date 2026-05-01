package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
	stateJSON := `{"source":"AirPlay","state":"playing","airplay_transport":{"available":true,"session_state":"ready"}}`
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
