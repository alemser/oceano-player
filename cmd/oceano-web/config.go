package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SPIDisplayConfig controls the current oceano-now-playing SPI display service.
// These settings apply only to the attached SPI screen for now; future HDMI
// and DSI display configuration should live alongside this as separate config
// sections rather than being folded into the same SPI-specific knobs.
// Values are written to /etc/oceano/display.env and read as environment
// variables by the service — no code changes required in oceano-now-playing.
type SPIDisplayConfig struct {
	// UIPreset is the combined layout+mode preset (e.g. "high_contrast_rotate").
	// Controls both visual style and what is shown on the display.
	UIPreset string `json:"ui_preset"`
	// CycleTime is the number of seconds between text and artwork in rotate mode.
	CycleTime int `json:"cycle_time"`
	// StandbyTimeout is the number of seconds before the display sleeps.
	StandbyTimeout int `json:"standby_timeout"`
	// ExternalArtworkEnabled controls whether artwork is fetched from online providers.
	ExternalArtworkEnabled bool `json:"external_artwork_enabled"`
}

// BroadlinkConfig holds the pairing credentials for a Broadlink RM4 Mini device.
// These values are populated once during the pairing wizard and not changed afterwards.
type BroadlinkConfig struct {
	// Host is the local IP address of the RM4 Mini (e.g. "192.168.1.100").
	Host string `json:"host"`
	// Port is the Broadlink local API port (default 80).
	Port int `json:"port"`
	// Token is the hex-encoded pairing token obtained during the pairing handshake.
	Token string `json:"token"`
	// DeviceID is the hex-encoded device identifier returned during pairing.
	DeviceID string `json:"device_id"`
}

// AmplifierInputConfig declares a single selectable input on the amplifier.
type AmplifierInputConfig struct {
	// Label is the user-facing name shown in the UI (e.g. "USB Audio", "Phono").
	Label string `json:"label"`
	// ID is the internal identifier used to address IR commands (e.g. "USB", "PHONO").
	ID string `json:"id"`
}

// AmplifierConfig controls the IR-controlled amplifier (e.g. Magnat MR 780).
type AmplifierConfig struct {
	// Enabled controls whether amplifier control is active.
	Enabled bool `json:"enabled"`
	// Maker is the manufacturer name (e.g. "Magnat").
	Maker string `json:"maker"`
	// Model is the model name (e.g. "MR 780").
	Model string `json:"model"`
	// Inputs is the ordered list of selectable inputs on this amplifier.
	Inputs []AmplifierInputConfig `json:"inputs"`
	// DefaultInput is the Input.ID that the amplifier is assumed to start on.
	// Required because IR cycling (NextInput) needs a known starting point.
	DefaultInput string `json:"default_input"`
	// WarmupSeconds is the delay after power-on before audio is available.
	// Defaults to 30 for tube amplifiers like the Magnat MR 780.
	WarmupSeconds int `json:"warmup_seconds"`
	// InputSwitchDelaySeconds is the settling time after an input change.
	InputSwitchDelaySeconds int `json:"input_switch_delay_seconds"`
	// InputSelectionMode controls how SetInput sends IR commands.
	// "cycle"  — sends next_input repeatedly (e.g. Magnat MR 780, no direct IR per input).
	// "direct" — sends a single input-specific IR code (most modern amplifiers).
	InputSelectionMode string `json:"input_selection_mode"`
	// Broadlink holds the pairing credentials for the RM4 Mini controlling this device.
	Broadlink BroadlinkConfig `json:"broadlink"`
	// IRCodes maps command names to base64-encoded Broadlink IR codes.
	// Cycle mode keys:  "power_on", "power_off", "volume_up", "volume_down", "next_input"
	// Direct mode adds: "input_<ID>" for each input (e.g. "input_USB", "input_PHONO")
	// Values are populated via the IR learning workflow or copied from a
	// community database. Empty string means the command is not yet configured.
	IRCodes map[string]string `json:"ir_codes"`
}

