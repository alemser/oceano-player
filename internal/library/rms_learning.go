package library

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// RMS learning uses fixed-width histograms over [0, MaxRMS) for stable storage.
const (
	DefaultRMSLearningBins   = 80
	DefaultRMSLearningMaxRMS = 0.25
)

// RMSLearningState holds histogram counts for one physical-media format key
// (e.g. "vinyl", "cd", "physical"). Used by the state manager to learn silence vs
// music RMS distributions over time.
type RMSLearningState struct {
	FormatKey    string
	Bins         int
	MaxRMS       float32
	Silence      []uint64
	Music        []uint64
	SilenceTotal uint64
	MusicTotal   uint64
	DerivedEnter float32
	DerivedExit  float32
	UpdatedAt    time.Time
}

// NewRMSLearningHistogramState returns an empty histogram state for the given
// dimensions (bins × max RMS range).
func NewRMSLearningHistogramState(formatKey string, bins int, maxRMS float32) *RMSLearningState {
	s := &RMSLearningState{
		FormatKey: NormalizeRMSLearningFormatKey(formatKey),
		Bins:      bins,
		MaxRMS:    maxRMS,
	}
	if s.Bins <= 0 {
		s.Bins = DefaultRMSLearningBins
	}
	if s.MaxRMS <= 0 {
		s.MaxRMS = DefaultRMSLearningMaxRMS
	}
	s.ensureSlices()
	return s
}

// NormalizeRMSLearningFormatKey maps UI / state format labels to DB keys.
func NormalizeRMSLearningFormatKey(k string) string {
	s := strings.ToLower(strings.TrimSpace(k))
	if s == "" {
		return "physical"
	}
	switch s {
	case "vinyl", "cd", "physical":
		return s
	default:
		return "physical"
	}
}

func (s *RMSLearningState) ensureSlices() {
	if s.Bins <= 0 {
		s.Bins = DefaultRMSLearningBins
	}
	if s.MaxRMS <= 0 {
		s.MaxRMS = DefaultRMSLearningMaxRMS
	}
	if len(s.Silence) != s.Bins {
		s.Silence = make([]uint64, s.Bins)
	}
	if len(s.Music) != s.Bins {
		s.Music = make([]uint64, s.Bins)
	}
}

// RMSHistogramBin maps avg RMS into [0, bins).
func RMSHistogramBin(avg, maxRMS float32, bins int) int {
	if bins <= 0 {
		return 0
	}
	if avg < 0 {
		avg = 0
	}
	if maxRMS <= 0 {
		maxRMS = DefaultRMSLearningMaxRMS
	}
	if avg >= maxRMS {
		return bins - 1
	}
	idx := int(float64(avg) / float64(maxRMS) * float64(bins))
	if idx < 0 {
		return 0
	}
	if idx >= bins {
		return bins - 1
	}
	return idx
}

// RMSHistogramPercentile returns the approximate RMS at cumulative quantile p in [0,1].
func RMSHistogramPercentile(counts []uint64, total uint64, p float64, maxRMS float32) float32 {
	if total == 0 || len(counts) == 0 || maxRMS <= 0 {
		return 0
	}
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return maxRMS * (1 - 0.5/float32(len(counts)))
	}
	target := p * float64(total)
	var acc uint64
	for i, c := range counts {
		acc += c
		if float64(acc) >= target {
			lo := float32(i) / float32(len(counts)) * maxRMS
			hi := float32(i+1) / float32(len(counts)) * maxRMS
			return (lo + hi) / 2
		}
	}
	return maxRMS * 0.99
}

const minRMSLearningSeparation = float32(0.0035)

// DeriveSilenceThresholdsFromHistograms maps learned silence/music histograms to
// VU silence enter/exit when the distributions separate (ok=false when not).
func DeriveSilenceThresholdsFromHistograms(sil, mus []uint64, silN, musN uint64, maxRMS float32) (enter, exit float32, ok bool) {
	if silN == 0 || musN == 0 || len(sil) == 0 || len(mus) == 0 || len(sil) != len(mus) {
		return 0, 0, false
	}
	s90 := RMSHistogramPercentile(sil, silN, 0.90, maxRMS)
	m10 := RMSHistogramPercentile(mus, musN, 0.10, maxRMS)
	if m10 <= s90+minRMSLearningSeparation {
		return 0, 0, false
	}
	gap := m10 - s90
	enter = s90 + 0.28*gap
	exit = enter + float32(math.Max(0.0006, float64(0.38*gap)))
	const minEnter = float32(0.001)
	if enter < minEnter {
		enter = minEnter
	}
	if exit <= enter {
		exit = enter + 0.0005
	}
	if exit >= maxRMS*0.98 {
		return 0, 0, false
	}
	return enter, exit, true
}

