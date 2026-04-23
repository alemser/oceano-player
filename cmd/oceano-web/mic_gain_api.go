package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// resolveCardNum returns the ALSA card number for the configured capture device.
// It parses /proc/asound/cards to match by DeviceMatch substring, or extracts
// the card number from an explicit Device string like "plughw:3,0".
func resolveCardNum(cfg AudioInputConfig) (int, string, error) {
	if cfg.Device != "" {
		// explicit: plughw:N,0 or hw:N,0
		re := regexp.MustCompile(`(?:plughw|hw):(\d+)`)
		if m := re.FindStringSubmatch(cfg.Device); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n, cfg.Device, nil
		}
		return -1, "", fmt.Errorf("cannot parse card number from device %q", cfg.Device)
	}
	if cfg.DeviceMatch == "" {
		return -1, "", fmt.Errorf("no capture device configured")
	}
	devices := scanALSADevices()
	match := strings.ToLower(cfg.DeviceMatch)
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Name), match) ||
			strings.Contains(strings.ToLower(d.Desc), match) {
			return d.Card, fmt.Sprintf("plughw:%d,0", d.Card), nil
		}
	}
	return -1, "", fmt.Errorf("capture device matching %q not found", cfg.DeviceMatch)
}

// findCaptureControl returns the best ALSA simple-control name for capture
// on the given card (e.g. "Mic", "Capture", "Line").
//
// Strategy (in order):
//  1. Has a capture keyword AND no playback keyword  → ideal (e.g. "Mic", "Capture Volume")
//  2. Has a capture keyword (regardless of playback) → acceptable
//  3. Has no playback keyword                        → fallback (e.g. "Device", "Auto Gain Control")
//  4. First control available                        → last resort
func findCaptureControl(cardNum int) (string, error) {
	out, err := exec.Command("amixer", "-c", strconv.Itoa(cardNum), "scontrols").Output()
	if err != nil {
		return "", fmt.Errorf("amixer scontrols: %w", err)
	}
	// Each line: Simple mixer control 'Name',0
	nameRe := regexp.MustCompile(`'([^']+)'`)
	var all []string
	for _, line := range strings.Split(string(out), "\n") {
		if m := nameRe.FindStringSubmatch(line); m != nil {
			all = append(all, m[1])
		}
	}

	captureWords  := []string{"capture", "mic", "line", "input"}
	playbackWords := []string{"playback", "speaker", "headphone", "output", "pcm"}

	hasCapture := func(name string) bool {
		lower := strings.ToLower(name)
		for _, w := range captureWords {
			if strings.Contains(lower, w) {
				return true
			}
		}
		return false
	}
	isPlayback := func(name string) bool {
		lower := strings.ToLower(name)
		for _, w := range playbackWords {
			if strings.Contains(lower, w) {
				return true
			}
		}
		return false
	}

	for _, name := range all {
		if hasCapture(name) && !isPlayback(name) {
			return name, nil
		}
	}
	for _, name := range all {
		if hasCapture(name) {
			return name, nil
		}
	}
	for _, name := range all {
		if !isPlayback(name) {
			return name, nil
		}
	}
	if len(all) > 0 {
		return all[0], nil
	}
	return "", fmt.Errorf("no mixer controls found on card %d", cardNum)
}

// amixerGetGain returns the current gain percentage (0–100) for a control.
func amixerGetGain(cardNum int, control string) (int, error) {
	out, err := exec.Command("amixer", "-c", strconv.Itoa(cardNum), "sget", control).Output()
	if err != nil {
		return -1, fmt.Errorf("amixer sget %q: %w", control, err)
	}
	re := regexp.MustCompile(`\[(\d+)%\]`)
	if m := re.FindStringSubmatch(string(out)); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v, nil
	}
	return -1, fmt.Errorf("could not parse gain from: %s", out)
}

// amixerSetGain sets gain percentage (0–100) on a control.
func amixerSetGain(cardNum int, control string, pct int) error {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	arg := fmt.Sprintf("%d%%", pct)
	out, err := exec.Command("amixer", "-c", strconv.Itoa(cardNum), "sset", control, arg).CombinedOutput()
	if err != nil {
		return fmt.Errorf("amixer sset %q %s: %w (output: %s)", control, arg, err, out)
	}
	return nil
}

