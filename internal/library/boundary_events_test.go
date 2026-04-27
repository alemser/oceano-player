package library

import (
	"path/filepath"
	"testing"
	"time"
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
	id, err := lib.RecordBoundaryEvent(ev)
	if err != nil {
		t.Fatalf("RecordBoundaryEvent: %v", err)
	}
	if id <= 0 {
		t.Fatalf("RecordBoundaryEvent: expected positive id")
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

func TestComputeEarlyBoundaryFlag(t *testing.T) {
	if !computeEarlyBoundaryFlag(15000, 120000) {
		t.Fatal("want early boundary for long track + low seek fraction")
	}
	if computeEarlyBoundaryFlag(90000, 120000) {
		t.Fatal("want not early when seek fraction too high")
	}
	if computeEarlyBoundaryFlag(5000, 60000) {
		t.Fatal("want not early when duration below minimum")
	}
}

func TestLinkBoundaryRecognitionFollowup_WritesEarlyFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lib.db")
	lib, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	ev := BoundaryEvent{
		OccurredAt:     time.Now(),
		Outcome:        BoundaryOutcomeFired,
		BoundaryType:   "silence->audio",
		IsHard:         true,
		PhysicalSource: "Physical",
		FormatAtEvent:  "Physical",
		DurationMs:     180000,
		SeekMS:         12000,
	}
	evID, err := lib.RecordBoundaryEvent(ev)
	if err != nil {
		t.Fatal(err)
	}

	f := false
	if err := lib.LinkBoundaryRecognitionFollowup(evID, BoundaryRecognitionFollowup{
		Outcome:      FollowupOutcomeMatched,
		PostACRID:    "acr1",
		NewRecording: &f,
	}); err != nil {
		t.Fatal(err)
	}
	var early int
	err = lib.DB().QueryRow(`SELECT IFNULL(early_boundary,0) FROM boundary_events WHERE id=?`, evID).Scan(&early)
	if err != nil {
		t.Fatal(err)
	}
	if early != 1 {
		t.Fatalf("early_boundary=%d want 1", early)
	}
}
