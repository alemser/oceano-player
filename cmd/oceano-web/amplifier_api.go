package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/alemser/oceano-player/internal/amplifier"
)

// amplifierServer holds the in-memory state for amplifier and CD player control.
// amp and cdPlayer are nil when the respective device is not configured or enabled.
type amplifierServer struct {
	configPath string
	ampConfig  AmplifierConfig // kept for input visibility filtering
	amp        *amplifier.BroadlinkAmplifier
	cdPlayer   *amplifier.BroadlinkCDPlayer
	monitor    *amplifier.PowerStateMonitor // nil when amp is not configured

	pairMu    sync.Mutex
	pairState *pairingAttempt
}

type pairingAttempt struct {
	ID       string `json:"pairing_id"`
	Host     string `json:"host,omitempty"`
	Status   string `json:"status"` // "waiting", "success", "failure"
	Token    string `json:"token,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	Message  string `json:"message,omitempty"`
}

// registerAmplifierRoutes wires all /api/amplifier/* and /api/cdplayer/* endpoints.
// amp, cdPlayer, and monitor may be nil; affected endpoints return 404 in that case.
func registerAmplifierRoutes(mux *http.ServeMux, amp *amplifier.BroadlinkAmplifier, cdPlayer *amplifier.BroadlinkCDPlayer, monitor *amplifier.PowerStateMonitor, ampConfig AmplifierConfig, configPath string) {
	s := &amplifierServer{
		configPath: configPath,
		ampConfig:  ampConfig,
		amp:        amp,
		cdPlayer:   cdPlayer,
		monitor:    monitor,
	}
	mux.HandleFunc("/api/amplifier/state", s.handleAmplifierState)
	mux.HandleFunc("/api/amplifier/power", s.handleAmplifierPower)
	mux.HandleFunc("/api/amplifier/volume", s.handleAmplifierVolume)
	mux.HandleFunc("/api/amplifier/input", s.handleAmplifierInput)
	mux.HandleFunc("/api/amplifier/next-input", s.handleAmplifierNextInput)
	mux.HandleFunc("/api/amplifier/pair-start", s.handlePairStart)
	mux.HandleFunc("/api/amplifier/pair-status", s.handlePairStatus)
	mux.HandleFunc("/api/amplifier/pair-complete", s.handlePairComplete)
	mux.HandleFunc("/api/cdplayer/state", s.handleCDPlayerState)
	mux.HandleFunc("/api/cdplayer/transport", s.handleCDPlayerTransport)
}

// visibleInputList returns only the inputs marked visible:true in config.
// Falls back to the full amp list if no config inputs exist (e.g. all visible).
func (s *amplifierServer) visibleInputList() []amplifier.Input {
	var visible []amplifier.Input
	for _, inp := range s.ampConfig.Inputs {
		if inp.Visible {
			visible = append(visible, amplifier.Input{Label: inp.Label, ID: inp.ID})
		}
	}
	// If no input is marked visible (e.g. old config without the field), show all.
	if len(visible) == 0 {
		return s.amp.InputList()
	}
	return visible
}

// --- response types ---

type amplifierStateResponse struct {
	Maker                   string            `json:"maker"`
	Model                   string            `json:"model"`
	PowerOn                 bool              `json:"power_on"`
	CurrentInput            amplifier.Input   `json:"current_input"`
	InputList               []amplifier.Input `json:"input_list"`
	DefaultInput            amplifier.Input   `json:"default_input"`
	AudioReady              bool              `json:"audio_ready"`
	AudioReadyAt            *time.Time        `json:"audio_ready_at,omitempty"`
	WarmupSeconds           int               `json:"warmup_seconds"`
	InputSwitchDelaySeconds int               `json:"input_switch_delay_seconds"`
	// DetectedPowerState is the hardware-detected state from the last monitor poll.
	// "on" | "off" | "unknown" — see internal/amplifier for detection strategy.
	DetectedPowerState amplifier.PowerState `json:"detected_power_state"`
	DetectedAt         *time.Time           `json:"detected_at,omitempty"`
	LastUpdated        time.Time            `json:"last_updated"`
}

type cdPlayerStateResponse struct {
	Maker              string    `json:"maker"`
	Model              string    `json:"model"`
	Track              *int      `json:"track"`
	TotalTracks        *int      `json:"total_tracks"`
	IsPlaying          *bool     `json:"is_playing"`
	CurrentTimeSeconds *int      `json:"current_time_seconds"`
	TotalTimeSeconds   *int      `json:"total_time_seconds"`
	LastUpdated        time.Time `json:"last_updated"`
}

// --- amplifier handlers ---

func (s *amplifierServer) handleAmplifierState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Return 404 only when neither the full amp nor the monitor is available.
	if s.amp == nil && s.monitor == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}

	resp := amplifierStateResponse{
		DetectedPowerState: amplifier.PowerStateUnknown,
		LastUpdated:        time.Now(),
	}

	if s.amp != nil {
		powerOn, _ := s.amp.CurrentState()
		currentInput, _ := s.amp.CurrentInput()
		resp.Maker = s.amp.Maker()
		resp.Model = s.amp.Model()
		resp.PowerOn = powerOn
		resp.CurrentInput = currentInput
		resp.InputList = s.visibleInputList()
		resp.DefaultInput = s.amp.DefaultInput()
		resp.AudioReady = s.amp.AudioReady()
		resp.WarmupSeconds = s.amp.WarmupTimeSeconds()
		resp.InputSwitchDelaySeconds = s.amp.InputSwitchDelaySeconds()
		if at := s.amp.AudioReadyAt(); !at.IsZero() {
			resp.AudioReadyAt = &at
		}
	} else {
		// Detection-only mode: identity from monitor's amp; IR fields stay zero.
		a := s.monitor.Amp()
		resp.Maker = a.Maker()
		resp.Model = a.Model()
	}

	if s.monitor != nil {
		detected, detAt := s.monitor.Current()
		resp.DetectedPowerState = detected
		if !detAt.IsZero() {
			resp.DetectedAt = &detAt
		}
	}

	jsonOK(w, resp)
}

func (s *amplifierServer) handleAmplifierPower(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}

	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var err error
	switch req.Action {
	case "on":
		err = s.amp.PowerOn()
	case "off":
		err = s.amp.PowerOff()
	default:
		jsonError(w, `action must be "on" or "off"`, http.StatusBadRequest)
		return
	}

	if err != nil {
		jsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *amplifierServer) handleAmplifierVolume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}

	var req struct {
		Direction string `json:"direction"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var err error
	switch req.Direction {
	case "up":
		err = s.amp.VolumeUp()
	case "down":
		err = s.amp.VolumeDown()
	default:
		jsonError(w, `direction must be "up" or "down"`, http.StatusBadRequest)
		return
	}

	if err != nil {
		jsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *amplifierServer) handleAmplifierInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		jsonError(w, "id is required", http.StatusBadRequest)
		return
	}

	if err := s.amp.SetInput(req.ID); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, amplifier.ErrUnknownInputID) {
			status = http.StatusBadRequest
		}
		jsonError(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *amplifierServer) handleAmplifierNextInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}

	if err := s.amp.NextInput(); err != nil {
		jsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- CD player handlers ---

func (s *amplifierServer) handleCDPlayerState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cdPlayer == nil {
		jsonError(w, "CD player not configured", http.StatusNotFound)
		return
	}

	// Track/time queries are not supported via IR on the CD-S300; all fields are null.
	jsonOK(w, cdPlayerStateResponse{
		Maker:       s.cdPlayer.Maker(),
		Model:       s.cdPlayer.Model(),
		LastUpdated: time.Now(),
	})
}

