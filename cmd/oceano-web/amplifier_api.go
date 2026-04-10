package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

	// pairFn is called by handlePairStart to perform the Broadlink auth handshake.
	// If nil, the real bridge subprocess is used. Injected in tests to avoid I/O.
	pairFn func(host string) (amplifier.BridgePairResult, error)

	pairMu    sync.Mutex
	pairState *pairingAttempt

	learnMu    sync.Mutex
	learnState *learningAttempt
}

// learningAttempt tracks an in-progress or completed IR learning session.
type learningAttempt struct {
	Command string `json:"command"`          // e.g. "power_on"
	Device  string `json:"device"`           // "amplifier" or "cdplayer"
	Status  string `json:"status"`           // "listening", "captured", "timeout", "error"
	Code    string `json:"code,omitempty"`   // base64 IR code on success
	Message string `json:"message,omitempty"` // error detail
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
	mux.HandleFunc("/api/amplifier/prev-input", s.handleAmplifierPrevInput)
	mux.HandleFunc("/api/amplifier/sync-input", s.handleAmplifierSyncInput)
	mux.HandleFunc("/api/amplifier/pair-start", s.handlePairStart)
	mux.HandleFunc("/api/amplifier/pair-status", s.handlePairStatus)
	mux.HandleFunc("/api/amplifier/pair-complete", s.handlePairComplete)
	mux.HandleFunc("/api/broadlink/learn-start", s.handleLearnStart)
	mux.HandleFunc("/api/broadlink/learn-status", s.handleLearnStatus)
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
	// InputSynced is false after startup until at least one IR input command is
	// sent. When false the assumed CurrentInput equals the configured default and
	// may not match the physical amplifier state.
	InputSynced bool `json:"input_synced"`
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
		resp.InputSynced = s.amp.InputSynced()
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

func (s *amplifierServer) handleAmplifierPrevInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}

	if err := s.amp.PrevInput(); err != nil {
		jsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleAmplifierSyncInput updates the assumed current input in software without
// sending any IR command. Use when the amp was changed manually and the UI state
// needs to catch up.
//
// POST /api/amplifier/sync-input
// Body: {"id": "USB_AUDIO"}
func (s *amplifierServer) handleAmplifierSyncInput(w http.ResponseWriter, r *http.Request) {
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

	if err := s.amp.SyncInput(req.ID); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
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

// --- pairing handlers ---

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

	doPair := s.pairFn
	if doPair == nil {
		bridgePath, err := findBridgePath()
		if err != nil {
			jsonError(w, "broadlink bridge not found: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		doPair = func(host string) (amplifier.BridgePairResult, error) {
			return amplifier.BridgePair(bridgePath, host)
		}
	}

	attempt := &pairingAttempt{
		ID:     fmt.Sprintf("pair-%d", time.Now().UnixMilli()),
		Host:   req.Host,
		Status: "waiting",
	}

	s.pairMu.Lock()
	s.pairState = attempt
	s.pairMu.Unlock()

	go func() {
		result, pairErr := doPair(req.Host)
		s.pairMu.Lock()
		defer s.pairMu.Unlock()
		if pairErr != nil {
			attempt.Status = "failure"
			attempt.Message = pairErr.Error()
			return
		}
		attempt.Status = "success"
		attempt.Token = result.Token
		attempt.DeviceID = result.DeviceID
	}()

	jsonOK(w, map[string]string{"pairing_id": attempt.ID, "status": "waiting"})
}

// findBridgePath returns the path to broadlink_bridge.py, searching:
//  1. /usr/local/lib/oceano/broadlink_bridge.py  (installed)
//  2. <binary-dir>/broadlink_bridge.py
//  3. ./scripts/broadlink_bridge.py              (development)
func findBridgePath() (string, error) {
	candidates := []string{
		"/usr/local/lib/oceano/broadlink_bridge.py",
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "broadlink_bridge.py"))
	}
	candidates = append(candidates, "scripts/broadlink_bridge.py")

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("searched %v — install python-broadlink and run install-oceano-web.sh", candidates)
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

// --- IR learning handlers ---

// handleLearnStart puts the RM4 Mini into IR learning mode for one command.
// The learning runs in a goroutine; poll /api/broadlink/learn-status for result.
//
// POST /api/broadlink/learn-start
// Body: {"command": "power_on", "device": "amplifier"|"cdplayer"}
func (s *amplifierServer) handleLearnStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Command string `json:"command"`
		Device  string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Command == "" || req.Device == "" {
		jsonError(w, "command and device are required", http.StatusBadRequest)
		return
	}
	if req.Device != "amplifier" && req.Device != "cdplayer" {
		jsonError(w, `device must be "amplifier" or "cdplayer"`, http.StatusBadRequest)
		return
	}

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		jsonError(w, "failed to load config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	host := cfg.Amplifier.Broadlink.Host
	if host == "" {
		jsonError(w, "Broadlink device not paired — complete pairing first", http.StatusBadRequest)
		return
	}

	bridgePath, err := findBridgePath()
	if err != nil {
		jsonError(w, "broadlink bridge not found: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	attempt := &learningAttempt{
		Command: req.Command,
		Device:  req.Device,
		Status:  "listening",
	}
	s.learnMu.Lock()
	s.learnState = attempt
	s.learnMu.Unlock()

	go func() {
		code, learnErr := amplifier.BridgeLearn(bridgePath, host, 30)

		s.learnMu.Lock()
		if learnErr != nil {
			attempt.Status = "error"
			attempt.Message = learnErr.Error()
			s.learnMu.Unlock()
			return
		}
		attempt.Status = "captured"
		attempt.Code = code
		s.learnMu.Unlock()

		// Persist the code to config immediately.
		s.saveLearnedCode(req.Device, req.Command, code)
	}()

	jsonOK(w, attempt)
}

// handleLearnStatus returns the current state of the active learning session.
//
// GET /api/broadlink/learn-status
func (s *amplifierServer) handleLearnStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.learnMu.Lock()
	state := s.learnState
	s.learnMu.Unlock()

	if state == nil {
		jsonError(w, "no learning session in progress", http.StatusNotFound)
		return
	}

	s.learnMu.Lock()
	copy := *state
	s.learnMu.Unlock()

	jsonOK(w, copy)
}

// saveLearnedCode persists a captured IR code to the config file.
func (s *amplifierServer) saveLearnedCode(device, command, code string) {
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		return
	}
	switch device {
	case "amplifier":
		if cfg.Amplifier.IRCodes == nil {
			cfg.Amplifier.IRCodes = make(map[string]string)
		}
		cfg.Amplifier.IRCodes[command] = code
	case "cdplayer":
		if cfg.CDPlayer.IRCodes == nil {
			cfg.CDPlayer.IRCodes = make(map[string]string)
		}
		cfg.CDPlayer.IRCodes[command] = code
	}
	_ = saveConfig(s.configPath, cfg)
}

