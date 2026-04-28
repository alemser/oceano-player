package main

import "time"

type vuBoundaryDetector struct {
	cfg vuBoundaryDetectorConfig

	silenceCount int
	activeCount  int
	inSilence    bool

	hadHardSilence bool
	silenceStarted time.Time

	slowEMA, fastEMA  float32
	energyFrameCount  int
	dipCount          int
	hadDip            bool
	dipMin            float32
	lastEnergyTrigger time.Time
}

type vuBoundaryDetectorConfig struct {
	silenceThreshold      float32
	silenceEnterThreshold float32
	silenceExitThreshold  float32
	silenceFrames         int
	activeFrames          int

	hardSilenceFrames int

	energySlowAlpha      float32
	energyFastAlpha      float32
	energyDipRatio       float32
	energyRecoverRatio   float32
	energyDipMinFrames   int
	energyDipMaxFrames   int
	energyWarmupFrames   int
	energyChangeCooldown time.Duration

	transitionGapRMS      float32
	transitionMinMusicRMS float32

	durationBypassSilenceFrames int
	durationBypassEnergyFrames  int
}

type vuBoundaryOutcome struct {
	enteredSilence     bool
	resumedFromSilence bool
	inSilence          bool
	silenceElapsed     time.Duration

	armDurationBypass bool

	boundary                   bool
	boundaryHard               bool
	boundaryType               string
	energySuppressedByCooldown bool
}

func defaultVUBoundaryDetectorConfig(silenceThreshold float32, silenceFrames, activeFrames int) vuBoundaryDetectorConfig {
	const hardSilenceFrames = 40
	const energyDipMinFrames = 32
	return vuBoundaryDetectorConfig{
		silenceThreshold:      silenceThreshold,
		silenceEnterThreshold: silenceThreshold,
		silenceExitThreshold:  silenceThreshold,
		silenceFrames:         silenceFrames,
		activeFrames:          activeFrames,

		hardSilenceFrames: hardSilenceFrames,

		energySlowAlpha:      float32(0.005),
		energyFastAlpha:      float32(0.15),
		energyDipRatio:       float32(0.45),
		energyRecoverRatio:   float32(0.75),
		energyDipMinFrames:   energyDipMinFrames,
		energyDipMaxFrames:   energyDipMinFrames * 4,
		energyWarmupFrames:   200,
		energyChangeCooldown: 30 * time.Second,

		durationBypassSilenceFrames: hardSilenceFrames,
		durationBypassEnergyFrames:  energyDipMinFrames,
	}
}

func newVUBoundaryDetector(cfg vuBoundaryDetectorConfig) *vuBoundaryDetector {
	return &vuBoundaryDetector{cfg: cfg}
}

// SetSilenceEnterExit updates live silence thresholds (RMS percentile learning)
// without resetting internal silence state.
func (d *vuBoundaryDetector) SetSilenceEnterExit(enter, exit float32) {
	if d == nil || enter <= 0 {
		return
	}
	if exit <= enter {
		exit = enter + 0.0005
	}
	d.cfg.silenceEnterThreshold = enter
	d.cfg.silenceExitThreshold = exit
}

