package main

import (
	"log"
	"strings"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

// boundaryFormatAtEventSnapshot returns the format label used for telemetry when
// the user may still have "Physical" until Vinyl/CD is assigned in the library.
func boundaryFormatAtEventSnapshot(physicalSource, physicalFormat string, rec *RecognitionResult) string {
	if physicalFormat == "Vinyl" || physicalFormat == "CD" {
		return physicalFormat
	}
	if rec != nil {
		f := strings.TrimSpace(rec.Format)
		if f == "Vinyl" || f == "CD" {
			return f
		}
	}
	if physicalSource == "Physical" {
		return "Physical"
	}
	if physicalSource != "" {
		return physicalSource
	}
	return "Unknown"
}

func (m *mgr) recordBoundaryTelemetry(outcome, boundaryType string, isHard bool) {
	if m.lib == nil {
		return
	}
	ev := internallibrary.BoundaryEvent{
		OccurredAt:   time.Now(),
		Outcome:      outcome,
		BoundaryType: boundaryType,
		IsHard:       isHard,
	}
	m.mu.Lock()
	ev.PhysicalSource = m.physicalSource
	ev.FormatAtEvent = boundaryFormatAtEventSnapshot(m.physicalSource, m.physicalFormat, m.recognitionResult)
	if m.recognitionResult != nil {
		ev.DurationMs = m.recognitionResult.DurationMs
	}
	ev.SeekMS = m.physicalSeekMS
	ev.PlayHistoryID = m.currentPlayHistoryID
	ev.CollectionID = m.physicalLibraryEntryID
	m.mu.Unlock()

	if err := m.lib.RecordBoundaryEvent(ev); err != nil {
		log.Printf("boundary telemetry: %v", err)
	}
}
