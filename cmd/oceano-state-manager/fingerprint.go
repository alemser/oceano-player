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
// to generate acoustic fingerprints.
type FpcalcFingerprinter struct {
	binaryPath string // path to fpcalc binary; defaults to "fpcalc" (PATH lookup)
}

// NewFpcalcFingerprinter creates a fingerprinter that calls the fpcalc binary at
// binaryPath. Pass an empty string to search for "fpcalc" in PATH.
func NewFpcalcFingerprinter(binaryPath string) *FpcalcFingerprinter {
	if binaryPath == "" {
		binaryPath = "fpcalc"
	}
	return &FpcalcFingerprinter{binaryPath: binaryPath}
}

// Fingerprint runs fpcalc on wavPath and returns the FINGERPRINT value.
func (f *FpcalcFingerprinter) Fingerprint(wavPath string) (string, error) {
	out, err := exec.Command(f.binaryPath, wavPath).Output()
	if err != nil {
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