// CDPlayerConfig controls the IR-controlled CD player (e.g. Yamaha CD-S300).
type CDPlayerConfig struct {
	// Enabled controls whether CD player control is active.
	Enabled bool `json:"enabled"`
	// Maker is the manufacturer name (e.g. "Yamaha").
	Maker string `json:"maker"`
	// Model is the model name (e.g. "CD-S300").
	Model string `json:"model"`
	// Broadlink holds the pairing credentials for the RM4 Mini controlling this device.
	// May share the same RM4 Mini as the amplifier (same host/token, different device_id).
	Broadlink BroadlinkConfig `json:"broadlink"`
	// IRCodes maps command names to base64-encoded Broadlink IR codes.
	// Keys: "play", "pause", "stop", "next", "previous", "power_on", "power_off".
	// Values are populated via the IR learning workflow or copied from a
	// community database. Empty string means the command is not yet configured.
	IRCodes map[string]string `json:"ir_codes"`
}

// Config is the central configuration for all Oceano services.
// It is stored at /etc/oceano/config.json and managed exclusively
// through the web UI. Each service reads its section on startup.
//
// Design principles:
//   - Every CLI flag exposed by oceano-source-detector and
//     oceano-state-manager has a corresponding field here.
//   - Device fields support two modes: auto-detect (DeviceMatch) or
//     explicit (Device). When Device is non-empty it takes precedence.
//   - The web server translates this struct into ExecStart arguments
//     when writing systemd service files.
type Config struct {
	AudioInput  AudioInputConfig  `json:"audio_input"`
	AudioOutput AudioOutputConfig `json:"audio_output"`
	Recognition RecognitionConfig `json:"recognition"`
	Advanced    AdvancedConfig    `json:"advanced"`
	Display     SPIDisplayConfig  `json:"display"`
	Weather     WeatherConfig     `json:"weather"`
	Amplifier   AmplifierConfig   `json:"amplifier"`
	CDPlayer    CDPlayerConfig    `json:"cd_player"`
}

// WeatherConfig controls idle-screen weather rendering in nowplaying.html.
// The web UI uses these values to query a weather provider directly from the
// browser (no backend weather proxy required).
type WeatherConfig struct {
	Enabled       bool    `json:"enabled"`
	LocationLabel string  `json:"location_label"`
	Latitude      float64 `json:"latitude"`
	Longitude     float64 `json:"longitude"`
	RefreshMins   int     `json:"refresh_mins"`
}

// AudioInputConfig controls the ALSA capture device used by
// oceano-source-detector to read audio from the amplifier REC OUT.
type AudioInputConfig struct {
	// DeviceMatch is a substring matched against /proc/asound/cards to
	// auto-detect the card number. Used when Device is empty.
	DeviceMatch string `json:"device_match"`
	// Device is an explicit ALSA device string (e.g. "plughw:3,0").
	// When set, DeviceMatch is ignored.
	Device string `json:"device"`
	// SilenceThreshold is the RMS level below which audio is considered
	// silence. Raise this if the phono stage has residual hum that causes
	// the source to oscillate between Physical and None.
	SilenceThreshold float64 `json:"silence_threshold"`
	// DebounceWindows is the majority-vote window size used to commit
	// source transitions. Higher = slower but more stable detection.
	DebounceWindows int `json:"debounce_windows"`
}

// AudioOutputConfig controls the AirPlay output device.
type AudioOutputConfig struct {
	// AirPlayName is the name shown in AirPlay device lists.
	AirPlayName string `json:"airplay_name"`
	// DeviceMatch is a substring matched against /proc/asound/cards to
	// auto-detect the USB DAC output device.
	DeviceMatch string `json:"device_match"`
	// Device is an explicit ALSA device string (e.g. "plughw:1,0").
	// When set, DeviceMatch is ignored.
	Device string `json:"device"`
}

