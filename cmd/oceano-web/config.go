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

// InputCyclingConfig controls the optional input-cycling probe used as a
// last resort to detect whether the amplifier is on when neither USB nor
// RMS signals are conclusive (e.g. no source is playing).
//
// Cycling sends repeated IR navigation commands until the USB DAC appears or
// MaxCycles is exhausted. It is only performed when the amplifier's RMS has
// been near zero for at least MinSilenceSecs seconds.
//
// This feature is amp-specific: only enable it when the IR codes and timing
// have been validated for a particular model.
type InputCyclingConfig struct {
	// Enabled controls whether input cycling is attempted during detection.
	Enabled bool `json:"enabled"`
	// Direction is which input navigation command to send: "prev" or "next".
	// Magnat MR 780 uses "prev" to cycle backwards to USB Audio.
	Direction string `json:"direction"`
	// MaxCycles is the maximum number of input steps before giving up.
	MaxCycles int `json:"max_cycles"`
	// StepWaitSecs is how many seconds to wait after each input step before
	// checking for USB DAC presence. Allows the amp to switch and enumerate.
	StepWaitSecs int `json:"step_wait_secs"`
	// MinSilenceSecs is the minimum number of seconds RMS must have been near
	// zero before cycling is allowed. Prevents interrupting active playback.
	MinSilenceSecs int `json:"min_silence_secs"`
}

// USBResetConfig controls how the web-triggered "reset to USB input" flow
// behaves for amplifiers that cycle inputs with repeated IR presses.
type USBResetConfig struct {
	// MaxAttempts is the maximum number of input jumps before giving up.
	MaxAttempts int `json:"max_attempts"`
	// FirstStepSettleMS is the short delay after the first press in a cycle.
	// This allows the selector highlight to settle before probing DAC visibility.
	FirstStepSettleMS int `json:"first_step_settle_ms"`
	// StepWaitMS is the delay after the effective input-jump press before probing
	// DAC visibility.
	StepWaitMS int `json:"step_wait_ms"`
}

// AmplifierInputID is an internal stable input identifier used by runtime
// logic and IR mapping. It accepts either JSON string or number.
type AmplifierInputID string

// UnmarshalJSON accepts "usb" and 40-style JSON IDs, normalizing both to
// string form internally.
func (id *AmplifierInputID) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		*id = ""
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*id = AmplifierInputID(strings.TrimSpace(s))
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("amplifier input id must be string or number")
	}
	*id = AmplifierInputID(n.String())
	return nil
}

// MarshalJSON serializes the normalized string form.
func (id AmplifierInputID) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(id))
}

// AmplifierInputConfig defines one registered amplifier input.
type AmplifierInputConfig struct {
	// ID is hidden from end users and used only for stable internal mapping.
	ID AmplifierInputID `json:"id"`
	// LogicalName is the user-facing label.
	LogicalName string `json:"logical_name"`
	// Visible controls whether this input is shown in the primary UI selectors.
	Visible bool `json:"visible"`
	// RecognitionPolicy controls how physical-source recognition behaves on this input:
	// "auto" (default conservative deduction), "library", "display_only", or "off".
	RecognitionPolicy string `json:"recognition_policy,omitempty"`
}

// ConnectedDeviceConfig describes a physical device connected to one or more
// amplifier inputs (e.g. turntable, CD player, streaming source).
// Devices are shown in the input selector to replace raw input names.
type ConnectedDeviceConfig struct {
	// ID is a stable internal identifier (not shown to users).
	ID string `json:"id"`
	// Name is the user-facing label shown in the input selector (e.g. "Yamaha CD-S300").
	Name string `json:"name"`
	// InputIDs lists the amplifier input IDs this device is connected to.
	InputIDs []AmplifierInputID `json:"input_ids,omitempty"`
	// HasRemote indicates whether this device has a remote control (IR codes).
	HasRemote bool `json:"has_remote,omitempty"`
	// IsTurntable marks this connected device as the vinyl source (legacy field).
	// Prefer Role="physical_media" + PhysicalFormat="vinyl" for new entries.
	// Calibration wizard uses this to target the proper input(s) even when
	// the input label is not explicitly "Phono".
	IsTurntable bool `json:"is_turntable,omitempty"`
	// Role classifies the device: "physical_media", "streaming", or "other".
	// Absent value is treated as "physical_media" (safe migration default —
	// existing connected devices are almost always physical sources).
	Role string `json:"role,omitempty"`
	// PhysicalFormat is the media type when Role == "physical_media":
	// "vinyl", "cd", "tape", "mixed", or "unspecified" (default when absent).
	// Drives vinyl gap copy, Now Playing format chips, and stylus hour accumulation.
	PhysicalFormat string `json:"physical_format,omitempty"`
	// IRCodes maps command names to base64-encoded Broadlink IR codes.
	// Keys follow the same convention as CD player: power_on, power_off,
	// play, pause, stop, next, previous, eject.
	IRCodes map[string]string `json:"ir_codes,omitempty"`
}

