package main

import (
	"testing"

	"github.com/alemser/oceano-player/internal/recognition"
)

func TestManagerArgs_AlwaysIncludesBundledShazamPython(t *testing.T) {
	cfg := defaultConfig()
	// Path is fixed in managerArgs regardless of shazam_recognizer_enabled (state-manager
	// gates subprocess startup on recognition.providers).
	cfg.Recognition.ShazamioRecognizerEnabled = false
	args := managerArgs(cfg, "/etc/oceano/config.json")
	var shazamArg string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--shazam-python" {
			shazamArg = args[i+1]
			break
		}
	}
	if shazamArg != recognition.BundledShazamioPythonBin {
		t.Fatalf("--shazam-python = %q, want %q", shazamArg, recognition.BundledShazamioPythonBin)
	}
}