// RecognitionConfig controls ACRCloud track identification.
type RecognitionConfig struct {
	// ACRCloudHost is the regional API endpoint.
	ACRCloudHost string `json:"acrcloud_host"`
	// ACRCloudAccessKey and ACRCloudSecretKey are the API credentials.
	ACRCloudAccessKey string `json:"acrcloud_access_key"`
	ACRCloudSecretKey string `json:"acrcloud_secret_key"`
	// CaptureDurationSecs is how many seconds of audio are sent per
	// recognition attempt. ACRCloud works well with 10s; minimum ~5s.
	CaptureDurationSecs int `json:"capture_duration_secs"`
	// MaxIntervalSecs is the fallback re-recognition interval when no
	// silence gap (track boundary) is detected and no track is identified.
	MaxIntervalSecs int `json:"max_interval_secs"`
	// RefreshIntervalSecs is how soon to re-check after a successful recognition
	// to catch gapless track changes (no silence gap). 0 = disabled.
	RefreshIntervalSecs int `json:"refresh_interval_secs"`
	// NoMatchBackoffSecs is how long to wait before retrying after the provider
	// returns no result. Lower values identify tracks faster at the cost of more
	// API calls. Default is 15s.
	NoMatchBackoffSecs int `json:"no_match_backoff_secs"`
	// ConfirmationDelaySecs is the delay before the second (confirmation) call.
	ConfirmationDelaySecs int `json:"confirmation_delay_secs"`
	// ConfirmationCaptureDurationSecs is the capture length for the confirmation call.
	ConfirmationCaptureDurationSecs int `json:"confirmation_capture_duration_secs"`
	// ConfirmationBypassScore skips confirmation when initial score >= value.
	// Set 0 to always require confirmation.
	ConfirmationBypassScore int `json:"confirmation_bypass_score"`
	// ShazamContinuityIntervalSecs controls how often Shazam checks whether the
	// currently playing physical track is still the same.
	ShazamContinuityIntervalSecs int `json:"shazam_continuity_interval_secs"`
	// ShazamContinuityCaptureDurationSecs controls the capture duration for each
	// periodic Shazam continuity check.
	ShazamContinuityCaptureDurationSecs int `json:"shazam_continuity_capture_duration_secs"`
	// RecognizerChain controls which API providers are active and their order.
	// Valid values: "acrcloud_first" (default), "shazam_first", "acrcloud_only", "shazam_only", "fingerprint_only".
	// Local fingerprint cache is always active as a final fallback. If the selected
	// provider policy resolves to no available API provider, the manager
	// automatically falls back to fingerprint-only recognition.
	RecognizerChain string `json:"recognizer_chain"`
	// FingerprintBoundaryLeadSkipSecs is how many seconds to discard at the
	// start of a boundary-triggered capture. Skipping a couple of seconds
	// avoids capturing vinyl crackle/transients before the music settles.
	// Default 2; set 0 to disable.
	FingerprintBoundaryLeadSkipSecs int `json:"fingerprint_boundary_lead_skip_secs"`
}

// AdvancedConfig holds paths and internal settings that rarely need
// to change. Exposed for completeness and debugging.
type AdvancedConfig struct {
	VUSocket     string `json:"vu_socket"`
	PCMSocket    string `json:"pcm_socket"`
	SourceFile   string `json:"source_file"`
	StateFile    string `json:"state_file"`
	ArtworkDir   string `json:"artwork_dir"`
	MetadataPipe string `json:"metadata_pipe"`
}