// AmplifierConfig controls the IR-controlled amplifier (e.g. Magnat MR 780).
type AmplifierConfig struct {
	// ProfileID selects a built-in/custom profile baseline to resolve from.
	// Empty preserves legacy field-only behavior.
	ProfileID string `json:"profile_id"`
	// InputMode is "cycle" or "direct".
	InputMode string `json:"input_mode"`
	// Inputs lists all registered amplifier inputs. Index 0 is the default input.
	Inputs []AmplifierInputConfig `json:"inputs"`
	// Enabled controls whether amplifier control is active.
	Enabled bool `json:"enabled"`
	// Maker is the manufacturer name (e.g. "Magnat").
	Maker string `json:"maker"`
	// Model is the model name (e.g. "MR 780").
	Model string `json:"model"`
	// Broadlink holds the pairing credentials for the RM4 Mini controlling this device.
	Broadlink BroadlinkConfig `json:"broadlink"`
	// IRCodes maps command names to base64-encoded Broadlink IR codes.
	// Keys: "power_on", "power_off", "volume_up", "volume_down", "next_input", "prev_input"
	// Values are populated via the IR learning workflow or copied from a
	// community database. Empty string means the command is not yet configured.
	IRCodes map[string]string `json:"ir_codes"`
	// WarmUpSecs is how long to wait after a power-on command before the amp
	// is expected to be ready. Tube amplifiers (e.g. Magnat MR 780) take ~30s.
	// During this window the monitor reports PowerStateWarmingUp.
	WarmUpSecs int `json:"warm_up_secs"`
	// StandbyTimeoutMins is the amplifier's auto-standby delay in minutes.
	// When RMS has been silent for longer than this, the monitor infers
	// PowerStateStandby instead of PowerStateOn.
	StandbyTimeoutMins int `json:"standby_timeout_mins"`
	// InputCycling controls the optional last-resort input-cycling probe.
	InputCycling InputCyclingConfig `json:"input_cycling"`
	// CycleArmingSettleMS is the settle delay (ms) after the arming pulse in
	// cycle mode when the selector is not active yet.
	CycleArmingSettleMS int `json:"cycle_arming_settle_ms,omitempty"`
	// CycleStepNextWaitMS is the inter-step delay (ms) for forward cycle input
	// navigation in cycle mode.
	CycleStepNextWaitMS int `json:"cycle_step_next_wait_ms,omitempty"`
	// CycleStepPrevWaitMS is the inter-step delay (ms) for reverse cycle input
	// navigation in cycle mode.
	CycleStepPrevWaitMS int `json:"cycle_step_prev_wait_ms,omitempty"`
	// USBReset controls the manual/automatic "reset to USB input" flow timing.
	USBReset USBResetConfig `json:"usb_reset"`
	// ConnectedDevices lists physical devices wired to the amplifier inputs.
	// Device names replace raw input names in the input selector.
	ConnectedDevices []ConnectedDeviceConfig `json:"connected_devices,omitempty"`
}

// AmplifierRuntimeConfig stores runtime-only UI/transport state that should
// persist across restarts but must not belong to reusable amplifier profiles.
type AmplifierRuntimeConfig struct {
	// LastKnownInputID is the last amplifier input explicitly selected or
	// confidently inferred by the web UI / reset flow.
	LastKnownInputID AmplifierInputID `json:"last_known_input_id,omitempty"`
}

// StoredAmplifierProfile holds a reusable amplifier profile persisted in
// config.json for activation/import/export workflows.
type StoredAmplifierProfile struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Origin string          `json:"origin,omitempty"` // builtin | custom | imported
	Config AmplifierConfig `json:"config"`
}

