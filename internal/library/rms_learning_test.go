package library

import (
	"path/filepath"
	"testing"
)

func TestDeriveSilenceThresholdsFromHistograms_Separable(t *testing.T) {
	bins := 80
	maxR := float32(0.25)
	sil := make([]uint64, bins)
	mus := make([]uint64, bins)
	// Silence mass near low RMS (bins 0–5), music mass higher (bins 20–40)
	for i := 0; i < 6; i++ {
		sil[i] = 100
	}
	for i := 20; i < 41; i++ {
		mus[i] = 80
	}
	silN := uint64(600)
	musN := uint64(1680)
	e, x, ok := DeriveSilenceThresholdsFromHistograms(sil, mus, silN, musN, maxR)
	if !ok {
		t.Fatal("expected ok")
	}
	if e <= 0 || x <= e {
		t.Fatalf("bad thresholds enter=%v exit=%v", e, x)
	}
}

func TestDeriveSilenceThresholdsFromHistograms_Overlap(t *testing.T) {
	bins := 40
	maxR := float32(0.2)
	sil := make([]uint64, bins)
	mus := make([]uint64, bins)
	for i := range sil {
		sil[i] = 50
		mus[i] = 50
	}
	_, _, ok := DeriveSilenceThresholdsFromHistograms(sil, mus, 2000, 2000, maxR)
	if ok {
		t.Fatal("expected not ok when distributions overlap")
	}
}

func TestSaveLoadRMSLearning_RoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lib.db")
	lib, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	st := NewRMSLearningHistogramState("vinyl", 40, 0.2)
	st.Silence[2] = 10
	st.SilenceTotal = 10
	st.Music[15] = 20
	st.MusicTotal = 20
	if err := lib.SaveRMSLearning(st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := lib.LoadRMSLearning("vinyl")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Silence[2] != 10 || loaded.Music[15] != 20 {
		t.Fatalf("histogram mismatch %+v %+v", loaded.Silence[2], loaded.Music[15])
	}
	if loaded.SilenceTotal != 10 || loaded.MusicTotal != 20 {
		t.Fatalf("totals mismatch sil=%d mus=%d", loaded.SilenceTotal, loaded.MusicTotal)
	}
}
