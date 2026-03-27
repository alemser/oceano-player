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
	MinVinylRMS      float64 // RMS must also exceed this to classify as Vinyl (guards against ambient noise)
	DebounceWindows  int
	OutputFile       string
	Verbose          bool
}

func defaultConfig() Config {
	return Config{
		AlsaDevice:       "plughw:CARD=Microphone,DEV=0",
		SampleRate:       44100,
		BufferSize:       8192, // power of 2, required for Cooley-Tukey FFT
		SilenceThreshold: 0.005,
		VinylThreshold:   0.12,
		MinVinylRMS:      0.08, // below this, a high ratio is ambient noise, not vinyl
		DebounceWindows:  5,
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
	flag.Float64Var(&cfg.MinVinylRMS, "min-vinyl-rms", cfg.MinVinylRMS, "Minimum RMS to trust a vinyl classification (rejects ambient noise)")
	flag.IntVar(&cfg.DebounceWindows, "debounce", cfg.DebounceWindows, "Consecutive windows before committing a state change")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "Log RMS and low-freq ratio on every window (useful for calibration)")
	flag.Parse()

	log.Printf("oceano-source-detector starting")
	log.Printf("  device:            %s", cfg.AlsaDevice)
	log.Printf("  output:            %s", cfg.OutputFile)
	log.Printf("  silence threshold: %.6f", cfg.SilenceThreshold)
	log.Printf("  vinyl threshold:   %.4f", cfg.VinylThreshold)
	log.Printf("  min vinyl rms:     %.4f", cfg.MinVinylRMS)
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

	log.Printf("listening on %s ...", cfg.AlsaDevice)

	// Retry loop: if arecord dies (e.g. device unplugged), restart it.
	for {
		select {
		case <-ctx.Done():
			log.Printf("shutting down, writing None")
			_ = writeState(cfg.OutputFile, SourceNone)
			return nil
		default:
		}

		if err := runStream(ctx, cfg); err != nil {
			log.Printf("stream error: %v — restarting in 2s", err)
			sleep(ctx, 2*time.Second)
		}
	}
}

// runStream starts a single long-running arecord process and reads windows
// from its stdout continuously. This avoids the per-window fork/exec overhead
// that was causing ~8s latency per classification window.
func runStream(ctx interface{ Done() <-chan struct{} }, cfg Config) error {
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
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("arecord start: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	log.Printf("arecord started (pid %d)", cmd.Process.Pid)

	current := SourceNone
	hysteresisMargin := cfg.VinylThreshold * 0.5

	// seenSilenceSinceLastSource tracks whether the signal has passed through
	// None (silence) since the last committed source change. Direct Vinyl↔CD
	// transitions are blocked until this is true, because a real amplifier
	// source switch always produces a brief silence. A ratio dip during a
	// loud passage is track content, not a source change.
	seenSilenceSinceLastSource := true // allow initial detection from None

	// Sliding window of the last DebounceWindows classifications.
	// We commit a new state when the majority of the window agrees,
	// so a single outlier during a source transition does not reset
	// the counter and cause multi-second delays.
	window := make([]Source, cfg.DebounceWindows)
	windowPos := 0
	for i := range window {
		window[i] = SourceNone
	}

	bytesPerWindow := cfg.BufferSize * 2 * 2 // stereo, 16-bit
	raw := make([]byte, bytesPerWindow)
	samples := make([]float64, cfg.BufferSize)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if _, err := io.ReadFull(stdout, raw); err != nil {
			return fmt.Errorf("read pcm: %w", err)
		}

		for i := 0; i < cfg.BufferSize; i++ {
			left := int16(binary.LittleEndian.Uint16(raw[i*4:]))
			right := int16(binary.LittleEndian.Uint16(raw[i*4+2:]))
			samples[i] = float64(left+right) / 2.0 / 32768.0
		}

		detected, rms, ratio := classify(samples, cfg)

		if cfg.Verbose {
			log.Printf("window  rms=%.6f  ratio=%.4f  raw=%s  current=%s  silence_seen=%v",
				rms, ratio, detected, current, seenSilenceSinceLastSource)
		}

		// Track whether we've passed through silence since the last source commit.
		if detected == SourceNone {
			seenSilenceSinceLastSource = true
		}

		detected = applyHysteresis(detected, current, rms, ratio, cfg, hysteresisMargin, seenSilenceSinceLastSource)

		// Insert into sliding window.
		window[windowPos] = detected
		windowPos = (windowPos + 1) % cfg.DebounceWindows

		// Count votes for each source in the window.
		votes := make(map[Source]int, 3)
		for _, s := range window {
			votes[s]++
		}

		// The winner needs a strict majority (> half the window).
		majority := cfg.DebounceWindows/2 + 1
		winner := current
		for src, count := range votes {
			if count >= majority {
				winner = src
				break
			}
		}

		if winner != current {
			log.Printf("source changed: %s → %s  (rms=%.5f  ratio=%.4f  votes=%v)",
				current, winner, rms, ratio, votes)
			current = winner
			// Reset silence gate — next Vinyl↔CD transition requires
			// passing through silence again.
			seenSilenceSinceLastSource = false
			if err := writeState(cfg.OutputFile, current); err != nil {
				log.Printf("write state error: %v", err)
			}
		}
	}
}

// applyHysteresis resists flipping between Vinyl and CD near the threshold.
// The margin creates a dead band: once in Vinyl, ratio must drop below
// (threshold - margin) to flip to CD; once in CD, ratio must exceed
// (threshold + margin) to flip to Vinyl.
//
// Additionally, direct Vinyl↔CD transitions are blocked unless the signal
// passed through None (silence) first. This reflects the physical reality
// that switching sources on an amplifier always produces a brief silence.
// A high RMS signal can never be a source change — it must be the same source.
func applyHysteresis(detected, current Source, rms, ratio float64, cfg Config, margin float64, seenSilenceSinceLastSource bool) Source {
	// Block direct Vinyl↔CD flip if we haven't seen silence since the
	// last committed source change. Real source switches always pass through
	// a quiet moment; a ratio dip during loud music is just the track content.
	if (current == SourceVinyl && detected == SourceCD) ||
		(current == SourceCD && detected == SourceVinyl) {
		if !seenSilenceSinceLastSource {
			return current
		}
	}

	if current == SourceVinyl && detected == SourceCD {
		if ratio > cfg.VinylThreshold-margin {
			return SourceVinyl
		}
	}
	if current == SourceCD && detected == SourceVinyl {
		if ratio < cfg.VinylThreshold+margin {
			return SourceCD
		}
	}
	return detected
}

// classify analyses a window of samples and returns Source, RMS, and low-freq ratio.
// Returning all three avoids recomputing FFT in the hysteresis step.
//
// Vinyl requires BOTH a high low-freq ratio AND a minimum RMS signal strength.
// This prevents ambient noise (which can have a high ratio at very low amplitude)
// from being misclassified as Vinyl.
func classify(samples []float64, cfg Config) (Source, float64, float64) {
	rms := computeRMS(samples)

	if rms < cfg.SilenceThreshold {
		return SourceNone, rms, 0
	}

	spectrum := fft(samples)
	ratio := lowFrequencyRatio(spectrum, cfg.SampleRate, cfg.BufferSize)

	if ratio > cfg.VinylThreshold && rms >= cfg.MinVinylRMS {
		return SourceVinyl, rms, ratio
	}
	return SourceCD, rms, ratio
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