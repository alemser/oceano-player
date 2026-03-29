package main

import (
	"math"
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
