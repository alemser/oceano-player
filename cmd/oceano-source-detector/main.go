package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/cmplx"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// Source represents the detected audio source.
type Source string

const (
	SourceNone  Source = "None"
	SourceCD    Source = "CD"
	SourceVinyl Source = "Vinyl"
)

// State is written to the output file.
type State struct {
	Source    Source `json:"source"`
	UpdatedAt string `json:"updated_at"`
}

// Config holds all tunable parameters.
type Config struct {
	// ALSA capture device (e.g. "plughw:CARD=M780,DEV=0")
	AlsaDevice string

	// SampleRate and buffer size for capture
	SampleRate int
	BufferSize int

	// Thresholds (tune after calibration)
	SilenceThreshold float64 // RMS below this = silence / nothing playing
	VinylThreshold   float64 // Low-freq energy ratio above this = vinyl

	// Debounce: how many consecutive agreeing windows before we commit
	DebounceWindows int

	// Output file path
	OutputFile string
}

func defaultConfig() Config {
	return Config{
		AlsaDevice:      "plughw:CARD=Microphone,DEV=0",
		SampleRate:      44100,
		BufferSize:      16384,
		SilenceThreshold: 0.0050,
		VinylThreshold:  0.08, // ratio of low-freq energy to total energy
		DebounceWindows: 7,
		OutputFile:      "/tmp/oceano-source.json",
	}
}

func main() {
	cfg := defaultConfig()

	flag.StringVar(&cfg.AlsaDevice, "device", cfg.AlsaDevice, "ALSA capture device")
	flag.StringVar(&cfg.OutputFile, "output", cfg.OutputFile, "Output JSON file path")
	flag.Float64Var(&cfg.SilenceThreshold, "silence-threshold", cfg.SilenceThreshold, "RMS threshold for silence")
	flag.Float64Var(&cfg.VinylThreshold, "vinyl-threshold", cfg.VinylThreshold, "Low-freq energy ratio threshold for vinyl")
	flag.IntVar(&cfg.DebounceWindows, "debounce", cfg.DebounceWindows, "Consecutive windows before committing a state change")
	flag.Parse()

	log.Printf("oceano-source-detector starting")
	log.Printf("  device:            %s", cfg.AlsaDevice)
	log.Printf("  output:            %s", cfg.OutputFile)
	log.Printf("  silence threshold: %.6f", cfg.SilenceThreshold)
	log.Printf("  vinyl threshold:   %.4f", cfg.VinylThreshold)
	log.Printf("  debounce windows:  %d", cfg.DebounceWindows)

	ctx, stop := signal.NotifyContext(backgroundCtx{}, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("detector error: %v", err)
	}
}

func run(ctx interface{ Done() <-chan struct{} }, cfg Config) error {
	// Ensure output directory exists.
	if err := os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// Write initial state.
	if err := writeState(cfg.OutputFile, SourceNone); err != nil {
		return err
	}

	current := SourceNone
	candidate := SourceNone
	candidateCount := 0

	log.Printf("listening on %s ...", cfg.AlsaDevice)

	for {
		select {
		case <-ctx.Done():
			log.Printf("shutting down, writing none")
			_ = writeState(cfg.OutputFile, SourceNone)
			return nil
		default:
		}

		samples, err := captureWindow(cfg)
		if err != nil {
			log.Printf("capture error: %v — retrying in 2s", err)
			sleep(ctx, 2*time.Second)
			continue
		}

		detected, rms := classify(samples, cfg)

		// REFINED HISTERESE
		if current == SourceVinyl && detected == SourceCD {
			spectrum := fft(samples)
			ratio := lowFrequencyRatio(spectrum, cfg.SampleRate, cfg.BufferSize)

            if rms > 0.02 && ratio > 0.01 {
                detected = SourceVinyl
            }
        }

		if current == SourceCD && detected == SourceVinyl {
			spectrum := fft(samples)
            ratio := lowFrequencyRatio(spectrum, cfg.SampleRate, cfg.BufferSize)
            if ratio < 0.20 {
                detected = SourceCD
            }
        }			
		// debug to understand what pi is listening in real time
		//rms := computeRMS(samples)
		spectrum := fft(samples)
		ratio := lowFrequencyRatio(spectrum, cfg.SampleRate, cfg.BufferSize)
		log.Printf("DEBUG: RMS: %.6f | Ratio: %.4f | Detected: %s", rms, ratio, detected)

		// Debounce: only commit a new state after N consecutive agreeing windows.
		if detected == candidate {
			candidateCount++
		} else {
			candidate = detected
			candidateCount = 1
		}

		if candidateCount >= cfg.DebounceWindows && detected != current {
			log.Printf("source changed: %s → %s", current, detected)
			current = detected
			if err := writeState(cfg.OutputFile, current); err != nil {
				log.Printf("write state error: %v", err)
			}
		}
	}
}

