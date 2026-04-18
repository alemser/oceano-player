package main

import (
	"context"
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
	amp        *amplifier.BroadlinkAmplifier
	cdPlayer   *amplifier.BroadlinkCDPlayer
	monitor    *amplifier.PowerStateMonitor

	// usbProbeFn and waitFn are optional test hooks.
	powerStateFn func() (amplifier.PowerState, time.Time)
	usbProbeFn   func(ctx context.Context) bool
	waitFn       func(ctx context.Context, d time.Duration) error

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
	Command string `json:"command"`           // e.g. "power_on"
	Device  string `json:"device"`            // "amplifier" or "cdplayer"
	Status  string `json:"status"`            // "listening", "captured", "timeout", "error"
	Code    string `json:"code,omitempty"`    // base64 IR code on success
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
// amp, monitor and cdPlayer may be nil; affected endpoints return 404 in that case.
func registerAmplifierRoutes(mux *http.ServeMux, amp *amplifier.BroadlinkAmplifier, monitor *amplifier.PowerStateMonitor, cdPlayer *amplifier.BroadlinkCDPlayer, configPath string) *amplifierServer {
	s := &amplifierServer{
		configPath: configPath,
		amp:        amp,
		monitor:    monitor,
		cdPlayer:   cdPlayer,
	}

	mux.HandleFunc("/api/amplifier/state", s.handleAmplifierState)
	mux.HandleFunc("/api/amplifier/power-state", s.handleAmplifierPowerState)
	mux.HandleFunc("/api/amplifier/power-on", s.handleAmplifierPowerOn)
	mux.HandleFunc("/api/amplifier/power-off", s.handleAmplifierPowerOff)
	mux.HandleFunc("/api/amplifier/power", s.handleAmplifierPower)
	mux.HandleFunc("/api/amplifier/volume", s.handleAmplifierVolume)
	mux.HandleFunc("/api/amplifier/next-input", s.handleAmplifierNextInput)
	mux.HandleFunc("/api/amplifier/prev-input", s.handleAmplifierPrevInput)
	mux.HandleFunc("/api/amplifier/reset-usb-input", s.handleAmplifierResetUSBInput)
	mux.HandleFunc("/api/amplifier/pair-start", s.handlePairStart)
	mux.HandleFunc("/api/amplifier/pair-status", s.handlePairStatus)
	mux.HandleFunc("/api/amplifier/pair-complete", s.handlePairComplete)
	mux.HandleFunc("/api/broadlink/learn-start", s.handleLearnStart)
	mux.HandleFunc("/api/broadlink/learn-status", s.handleLearnStatus)
	mux.HandleFunc("/api/cdplayer/state", s.handleCDPlayerState)
	mux.HandleFunc("/api/cdplayer/transport", s.handleCDPlayerTransport)

	return s
}

// --- response types ---

type amplifierStateResponse struct {
	Maker       string    `json:"maker"`
	Model       string    `json:"model"`
	PowerState  string    `json:"power_state"`
	LastUpdated time.Time `json:"last_updated"`
}

type amplifierPowerStateResponse struct {
	PowerState  string    `json:"power_state"`
	LastUpdated time.Time `json:"last_updated"`
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
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}
	ps, at := s.currentPowerState()
	jsonOK(w, amplifierStateResponse{
		Maker:       s.amp.Maker(),
		Model:       s.amp.Model(),
		PowerState:  string(ps),
		LastUpdated: at,
	})
}

// handleAmplifierPowerState returns only the current detected power state.
//
// GET /api/amplifier/power-state
func (s *amplifierServer) handleAmplifierPowerState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}
	ps, at := s.currentPowerState()
	jsonOK(w, amplifierPowerStateResponse{
		PowerState:  string(ps),
		LastUpdated: at,
	})
}

// handleAmplifierPowerOn sends the power_on IR command and notifies the monitor.
//
// POST /api/amplifier/power-on
func (s *amplifierServer) handleAmplifierPowerOn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}
	if err := s.amp.PowerOn(); err != nil {
		jsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if s.monitor != nil {
		s.monitor.NotifyPowerOn()
	}
	w.WriteHeader(http.StatusOK)
}

// handleAmplifierPowerOff sends the power_off IR command and notifies the monitor.
//
// POST /api/amplifier/power-off
func (s *amplifierServer) handleAmplifierPowerOff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}
	if err := s.amp.PowerOff(); err != nil {
		jsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if s.monitor != nil {
		s.monitor.NotifyPowerOff()
	}
	w.WriteHeader(http.StatusOK)
}

// currentPowerState returns the cached power state from the monitor, or
// PowerStateUnknown with the current time when no monitor is running.
func (s *amplifierServer) currentPowerState() (amplifier.PowerState, time.Time) {
	if s.powerStateFn != nil {
		return s.powerStateFn()
	}
	if s.monitor != nil {
		return s.monitor.Current()
	}
	return amplifier.PowerStateUnknown, time.Now()
}

