package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

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
	// Calibrated value for Magnat MR 780 at capture volume 3: 0.025.
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
	// silence gap (track boundary) is detected.
	MaxIntervalSecs int `json:"max_interval_secs"`
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
			DeviceMatch: "MR 780",
		},
		Recognition: RecognitionConfig{
			ACRCloudHost:        "identify-eu-west-1.acrcloud.com",
			CaptureDurationSecs: 10,
			MaxIntervalSecs:     300,
		},
		Advanced: AdvancedConfig{
			VUSocket:     "/tmp/oceano-vu.sock",
			PCMSocket:    "/tmp/oceano-pcm.sock",
			SourceFile:   "/tmp/oceano-source.json",
			StateFile:    "/tmp/oceano-state.json",
			ArtworkDir:   "/tmp",
			MetadataPipe: "/tmp/shairport-sync-metadata",
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
