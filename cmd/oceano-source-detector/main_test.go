package main

import (
	"math"
	"testing"
)

// TestClassify garante que a lógica básica de separação entre 
// Silêncio, Vinil (Grave) e CD (Agudo) funciona.
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
			name:     "Vinyl Detection (Low Freq)",
			samples:  generateSineWave(60, 44100, 4096, 0.1), // 60Hz (Rumble/Grave)
			expected: SourceVinyl,
		},
		{
			name:     "CD Detection (High Freq)",
			samples:  generateSineWave(5000, 44100, 4096, 0.1), // 5kHz (Agudo)
			expected: SourceCD,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := classify(tt.samples, cfg)
			if got != tt.expected {
				t.Errorf("%s: classify() = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

// TestHysteresis valida a "teimosia" que evita pulos de Vinil para CD 
// quando a música está alta.
func TestHysteresis(t *testing.T) {
	cfg := Config{
		SilenceThreshold: 0.0005,
		VinylThreshold:   0.15,
	}

	// Cenário: Estamos ouvindo Vinil, mas a música ficou muito aguda (Ratio baixo)
	current := SourceVinyl
	samples := generateSineWave(8000, 44100, 4096, 0.1) // 8kHz = Ratio baixo (CD)
	
	detected, rms := classify(samples, cfg)
	
	// Simula a lógica que colocamos na função run:
	if current == SourceVinyl && detected == SourceCD {
		if rms > (cfg.SilenceThreshold * 5) {
			detected = SourceVinyl
		}
	}

	if detected != SourceVinyl {
		t.Errorf("Hysteresis failed: should have kept Vinyl status due to high RMS, got %v", detected)
	}
}

// Helper para gerar ondas
func generateSineWave(freq, sampleRate float64, size int, amp float64) []float64 {
	s := make([]float64, size)
	for i := range s {
		s[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
	}
	return s
}