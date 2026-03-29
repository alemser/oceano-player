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
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	AlsaDevice       string
	DeviceMatch      string // substring to match in /proc/asound/cards (e.g. "USB Microphone")
	SampleRate       int
	BufferSize       int
	SilenceThreshold float64
	DebounceWindows  int
	OutputFile       string
	VUSocket         string
	PCMSocket        string // Unix socket for raw PCM relay; consumers read S16_LE stereo at SampleRate Hz
	Verbose          bool
}

func defaultConfig() Config {
	return Config{
		AlsaDevice:       "",
		DeviceMatch:      "USB Microphone",
		SampleRate:       44100,
		BufferSize:       2048,
		SilenceThreshold: 0.008,
		DebounceWindows:  10,
		OutputFile:       "/tmp/oceano-source.json",
		VUSocket:         "/tmp/oceano-vu.sock",
		PCMSocket:        "/tmp/oceano-pcm.sock",
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

func main() {
	cfg := defaultConfig()

	flag.StringVar(&cfg.DeviceMatch, "device-match", cfg.DeviceMatch, "Substring to match in /proc/asound/cards (auto-detects card number)")
	flag.StringVar(&cfg.AlsaDevice, "device", cfg.AlsaDevice, "Explicit ALSA capture device (overridden by --device-match if both set)")
	flag.StringVar(&cfg.OutputFile, "output", cfg.OutputFile, "Output JSON file path")
	flag.StringVar(&cfg.VUSocket, "vu-socket", cfg.VUSocket, "Unix socket path for VU meter frames (8 bytes: float32 L + float32 R)")
	flag.StringVar(&cfg.PCMSocket, "pcm-socket", cfg.PCMSocket, "Unix socket path for raw PCM relay (S16_LE stereo at sample-rate Hz)")
	flag.Float64Var(&cfg.SilenceThreshold, "silence-threshold", cfg.SilenceThreshold, "RMS below this = silence / no physical source")
	flag.IntVar(&cfg.DebounceWindows, "debounce", cfg.DebounceWindows, "Majority vote window size")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "Detailed logging")
	flag.Parse()

	log.Printf("oceano-source-detector starting")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("detector error: %v", err)
	}
}

func run(ctx context.Context, cfg Config) error {
	_ = os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755)
	_ = writeState(cfg.OutputFile, SourceNone)

	hub := newVUHub()
	go hub.run(ctx)
	go listenVU(ctx, cfg.VUSocket, hub)

	pcm := newPCMHub()
	go pcm.run(ctx)
	go listenPCM(ctx, cfg.PCMSocket, pcm)

	for {
		select {
		case <-ctx.Done():
			_ = writeState(cfg.OutputFile, SourceNone)
			return nil
		default:
			dev, err := resolveDevice(cfg)
			if err != nil {
				log.Printf("device error: %v — retrying in 5s", err)
				time.Sleep(5 * time.Second)
				continue
			}
			if err := runStream(ctx, cfg, dev, hub, pcm); err != nil {
				log.Printf("stream error on %s: %v — restarting in 2s", dev, err)
				time.Sleep(2 * time.Second)
			}
		}
	}
}

func runStream(ctx context.Context, cfg Config, device string, hub *vuHub, pcm *pcmHub) error {
	log.Printf("opening capture device %s", device)
	cmd := exec.Command("arecord",
		"-D", device,
		"-f", "S16_LE",
		"-r", fmt.Sprintf("%d", cfg.SampleRate),
		"-c", "2",
		"-t", "raw",
		"--duration=0",
	)
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
	voteWindow := make([]Source, cfg.DebounceWindows)
	for i := range voteWindow {
		voteWindow[i] = SourceNone
	}
	voteIdx := 0
	lastHeartbeat := time.Now()

	raw := make([]byte, cfg.BufferSize*4)
	left := make([]float64, cfg.BufferSize)
	right := make([]float64, cfg.BufferSize)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if _, err := io.ReadFull(stdout, raw); err != nil {
			return err
		}

		// Relay raw PCM to any connected consumers (e.g. recognizer).
		// Copy the buffer since raw is reused on the next iteration.
		chunk := make([]byte, len(raw))
		copy(chunk, raw)
		pcm.publish(chunk)

		for i := 0; i < cfg.BufferSize; i++ {
			l := int16(binary.LittleEndian.Uint16(raw[i*4:]))
			r := int16(binary.LittleEndian.Uint16(raw[i*4+2:]))
			left[i] = float64(l) / 32768.0
			right[i] = float64(r) / 32768.0
		}

		rmsL := computeRMS(left)
		rmsR := computeRMS(right)
		rms := (rmsL + rmsR) / 2.0

		// Publish VU levels regardless of silence state — the consumer decides
		// whether to display them.
		hub.publish(VUFrame{Left: float32(rmsL), Right: float32(rmsR)})

		var detected Source
		if rms >= cfg.SilenceThreshold {
			detected = SourcePhysical
		} else {
			detected = SourceNone
		}

		voteWindow[voteIdx%cfg.DebounceWindows] = detected
		voteIdx++

		if voteIdx < cfg.DebounceWindows {
			if cfg.Verbose {
				log.Printf("warming up: rms=%.5f det=%s (%d/%d)", rms, detected, voteIdx, cfg.DebounceWindows)
			}
			continue
		}

		noneVotes, physicalVotes := 0, 0
		for _, v := range voteWindow {
			if v == SourceNone {
				noneVotes++
			} else {
				physicalVotes++
			}
		}
		majority := cfg.DebounceWindows/2 + 1

		var winner Source
		switch {
		case physicalVotes >= majority:
			winner = SourcePhysical
		case noneVotes >= majority:
			winner = SourceNone
		default:
			winner = current
		}

		if cfg.Verbose {
			log.Printf("rms=%.5f det=%s votes(none=%d physical=%d) curr=%s",
				rms, detected, noneVotes, physicalVotes, current)
		} else if now := time.Now(); now.Sub(lastHeartbeat) >= time.Minute {
			log.Printf("heartbeat: source=%s rms=%.5f", current, rms)
			lastHeartbeat = now
		}

		if winner != current {
			log.Printf("SOURCE: %s → %s (rms=%.5f)", current, winner, rms)
			current = winner
			_ = writeState(cfg.OutputFile, current)
		}
	}
}

