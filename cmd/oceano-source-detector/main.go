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
//
// Algorithm overview:
//
//	The detector classifies audio using the QUIET-WINDOW BASS FLOOR approach.
//	During active music, CD and Vinyl are indistinguishable — both have similar
//	RMS and frequency content. But during quiet passages (between tracks, soft
//	moments), Vinyl's bass floor stays elevated due to motor rumble and stylus
//	friction, while CD drops to the hardware noise floor.
//
//	Classification only happens during quiet windows. During active music the
//	last known classification is held. This makes the detector immune to
//	musical content and only sensitive to the source's noise floor.
type Config struct {
	AlsaDevice      string
	SampleRate      int
	BufferSize      int
	SilenceThreshold float64 // RMS below this = amp off / no source (true silence)
	QuietThreshold  float64 // RMS below this = quiet passage (classify here)
	BassVinylThreshold float64 // rms_bass above this during quiet = Vinyl
	DebounceWindows int
	OutputFile      string
	Verbose         bool
}

func defaultConfig() Config {
	return Config{
		AlsaDevice:         "plughw:CARD=Microphone,DEV=0",
		SampleRate:         44100,
		BufferSize:         8192,
		SilenceThreshold:   0.005,  // below = amp off or nothing playing
		QuietThreshold:     0.015,  // below = quiet passage, classify here
		BassVinylThreshold: 0.0017, // rms_bass above this during quiet = Vinyl
		DebounceWindows:    5,
		OutputFile:         "/tmp/oceano-source.json",
		Verbose:            false,
	}
}

func main() {
	cfg := defaultConfig()

	flag.StringVar(&cfg.AlsaDevice, "device", cfg.AlsaDevice, "ALSA capture device")
	flag.StringVar(&cfg.OutputFile, "output", cfg.OutputFile, "Output JSON file path")
	flag.Float64Var(&cfg.SilenceThreshold, "silence-threshold", cfg.SilenceThreshold, "RMS below this = nothing playing (amp off or muted)")
	flag.Float64Var(&cfg.QuietThreshold, "quiet-threshold", cfg.QuietThreshold, "RMS below this = quiet passage, classify source here")
	flag.Float64Var(&cfg.BassVinylThreshold, "bass-vinyl-threshold", cfg.BassVinylThreshold, "Bass RMS above this during quiet = Vinyl")
	flag.IntVar(&cfg.DebounceWindows, "debounce", cfg.DebounceWindows, "Consecutive agreeing quiet windows before committing")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "Log every window (useful for calibration)")
	flag.Parse()

	log.Printf("oceano-source-detector starting")
	log.Printf("  device:               %s", cfg.AlsaDevice)
	log.Printf("  output:               %s", cfg.OutputFile)
	log.Printf("  silence-threshold:    %.5f", cfg.SilenceThreshold)
	log.Printf("  quiet-threshold:      %.5f", cfg.QuietThreshold)
	log.Printf("  bass-vinyl-threshold: %.5f", cfg.BassVinylThreshold)
	log.Printf("  debounce windows:     %d", cfg.DebounceWindows)
	log.Printf("  verbose:              %v", cfg.Verbose)

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

	// current is the committed source shown to the outside world.
	// candidate tracks what quiet windows are voting for.
	current := SourceNone
	candidate := SourceNone
	candidateCount := 0

	bytesPerWindow := cfg.BufferSize * 2 * 2 // stereo S16_LE
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

		// Convert interleaved S16_LE stereo to mono float64.
		for i := 0; i < cfg.BufferSize; i++ {
			left := int16(binary.LittleEndian.Uint16(raw[i*4:]))
			right := int16(binary.LittleEndian.Uint16(raw[i*4+2:]))
			samples[i] = float64(left+right) / 2.0 / 32768.0
		}

		rms := computeRMS(samples)

		// True silence: amp off or no source selected.
		if rms < cfg.SilenceThreshold {
			if cfg.Verbose {
				log.Printf("silence  rms=%.5f", rms)
			}
			if current != SourceNone {
				log.Printf("source changed: %s → None (silence)", current)
				current = SourceNone
				candidate = SourceNone
				candidateCount = 0
				_ = writeState(cfg.OutputFile, current)
			}
			continue
		}

		// Active music: hold current classification, do not classify.
		if rms >= cfg.QuietThreshold {
			if cfg.Verbose {
				log.Printf("active   rms=%.5f  holding=%s", rms, current)
			}
			// Reset candidate count — we only count consecutive quiet windows.
			candidate = SourceNone
			candidateCount = 0
			continue
		}

		// Quiet passage: rms is between SilenceThreshold and QuietThreshold.
		// This is where we classify the source using the bass floor.
		spectrum := fft(samples)
		bassRMS := computeBassRMS(spectrum, cfg.SampleRate, cfg.BufferSize)

		var detected Source
		if bassRMS > cfg.BassVinylThreshold {
			detected = SourceVinyl
		} else {
			detected = SourceCD
		}

		if cfg.Verbose {
			log.Printf("quiet    rms=%.5f  bass=%.5f  detected=%s  candidate=%s(%d)  current=%s",
				rms, bassRMS, detected, candidate, candidateCount, current)
		}

		// Debounce: require N consecutive quiet windows agreeing before commit.
		if detected == candidate {
			candidateCount++
		} else {
			candidate = detected
			candidateCount = 1
		}

		if candidateCount >= cfg.DebounceWindows && candidate != current {
			log.Printf("source changed: %s → %s  (rms=%.5f  bass=%.5f  quiet windows=%d)",
				current, candidate, rms, bassRMS, candidateCount)
			current = candidate
			if err := writeState(cfg.OutputFile, current); err != nil {
				log.Printf("write state error: %v", err)
			}
		}
	}
}

// computeRMS returns the root mean square of the samples.
func computeRMS(samples []float64) float64 {
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// computeBassRMS returns the RMS of the signal in the 20–80 Hz band.
// This is the vinyl motor rumble range. Computed from the FFT spectrum.
func computeBassRMS(spectrum []complex128, sampleRate, bufferSize int) float64 {
	binHz  := float64(sampleRate) / float64(bufferSize)
	lowMin := max(1, int(20.0/binHz))
	lowMax := int(80.0 / binHz)

	var energy float64
	count := 0
	for i, c := range spectrum[:bufferSize/2] {
		if i >= lowMin && i <= lowMax {
			mag := cmplx.Abs(c)
			energy += mag * mag
			count++
		}
	}
	if count == 0 {
		return 0
	}
	// Normalise to match the scale of time-domain RMS.
	return math.Sqrt(energy/float64(bufferSize)) / float64(bufferSize/2)
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
	bits := int(math.Log2(float64(n)))
	for i := 0; i < n; i++ {
		j := bitReverse(i, bits)
		if j > i {
			a[i], a[j] = a[j], a[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		half  := length / 2
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

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

func sleep(ctx interface{ Done() <-chan struct{} }, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

type backgroundCtx struct{}

func (backgroundCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (backgroundCtx) Done() <-chan struct{}         { return nil }
func (backgroundCtx) Err() error                   { return nil }
func (backgroundCtx) Value(any) any                { return nil }