package main

import (
	"math"
	"testing"
)

// TestClassify ensures the basic separation between Silence, Vinyl (low freq)
// and CD (high freq) works correctly.
func TestClassify(t *testing.T) {
	cfg := Config{
		SilenceThreshold: 0.0005,
		VinylThreshold:   0.15,
		SampleRate:       44100,
		BufferSize:       4096,
	}

	tests := []struct {
		name     string
		samples  []float64
		expected Source
	}{
		{
			name:     "Silence Detection",
			samples:  make([]float64, 4096),
			expected: SourceNone,
		},
		{
			name:     "Vinyl Detection (Low Freq rumble at 60Hz)",
			samples:  generateSineWave(60, 44100, 4096, 0.1),
			expected: SourceVinyl,
		},
		{
			name:     "CD Detection (High Freq at 5kHz)",
			samples:  generateSineWave(5000, 44100, 4096, 0.1),
			expected: SourceCD,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, _ := classify(tt.samples, cfg)
			if got != tt.expected {
				t.Errorf("classify() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestHysteresis validates that transitions between Vinyl and CD are resisted
// near the threshold boundary using applyHysteresis().
func TestHysteresis(t *testing.T) {
	cfg := Config{
		SilenceThreshold: 0.0005,
		VinylThreshold:   0.15,
		SampleRate:       44100,
		BufferSize:       4096,
	}
	margin := cfg.VinylThreshold * 0.5

	tests := []struct {
		name     string
		detected Source
		current  Source
		ratio    float64
		expected Source
	}{
		{
			name:     "Vinyl holds when ratio is near threshold (Vinyl->CD resisted)",
			detected: SourceCD,
			current:  SourceVinyl,
			ratio:    cfg.VinylThreshold - (margin * 0.5), // inside dead band
			expected: SourceVinyl,
		},
		{
			name:     "Vinyl flips to CD when ratio drops well below threshold",
			detected: SourceCD,
			current:  SourceVinyl,
			ratio:    cfg.VinylThreshold - margin - 0.01, // outside dead band
			expected: SourceCD,
		},
		{
			name:     "CD holds when ratio is near threshold (CD->Vinyl resisted)",
			detected: SourceVinyl,
			current:  SourceCD,
			ratio:    cfg.VinylThreshold + (margin * 0.5), // inside dead band
			expected: SourceCD,
		},
		{
			name:     "CD flips to Vinyl when ratio rises well above threshold",
			detected: SourceVinyl,
			current:  SourceCD,
			ratio:    cfg.VinylThreshold + margin + 0.01, // outside dead band
			expected: SourceVinyl,
		},
		{
			name:     "None is never blocked by hysteresis",
			detected: SourceNone,
			current:  SourceVinyl,
			ratio:    0,
			expected: SourceNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyHysteresis(tt.detected, tt.current, 0.1, tt.ratio, cfg, margin)
			if got != tt.expected {
				t.Errorf("applyHysteresis() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestComputeRMS validates the RMS calculation on known inputs.
func TestComputeRMS(t *testing.T) {
	// A sine wave of amplitude A has RMS = A / sqrt(2)
	samples := generateSineWave(1000, 44100, 4096, 1.0)
	rms := computeRMS(samples)
	expected := 1.0 / math.Sqrt2

	if math.Abs(rms-expected) > 0.01 {
		t.Errorf("computeRMS() = %.4f, want ~%.4f", rms, expected)
	}
}

// TestLowFrequencyRatio validates that a low-freq signal produces a high ratio
// and a high-freq signal produces a low ratio.
func TestLowFrequencyRatio(t *testing.T) {
	sampleRate := 44100
	bufferSize := 4096

	lowSpectrum := fft(generateSineWave(60, float64(sampleRate), bufferSize, 0.1))
	lowRatio := lowFrequencyRatio(lowSpectrum, sampleRate, bufferSize)

	highSpectrum := fft(generateSineWave(5000, float64(sampleRate), bufferSize, 0.1))
	highRatio := lowFrequencyRatio(highSpectrum, sampleRate, bufferSize)

	if lowRatio <= highRatio {
		t.Errorf("expected low-freq ratio (%.4f) > high-freq ratio (%.4f)", lowRatio, highRatio)
	}
}

// generateSineWave generates a sine wave at the given frequency.
func generateSineWave(freq, sampleRate float64, size int, amp float64) []float64 {
	s := make([]float64, size)
	for i := range s {
		s[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
	}
	return s
}