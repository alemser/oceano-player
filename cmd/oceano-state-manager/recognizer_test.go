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

// writeTestToneWAV writes a minimal mono 16-bit PCM WAV with a full-scale sine.
func writeTestToneWAV(path string, sampleRate, numSamples int, freqHz float64) error {
	pcm := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		s := int16(32767.0 * math.Sin(2*math.Pi*freqHz*float64(i)/float64(sampleRate)))
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
