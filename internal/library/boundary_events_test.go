package library

import (
	"path/filepath"
	"testing"
)

func TestRecordBoundaryEvent_RoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lib.db")
	lib, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	ev := BoundaryEvent{
		Outcome:        BoundaryOutcomeFired,
		BoundaryType:   "silence->audio",
		IsHard:         true,
		PhysicalSource: "Physical",
		FormatAtEvent:  "Physical",
		DurationMs:     180000,
		SeekMS:         12000,
	}
	if err := lib.RecordBoundaryEvent(ev); err != nil {
		t.Fatalf("RecordBoundaryEvent: %v", err)
	}

	var gotOutcome, gotType string
	var gotHard int
	err = lib.DB().QueryRow(`
		SELECT outcome, boundary_type, is_hard FROM boundary_events ORDER BY id DESC LIMIT 1`,
	).Scan(&gotOutcome, &gotType, &gotHard)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if gotOutcome != BoundaryOutcomeFired || gotType != "silence->audio" || gotHard != 1 {
		t.Fatalf("row mismatch: outcome=%q type=%q hard=%d", gotOutcome, gotType, gotHard)
	}
}
