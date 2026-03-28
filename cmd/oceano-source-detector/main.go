package main

import (
	"context" // Importação correta do context
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
	BassVinylThreshold float64
	DebounceWindows    int
	OutputFile         string
	Verbose            bool
}

func defaultConfig() Config {
	return Config{
		AlsaDevice:         "plughw:2,0",
		SampleRate:         44100,
		BufferSize:         8192,
		SilenceThreshold:   0.008,
		QuietThreshold:     0.040,
		BassVinylThreshold: 0.0025,
		DebounceWindows:    5,
		OutputFile:         "/tmp/oceano-source.json",
		Verbose:            false,
	}
}

func main() {
	cfg := defaultConfig()

	flag.StringVar(&cfg.AlsaDevice, "device", cfg.AlsaDevice, "ALSA capture device")
	flag.StringVar(&cfg.OutputFile, "output", cfg.OutputFile, "Output JSON file path")
	flag.Float64Var(&cfg.SilenceThreshold, "silence-threshold", cfg.SilenceThreshold, "RMS abaixo disso = desligado")
	flag.Float64Var(&cfg.QuietThreshold, "quiet-threshold", cfg.QuietThreshold, "RMS abaixo disso = passagem calma")
	flag.Float64Var(&cfg.BassVinylThreshold, "bass-vinyl-threshold", cfg.BassVinylThreshold, "Bass RMS acima disso = Vinyl")
	flag.IntVar(&cfg.DebounceWindows, "debounce", cfg.DebounceWindows, "Janelas consecutivas")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "Log detalhado")
	flag.Parse()

	log.Printf("Oceano Source Detector iniciado")

	if !isPowerOfTwo(cfg.BufferSize) {
		log.Fatalf("buffer-size deve ser potência de 2")
	}

	// Corrigido: context.WithSignal (ou o padrão NotifyContext)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("Erro: %v", err)
	}
}

// Corrigido: context.Context como tipo de argumento
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
				log.Printf("Erro na stream: %v — Reiniciando em 2s", err)
				time.Sleep(2 * time.Second)
			}
		}
	}
}

// Corrigido: context.Context como tipo de argumento
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
				log.Printf("Mudança: %s → None", current)
				current, candidate, candidateCount = SourceNone, SourceNone, 0
				_ = writeState(cfg.OutputFile, current)
			}
			continue
		}

		// FIX: Se None, permite classificar mesmo em volume alto
		if rms >= cfg.QuietThreshold && current != SourceNone {
			if cfg.Verbose {
				log.Printf("Hold: %s (RMS: %.4f)", current, rms)
			}
			candidate, candidateCount = SourceNone, 0
			continue
		}

		spectrum := fft(samples)
		bassRMS := computeBassRMS(spectrum, cfg.SampleRate, cfg.BufferSize)

		var detected Source
		if bassRMS > cfg.BassVinylThreshold {
			detected = SourceVinyl
		} else {
			detected = SourceCD
		}

		if detected == candidate {
			candidateCount++
		} else {
			candidate, candidateCount = detected, 1
		}

		if candidateCount >= cfg.DebounceWindows && candidate != current {
			log.Printf("DETECTADO: %s → %s (Bass: %.5f)", current, candidate, bassRMS)
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

func computeBassRMS(spectrum []complex128, sampleRate, bufferSize int) float64 {
	binHz := float64(sampleRate) / float64(bufferSize)
	lowMin := int(20.0 / binHz)
	lowMax := int(80.0 / binHz)
	var energy float64
	for i := lowMin; i <= lowMax; i++ {
		mag := cmplx.Abs(spectrum[i])
		energy += mag * mag
	}
	return (math.Sqrt(energy/float64(bufferSize)) / float64(bufferSize/2))
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