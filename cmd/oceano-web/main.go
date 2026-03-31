package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

//go:embed static
var staticFiles embed.FS

const (
	detectorBinary  = "/usr/local/bin/oceano-source-detector"
	managerBinary   = "/usr/local/bin/oceano-state-manager"
	detectorUnit    = "oceano-source-detector.service"
	managerUnit     = "oceano-state-manager.service"
	displayUnit     = "oceano-now-playing.service"
	detectorSvc     = "/etc/systemd/system/" + detectorUnit
	managerSvc      = "/etc/systemd/system/" + managerUnit
	displayEnvPath  = "/etc/oceano/display.env"
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

	// API: current playback state
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

	// API: service logs
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		unit := map[string]string{
			"detector": detectorUnit,
			"manager":  managerUnit,
			"display":  displayUnit,
		}[service]
		if unit == "" {
			http.Error(w, "unknown service", http.StatusBadRequest)
			return
		}
		out, _ := exec.Command("journalctl", "-u", unit, "-n", "100", "--no-pager", "--output=short").CombinedOutput()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(out)
	})

	// API: physical media collection (library)
	registerLibraryRoutes(mux, *libraryDB)

	// API: scan ALSA capture and playback devices
	mux.HandleFunc("/api/devices", func(w http.ResponseWriter, r *http.Request) {
		devices := scanALSADevices()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(devices)
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

func apiPostConfig(w http.ResponseWriter, r *http.Request, configPath string) {
	var cfg Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := saveConfig(configPath, cfg); err != nil {
		http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var results []string

	// Rewrite and restart source detector if service file exists.
	if _, err := os.Stat(detectorSvc); err == nil {
		if err := writeDetectorService(cfg); err != nil {
			results = append(results, "detector service write: "+err.Error())
		} else if err := restartService(detectorUnit); err != nil {
			results = append(results, "detector restart: "+err.Error())
		} else {
			results = append(results, "oceano-source-detector restarted")
		}
	}

	// Rewrite and restart state manager if service file exists.
	if _, err := os.Stat(managerSvc); err == nil {
		if err := writeManagerService(cfg); err != nil {
			results = append(results, "manager service write: "+err.Error())
		} else if err := restartService(managerUnit); err != nil {
			results = append(results, "manager restart: "+err.Error())
		} else {
			results = append(results, "oceano-state-manager restarted")
		}
	}

	// Update AirPlay name in shairport-sync.conf and restart if name changed.
	if cfg.AudioOutput.AirPlayName != "" {
		if err := updateShairportName(cfg.AudioOutput.AirPlayName); err != nil {
			results = append(results, "shairport-sync name update: "+err.Error())
		} else {
			results = append(results, "shairport-sync restarted (new AirPlay name)")
		}
	}

	// Write display env and restart oceano-now-playing if it is installed.
	if err := saveDisplayEnv(displayEnvPath, cfg.Display); err != nil {
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      len(results) > 0,
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