// handleAmplifierPower sends the power IR command. No state is tracked —
// the button behaves like a physical remote toggle.
func (s *amplifierServer) handleAmplifierPower(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}
	if err := s.amp.PowerOn(); err != nil {
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

type resetUSBInputResponse struct {
	Status   string `json:"status"`
	Attempts int    `json:"attempts"`
}

func (s *amplifierServer) usbDACPresent(ctx context.Context) bool {
	if s.usbProbeFn != nil {
		return s.usbProbeFn(ctx)
	}
	if s.amp == nil {
		return false
	}
	return s.amp.IsUSBDACPresent(ctx)
}

func (s *amplifierServer) waitWithContext(ctx context.Context, d time.Duration) error {
	if s.waitFn != nil {
		return s.waitFn(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *amplifierServer) handleAmplifierResetUSBInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.amp == nil {
		jsonError(w, "amplifier not configured", http.StatusNotFound)
		return
	}

	resp, err := s.resetUSBInput(r.Context())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			jsonError(w, "request canceled", http.StatusRequestTimeout)
			return
		}
		jsonError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	jsonOK(w, resp)
}

func (s *amplifierServer) resetUSBInput(ctx context.Context) (resetUSBInputResponse, error) {
	if s.amp == nil {
		return resetUSBInputResponse{}, fmt.Errorf("amplifier not configured")
	}

	if s.usbDACPresent(ctx) {
		return resetUSBInputResponse{Status: "already_usb", Attempts: 0}, nil
	}

	maxAttempts, firstStepSettle, stepWait := s.usbResetSettings()
	jumps := 0

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Rotary input selectors may need two presses: first highlights current
		// input, second performs the change. Probe once after the first press so
		// we can stop immediately if USB was reached and avoid an extra step.
		if err := s.amp.PrevInput(); err != nil {
			return resetUSBInputResponse{}, err
		}
		if err := s.waitWithContext(ctx, firstStepSettle); err != nil {
			return resetUSBInputResponse{}, err
		}
		if s.usbDACPresent(ctx) {
			return resetUSBInputResponse{Status: "found_usb", Attempts: jumps}, nil
		}

		if err := s.amp.PrevInput(); err != nil {
			return resetUSBInputResponse{}, err
		}
		jumps++
		if err := s.waitWithContext(ctx, stepWait); err != nil {
			return resetUSBInputResponse{}, err
		}
		if s.usbDACPresent(ctx) {
			return resetUSBInputResponse{Status: "found_usb", Attempts: jumps}, nil
		}
	}

	return resetUSBInputResponse{Status: "usb_not_found", Attempts: maxAttempts}, nil
}

func (s *amplifierServer) usbResetSettings() (int, time.Duration, time.Duration) {
	maxAttempts := 13
	firstStepSettle := 150 * time.Millisecond
	stepWait := 2400 * time.Millisecond

	if s.configPath == "" {
		return maxAttempts, firstStepSettle, stepWait
	}

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		return maxAttempts, firstStepSettle, stepWait
	}

	if v := cfg.Amplifier.USBReset.MaxAttempts; v > 0 {
		maxAttempts = v
	}
	if v := cfg.Amplifier.USBReset.FirstStepSettleMS; v > 0 {
		firstStepSettle = time.Duration(v) * time.Millisecond
	}
	if v := cfg.Amplifier.USBReset.StepWaitMS; v > 0 {
		stepWait = time.Duration(v) * time.Millisecond
	}

	return maxAttempts, firstStepSettle, stepWait
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

// buildAmplifierFromConfig constructs a BroadlinkAmplifier from AmplifierConfig.
// Returns nil, nil when the amplifier is disabled or not yet configured.
func buildAmplifierFromConfig(cfg AmplifierConfig, vuSocketPath, outputDeviceMatch string) (*amplifier.BroadlinkAmplifier, error) {
	if !cfg.Enabled || cfg.Maker == "" || cfg.Model == "" {
		return nil, nil
	}
	return amplifier.NewBroadlinkAmplifier(
		broadlinkClientFromConfig(cfg.Broadlink.Host),
		amplifier.AmplifierSettings{
			Maker:          cfg.Maker,
			Model:          cfg.Model,
			IRCodes:        cfg.IRCodes,
			VUSocketPath:   vuSocketPath,
			DACMatchString: outputDeviceMatch,
			WarmUp:         time.Duration(cfg.WarmUpSecs) * time.Second,
			StandbyTimeout: time.Duration(cfg.StandbyTimeoutMins) * time.Minute,
			InputCycling: amplifier.InputCyclingSettings{
				Enabled:    cfg.InputCycling.Enabled,
				Direction:  cfg.InputCycling.Direction,
				MaxCycles:  cfg.InputCycling.MaxCycles,
				StepWait:   time.Duration(cfg.InputCycling.StepWaitSecs) * time.Second,
				MinSilence: time.Duration(cfg.InputCycling.MinSilenceSecs) * time.Second,
			},
		},
	)
}

// monitorConfigFromAmplifierConfig derives MonitorConfig from AmplifierConfig.
func monitorConfigFromAmplifierConfig(cfg AmplifierConfig) amplifier.MonitorConfig {
	return amplifier.MonitorConfig{
		WarmUp:            time.Duration(cfg.WarmUpSecs) * time.Second,
		StandbyTimeout:    time.Duration(cfg.StandbyTimeoutMins) * time.Minute,
		CyclingEnabled:    cfg.InputCycling.Enabled,
		CyclingMinSilence: time.Duration(cfg.InputCycling.MinSilenceSecs) * time.Second,
	}
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