func defaultConfig() Config {
	return Config{
		AudioInput: AudioInputConfig{
			DeviceMatch:      "USB Microphone",
			SilenceThreshold: 0.025,
			DebounceWindows:  10,
		},
		AudioOutput: AudioOutputConfig{
			AirPlayName: "Oceano",
			DeviceMatch: "",
		},
		Recognition: RecognitionConfig{
			ACRCloudHost:                        "identify-eu-west-1.acrcloud.com",
			CaptureDurationSecs:                 7,
			MaxIntervalSecs:                     300,
			RefreshIntervalSecs:                 120,
			NoMatchBackoffSecs:                  15,
			ConfirmationDelaySecs:               0,
			ConfirmationCaptureDurationSecs:     4,
			ConfirmationBypassScore:             95,
			ShazamContinuityIntervalSecs:        8,
			ShazamContinuityCaptureDurationSecs: 4,
			RecognizerChain:                     "acrcloud_first",
			FingerprintBoundaryLeadSkipSecs:     2,
		},
		Advanced: AdvancedConfig{
			VUSocket:     "/tmp/oceano-vu.sock",
			PCMSocket:    "/tmp/oceano-pcm.sock",
			SourceFile:   "/tmp/oceano-source.json",
			StateFile:    "/tmp/oceano-state.json",
			ArtworkDir:   "/var/lib/oceano/artwork",
			MetadataPipe: "/tmp/shairport-sync-metadata",
		},
		Display: SPIDisplayConfig{
			UIPreset:               "high_contrast_rotate",
			CycleTime:              30,
			StandbyTimeout:         600,
			ExternalArtworkEnabled: true,
		},
		Weather: WeatherConfig{
			Enabled:       true,
			LocationLabel: "Dublin",
			Latitude:      53.3498,
			Longitude:     -6.2603,
			RefreshMins:   10,
		},
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func saveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// detectorArgs translates AudioInput and Advanced config into
// ExecStart arguments for oceano-source-detector.service.
func detectorArgs(cfg Config) []string {
	args := []string{}
	inp := cfg.AudioInput
	adv := cfg.Advanced

	if inp.Device != "" {
		args = append(args, "--device", inp.Device)
	} else if inp.DeviceMatch != "" {
		args = append(args, "--device-match", inp.DeviceMatch)
	}
	args = append(args,
		"--silence-threshold", fmt.Sprintf("%.4f", inp.SilenceThreshold),
		"--debounce", fmt.Sprintf("%d", inp.DebounceWindows),
		"--output", adv.SourceFile,
		"--vu-socket", adv.VUSocket,
		"--pcm-socket", adv.PCMSocket,
	)
	return args
}

// managerArgs translates Recognition and Advanced config into
// ExecStart arguments for oceano-state-manager.service.
func managerArgs(cfg Config) []string {
	rec := cfg.Recognition
	adv := cfg.Advanced
	args := []string{
		"--metadata-pipe", adv.MetadataPipe,
		"--source-file", adv.SourceFile,
		"--output", adv.StateFile,
		"--artwork-dir", adv.ArtworkDir,
		"--vu-socket", adv.VUSocket,
		"--pcm-socket", adv.PCMSocket,
		"--recognizer-capture-duration", fmt.Sprintf("%ds", rec.CaptureDurationSecs),
		"--recognizer-max-interval", fmt.Sprintf("%ds", rec.MaxIntervalSecs),
		"--recognizer-refresh-interval", fmt.Sprintf("%ds", rec.RefreshIntervalSecs),
		"--recognizer-no-match-backoff", fmt.Sprintf("%ds", rec.NoMatchBackoffSecs),
		"--confirmation-delay", fmt.Sprintf("%ds", rec.ConfirmationDelaySecs),
		"--confirmation-capture-duration", fmt.Sprintf("%ds", rec.ConfirmationCaptureDurationSecs),
		"--confirmation-bypass-score", fmt.Sprintf("%d", rec.ConfirmationBypassScore),
		"--shazam-continuity-interval", fmt.Sprintf("%ds", rec.ShazamContinuityIntervalSecs),
		"--shazam-continuity-capture-duration", fmt.Sprintf("%ds", rec.ShazamContinuityCaptureDurationSecs),
		"--recognizer-chain", rec.RecognizerChain,
	}
	if rec.FingerprintBoundaryLeadSkipSecs > 0 {
		args = append(args, "--fingerprint-boundary-lead-skip", fmt.Sprintf("%d", rec.FingerprintBoundaryLeadSkipSecs))
	}
	if rec.ACRCloudHost != "" {
		args = append(args,
			"--acrcloud-host", rec.ACRCloudHost,
			"--acrcloud-access-key", rec.ACRCloudAccessKey,
			"--acrcloud-secret-key", rec.ACRCloudSecretKey,
		)
	}
	// --verbose is a boolean flag with no value — must be last so formatExecStart
	// does not pair it with the next argument.
	args = append(args, "--verbose")
	return args
}

// saveSPIDisplayEnv writes /etc/oceano/display.env so that oceano-now-playing
// picks up SPI display settings as environment variables (EnvironmentFile=).
func saveSPIDisplayEnv(path string, cfg SPIDisplayConfig) error {
	artwork := "true"
	if !cfg.ExternalArtworkEnabled {
		artwork = "false"
	}
	content := fmt.Sprintf(
		"UI_PRESET=%s\nCYCLE_TIME=%d\nSTANDBY_TIMEOUT=%d\nEXTERNAL_ARTWORK_ENABLED=%s\n",
		cfg.UIPreset, cfg.CycleTime, cfg.StandbyTimeout, artwork,
	)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// formatExecStart builds the ExecStart line for a systemd service file.
func formatExecStart(binary string, args []string) string {
	parts := []string{binary}
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			parts = append(parts, fmt.Sprintf("%s %q", args[i], args[i+1]))
		} else {
			parts = append(parts, args[i])
		}
	}
	return strings.Join(parts, " \\\n  ")
}