// CDPlayerConfig is a legacy config block kept only for backward-compatibility
// migration from old config.json files that still contain "cd_player".
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
	// InputIDs lists the amplifier input IDs that correspond to this CD player.
	// Used to automatically switch the amplifier to the correct input when
	// CD playback starts. An empty slice means no automatic input switching.
	InputIDs []AmplifierInputID `json:"input_ids,omitempty"`
	// IRCodes maps command names to base64-encoded Broadlink IR codes.
	// Keys: "play", "pause", "stop", "next", "previous", "power_on", "power_off".
	// Values are populated via the IR learning workflow or copied from a
	// community database. Empty string means the command is not yet configured.
	IRCodes map[string]string `json:"ir_codes"`
}

// BluetoothConfig controls the built-in Bluetooth adapter.
type BluetoothConfig struct {
	// Enabled controls whether Bluetooth is powered on and the adapter is
	// discoverable. When false, the adapter is left in its current state
	// (not explicitly powered off to avoid disconnecting paired devices).
	Enabled bool `json:"enabled"`
	// Name is the device name advertised in Bluetooth device lists.
	// Defaults to the AirPlay name when empty.
	Name string `json:"name"`
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
	AudioInput        AudioInputConfig         `json:"audio_input"`
	AudioOutput       AudioOutputConfig        `json:"audio_output"`
	Bluetooth         BluetoothConfig          `json:"bluetooth"`
	Recognition       RecognitionConfig        `json:"recognition"`
	Advanced          AdvancedConfig           `json:"advanced"`
	Display           SPIDisplayConfig         `json:"display"`
	Weather           WeatherConfig            `json:"weather"`
	NowPlaying        NowPlayingConfig         `json:"now_playing"`
	Amplifier         AmplifierConfig          `json:"amplifier"`
	AmplifierRuntime  AmplifierRuntimeConfig   `json:"amplifier_runtime,omitempty"`
	AmplifierProfiles []StoredAmplifierProfile `json:"amplifier_profiles,omitempty"`
	// LegacyCDPlayer is read from older files and migrated into
	// amplifier.connected_devices at load time.
	LegacyCDPlayer *CDPlayerConfig `json:"cd_player,omitempty"`
}

