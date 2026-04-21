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

	"github.com/alemser/oceano-player/internal/amplifier"
)

// --- test fixtures ---

var apiIRCodes = map[string]string{
	"power_on":    "IR_ON",
	"power_off":   "IR_OFF",
	"volume_up":   "IR_VOL_UP",
	"volume_down": "IR_VOL_DOWN",
	"next_input":  "IR_NEXT",
	"prev_input":  "IR_PREV",
}

func newTestAmp(t *testing.T) (*amplifier.BroadlinkAmplifier, *amplifier.MockBroadlinkClient) {
	t.Helper()
	mock := &amplifier.MockBroadlinkClient{}
	amp, err := amplifier.NewBroadlinkAmplifier(mock, amplifier.AmplifierSettings{
		Maker:   "Magnat",
		Model:   "MR 780",
		IRCodes: apiIRCodes,
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}
	return amp, mock
}

func newTestServer(t *testing.T, amp *amplifier.BroadlinkAmplifier) *amplifierServer {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	_ = saveConfig(cfgPath, defaultConfig())
	return &amplifierServer{
		configPath: cfgPath,
		amp:        amp,
		pairFn: func(host string) (amplifier.BridgePairResult, error) {
			return amplifier.BridgePairResult{
				Token:    "test-token-000000000000000000000000000000",
				DeviceID: "test-device-id-0000",
			}, nil
		},
	}
}

func do(t *testing.T, handler http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != "" {
		bodyReader = bytes.NewReader([]byte(body))
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

// --- /api/amplifier/state ---

func TestAmplifierState_NotConfigured(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handleAmplifierState, http.MethodGet, "/api/amplifier/state", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestAmplifierState_OK(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp)

	w := do(t, s.handleAmplifierState, http.MethodGet, "/api/amplifier/state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp amplifierStateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Maker != "Magnat" || resp.Model != "MR 780" {
		t.Errorf("unexpected identity: %s %s", resp.Maker, resp.Model)
	}
}

func TestAmplifierState_WrongMethod(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp)
	w := do(t, s.handleAmplifierState, http.MethodPost, "/api/amplifier/state", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// --- /api/amplifier/power ---

func TestAmplifierPower_SendsIR(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)

	w := do(t, s.handleAmplifierPower, http.MethodPost, "/api/amplifier/power", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_ON" {
		t.Errorf("expected [IR_ON] sent, got %v", mock.Sent)
	}
}

func TestAmplifierPower_NotConfigured(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handleAmplifierPower, http.MethodPost, "/api/amplifier/power", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// --- /api/amplifier/volume ---

func TestAmplifierVolume_Up(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)

	w := do(t, s.handleAmplifierVolume, http.MethodPost, "/api/amplifier/volume", `{"direction":"up"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_VOL_UP" {
		t.Errorf("expected [IR_VOL_UP], got %v", mock.Sent)
	}
}

func TestAmplifierVolume_InvalidDirection(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp)
	w := do(t, s.handleAmplifierVolume, http.MethodPost, "/api/amplifier/volume", `{"direction":"left"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// --- /api/amplifier/next-input / prev-input ---

func TestAmplifierNextInput_OK(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)

	w := do(t, s.handleAmplifierNextInput, http.MethodPost, "/api/amplifier/next-input", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_NEXT" {
		t.Errorf("expected [IR_NEXT], got %v", mock.Sent)
	}
}

func TestAmplifierPrevInput_OK(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)

	w := do(t, s.handleAmplifierPrevInput, http.MethodPost, "/api/amplifier/prev-input", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_PREV" {
		t.Errorf("expected [IR_PREV], got %v", mock.Sent)
	}
}

func TestAmplifierSelectInput_CycleMode_ArmsThenAdvancesRequestedSteps(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)
	s.waitFn = func(context.Context, time.Duration) error { return nil }

	w := do(t, s.handleAmplifierSelectInput, http.MethodPost, "/api/amplifier/select-input", `{"steps":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 3 || mock.Sent[0] != "IR_NEXT" || mock.Sent[1] != "IR_NEXT" || mock.Sent[2] != "IR_NEXT" {
		t.Fatalf("expected [IR_NEXT IR_NEXT IR_NEXT], got %v", mock.Sent)
	}
}

func TestAmplifierSelectInput_CycleMode_WhileActive_OnlyAdvancesSteps(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)
	s.waitFn = func(context.Context, time.Duration) error { return nil }
	// Simulate that input selection was just activated by a previous quick press.
	s.lastInputNavPressAt = time.Now()

	w := do(t, s.handleAmplifierSelectInput, http.MethodPost, "/api/amplifier/select-input", `{"steps":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 2 || mock.Sent[0] != "IR_NEXT" || mock.Sent[1] != "IR_NEXT" {
		t.Fatalf("expected [IR_NEXT IR_NEXT], got %v", mock.Sent)
	}
}

func TestAmplifierSelectInput_DirectMode_UsesRequestedPressCount(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg.Amplifier.InputMode = "direct"
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	w := do(t, s.handleAmplifierSelectInput, http.MethodPost, "/api/amplifier/select-input", `{"steps":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 2 || mock.Sent[0] != "IR_NEXT" || mock.Sent[1] != "IR_NEXT" {
		t.Fatalf("expected [IR_NEXT IR_NEXT], got %v", mock.Sent)
	}
}

func TestAmplifierSetLastKnownInput_OK(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp)

	w := do(t, s.handleAmplifierSetLastKnownInput, http.MethodPost, "/api/amplifier/last-known-input", `{"input_id":"20"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded.AmplifierRuntime.LastKnownInputID != AmplifierInputID("20") {
		t.Fatalf("last_known_input_id = %q, want 20", loaded.AmplifierRuntime.LastKnownInputID)
	}
}

func TestAmplifierResetUSBInput_RunsWhenPowerNotOn(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)
	s.powerStateFn = func() (amplifier.PowerState, time.Time) {
		return amplifier.PowerStateOff, time.Now()
	}
	s.waitFn = func(context.Context, time.Duration) error { return nil }
	probeCalls := 0
	s.usbProbeFn = func(context.Context) bool {
		probeCalls++
		// initial check=false, after first click=false, after second click=true
		return probeCalls >= 3
	}

	w := do(t, s.handleAmplifierResetUSBInput, http.MethodPost, "/api/amplifier/reset-usb-input", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp resetUSBInputResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "found_usb" || resp.Attempts != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(mock.Sent) != 2 {
		t.Fatalf("expected 2 IR commands, got %d (%v)", len(mock.Sent), mock.Sent)
	}
	for i, ir := range mock.Sent {
		if ir != "IR_PREV" {
			t.Fatalf("IR command %d = %q, want IR_PREV", i, ir)
		}
	}
}

func TestAmplifierResetUSBInput_AlreadyUSB_NoInputChange(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)
	s.powerStateFn = func() (amplifier.PowerState, time.Time) {
		return amplifier.PowerStateOn, time.Now()
	}
	s.usbProbeFn = func(context.Context) bool { return true }

	w := do(t, s.handleAmplifierResetUSBInput, http.MethodPost, "/api/amplifier/reset-usb-input", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp resetUSBInputResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "already_usb" || resp.Attempts != 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(mock.Sent) != 0 {
		t.Fatalf("expected no IR commands, got %v", mock.Sent)
	}
}

func TestAmplifierResetUSBInput_FindsUSBStopsEarly(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)
	s.powerStateFn = func() (amplifier.PowerState, time.Time) {
		return amplifier.PowerStateOn, time.Now()
	}
	s.waitFn = func(context.Context, time.Duration) error { return nil }
	probeCalls := 0
	s.usbProbeFn = func(context.Context) bool {
		probeCalls++
		// Call sequence: initial check, after each first and second click.
		// Succeed after the first click of attempt 3.
		return probeCalls >= 6
	}

	w := do(t, s.handleAmplifierResetUSBInput, http.MethodPost, "/api/amplifier/reset-usb-input", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp resetUSBInputResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "found_usb" || resp.Attempts != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(mock.Sent) != 5 {
		t.Fatalf("expected 5 IR commands, got %d (%v)", len(mock.Sent), mock.Sent)
	}
	for i, ir := range mock.Sent {
		if ir != "IR_PREV" {
			t.Fatalf("IR command %d = %q, want IR_PREV", i, ir)
		}
	}
}

func TestAmplifierResetUSBInput_UsesKnownInputToChooseShorterDirection(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)
	s.powerStateFn = func() (amplifier.PowerState, time.Time) {
		return amplifier.PowerStateOn, time.Now()
	}
	s.waitFn = func(context.Context, time.Duration) error { return nil }

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	// Aux -> USB Audio is one jump forward, three backwards in default inputs.
	cfg.AmplifierRuntime.LastKnownInputID = AmplifierInputID("30")
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	probeCalls := 0
	s.usbProbeFn = func(context.Context) bool {
		probeCalls++
		return probeCalls >= 3 // initial=false, after first press=false, after second=true
	}

	w := do(t, s.handleAmplifierResetUSBInput, http.MethodPost, "/api/amplifier/reset-usb-input", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 2 {
		t.Fatalf("expected 2 IR commands, got %d (%v)", len(mock.Sent), mock.Sent)
	}
	for i, ir := range mock.Sent {
		if ir != "IR_NEXT" {
			t.Fatalf("IR command %d = %q, want IR_NEXT", i, ir)
		}
	}

	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded.AmplifierRuntime.LastKnownInputID != AmplifierInputID("40") {
		t.Fatalf("last_known_input_id = %q, want 40 after reset", loaded.AmplifierRuntime.LastKnownInputID)
	}
}

func TestAmplifierResetUSBInput_ExhaustsAfterConfiguredInputCycle(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)
	s.powerStateFn = func() (amplifier.PowerState, time.Time) {
		return amplifier.PowerStateOn, time.Now()
	}
	s.waitFn = func(context.Context, time.Duration) error { return nil }
	s.usbProbeFn = func(context.Context) bool { return false }

	w := do(t, s.handleAmplifierResetUSBInput, http.MethodPost, "/api/amplifier/reset-usb-input", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp resetUSBInputResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "usb_not_found" || resp.Attempts != 4 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(mock.Sent) != 8 {
		t.Fatalf("expected 8 IR commands, got %d (%v)", len(mock.Sent), mock.Sent)
	}
	for i, ir := range mock.Sent {
		if ir != "IR_PREV" {
			t.Fatalf("IR command %d = %q, want IR_PREV", i, ir)
		}
	}
}

func TestAmplifierResetUSBInput_UsesProfileInputsWhenRawInputsEmpty(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)
	s.powerStateFn = func() (amplifier.PowerState, time.Time) {
		return amplifier.PowerStateOn, time.Now()
	}
	s.waitFn = func(context.Context, time.Duration) error { return nil }
	s.usbProbeFn = func(context.Context) bool { return false }

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	// Keep built-in profile active but clear explicit input list.
	cfg.Amplifier.Inputs = nil
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	w := do(t, s.handleAmplifierResetUSBInput, http.MethodPost, "/api/amplifier/reset-usb-input", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp resetUSBInputResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "usb_not_found" || resp.Attempts != 4 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(mock.Sent) != 8 {
		t.Fatalf("expected 8 IR commands, got %d (%v)", len(mock.Sent), mock.Sent)
	}
}

// --- pairing ---

func TestPairStart_ReturnsWaiting(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handlePairStart, http.MethodPost, "/api/amplifier/pair-start", `{"host":"192.168.1.100"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "waiting" {
		t.Errorf("expected status=waiting, got %q", resp["status"])
	}
	if resp["pairing_id"] == "" {
		t.Error("expected non-empty pairing_id")
	}
}

func TestPairStart_MissingHost(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handlePairStart, http.MethodPost, "/api/amplifier/pair-start", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestPairStatus_AfterStart_BecomesSuccess(t *testing.T) {
	s := newTestServer(t, nil)
	_ = do(t, s.handlePairStart, http.MethodPost, "/api/amplifier/pair-start", `{"host":"192.168.1.100"}`)

	deadline := time.Now().Add(500 * time.Millisecond)
	var last map[string]string
	for time.Now().Before(deadline) {
		w := do(t, s.handlePairStatus, http.MethodGet, "/api/amplifier/pair-status", "")
		_ = json.NewDecoder(w.Body).Decode(&last)
		if last["status"] == "success" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("pairing did not resolve to success within timeout; last status: %q", last["status"])
}

func TestPairStatus_NoPairingInProgress(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handlePairStatus, http.MethodGet, "/api/amplifier/pair-status", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestPairComplete_WritesTokenToConfig(t *testing.T) {
	s := newTestServer(t, nil)
	start := do(t, s.handlePairStart, http.MethodPost, "/api/amplifier/pair-start", `{"host":"192.168.1.100"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("pair-start want 200, got %d: %s", start.Code, start.Body)
	}
	var startResp map[string]string
	if err := json.NewDecoder(start.Body).Decode(&startResp); err != nil {
		t.Fatalf("decode pair-start response: %v", err)
	}
	pairID := startResp["pairing_id"]
	if pairID == "" {
		t.Fatal("expected pairing_id in pair-start response")
	}

	body := `{"pairing_id":"` + pairID + `","token":"abc123","device_id":"dev456"}`
	w := do(t, s.handlePairComplete, http.MethodPost, "/api/amplifier/pair-complete", body)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Amplifier.Broadlink.Token != "abc123" {
		t.Errorf("token not persisted: got %q", cfg.Amplifier.Broadlink.Token)
	}
	if cfg.Amplifier.Broadlink.DeviceID != "dev456" {
		t.Errorf("device_id not persisted: got %q", cfg.Amplifier.Broadlink.DeviceID)
	}
	if cfg.Amplifier.Broadlink.Host != "192.168.1.100" {
		t.Errorf("host not persisted: got %q", cfg.Amplifier.Broadlink.Host)
	}
}

func TestPairComplete_MissingToken(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handlePairComplete, http.MethodPost, "/api/amplifier/pair-complete", `{"pairing_id":"pair-1"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestPairComplete_InvalidPairingID(t *testing.T) {
	s := newTestServer(t, nil)
	_ = do(t, s.handlePairStart, http.MethodPost, "/api/amplifier/pair-start", `{"host":"192.168.1.100"}`)
	w := do(t, s.handlePairComplete, http.MethodPost, "/api/amplifier/pair-complete", `{"pairing_id":"wrong","token":"abc123","device_id":"dev456"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// --- buildAmplifierFromConfig / buildCDPlayerFromConfig ---

func TestBuildAmplifierFromConfig_Disabled(t *testing.T) {
	amp, err := buildAmplifierFromConfig(AmplifierConfig{Enabled: false}, "", "")
	if err != nil || amp != nil {
		t.Errorf("expected nil,nil for disabled amp; got %v, %v", amp, err)
	}
}

func TestBuildAmplifierFromConfig_Enabled(t *testing.T) {
	cfg := AmplifierConfig{
		Enabled: true,
		Maker:   "Magnat",
		Model:   "MR 780",
		IRCodes: map[string]string{"power_on": "IR_ON"},
	}
	amp, err := buildAmplifierFromConfig(cfg, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amp == nil {
		t.Fatal("expected non-nil amp")
	}
	if amp.Maker() != "Magnat" {
		t.Errorf("Maker = %q", amp.Maker())
	}
}

func TestBuildAmplifierFromConfig_ProfileResolved(t *testing.T) {
	cfg := AmplifierConfig{
		Enabled:   true,
		ProfileID: builtInAmplifierProfileMagnatMR780,
		IRCodes:   map[string]string{"power_on": "IR_ON"},
	}
	amp, err := buildAmplifierFromConfig(cfg, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amp == nil {
		t.Fatal("expected non-nil amp")
	}
	if amp.Model() != "MR 780" {
		t.Errorf("Model = %q, want MR 780", amp.Model())
	}
}

func TestPowerCalibrationForConfiguredInput_UsesLastKnownOnly(t *testing.T) {
	advanced := AdvancedConfig{
		CalibrationProfiles: map[string]CalibrationProfile{
			"20": {Off: &CalibrationSample{AvgRMS: 0.007}, On: &CalibrationSample{AvgRMS: 0.013}},
			"30": {Off: &CalibrationSample{AvgRMS: 0.010}, On: &CalibrationSample{AvgRMS: 0.016}},
		},
	}
	runtime := AmplifierRuntimeConfig{LastKnownInputID: AmplifierInputID("30")}

	cal := powerCalibrationForConfiguredInput(advanced, runtime)
	if cal == nil {
		t.Fatal("expected non-nil calibration")
	}
	if cal.InputID != "30" {
		t.Fatalf("InputID = %q, want 30", cal.InputID)
	}
	if cal.OffRMS != 0.010 || cal.OnRMS != 0.016 {
		t.Fatalf("unexpected calibration values: off=%.4f on=%.4f", cal.OffRMS, cal.OnRMS)
	}
}

func TestPowerCalibrationForConfiguredInput_NoFallbackToOtherInputs(t *testing.T) {
	advanced := AdvancedConfig{
		CalibrationProfiles: map[string]CalibrationProfile{
			"20": {Off: &CalibrationSample{AvgRMS: 0.007}, On: &CalibrationSample{AvgRMS: 0.013}},
		},
	}
	runtime := AmplifierRuntimeConfig{LastKnownInputID: AmplifierInputID("30")}

	cal := powerCalibrationForConfiguredInput(advanced, runtime)
	if cal != nil {
		t.Fatalf("expected nil calibration when configured input has no profile, got %+v", cal)
	}
}

func TestResolveAmplifierConfig_LegacyNoProfilePreserved(t *testing.T) {
	legacy := AmplifierConfig{
		Enabled:            true,
		Maker:              "Yamaha",
		Model:              "A-S501",
		WarmUpSecs:         10,
		StandbyTimeoutMins: 15,
	}
	resolved := resolveAmplifierConfig(legacy)
	if resolved.Maker != "Yamaha" || resolved.Model != "A-S501" {
		t.Fatalf("legacy config should be preserved, got maker=%q model=%q", resolved.Maker, resolved.Model)
	}
	if resolved.ProfileID != "" {
		t.Fatalf("legacy config should not force profile id, got %q", resolved.ProfileID)
	}
}

func TestAmplifierInputID_AcceptsStringAndNumber(t *testing.T) {
	var cfg AmplifierConfig
	raw := `{"inputs":[{"id":40,"logical_name":"USB Audio","visible":true},{"id":"200","logical_name":"CD","visible":false}]}`
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Inputs) != 2 {
		t.Fatalf("inputs len = %d, want 2", len(cfg.Inputs))
	}
	if cfg.Inputs[0].ID != AmplifierInputID("40") {
		t.Fatalf("first id = %q, want 40", cfg.Inputs[0].ID)
	}
	if cfg.Inputs[1].ID != AmplifierInputID("200") {
		t.Fatalf("second id = %q, want 200", cfg.Inputs[1].ID)
	}
}

// --- content-type / helpers ---

func TestAmplifierState_ContentType(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp)
	w := do(t, s.handleAmplifierState, http.MethodGet, "/api/amplifier/state", "")
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestJsonError_SetsCodeAndBody(t *testing.T) {
	w := httptest.NewRecorder()
	jsonError(w, "something went wrong", http.StatusBadRequest)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "something went wrong" {
		t.Errorf("unexpected error body: %v", resp)
	}
}

// --- /api/amplifier/power-state ---

func TestHandleAmplifierPowerState_ReturnsUnknownByDefault(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp) // no monitor → unknown

	w := do(t, s.handleAmplifierPowerState, http.MethodGet, "/api/amplifier/power-state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp amplifierPowerStateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PowerState != string(amplifier.PowerStateUnknown) {
		t.Errorf("power_state = %q, want %q", resp.PowerState, amplifier.PowerStateUnknown)
	}
}

func TestHandleAmplifierPowerState_NoAmp(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handleAmplifierPowerState, http.MethodGet, "/api/amplifier/power-state", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestHandleAmplifierPowerState_MethodNotAllowed(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp)
	w := do(t, s.handleAmplifierPowerState, http.MethodPost, "/api/amplifier/power-state", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// --- /api/amplifier/power-on ---

func TestHandleAmplifierPowerOn_SendsIR(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)

	w := do(t, s.handleAmplifierPowerOn, http.MethodPost, "/api/amplifier/power-on", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_ON" {
		t.Errorf("IR sent = %v, want [IR_ON]", mock.Sent)
	}
}

func TestHandleAmplifierPowerOn_NotifiesMonitor(t *testing.T) {
	amp, _ := newTestAmp(t)
	monitor := amplifier.NewPowerStateMonitor(amp, time.Hour, amplifier.MonitorConfig{
		WarmUp: time.Hour,
	})
	s := newTestServer(t, amp)
	s.monitor = monitor

	do(t, s.handleAmplifierPowerOn, http.MethodPost, "/api/amplifier/power-on", "")

	// After NotifyPowerOn the monitor should report WarmingUp when detection is Unknown.
	// We verify indirectly: start monitor, detect (no socket → Unknown), expect WarmingUp.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go monitor.Start(ctx)

	select {
	case <-time.After(12 * time.Second):
		state, _ := monitor.Current()
		if state != amplifier.PowerStateWarmingUp {
			t.Errorf("power state after power-on = %q, want %q", state, amplifier.PowerStateWarmingUp)
		}
	case <-ctx.Done():
	}
}

func TestHandleAmplifierPowerOn_NoAmp(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handleAmplifierPowerOn, http.MethodPost, "/api/amplifier/power-on", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// --- /api/amplifier/power-off ---

func TestHandleAmplifierPowerOff_SendsIR(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp)

	w := do(t, s.handleAmplifierPowerOff, http.MethodPost, "/api/amplifier/power-off", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_OFF" {
		t.Errorf("IR sent = %v, want [IR_OFF]", mock.Sent)
	}
}

func TestHandleAmplifierPowerOff_NoAmp(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handleAmplifierPowerOff, http.MethodPost, "/api/amplifier/power-off", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// --- /api/amplifier/state includes power_state ---

func TestHandleAmplifierState_IncludesPowerState(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp)

	w := do(t, s.handleAmplifierState, http.MethodGet, "/api/amplifier/state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp amplifierStateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PowerState == "" {
		t.Error("power_state field missing in amplifier state response")
	}
}

// --- monitorConfigFromAmplifierConfig ---

func TestMonitorConfigFromAmplifierConfig_DurationConversion(t *testing.T) {
	cfg := AmplifierConfig{
		WarmUpSecs:         45,
		StandbyTimeoutMins: 25,
	}
	mc := monitorConfigFromAmplifierConfig(cfg)

	if mc.WarmUp != 45*time.Second {
		t.Errorf("WarmUp = %v, want 45s", mc.WarmUp)
	}
	if mc.StandbyTimeout != 25*time.Minute {
		t.Errorf("StandbyTimeout = %v, want 25m", mc.StandbyTimeout)
	}
}

// --- buildAmplifierFromConfig propagates timing settings ---

func TestBuildAmplifierFromConfig_PropagatesTimings(t *testing.T) {
	cfg := AmplifierConfig{
		Enabled:            true,
		Maker:              "Magnat",
		Model:              "MR 780",
		IRCodes:            map[string]string{"power_on": "IR_ON"},
		WarmUpSecs:         30,
		StandbyTimeoutMins: 20,
		InputCycling: InputCyclingConfig{
			Enabled:        true,
			Direction:      "prev",
			MaxCycles:      8,
			StepWaitSecs:   3,
			MinSilenceSecs: 120,
		},
	}
	amp, err := buildAmplifierFromConfig(cfg, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amp == nil {
		t.Fatal("expected non-nil amp")
	}
	// Settings are private but we can verify the amp was constructed without error.
	// The MonitorConfig conversion is tested separately.
}

func TestSaveAndLoadConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	cfg.Amplifier.Broadlink.Token = "tok"
	cfg.Amplifier.Inputs = []AmplifierInputConfig{{ID: AmplifierInputID("40"), LogicalName: "USB Audio", Visible: true}}
	cfg.AmplifierProfiles = []StoredAmplifierProfile{{
		ID:     "custom_one",
		Name:   "Custom One",
		Origin: "custom",
		Config: AmplifierConfig{Maker: "Acme", Model: "A1", InputMode: "cycle"},
	}}
	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	loaded, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded.Amplifier.Broadlink.Token != "tok" {
		t.Errorf("round-trip failed: got %q", loaded.Amplifier.Broadlink.Token)
	}
	if len(loaded.Amplifier.Inputs) != 1 || loaded.Amplifier.Inputs[0].ID != AmplifierInputID("40") {
		t.Fatalf("amplifier inputs round-trip failed: %+v", loaded.Amplifier.Inputs)
	}
	if len(loaded.AmplifierProfiles) != 1 || loaded.AmplifierProfiles[0].ID != "custom_one" {
		t.Fatalf("amplifier profiles round-trip failed: %+v", loaded.AmplifierProfiles)
	}
	_ = os.Remove(path)
}

func TestAmplifierProfiles_ListIncludesBuiltinAndCustom(t *testing.T) {
	s := newTestServer(t, nil)
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg.AmplifierProfiles = []StoredAmplifierProfile{{
		ID:     "custom_amp",
		Name:   "Custom Amp",
		Origin: "custom",
		Config: AmplifierConfig{Maker: "Acme", Model: "A1", InputMode: "cycle"},
	}}
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	w := do(t, s.handleAmplifierProfiles, http.MethodGet, "/api/amplifier/profiles", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp amplifierProfilesListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Profiles) < 2 {
		t.Fatalf("expected builtin+custom profiles, got %d", len(resp.Profiles))
	}
}

func TestAmplifierProfileActivate_Builtin(t *testing.T) {
	s := newTestServer(t, nil)
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg.Amplifier.Enabled = true
	cfg.Amplifier.Broadlink.Token = "tok"
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	w := do(t, s.handleAmplifierProfileActivate, http.MethodPost, "/api/amplifier/profiles/activate", `{"profile_id":"magnat_mr780"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded.Amplifier.ProfileID != "magnat_mr780" {
		t.Fatalf("profile id not activated, got %q", loaded.Amplifier.ProfileID)
	}
	if loaded.Amplifier.Broadlink.Token != "tok" {
		t.Fatalf("expected token preserved, got %q", loaded.Amplifier.Broadlink.Token)
	}
}

func TestAmplifierProfileExport_SafeRedactsSecrets(t *testing.T) {
	s := newTestServer(t, nil)
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg.AmplifierProfiles = []StoredAmplifierProfile{{
		ID:   "custom_amp",
		Name: "Custom Amp",
		Config: AmplifierConfig{
			Maker: "Acme", Model: "A1", InputMode: "cycle",
			Broadlink: BroadlinkConfig{Host: "192.168.1.8", Token: "tok", DeviceID: "did"},
		},
	}}
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/amplifier/profiles/export?profile_id=custom_amp&mode=safe", nil)
	w := httptest.NewRecorder()
	s.handleAmplifierProfileExport(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var doc amplifierProfileExportDoc
	if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Profile.Config.Broadlink.Token != "" || doc.Profile.Config.Broadlink.DeviceID != "" {
		t.Fatalf("safe export must redact token/device id, got %+v", doc.Profile.Config.Broadlink)
	}
}

func TestAmplifierProfileImport_StoresProfile(t *testing.T) {
	s := newTestServer(t, nil)
	body := `{
		"schema_version":"1.0",
		"profile":{
			"id":"imported_amp",
			"name":"Imported Amp",
			"origin":"imported",
			"config":{
				"maker":"Acme",
				"model":"A2",
				"input_mode":"cycle",
				"inputs":[{"id":1,"logical_name":"USB","visible":true}]
			}
		}
	}`
	w := do(t, s.handleAmplifierProfileImport, http.MethodPost, "/api/amplifier/profiles/import", body)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if _, _, ok := findStoredAmplifierProfile(loaded.AmplifierProfiles, "imported_amp"); !ok {
		t.Fatalf("imported profile not found: %+v", loaded.AmplifierProfiles)
	}
}

func TestAmplifierProfilesUpsert_Custom(t *testing.T) {
	s := newTestServer(t, nil)
	body := `{
		"id":"my_custom",
		"name":"My Custom",
		"origin":"custom",
		"config":{
			"maker":"Acme",
			"model":"A9",
			"input_mode":"cycle",
			"inputs":[{"id":"10","logical_name":"USB","visible":true}]
		}
	}`
	w := do(t, s.handleAmplifierProfiles, http.MethodPost, "/api/amplifier/profiles", body)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if _, _, ok := findStoredAmplifierProfile(loaded.AmplifierProfiles, "my_custom"); !ok {
		t.Fatalf("custom profile not saved: %+v", loaded.AmplifierProfiles)
	}
}

func TestAmplifierProfilesDelete_Custom(t *testing.T) {
	s := newTestServer(t, nil)
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg.AmplifierProfiles = []StoredAmplifierProfile{{
		ID:     "to_delete",
		Name:   "To Delete",
		Origin: "custom",
		Config: AmplifierConfig{Maker: "Acme", Model: "A1", Inputs: []AmplifierInputConfig{{ID: "10", LogicalName: "USB", Visible: true}}},
	}}
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/api/amplifier/profiles?profile_id=to_delete", nil)
	w := httptest.NewRecorder()
	s.handleAmplifierProfiles(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if _, _, ok := findStoredAmplifierProfile(loaded.AmplifierProfiles, "to_delete"); ok {
		t.Fatalf("profile should be deleted: %+v", loaded.AmplifierProfiles)
	}
}

func TestAmplifierProfileActivate_Builtin_PreservesIRCodes(t *testing.T) {
	s := newTestServer(t, nil)
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg.Amplifier.IRCodes = map[string]string{
		"power_on":  "LEARNED_ON",
		"power_off": "LEARNED_OFF",
	}
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{{ID: "cd1", Name: "Yamaha CD"}}
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	w := do(t, s.handleAmplifierProfileActivate, http.MethodPost, "/api/amplifier/profiles/activate", `{"profile_id":"magnat_mr780"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded.Amplifier.IRCodes["power_on"] != "LEARNED_ON" {
		t.Errorf("IR code power_on not preserved: got %q", loaded.Amplifier.IRCodes["power_on"])
	}
	if loaded.Amplifier.IRCodes["power_off"] != "LEARNED_OFF" {
		t.Errorf("IR code power_off not preserved: got %q", loaded.Amplifier.IRCodes["power_off"])
	}
	if len(loaded.Amplifier.ConnectedDevices) != 1 || loaded.Amplifier.ConnectedDevices[0].ID != "cd1" {
		t.Errorf("connected devices not preserved: %+v", loaded.Amplifier.ConnectedDevices)
	}
}

func TestAmplifierProfileActivate_Custom(t *testing.T) {
	s := newTestServer(t, nil)
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg.AmplifierProfiles = []StoredAmplifierProfile{{
		ID:     "my_amp",
		Name:   "My Amp",
		Origin: "custom",
		Config: AmplifierConfig{
			ProfileID: "my_amp",
			Maker:     "Acme",
			Model:     "A9",
			InputMode: "cycle",
			Inputs:    []AmplifierInputConfig{{ID: "10", LogicalName: "USB", Visible: true}},
			IRCodes:   map[string]string{"power_on": "CUSTOM_ON"},
		},
	}}
	cfg.Amplifier.Enabled = true
	cfg.Amplifier.Broadlink.Token = "tok"
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	w := do(t, s.handleAmplifierProfileActivate, http.MethodPost, "/api/amplifier/profiles/activate", `{"profile_id":"my_amp"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded.Amplifier.ProfileID != "my_amp" {
		t.Fatalf("profile_id = %q, want my_amp", loaded.Amplifier.ProfileID)
	}
	if loaded.Amplifier.Maker != "Acme" {
		t.Errorf("maker = %q, want Acme", loaded.Amplifier.Maker)
	}
	if loaded.Amplifier.IRCodes["power_on"] != "CUSTOM_ON" {
		t.Errorf("IR code not applied from profile: got %q", loaded.Amplifier.IRCodes["power_on"])
	}
	if !loaded.Amplifier.Enabled {
		t.Error("enabled flag should be preserved from previous config")
	}
	if loaded.Amplifier.Broadlink.Token != "tok" {
		t.Errorf("broadlink token not preserved: got %q", loaded.Amplifier.Broadlink.Token)
	}
}

func TestAmplifierProfileActivate_UnknownProfile_Returns404(t *testing.T) {
	s := newTestServer(t, nil)
	w := do(t, s.handleAmplifierProfileActivate, http.MethodPost, "/api/amplifier/profiles/activate", `{"profile_id":"does_not_exist"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d: %s", w.Code, w.Body)
	}
}

func TestSaveLearnedCode_MirrorsToActiveStoredProfile(t *testing.T) {
	s := newTestServer(t, nil)
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	cfg.AmplifierProfiles = []StoredAmplifierProfile{{
		ID:     "my_amp",
		Name:   "My Amp",
		Origin: "custom",
		Config: AmplifierConfig{
			ProfileID: "my_amp",
			Maker:     "Acme",
			Model:     "A9",
			InputMode: "cycle",
			Inputs:    []AmplifierInputConfig{{ID: "10", LogicalName: "USB", Visible: true}},
		},
	}}
	cfg.Amplifier.ProfileID = "my_amp"
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	s.saveLearnedCode("amplifier", "power_on", "LEARNED_CODE")

	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded.Amplifier.IRCodes["power_on"] != "LEARNED_CODE" {
		t.Errorf("code not saved to cfg.Amplifier: got %q", loaded.Amplifier.IRCodes["power_on"])
	}
	p, _, ok := findStoredAmplifierProfile(loaded.AmplifierProfiles, "my_amp")
	if !ok {
		t.Fatal("stored profile not found")
	}
	if p.Config.IRCodes["power_on"] != "LEARNED_CODE" {
		t.Errorf("code not mirrored to stored profile: got %q", p.Config.IRCodes["power_on"])
	}
}

func TestSaveLearnedCode_BuiltinProfileNotInStore(t *testing.T) {
	s := newTestServer(t, nil)
	// Active profile is the built-in magnat_mr780 (default config).
	// saveLearnedCode should persist the code to cfg.Amplifier.IRCodes only,
	// without crashing when the active ID is not in AmplifierProfiles.
	s.saveLearnedCode("amplifier", "power_on", "BUILTIN_CODE")

	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded.Amplifier.IRCodes["power_on"] != "BUILTIN_CODE" {
		t.Errorf("code not saved for built-in active profile: got %q", loaded.Amplifier.IRCodes["power_on"])
	}
}

func TestAmplifierProfileActivate_Builtin_PreservesConnectedDevicesOnCustomIfEmpty(t *testing.T) {
	s := newTestServer(t, nil)
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	// Custom profile with no connected_devices (e.g. cloned from built-in).
	cfg.AmplifierProfiles = []StoredAmplifierProfile{{
		ID:     "clone_amp",
		Name:   "Clone",
		Origin: "custom",
		Config: AmplifierConfig{
			ProfileID: "clone_amp",
			Maker:     "Magnat",
			Model:     "MR 780",
			InputMode: "cycle",
			Inputs:    []AmplifierInputConfig{{ID: "10", LogicalName: "Phono", Visible: false}},
		},
	}}
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{{ID: "cd1", Name: "Yamaha CD"}}
	if err := saveConfig(s.configPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	w := do(t, s.handleAmplifierProfileActivate, http.MethodPost, "/api/amplifier/profiles/activate", `{"profile_id":"clone_amp"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	loaded, err := loadConfig(s.configPath)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(loaded.Amplifier.ConnectedDevices) != 1 || loaded.Amplifier.ConnectedDevices[0].ID != "cd1" {
		t.Errorf("connected devices not preserved from previous config: %+v", loaded.Amplifier.ConnectedDevices)
	}
}

func TestAmplifierProfileExport_Builtin(t *testing.T) {
	s := newTestServer(t, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/amplifier/profiles/export?profile_id=magnat_mr780&mode=safe", nil)
	w := httptest.NewRecorder()
	s.handleAmplifierProfileExport(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var doc amplifierProfileExportDoc
	if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Profile.ID != builtInAmplifierProfileMagnatMR780 {
		t.Errorf("profile id = %q, want %q", doc.Profile.ID, builtInAmplifierProfileMagnatMR780)
	}
	if doc.SchemaVersion != amplifierProfileSchemaVersion {
		t.Errorf("schema_version = %q, want %q", doc.SchemaVersion, amplifierProfileSchemaVersion)
	}
}

func TestAmplifierProfileImport_UnsupportedSchemaVersion(t *testing.T) {
	s := newTestServer(t, nil)
	body := `{
		"schema_version":"2.0",
		"profile":{"id":"x","name":"X","config":{"maker":"A","model":"B"}}
	}`
	w := do(t, s.handleAmplifierProfileImport, http.MethodPost, "/api/amplifier/profiles/import", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", w.Code, w.Body)
	}
}

func TestAmplifierProfileImport_DirectModeRequiresInputIRCodes(t *testing.T) {
	s := newTestServer(t, nil)
	body := `{
		"schema_version":"1.0",
		"profile":{
			"id":"direct_amp",
			"name":"Direct Amp",
			"origin":"imported",
			"config":{
				"maker":"Acme",
				"model":"D1",
				"input_mode":"direct",
				"inputs":[{"id":1,"logical_name":"USB","visible":true}],
				"ir_codes":{}
			}
		}
	}`
	w := do(t, s.handleAmplifierProfileImport, http.MethodPost, "/api/amplifier/profiles/import", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body)
	}
}
