package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

// File map for this service:
// - main.go: process entrypoint + CLI flags
// - config_device.go: config/defaults + ALSA device detection
// - runtime.go: arecord loop + RMS/source detection + state writes
// - sockets.go: PCM/VU socket fan-out hubs

func parseFlags(cfg *Config) {
	flag.StringVar(&cfg.DeviceMatch, "device-match", cfg.DeviceMatch, "Substring to match in /proc/asound/cards (auto-detects card number)")
	flag.StringVar(&cfg.AlsaDevice, "device", cfg.AlsaDevice, "Explicit ALSA capture device (overridden by --device-match if both set)")
	flag.StringVar(&cfg.OutputFile, "output", cfg.OutputFile, "Output JSON file path")
	flag.StringVar(&cfg.VUSocket, "vu-socket", cfg.VUSocket, "Unix socket path for VU meter frames (8 bytes: float32 L + float32 R)")
	flag.StringVar(&cfg.PCMSocket, "pcm-socket", cfg.PCMSocket, "Unix socket path for raw PCM relay (S16_LE stereo at sample-rate Hz)")
	flag.StringVar(&cfg.CalibrationFile, "calibration-file", cfg.CalibrationFile, "Path to persisted noise-floor calibration JSON (generic; per-format files are derived automatically)")
	flag.StringVar(&cfg.FormatHintFile, "format-hint-file", cfg.FormatHintFile, "Path to format hint JSON written by oceano-state-manager (selects per-format calibration file at startup)")
	flag.Float64Var(&cfg.SilenceThreshold, "silence-threshold", cfg.SilenceThreshold, "Manual RMS threshold override (0 = use adaptive learner)")
	flag.Float64Var(&cfg.StdDevThreshold, "stddev-threshold", cfg.StdDevThreshold, "Manual StdDev threshold override (0 = use adaptive learner)")
	flag.IntVar(&cfg.DebounceWindows, "debounce", cfg.DebounceWindows, "Majority vote window size")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "Detailed logging")
	flag.Parse()
}

func main() {
	cfg := defaultConfig()
	parseFlags(&cfg)

	log.Printf("oceano-source-detector starting")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("detector error: %v", err)
	}
}
