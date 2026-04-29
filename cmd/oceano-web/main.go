package main

import (
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
	"github.com/alemser/oceano-player/internal/shairport"
)

//go:embed static
var staticFiles embed.FS

const (
	detectorBinary = "/usr/local/bin/oceano-source-detector"
	managerBinary  = "/usr/local/bin/oceano-state-manager"
	detectorUnit   = "oceano-source-detector.service"
	managerUnit    = "oceano-state-manager.service"
	displayUnit       = "oceano-display.service"
	spiDisplayUnit    = "oceano-now-playing.service"
	detectorSvc       = "/etc/systemd/system/" + detectorUnit
	managerSvc        = "/etc/systemd/system/" + managerUnit
	displayEnvPath    = "/etc/oceano/display.env"
)

// ALSADevice is a detected ALSA sound card.
type ALSADevice struct {
	Card int    `json:"card"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}

const shairportConfigPath = "/etc/shairport-sync.conf"

func resolveAirplayOutputDevice(cfg AudioOutputConfig) string {
	device := strings.TrimSpace(cfg.Device)
	if device != "" {
		return device
	}
	match := strings.ToLower(strings.TrimSpace(cfg.DeviceMatch))
	if match == "" {
		return "default"
	}
	for _, d := range scanALSADevices() {
		if strings.Contains(strings.ToLower(d.Name), match) ||
			strings.Contains(strings.ToLower(d.Desc), match) {
			return fmt.Sprintf("plughw:%d,0", d.Card)
		}
	}
	// Keep legacy safe fallback if the DAC is temporarily missing.
	return "default"
}

// migrateShairportPAToAlsaOnStartup rewrites a legacy "pa" shairport config. The
// shairport-sync system user cannot connect to the session PipeWire at /run/user/…/pulse
// (see journal: "Connection refused" to pulseaudio), so the unit failed and AirPlay disappeared.
// One-time: uses DAC + name from config and restarts shairport-sync.
func migrateShairportPAToAlsaOnStartup(configPath string) {
	b, err := os.ReadFile(shairportConfigPath)
	if err != nil {
		return
	}
	if !strings.Contains(string(b), `output_backend = "pa"`) {
		return
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Printf("shairport migration: read config: %v", err)
		return
	}
	alsa := resolveAirplayOutputDevice(cfg.AudioOutput)
	name := strings.TrimSpace(cfg.AudioOutput.AirPlayName)
	if name == "" {
		name = "Oceano"
	}
	if err := shairport.WriteConfig(shairportConfigPath, name, alsa); err != nil {
		log.Printf("shairport pa→alsa migration: %v", err)
		return
	}
	if err := exec.Command("systemctl", "restart", "shairport-sync.service").Run(); err != nil {
		log.Printf("shairport migration restart: %v", err)
		return
	}
	log.Println("shairport: migrated PA backend to ALSA (restarted service)")
}

func main() {
	configPath := flag.String("config", "/etc/oceano/config.json", "path to Oceano config file")
	addr := flag.String("addr", ":8080", "listen address")
	libraryDB := flag.String("library-db", "/var/lib/oceano/library.db", "path to collection SQLite database")
	flag.Parse()

	var err error

	_ = os.MkdirAll("/etc/oceano", 0o755)
	migrateShairportPAToAlsaOnStartup(*configPath)

	mux := http.NewServeMux()

	// Static files (HTML, CSS, JS)
	sub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		redirectWithQuery(w, r, "/config.html")
	})
	mux.HandleFunc("/stylus", func(w http.ResponseWriter, r *http.Request) {
		redirectWithQuery(w, r, "/stylus.html")
	})
	mux.HandleFunc("/topology", func(w http.ResponseWriter, r *http.Request) {
		redirectWithQuery(w, r, "/topology.html")
	})
	mux.HandleFunc("/ir-setup", func(w http.ResponseWriter, r *http.Request) {
		redirectWithQuery(w, r, "/ir-setup.html")
	})

	// API: core state and config endpoints.
	mux.HandleFunc("/api/config", handleConfig(*configPath))
	mux.HandleFunc("/api/status", handleStatus(*configPath))
	mux.HandleFunc("/api/stream", handleStream(*configPath))
	mux.HandleFunc("/api/artwork", handleArtwork(*configPath))
	mux.HandleFunc("/api/setup-status", handleSetupStatus(*configPath, *libraryDB))

	// API: physical media collection (library) and backup/restore.
	cfg, _ := loadConfig(*configPath)
	registerLibraryRoutes(mux, *libraryDB, cfg.Advanced.StateFile, cfg.Advanced.ArtworkDir, *configPath)
	registerBackupRoutes(mux, *libraryDB, cfg.Advanced.ArtworkDir, *configPath)
	registerHistoryRoutes(mux, *libraryDB)
	registerStylusRoutes(mux, *libraryDB)
	registerCalibrationRoutes(mux, cfg.Advanced.VUSocket)
	registerMicGainRoutes(mux, *configPath)

	// API: amplifier IR control.
	powerCal := powerCalibrationForConfiguredInput(cfg.Advanced, cfg.AmplifierRuntime)
	amp, err := buildAmplifierFromConfig(cfg.Amplifier, cfg.Advanced.VUSocket, cfg.AudioOutput.DeviceMatch, powerCal)
	if err != nil {
		log.Printf("amplifier config error: %v (amplifier control disabled)", err)
	}
	var monitor *amplifier.PowerStateMonitor
	if amp != nil {
		monitor = amplifier.NewPowerStateMonitor(amp, 30*time.Second, monitorConfigFromAmplifierConfig(cfg.Amplifier))
		go monitor.Start(context.Background())
	}
	registerAmplifierRoutes(mux, amp, monitor, *configPath)

	// Scheduled backup: generate a fresh timestamped backup every 24 hours.
	// Backups land in the same directory as the library database.
	// At most backupMaxHistory (7) are kept; older ones are pruned automatically.
	// The first backup runs shortly after startup; subsequent ones every 24 h.
	go func() {
		backupDir := filepath.Dir(*libraryDB)
		for {
			lib, err := openLibraryDB(*libraryDB)
			if err != nil || lib == nil {
				log.Printf("scheduled backup: library not available: %v", err)
			} else {
				destPath := filepath.Join(backupDir, backupFileName())
				if err := lib.generateBackup(destPath, cfg.Advanced.ArtworkDir, *configPath); err != nil {
					log.Printf("scheduled backup failed: %v", err)
				} else {
					log.Printf("scheduled backup written to %s", destPath)
					pruneOldBackups(backupDir, backupMaxHistory)
				}
				lib.close()
			}
			time.Sleep(24 * time.Hour)
		}
	}()

	// API: operational actions.
	mux.HandleFunc("/api/power", handlePower())
	mux.HandleFunc("/api/recognize", handleRecognize())
	mux.HandleFunc("/api/devices", handleDevices())
	mux.HandleFunc("/api/display-detected", handleDisplayDetected())
	mux.HandleFunc("/api/display/restart", handleDisplayServiceRestart)
	mux.HandleFunc("/api/spi-display-installed", handleSPIDisplayInstalled())
	mux.HandleFunc("/api/bluetooth/devices", handleBluetoothDevices())

	log.Printf("oceano-web listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func redirectWithQuery(w http.ResponseWriter, r *http.Request, target string) {
	if q := strings.TrimSpace(r.URL.RawQuery); q != "" {
		target += "?" + q
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

// BluetoothDevice is a paired Bluetooth device.
type BluetoothDevice struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
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
	hadError := false

	detectorChanged := old.AudioInput != cfg.AudioInput ||
		old.Advanced.SourceFile != cfg.Advanced.SourceFile ||
		old.Advanced.VUSocket != cfg.Advanced.VUSocket ||
		old.Advanced.PCMSocket != cfg.Advanced.PCMSocket

	managerChanged := old.Recognition != cfg.Recognition ||
		old.Advanced.MetadataPipe != cfg.Advanced.MetadataPipe ||
		old.Advanced.SourceFile != cfg.Advanced.SourceFile ||
		old.Advanced.StateFile != cfg.Advanced.StateFile ||
		old.Advanced.ArtworkDir != cfg.Advanced.ArtworkDir ||
		old.Advanced.VUSocket != cfg.Advanced.VUSocket ||
		old.Advanced.VUSilenceThreshold != cfg.Advanced.VUSilenceThreshold ||
		old.Advanced.PCMSocket != cfg.Advanced.PCMSocket ||
		old.Advanced.IdleDelaySecs != cfg.Advanced.IdleDelaySecs ||
		old.Advanced.SessionGapThresholdSecs != cfg.Advanced.SessionGapThresholdSecs ||
		old.Advanced.LibraryDB != cfg.Advanced.LibraryDB

	managerNeedsReconcile := false
	if _, err := os.Stat(managerSvc); err == nil {
		desiredExecStart := formatExecStart(managerBinary, managerArgs(cfg, configPath))
		matches, matchErr := unitContainsExecStart(managerSvc, desiredExecStart)
		if matchErr != nil {
			results = append(results, "manager unit verify: "+matchErr.Error())
			hadError = true
		} else if !matches {
			managerNeedsReconcile = true
		}
	}

	// Restart source detector only when audio input settings or shared socket
	// paths changed — recognition-only edits leave the detector untouched.
	if detectorChanged {
		if _, err := os.Stat(detectorSvc); err == nil {
			if err := writeDetectorService(cfg); err != nil {
				results = append(results, "detector service write: "+err.Error())
				hadError = true
			} else if err := restartService(detectorUnit); err != nil {
				results = append(results, "detector restart: "+err.Error())
				hadError = true
			} else {
				results = append(results, "oceano-source-detector restarted")
			}
		}
	}

	// Restart state manager only when recognition settings or shared socket
	// paths changed — audio input edits leave the manager untouched.
	if managerChanged || managerNeedsReconcile {
		if _, err := os.Stat(managerSvc); err == nil {
			if err := writeManagerService(cfg, configPath); err != nil {
				results = append(results, "manager service write: "+err.Error())
				hadError = true
			} else if err := restartService(managerUnit); err != nil {
				results = append(results, "manager restart: "+err.Error())
				hadError = true
			} else {
				if managerNeedsReconcile && !managerChanged {
					results = append(results, "oceano-state-manager restarted (unit reconciled)")
				} else {
					results = append(results, "oceano-state-manager restarted")
				}
			}
		}
	}

	// Rewrite shairport when AirPlay name or DAC (ALSA) changes — use ALSA output so
	// the system shairport-sync user does not need the user's PipeWire-Pulse socket.
	if old.AudioOutput != cfg.AudioOutput {
		alsa := resolveAirplayOutputDevice(cfg.AudioOutput)
		name := strings.TrimSpace(cfg.AudioOutput.AirPlayName)
		if name == "" {
			name = "Oceano"
		}
		if err := shairport.WriteConfig(shairportConfigPath, name, alsa); err != nil {
			results = append(results, "shairport-sync config: "+err.Error())
			hadError = true
		} else if err := restartService("shairport-sync.service"); err != nil {
			results = append(results, "shairport-sync restart: "+err.Error())
			hadError = true
		} else {
			results = append(results, "shairport-sync: ALSA output updated and service restarted")
		}
	}

	// Restart now-playing display only when display settings actually changed.
	if old.Display != cfg.Display {
		if err := saveSPIDisplayEnv(displayEnvPath, cfg.Display); err != nil {
			results = append(results, "display env write: "+err.Error())
			hadError = true
		} else {
			displaySvc := "/etc/systemd/system/" + displayUnit
			if _, err := os.Stat(displaySvc); err == nil {
				if err := restartService(displayUnit); err != nil {
					results = append(results, "display restart: "+err.Error())
					hadError = true
				} else {
					results = append(results, "oceano-now-playing restarted")
				}
			} else {
				results = append(results, "display.env written (oceano-now-playing not installed)")
			}
		}
	}

	// Apply Bluetooth settings when name or enabled flag changed.
	if old.Bluetooth != cfg.Bluetooth {
		if err := applyBluetoothConfig(cfg.Bluetooth); err != nil {
			results = append(results, "bluetooth config: "+err.Error())
			hadError = true
		} else {
			results = append(results, "bluetooth settings applied")
		}
	}

	// amplifier controls: managed in-memory by oceano-web — no systemd restart needed.
	// weather and now_playing.idle_screen_theme: rendered client-side from /api/config — no restart needed.

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      !hadError,
		"results": results,
	})
}

// applyBluetoothConfig applies Bluetooth adapter settings that take effect
// immediately without restarting any Oceano service.
// Name changes update /etc/bluetooth/main.conf and restart bluetoothd.
// Enabling powers on the adapter and makes it discoverable/pairable.
func applyBluetoothConfig(cfg BluetoothConfig) error {
	const confPath = "/etc/bluetooth/main.conf"

	if cfg.Name != "" {
		data, err := os.ReadFile(confPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", confPath, err)
		}
		content := string(data)

		// Replace or insert Name under [General].
		updated := false
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "Name") && strings.Contains(line, "=") {
				lines[i] = "Name = " + cfg.Name
				updated = true
				break
			}
		}
		if !updated {
			// Append after [General] header if present, else append at end.
			inserted := false
			for i, line := range lines {
				if strings.TrimSpace(line) == "[General]" {
					newLines := make([]string, 0, len(lines)+1)
					newLines = append(newLines, lines[:i+1]...)
					newLines = append(newLines, "Name = "+cfg.Name)
					newLines = append(newLines, lines[i+1:]...)
					lines = newLines
					inserted = true
					break
				}
			}
			if !inserted {
				lines = append(lines, "", "[General]", "Name = "+cfg.Name)
			}
		}

		newContent := strings.Join(lines, "\n")
		tmp := confPath + ".tmp"
		if err := os.WriteFile(tmp, []byte(newContent), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", confPath, err)
		}
		if err := os.Rename(tmp, confPath); err != nil {
			return fmt.Errorf("rename %s: %w", confPath, err)
		}
		// Restart bluetoothd to pick up the new name from main.conf.
		_ = commandRunner.Run("systemctl", "restart", "bluetooth.service")
		// Wait for bluetoothd D-Bus service to be ready before setting the alias.
		time.Sleep(2 * time.Second)
	}

	if cfg.Enabled {
		_ = commandRunner.Run("bluetoothctl", "power", "on")
		_ = commandRunner.Run("bluetoothctl", "discoverable", "on")
		_ = commandRunner.Run("bluetoothctl", "pairable", "on")
	}

	// Set adapter alias immediately via dbus-send and update the persistent
	// oceano-bt-alias service so the alias survives shairport-sync restarts.
	if cfg.Name != "" {
		// Sanitize: remove control characters (newlines break unit file, CRs corrupt it).
		safeName := strings.Map(func(r rune) rune {
			if r < 32 {
				return -1
			}
			return r
		}, cfg.Name)

		// Apply immediately to the running adapter. exec.Command args are not
		// shell-parsed, so spaces in safeName are safe here.
		_ = commandRunner.Run("dbus-send", "--system", "--print-reply", "--dest=org.bluez",
			"/org/bluez/hci0", "org.freedesktop.DBus.Properties.Set",
			"string:org.bluez.Adapter1", "string:Alias",
			"variant:string:"+safeName)

		// Build a quoted ExecStart argument so systemd does not split on spaces.
		// Escape backslashes and double quotes within the name first.
		escapedName := strings.ReplaceAll(strings.ReplaceAll(safeName, `\`, `\\`), `"`, `\"`)
		execArg := `"variant:string:` + escapedName + `"`

		// Update the oneshot service with the new name and restart it so the
		// alias is also re-applied after any future shairport-sync restart.
		unit := "[Unit]\nDescription=Restore Bluetooth adapter alias to " + safeName + "\n" +
			"After=shairport-sync.service\nWants=shairport-sync.service\n\n" +
			"[Service]\nType=oneshot\nExecStartPre=/bin/sleep 2\n" +
			"ExecStart=/usr/bin/dbus-send --system --print-reply --dest=org.bluez " +
			"/org/bluez/hci0 org.freedesktop.DBus.Properties.Set " +
			"string:org.bluez.Adapter1 string:Alias " + execArg + "\n" +
			"RemainAfterExit=no\n\n[Install]\nWantedBy=multi-user.target\n"
		_ = os.WriteFile("/etc/systemd/system/oceano-bt-alias.service", []byte(unit), 0o644)
		_ = commandRunner.Run("systemctl", "daemon-reload")
		_ = commandRunner.Run("systemctl", "restart", "oceano-bt-alias.service")
	}

	return nil
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
func writeManagerService(cfg Config, configPath string) error {
	execStart := formatExecStart(managerBinary, managerArgs(cfg, configPath))
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
	return serviceMgr.Restart(unit)
}

func unitContainsExecStart(unitPath, desiredExecStart string) (bool, error) {
	unitBytes, err := os.ReadFile(unitPath)
	if err != nil {
		return false, err
	}
	expectedLine := "ExecStart=" + desiredExecStart
	return strings.Contains(string(unitBytes), expectedLine), nil
}

