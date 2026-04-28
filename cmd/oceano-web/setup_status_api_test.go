package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newSetupStatusConfig returns a minimal Config that represents a fully-configured
// system: capture, ACRCloud credentials, 1 amplifier input, and a phono vinyl device.
func newSetupStatusConfig() Config {
	cfg := defaultConfig()
	cfg.AudioInput.DeviceMatch = "USB Audio"
	cfg.Recognition.ACRCloudHost = "identify-eu-west-1.acrcloud.com"
	cfg.Recognition.ACRCloudAccessKey = "key"
	cfg.Recognition.ACRCloudSecretKey = "secret"
	cfg.Amplifier.Inputs = []AmplifierInputConfig{
		{ID: AmplifierInputID("10"), LogicalName: "Phono", Visible: true},
	}
	return cfg
}

func TestComputeSetupStatus_FreshInstall(t *testing.T) {
	cfg := defaultConfig()
	s := computeSetupStatus(cfg, "")

	if s.SchemaVersion != setupStatusSchemaVersion {
		t.Errorf("schema_version = %d, want %d", s.SchemaVersion, setupStatusSchemaVersion)
	}
	if s.OceanoSetupAcknowledged {
		t.Error("oceano_setup_acknowledged should be false on fresh install")
	}
	if s.CaptureConfigured {
		t.Error("capture_configured should be false on fresh install")
	}
	if s.RecognitionCredentialsSet {
		t.Error("recognition_credentials_set should be false on fresh install")
	}
	if s.AmplifierTopologyComplete {
		t.Error("amplifier_topology_complete should be false on fresh install")
	}
	if s.AmplifierIREnabled {
		t.Error("amplifier_ir_enabled should be false on fresh install")
	}
	if s.VinylTopologyPresent {
		t.Error("vinyl_topology_present should be false on fresh install")
	}
	if s.CalibrationPhysicalRecommended {
		t.Error("calibration_physical_recommended should be false when no devices")
	}
	if s.CalibrationPhysicalComplete {
		t.Error("calibration_physical_complete should be false on fresh install")
	}
	if s.StylusTrackingRecommended {
		t.Error("stylus_tracking_recommended should be false when no vinyl topology")
	}
}

func TestComputeSetupStatus_CaptureConfigured(t *testing.T) {
	cfg := defaultConfig()

	cfg.AudioInput.DeviceMatch = "USB Audio"
	if s := computeSetupStatus(cfg, ""); !s.CaptureConfigured {
		t.Error("CaptureConfigured should be true when DeviceMatch is set")
	}

	cfg.AudioInput.DeviceMatch = ""
	cfg.AudioInput.Device = "plughw:3,0"
	if s := computeSetupStatus(cfg, ""); !s.CaptureConfigured {
		t.Error("CaptureConfigured should be true when Device is set")
	}
}

func TestComputeSetupStatus_RecognitionCredentials(t *testing.T) {
	cfg := defaultConfig()
	cfg.Recognition.ACRCloudHost = "identify-eu-west-1.acrcloud.com"
	cfg.Recognition.ACRCloudAccessKey = "key"

	// Missing secret.
	if s := computeSetupStatus(cfg, ""); s.RecognitionCredentialsSet {
		t.Error("RecognitionCredentialsSet should be false when secret is missing")
	}

	cfg.Recognition.ACRCloudSecretKey = "secret"
	if s := computeSetupStatus(cfg, ""); !s.RecognitionCredentialsSet {
		t.Error("RecognitionCredentialsSet should be true when all credentials set")
	}
}

