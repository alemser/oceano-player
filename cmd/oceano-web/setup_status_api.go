package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const setupStatusSchemaVersion = 1

// setupStatus is the response body for GET /api/setup-status.
// Field names and semantics follow the contract in
// docs/plans/configuration-onboarding-improvement-plan.md §"GET /api/setup-status".
type setupStatus struct {
	SchemaVersion int `json:"schema_version"`

	// CLI / foundation
	OceanoSetupAcknowledged bool `json:"oceano_setup_acknowledged"`

	// Capture
	CaptureConfigured bool `json:"capture_configured"`

	// Recognition
	RecognitionCredentialsSet bool `json:"recognition_credentials_set"`

	// Amplifier topology
	AmplifierTopologyComplete bool `json:"amplifier_topology_complete"`
	AmplifierIREnabled        bool `json:"amplifier_ir_enabled"`
	BroadlinkPaired           bool `json:"broadlink_paired"`

	// Calibration
	CalibrationPhysicalRecommended bool `json:"calibration_physical_recommended"`
	CalibrationPhysicalComplete    bool `json:"calibration_physical_complete"`

	// Vinyl / stylus
	VinylTopologyPresent      bool `json:"vinyl_topology_present"`
	StylusTrackingRecommended bool `json:"stylus_tracking_recommended"`
	StylusProfileConfigured   bool `json:"stylus_profile_configured"`

	// Service health (map key = snake_case service name without .service suffix)
	ServicesHealthy map[string]bool `json:"services_healthy"`
}

// monitoredServices lists the systemd units checked for health.
// Keys in ServicesHealthy use underscores in place of hyphens.
var monitoredServices = []string{
	"oceano-source-detector",
	"oceano-state-manager",
	"oceano-web",
}

var (
	svcHealthMu     sync.Mutex
	svcHealthCache  map[string]bool
	svcHealthExpiry time.Time
	svcHealthTTL    = 20 * time.Second
	systemctlOnce   sync.Once
	systemctlFound  bool
)

var (
	stylusCacheMu     sync.Mutex
	stylusCacheDBPath string
	stylusCacheValue  bool
	stylusCacheExpiry time.Time
	stylusCacheTTL    = 30 * time.Second
)

func hasSystemctl() bool {
	systemctlOnce.Do(func() {
		_, err := exec.LookPath("systemctl")
		systemctlFound = err == nil
	})
	return systemctlFound
}

// queryServicesHealthy calls systemctl is-active for each monitored service,
// caching the result for svcHealthTTL to avoid forking on every hub poll.
func queryServicesHealthy() map[string]bool {
	svcHealthMu.Lock()
	defer svcHealthMu.Unlock()

	if !hasSystemctl() {
		if svcHealthCache == nil {
			svcHealthCache = map[string]bool{}
		}
		svcHealthExpiry = time.Now().Add(svcHealthTTL)
		return svcHealthCache
	}

	if time.Now().Before(svcHealthExpiry) && svcHealthCache != nil {
		return svcHealthCache
	}

	result := make(map[string]bool, len(monitoredServices))
	for _, svc := range monitoredServices {
		key := strings.ReplaceAll(svc, "-", "_")
		err := exec.Command("systemctl", "is-active", "--quiet", svc).Run()
		result[key] = err == nil
	}

	svcHealthCache = result
	svcHealthExpiry = time.Now().Add(svcHealthTTL)
	return result
}

// effectiveRole returns the device role, treating absent as "physical_media"
// per the migration rule in the plan.
func effectiveRole(dev ConnectedDeviceConfig) string {
	r := strings.TrimSpace(dev.Role)
	r = strings.ToLower(r)
	if r == "" {
		return "physical_media"
	}
	if r != "physical_media" && r != "streaming" && r != "other" {
		return "physical_media"
	}
	return r
}

func effectivePhysicalFormat(dev ConnectedDeviceConfig) string {
	f := strings.ToLower(strings.TrimSpace(dev.PhysicalFormat))
	if f == "" {
		return "unspecified"
	}
	switch f {
	case "vinyl", "cd", "tape", "mixed", "unspecified":
		return f
	default:
		return "unspecified"
	}
}

