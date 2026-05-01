package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func handleStatus(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, _ := loadConfig(configPath)
		data, err := os.ReadFile(cfg.Advanced.StateFile)
		if err != nil {
			http.Error(w, `{"error":"state file not found"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

func handleStream(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		cfg, _ := loadConfig(configPath)
		stateFile := cfg.Advanced.StateFile

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		writeStateEvent := func(data []byte) {
			fmt.Fprint(w, formatSSEDataFrame(data))
			flusher.Flush()
		}

		var lastMod time.Time
		if info, err := os.Stat(stateFile); err == nil {
			lastMod = info.ModTime()
			if data, err := os.ReadFile(stateFile); err == nil {
				writeStateEvent(data)
			}
		}

		tick := time.NewTicker(500 * time.Millisecond)
		ping := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		defer ping.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				fmt.Fprintf(w, ": ping\n\n")
				flusher.Flush()
			case <-tick.C:
				info, err := os.Stat(stateFile)
				if err != nil {
					continue
				}
				if !info.ModTime().After(lastMod) {
					continue
				}
				lastMod = info.ModTime()
				data, err := os.ReadFile(stateFile)
				if err != nil {
					continue
				}
				writeStateEvent(data)
			}
		}
	}
}

var airplayTransportHTTPClient = &http.Client{Timeout: 2 * time.Second}
var airplayTransportAmpPowerStateFn = func() string { return "" }

func handleAirPlayTransportCapabilities(configPath string, dacpReader airplayDACPContextReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp, _, statusCode, err := resolveAirPlayTransportStatus(configPath, dacpReader)
		if err != nil {
			http.Error(w, err.Error(), statusCode)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleAirPlayTransport(configPath string, dacpReader airplayDACPContextReader, limiter *airplayTransportRateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp, ctx, statusCode, err := resolveAirPlayTransportStatus(configPath, dacpReader)
		if err != nil {
			http.Error(w, err.Error(), statusCode)
			return
		}
		if !resp.Available {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":            false,
				"reason":        fallbackReason(resp.Reason, resp.SessionState),
				"session_state": resp.SessionState,
			})
			return
		}

		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		action := strings.TrimSpace(strings.ToLower(req.Action))
		cmdPath, ok := mapAirPlayActionToDACPPath(action)
		if !ok {
			log.Printf("airplay_transport event=command action=%s result=rejected reason=invalid_action", action)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     false,
				"reason": "invalid_action",
			})
			return
		}
		if limiter != nil && !limiter.Allow(action) {
			log.Printf("airplay_transport event=command action=%s result=rejected reason=rate_limited", action)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     false,
				"reason": "rate_limited",
			})
			return
		}

		targetURL := fmt.Sprintf("http://%s:3689%s", ctx.ClientIP, cmdPath)
		httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
		if err != nil {
			http.Error(w, `{"error":"failed to build dacp request"}`, http.StatusInternalServerError)
			return
		}
		httpReq.Header.Set("Active-Remote", ctx.ActiveRemote)

		httpResp, err := doDACPRequestWithRetry(httpReq, 2)
		if err != nil {
			log.Printf("airplay_transport event=command action=%s result=failed reason=network_unreachable target_ip=%s", action, ctx.ClientIP)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     false,
				"reason": "network_unreachable",
			})
			return
		}
		defer httpResp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(httpResp.Body, 1024))

		if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
			log.Printf("airplay_transport event=command action=%s result=failed reason=dacp_error status_code=%d target_ip=%s", action, httpResp.StatusCode, ctx.ClientIP)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":            false,
				"reason":        "dacp_error",
				"status_code":   httpResp.StatusCode,
				"session_state": resp.SessionState,
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		log.Printf("airplay_transport event=command action=%s result=ok target=%s target_ip=%s", action, cmdPath, ctx.ClientIP)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":            true,
			"action":        action,
			"target":        cmdPath,
			"session_state": resp.SessionState,
		})
	}
}

type airplayTransportRateLimiter struct {
	minInterval time.Duration
	mu          sync.Mutex
	lastByKey   map[string]time.Time
}

func newAirplayTransportRateLimiter(minInterval time.Duration) *airplayTransportRateLimiter {
	return &airplayTransportRateLimiter{
		minInterval: minInterval,
		lastByKey:   map[string]time.Time{},
	}
}

func (l *airplayTransportRateLimiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	key = strings.TrimSpace(strings.ToLower(key))
	if key == "" {
		key = "unknown"
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	last, ok := l.lastByKey[key]
	if ok && now.Sub(last) < l.minInterval {
		return false
	}
	l.lastByKey[key] = now
	return true
}

func doDACPRequestWithRetry(req *http.Request, maxAttempts int) (*http.Response, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := airplayTransportHTTPClient.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryableNetworkError(err) || attempt == maxAttempts {
			break
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, lastErr
}

func isRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	return false
}

type airplayTransportCapabilitiesResponse struct {
	Available        bool     `json:"available"`
	SessionState     string   `json:"session_state"`
	SupportedActions []string `json:"supported_actions"`
	Reason           string   `json:"reason,omitempty"`
}

func resolveAirPlayTransportStatus(configPath string, dacpReader airplayDACPContextReader) (airplayTransportCapabilitiesResponse, airplayDACPContext, int, error) {
	resp := airplayTransportCapabilitiesResponse{
		Available:        false,
		SessionState:     "no_airplay_session",
		SupportedActions: []string{"play", "pause", "next", "previous"},
		Reason:           "no_airplay_session",
	}
	cfg, _ := loadConfig(configPath)
	data, err := os.ReadFile(cfg.Advanced.StateFile)
	if err != nil {
		return resp, airplayDACPContext{}, http.StatusServiceUnavailable, fmt.Errorf(`{"error":"state file not found"}`)
	}
	var state struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		return resp, airplayDACPContext{}, http.StatusInternalServerError, fmt.Errorf(`{"error":"invalid state file"}`)
	}
	if state.Source != "AirPlay" {
		return resp, airplayDACPContext{}, http.StatusOK, nil
	}
	if isAmplifierOffForAirPlay(airplayTransportAmpPowerStateFn()) {
		resp.SessionState = "amp_off"
		resp.Reason = "amp_off"
		return resp, airplayDACPContext{}, http.StatusOK, nil
	}

	resp.Reason = "missing_dacp_context"
	resp.SessionState = "missing_dacp_context"
	var snapshot airplayDACPContext
	if dacpReader != nil {
		snapshot = dacpReader.Snapshot()
	}
	if strings.TrimSpace(snapshot.ActiveRemote) == "" || strings.TrimSpace(snapshot.DACPID) == "" || strings.TrimSpace(snapshot.ClientIP) == "" {
		return resp, snapshot, http.StatusOK, nil
	}
	if snapshot.UpdatedAt.IsZero() || time.Since(snapshot.UpdatedAt) > 5*time.Minute {
		resp.SessionState = "session_stale"
		resp.Reason = "session_stale"
		return resp, snapshot, http.StatusOK, nil
	}
	resp.Available = true
	resp.SessionState = "ready"
	resp.Reason = ""
	return resp, snapshot, http.StatusOK, nil
}

func isAmplifierOffForAirPlay(powerState string) bool {
	switch strings.TrimSpace(strings.ToLower(powerState)) {
	case "off", "standby":
		return true
	default:
		return false
	}
}

func mapAirPlayActionToDACPPath(action string) (string, bool) {
	switch action {
	case "play":
		return "/ctrl-int/1/play", true
	case "pause":
		return "/ctrl-int/1/pause", true
	case "next":
		return "/ctrl-int/1/nextitem", true
	case "previous":
		return "/ctrl-int/1/previtem", true
	default:
		return "", false
	}
}

func fallbackReason(reason, sessionState string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	return strings.TrimSpace(sessionState)
}

func handleArtwork(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, _ := loadConfig(configPath)
		data, err := os.ReadFile(cfg.Advanced.StateFile)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var state struct {
			Track *struct {
				ArtworkPath string `json:"artwork_path"`
			} `json:"track"`
		}
		if err := json.Unmarshal(data, &state); err != nil || state.Track == nil || state.Track.ArtworkPath == "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, state.Track.ArtworkPath)
	}
}

func handlePower() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		var args []string
		switch req.Action {
		case "shutdown":
			args = []string{"poweroff"}
		case "restart":
			args = []string{"reboot"}
		default:
			http.Error(w, "action must be shutdown or restart", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		go serviceMgr.PowerAction(args[0])
	}
}

func handleRecognize() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := serviceMgr.SignalMain(managerUnit, "SIGUSR1"); err != nil {
			http.Error(w, "failed to signal state manager", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDevices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices := scanALSADevices()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(devices)
	}
}

func handleDisplayDetected() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		connected, connectors := detectConnectedDisplay()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"connected":  connected,
			"connectors": connectors,
		})
	}
}

func handleSPIDisplayInstalled() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svcPath := "/etc/systemd/system/" + spiDisplayUnit
		_, svcErr := os.Stat(svcPath)
		_, fbErr := os.Stat("/dev/fb0")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"installed": svcErr == nil && fbErr == nil,
		})
	}
}

func handleBluetoothDevices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			apiGetBluetoothDevices(w)
		case http.MethodDelete:
			mac := r.URL.Query().Get("mac")
			if mac == "" {
				http.Error(w, "missing mac parameter", http.StatusBadRequest)
				return
			}
			apiRemoveBluetoothDevice(w, mac)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// formatSSEDataFrame converts arbitrary JSON/text payload into a valid SSE
// event frame. Each line must be prefixed with "data: " by spec.
func formatSSEDataFrame(data []byte) string {
	payload := strings.TrimRight(string(data), "\r\n")
	lines := strings.Split(payload, "\n")
	for i, line := range lines {
		lines[i] = "data: " + line
	}
	return strings.Join(lines, "\n") + "\n\n"
}

// apiGetBluetoothDevices lists paired Bluetooth devices via bluetoothctl.
func apiGetBluetoothDevices(w http.ResponseWriter) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := commandRunner.OutputContext(ctx, "bluetoothctl", "devices", "Paired")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]BluetoothDevice{})
		return
	}

	var devices []BluetoothDevice
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 2 || parts[0] != "Device" {
			continue
		}
		mac := parts[1]
		name := mac
		if len(parts) == 3 {
			name = parts[2]
		}
		devices = append(devices, BluetoothDevice{MAC: mac, Name: name})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devices)
}

// apiRemoveBluetoothDevice removes a paired device by MAC address.
func apiRemoveBluetoothDevice(w http.ResponseWriter, mac string) {
	for _, c := range mac {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f') || c == ':') {
			http.Error(w, "invalid MAC address", http.StatusBadRequest)
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := commandRunner.CombinedOutputContext(ctx, "bluetoothctl", "remove", mac); err != nil {
		http.Error(w, strings.TrimSpace(string(out)), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleConfig(configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			apiGetConfig(w, configPath)
		case http.MethodPost:
			apiPostConfig(w, r, configPath)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// scanALSADevices reads /proc/asound/cards and returns all detected cards.
func scanALSADevices() []ALSADevice {
	f, err := os.Open("/proc/asound/cards")
	if err != nil {
		return nil
	}
	defer f.Close()

	var devices []ALSADevice
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		var cardNum int
		if _, err := fmt.Sscanf(fields[0], "%d", &cardNum); err != nil {
			continue
		}
		desc := ""
		if idx := strings.Index(line, "- "); idx >= 0 {
			desc = strings.TrimSpace(line[idx+2:])
		}
		shortName := ""
		if start := strings.Index(line, "["); start >= 0 {
			if end := strings.Index(line, "]"); end > start {
				shortName = strings.TrimSpace(line[start+1 : end])
			}
		}
		devices = append(devices, ALSADevice{
			Card: cardNum,
			Name: shortName,
			Desc: desc,
		})
	}
	return devices
}

// detectConnectedDisplay checks DRM connector status files and reports whether
// any HDMI/DSI connector is currently in "connected" state.
func detectConnectedDisplay() (bool, []string) {
	statusFiles, err := filepath.Glob("/sys/class/drm/card*/status")
	if err != nil || len(statusFiles) == 0 {
		return false, nil
	}

	var connected []string
	for _, statusFile := range statusFiles {
		connector := filepath.Base(filepath.Dir(statusFile))
		upper := strings.ToUpper(connector)
		if !strings.Contains(upper, "HDMI") && !strings.Contains(upper, "DSI") {
			continue
		}
		statusRaw, err := os.ReadFile(statusFile)
		if err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(string(statusRaw)), "connected") {
			connected = append(connected, connector)
		}
	}

	return len(connected) > 0, connected
}
