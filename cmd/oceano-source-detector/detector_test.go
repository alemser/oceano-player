package main

import (
	"math"
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

func TestComputeRMSStats(t *testing.T) {
	tests := []struct {
		name       string
		values     []float64
		wantMean   float64
		wantStdDev float64
	}{
		{
			name:       "empty",
			values:     []float64{},
			wantMean:   0,
			wantStdDev: 0,
		},
		{
			name:       "single value",
			values:     []float64{0.01},
			wantMean:   0.01,
			wantStdDev: 0,
		},
		{
			name:       "constant values",
			values:     []float64{0.01, 0.01, 0.01, 0.01},
			wantMean:   0.01,
			wantStdDev: 0,
		},
		{
			name:       "two distinct values",
			values:     []float64{0.0, 0.02},
			wantMean:   0.01,
			wantStdDev: 0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mean, stddev := computeRMSStats(tt.values)
			if math.Abs(mean-tt.wantMean) > 0.0001 {
				t.Errorf("computeRMSStats() mean = %.5f, want %.5f", mean, tt.wantMean)
			}
			if math.Abs(stddev-tt.wantStdDev) > 0.0001 {
				t.Errorf("computeRMSStats() stddev = %.5f, want %.5f", stddev, tt.wantStdDev)
			}
		})
	}
}

func TestMedian(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   float64
	}{
		{
			name:   "empty",
			values: []float64{},
			want:   0,
		},
		{
			name:   "single",
			values: []float64{5.0},
			want:   5.0,
		},
		{
			name:   "odd count",
			values: []float64{1.0, 5.0, 3.0},
			want:   3.0,
		},
		{
			name:   "even count",
			values: []float64{1.0, 2.0, 3.0, 4.0},
			want:   2.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := median(tt.values)
			if math.Abs(got-tt.want) > 0.0001 {
				t.Errorf("median() = %.5f, want %.5f", got, tt.want)
			}
		})
	}
}

func TestNoiseFloorSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "noise-floor.json")

	nf := NoiseFloor{
		RMS:     0.0055,
		Stddev:  0.0012,
		Samples: 53,
	}

	if err := saveNoiseFloor(path, nf); err != nil {
		t.Fatalf("saveNoiseFloor() error: %v", err)
	}

	loaded, ok := loadNoiseFloor(path)
	if !ok {
		t.Fatal("loadNoiseFloor() returned false, expected true")
	}

	if math.Abs(loaded.RMS-nf.RMS) > 0.0001 {
		t.Errorf("loadNoiseFloor() RMS = %.5f, want %.5f", loaded.RMS, nf.RMS)
	}
	if math.Abs(loaded.Stddev-nf.Stddev) > 0.0001 {
		t.Errorf("loadNoiseFloor() Stddev = %.5f, want %.5f", loaded.Stddev, nf.Stddev)
	}
	if loaded.Samples != nf.Samples {
		t.Errorf("loadNoiseFloor() Samples = %d, want %d", loaded.Samples, nf.Samples)
	}
	if loaded.MeasuredAt == "" {
		t.Error("loadNoiseFloor() MeasuredAt is empty")
	}
}

func TestLoadNoiseFloorNotExists(t *testing.T) {
	_, ok := loadNoiseFloor("/nonexistent/path/noise-floor.json")
	if ok {
		t.Error("loadNoiseFloor() returned true for nonexistent file")
	}
}

func TestHybridDetection(t *testing.T) {
	tests := []struct {
		name            string
		rms             float64
		stddev          float64
		rmsThreshold    float64
		stddevThreshold float64
		want            Source
	}{
		{
			name:            "silence: low rms + low stddev → None",
			rms:             0.005,
			stddev:          0.001,
			rmsThreshold:    0.01,
			stddevThreshold: 0.005,
			want:            SourceNone,
		},
		{
			name:            "music: high rms → Physical",
			rms:             0.15,
			stddev:          0.01,
			rmsThreshold:    0.01,
			stddevThreshold: 0.005,
			want:            SourcePhysical,
		},
		{
			name:            "vinyl noise floor: moderate rms but low stddev → None",
			rms:             0.008,
			stddev:          0.0008,
			rmsThreshold:    0.01,
			stddevThreshold: 0.005,
			want:            SourceNone,
		},
		{
			name:            "dynamic music: low rms but high stddev → Physical",
			rms:             0.007,
			stddev:          0.01,
			rmsThreshold:    0.01,
			stddevThreshold: 0.005,
			want:            SourcePhysical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Source
			if tt.rms >= tt.rmsThreshold || tt.stddev >= tt.stddevThreshold {
				got = SourcePhysical
			} else {
				got = SourceNone
			}
			if got != tt.want {
				t.Errorf("hybrid detection (rms=%.4f stddev=%.4f thresholds=%.4f/%.4f) = %s, want %s",
					tt.rms, tt.stddev, tt.rmsThreshold, tt.stddevThreshold, got, tt.want)
			}
		})
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
