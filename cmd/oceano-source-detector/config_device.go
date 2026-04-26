package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

type Source string

const (
	SourceNone     Source = "None"
	SourcePhysical Source = "Physical"
)

type State struct {
	Source    Source `json:"source"`
	UpdatedAt string `json:"updated_at"`
}

// VUFrame is published to the VU socket every audio window (~186 ms at 44.1 kHz).
// Each value is a float32 RMS level in [0.0, 1.0], little-endian.
// Total: 8 bytes per frame.
type VUFrame struct {
	Left  float32
	Right float32
}

type Config struct {
	AlsaDevice       string
	DeviceMatch      string  // substring to match in /proc/asound/cards (e.g. "Device", "UAC2"); empty = use AlsaDevice only
	SampleRate       int
	BufferSize       int
	SilenceThreshold float64 // upper cap on adaptive RMS threshold; 0 = uncapped
	StdDevThreshold  float64 // manual StdDev override; 0 = use adaptive learner
	DebounceWindows  int
	OutputFile       string
	VUSocket         string
	PCMSocket        string // Unix socket for raw PCM relay; consumers read S16_LE stereo at SampleRate Hz
	CalibrationFile  string // path to persisted noise-floor JSON
	FormatHintFile   string // written by state-manager when format (Vinyl|CD) is identified
	Verbose          bool
}

func defaultConfig() Config {
	return Config{
		AlsaDevice:       "",
		DeviceMatch:      "",
		SampleRate:       44100,
		BufferSize:       2048,
		SilenceThreshold: 0.025,
		DebounceWindows:  10,
		OutputFile:       "/tmp/oceano-source.json",
		VUSocket:         "/tmp/oceano-vu.sock",
		PCMSocket:        "/tmp/oceano-pcm.sock",
		CalibrationFile:  "/var/lib/oceano/noise-floor.json",
		FormatHintFile:   "/tmp/oceano-format.json",
		Verbose:          false,
	}
}

// findAlsaCaptureDevice searches /proc/asound/cards for a card whose name
// contains match (case-insensitive). Returns a plughw:N,0 string or "".
func findAlsaCaptureDevice(match string) string {
	data, err := os.ReadFile("/proc/asound/cards")
	if err != nil {
		return ""
	}
	lower := strings.ToLower(match)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(strings.ToLower(line), lower) {
			// Lines alternate: " N [ShortName]: ..." and "            LongName"
			// Card number is the leading integer.
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			var cardNum int
			if _, err := fmt.Sscanf(fields[0], "%d", &cardNum); err != nil {
				continue
			}
			return fmt.Sprintf("plughw:%d,0", cardNum)
		}
	}
	return ""
}

// resolveDevice returns the ALSA device string to use, re-detecting each call
// when DeviceMatch is set. Falls back to AlsaDevice if no match is found.
func resolveDevice(cfg Config) (string, error) {
	if cfg.DeviceMatch != "" {
		dev := findAlsaCaptureDevice(cfg.DeviceMatch)
		if dev != "" {
			return dev, nil
		}
		if cfg.AlsaDevice != "" {
			log.Printf("device-match %q not found — falling back to --device %s", cfg.DeviceMatch, cfg.AlsaDevice)
			return cfg.AlsaDevice, nil
		}
		return "", fmt.Errorf("capture device matching %q not found in /proc/asound/cards", cfg.DeviceMatch)
	}
	if cfg.AlsaDevice != "" {
		return cfg.AlsaDevice, nil
	}
	return "", fmt.Errorf("no capture device configured (set --device-match or --device)")
}
