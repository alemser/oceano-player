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
		// VinylRatioThreshold: sub-bass (15-40 Hz) energy as fraction of total.
		//   15-40 Hz is below musical content (kick drums start at ~50 Hz, bass guitar ~40-200 Hz).
		//   Vinyl platter/arm resonance lives at 7-40 Hz; CD has near-zero energy here.
		//   Start at 0.02 and tune from calibration output.
		MinVinylRMS:         0.010,
		VinylRatioThreshold: 0.02,
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
	// Majority vote over the last N windows. Tolerates occasional miscounts without
	// resetting the streak to zero — unlike a consecutive-only debounce.
	voteWindow := make([]Source, cfg.DebounceWindows)
	for i := range voteWindow {
		voteWindow[i] = SourceNone
	}
	voteIdx := 0
	confirmed := false
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
				current = SourceNone
				confirmed = false
				for i := range voteWindow {
					voteWindow[i] = SourceNone
				}
				voteIdx = 0
				_ = writeState(cfg.OutputFile, current)
			}
			continue
		}

		// Hold confirmed source during active music to prevent misclassification.
		if rms >= cfg.QuietThreshold && current != SourceNone && confirmed {
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

		// Dual gate: require meaningful signal AND high sub-bass ratio for Vinyl.
		var detected Source
		if rms >= cfg.MinVinylRMS && bassRatio >= cfg.VinylRatioThreshold {
			detected = SourceVinyl
		} else {
			detected = SourceCD
		}

		// Record vote in rolling window.
		voteWindow[voteIdx%cfg.DebounceWindows] = detected
		voteIdx++

		// Count votes only once the window is full.
		if voteIdx < cfg.DebounceWindows {
			if cfg.Verbose {
				log.Printf("warming up: rms=%.5f bass_ratio=%.4f hf_ratio=%.4f det=%s (%d/%d)",
					rms, bassRatio, hfRatio, detected, voteIdx, cfg.DebounceWindows)
			}
			continue
		}

		cdVotes, vinylVotes := 0, 0
		for _, v := range voteWindow {
			if v == SourceCD {
				cdVotes++
			} else if v == SourceVinyl {
				vinylVotes++
			}
		}
		majority := cfg.DebounceWindows/2 + 1

		var winner Source
		switch {
		case vinylVotes >= majority:
			winner = SourceVinyl
		case cdVotes >= majority:
			winner = SourceCD
		default:
			winner = current // no majority yet, keep current
		}

		if cfg.Verbose {
			log.Printf("analyzing: rms=%.5f bass_ratio=%.4f hf_ratio=%.4f det=%s votes(cd=%d vinyl=%d) curr=%s",
				rms, bassRatio, hfRatio, detected, cdVotes, vinylVotes, current)
		}

		if winner != SourceNone && winner != current {
			log.Printf("SOURCE DETECTED: %s → %s (rms=%.5f bass_ratio=%.4f hf_ratio=%.4f cd_votes=%d vinyl_votes=%d)",
				current, winner, rms, bassRatio, hfRatio, cdVotes, vinylVotes)
			current = winner
			confirmed = true
			_ = writeState(cfg.OutputFile, current)
		} else if winner == current {
			confirmed = true
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

// computeLowFreqRatio returns the fraction of spectral energy in the 15–40 Hz sub-bass band.
// This band is below musical content: kick drums start ~50 Hz, bass guitar ~40-200 Hz.
// Vinyl platter/arm resonance concentrates at 7-40 Hz; CD digital audio has near-zero
// energy here. Using a sub-musical band avoids false Vinyl detections on bass-heavy CD tracks.
func computeLowFreqRatio(spectrum []complex128, sampleRate, bufferSize int) float64 {
	binHz := float64(sampleRate) / float64(bufferSize)
	lowMin := int(15.0 / binHz)
	lowMax := int(40.0 / binHz)

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
