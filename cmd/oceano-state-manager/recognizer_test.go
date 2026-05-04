package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestWavPCMLevelStats_SineTone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tone.wav")
	if err := writeTestToneWAV(path, 44100, 2000, 1000.0); err != nil {
		t.Fatal(err)
	}
	mean, peak, err := wavPCMLevelStats(path)
	if err != nil {
		t.Fatalf("wavPCMLevelStats: %v", err)
	}
	if peak < 0.9 || peak > 1.01 {
		t.Fatalf("peak = %v, want ~1", peak)
	}
	if mean < 0.68 || mean > 0.73 {
		t.Fatalf("mean RMS = %v, want ~1/sqrt(2) for full-scale sine", mean)
	}
	_ = os.Remove(path)
}

func TestMaybeApplyRecognitionCaptureAutoGain_BoostsLowLevelCapture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quiet.wav")
	if err := writeTestToneWAVWithAmplitude(path, 44100, 2000, 440.0, 0.1); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := RecognitionCaptureAutoGainConfig{
		Enabled:   true,
		TargetRMS: 0.16,
		MinGain:   1.0,
		MaxGain:   2.5,
		PeakLimit: 0.98,
	}
	adjusted, tel, err := maybeApplyRecognitionCaptureAutoGain(raw, cfg)
	if err != nil {
		t.Fatalf("maybeApplyRecognitionCaptureAutoGain: %v", err)
	}
	if !tel.Applied {
		t.Fatalf("expected gain to be applied, telemetry=%+v", tel)
	}
	if tel.AfterRMS <= tel.BeforeRMS {
		t.Fatalf("expected after RMS > before RMS, got %.4f <= %.4f", tel.AfterRMS, tel.BeforeRMS)
	}
	if tel.AfterPeak > cfg.PeakLimit+0.01 {
		t.Fatalf("expected after peak <= peak limit, got %.4f", tel.AfterPeak)
	}
	if len(adjusted) != len(raw) {
		t.Fatalf("expected adjusted wav size preserved")
	}
}

func TestMaybeApplyRecognitionCaptureAutoGain_DisabledNoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quiet.wav")
	if err := writeTestToneWAVWithAmplitude(path, 44100, 2000, 440.0, 0.1); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := RecognitionCaptureAutoGainConfig{Enabled: false}
	adjusted, tel, err := maybeApplyRecognitionCaptureAutoGain(raw, cfg)
	if err != nil {
		t.Fatalf("maybeApplyRecognitionCaptureAutoGain: %v", err)
	}
	if tel.Applied {
		t.Fatalf("expected no gain application when disabled")
	}
	if !bytes.Equal(raw, adjusted) {
		t.Fatalf("expected unchanged bytes when disabled")
	}
}

// writeTestToneWAV writes a minimal mono 16-bit PCM WAV with a full-scale sine.
func writeTestToneWAV(path string, sampleRate, numSamples int, freqHz float64) error {
	return writeTestToneWAVWithAmplitude(path, sampleRate, numSamples, freqHz, 1.0)
}

func writeTestToneWAVWithAmplitude(path string, sampleRate, numSamples int, freqHz, amp float64) error {
	if amp < 0 {
		amp = 0
	}
	if amp > 1 {
		amp = 1
	}
	pcm := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		s := int16(32767.0 * amp * math.Sin(2*math.Pi*freqHz*float64(i)/float64(sampleRate)))
		binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(s))
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+len(pcm)))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&b, binary.LittleEndian, uint32(sampleRate*2))
	binary.Write(&b, binary.LittleEndian, uint16(2))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(pcm)))
	b.Write(pcm)
	return os.WriteFile(path, b.Bytes(), 0o644)
}
