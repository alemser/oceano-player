package main

import (
	"log"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

// applyPersistedRMSLearningToDetector overrides silence enter/exit from the
// library histogram when autonomous_apply is on and sample counts are sufficient.
func applyPersistedRMSLearningToDetector(lib *internallibrary.Library, cfg rmsLearningRuntimeConfig, floor float32, calFormat string, detCfg *vuBoundaryDetectorConfig) {
	if lib == nil || detCfg == nil || !cfg.Enabled || !cfg.AutonomousApply {
		return
	}
	key := internallibrary.NormalizeRMSLearningFormatKey(calFormat)
	st, err := lib.LoadRMSLearning(key)
	if err != nil || st == nil {
		return
	}
	if int(st.SilenceTotal) < cfg.MinSilenceSamples || int(st.MusicTotal) < cfg.MinMusicSamples {
		return
	}
	e, x, ok := internallibrary.DeriveSilenceThresholdsFromHistograms(
		st.Silence, st.Music, st.SilenceTotal, st.MusicTotal, st.MaxRMS)
	if !ok {
		return
	}
	e2, x2 := clampSilenceThresholdsToFloor(e, x, floor)
	detCfg.silenceEnterThreshold = e2
	detCfg.silenceExitThreshold = x2
	log.Printf("VU monitor: RMS percentile learning (persisted) for %q enter=%.4f exit=%.4f (sil_n=%d mus_n=%d)",
		key, e2, x2, st.SilenceTotal, st.MusicTotal)
}

type rmsPercentileLearner struct {
	cfg           rmsLearningRuntimeConfig
	lib           *internallibrary.Library
	silenceNudge  float32
	byFormat      map[string]*internallibrary.RMSLearningState
	dirty         map[string]struct{}
	lastFlush     time.Time
	lastLiveLog   time.Time
}

func newRMSPercentileLearner(lib *internallibrary.Library, cfg rmsLearningRuntimeConfig, silenceNudge float32) *rmsPercentileLearner {
	return &rmsPercentileLearner{
		cfg:          cfg,
		lib:          lib,
		silenceNudge: silenceNudge,
		byFormat:     make(map[string]*internallibrary.RMSLearningState),
		dirty:        make(map[string]struct{}),
	}
}

func (lr *rmsPercentileLearner) stateFor(key string) *internallibrary.RMSLearningState {
	if st := lr.byFormat[key]; st != nil {
		return st
	}
	var st *internallibrary.RMSLearningState
	if lr.lib != nil {
		loaded, err := lr.lib.LoadRMSLearning(key)
		if err != nil || loaded == nil {
			st = internallibrary.NewRMSLearningHistogramState(key, lr.cfg.Bins, lr.cfg.MaxRMS)
		} else if loaded.Bins != lr.cfg.Bins || loaded.MaxRMS != lr.cfg.MaxRMS {
			st = internallibrary.NewRMSLearningHistogramState(key, lr.cfg.Bins, lr.cfg.MaxRMS)
		} else {
			st = loaded
		}
	} else {
		st = internallibrary.NewRMSLearningHistogramState(key, lr.cfg.Bins, lr.cfg.MaxRMS)
	}
	lr.byFormat[key] = st
	return st
}

func (lr *rmsPercentileLearner) observe(m *mgr, avg float32, out vuBoundaryOutcome, floorThresh float32, det *vuBoundaryDetector) {
	if lr == nil || lr.lib == nil || !lr.cfg.Enabled {
		return
	}
	m.mu.Lock()
	phy := m.physicalSource == "Physical"
	m.mu.Unlock()
	if !phy {
		return
	}
	key := internallibrary.NormalizeRMSLearningFormatKey(m.currentPhysicalFormatForCalibration())
	st := lr.stateFor(key)
	bins := lr.cfg.Bins
	maxR := lr.cfg.MaxRMS
	if out.inSilence && out.silenceElapsed >= 1500*time.Millisecond {
		i := internallibrary.RMSHistogramBin(avg, maxR, bins)
		st.Silence[i]++
		st.SilenceTotal++
		lr.dirty[key] = struct{}{}
	}
	if !out.inSilence && avg >= floorThresh*1.04 {
		i := internallibrary.RMSHistogramBin(avg, maxR, bins)
		st.Music[i]++
		st.MusicTotal++
		lr.dirty[key] = struct{}{}
	}
	if lr.lastFlush.IsZero() {
		lr.lastFlush = time.Now()
	}
	if time.Since(lr.lastFlush) < lr.cfg.PersistInterval {
		return
	}
	lr.flush(det, floorThresh, key)
	lr.lastFlush = time.Now()
}

func (lr *rmsPercentileLearner) flush(det *vuBoundaryDetector, floor float32, currentKey string) {
	if lr.lib == nil {
		return
	}
	for k := range lr.dirty {
		st := lr.byFormat[k]
		if st != nil {
			if err := lr.lib.SaveRMSLearning(st); err != nil {
				log.Printf("rms learning: save %q: %v", k, err)
			}
		}
	}
	lr.dirty = make(map[string]struct{})

	if !lr.cfg.AutonomousApply || det == nil {
		return
	}
	k := internallibrary.NormalizeRMSLearningFormatKey(currentKey)
	st := lr.byFormat[k]
	if st == nil {
		return
	}
	if int(st.SilenceTotal) < lr.cfg.MinSilenceSamples || int(st.MusicTotal) < lr.cfg.MinMusicSamples {
		return
	}
	e, x, ok := internallibrary.DeriveSilenceThresholdsFromHistograms(
		st.Silence, st.Music, st.SilenceTotal, st.MusicTotal, st.MaxRMS)
	if !ok {
		return
	}
	e2, x2 := clampSilenceThresholdsToFloor(e, x, floor)
	if lr.silenceNudge != 0 {
		e2 += lr.silenceNudge
		x2 += lr.silenceNudge
		e2, x2 = clampSilenceThresholdsToFloor(e2, x2, floor)
	}
	det.SetSilenceEnterExit(e2, x2)
	if time.Since(lr.lastLiveLog) > 45*time.Second {
		log.Printf("VU monitor: RMS percentile live update for %q enter=%.4f exit=%.4f (sil_n=%d mus_n=%d)",
			k, e2, x2, st.SilenceTotal, st.MusicTotal)
		lr.lastLiveLog = time.Now()
	}
}
