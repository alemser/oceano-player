package library

import (
	"database/sql"
	"log"
	"time"
)

// InsertRecognitionAttempt appends one row when library and meta are non-nil.
// Safe to call with nil receiver or nil meta (no-op).
func (l *Library) InsertRecognitionAttempt(meta *RecognitionAttemptContext, provider, outcome, errorClass string, latency time.Duration) {
	if l == nil || l.db == nil || meta == nil || provider == "" || outcome == "" {
		return
	}
	if meta.Phase == "" {
		meta.Phase = "primary"
	}
	hard := 0
	if meta.IsHardBoundary {
		hard = 1
	}
	var boundary sql.NullInt64
	if meta.BoundaryEventID > 0 {
		boundary = sql.NullInt64{Int64: meta.BoundaryEventID, Valid: true}
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	latMs := int(latency / time.Millisecond)
	if latMs < 0 {
		latMs = 0
	}
	_, err := l.db.Exec(`
		INSERT INTO recognition_attempts (
			occurred_at, provider, trigger_source, boundary_event_id, is_hard_boundary,
			phase, skip_ms, capture_duration_ms, outcome, error_class, latency_ms,
			rms_mean, rms_peak, physical_format
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ts, provider, meta.Trigger, boundary, hard,
		meta.Phase, meta.SkipMs, meta.CaptureDurationMs, outcome, errorClass, latMs,
		meta.RMSMean, meta.RMSPeak, meta.PhysicalFormat,
	)
	if err != nil {
		log.Printf("library: InsertRecognitionAttempt: %v", err)
	}
}
