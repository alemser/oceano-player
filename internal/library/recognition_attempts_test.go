package library

import (
	"path/filepath"
	"testing"
	"time"
)

func TestInsertRecognitionAttempt_RoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "library.db")
	lib, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	meta := &RecognitionAttemptContext{
		Trigger:           "boundary",
		BoundaryEventID:   42,
		IsHardBoundary:    true,
		Phase:             "primary",
		SkipMs:            2000,
		CaptureDurationMs: 7000,
		RMSMean:           0.05,
		RMSPeak:           0.12,
		PhysicalFormat:    "vinyl",
	}
	lib.InsertRecognitionAttempt(meta, "ACRCloud", "success", "", 1500*time.Millisecond)

	var got int
	if err := lib.DB().QueryRow(`SELECT COUNT(*) FROM recognition_attempts WHERE provider = 'ACRCloud' AND outcome = 'success'`).Scan(&got); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got != 1 {
		t.Fatalf("rows = %d, want 1", got)
	}
}