// physicalMediaInputIDs returns the deduplicated amplifier input IDs belonging
// to devices classified as physical_media (or legacy is_turntable).
func physicalMediaInputIDs(cfg Config) []string {
	seen := map[string]bool{}
	var ids []string
	for _, dev := range cfg.Amplifier.ConnectedDevices {
		if effectiveRole(dev) != "physical_media" && !dev.IsTurntable {
			continue
		}
		for _, inputID := range dev.InputIDs {
			id := strings.TrimSpace(string(inputID))
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// stylusProfileConfigured returns true when the library DB contains an active
// (non-replaced) stylus row — i.e. the user has completed stylus onboarding.
func stylusProfileConfigured(libraryDB string) bool {
	dbPath := strings.TrimSpace(libraryDB)
	if dbPath == "" {
		return false
	}

	now := time.Now()
	stylusCacheMu.Lock()
	if dbPath == stylusCacheDBPath && now.Before(stylusCacheExpiry) {
		cached := stylusCacheValue
		stylusCacheMu.Unlock()
		return cached
	}
	stylusCacheMu.Unlock()

	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return false
	}
	defer db.Close()

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM stylus_profiles WHERE replaced_at IS NULL`).Scan(&count)
	ok := err == nil && count > 0

	stylusCacheMu.Lock()
	stylusCacheDBPath = dbPath
	stylusCacheValue = ok
	stylusCacheExpiry = time.Now().Add(stylusCacheTTL)
	stylusCacheMu.Unlock()
	return ok
}

// computeSetupStatus derives setup readiness from config and the library DB.
func computeSetupStatus(cfg Config, libraryDB string) setupStatus {
	s := setupStatus{
		SchemaVersion:   setupStatusSchemaVersion,
		ServicesHealthy: queryServicesHealthy(),
	}

	// oceano-setup CLI completion flag.
	s.OceanoSetupAcknowledged = cfg.Advanced.OceanoSetupAcknowledged

	// Capture: non-empty device_match or explicit device string.
	s.CaptureConfigured = strings.TrimSpace(cfg.AudioInput.DeviceMatch) != "" ||
		strings.TrimSpace(cfg.AudioInput.Device) != ""

	// Recognition: all three ACRCloud credentials present.
	s.RecognitionCredentialsSet = strings.TrimSpace(cfg.Recognition.ACRCloudHost) != "" &&
		strings.TrimSpace(cfg.Recognition.ACRCloudAccessKey) != "" &&
		strings.TrimSpace(cfg.Recognition.ACRCloudSecretKey) != ""

	// Amplifier topology: ≥1 input with non-empty ID and logical name.
	for _, inp := range cfg.Amplifier.Inputs {
		if strings.TrimSpace(string(inp.ID)) != "" && strings.TrimSpace(inp.LogicalName) != "" {
			s.AmplifierTopologyComplete = true
			break
		}
	}

	// IR enabled and Broadlink pairing.
	s.AmplifierIREnabled = cfg.Amplifier.Enabled
	if s.AmplifierIREnabled {
		s.BroadlinkPaired = strings.TrimSpace(cfg.Amplifier.Broadlink.Host) != "" &&
			strings.TrimSpace(cfg.Amplifier.Broadlink.Token) != ""
	}

	// Vinyl topology: physical_media device with physical_format=vinyl, or legacy is_turntable.
	for _, dev := range cfg.Amplifier.ConnectedDevices {
		if effectiveRole(dev) == "physical_media" && effectivePhysicalFormat(dev) == "vinyl" {
			s.VinylTopologyPresent = true
			break
		}
		if dev.IsTurntable {
			s.VinylTopologyPresent = true
			break
		}
	}

	// Calibration: check physical input IDs against stored profiles.
	physIDs := physicalMediaInputIDs(cfg)
	if len(physIDs) > 0 {
		allCalibrated := true
		anyMissing := false
		for _, id := range physIDs {
			prof, ok := cfg.Advanced.CalibrationProfiles[id]
			if !ok || prof.Off == nil || prof.On == nil {
				allCalibrated = false
				anyMissing = true
			}
		}
		s.CalibrationPhysicalComplete = allCalibrated
		s.CalibrationPhysicalRecommended = anyMissing
	}

	// Stylus.
	s.StylusProfileConfigured = stylusProfileConfigured(libraryDB)
	s.StylusTrackingRecommended = s.VinylTopologyPresent && !s.StylusProfileConfigured

	return s
}

func handleSetupStatus(configPath, libraryDB string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg, err := loadConfig(configPath)
		if err != nil {
			jsonError(w, "failed to load config", http.StatusInternalServerError)
			return
		}
		effectiveLibraryDB := strings.TrimSpace(cfg.Advanced.LibraryDB)
		if effectiveLibraryDB == "" {
			effectiveLibraryDB = strings.TrimSpace(libraryDB)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(computeSetupStatus(cfg, effectiveLibraryDB))
	}
}
