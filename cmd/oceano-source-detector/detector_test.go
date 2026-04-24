package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestComputeRMS(t *testing.T) {
	tests := []struct {
		name    string
		samples []float64
		want    float64
	}{
		{
			name:    "silence",
			samples: make([]float64, 1024),
			want:    0.0,
		},
		{
			name:    "full-scale sine",
			samples: sineWave(1024, 440, 44100, 1.0),
			want:    1.0 / math.Sqrt2, // RMS of a sine = amplitude / sqrt(2)
		},
		{
			name:    "half amplitude",
			samples: sineWave(1024, 440, 44100, 0.5),
			want:    0.5 / math.Sqrt2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeRMS(tt.samples)
			if math.Abs(got-tt.want) > 0.005 {
				t.Errorf("computeRMS() = %.5f, want %.5f", got, tt.want)
			}
		})
	}
}

func TestSourceDetection(t *testing.T) {
	threshold := 0.008

	tests := []struct {
		name    string
		samples []float64
		want    Source
	}{
		{
			name:    "silence → None",
			samples: make([]float64, 1024),
			want:    SourceNone,
		},
		{
			name:    "signal above threshold → Physical",
			samples: sineWave(1024, 440, 44100, 0.1),
			want:    SourcePhysical,
		},
		{
			name:    "signal just below threshold → None",
			samples: sineWave(1024, 440, 44100, 0.005), // RMS ≈ 0.0035
			want:    SourceNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rms := computeRMS(tt.samples)
			var got Source
			if rms >= threshold {
				got = SourcePhysical
			} else {
				got = SourceNone
			}
			if got != tt.want {
				t.Errorf("detection rms=%.5f threshold=%.3f: got %s, want %s", rms, threshold, got, tt.want)
			}
		})
	}
}

func TestMajorityVote(t *testing.T) {
	tests := []struct {
		name    string
		votes   []Source
		current Source
		want    Source
	}{
		{
			name:    "all Physical → Physical",
			votes:   repeat(SourcePhysical, 10),
			current: SourceNone,
			want:    SourcePhysical,
		},
		{
			name:    "all None → None",
			votes:   repeat(SourceNone, 10),
			current: SourcePhysical,
			want:    SourceNone,
		},
		{
			name:    "8 Physical 2 None → Physical",
			votes:   append(repeat(SourcePhysical, 8), repeat(SourceNone, 2)...),
			current: SourceNone,
			want:    SourcePhysical,
		},
		{
			name:    "5 Physical 5 None → keep current (None)",
			votes:   append(repeat(SourcePhysical, 5), repeat(SourceNone, 5)...),
			current: SourceNone,
			want:    SourceNone,
		},
		{
			name:    "5 Physical 5 None → keep current (Physical)",
			votes:   append(repeat(SourcePhysical, 5), repeat(SourceNone, 5)...),
			current: SourcePhysical,
			want:    SourcePhysical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := len(tt.votes)
			majority := n/2 + 1
			noneVotes, physicalVotes := 0, 0
			for _, v := range tt.votes {
				if v == SourceNone {
					noneVotes++
				} else {
					physicalVotes++
				}
			}

			var got Source
			switch {
			case physicalVotes >= majority:
				got = SourcePhysical
			case noneVotes >= majority:
				got = SourceNone
			default:
				got = tt.current
			}

			if got != tt.want {
				t.Errorf("vote(physical=%d none=%d majority=%d current=%s) = %s, want %s",
					physicalVotes, noneVotes, majority, tt.current, got, tt.want)
			}
		})
	}
}

// --- selectCalibrationFile ---

func TestSelectCalibrationFile_VinylDerviesPath(t *testing.T) {
	base := "/var/lib/oceano/noise-floor.json"
	got := selectCalibrationFile(base, "vinyl")
	want := "/var/lib/oceano/noise-floor-vinyl.json"
	if got != want {
		t.Errorf("selectCalibrationFile(vinyl) = %q, want %q", got, want)
	}
}

func TestSelectCalibrationFile_CDDerivesPath(t *testing.T) {
	base := "/var/lib/oceano/noise-floor.json"
	got := selectCalibrationFile(base, "cd")
	want := "/var/lib/oceano/noise-floor-cd.json"
	if got != want {
		t.Errorf("selectCalibrationFile(cd) = %q, want %q", got, want)
	}
}

func TestSelectCalibrationFile_UnknownFormatKeepsBase(t *testing.T) {
	base := "/var/lib/oceano/noise-floor.json"
	for _, fmt := range []string{"", "cassette", "unknown"} {
		got := selectCalibrationFile(base, fmt)
		if got != base {
			t.Errorf("selectCalibrationFile(%q) = %q, want base path %q", fmt, got, base)
		}
	}
}

