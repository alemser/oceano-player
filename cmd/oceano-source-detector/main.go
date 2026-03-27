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
	AlsaDevice       string
	SampleRate       int
	BufferSize       int
	SilenceThreshold float64 // RMS below this = silence / nothing playing
	VinylThreshold   float64 // Low-freq energy ratio above this = vinyl
	DebounceWindows  int
	OutputFile       string
	Verbose          bool
}

func defaultConfig() Config {
	return Config{
		AlsaDevice:       "plughw:CARD=Microphone,DEV=0",
		SampleRate:       44100,
		BufferSize:       8192, // power of 2, required for Cooley-Tukey FFT
		SilenceThreshold: 0.0050,
		VinylThreshold:   0.08,
		DebounceWindows:  7,
		OutputFile:       "/tmp/oceano-source.json",
		Verbose:          false,
	}
}

func main() {
	cfg := defaultConfig()

	flag.StringVar(&cfg.AlsaDevice, "device", cfg.AlsaDevice, "ALSA capture device")
	flag.StringVar(&cfg.OutputFile, "output", cfg.OutputFile, "Output JSON file path")
	flag.Float64Var(&cfg.SilenceThreshold, "silence-threshold", cfg.SilenceThreshold, "RMS threshold for silence")
	flag.Float64Var(&cfg.VinylThreshold, "vinyl-threshold", cfg.VinylThreshold, "Low-freq energy ratio threshold for vinyl")
	flag.IntVar(&cfg.DebounceWindows, "debounce", cfg.DebounceWindows, "Consecutive windows before committing a state change")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "Log RMS and low-freq ratio on every window (useful for calibration)")
	flag.Parse()

	log.Printf("oceano-source-detector starting")
	log.Printf("  device:            %s", cfg.AlsaDevice)
	log.Printf("  output:            %s", cfg.OutputFile)
	log.Printf("  silence threshold: %.6f", cfg.SilenceThreshold)
	log.Printf("  vinyl threshold:   %.4f", cfg.VinylThreshold)
	log.Printf("  debounce windows:  %d", cfg.DebounceWindows)
	log.Printf("  verbose:           %v", cfg.Verbose)

	if !isPowerOfTwo(cfg.BufferSize) {
		log.Fatalf("buffer-size must be a power of 2 (got %d)", cfg.BufferSize)
	}

	ctx, stop := signal.NotifyContext(backgroundCtx{}, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("detector error: %v", err)
	}
}

func run(ctx interface{ Done() <-chan struct{} }, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
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
			log.Printf("shutting down, writing None")
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

		// classify returns the raw detection plus the signal metrics,
		// so we never need to recompute FFT in the hysteresis step.
		detected, rms, lowFreqRatio := classify(samples, cfg)
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

		detected, rms, ratio := classify(samples, cfg)

		// Histerese refinada: só permite troca se o novo estado for estável e bem definido
		// Exemplo: só troca de Vinyl para CD se ratio < threshold e rms baixo
		if current == SourceVinyl && detected == SourceCD {
			if rms > 0.02 && ratio > 0.01 {
				log.Printf("[hysteresis] Mantendo Vinyl: rms=%.4f ratio=%.4f", rms, ratio)
				detected = SourceVinyl
			}
		}
		if current == SourceCD && detected == SourceVinyl {
			if ratio < 0.20 {
				log.Printf("[hysteresis] Mantendo CD: rms=%.4f ratio=%.4f", rms, ratio)
				detected = SourceCD
			}
		}

		// Debounce: só troca se houver N janelas consecutivas
		if detected == candidate {
			candidateCount++
		} else {
			candidate = detected
			candidateCount = 1
		}

		log.Printf("detected=%s candidate=%s count=%d current=%s rms=%.4f ratio=%.4f", detected, candidate, candidateCount, current, rms, ratio)

		if candidateCount >= cfg.DebounceWindows && detected != current {
			log.Printf("source changed: %s → %s [rms=%.4f ratio=%.4f]", current, detected, rms, ratio)
			current = detected
		}
		if current != SourceNone {
			if err := writeState(cfg.OutputFile, current); err != nil {
				log.Printf("write state error: %v", err)
			}
		}
	}
	// which avoids leaving zombie processes on every window.
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