func (s *amplifierServer) handleCDPlayerTransport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cdPlayer == nil {
		jsonError(w, "CD player not configured", http.StatusNotFound)
		return
	}

	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var err error
	switch req.Action {
	case "play":
		err = s.cdPlayer.Play()
	case "pause":
		err = s.cdPlayer.Pause()
	case "stop":
		err = s.cdPlayer.Stop()
	case "next":
		err = s.cdPlayer.Next()
	case "prev":
		err = s.cdPlayer.Previous()
	case "eject":
		err = s.cdPlayer.Eject()
	default:
		jsonError(w, `action must be "play", "pause", "stop", "next", "prev", or "eject"`, http.StatusBadRequest)
		return
	}

	if err != nil {
		jsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// --- pairing handlers (stubbed; real implementation in Milestone 5) ---

func (s *amplifierServer) handlePairStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Host string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Host == "" {
		jsonError(w, "host is required", http.StatusBadRequest)
		return
	}

	attempt := &pairingAttempt{
		ID:     fmt.Sprintf("pair-%d", time.Now().UnixMilli()),
		Host:   req.Host,
		Status: "waiting",
	}

	s.pairMu.Lock()
	s.pairState = attempt
	s.pairMu.Unlock()

	// Stub: resolve to success after a short delay so the UI sees "waiting" first.
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.pairMu.Lock()
		attempt.Status = "success"
		attempt.Token = "stub-token-000000000000000000000000000000"
		attempt.DeviceID = "stub-device-id-0000"
		attempt.Message = "stub pairing — real handshake requires Broadlink RM4 Mini (Milestone 5)"
		s.pairMu.Unlock()
	}()

	jsonOK(w, map[string]string{"pairing_id": attempt.ID, "status": "waiting"})
}