// NowPlayingConfig controls visual effects on the nowplaying.html display page.
type NowPlayingConfig struct {
	// AmbientColorEnabled extracts a dominant colour from the current track artwork
	// and renders a soft radial glow behind the metadata column.
	AmbientColorEnabled bool `json:"ambient_color_enabled"`
	// IdleScreenTheme selects the clock/weather idle screen: "classic" (monochrome, default)
	// or "colourful" (gradients and text colours driven by current weather, similar to
	// Apple Weather). Rendered in the browser from /api/config; no service restart.
	IdleScreenTheme string `json:"idle_screen_theme"`
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
	// AudDAPIToken is the AudD API token (BYOK, https://docs.audd.io/).
	AudDAPIToken string `json:"audd_api_token"`
	// AcoustIDClientKey is legacy; AcoustID is not a supported provider (see recognition plan doc).
	// Value is passed through to state-manager for config round-trip; state-manager ignores it for recognition.
	// Legacy comment was: application API key from https://acoustid.org/register (BYOK). Previously intended if
	// an in-process AcoustID recognizer were enabled; that path was not pursued.
	AcoustIDClientKey string `json:"acoustid_client_key"`
	// CaptureDurationSecs is how many seconds of audio are sent per recognition
	// attempt (one WAV for the recognizer chain). Default matches
	// defaultConfig().RecognizerCaptureDuration in cmd/oceano-state-manager/config_types.go;
	// saving the web UI regenerates oceano-state-manager.service with
	// --recognizer-capture-duration to this value. Typical range ~5–12s.
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
	// Valid values: "acrcloud_first" (default), "shazam_first", "acrcloud_only", "shazam_only",
	// "audd_first", "audd_only".
	RecognizerChain string `json:"recognizer_chain"`
	// ShazamPythonBin is the path to the Python binary with shazamio installed.
	// Empty string disables Shazam in the recognition chain and continuity monitor.
	ShazamPythonBin string `json:"shazam_python_bin"`

	// --- Gapless / Continuity Tuning (Advanced) ---
	// ContinuityCalibrationGraceSecs is how long to wait before the Shazam
	// continuity monitor starts checking for track changes. During this grace
	// period after recognition, the monitor is in "learning" mode.
	// Lower = faster gapless detection (but more false positives).
	// Typical: 45s for gapless albums, 120s for tolerance to mode switches.
	ContinuityCalibrationGraceSecs int `json:"continuity_calibration_grace_secs"`
	// ContinuityMismatchConfirmWindowSecs is the time window during which repeated
	// sightings of the same track change (from→to pair) are counted toward
	// confirmation. Each new sighting increments a counter; reaching the required
	// threshold triggers re-recognition.
	// Typical: 180s (3 min); longer = slower but less prone to interference.
	ContinuityMismatchConfirmWindowSecs int `json:"continuity_mismatch_confirm_window_secs"`
	// ContinuityRequiredSightingsCalibrated is the number of repeated sightings
	// of the same track change that must be observed (when calibrated) before
	// re-recognition is triggered. Higher = more stable but slower on gapless.
	// Typical: 2 sightings.
	ContinuityRequiredSightingsCalibrated int `json:"continuity_required_sightings_calibrated"`
	// ContinuityRequiredSightingsUncalibrated is the stricter threshold used
	// during the grace period (when the monitor is still learning). This prevents
	// false positives on mode switches and UI noise.
	// Typical: 3 sightings (higher = more confirmation needed).
	ContinuityRequiredSightingsUncalibrated int `json:"continuity_required_sightings_uncalibrated"`
	// EarlyCheckMarginSecs is how close to the end of the track the continuity
	// monitor begins to anticipate a track change. When within this margin of
	// the known duration, the next Shazam poll becomes more sensitive.
	// Typical: 20s before end of track.
	EarlyCheckMarginSecs int `json:"early_check_margin_secs"`
	// DurationGuardBypassWindowSecs is the time window (after a potential false
	// boundary is detected) during which the duration-based suppression guard is
	// armed. If a new boundary is detected within this window, it is suppressed
	// (treated as noise in a quiet passage of the same track).
	// Typical: 20s; raise if you have long quiet intro/outro sections.
	DurationGuardBypassWindowSecs int `json:"duration_guard_bypass_window_secs"`
	// DurationPessimism is the temporal threshold (0.0–1.0) used to split VU
	// boundary behavior by progress: below threshold, boundaries are guarded
	// against false positives; at/above threshold, VU boundaries are ignored and
	// track changes rely on continuity/fallback recognition paths.
	// Typical: 0.75.
	DurationPessimism float64 `json:"duration_pessimism"`
	// BoundaryRestoreMinSeekSecs is the minimum pre-boundary seek position
	// required before the previous track metadata may be restored after a same-track
	// re-confirmation. Higher values reduce false positives after manual needle
	// repositioning. Typical: 60 s for vinyl-safe behavior.
	BoundaryRestoreMinSeekSecs int `json:"boundary_restore_min_seek_secs"`
}

// AdvancedConfig holds paths and internal settings that rarely need
// to change. Exposed for completeness and debugging.
type AdvancedConfig struct {
	VUSocket   string `json:"vu_socket"`
	PCMSocket  string `json:"pcm_socket"`
	SourceFile string `json:"source_file"`
	StateFile  string `json:"state_file"`
	// VUSilenceThreshold is the RMS threshold used by oceano-state-manager
	// VU monitor to classify silence vs active audio for track-boundary detection.
	VUSilenceThreshold float64 `json:"vu_silence_threshold"`
	ArtworkDir         string  `json:"artwork_dir"`
	MetadataPipe       string  `json:"metadata_pipe"`
	// IdleDelaySecs is how long to keep showing the last physical track after
	// audio stops before switching to the idle screen.
	IdleDelaySecs int `json:"idle_delay_secs"`
	// SessionGapThresholdSecs is the max silence gap treated as an inter-track
	// pause. Gaps longer than this start a new recognition session.
	SessionGapThresholdSecs int `json:"session_gap_threshold_secs"`
	// LibraryDB is the path to the SQLite database used to record physical-media
	// plays. Empty string disables library features.
	LibraryDB string `json:"library_db"`
	// CalibrationProfiles stores per-input RMS capture snapshots used by the
	// recognition calibration wizard. Keys are amplifier input IDs (for example
	// "10"=Phono, "20"=CD).
	CalibrationProfiles map[string]CalibrationProfile `json:"calibration_profiles,omitempty"`
	// TelemetryNudges optionally adjusts effective VU silence thresholds and
	// duration pessimism from boundary_events follow-up telemetry (same_track_restored vs matched).
	TelemetryNudges *TelemetryNudgesConfig `json:"r3_telemetry_nudges,omitempty"`
	// AutonomousCalibration when enabled forces telemetry nudges (R3) on at runtime
	// even if r3_telemetry_nudges.enabled is false — bounded adjustments still require
	// enough paired follow-ups (see Listening Metrics calibration readiness).
	AutonomousCalibration *AutonomousCalibrationConfig `json:"autonomous_calibration,omitempty"`
	// RMSPercentileLearning collects stable-silence vs stable-music RMS histograms
	// into the library DB and can autonomously set VU silence enter/exit thresholds.
	RMSPercentileLearning *RMSPercentileLearningConfig `json:"rms_percentile_learning,omitempty"`
	// OceanoSetupAcknowledged is written by the oceano-setup CLI on successful
	// completion. The web UI uses it to suppress the "run oceano-setup first" row
	// in the onboarding checklist.
	OceanoSetupAcknowledged bool `json:"oceano_setup_acknowledged,omitempty"`
}