// --- config helpers ---

// broadlinkClientFromConfig returns a PythonBroadlinkClient when the Broadlink
// host is configured and the bridge script can be found. Falls back to
// NotImplementedBroadlinkClient so the rest of the amp state machine still works
// (power tracking, input tracking) even without a paired RM4 Mini.
func broadlinkClientFromConfig(host string) amplifier.BroadlinkClient {
	if host == "" {
		return &amplifier.NotImplementedBroadlinkClient{}
	}
	bridgePath, err := findBridgePath()
	if err != nil {
		return &amplifier.NotImplementedBroadlinkClient{}
	}
	return &amplifier.PythonBroadlinkClient{BridgePath: bridgePath, Host: host}
}

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
		broadlinkClientFromConfig(cfg.Broadlink.Host),
		amplifier.AmplifierSettings{
			Maker:               cfg.Maker,
			Model:               cfg.Model,
			Inputs:              inputs,
			DefaultInputID:      cfg.DefaultInput,
			WarmupSecs:          cfg.WarmupSeconds,
			SwitchDelaySecs:     cfg.InputSwitchDelaySeconds,
			InputMode:           mode,
			IRCodes:             cfg.IRCodes,
			VUSocketPath:        vuSocketPath,
			SelectorTimeoutSecs: cfg.SelectorTimeoutSeconds,
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
func buildCDPlayerFromConfig(cfg CDPlayerConfig, ampBroadlink BroadlinkConfig) *amplifier.BroadlinkCDPlayer {
	if !cfg.Enabled {
		return nil
	}
	// CD player shares the amplifier's RM4 Mini (same host).
	host := cfg.Broadlink.Host
	if host == "" {
		host = ampBroadlink.Host
	}
	return amplifier.NewBroadlinkCDPlayer(
		broadlinkClientFromConfig(host),
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
