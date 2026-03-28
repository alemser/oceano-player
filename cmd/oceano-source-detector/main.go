package main

import (
	"context"
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

type Source string

const (
	SourceNone  Source = "None"
	SourceCD    Source = "CD"
	SourceVinyl Source = "Vinyl"
)

type State struct {
	Source    Source `json:"source"`
	UpdatedAt string `json:"updated_at"`
}

type Config struct {
	AlsaDevice         string
	SampleRate         int
	BufferSize         int
	SilenceThreshold   float64
	QuietThreshold     float64
	MinVinylRMS        float64
	VinylRatioThreshold float64
	DebounceWindows    int
	OutputFile         string
	Verbose            bool
}

func defaultConfig() Config {
	return Config{
		AlsaDevice:          "plughw:2,0",
		SampleRate:          44100,
		BufferSize:          8192,
		SilenceThreshold:    0.008,
		QuietThreshold:      0.040,
		// Dual-gate: both must be true to detect Vinyl.
		// MinVinylRMS: any signal above silence is enough (tune up if noisy).
		// VinylRatioThreshold: low-freq (15-140 Hz) energy as fraction of total.
		//   Vinyl ~0.10-0.30, CD ~0.02-0.08 — start at 0.08, tune from calibration.
		MinVinylRMS:         0.010,
		VinylRatioThreshold: 0.08,
		DebounceWindows:     10,
		OutputFile:          "/tmp/oceano-source.json",
		Verbose:             false,
	}
}

func main() {
	cfg := defaultConfig()

	flag.StringVar(&cfg.AlsaDevice, "device", cfg.AlsaDevice, "ALSA capture device")
	flag.StringVar(&cfg.OutputFile, "output", cfg.OutputFile, "Output JSON file path")
	flag.Float64Var(&cfg.SilenceThreshold, "silence-threshold", cfg.SilenceThreshold, "RMS below this = silence")
	flag.Float64Var(&cfg.QuietThreshold, "quiet-threshold", cfg.QuietThreshold, "RMS below this = quiet passage")
	flag.Float64Var(&cfg.MinVinylRMS, "min-vinyl-rms", cfg.MinVinylRMS, "Minimum RMS required to classify as Vinyl")
	flag.Float64Var(&cfg.VinylRatioThreshold, "vinyl-ratio-threshold", cfg.VinylRatioThreshold, "Low-freq energy ratio (15-140 Hz / total) above this = Vinyl")
	flag.IntVar(&cfg.DebounceWindows, "debounce", cfg.DebounceWindows, "Consecutive windows to confirm")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "Detailed logging")
	flag.Parse()

	log.Printf("oceano-source-detector starting")

	if !isPowerOfTwo(cfg.BufferSize) {
		log.Fatalf("buffer-size must be a power of 2")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("detector error: %v", err)
	}
}

func run(ctx context.Context, cfg Config) error {
	_ = os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755)
	_ = writeState(cfg.OutputFile, SourceNone)

	for {
		select {
		case <-ctx.Done():
			_ = writeState(cfg.OutputFile, SourceNone)
			return nil
		default:
			if err := runStream(ctx, cfg); err != nil {
				log.Printf("stream error: %v — restarting in 2s", err)
				time.Sleep(2 * time.Second)
			}
		}
	}
}