// RMSPercentileLearningConfig enables autonomous RMS histogram learning (library DB).
type RMSPercentileLearningConfig struct {
	Enabled             *bool `json:"enabled,omitempty"`
	AutonomousApply     bool `json:"autonomous_apply"`
	MinSilenceSamples   int  `json:"min_silence_samples,omitempty"`
	MinMusicSamples     int  `json:"min_music_samples,omitempty"`
	PersistIntervalSecs int  `json:"persist_interval_secs,omitempty"`
	HistogramBins       int  `json:"histogram_bins,omitempty"`
	HistogramMaxRMS     float64 `json:"histogram_max_rms,omitempty"`
}

// AutonomousCalibrationConfig enables hands-off application of R3 telemetry nudges.
type AutonomousCalibrationConfig struct {
	Enabled bool `json:"enabled"`
}

// TelemetryNudgesConfig enables bounded calibration nudges driven by Listening Metrics telemetry (R3).
type TelemetryNudgesConfig struct {
	Enabled bool `json:"enabled"`
	// LookbackDays restricts boundary_events aggregation (default 14).
	LookbackDays int `json:"lookback_days,omitempty"`
	// MinFollowupPairs requires at least this many (same_track_restored + matched) rows before nudging.
	MinFollowupPairs int `json:"min_followup_pairs,omitempty"`
	// BaselineFalsePositiveRatio is the target acceptable rate of same_track_restored among pairs (default 0.10).
	BaselineFalsePositiveRatio float64 `json:"baseline_false_positive_ratio,omitempty"`
	// MaxSilenceThresholdDelta caps the absolute additive change to RMS silence enter/exit (default 0.004).
	MaxSilenceThresholdDelta float64 `json:"max_silence_threshold_delta,omitempty"`
	// MaxDurationPessimismDelta caps the additive adjustment to duration pessimism (default 0.06).
	MaxDurationPessimismDelta float64 `json:"max_duration_pessimism_delta,omitempty"`
	// EarlyTrackProgressP75Threshold: when P75 seek/duration among matched fires is below this, apply an extra silence nudge (default 0.18).
	EarlyTrackProgressP75Threshold float64 `json:"early_track_progress_p75_threshold,omitempty"`
	EarlyTrackExtraSilenceDelta      float64 `json:"early_track_extra_silence_delta,omitempty"`
}

// CalibrationProfile stores OFF/ON RMS snapshots captured by the recognition
// calibration wizard for a specific amplifier input.
type CalibrationProfile struct {
	Off             *CalibrationSample          `json:"off,omitempty"`
	On              *CalibrationSample          `json:"on,omitempty"`
	VinylTransition *VinylTransitionCalibration `json:"vinyl_transition,omitempty"`
}

// CalibrationSample mirrors the RMS summary returned by /api/calibration/vu-sample.
type CalibrationSample struct {
	AvgRMS  float64 `json:"avg_rms"`
	MinRMS  float64 `json:"min_rms"`
	MaxRMS  float64 `json:"max_rms"`
	Samples int     `json:"samples"`
}