// --- PCM hub: fan-out raw PCM chunks to all connected socket clients ---
// Consumers receive continuous S16_LE stereo bytes at cfg.SampleRate Hz.
// This allows multiple readers (e.g. the recognizer) without opening the
// ALSA device a second time.

type pcmHub struct {
	mu       sync.Mutex
	clients  map[chan []byte]struct{}
	publish_ chan []byte
}

func newPCMHub() *pcmHub {
	return &pcmHub{
		clients:  make(map[chan []byte]struct{}),
		publish_: make(chan []byte, 4),
	}
}

func (h *pcmHub) publish(chunk []byte) {
	select {
	case h.publish_ <- chunk:
	default:
		// No consumer keeping up — drop chunk rather than block the audio loop.
	}
}

func (h *pcmHub) subscribe() chan []byte {
	ch := make(chan []byte, 32) // larger buffer: PCM chunks are ~8 KB each
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *pcmHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *pcmHub) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case chunk := <-h.publish_:
			h.mu.Lock()
			for ch := range h.clients {
				select {
				case ch <- chunk:
				default:
					// Slow client — drop chunk.
				}
			}
			h.mu.Unlock()
		}
	}
}

// listenPCM accepts connections on a Unix socket and streams raw PCM bytes.
// The stream format is S16_LE, 2 channels, at cfg.SampleRate Hz (default 44100).
// There is no framing — bytes arrive as a continuous stream.
func listenPCM(ctx context.Context, socketPath string, hub *pcmHub) {
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Printf("PCM socket: failed to listen on %s: %v", socketPath, err)
		return
	}
	defer func() {
		ln.Close()
		_ = os.Remove(socketPath)
	}()
	log.Printf("PCM socket listening on %s", socketPath)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("PCM socket: accept error: %v", err)
			return
		}
		go handlePCMConn(ctx, conn, hub)
	}
}

func handlePCMConn(ctx context.Context, conn net.Conn, hub *pcmHub) {
	defer conn.Close()
	ch := hub.subscribe()
	defer hub.unsubscribe(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case chunk := <-ch:
			if _, err := conn.Write(chunk); err != nil {
				return // client disconnected
			}
		}
	}
}

// --- VU hub: fan-out to all connected socket clients ---

type vuHub struct {
	mu       sync.Mutex
	clients  map[chan VUFrame]struct{}
	publish_ chan VUFrame
}

func newVUHub() *vuHub {
	return &vuHub{
		clients:  make(map[chan VUFrame]struct{}),
		publish_: make(chan VUFrame, 4),
	}
}

func (h *vuHub) publish(f VUFrame) {
	select {
	case h.publish_ <- f:
	default:
		// No consumer keeping up — drop frame rather than block the audio loop.
	}
}

func (h *vuHub) subscribe() chan VUFrame {
	ch := make(chan VUFrame, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *vuHub) unsubscribe(ch chan VUFrame) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *vuHub) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-h.publish_:
			h.mu.Lock()
			for ch := range h.clients {
				select {
				case ch <- frame:
				default:
					// Slow client — drop frame.
				}
			}
			h.mu.Unlock()
		}
	}
}

// --- VU Unix socket server ---

// listenVU accepts connections on a Unix socket and streams VU frames.
// Each frame is 8 bytes: float32 left RMS + float32 right RMS, little-endian.
func listenVU(ctx context.Context, socketPath string, hub *vuHub) {
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Printf("VU socket: failed to listen on %s: %v", socketPath, err)
		return
	}
	defer func() {
		ln.Close()
		_ = os.Remove(socketPath)
	}()
	log.Printf("VU socket listening on %s", socketPath)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("VU socket: accept error: %v", err)
			return
		}
		go handleVUConn(ctx, conn, hub)
	}
}

func handleVUConn(ctx context.Context, conn net.Conn, hub *vuHub) {
	defer conn.Close()
	ch := hub.subscribe()
	defer hub.unsubscribe(ch)

	buf := make([]byte, 8)
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-ch:
			binary.LittleEndian.PutUint32(buf[0:4], math.Float32bits(frame.Left))
			binary.LittleEndian.PutUint32(buf[4:8], math.Float32bits(frame.Right))
			if _, err := conn.Write(buf); err != nil {
				return // client disconnected
			}
		}
	}
}

// --- Helpers ---

func computeRMS(samples []float64) float64 {
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
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
