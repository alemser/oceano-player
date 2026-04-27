package library

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// BoundaryTelemetryStats aggregates boundary_events rows used for optional R3
// calibration nudges (false-positive rate + matched-event progress distribution).
type BoundaryTelemetryStats struct {
	SameTrackRestored int
	Matched           int
	// MatchedSeekFractions holds seek_ms/duration_ms for fired+matched rows (capped at 1).
	MatchedSeekFractions []float64
}

// QueryBoundaryTelemetryStats reads telemetry since the given time. formatFilter is
// empty for all formats, or a lowercase label matched against
// COALESCE(format_resolved, format_at_event) (e.g. "vinyl", "cd", "physical").
func (l *Library) QueryBoundaryTelemetryStats(since time.Time, formatFilter string) (*BoundaryTelemetryStats, error) {
	if l == nil || l.db == nil {
		return &BoundaryTelemetryStats{}, nil
	}
	cut := since.UTC().Format(time.RFC3339Nano)
	fmtArg := strings.ToLower(strings.TrimSpace(formatFilter))

	queryPairs := `
		SELECT
			IFNULL(SUM(CASE WHEN followup_outcome = ? THEN 1 ELSE 0 END), 0),
			IFNULL(SUM(CASE WHEN followup_outcome = ? THEN 1 ELSE 0 END), 0)
		FROM boundary_events
		WHERE occurred_at >= ?
		  AND outcome = 'fired'
		  AND followup_outcome IN (?, ?)`
	args := []interface{}{
		FollowupOutcomeSameTrackRestored,
		FollowupOutcomeMatched,
		cut,
		FollowupOutcomeSameTrackRestored,
		FollowupOutcomeMatched,
	}

	if fmtArg != "" {
		queryPairs += ` AND lower(COALESCE(NULLIF(trim(format_resolved),''), format_at_event)) = ?`
		args = append(args, fmtArg)
	}

	var sameN, matchN int
	err := l.db.QueryRow(queryPairs, args...).Scan(&sameN, &matchN)
	if err != nil {
		return nil, fmt.Errorf("library: R3 pair counts: %w", err)
	}

	argsProg := []interface{}{cut, FollowupOutcomeMatched}
	queryProg := `
		SELECT seek_ms, duration_ms
		FROM boundary_events
		WHERE occurred_at >= ?
		  AND outcome = 'fired'
		  AND followup_outcome = ?
		  AND duration_ms > 0
		  AND seek_ms >= 0`
	if fmtArg != "" {
		queryProg += ` AND lower(COALESCE(NULLIF(trim(format_resolved),''), format_at_event)) = ?`
		argsProg = append(argsProg, fmtArg)
	}

	rows, err := l.db.Query(queryProg, argsProg...)
	if err != nil {
		return nil, fmt.Errorf("library: R3 progress query: %w", err)
	}
	defer rows.Close()

	var fracs []float64
	for rows.Next() {
		var seek int64
		var dur int
		if err := rows.Scan(&seek, &dur); err != nil {
			return nil, fmt.Errorf("library: R3 progress scan: %w", err)
		}
		if seek < 0 || dur <= 0 {
			continue
		}
		f := float64(seek) / float64(dur)
		if f > 1 {
			f = 1
		}
		fracs = append(fracs, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Float64s(fracs)
	return &BoundaryTelemetryStats{
		SameTrackRestored:    sameN,
		Matched:              matchN,
		MatchedSeekFractions: fracs,
	}, nil
}

// PercentileSorted returns the p quantile (0–1) of a sorted slice.
func PercentileSorted(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := p * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
