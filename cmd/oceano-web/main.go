package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alemser/oceano-player/internal/amplifier"
)

//go:embed static
var staticFiles embed.FS

const (
	detectorBinary = "/usr/local/bin/oceano-source-detector"
	managerBinary  = "/usr/local/bin/oceano-state-manager"
	detectorUnit   = "oceano-source-detector.service"
	managerUnit    = "oceano-state-manager.service"
	displayUnit    = "oceano-now-playing.service"
	detectorSvc    = "/etc/systemd/system/" + detectorUnit
	managerSvc     = "/etc/systemd/system/" + managerUnit
	displayEnvPath = "/etc/oceano/display.env"
)

// ALSADevice is a detected ALSA sound card.
type ALSADevice struct {
	Card int    `json:"card"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}

func main() {
	configPath := flag.String("config", "/etc/oceano/config.json", "path to Oceano config file")
	addr := flag.String("addr", ":8080", "listen address")
	libraryDB := flag.String("library-db", "/var/lib/oceano/library.db", "path to collection SQLite database")
	flag.Parse()

	var err error

	_ = os.MkdirAll("/etc/oceano", 0o755)

	mux := http.NewServeMux()

	// Static files (HTML, CSS, JS)
	sub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// API: read current config
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			apiGetConfig(w, *configPath)
		case http.MethodPost:
			apiPostConfig(w, r, *configPath)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// API: current playback state (single poll)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		cfg, _ := loadConfig(*configPath)
		data, err := os.ReadFile(cfg.Advanced.StateFile)
		if err != nil {
			http.Error(w, `{"error":"state file not found"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	// API: Server-Sent Events stream for real-time state updates.
	// Emits a "data:" frame whenever the state file changes (checked every 500 ms).
	// A ": ping" comment is sent every 15 s to prevent proxy/browser timeouts.
	// Supports local development: CORS is wide-open and missing state file is not fatal.
	mux.HandleFunc("/api/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		cfg, _ := loadConfig(*configPath)
		stateFile := cfg.Advanced.StateFile

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		// Allow cross-origin requests so the page works when the browser is
		// pointed directly at the Pi host during local development.
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// writeStateEvent writes JSON as valid SSE data frames. Because
		// /tmp/oceano-state.json is pretty-printed with newlines, every line must
		// be prefixed with "data: " per SSE framing rules.
		writeStateEvent := func(data []byte) {
			fmt.Fprint(w, formatSSEDataFrame(data))
			flusher.Flush()
		}

		var lastMod time.Time
		// Push the current state immediately so the client doesn't need to wait
		// up to 500 ms before it receives its first event. Capture the file
		// modtime at the same time so the first poll tick doesn't resend the same
		// unchanged state.
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
	})

	// API: current artwork
	mux.HandleFunc("/api/artwork", func(w http.ResponseWriter, r *http.Request) {
		cfg, _ := loadConfig(*configPath)
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
	})

	// API: physical media collection (library) and backup download.
	cfg, _ := loadConfig(*configPath)
	registerLibraryRoutes(mux, *libraryDB, cfg.Advanced.StateFile, cfg.Advanced.ArtworkDir)
	registerBackupRoute(mux, *libraryDB, cfg.Advanced.ArtworkDir)

	// API: amplifier and CD player IR control.
	amp, err := buildAmplifierFromConfig(cfg.Amplifier, cfg.Advanced.VUSocket)
	if err != nil {
		log.Printf("amplifier config error: %v (amplifier control disabled)", err)
	}
	cdPlayer := buildCDPlayerFromConfig(cfg.CDPlayer)

	// Power state monitor: polls hardware every 30 s.
	// Uses the full amp when inputs are configured; falls back to a detection-only
	// instance (Maker+Model+VUSocket only) when inputs are not yet set up.
	var powerMonitor *amplifier.PowerStateMonitor
	monitorAmp := amp
	if monitorAmp == nil {
		monitorAmp = buildDetectionAmpFromConfig(cfg.Amplifier, cfg.Advanced.VUSocket)
	}
	if monitorAmp != nil {
		powerMonitor = amplifier.NewPowerStateMonitor(monitorAmp, 30*time.Second)
		go powerMonitor.Start(context.Background())
	}
	registerAmplifierRoutes(mux, amp, cdPlayer, powerMonitor, cfg.Amplifier, *configPath)

	// Scheduled backup: generate a fresh backup every 24 hours.
	// The backup is written to the same directory as the library database.
	// There is no history — each run replaces the previous backup.
	// The first backup runs shortly after startup; subsequent ones every 24 h.
	go func() {
		backupDir := filepath.Dir(*libraryDB)
		backupPath := filepath.Join(backupDir, "oceano-backup.tar.gz")
		for {
			lib, err := openLibraryDB(*libraryDB)
			if err != nil || lib == nil {
				log.Printf("scheduled backup: library not available: %v", err)
			} else {
				if err := lib.generateBackup(backupPath, cfg.Advanced.ArtworkDir); err != nil {
					log.Printf("scheduled backup failed: %v", err)
				} else {
					log.Printf("scheduled backup written to %s", backupPath)
				}
				lib.close()
			}
			time.Sleep(24 * time.Hour)
		}
	}()

	// API: system power control (shutdown / restart)
	mux.HandleFunc("/api/power", func(w http.ResponseWriter, r *http.Request) {
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
		go exec.Command("systemctl", args...).Run()
	})

	// API: scan ALSA capture and playback devices
	mux.HandleFunc("/api/devices", func(w http.ResponseWriter, r *http.Request) {
		devices := scanALSADevices()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(devices)
	})

	// API: detect whether an HDMI/DSI display is currently connected.
	mux.HandleFunc("/api/display-detected", func(w http.ResponseWriter, r *http.Request) {
		connected, connectors := detectConnectedDisplay()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"connected":  connected,
			"connectors": connectors,
		})
	})

	log.Printf("oceano-web listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func apiGetConfig(w http.ResponseWriter, configPath string) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
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

func apiPostConfig(w http.ResponseWriter, r *http.Request, configPath string) {
	old, err := loadConfig(configPath)
	if err != nil {
		http.Error(w, "load current config failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cfg := old
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := saveConfig(configPath, cfg); err != nil {
		http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var results []string

	// Restart source detector only when audio input settings or shared socket
	// paths changed — recognition-only edits leave the detector untouched.
	if old.AudioInput != cfg.AudioInput || old.Advanced != cfg.Advanced {
		if _, err := os.Stat(detectorSvc); err == nil {
			if err := writeDetectorService(cfg); err != nil {
				results = append(results, "detector service write: "+err.Error())
			} else if err := restartService(detectorUnit); err != nil {
				results = append(results, "detector restart: "+err.Error())
			} else {
				results = append(results, "oceano-source-detector restarted")
			}
		}
	}

	// Restart state manager only when recognition settings or shared socket
	// paths changed — audio input edits leave the manager untouched.
	if old.Recognition != cfg.Recognition || old.Advanced != cfg.Advanced {
		if _, err := os.Stat(managerSvc); err == nil {
			if err := writeManagerService(cfg); err != nil {
				results = append(results, "manager service write: "+err.Error())
			} else if err := restartService(managerUnit); err != nil {
				results = append(results, "manager restart: "+err.Error())
			} else {
				results = append(results, "oceano-state-manager restarted")
			}
		}
	}

	// Restart shairport-sync only when the AirPlay name actually changed.
	if old.AudioOutput.AirPlayName != cfg.AudioOutput.AirPlayName && cfg.AudioOutput.AirPlayName != "" {
		if err := updateShairportName(cfg.AudioOutput.AirPlayName); err != nil {
			results = append(results, "shairport-sync name update: "+err.Error())
		} else {
			results = append(results, "shairport-sync restarted (new AirPlay name)")
		}
	}

	// Restart now-playing display only when display settings actually changed.
	if old.Display != cfg.Display {
		if err := saveSPIDisplayEnv(displayEnvPath, cfg.Display); err != nil {
			results = append(results, "display env write: "+err.Error())
		} else {
			displaySvc := "/etc/systemd/system/" + displayUnit
			if _, err := os.Stat(displaySvc); err == nil {
				if err := restartService(displayUnit); err != nil {
					results = append(results, "display restart: "+err.Error())
				} else {
					results = append(results, "oceano-now-playing restarted")
				}
			} else {
				results = append(results, "display.env written (oceano-now-playing not installed)")
			}
		}
	}

	// amplifier, cd_player: managed in-memory by oceano-web — no systemd restart needed.
	// weather: rendered client-side from /api/config — no restart needed.

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"results": results,
	})
}

// updateShairportName replaces the name field in /etc/shairport-sync.conf
// and restarts shairport-sync so the new name is advertised on mDNS immediately.
func updateShairportName(name string) error {
	const confPath = "/etc/shairport-sync.conf"
	data, err := os.ReadFile(confPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", confPath, err)
	}
	lines := strings.Split(string(data), "\n")
	updated := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "name") && strings.Contains(trimmed, "=") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = fmt.Sprintf(`%sname = "%s";`, indent, name)
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("name field not found in %s", confPath)
	}
	tmp := confPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, confPath); err != nil {
		return err
	}
	return restartService("shairport-sync.service")
}

// writeDetectorService rewrites the oceano-source-detector systemd unit.
func writeDetectorService(cfg Config) error {
	execStart := formatExecStart(detectorBinary, detectorArgs(cfg))
	unit := fmt.Sprintf(`[Unit]
Description=Oceano Source Detector (physical audio presence + VU + PCM relay)
After=sound.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, execStart)
	return os.WriteFile(detectorSvc, []byte(unit), 0o644)
}

// writeManagerService rewrites the oceano-state-manager systemd unit.
func writeManagerService(cfg Config) error {
	execStart := formatExecStart(managerBinary, managerArgs(cfg))
	unit := fmt.Sprintf(`[Unit]
Description=Oceano State Manager (unified playback state + ACRCloud recognition)
After=shairport-sync.service oceano-source-detector.service
Wants=shairport-sync.service

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, execStart)
	return os.WriteFile(managerSvc, []byte(unit), 0o644)
}

func restartService(unit string) error {
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	cmd := exec.Command("systemctl", "restart", unit)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", unit, strings.TrimSpace(string(out)))
	}
	// Give the service a moment to start before responding.
	time.Sleep(500 * time.Millisecond)
	return nil
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
			continue // description line, skip
		}
		// Format: " N [ShortName       ]: Driver - Long Name"
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