type micGainInfoResponse struct {
	CardNum    int    `json:"card_num"`
	Device     string `json:"device"`
	Control    string `json:"control"`
	GainPct    int    `json:"gain_pct"`
	DeviceName string `json:"device_name"`
	Error      string `json:"error,omitempty"`
}

type micGainAdjustRequest struct {
	Direction string `json:"direction"` // "up" | "down"
	Step      int    `json:"step"`      // optional, defaults to 5
	CardNum   *int   `json:"card_num"`  // optional override; null means use config
}

type micGainAdjustResponse struct {
	GainPct int    `json:"gain_pct"`
	Error   string `json:"error,omitempty"`
}

func registerMicGainRoutes(mux *http.ServeMux, configPath string) {
	// GET /api/mic-gain/info?card=N  — omit card to use configured device
	mux.HandleFunc("/api/mic-gain/info", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var cardNum int
		var device, deviceName string

		if cardStr := r.URL.Query().Get("card"); cardStr != "" {
			n, err := strconv.Atoi(cardStr)
			if err != nil {
				jsonError(w, "invalid card number", http.StatusBadRequest)
				return
			}
			cardNum = n
			device = fmt.Sprintf("plughw:%d,0", n)
			// resolve friendly name from /proc/asound/cards
			for _, d := range scanALSADevices() {
				if d.Card == n {
					deviceName = d.Name
					if d.Desc != "" {
						deviceName = d.Desc
					}
					break
				}
			}
			if deviceName == "" {
				deviceName = device
			}
		} else {
			cfg, err := loadConfig(configPath)
			if err != nil {
				jsonError(w, "could not load config: "+err.Error(), http.StatusInternalServerError)
				return
			}
			var rerr error
			cardNum, device, rerr = resolveCardNum(cfg.AudioInput)
			if rerr != nil {
				jsonOK(w, micGainInfoResponse{Error: rerr.Error()})
				return
			}
			deviceName = cfg.AudioInput.DeviceMatch
			if deviceName == "" {
				deviceName = cfg.AudioInput.Device
			}
		}

		control, err := findCaptureControl(cardNum)
		if err != nil {
			jsonOK(w, micGainInfoResponse{CardNum: cardNum, Device: device, DeviceName: deviceName, Error: err.Error()})
			return
		}
		gainPct, err := amixerGetGain(cardNum, control)
		if err != nil {
			jsonOK(w, micGainInfoResponse{CardNum: cardNum, Device: device, DeviceName: deviceName, Control: control, Error: err.Error()})
			return
		}
		jsonOK(w, micGainInfoResponse{
			CardNum:    cardNum,
			Device:     device,
			DeviceName: deviceName,
			Control:    control,
			GainPct:    gainPct,
		})
	})

	mux.HandleFunc("/api/mic-gain/adjust", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req micGainAdjustRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		step := req.Step
		if step <= 0 {
			step = 5
		}

		var cardNum int
		if req.CardNum != nil {
			cardNum = *req.CardNum
		} else {
			cfg, err := loadConfig(configPath)
			if err != nil {
				jsonError(w, "could not load config: "+err.Error(), http.StatusInternalServerError)
				return
			}
			var rerr error
			cardNum, _, rerr = resolveCardNum(cfg.AudioInput)
			if rerr != nil {
				jsonError(w, rerr.Error(), http.StatusBadRequest)
				return
			}
		}

		control, err := findCaptureControl(cardNum)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		current, err := amixerGetGain(cardNum, control)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var next int
		switch req.Direction {
		case "up":
			next = current + step
		case "down":
			next = current - step
		default:
			jsonError(w, "direction must be 'up' or 'down'", http.StatusBadRequest)
			return
		}
		if err := amixerSetGain(cardNum, control, next); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		actual, _ := amixerGetGain(cardNum, control)
		jsonOK(w, micGainAdjustResponse{GainPct: actual})
	})

	mux.HandleFunc("/api/mic-gain/store", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		out, err := exec.Command("alsactl", "store").CombinedOutput()
		if err != nil {
			jsonError(w, "alsactl store failed: "+strings.TrimSpace(string(out)), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]bool{"ok": true})
	})
}