// VinylTransitionCalibration stores RMS transition metrics captured during
// the optional vinyl-specific transition wizard step.
type VinylTransitionCalibration struct {
	TailAvgRMS      float64 `json:"tail_avg_rms"`
	GapAvgRMS       float64 `json:"gap_avg_rms"`
	AttackAvgRMS    float64 `json:"attack_avg_rms"`
	GapDurationSecs float64 `json:"gap_duration_secs"`
	SamplesPerSec   float64 `json:"samples_per_sec"`
	Samples         int     `json:"samples"`
}

func defaultConfig() Config {
	return Config{
		AudioInput: AudioInputConfig{
			DeviceMatch:      "",
			SilenceThreshold: 0.025,
			DebounceWindows:  10,
		},
		AudioOutput: AudioOutputConfig{
			AirPlayName: "Oceano",
			DeviceMatch: "",
		},
		Recognition: RecognitionConfig{
			ACRCloudHost:                            "identify-eu-west-1.acrcloud.com",
			CaptureDurationSecs:                     7,
			MaxIntervalSecs:                         300,
			RefreshIntervalSecs:                     120,
			NoMatchBackoffSecs:                      15,
			ConfirmationDelaySecs:                   0,
			ConfirmationCaptureDurationSecs:         4,
			ConfirmationBypassScore:                 95,
			ShazamContinuityIntervalSecs:            8,
			ShazamContinuityCaptureDurationSecs:     4,
			RecognizerChain:                         "acrcloud_first",
			ShazamPythonBin:                         "/opt/shazam-env/bin/python",
			ContinuityCalibrationGraceSecs:          45,
			ContinuityMismatchConfirmWindowSecs:     180,
			ContinuityRequiredSightingsCalibrated:   2,
			ContinuityRequiredSightingsUncalibrated: 3,
			EarlyCheckMarginSecs:                    20,
			DurationGuardBypassWindowSecs:           20,
			DurationPessimism:                       0.75,
			BoundaryRestoreMinSeekSecs:              60,
		},
		Advanced: AdvancedConfig{
			VUSocket:                "/tmp/oceano-vu.sock",
			PCMSocket:               "/tmp/oceano-pcm.sock",
			SourceFile:              "/tmp/oceano-source.json",
			StateFile:               "/tmp/oceano-state.json",
			VUSilenceThreshold:      0.0095,
			ArtworkDir:              "/var/lib/oceano/artwork",
			MetadataPipe:            "/tmp/shairport-sync-metadata",
			IdleDelaySecs:           3,
			SessionGapThresholdSecs: 45,
			LibraryDB:               "/var/lib/oceano/library.db",
		},
		Display: SPIDisplayConfig{
			UIPreset:               "high_contrast_rotate",
			CycleTime:              30,
			StandbyTimeout:         600,
			ExternalArtworkEnabled: true,
		},
		Bluetooth: BluetoothConfig{
			Enabled: false,
			Name:    "", // empty → derived from AirPlay name at install time
		},
		Amplifier: AmplifierConfig{
			InputMode:          "cycle",
			WarmUpSecs:         30,
			StandbyTimeoutMins: 20,
			InputCycling: InputCyclingConfig{
				Enabled:        false,
				Direction:      "prev",
				MaxCycles:      8,
				StepWaitSecs:   3,
				MinSilenceSecs: 120,
			},
			USBReset: USBResetConfig{
				MaxAttempts:       13,
				FirstStepSettleMS: 150,
				StepWaitMS:        2400,
			},
		},
		Weather: WeatherConfig{
			Enabled:     false,
			RefreshMins: 10,
		},
		NowPlaying: NowPlayingConfig{
			AmbientColorEnabled: true,
			IdleScreenTheme:     "classic",
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
	migrateLegacyCDPlayer(&cfg)
	return cfg, nil
}

func migrateLegacyCDPlayer(cfg *Config) {
	if cfg == nil || cfg.LegacyCDPlayer == nil {
		return
	}
	legacy := cfg.LegacyCDPlayer

	name := strings.TrimSpace(strings.TrimSpace(legacy.Maker + " " + legacy.Model))
	if !legacy.Enabled || name == "" {
		cfg.LegacyCDPlayer = nil
		return
	}

	for i := range cfg.Amplifier.ConnectedDevices {
		if strings.EqualFold(strings.TrimSpace(cfg.Amplifier.ConnectedDevices[i].Name), name) {
			if len(cfg.Amplifier.ConnectedDevices[i].InputIDs) == 0 && len(legacy.InputIDs) > 0 {
				cfg.Amplifier.ConnectedDevices[i].InputIDs = legacy.InputIDs
			}
			if len(cfg.Amplifier.ConnectedDevices[i].IRCodes) == 0 && len(legacy.IRCodes) > 0 {
				cfg.Amplifier.ConnectedDevices[i].IRCodes = legacy.IRCodes
			}
			cfg.Amplifier.ConnectedDevices[i].HasRemote = true
			cfg.LegacyCDPlayer = nil
			return
		}
	}

	id := "legacy-cdplayer"
	for n := 2; ; n++ {
		exists := false
		for _, d := range cfg.Amplifier.ConnectedDevices {
			if d.ID == id {
				exists = true
				break
			}
		}
		if !exists {
			break
		}
		id = fmt.Sprintf("legacy-cdplayer-%d", n)
	}

	cfg.Amplifier.ConnectedDevices = append(cfg.Amplifier.ConnectedDevices, ConnectedDeviceConfig{
		ID:        id,
		Name:      name,
		InputIDs:  legacy.InputIDs,
		HasRemote: true,
		IRCodes:   legacy.IRCodes,
	})
	cfg.LegacyCDPlayer = nil
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
func managerArgs(cfg Config, configPath string) []string {
	rec := cfg.Recognition
	adv := cfg.Advanced
	args := []string{
		"--metadata-pipe", adv.MetadataPipe,
		"--source-file", adv.SourceFile,
		"--output", adv.StateFile,
		"--artwork-dir", adv.ArtworkDir,
		"--vu-socket", adv.VUSocket,
		"--vu-silence-threshold", fmt.Sprintf("%.4f", adv.VUSilenceThreshold),
		"--calibration-config", configPath,
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
		"--continuity-calibration-grace", fmt.Sprintf("%ds", rec.ContinuityCalibrationGraceSecs),
		"--continuity-mismatch-confirm-window", fmt.Sprintf("%ds", rec.ContinuityMismatchConfirmWindowSecs),
		"--continuity-required-sightings-calibrated", fmt.Sprintf("%d", rec.ContinuityRequiredSightingsCalibrated),
		"--continuity-required-sightings-uncalibrated", fmt.Sprintf("%d", rec.ContinuityRequiredSightingsUncalibrated),
		"--early-check-margin", fmt.Sprintf("%ds", rec.EarlyCheckMarginSecs),
		"--duration-guard-bypass-window", fmt.Sprintf("%ds", rec.DurationGuardBypassWindowSecs),
		"--duration-pessimism", fmt.Sprintf("%.2f", rec.DurationPessimism),
		"--boundary-restore-min-seek", fmt.Sprintf("%ds", rec.BoundaryRestoreMinSeekSecs),
		"--recognizer-chain", rec.RecognizerChain,
	}
	if adv.IdleDelaySecs > 0 {
		args = append(args, "--idle-delay", fmt.Sprintf("%ds", adv.IdleDelaySecs))
	}
	if adv.SessionGapThresholdSecs > 0 {
		args = append(args, "--session-gap-threshold", fmt.Sprintf("%ds", adv.SessionGapThresholdSecs))
	}
	if adv.LibraryDB != "" {
		args = append(args, "--library-db", adv.LibraryDB)
	}
	if rec.ACRCloudHost != "" {
		args = append(args,
			"--acrcloud-host", rec.ACRCloudHost,
			"--acrcloud-access-key", rec.ACRCloudAccessKey,
			"--acrcloud-secret-key", rec.ACRCloudSecretKey,
		)
	}
	if strings.TrimSpace(rec.AcoustIDClientKey) != "" {
		args = append(args, "--acoustid-client-key", strings.TrimSpace(rec.AcoustIDClientKey))
	}
	if strings.TrimSpace(rec.AudDAPIToken) != "" {
		args = append(args, "--audd-api-token", strings.TrimSpace(rec.AudDAPIToken))
	}
	if rec.ShazamPythonBin != "" {
		args = append(args, "--shazam-python", rec.ShazamPythonBin)
	}
	// Boolean flags and --verbose must be last (no paired value).
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
