package library

import (
	"testing"
	"time"
)

func TestPercentileSorted(t *testing.T) {
	s := []float64{0.1, 0.2, 0.3, 0.4}
	if got := PercentileSorted(s, 0.5); got < 0.24 || got > 0.26 {
		t.Fatalf("p50: got %v want ~0.25", got)
	}
	if got := PercentileSorted([]float64{42}, 0.75); got != 42 {
		t.Fatalf("single: got %v", got)
	}
}

func TestQueryR3BoundaryTelemetry(t *testing.T) {
	path := t.TempDir() + "/lib.db"
	lib, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()

	cut := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	_, err = lib.DB().Exec(`
		INSERT INTO boundary_events (
			occurred_at, outcome, boundary_type, is_hard, physical_source, format_at_event,
			duration_ms, seek_ms, followup_outcome
		) VALUES (?,?,?,?,?,?,?,?,?)`,
		cut, BoundaryOutcomeFired, "silence->audio", 1, "Physical", "Vinyl",
		180000, 30000, FollowupOutcomeSameTrackRestored,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.DB().Exec(`
		INSERT INTO boundary_events (
			occurred_at, outcome, boundary_type, is_hard, physical_source, format_at_event,
			duration_ms, seek_ms, followup_outcome
		) VALUES (?,?,?,?,?,?,?,?,?)`,
		cut, BoundaryOutcomeFired, "silence->audio", 1, "Physical", "Vinyl",
		180000, 90000, FollowupOutcomeMatched,
	)
	if err != nil {
		t.Fatal(err)
	}

	tel, err := lib.QueryR3BoundaryTelemetry(time.Now().UTC().Add(-72*time.Hour), "vinyl")
	if err != nil {
		t.Fatal(err)
	}
	if tel.SameTrackRestored != 1 || tel.Matched != 1 {
		t.Fatalf("counts: same=%d matched=%d", tel.SameTrackRestored, tel.Matched)
	}
	if len(tel.MatchedSeekFractions) != 1 {
		t.Fatalf("fractions len: %d", len(tel.MatchedSeekFractions))
	}
	if tel.MatchedSeekFractions[0] < 0.49 || tel.MatchedSeekFractions[0] > 0.51 {
		t.Fatalf("fraction: got %v want ~0.5", tel.MatchedSeekFractions[0])
	}
}
