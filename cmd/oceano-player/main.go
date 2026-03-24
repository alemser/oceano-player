package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Audio struct {
		AlsaDevice string `yaml:"alsa_device"`
	} `yaml:"audio"`

	AirPlay struct {
		Enabled                  bool   `yaml:"enabled"`
		Name                     string `yaml:"name"`
		EnableAirPlay2IfAvailable bool  `yaml:"enable_airplay2_if_available"`
	} `yaml:"airplay"`

	Process struct {
		RuntimeDir string `yaml:"runtime_dir"`
	} `yaml:"process"`
}

func readConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "Path to config YAML")
	flag.Parse()

	cfg, err := readConfig(configPath)
	if err != nil {
		log.Fatalf("read config: %v", err)
	}

	if cfg.Process.RuntimeDir == "" {
		cfg.Process.RuntimeDir = "./run"
	}
	if err := os.MkdirAll(cfg.Process.RuntimeDir, 0o755); err != nil {
		log.Fatalf("create runtime dir: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.AirPlay.Enabled {
		if err := runAndSupervise(ctx, cfg, configPath); err != nil {
			log.Fatalf("airplay supervisor: %v", err)
		}
	} else {
		<-ctx.Done()
	}
}

func runAndSupervise(ctx context.Context, cfg *Config, configPath string) error {
	// We intentionally keep this minimal: start shairport-sync with sane flags
	// and restart it if it exits unexpectedly.
	backoff := 500 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		cmd, err := buildShairportCmd(ctx, cfg)
		if err != nil {
			return err
		}

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		log.Printf("starting shairport-sync (airplay): name=%q alsa=%q", cfg.AirPlay.Name, cfg.Audio.AlsaDevice)
		err = cmd.Run()

		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if err == nil {
			log.Printf("shairport-sync exited cleanly; restarting in %s", backoff)
		} else {
			log.Printf("shairport-sync exited with error: %v; restarting in %s", err, backoff)
		}

		time.Sleep(backoff)
		if backoff < 10*time.Second {
			backoff *= 2
		}

		// If config gets changed, user restarts systemd service.
		_ = configPath
	}
}

func buildShairportCmd(ctx context.Context, cfg *Config) (*exec.Cmd, error) {
	if cfg.Audio.AlsaDevice == "" {
		return nil, fmt.Errorf("audio.alsa_device must be set")
	}
	if cfg.AirPlay.Name == "" {
		cfg.AirPlay.Name = "Oceano"
	}

	// Minimal CLI configuration:
	// -a name
	// -o alsa
	// -- -d <device> (alsa backend device)
	//
	// Different distros/packages expose slightly different flags; we stick to
	// the common subset and allow users to switch to a shairport-sync.conf later.
	args := []string{
		"-a", cfg.AirPlay.Name,
		"-o", "alsa",
		"--",
		"-d", cfg.Audio.AlsaDevice,
	}

	cmd := exec.CommandContext(ctx, "shairport-sync", args...)

	// Ensure relative runtime paths behave predictably under systemd.
	cmd.Dir = filepath.Dir(mustAbs("."))
	return cmd, nil
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