// captureWindow reads one buffer of PCM audio from ALSA via arecord.
// arecord is available everywhere shairport-sync runs; no extra deps needed.
func captureWindow(cfg Config) ([]float64, error) {
	// arecord -D <device> -f S16_LE -r <rate> -c 2 --duration=0 -t raw
	// We capture exactly BufferSize stereo frames = BufferSize*2 samples * 2 bytes.
	frames := cfg.BufferSize
	bytesNeeded := frames * 2 * 2 // stereo, 16-bit

	cmd := exec.Command("arecord",
		"-D", cfg.AlsaDevice,
		"-f", "S16_LE",
		"-r", fmt.Sprintf("%d", cfg.SampleRate),
		"-c", "2",
		"-t", "raw",
		"--duration=0",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("arecord start: %w", err)
	}

	raw := make([]byte, bytesNeeded)
	_, err = io.ReadFull(stdout, raw)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	if err != nil {
		return nil, fmt.Errorf("read pcm: %w", err)
	}

	// Convert interleaved S16_LE stereo to mono float64 samples.
	samples := make([]float64, frames)
	for i := 0; i < frames; i++ {
		left := int16(binary.LittleEndian.Uint16(raw[i*4:]))
		right := int16(binary.LittleEndian.Uint16(raw[i*4+2:]))
		samples[i] = float64(left+right) / 2.0 / 32768.0
	}

	return samples, nil
}

// classify analyses a window of samples and returns a Source.
func classify(samples []float64, cfg Config) (Source, float64) {
	rms := computeRMS(samples)

	// Nothing playing or amp is off.
	if rms < cfg.SilenceThreshold {
		return SourceNone, rms
	}

	// Compute FFT and check low-frequency energy ratio.
	spectrum := fft(samples)
	lowFreqRatio := lowFrequencyRatio(spectrum, cfg.SampleRate, cfg.BufferSize)

	if lowFreqRatio > cfg.VinylThreshold {
		return SourceVinyl, rms
	}
	return SourceCD, rms
}

// computeRMS returns the root mean square of the samples.
func computeRMS(samples []float64) float64 {
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// lowFrequencyRatio returns the ratio of energy in the 20–80 Hz band
// to total energy. Vinyl has a characteristic rumble in this range.
func lowFrequencyRatio(spectrum []complex128, sampleRate, bufferSize int) float64 {
	binHz := float64(sampleRate) / float64(bufferSize)
	lowMin := int(20.0 / binHz)
	lowMax := int(80.0 / binHz)

	var lowEnergy, totalEnergy float64
	for i, c := range spectrum[:bufferSize/2] {
		mag := cmplx.Abs(c)
		energy := mag * mag
		totalEnergy += energy
		if i >= lowMin && i <= lowMax {
			lowEnergy += energy
		}
	}

	if totalEnergy == 0 {
		return 0
	}
	return lowEnergy / totalEnergy
}

// fft computes a basic DFT. For production you'd use go-dsp or similar,
// but this avoids external dependencies and is fine for BufferSize <= 8192.
func fft(samples []float64) []complex128 {
	n := len(samples)
	out := make([]complex128, n)
	for k := 0; k < n/2; k++ {
		var sum complex128
		for t, s := range samples {
			angle := -2 * math.Pi * float64(k) * float64(t) / float64(n)
			sum += complex(s*math.Cos(angle), s*math.Sin(angle))
		}
		out[k] = sum
	}
	return out
}

// writeState serialises the current source to the output JSON file.
func writeState(path string, src Source) error {
	state := State{
		Source:    src,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	// Write atomically via a temp file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sleep waits for d or until ctx is done.
func sleep(ctx interface{ Done() <-chan struct{} }, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// backgroundCtx is a minimal context.Background() substitute to avoid
// importing context just for the signal.NotifyContext call.
type backgroundCtx struct{}

func (backgroundCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (backgroundCtx) Done() <-chan struct{}         { return nil }
func (backgroundCtx) Err() error                   { return nil }
func (backgroundCtx) Value(any) any                { return nil }