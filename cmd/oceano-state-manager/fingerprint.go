package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// Fingerprinter generates an acoustic fingerprint from a WAV file.
// Implementations must be safe for concurrent use.
type Fingerprinter interface {
	// Fingerprint returns a Chromaprint fingerprint string for the given WAV file.
	// Returns an error if the fingerprint cannot be generated.
	Fingerprint(wavPath string) (string, error)
}

// FpcalcFingerprinter uses the fpcalc binary (part of libchromaprint-tools)
// to generate acoustic fingerprints. fpcalc is a required runtime dependency
// for physical-media recognition.
type FpcalcFingerprinter struct {
	binaryPath string // path to fpcalc binary; use "fpcalc" explicitly for PATH lookup
}

// NewFpcalcFingerprinter creates a fingerprinter that calls the fpcalc binary at
// binaryPath. Startup should pass "fpcalc" explicitly so the binary is resolved
// from PATH on installed systems.
func NewFpcalcFingerprinter(binaryPath string) *FpcalcFingerprinter {
	return &FpcalcFingerprinter{binaryPath: binaryPath}
}

// Fingerprint runs fpcalc on wavPath and returns the FINGERPRINT value.
func (f *FpcalcFingerprinter) Fingerprint(wavPath string) (string, error) {
	if f.binaryPath == "" {
		return "", fmt.Errorf("fpcalc: binary path is empty")
	}
	out, err := exec.Command(f.binaryPath, wavPath).CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		if output != "" {
			return "", fmt.Errorf("fpcalc: %w: %s", err, output)
		}
		return "", fmt.Errorf("fpcalc: %w", err)
	}
	return parseFpcalcOutput(string(out))
}

// parseFpcalcOutput extracts the FINGERPRINT value from fpcalc output.
//
// fpcalc output format:
//
//	DURATION=240
//	FINGERPRINT=AQADtJm...
func parseFpcalcOutput(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "FINGERPRINT="); ok {
			fp := strings.TrimSpace(after)
			if fp != "" {
				return fp, nil
			}
		}
	}
	return "", fmt.Errorf("fpcalc: FINGERPRINT not found in output")
}