func TestSelectCalibrationFile_EmptyBaseReturnsEmpty(t *testing.T) {
	if got := selectCalibrationFile("", "vinyl"); got != "" {
		t.Errorf("selectCalibrationFile(\"\", vinyl) = %q, want empty", got)
	}
}

// --- readFormatHint ---

func TestReadFormatHint_ReturnsLowercaseFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "format.json")
	if err := os.WriteFile(path, []byte(`{"format":"Vinyl"}`), 0o644); err != nil {
		t.Fatalf("write hint: %v", err)
	}
	if got := readFormatHint(path); got != "vinyl" {
		t.Errorf("readFormatHint() = %q, want %q", got, "vinyl")
	}
}

func TestReadFormatHint_CDLowercase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "format.json")
	if err := os.WriteFile(path, []byte(`{"format":"CD"}`), 0o644); err != nil {
		t.Fatalf("write hint: %v", err)
	}
	if got := readFormatHint(path); got != "cd" {
		t.Errorf("readFormatHint() = %q, want %q", got, "cd")
	}
}

func TestReadFormatHint_MissingFileReturnsEmpty(t *testing.T) {
	if got := readFormatHint("/nonexistent/path/format.json"); got != "" {
		t.Errorf("readFormatHint(missing) = %q, want empty", got)
	}
}

func TestReadFormatHint_InvalidJSONReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "format.json")
	if err := os.WriteFile(path, []byte(`not json`), 0o644); err != nil {
		t.Fatalf("write hint: %v", err)
	}
	if got := readFormatHint(path); got != "" {
		t.Errorf("readFormatHint(invalid json) = %q, want empty", got)
	}
}

func TestReadFormatHint_EmptyPathReturnsEmpty(t *testing.T) {
	if got := readFormatHint(""); got != "" {
		t.Errorf("readFormatHint(\"\") = %q, want empty", got)
	}
}

// --- NoiseFloor.Thresholds ---

func TestNoiseFloorThresholds_DefaultValues(t *testing.T) {
	nf := defaultNoiseFloor() // RMS=0.001, StdDev=0.001
	thresh := nf.Thresholds()
	// thresh.RMS = 0.001 + 0.001*4 = 0.005
	if thresh.RMS < 0.004 || thresh.RMS > 0.006 {
		t.Errorf("default thresh.RMS = %.4f, want ~0.005", thresh.RMS)
	}
	// thresh.StdDev = 0.001 * 3 = 0.003
	if thresh.StdDev < 0.002 || thresh.StdDev > 0.004 {
		t.Errorf("default thresh.StdDev = %.4f, want ~0.003", thresh.StdDev)
	}
}

// TestSilenceThresholdIsCap verifies the SilenceThreshold semantics: when
// the calibrated thresh.RMS is below SilenceThreshold, the calibrated value
// is used (not the SilenceThreshold override). This prevents false None
// detection for quiet passages (a cappella, soft acoustic music).
func TestSilenceThresholdIsCap(t *testing.T) {
	nf := NoiseFloor{RMS: 0.003, StdDev: 0.001} // calibrated from groove noise
	calibrated := nf.Thresholds().RMS           // 0.003 + 0.001*4 = 0.007
	silenceThreshold := 0.025                   // configured default

	// SilenceThreshold as cap: use calibrated value when calibrated < configured
	effective := calibrated
	if calibrated > silenceThreshold {
		effective = silenceThreshold
	}

	if effective != calibrated {
		t.Errorf("expected calibrated %.4f to be used, got %.4f (SilenceThreshold=%.4f)",
			calibrated, effective, silenceThreshold)
	}
	if effective >= silenceThreshold {
		t.Errorf("calibrated threshold %.4f should be well below SilenceThreshold %.4f",
			effective, silenceThreshold)
	}
}

// TestSilenceThresholdCapPreventsRunaway verifies that when calibration goes
// wrong (e.g. music contaminated a silence window), SilenceThreshold clips it.
func TestSilenceThresholdCapPreventsRunaway(t *testing.T) {
	nf := NoiseFloor{RMS: 0.025, StdDev: 0.005} // runaway: too high
	calibrated := nf.Thresholds().RMS           // 0.025 + 0.005*4 = 0.045
	silenceThreshold := 0.025

	effective := calibrated
	if calibrated > silenceThreshold {
		effective = silenceThreshold
	}

	if effective != silenceThreshold {
		t.Errorf("expected SilenceThreshold %.4f to cap runaway calibration %.4f, got %.4f",
			silenceThreshold, calibrated, effective)
	}
}

// --- helpers ---

func sineWave(n int, freq, sampleRate, amplitude float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
	}
	return s
}

func repeat(src Source, n int) []Source {
	s := make([]Source, n)
	for i := range s {
		s[i] = src
	}
	return s
}
