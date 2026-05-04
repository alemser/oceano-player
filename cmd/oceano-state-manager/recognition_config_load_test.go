package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyRecognitionProvidersFromConfigFile_DiscogsDefaultsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"recognition":{"providers":[]}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := defaultConfig()
	cfg.CalibrationConfigPath = path
	applyRecognitionProvidersFromConfigFile(&cfg)

	if cfg.Discogs.Enabled {
		t.Fatal("discogs.enabled should default to false")
	}
	if cfg.Discogs.Timeout != 6*time.Second {
		t.Fatalf("discogs.timeout=%v want 6s", cfg.Discogs.Timeout)
	}
	if cfg.Discogs.MaxRetries != 2 {
		t.Fatalf("discogs.max_retries=%d want 2", cfg.Discogs.MaxRetries)
	}
	if cfg.Discogs.CacheTTL != 72*time.Hour {
		t.Fatalf("discogs.cache_ttl=%v want 72h", cfg.Discogs.CacheTTL)
	}
}

func TestApplyRecognitionProvidersFromConfigFile_DiscogsNormalizesValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
  "recognition": {
    "providers": [],
    "discogs": {
      "enabled": true,
      "token": "  abc  ",
      "timeout_secs": 0,
      "max_retries": -1,
      "cache_ttl_hours": 0
    }
  }
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := defaultConfig()
	cfg.CalibrationConfigPath = path
	applyRecognitionProvidersFromConfigFile(&cfg)

	if !cfg.Discogs.Enabled {
		t.Fatal("discogs.enabled should remain true")
	}
	if cfg.Discogs.Token != "abc" {
		t.Fatalf("discogs.token=%q want abc", cfg.Discogs.Token)
	}
	if cfg.Discogs.Timeout != 6*time.Second {
		t.Fatalf("discogs.timeout=%v want 6s", cfg.Discogs.Timeout)
	}
	if cfg.Discogs.MaxRetries != 2 {
		t.Fatalf("discogs.max_retries=%d want 2", cfg.Discogs.MaxRetries)
	}
	if cfg.Discogs.CacheTTL != 72*time.Hour {
		t.Fatalf("discogs.cache_ttl=%v want 72h", cfg.Discogs.CacheTTL)
	}
}