func TestComputeSetupStatus_AmplifierTopology(t *testing.T) {
	cfg := defaultConfig()

	// Empty inputs → not complete.
	if s := computeSetupStatus(cfg, ""); s.AmplifierTopologyComplete {
		t.Error("AmplifierTopologyComplete should be false with empty inputs")
	}

	// Input with empty logical name → not complete.
	cfg.Amplifier.Inputs = []AmplifierInputConfig{{ID: "10", LogicalName: ""}}
	if s := computeSetupStatus(cfg, ""); s.AmplifierTopologyComplete {
		t.Error("AmplifierTopologyComplete should be false when LogicalName is empty")
	}

	// Well-formed input → complete.
	cfg.Amplifier.Inputs = []AmplifierInputConfig{{ID: "10", LogicalName: "Phono", Visible: true}}
	if s := computeSetupStatus(cfg, ""); !s.AmplifierTopologyComplete {
		t.Error("AmplifierTopologyComplete should be true with ≥1 valid input")
	}
}

func TestComputeSetupStatus_IRAndBroadlink(t *testing.T) {
	cfg := defaultConfig()
	cfg.Amplifier.Enabled = true

	// No pairing → broadlink_paired = false.
	s := computeSetupStatus(cfg, "")
	if !s.AmplifierIREnabled {
		t.Error("amplifier_ir_enabled should be true when Enabled=true")
	}
	if s.BroadlinkPaired {
		t.Error("broadlink_paired should be false without credentials")
	}

	// With host+token → paired.
	cfg.Amplifier.Broadlink.Host = "192.168.1.100"
	cfg.Amplifier.Broadlink.Token = "aabbccdd"
	s = computeSetupStatus(cfg, "")
	if !s.BroadlinkPaired {
		t.Error("broadlink_paired should be true when host+token are set")
	}

	// IR disabled → broadlink_paired stays false regardless of credentials.
	cfg.Amplifier.Enabled = false
	s = computeSetupStatus(cfg, "")
	if s.BroadlinkPaired {
		t.Error("broadlink_paired should be false when IR is disabled")
	}
}

func TestComputeSetupStatus_VinylTopology(t *testing.T) {
	cfg := defaultConfig()

	// No devices → no vinyl.
	if s := computeSetupStatus(cfg, ""); s.VinylTopologyPresent {
		t.Error("vinyl_topology_present should be false with no devices")
	}

	// IsTurntable legacy flag.
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{
		{ID: "tt1", Name: "Rega Planar", IsTurntable: true},
	}
	if s := computeSetupStatus(cfg, ""); !s.VinylTopologyPresent {
		t.Error("vinyl_topology_present should be true via IsTurntable")
	}

	// Role+PhysicalFormat (Phase 4 fields).
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{
		{ID: "tt2", Name: "Thorens", Role: "physical_media", PhysicalFormat: "vinyl"},
	}
	if s := computeSetupStatus(cfg, ""); !s.VinylTopologyPresent {
		t.Error("vinyl_topology_present should be true via Role+PhysicalFormat")
	}

	// Streaming device is not vinyl.
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{
		{ID: "str1", Name: "Chromecast", Role: "streaming"},
	}
	if s := computeSetupStatus(cfg, ""); s.VinylTopologyPresent {
		t.Error("vinyl_topology_present should be false for streaming device")
	}
}

func TestComputeSetupStatus_CalibrationReadiness(t *testing.T) {
	cfg := defaultConfig()
	cfg.Amplifier.Inputs = []AmplifierInputConfig{
		{ID: "10", LogicalName: "Phono", Visible: true},
	}
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{
		{ID: "tt1", Name: "Rega", IsTurntable: true, InputIDs: []AmplifierInputID{"10"}},
	}

	// No calibration profiles → recommended, not complete.
	s := computeSetupStatus(cfg, "")
	if !s.CalibrationPhysicalRecommended {
		t.Error("calibration_physical_recommended should be true when no profile for physical input")
	}
	if s.CalibrationPhysicalComplete {
		t.Error("calibration_physical_complete should be false when profiles missing")
	}

	// Add profile with off+on → complete.
	cfg.Advanced.CalibrationProfiles = map[string]CalibrationProfile{
		"10": {
			Off: &CalibrationSample{AvgRMS: 0.007},
			On:  &CalibrationSample{AvgRMS: 0.013},
		},
	}
	s = computeSetupStatus(cfg, "")
	if s.CalibrationPhysicalRecommended {
		t.Error("calibration_physical_recommended should be false when profile complete")
	}
	if !s.CalibrationPhysicalComplete {
		t.Error("calibration_physical_complete should be true when off+on present")
	}
}