func (s *amplifierServer) handlePairStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.pairMu.Lock()
	state := s.pairState
	s.pairMu.Unlock()

	if state == nil {
		jsonError(w, "no pairing in progress", http.StatusNotFound)
		return
	}

	s.pairMu.Lock()
	resp := *state // copy under lock
	s.pairMu.Unlock()

	jsonOK(w, resp)
}

func (s *amplifierServer) handlePairComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PairingID string `json:"pairing_id"`
		Token     string `json:"token"`
		DeviceID  string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Token == "" || req.DeviceID == "" {
		jsonError(w, "token and device_id are required", http.StatusBadRequest)
		return
	}

	s.pairMu.Lock()
	active := s.pairState
	s.pairMu.Unlock()
	if active == nil {
		jsonError(w, "no pairing in progress", http.StatusNotFound)
		return
	}
	if req.PairingID == "" || req.PairingID != active.ID {
		jsonError(w, "invalid pairing_id", http.StatusBadRequest)
		return
	}

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		jsonError(w, "failed to load config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	cfg.Amplifier.Broadlink.Host = active.Host
	if cfg.Amplifier.Broadlink.Port == 0 {
		cfg.Amplifier.Broadlink.Port = 80
	}
	cfg.Amplifier.Broadlink.Token = req.Token
	cfg.Amplifier.Broadlink.DeviceID = req.DeviceID
	if err := saveConfig(s.configPath, cfg); err != nil {
		jsonError(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.pairMu.Lock()
	if s.pairState != nil && s.pairState.ID == active.ID {
		s.pairState = nil
	}
	s.pairMu.Unlock()

	w.WriteHeader(http.StatusOK)
}

// --- config helpers ---

// buildAmplifierFromConfig constructs a full BroadlinkAmplifier (IR commands +
// detection) from AmplifierConfig. Returns nil, nil when disabled or when
// inputs are not yet configured (detection-only monitor is used instead).
func buildAmplifierFromConfig(cfg AmplifierConfig, vuSocketPath string) (*amplifier.BroadlinkAmplifier, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if len(cfg.Inputs) == 0 {
		// Inputs not configured yet — IR commands unavailable; use detection-only amp.
		return nil, nil
	}

	inputs := make([]amplifier.Input, len(cfg.Inputs))
	for i, inp := range cfg.Inputs {
		inputs[i] = amplifier.Input{Label: inp.Label, ID: inp.ID}
	}

	mode := amplifier.InputSelectionCycle
	if cfg.InputSelectionMode == string(amplifier.InputSelectionDirect) {
		mode = amplifier.InputSelectionDirect
	}

	return amplifier.NewBroadlinkAmplifier(
		&amplifier.NotImplementedBroadlinkClient{}, // replaced by RealBroadlinkClient in Milestone 5
		amplifier.AmplifierSettings{
			Maker:           cfg.Maker,
			Model:           cfg.Model,
			Inputs:          inputs,
			DefaultInputID:  cfg.DefaultInput,
			WarmupSecs:      cfg.WarmupSeconds,
			SwitchDelaySecs: cfg.InputSwitchDelaySeconds,
			InputMode:       mode,
			IRCodes:         cfg.IRCodes,
			VUSocketPath:    vuSocketPath,
		},
	)
}

// buildDetectionAmpFromConfig constructs a detection-only BroadlinkAmplifier
// used exclusively by PowerStateMonitor when the full amp (inputs) is not yet
// configured. Requires only Maker, Model, and VUSocketPath.
func buildDetectionAmpFromConfig(cfg AmplifierConfig, vuSocketPath string) *amplifier.BroadlinkAmplifier {
	if !cfg.Enabled || cfg.Maker == "" || cfg.Model == "" {
		return nil
	}
	return amplifier.NewBroadlinkAmplifierForDetection(amplifier.AmplifierSettings{
		Maker:        cfg.Maker,
		Model:        cfg.Model,
		VUSocketPath: vuSocketPath,
	})
}

// buildCDPlayerFromConfig constructs a BroadlinkCDPlayer from CDPlayerConfig.
// Returns nil when the CD player is disabled.
func buildCDPlayerFromConfig(cfg CDPlayerConfig) *amplifier.BroadlinkCDPlayer {
	if !cfg.Enabled {
		return nil
	}
	return amplifier.NewBroadlinkCDPlayer(
		&amplifier.NotImplementedBroadlinkClient{}, // replaced by RealBroadlinkClient in Milestone 5
		amplifier.CDPlayerSettings{
			Maker:   cfg.Maker,
			Model:   cfg.Model,
			IRCodes: cfg.IRCodes,
		},
	)
}

// --- shared helpers ---

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
