package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

func r3FormatFilterForCalibration(preferredPhysicalFormat string) string {
	k := strings.ToLower(strings.TrimSpace(preferredPhysicalFormat))
	switch k {
	case "vinyl", "cd", "physical":
		return k
	default:
		return ""
	}
}

func clampFloat64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampDurationPessimismScalar(p float64) float64 {
	if p < 0.5 {
		return 0.5
	}
	if p > 0.98 {
		return 0.98
	}
	return p
}

// effectiveDurationPessimismForPhysicalPolicy returns cfg.DurationPessimism plus
// optional R3 telemetry delta (bounded).
func (m *mgr) effectiveDurationPessimismForPhysicalPolicy() float64 {
	m.mu.Lock()
	d := m.r3DurationPessimismDelta
	m.mu.Unlock()
	return clampDurationPessimismScalar(m.cfg.DurationPessimism + d)
}

// computeR3CalibrationNudges derives bounded additive adjustments from
// boundary_events follow-up telemetry (same_track_restored vs matched).
func computeR3CalibrationNudges(lib *internallibrary.Library, cfg r3TelemetryFileConfig, preferredPhysicalFormat string) (silenceDelta float32, pessimismDelta float64, summary string) {
	if lib == nil || !cfg.Enabled {
		return 0, 0, ""
	}

	def := defaultR3TelemetryFileConfig()
	lookback := cfg.LookbackDays
	if lookback <= 0 {
		lookback = def.LookbackDays
	}
	since := time.Now().Add(-time.Duration(lookback) * 24 * time.Hour)

	fmtKey := r3FormatFilterForCalibration(preferredPhysicalFormat)
	tel, err := lib.QueryR3BoundaryTelemetry(since, fmtKey)
	if err != nil {
		return 0, 0, ""
	}

	pairs := tel.SameTrackRestored + tel.Matched
	minP := cfg.MinFollowupPairs
	if minP <= 0 {
		minP = def.MinFollowupPairs
	}
	if pairs < minP {
		return 0, 0, fmt.Sprintf("insufficient pairs (%d < %d lookback=%dd)", pairs, minP, lookback)
	}

	fpRatio := float64(tel.SameTrackRestored) / float64(pairs)
	baseline := cfg.BaselineFalsePositiveRatio
	if baseline <= 0 {
		baseline = def.BaselineFalsePositiveRatio
	}
	maxSil := cfg.MaxSilenceThresholdDelta
	if maxSil <= 0 {
		maxSil = def.MaxSilenceThresholdDelta
	}
	scale := maxSil / 0.25
	if scale <= 0 {
		scale = maxSil
	}
	rawSilence := (fpRatio - baseline) * scale
	silence := clampFloat64(rawSilence, -maxSil, maxSil)

	maxPess := cfg.MaxDurationPessimismDelta
	if maxPess <= 0 {
		maxPess = def.MaxDurationPessimismDelta
	}
	rawPess := math.Max(0, fpRatio-baseline) * (maxPess / 0.25)
	pessimism := math.Min(maxPess, rawPess)

	thP75 := cfg.EarlyTrackProgressP75Threshold
	if thP75 <= 0 {
		thP75 = def.EarlyTrackProgressP75Threshold
	}
	extras := cfg.EarlyTrackExtraSilenceDelta
	if extras <= 0 {
		extras = def.EarlyTrackExtraSilenceDelta
	}
	nFrac := len(tel.MatchedSeekFractions)
	p75 := 0.0
	if nFrac > 0 {
		p75 = internallibrary.PercentileSorted(tel.MatchedSeekFractions, 0.75)
	}
	if nFrac >= 10 && p75 > 0 && p75 < thP75 {
		silence = clampFloat64(silence+extras, -maxSil, maxSil)
	}

	summary = fmt.Sprintf(
		"pairs=%d fp_ratio=%.3f baseline=%.3f silence_delta=%.5f pessimism_delta=%.3f format=%q p75_progress=%.3f n_matched_frac=%d",
		pairs, fpRatio, baseline, silence, pessimism, fmtKey, p75, nFrac,
	)
	return float32(silence), pessimism, summary
}