func TestComputeSetupStatus_StylusTracking(t *testing.T) {
	cfg := defaultConfig()
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{
		{ID: "tt1", Name: "Rega", IsTurntable: true},
	}

	// Vinyl topology but no DB → stylus not configured → tracking recommended.
	s := computeSetupStatus(cfg, "")
	if !s.VinylTopologyPresent {
		t.Fatal("vinyl_topology_present should be true")
	}
	if s.StylusProfileConfigured {
		t.Error("stylus_profile_configured should be false without DB")
	}
	if !s.StylusTrackingRecommended {
		t.Error("stylus_tracking_recommended should be true when vinyl+no stylus")
	}
}

func TestComputeSetupStatus_OceanoSetupAcknowledged(t *testing.T) {
	cfg := defaultConfig()
	if s := computeSetupStatus(cfg, ""); s.OceanoSetupAcknowledged {
		t.Error("should be false by default")
	}
	cfg.Advanced.OceanoSetupAcknowledged = true
	if s := computeSetupStatus(cfg, ""); !s.OceanoSetupAcknowledged {
		t.Error("should be true when flag is set")
	}
}

func TestHandleSetupStatus_GET(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.json"
	cfg := newSetupStatusConfig()
	if err := saveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	handler := handleSetupStatus(cfgPath, "")
	r := httptest.NewRequest(http.MethodGet, "/api/setup-status", nil)
	w := httptest.NewRecorder()
	handler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var s setupStatus
	if err := json.NewDecoder(w.Body).Decode(&s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s.SchemaVersion != setupStatusSchemaVersion {
		t.Errorf("schema_version = %d, want %d", s.SchemaVersion, setupStatusSchemaVersion)
	}
	if !s.CaptureConfigured {
		t.Error("CaptureConfigured should be true for the test config")
	}
	if !s.RecognitionCredentialsSet {
		t.Error("RecognitionCredentialsSet should be true for the test config")
	}
	if !s.AmplifierTopologyComplete {
		t.Error("AmplifierTopologyComplete should be true for the test config")
	}
}

func TestHandleSetupStatus_WrongMethod(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.json"
	_ = saveConfig(cfgPath, defaultConfig())

	handler := handleSetupStatus(cfgPath, "")
	r := httptest.NewRequest(http.MethodPost, "/api/setup-status", nil)
	w := httptest.NewRecorder()
	handler(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

func TestPhysicalMediaInputIDs(t *testing.T) {
	cfg := defaultConfig()

	// No devices → empty.
	if ids := physicalMediaInputIDs(cfg); len(ids) != 0 {
		t.Errorf("expected empty, got %v", ids)
	}

	// Missing Role defaults to physical_media.
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{
		{ID: "d1", Name: "Turntable", InputIDs: []AmplifierInputID{"10", "20"}},
	}
	ids := physicalMediaInputIDs(cfg)
	if len(ids) != 2 {
		t.Errorf("expected 2 IDs, got %v", ids)
	}

	// Streaming device excluded.
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{
		{ID: "d2", Name: "Chromecast", Role: "streaming", InputIDs: []AmplifierInputID{"40"}},
	}
	if ids := physicalMediaInputIDs(cfg); len(ids) != 0 {
		t.Errorf("streaming device should be excluded, got %v", ids)
	}

	// Deduplication.
	cfg.Amplifier.ConnectedDevices = []ConnectedDeviceConfig{
		{ID: "d3", Name: "TT", IsTurntable: true, InputIDs: []AmplifierInputID{"10"}},
		{ID: "d4", Name: "CD", Role: "physical_media", InputIDs: []AmplifierInputID{"10", "20"}},
	}
	ids = physicalMediaInputIDs(cfg)
	if len(ids) != 2 {
		t.Errorf("expected 2 deduplicated IDs, got %v", ids)
	}
}
