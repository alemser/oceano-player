package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_DiscogsDefaultsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"recognition":{"acrcloud_host":"identify-eu-west-1.acrcloud.com"}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.Recognition.Discogs.Enabled {
		t.Fatal("discogs.enabled should default to false")
	}
	if cfg.Recognition.Discogs.TimeoutSecs != 6 {
		t.Fatalf("discogs.timeout_secs=%d want 6", cfg.Recognition.Discogs.TimeoutSecs)
	}
	if cfg.Recognition.Discogs.MaxRetries != 2 {
		t.Fatalf("discogs.max_retries=%d want 2", cfg.Recognition.Discogs.MaxRetries)
	}
	if cfg.Recognition.Discogs.CacheTTLHours != 72 {
		t.Fatalf("discogs.cache_ttl_hours=%d want 72", cfg.Recognition.Discogs.CacheTTLHours)
	}
}

func TestLoadConfig_DiscogsNormalizesInvalidValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
  "recognition": {
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

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if !cfg.Recognition.Discogs.Enabled {
		t.Fatal("discogs.enabled should remain true")
	}
	if cfg.Recognition.Discogs.Token != "abc" {
		t.Fatalf("discogs.token=%q want abc", cfg.Recognition.Discogs.Token)
	}
	if cfg.Recognition.Discogs.TimeoutSecs != 6 {
		t.Fatalf("discogs.timeout_secs=%d want 6", cfg.Recognition.Discogs.TimeoutSecs)
	}
	if cfg.Recognition.Discogs.MaxRetries != 2 {
		t.Fatalf("discogs.max_retries=%d want 2", cfg.Recognition.Discogs.MaxRetries)
	}
	if cfg.Recognition.Discogs.CacheTTLHours != 72 {
		t.Fatalf("discogs.cache_ttl_hours=%d want 72", cfg.Recognition.Discogs.CacheTTLHours)
	}
}

