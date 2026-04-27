package library

import (
	"database/sql"
	"fmt"
	"time"
)

// Boundary telemetry outcomes for the VU / duration-boundary path (R1).
const (
	BoundaryOutcomeFired                   = "fired"
	BoundaryOutcomeSuppressedDurationGuard = "suppressed_duration_guard"
	BoundaryOutcomeIgnoredMatureProgress   = "ignored_mature_progress"
	BoundaryOutcomeSuppressedNotPhysical   = "suppressed_not_physical"
	BoundaryOutcomeTriggerChannelFull      = "trigger_channel_full"
	BoundaryOutcomeEnergyChangeCooldown    = "energy_change_cooldown"
)

// Follow-up outcomes stored on boundary_events after recognition completes (R7).
const (
	FollowupOutcomeMatched             = "matched"
	FollowupOutcomeNoMatch             = "no_match"
	FollowupOutcomeRecognitionError    = "recognition_error"
	FollowupOutcomeCaptureError        = "capture_error"
	FollowupOutcomeDiscarded           = "discarded_source_changed"
	FollowupOutcomeSkippedCoordinator  = "skipped_coordinator"
	FollowupOutcomeSameTrackRestored   = "same_track_restored"
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

// BoundaryRecognitionFollowup is written once per fired boundary after the
// matching recognition attempt finishes (or aborts).
type BoundaryRecognitionFollowup struct {
	Outcome           string
	PostACRID         string
	PostShazamID      string
	PostCollectionID  int64
	PostPlayHistoryID int64
	NewRecording      *bool
}

// Conservative early-boundary cohort: long tracks only, boundary seek well
// before nominal end (see recognition-enhancement-plan Axis 3).
const (
	earlyBoundaryMinTrackDurationMs = 90000
	earlyBoundarySeekFraction       = 0.25
)

func computeEarlyBoundaryFlag(seekMS int64, durationMs int) bool {
	if durationMs < earlyBoundaryMinTrackDurationMs {
		return false
	}
	return float64(seekMS) < earlyBoundarySeekFraction*float64(durationMs)
}

// RecordBoundaryEvent inserts a boundary telemetry row. It is safe to call with
// l == nil (no-op, returns 0, nil). format_resolved / format_resolved_at are
// populated later by the web UI when the user saves Vinyl/CD for the collection
// row (R2). Returns the new row id (SQLite last_insert_rowid).
func (l *Library) RecordBoundaryEvent(ev BoundaryEvent) (int64, error) {
	if l == nil || l.db == nil {
		return 0, nil
	}
	if ev.Outcome == "" {
		return 0, fmt.Errorf("library: RecordBoundaryEvent: empty outcome")
	}
	ts := ev.OccurredAt.UTC().Format(time.RFC3339Nano)
	if ev.OccurredAt.IsZero() {
		ts = time.Now().UTC().Format(time.RFC3339Nano)
	}
	hard := 0
	if ev.IsHard {
		hard = 1
	}
	res, err := l.db.Exec(`
		INSERT INTO boundary_events (
			occurred_at, outcome, boundary_type, is_hard, physical_source, format_at_event,
			duration_ms, seek_ms, play_history_id, collection_id
		) VALUES (?,?,?,?,?,?,?,?,NULLIF(?,0),NULLIF(?,0))`,
		ts, ev.Outcome, ev.BoundaryType, hard, ev.PhysicalSource, ev.FormatAtEvent,
		ev.DurationMs, ev.SeekMS, ev.PlayHistoryID, ev.CollectionID,
	)
	if err != nil {
		return 0, fmt.Errorf("library: RecordBoundaryEvent: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("library: RecordBoundaryEvent lastInsertId: %w", err)
	}
	return id, nil
}

// ConvertBoundaryEventOutcome rewrites outcome/type when a fired row could not
// be queued to the recognition coordinator (trigger channel full).
func (l *Library) ConvertBoundaryEventOutcome(evID int64, outcome, boundaryType string, isHard bool) error {
	if l == nil || l.db == nil || evID <= 0 {
		return nil
	}
	hard := 0
	if isHard {
		hard = 1
	}
	_, err := l.db.Exec(`
		UPDATE boundary_events SET outcome = ?, boundary_type = ?, is_hard = ? WHERE id = ?`,
		outcome, boundaryType, hard, evID,
	)
	if err != nil {
		return fmt.Errorf("library: ConvertBoundaryEventOutcome: %w", err)
	}
	return nil
}

// LinkBoundaryRecognitionFollowup attaches post-recognition outcome to a fired
// boundary row. Safe to call with l == nil or evID <= 0 (no-op).
func (l *Library) LinkBoundaryRecognitionFollowup(evID int64, fu BoundaryRecognitionFollowup) error {
	if l == nil || l.db == nil || evID <= 0 || fu.Outcome == "" {
		return nil
	}
	var dur, seek sql.NullInt64
	err := l.db.QueryRow(`
		SELECT duration_ms, seek_ms FROM boundary_events WHERE id = ?`, evID).Scan(&dur, &seek)
	if err != nil {
		return fmt.Errorf("library: LinkBoundaryRecognitionFollowup select: %w", err)
	}
	early := 0
	if dur.Valid && seek.Valid {
		if computeEarlyBoundaryFlag(seek.Int64, int(dur.Int64)) {
			early = 1
		}
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)

	var newRec sql.NullInt64
	if fu.NewRecording != nil {
		if *fu.NewRecording {
			newRec = sql.NullInt64{Int64: 1, Valid: true}
		} else {
			newRec = sql.NullInt64{Int64: 0, Valid: true}
		}
	}

	_, err = l.db.Exec(`
		UPDATE boundary_events SET
			followup_outcome = ?,
			followup_acrid = NULLIF(?,''),
			followup_shazam_id = NULLIF(?,''),
			followup_collection_id = NULLIF(?,0),
			followup_play_history_id = NULLIF(?,0),
			followup_new_recording = ?,
			early_boundary = ?,
			followup_recorded_at = ?
		WHERE id = ?`,
		fu.Outcome,
		fu.PostACRID,
		fu.PostShazamID,
		fu.PostCollectionID,
		fu.PostPlayHistoryID,
		newRec,
		early,
		ts,
		evID,
	)
	if err != nil {
		return fmt.Errorf("library: LinkBoundaryRecognitionFollowup update: %w", err)
	}
	return nil
}