func runStream(ctx context.Context, cfg Config) error {
	cmd := exec.Command("arecord", "-D", cfg.AlsaDevice, "-f", "S16_LE", "-r", fmt.Sprintf("%d", cfg.SampleRate), "-c", "2", "-t", "raw", "--duration=0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	current := SourceNone
	candidate := SourceNone
	candidateCount := 0
	bytesPerWindow := cfg.BufferSize * 4
	raw := make([]byte, bytesPerWindow)
	samples := make([]float64, cfg.BufferSize)

	// Precompute Hann window coefficients once.
	hann := make([]float64, cfg.BufferSize)
	for i := range hann {
		hann[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(cfg.BufferSize-1)))
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if _, err := io.ReadFull(stdout, raw); err != nil {
			return err
		}

		for i := 0; i < cfg.BufferSize; i++ {
			left := int16(binary.LittleEndian.Uint16(raw[i*4:]))
			right := int16(binary.LittleEndian.Uint16(raw[i*4+2:]))
			samples[i] = float64(left+right) / 2.0 / 32768.0
		}

		rms := computeRMS(samples)

		if rms < cfg.SilenceThreshold {
			if current != SourceNone {
				log.Printf("source changed: %s → None", current)
				current, candidate, candidateCount = SourceNone, SourceNone, 0
				_ = writeState(cfg.OutputFile, current)
			}
			continue
		}

		// Hold confirmed source during active music to prevent misclassification.
		if rms >= cfg.QuietThreshold && current != SourceNone && candidateCount >= cfg.DebounceWindows {
			if cfg.Verbose {
				log.Printf("active music (rms=%.5f) - holding: %s", rms, current)
			}
			continue
		}

		// Apply Hann window before FFT to reduce spectral leakage.
		windowed := make([]float64, cfg.BufferSize)
		for i, s := range samples {
			windowed[i] = s * hann[i]
		}

		spectrum := fft(windowed)
		bassRatio := computeLowFreqRatio(spectrum, cfg.SampleRate, cfg.BufferSize)
		hfRatio := computeHFRatio(spectrum, cfg.SampleRate, cfg.BufferSize)

		// Dual gate: require meaningful signal AND high low-freq ratio for Vinyl.
		var detected Source
		if rms >= cfg.MinVinylRMS && bassRatio >= cfg.VinylRatioThreshold {
			detected = SourceVinyl
		} else {
			detected = SourceCD
		}

		if cfg.Verbose {
			log.Printf("analyzing: rms=%.5f bass_ratio=%.4f hf_ratio=%.4f det=%s cand=%s(%d) curr=%s",
				rms, bassRatio, hfRatio, detected, candidate, candidateCount, current)
		}

		if detected == candidate {
			candidateCount++
		} else {
			candidate, candidateCount = detected, 1
		}

		if candidateCount >= cfg.DebounceWindows && candidate != current {
			log.Printf("SOURCE DETECTED: %s → %s (rms=%.5f bass_ratio=%.4f hf_ratio=%.4f)",
				current, candidate, rms, bassRatio, hfRatio)
			current = candidate
			_ = writeState(cfg.OutputFile, current)
		}
	}
}

func computeRMS(samples []float64) float64 {
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// computeLowFreqRatio returns the fraction of spectral energy in the 15–140 Hz band.
// This is volume-independent: vinyl rumble stays high relative to total energy
// regardless of how loud the music is.
func computeLowFreqRatio(spectrum []complex128, sampleRate, bufferSize int) float64 {
	binHz := float64(sampleRate) / float64(bufferSize)
	lowMin := int(15.0 / binHz)
	lowMax := int(140.0 / binHz)

	var lowEnergy, totalEnergy float64
	nyquist := bufferSize / 2
	for i := 0; i <= nyquist; i++ {
		mag := cmplx.Abs(spectrum[i])
		e := mag * mag
		totalEnergy += e
		if i >= lowMin && i <= lowMax {
			lowEnergy += e
		}
	}
	if totalEnergy == 0 {
		return 0
	}
	return lowEnergy / totalEnergy
}

// computeHFRatio returns the fraction of spectral energy above 8 kHz.
// Vinyl surface noise creates broadband HF hiss that CD lacks.
// Logged in verbose mode as a diagnostic aid for future threshold tuning.
func computeHFRatio(spectrum []complex128, sampleRate, bufferSize int) float64 {
	binHz := float64(sampleRate) / float64(bufferSize)
	hfMin := int(8000.0 / binHz)
	nyquist := bufferSize / 2

	var hfEnergy, totalEnergy float64
	for i := 0; i <= nyquist; i++ {
		mag := cmplx.Abs(spectrum[i])
		e := mag * mag
		totalEnergy += e
		if i >= hfMin {
			hfEnergy += e
		}
	}
	if totalEnergy == 0 {
		return 0
	}
	return hfEnergy / totalEnergy
}

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
	bits := int(math.Log2(float64(n)))
	for i := 0; i < n; i++ {
		j := bitReverse(i, bits)
		if j > i {
			a[i], a[j] = a[j], a[i]
		}
	}
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

func isPowerOfTwo(n int) bool {
	return n > 0 && (n&(n-1)) == 0
}

func writeState(path string, src Source) error {
	state := State{
		Source:    src,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	b, _ := json.MarshalIndent(state, "", "  ")
	tmp := path + ".tmp"
	_ = os.WriteFile(tmp, b, 0o644)
	return os.Rename(tmp, path)
}