func (d *vuBoundaryDetector) Feed(avg float32, now time.Time) vuBoundaryOutcome {
	out := vuBoundaryOutcome{}
	silenceThreshold := d.cfg.silenceEnterThreshold
	if d.inSilence {
		silenceThreshold = d.cfg.silenceExitThreshold
	}
	if silenceThreshold <= 0 {
		silenceThreshold = d.cfg.silenceThreshold
	}

	if avg < silenceThreshold {
		if d.silenceCount == 0 {
			d.silenceStarted = now
		}
		d.silenceCount++
		d.activeCount = 0
		if d.silenceCount >= d.cfg.hardSilenceFrames {
			d.hadHardSilence = true
		}
		if d.silenceCount == d.cfg.durationBypassSilenceFrames {
			out.armDurationBypass = true
		}
		if d.silenceCount >= d.cfg.silenceFrames && !d.inSilence {
			d.inSilence = true
			out.enteredSilence = true
			// Freeze energy model during silence; restart on resumption.
			d.energyFrameCount = 0
			d.dipCount = 0
			d.hadDip = false
			d.dipMin = 0
		}
		out.inSilence = d.inSilence
		if !d.silenceStarted.IsZero() {
			out.silenceElapsed = now.Sub(d.silenceStarted)
		}
		return out
	}

	d.activeCount++
	d.silenceCount = 0
	d.silenceStarted = time.Time{}

	if d.inSilence && d.activeCount >= d.cfg.activeFrames {
		d.inSilence = false
		out.resumedFromSilence = true
		out.boundary = true
		out.boundaryHard = d.hadHardSilence
		out.boundaryType = "silence->audio"
		d.hadHardSilence = false
		d.lastEnergyTrigger = now
	}
	out.inSilence = d.inSilence

	if d.inSilence {
		return out
	}

	d.energyFrameCount++
	if d.energyFrameCount == 1 {
		d.slowEMA = avg
		d.fastEMA = avg
	} else {
		d.slowEMA = d.cfg.energySlowAlpha*avg + (1-d.cfg.energySlowAlpha)*d.slowEMA
		d.fastEMA = d.cfg.energyFastAlpha*avg + (1-d.cfg.energyFastAlpha)*d.fastEMA
	}

	if d.energyFrameCount < d.cfg.energyWarmupFrames {
		return out
	}

	dipThreshold := d.slowEMA * d.cfg.energyDipRatio
	if d.cfg.transitionGapRMS > 0 {
		transitionDipThreshold := d.cfg.transitionGapRMS * 1.22
		if transitionDipThreshold > dipThreshold {
			dipThreshold = transitionDipThreshold
		}
	}

	if d.fastEMA < dipThreshold {
		d.dipCount++
		if d.dipMin == 0 || avg < d.dipMin {
			d.dipMin = avg
		}
		if d.dipCount == d.cfg.durationBypassEnergyFrames {
			out.armDurationBypass = true
		}
		if d.cfg.energyDipMaxFrames > 0 && d.dipCount > d.cfg.energyDipMaxFrames {
			d.hadDip = false
			return out
		}
		if d.dipCount >= d.cfg.energyDipMinFrames && !d.hadDip {
			d.hadDip = true
		}
		return out
	}

	recoveryFloor := d.slowEMA * d.cfg.energyRecoverRatio
	if d.cfg.transitionGapRMS > 0 {
		calibratedRecover := d.cfg.transitionGapRMS * 1.35
		if d.cfg.transitionMinMusicRMS > d.cfg.transitionGapRMS {
			calibratedRecover = d.cfg.transitionGapRMS + (d.cfg.transitionMinMusicRMS-d.cfg.transitionGapRMS)*0.25
		}
		if calibratedRecover > recoveryFloor {
			recoveryFloor = calibratedRecover
		}
	}

	isRecovering := d.fastEMA > recoveryFloor
	if d.hadDip && isRecovering {
		validDip := true
		if d.cfg.transitionGapRMS > 0 && d.dipMin > 0 {
			validDip = d.dipMin <= d.cfg.transitionGapRMS*1.28
		}
		if d.cfg.energyDipMaxFrames > 0 {
			validDip = validDip && d.dipCount <= d.cfg.energyDipMaxFrames
		}
		d.hadDip = false
		d.dipCount = 0
		d.dipMin = 0
		if validDip && now.Sub(d.lastEnergyTrigger) >= d.cfg.energyChangeCooldown {
			d.lastEnergyTrigger = now
			d.energyFrameCount = 0 // restart model for the new track
			out.boundary = true
			out.boundaryHard = false
			out.boundaryType = "energy-change"
		} else {
			out.energySuppressedByCooldown = true
		}
		return out
	}

	d.dipCount = 0
	d.dipMin = 0

	return out
}
