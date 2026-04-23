package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	AlsaDevice          string
	DeviceMatch         string // substring to match in /proc/asound/cards (e.g. "USB Microphone")
	SampleRate          int
	BufferSize          int
	SilenceThreshold    float64
	StddevThreshold     float64
	DebounceWindows     int
	OutputFile          string
	VUSocket            string
	PCMSocket           string // Unix socket for raw PCM relay; consumers read S16_LE stereo at SampleRate Hz
	CalibrationFile     string
	CalibrationDuration int // seconds to measure noise floor (0 = disabled, load from file)
	Verbose             bool
}

func defaultConfig() Config {
	return Config{
		AlsaDevice:          "",
		DeviceMatch:         "USB Microphone",
		SampleRate:          44100,
		BufferSize:          2048,
		SilenceThreshold:    0.008,
		StddevThreshold:     0.0, // 0 = auto-calculate from noise floor
		DebounceWindows:     10,
		OutputFile:          "/tmp/oceano-source.json",
		VUSocket:            "/tmp/oceano-vu.sock",
		PCMSocket:           "/tmp/oceano-pcm.sock",
		CalibrationFile:     "/var/lib/oceano/noise-floor.json",
		CalibrationDuration: 10,
		Verbose:             false,
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

type NoiseFloor struct {
	MeasuredAt string  `json:"measured_at"`
	RMS        float64 `json:"rms"`
	Stddev     float64 `json:"stddev"`
	Samples    int     `json:"samples"`
}

func loadNoiseFloor(path string) (NoiseFloor, bool) {
	if path == "" {
		return NoiseFloor{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return NoiseFloor{}, false
	}
	var nf NoiseFloor
	if err := json.Unmarshal(data, &nf); err != nil {
		return NoiseFloor{}, false
	}
	return nf, true
}

func saveNoiseFloor(path string, nf NoiseFloor) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	nf.MeasuredAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(nf, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func computeRMSStats(values []float64) (mean, stddev float64) {
	if len(values) == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean = sum / float64(len(values))
	var sumSq float64
	for _, v := range values {
		d := v - mean
		sumSq += d * d
	}
	stddev = math.Sqrt(sumSq / float64(len(values)))
	return mean, stddev
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	if len(sorted)%2 == 0 {
		return (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	return sorted[len(sorted)/2]
}
