// Package shairport provides shared shairport-sync configuration for Oceano services.
//
// Shairport is installed as a system unit running as the shairport-sync user, which
// cannot connect to the per-user PipeWire-Pulse socket at /run/user/*/pulse. The
// reliable output path is the ALSA backend to the same DAC the UI configures.
package shairport

import (
	"fmt"
	"os"
	"strings"
)

// WriteConfig writes shairport-sync.conf with direct ALSA output. alsaOutputDevice
// is typically plughw:… from oceano-setup / config.json. If empty, "default" is used.
func WriteConfig(path, airplayName, alsaOutputDevice string) error {
	alsaOutputDevice = strings.TrimSpace(alsaOutputDevice)
	if alsaOutputDevice == "" {
		alsaOutputDevice = "default"
	}
	name := strings.TrimSpace(airplayName)
	if name == "" {
		name = "Oceano"
	}
	if _, err := os.Stat(path); err == nil {
		bak := path + ".oceano.bak"
		if _, e := os.Stat(bak); os.IsNotExist(e) {
			orig, re := os.ReadFile(path)
			if re == nil {
				_ = os.WriteFile(bak, orig, 0o644)
			}
		}
	}
	// soxr: matches prior settings; alsa: works for the dedicated service user, which
	// cannot use the logged-in user's PipeWire-Pulse at /run/user/…/pulse.
	content := fmt.Sprintf(`general =
{
  name = %q;
  output_backend = "alsa";
  interpolation = "soxr";
};

alsa =
{
  output_device = %q;
};

metadata =
{
  enabled = "yes";
  include_cover_art = "yes";
  pipe_name = "/tmp/shairport-sync-metadata";
  pipe_timeout = 5000;
  cover_art_cache_directory = "/tmp/shairport-sync/.cache/coverart";
};

sessioncontrol =
{
  wait_for_completion = "yes";
};
`, name, alsaOutputDevice)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