// classify analyses a window of samples and returns Source, RMS, and low-freq ratio.
// Returning all three avoids recomputing FFT in the hysteresis step.
// classify analyses a window of samples and returns a Source.
// Now uses extra heuristics: background hiss and click/pop detection for improved vinyl/CD distinction.
func classify(samples []float64, cfg Config) (Source, float64, float64) {
       rms := computeRMS(samples)

       // Heuristic 1: Silence (CD) vs. background hiss (vinyl)
       if rms < cfg.SilenceThreshold {
	       hiss := estimateHiss(samples)
	       if hiss > 0.002 { // empirical value, tune as needed
		       return SourceVinyl, rms, 0
	       }
	       return SourceNone, rms, 0
       }

       spectrum := fft(samples)
       ratio := lowFrequencyRatio(spectrum, cfg.SampleRate, cfg.BufferSize)

       // Heuristic 2: Clicks/pops typical of vinyl
       if detectClicks(samples) {
	       return SourceVinyl, rms, ratio
       }

       if ratio > cfg.VinylThreshold {
	       return SourceVinyl, rms, ratio
       }
       return SourceCD, rms, ratio
}

// estimateHiss computes the standard deviation of sample-to-sample differences (proxy for background hiss/noise).
func estimateHiss(samples []float64) float64 {
       if len(samples) < 2 {
	       return 0
       }
       var sum, sumSq float64
       for i := 1; i < len(samples); i++ {
	       diff := samples[i] - samples[i-1]
	       sum += diff
	       sumSq += diff * diff
       }
       n := float64(len(samples) - 1)
       mean := sum / n
       variance := (sumSq / n) - (mean * mean)
       if variance < 0 {
	       return 0
       }
       return math.Sqrt(variance)
}

// detectClicks looks for fast transients (sample-to-sample spikes) typical of vinyl clicks/pops.
func detectClicks(samples []float64) bool {
       threshold := 0.15 // empirical value, tune as needed
       count := 0
       for i := 1; i < len(samples); i++ {
	       if math.Abs(samples[i]-samples[i-1]) > threshold {
		       count++
	       }
       }
       // If more than 3 spikes in a window, assume vinyl
       return count > 3
}

// computeRMS returns the root mean square of the samples.
func computeRMS(samples []float64) float64 {
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// lowFrequencyRatio returns the ratio of energy in the 20–80 Hz band to total energy.
// Vinyl has a characteristic rumble in this range due to motor and stylus friction.
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

// fft computes the DFT using the Cooley-Tukey radix-2 algorithm (O(n log n)).
// BufferSize must be a power of 2.
func fft(samples []float64) []complex128 {
	n := len(samples)
	out := make([]complex128, n)
	for i, s := range samples {
		out[i] = complex(s, 0)
	}
	cooleyTukey(out)
	return out
}

func cooleyTukey(a []complex128) {
	n := len(a)
	if n <= 1 {
		return
	}

	// Bit-reversal permutation.
	bits := int(math.Log2(float64(n)))
	for i := 0; i < n; i++ {
		j := bitReverse(i, bits)
		if j > i {
			a[i], a[j] = a[j], a[i]
		}
	}

	// Butterfly stages.
	for length := 2; length <= n; length <<= 1 {
		half := length / 2
		wBase := complex(math.Cos(-2*math.Pi/float64(length)), math.Sin(-2*math.Pi/float64(length)))
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			for j := 0; j < half; j++ {
				u := a[i+j]
				v := a[i+j+half] * w
				a[i+j] = u + v
				a[i+j+half] = u - v
				w *= wBase
			}
		}
	}
}

func bitReverse(x, bits int) int {
	result := 0
	for i := 0; i < bits; i++ {
		result = (result << 1) | (x & 1)
		x >>= 1
	}
	return result
}

// isPowerOfTwo returns true when n is a positive power of 2.
func isPowerOfTwo(n int) bool {
	return n > 0 && (n&(n-1)) == 0
}

// writeState serialises the current source to the output JSON file atomically.
func writeState(path string, src Source) error {
	state := State{
		Source:    src,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
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

// backgroundCtx is a minimal context.Background() substitute.
type backgroundCtx struct{}

func (backgroundCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (backgroundCtx) Done() <-chan struct{}         { return nil }
func (backgroundCtx) Err() error                   { return nil }
func (backgroundCtx) Value(any) any                { return nil }