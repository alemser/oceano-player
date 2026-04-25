package library

import (
	"fmt"
	"time"
)

// Boundary telemetry outcomes for the VU / duration-boundary path (R1).
const (
	BoundaryOutcomeFired                       = "fired"
	BoundaryOutcomeSuppressedDurationGuard     = "suppressed_duration_guard"
	BoundaryOutcomeSuppressedIntraTrackSilence = "suppressed_intra_track_silence"
	BoundaryOutcomeIgnoredMatureProgress       = "ignored_mature_progress"
	BoundaryOutcomeSuppressedNotPhysical       = "suppressed_not_physical"
	BoundaryOutcomeTriggerChannelFull          = "trigger_channel_full"
	BoundaryOutcomeEnergyChangeCooldown        = "energy_change_cooldown"
)

// BoundaryEvent is one row in boundary_events (append-only telemetry).
type BoundaryEvent struct {
	OccurredAt     time.Time
	Outcome        string
	BoundaryType   string
	IsHard         bool
	PhysicalSource string
	FormatAtEvent  string
	DurationMs     int
	SeekMS         int64
	PlayHistoryID  int64
	CollectionID   int64
}

// RecordBoundaryEvent inserts a boundary telemetry row. It is safe to call with
// l == nil (no-op). format_resolved columns are reserved for R2 backfill.
func (l *Library) RecordBoundaryEvent(ev BoundaryEvent) error {
	if l == nil || l.db == nil {
		return nil
	}
	if ev.Outcome == "" {
		return fmt.Errorf("library: RecordBoundaryEvent: empty outcome")
	}
	ts := ev.OccurredAt.UTC().Format(time.RFC3339Nano)
	if ev.OccurredAt.IsZero() {
		ts = time.Now().UTC().Format(time.RFC3339Nano)
	}
	hard := 0
	if ev.IsHard {
		hard = 1
	}
	_, err := l.db.Exec(`
		INSERT INTO boundary_events (
			occurred_at, outcome, boundary_type, is_hard, physical_source, format_at_event,
			duration_ms, seek_ms, play_history_id, collection_id
		) VALUES (?,?,?,?,?,?,?,?,NULLIF(?,0),NULLIF(?,0))`,
		ts, ev.Outcome, ev.BoundaryType, hard, ev.PhysicalSource, ev.FormatAtEvent,
		ev.DurationMs, ev.SeekMS, ev.PlayHistoryID, ev.CollectionID,
	)
	if err != nil {
		return fmt.Errorf("library: RecordBoundaryEvent: %w", err)
	}
	return nil
}
