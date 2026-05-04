package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// RecognitionCaptureAutoGainConfig controls optional low-level gain correction
// applied only to recognition WAV captures before providers are called.
// It does not affect source detection or VU boundary logic.
type RecognitionCaptureAutoGainConfig struct {
	Enabled   bool    `json:"enabled"`
	TargetRMS float64 `json:"target_rms"`
	MinGain   float64 `json:"min_gain"`
	MaxGain   float64 `json:"max_gain"`
	PeakLimit float64 `json:"peak_limit"`
}

func defaultRecognitionCaptureAutoGainConfig() RecognitionCaptureAutoGainConfig {
	return RecognitionCaptureAutoGainConfig{
		Enabled:   true,
		TargetRMS: 0.16,
		MinGain:   1.0,
		MaxGain:   2.5,
		PeakLimit: 0.98,
	}
}

func normalizeRecognitionCaptureAutoGainConfig(in RecognitionCaptureAutoGainConfig) RecognitionCaptureAutoGainConfig {
	out := in
	if out.TargetRMS <= 0 {
		out.TargetRMS = 0.16
	}
	if out.MinGain <= 0 {
		out.MinGain = 1.0
	}
	if out.MaxGain <= 0 {
		out.MaxGain = 2.5
	}
	if out.MinGain > out.MaxGain {
		out.MinGain, out.MaxGain = out.MaxGain, out.MinGain
	}
	if out.PeakLimit <= 0 || out.PeakLimit > 1 {
		out.PeakLimit = 0.98
	}
	return out
}

type captureAutoGainTelemetry struct {
	Applied    bool
	Gain       float64
	BeforeRMS  float64
	BeforePeak float64
	AfterRMS   float64
	AfterPeak  float64
	Clipped    int
}

func maybeApplyRecognitionCaptureAutoGain(wav []byte, cfg RecognitionCaptureAutoGainConfig) ([]byte, captureAutoGainTelemetry, error) {
	cfg = normalizeRecognitionCaptureAutoGainConfig(cfg)
	tel := captureAutoGainTelemetry{}
	if !cfg.Enabled {
		return wav, tel, nil
	}

	dataIdx := bytes.Index(wav, []byte("data"))
	if dataIdx < 0 || len(wav) < dataIdx+8 {
		return wav, tel, fmt.Errorf("wav: missing data chunk")
	}
	chunkSize := int(binary.LittleEndian.Uint32(wav[dataIdx+4 : dataIdx+8]))
	pcmStart := dataIdx + 8
	pcmEnd := pcmStart + chunkSize
	if pcmEnd > len(wav) {
		pcmEnd = len(wav)
	}
	if pcmEnd-pcmStart < 2 || (pcmEnd-pcmStart)%2 != 0 {
		return wav, tel, fmt.Errorf("wav: empty or odd pcm")
	}

	pcm := make([]byte, pcmEnd-pcmStart)
	copy(pcm, wav[pcmStart:pcmEnd])
	beforeRMS, beforePeak := pcmLevelStats(pcm)
	tel.BeforeRMS, tel.BeforePeak = beforeRMS, beforePeak
	if beforeRMS <= 0 {
		return wav, tel, nil
	}

	gain := cfg.TargetRMS / beforeRMS
	if gain < cfg.MinGain {
		gain = cfg.MinGain
	}
	if gain > cfg.MaxGain {
		gain = cfg.MaxGain
	}
	if beforePeak > 0 {
		peakGuardGain := cfg.PeakLimit / beforePeak
		if peakGuardGain < gain {
			gain = peakGuardGain
		}
	}
	if gain <= 0 || math.Abs(gain-1.0) < 0.02 {
		return wav, tel, nil
	}

	clipped := 0
	for i := 0; i < len(pcm); i += 2 {
		v := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		scaled := int(math.Round(float64(v) * gain))
		if scaled > math.MaxInt16 {
			scaled = math.MaxInt16
			clipped++
		} else if scaled < math.MinInt16 {
			scaled = math.MinInt16
			clipped++
		}
		binary.LittleEndian.PutUint16(pcm[i:i+2], uint16(int16(scaled)))
	}
	afterRMS, afterPeak := pcmLevelStats(pcm)
	tel.Applied = true
	tel.Gain = gain
	tel.AfterRMS = afterRMS
	tel.AfterPeak = afterPeak
	tel.Clipped = clipped

	out := make([]byte, len(wav))
	copy(out, wav)
	copy(out[pcmStart:pcmEnd], pcm)
	return out, tel, nil
}

func pcmLevelStats(pcm []byte) (rms, peak float64) {
	if len(pcm) < 2 {
		return 0, 0
	}
	samples := len(pcm) / 2
	var sumSq float64
	peak = 0
	for i := 0; i < samples; i++ {
		v := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		x := float64(v) / 32768.0
		ax := math.Abs(x)
		if ax > peak {
			peak = ax
		}
		sumSq += x * x
	}
	return math.Sqrt(sumSq / float64(samples)), peak
}
