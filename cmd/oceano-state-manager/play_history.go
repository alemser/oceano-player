package main

import (
	"context"
	"log"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

// runPlayHistoryRecorder polls playback state every 5 seconds and writes
// play_history records to the library database. It detects track changes across
// all sources (AirPlay, Bluetooth, Physical) and closes open play records when
// playback stops or a new track begins.
func (m *mgr) runPlayHistoryRecorder(ctx context.Context) {
	if m.lib == nil {
		return
	}

	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			openID := m.currentPlayHistoryID
			m.currentPlayHistoryID = 0
			m.currentPlayKey = ""
			m.mu.Unlock()
			if openID > 0 {
				m.lib.ClosePlayHistory(openID, time.Now())
			}
			return
		case <-tick.C:
			m.tickPlayHistory()
		}
	}
}

func (m *mgr) tickPlayHistory() {
	snap := m.snapshotForHistory()

	m.mu.Lock()
	openID := m.currentPlayHistoryID
	currentKey := m.currentPlayKey
	m.mu.Unlock()

	if snap == nil {
		// Nothing playing — close any open play.
		if openID > 0 {
			m.mu.Lock()
			m.currentPlayHistoryID = 0
			m.currentPlayKey = ""
			m.mu.Unlock()
			m.lib.ClosePlayHistory(openID, time.Now())
		}
		return
	}

	newKey := snap.Source + "\x00" + snap.Title + "\x00" + snap.Artist
	if newKey == currentKey && openID > 0 {
		return // same track still playing
	}

	// Track changed or first play — close previous and open new.
	if openID > 0 {
		m.lib.ClosePlayHistory(openID, time.Now())
	}

	id, err := m.lib.OpenPlayHistory(*snap)
	if err != nil {
		log.Printf("history: open play error: %v", err)
		m.mu.Lock()
		m.currentPlayHistoryID = 0
		m.currentPlayKey = ""
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.currentPlayHistoryID = id
	m.currentPlayKey = newKey
	m.mu.Unlock()

}

// snapshotForHistory returns a PlayHistoryEntry describing what is currently
// playing, or nil when nothing is playing. Called without holding mu.
func (m *mgr) snapshotForHistory() *internallibrary.PlayHistoryEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Priority matches state_output.go: Physical > AirPlay > Bluetooth.
	if m.physicalSource == "Physical" && m.recognitionResult != nil {
		r := m.recognitionResult
		if r.Title == "" && r.Artist == "" {
			return nil
		}
		provider := ""
		if r.ACRID != "" {
			provider = "acrcloud"
		} else if r.ShazamID != "" {
			provider = "shazam"
		}
		return &internallibrary.PlayHistoryEntry{
			CollectionID:        m.physicalLibraryEntryID,
			Title:               r.Title,
			Artist:              r.Artist,
			Album:               r.Album,
			TrackNumber:         r.TrackNumber,
			Source:              "Physical",
			MediaFormat:         m.physicalFormat,
			VinylSide:           internallibrary.VinylSideFromTrackNumber(r.TrackNumber),
			ArtworkPath:         m.physicalArtworkPath,
			ArtworkSource:       artworkSource(m.physicalArtworkPath, m.physicalLibraryEntryID),
			RecognitionScore:    r.Score,
			RecognitionProvider: provider,
			MatchedLibrary:      m.physicalLibraryEntryID > 0,
			DurationMs:          r.DurationMs,
			ISRC:                r.ISRC,
		}
	}

	if m.airplayPlaying && m.title != "" {
		return &internallibrary.PlayHistoryEntry{
			Title:      m.title,
			Artist:     m.artist,
			Album:      m.album,
			Source:     "AirPlay",
			SampleRate: airplaySampleRate,
			BitDepth:   airplayBitDepth,
			ArtworkPath: m.artworkPath,
			ArtworkSource: artworkSource(m.artworkPath, 0),
			DurationMs: int(m.durationMS),
		}
	}

	if m.bluetoothPlaying && m.bluetoothTitle != "" {
		return &internallibrary.PlayHistoryEntry{
			Title:       m.bluetoothTitle,
			Artist:      m.bluetoothArtist,
			Album:       m.bluetoothAlbum,
			Source:      "Bluetooth",
			Codec:       m.bluetoothCodec,
			SampleRate:  m.bluetoothSampleRate,
			BitDepth:    m.bluetoothBitDepth,
			ArtworkPath: m.bluetoothArtworkPath,
			ArtworkSource: artworkSource(m.bluetoothArtworkPath, 0),
			DurationMs:  int(m.bluetoothDurationMS),
		}
	}

	return nil
}

func artworkSource(path string, collectionID int64) string {
	if path == "" {
		return "none"
	}
	return "recognized"
}
