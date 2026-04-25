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
func findCaptureControl(cardNum int) (string, error) {
	out, err := exec.Command("amixer", "-c", strconv.Itoa(cardNum), "scontrols").Output()
	if err != nil {
		return "", fmt.Errorf("amixer scontrols: %w", err)
	}
	nameRe := regexp.MustCompile(`'([^']+)'`)
	var all []string
	for _, line := range strings.Split(string(out), "\n") {
		if m := nameRe.FindStringSubmatch(line); m != nil {
			all = append(all, m[1])
		}
	}

	captureWords := []string{"capture", "mic", "line", "input"}
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

// captureGainState holds both the raw value and percentage for the capture path.
type captureGainState struct {
	Raw int
	Max int
	Pct int
}

func amixerGetCaptureState(cardNum int, control string) (captureGainState, error) {
	out, err := exec.Command("amixer", "-c", strconv.Itoa(cardNum), "sget", control).Output()
	if err != nil {
		return captureGainState{}, fmt.Errorf("amixer sget %q: %w", control, err)
	}
	s := string(out)

	reMax := regexp.MustCompile(`(?i)Capture\s+\d+\s+-\s+(\d+)`)
	maxVal := 100
	if m := reMax.FindStringSubmatch(s); m != nil {
		maxVal, _ = strconv.Atoi(m[1])
	}

	reCapture := regexp.MustCompile(`(?i)Capture\s+(\d+)\s+\[(\d+)%\]`)
	if m := reCapture.FindStringSubmatch(s); m != nil {
		raw, _ := strconv.Atoi(m[1])
		pct, _ := strconv.Atoi(m[2])
		return captureGainState{Raw: raw, Max: maxVal, Pct: pct}, nil
	}

	rePct := regexp.MustCompile(`\[(\d+)%\]`)
	if m := rePct.FindStringSubmatch(s); m != nil {
		pct, _ := strconv.Atoi(m[1])
		raw := pct * maxVal / 100
		return captureGainState{Raw: raw, Max: maxVal, Pct: pct}, nil
	}

	return captureGainState{}, fmt.Errorf("could not parse capture gain from: %s", s)
}

func amixerGetGain(cardNum int, control string) (int, error) {
	st, err := amixerGetCaptureState(cardNum, control)
	return st.Pct, err
}

func amixerSetGainRaw(cardNum int, control string, raw int) error {
	arg := strconv.Itoa(raw)
	out, err := exec.Command("amixer", "-c", strconv.Itoa(cardNum), "sset", control, arg, "capture").CombinedOutput()
	if err != nil {
		out2, err2 := exec.Command("amixer", "-c", strconv.Itoa(cardNum), "sset", control, arg).CombinedOutput()
		if err2 != nil {
			return fmt.Errorf("amixer sset %q %s capture: %w (output: %s)", control, arg, err, out)
		}
		_ = out2
	}
	return nil
}

func amixerSetGain(cardNum int, control string, pct int) error {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	arg := fmt.Sprintf("%d%%", pct)
	out, err := exec.Command("amixer", "-c", strconv.Itoa(cardNum), "sset", control, arg, "capture").CombinedOutput()
	if err != nil {
		out2, err2 := exec.Command("amixer", "-c", strconv.Itoa(cardNum), "sset", control, arg).CombinedOutput()
		if err2 != nil {
			return fmt.Errorf("amixer sset %q %s: %w (output: %s)", control, arg, err, out)
		}
		_ = out2
	}
	return nil
}

type micGainInfoResponse struct {
	CardNum    int    `json:"card_num"`
	Device     string `json:"device"`
	Control    string `json:"control"`
	GainPct    int    `json:"gain_pct"`
	GainRaw    int    `json:"gain_raw"`
	GainMax    int    `json:"gain_max"`
	DeviceName string `json:"device_name"`
	Error      string `json:"error,omitempty"`
}

type micGainAdjustRequest struct {
	Direction string `json:"direction"`
	Step      int    `json:"step"`
	RawStep   bool   `json:"raw_step"`
	CardNum   *int   `json:"card_num"`
}

type micGainAdjustResponse struct {
	GainPct int    `json:"gain_pct"`
	GainRaw int    `json:"gain_raw"`
	GainMax int    `json:"gain_max"`
	Error   string `json:"error,omitempty"`
}

func registerMicGainRoutes(mux *http.ServeMux, configPath string) {
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
		st, err := amixerGetCaptureState(cardNum, control)
		if err != nil {
			jsonOK(w, micGainInfoResponse{CardNum: cardNum, Device: device, DeviceName: deviceName, Control: control, Error: err.Error()})
			return
		}
		jsonOK(w, micGainInfoResponse{
			CardNum:    cardNum,
			Device:     device,
			DeviceName: deviceName,
			Control:    control,
			GainPct:    st.Pct,
			GainRaw:    st.Raw,
			GainMax:    st.Max,
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

		if req.RawStep {
			st, err := amixerGetCaptureState(cardNum, control)
			if err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			rawNext := st.Raw
			switch req.Direction {
			case "up":
				rawNext = st.Raw + step
			case "down":
				rawNext = st.Raw - step
			default:
				jsonError(w, "direction must be 'up' or 'down'", http.StatusBadRequest)
				return
			}
			if rawNext < 0 {
				rawNext = 0
			}
			if rawNext > st.Max {
				rawNext = st.Max
			}
			if err := amixerSetGainRaw(cardNum, control, rawNext); err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
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
		}

		actual, _ := amixerGetCaptureState(cardNum, control)
		jsonOK(w, micGainAdjustResponse{GainPct: actual.Pct, GainRaw: actual.Raw, GainMax: actual.Max})
	})

	mux.HandleFunc("/api/mic-gain/store", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		out, err := exec.Command("sudo", "/usr/sbin/alsactl", "store").CombinedOutput()
		if err != nil {
			jsonError(w, "alsactl store failed: "+strings.TrimSpace(string(out)), http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]bool{"ok": true})
	})
}
