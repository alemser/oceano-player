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

	"github.com/alemser/oceano-player/internal/amplifier"
)

// --- test fixtures ---

var apiTestInputs = []amplifier.Input{
	{Label: "USB Audio", ID: "USB"},
	{Label: "Phono", ID: "PHONO"},
	{Label: "CD", ID: "CD"},
}

var apiCycleIRCodes = map[string]string{
	"power_on":    "IR_ON",
	"power_off":   "IR_OFF",
	"volume_up":   "IR_VOL_UP",
	"volume_down": "IR_VOL_DOWN",
	"next_input":  "IR_NEXT",
}

var apiCDIRCodes = map[string]string{
	"power_on":  "IR_CD_ON",
	"power_off": "IR_CD_OFF",
	"play":      "IR_PLAY",
	"pause":     "IR_PAUSE",
	"stop":      "IR_STOP",
	"next":      "IR_NEXT_T",
	"previous":  "IR_PREV_T",
	"eject":     "IR_EJECT",
}

func newTestAmp(t *testing.T) (*amplifier.BroadlinkAmplifier, *amplifier.MockBroadlinkClient) {
	t.Helper()
	mock := &amplifier.MockBroadlinkClient{}
	amp, err := amplifier.NewBroadlinkAmplifier(mock, amplifier.AmplifierSettings{
		Maker:           "Magnat",
		Model:           "MR 780",
		Inputs:          apiTestInputs,
		DefaultInputID:  "USB",
		WarmupSecs:      0,
		SwitchDelaySecs: 0,
		InputMode:       amplifier.InputSelectionCycle,
		IRCodes:         apiCycleIRCodes,
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}
	return amp, mock
}

func newTestCDPlayer() (*amplifier.BroadlinkCDPlayer, *amplifier.MockBroadlinkClient) {
	mock := &amplifier.MockBroadlinkClient{}
	cd := amplifier.NewBroadlinkCDPlayer(mock, amplifier.CDPlayerSettings{
		Maker:   "Yamaha",
		Model:   "CD-S300",
		IRCodes: apiCDIRCodes,
	})
	return cd, mock
}

func newTestServer(t *testing.T, amp *amplifier.BroadlinkAmplifier, cd *amplifier.BroadlinkCDPlayer) *amplifierServer {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	_ = saveConfig(cfgPath, defaultConfig())
	return &amplifierServer{
		configPath: cfgPath,
		amp:        amp,
		cdPlayer:   cd,
		// Stub pairing function: no subprocess, no file system required.
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
	s := newTestServer(t, nil, nil)
	w := do(t, s.handleAmplifierState, http.MethodGet, "/api/amplifier/state", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestAmplifierState_OK(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp, nil)

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
	if len(resp.InputList) != 3 {
		t.Errorf("expected 3 inputs, got %d", len(resp.InputList))
	}
	if resp.CurrentInput.ID != "USB" {
		t.Errorf("expected default input USB, got %q", resp.CurrentInput.ID)
	}
}

func TestAmplifierState_WrongMethod(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp, nil)
	w := do(t, s.handleAmplifierState, http.MethodPost, "/api/amplifier/state", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestAmplifierState_ReflectsStateChanges(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp, nil)

	_ = amp.PowerOn()
	time.Sleep(20 * time.Millisecond) // warmup=0, so audioReady fires quickly

	w := do(t, s.handleAmplifierState, http.MethodGet, "/api/amplifier/state", "")
	var resp amplifierStateResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !resp.PowerOn {
		t.Error("expected power_on=true")
	}
	if !resp.AudioReady {
		t.Error("expected audio_ready=true after warmup")
	}
}

// --- /api/amplifier/power ---

func TestAmplifierPower_On(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp, nil)

	w := do(t, s.handleAmplifierPower, http.MethodPost, "/api/amplifier/power", `{"action":"on"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_ON" {
		t.Errorf("expected [IR_ON] sent, got %v", mock.Sent)
	}
}

func TestAmplifierPower_Off(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp, nil)

	w := do(t, s.handleAmplifierPower, http.MethodPost, "/api/amplifier/power", `{"action":"off"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_OFF" {
		t.Errorf("expected [IR_OFF] sent, got %v", mock.Sent)
	}
}

func TestAmplifierPower_InvalidAction(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp, nil)
	w := do(t, s.handleAmplifierPower, http.MethodPost, "/api/amplifier/power", `{"action":"toggle"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAmplifierPower_NotConfigured(t *testing.T) {
	s := newTestServer(t, nil, nil)
	w := do(t, s.handleAmplifierPower, http.MethodPost, "/api/amplifier/power", `{"action":"on"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

// --- /api/amplifier/volume ---

func TestAmplifierVolume_Up(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp, nil)

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
	s := newTestServer(t, amp, nil)
	w := do(t, s.handleAmplifierVolume, http.MethodPost, "/api/amplifier/volume", `{"direction":"left"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// --- /api/amplifier/input ---

func TestAmplifierInput_ValidID(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp, nil)

	// USB → CD = 2 steps
	w := do(t, s.handleAmplifierInput, http.MethodPost, "/api/amplifier/input", `{"id":"CD"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 2 {
		t.Errorf("expected 2 IR codes for USB→CD, got %d: %v", len(mock.Sent), mock.Sent)
	}
}

func TestAmplifierInput_UnknownID(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp, nil)
	w := do(t, s.handleAmplifierInput, http.MethodPost, "/api/amplifier/input", `{"id":"HDMI"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestAmplifierInput_MissingID(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp, nil)
	w := do(t, s.handleAmplifierInput, http.MethodPost, "/api/amplifier/input", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// --- /api/amplifier/next-input ---

func TestAmplifierNextInput_OK(t *testing.T) {
	amp, mock := newTestAmp(t)
	s := newTestServer(t, amp, nil)

	w := do(t, s.handleAmplifierNextInput, http.MethodPost, "/api/amplifier/next-input", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_NEXT" {
		t.Errorf("expected [IR_NEXT], got %v", mock.Sent)
	}
}

// --- /api/cdplayer/state ---

func TestCDPlayerState_NotConfigured(t *testing.T) {
	s := newTestServer(t, nil, nil)
	w := do(t, s.handleCDPlayerState, http.MethodGet, "/api/cdplayer/state", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestCDPlayerState_OK(t *testing.T) {
	cd, _ := newTestCDPlayer()
	s := newTestServer(t, nil, cd)

	w := do(t, s.handleCDPlayerState, http.MethodGet, "/api/cdplayer/state", "")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}

	var resp cdPlayerStateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Maker != "Yamaha" || resp.Model != "CD-S300" {
		t.Errorf("unexpected identity: %s %s", resp.Maker, resp.Model)
	}
	// All query fields must be null (IR protocol doesn't support them)
	if resp.Track != nil || resp.IsPlaying != nil || resp.TotalTracks != nil {
		t.Error("expected null query fields for IR-only CD player")
	}
}

// --- /api/cdplayer/transport ---

func TestCDPlayerTransport_Play(t *testing.T) {
	cd, mock := newTestCDPlayer()
	s := newTestServer(t, nil, cd)

	w := do(t, s.handleCDPlayerTransport, http.MethodPost, "/api/cdplayer/transport", `{"action":"play"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_PLAY" {
		t.Errorf("expected [IR_PLAY], got %v", mock.Sent)
	}
}

func TestCDPlayerTransport_AllActions(t *testing.T) {
	cases := []struct {
		action string
		wantIR string
	}{
		{"play", "IR_PLAY"},
		{"pause", "IR_PAUSE"},
		{"stop", "IR_STOP"},
		{"next", "IR_NEXT_T"},
		{"prev", "IR_PREV_T"},
		{"eject", "IR_EJECT"},
	}
	for _, tc := range cases {
		cd, mock := newTestCDPlayer()
		s := newTestServer(t, nil, cd)
		body := `{"action":"` + tc.action + `"}`
		w := do(t, s.handleCDPlayerTransport, http.MethodPost, "/api/cdplayer/transport", body)
		if w.Code != http.StatusOK {
			t.Errorf("%s: want 200, got %d", tc.action, w.Code)
			continue
		}
		if len(mock.Sent) != 1 || mock.Sent[0] != tc.wantIR {
			t.Errorf("%s: expected [%s], got %v", tc.action, tc.wantIR, mock.Sent)
		}
	}
}

func TestCDPlayerTransport_InvalidAction(t *testing.T) {
	cd, _ := newTestCDPlayer()
	s := newTestServer(t, nil, cd)
	w := do(t, s.handleCDPlayerTransport, http.MethodPost, "/api/cdplayer/transport", `{"action":"shuffle"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// --- pairing ---

func TestPairStart_ReturnsWaiting(t *testing.T) {
	s := newTestServer(t, nil, nil)
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
	s := newTestServer(t, nil, nil)
	w := do(t, s.handlePairStart, http.MethodPost, "/api/amplifier/pair-start", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestPairStatus_AfterStart_BecomesSuccess(t *testing.T) {
	s := newTestServer(t, nil, nil)

	// start pairing
	_ = do(t, s.handlePairStart, http.MethodPost, "/api/amplifier/pair-start", `{"host":"192.168.1.100"}`)

	// poll until success or timeout
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
	s := newTestServer(t, nil, nil)
	w := do(t, s.handlePairStatus, http.MethodGet, "/api/amplifier/pair-status", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestPairComplete_WritesTokenToConfig(t *testing.T) {
	s := newTestServer(t, nil, nil)
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
	s := newTestServer(t, nil, nil)
	w := do(t, s.handlePairComplete, http.MethodPost, "/api/amplifier/pair-complete", `{"pairing_id":"pair-1"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestPairComplete_InvalidPairingID(t *testing.T) {
	s := newTestServer(t, nil, nil)
	_ = do(t, s.handlePairStart, http.MethodPost, "/api/amplifier/pair-start", `{"host":"192.168.1.100"}`)

	w := do(t, s.handlePairComplete, http.MethodPost, "/api/amplifier/pair-complete", `{"pairing_id":"wrong","token":"abc123","device_id":"dev456"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// --- buildAmplifierFromConfig / buildCDPlayerFromConfig ---

func TestBuildAmplifierFromConfig_Disabled(t *testing.T) {
	amp, err := buildAmplifierFromConfig(AmplifierConfig{Enabled: false}, "")
	if err != nil || amp != nil {
		t.Errorf("expected nil,nil for disabled amp; got %v, %v", amp, err)
	}
}

func TestBuildAmplifierFromConfig_Enabled(t *testing.T) {
	cfg := AmplifierConfig{
		Enabled:            true,
		Maker:              "Magnat",
		Model:              "MR 780",
		Inputs:             []AmplifierInputConfig{{Label: "USB", ID: "USB"}},
		DefaultInput:       "USB",
		InputSelectionMode: "cycle",
		IRCodes:            map[string]string{"power_on": "IR_ON"},
	}
	amp, err := buildAmplifierFromConfig(cfg, "")
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

func TestBuildCDPlayerFromConfig_Disabled(t *testing.T) {
	cd := buildCDPlayerFromConfig(CDPlayerConfig{Enabled: false}, BroadlinkConfig{})
	if cd != nil {
		t.Error("expected nil for disabled CD player")
	}
}

func TestBuildCDPlayerFromConfig_Enabled(t *testing.T) {
	cfg := CDPlayerConfig{
		Enabled: true,
		Maker:   "Yamaha",
		Model:   "CD-S300",
		IRCodes: map[string]string{"play": "IR_PLAY"},
	}
	cd := buildCDPlayerFromConfig(cfg, BroadlinkConfig{})
	if cd == nil {
		t.Fatal("expected non-nil CD player")
	}
	if cd.Maker() != "Yamaha" {
		t.Errorf("Maker = %q", cd.Maker())
	}
}

// --- content-type response check ---

func TestAmplifierState_ContentType(t *testing.T) {
	amp, _ := newTestAmp(t)
	s := newTestServer(t, amp, nil)
	w := do(t, s.handleAmplifierState, http.MethodGet, "/api/amplifier/state", "")
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// --- jsonError helper ---

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

// Ensure we can write and read a temp config (used by pair-complete test).
func TestSaveAndLoadConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	cfg.Amplifier.Broadlink.Token = "tok"
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
	_ = os.Remove(path)
}