// LoadRMSLearning reads persisted histograms for formatKey (normalized).
func (l *Library) LoadRMSLearning(formatKey string) (*RMSLearningState, error) {
	if l == nil || l.db == nil {
		return &RMSLearningState{}, nil
	}
	key := NormalizeRMSLearningFormatKey(formatKey)
	var bins int
	var maxR float64
	var silJSON, musJSON string
	var silTot, musTot int64
	var derEnter, derExit sql.NullFloat64
	var updated string
	err := l.db.QueryRow(`
		SELECT bins, max_rms, silence_counts, music_counts, silence_total, music_total,
		       derived_enter, derived_exit, updated_at
		FROM rms_learning WHERE format_key = ?`, key).Scan(
		&bins, &maxR, &silJSON, &musJSON, &silTot, &musTot, &derEnter, &derExit, &updated)
	if err == sql.ErrNoRows {
		st := &RMSLearningState{FormatKey: key, Bins: DefaultRMSLearningBins, MaxRMS: DefaultRMSLearningMaxRMS}
		st.ensureSlices()
		return st, nil
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			st := &RMSLearningState{FormatKey: key, Bins: DefaultRMSLearningBins, MaxRMS: DefaultRMSLearningMaxRMS}
			st.ensureSlices()
			return st, nil
		}
		return nil, fmt.Errorf("library: LoadRMSLearning: %w", err)
	}
	if bins <= 0 {
		bins = DefaultRMSLearningBins
	}
	maxRF := float32(maxR)
	if maxRF <= 0 {
		maxRF = DefaultRMSLearningMaxRMS
	}
	var sil, mus []uint64
	if silJSON != "" {
		_ = json.Unmarshal([]byte(silJSON), &sil)
	}
	if musJSON != "" {
		_ = json.Unmarshal([]byte(musJSON), &mus)
	}
	st := &RMSLearningState{
		FormatKey:    key,
		Bins:         bins,
		MaxRMS:       maxRF,
		Silence:      sil,
		Music:        mus,
		SilenceTotal: uint64(silTot),
		MusicTotal:   uint64(musTot),
	}
	st.ensureSlices()
	for i := range st.Silence {
		if i < len(sil) {
			st.Silence[i] = sil[i]
		}
		if i < len(mus) {
			st.Music[i] = mus[i]
		}
	}
	if derEnter.Valid {
		st.DerivedEnter = float32(derEnter.Float64)
	}
	if derExit.Valid {
		st.DerivedExit = float32(derExit.Float64)
	}
	if updated != "" {
		if t, e := time.Parse(time.RFC3339Nano, updated); e == nil {
			st.UpdatedAt = t
		}
	}
	return st, nil
}

// SaveRMSLearning persists histograms and derived thresholds for one format.
func (l *Library) SaveRMSLearning(st *RMSLearningState) error {
	if l == nil || l.db == nil || st == nil {
		return nil
	}
	st.ensureSlices()
	key := NormalizeRMSLearningFormatKey(st.FormatKey)
	silB, err := json.Marshal(st.Silence)
	if err != nil {
		return err
	}
	musB, err := json.Marshal(st.Music)
	if err != nil {
		return err
	}
	enter, exit, ok := DeriveSilenceThresholdsFromHistograms(st.Silence, st.Music, st.SilenceTotal, st.MusicTotal, st.MaxRMS)
	if ok {
		st.DerivedEnter, st.DerivedExit = enter, exit
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = l.db.Exec(`
		INSERT INTO rms_learning (
			format_key, updated_at, bins, max_rms, silence_counts, music_counts,
			silence_total, music_total, derived_enter, derived_exit
		) VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(format_key) DO UPDATE SET
			updated_at=excluded.updated_at,
			bins=excluded.bins,
			max_rms=excluded.max_rms,
			silence_counts=excluded.silence_counts,
			music_counts=excluded.music_counts,
			silence_total=excluded.silence_total,
			music_total=excluded.music_total,
			derived_enter=excluded.derived_enter,
			derived_exit=excluded.derived_exit`,
		key, ts, st.Bins, float64(st.MaxRMS), string(silB), string(musB),
		int64(st.SilenceTotal), int64(st.MusicTotal),
		nullFloat(enter, ok), nullFloat(exit, ok),
	)
	if err != nil {
		return fmt.Errorf("library: SaveRMSLearning: %w", err)
	}
	return nil
}

func nullFloat(v float32, ok bool) interface{} {
	if !ok {
		return nil
	}
	return float64(v)
}
