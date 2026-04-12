package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

// buildState merges AirPlay and physical source state into the output schema.
// Source priority: AirPlay (active session) > physical detector (Vinyl/CD) > None.
//
// Idle delay: when physical audio stops, the last track is kept visible for
// IdleDelay seconds before switching to the idle screen. This covers the normal
// gap between tracks on a record without blanking the display.
func (m *mgr) buildState() PlayerState {
	m.mu.Lock()
	defer m.mu.Unlock()

	source := "None"
	state := "stopped"

	// physicalActive is true either when audio is currently detected, or when
	// it stopped recently enough to still be within the idle delay window.
	physicalActive := m.physicalSource == "Physical" ||
		(!m.lastPhysicalAt.IsZero() && time.Since(m.lastPhysicalAt) < m.cfg.IdleDelay)

	switch {
	case m.airplayPlaying:
		source = "AirPlay"
		state = "playing"
	case m.bluetoothPlaying:
		source = "Bluetooth"
		state = "playing"
	case physicalActive:
		source = "Physical"
		state = "playing"
	}

	var track *TrackInfo
	displaySource := source
	physFmt := "" // populated when Physical source format is known
	if source == "Physical" {
		// physicalFormat persists across track boundaries so source stays
		// "CD"/"Vinyl" even when recognitionResult is nil between tracks.
		fmtStr := m.physicalFormat
		if m.recognitionResult != nil && m.recognitionResult.Format != "" {
			fmtStr = m.recognitionResult.Format
		}
		switch strings.ToLower(strings.TrimSpace(fmtStr)) {
		case "cd":
			displaySource = "CD"
			physFmt = "CD"
		case "vinyl":
			displaySource = "Vinyl"
			physFmt = "Vinyl"
		}
	}

	switch source {
	case "AirPlay":
		track = &TrackInfo{
			Title:         m.title,
			Artist:        m.artist,
			Album:         m.album,
			DurationMS:    m.durationMS,
			SeekMS:        m.seekMS,
			SeekUpdatedAt: m.seekUpdatedAt.UTC().Format(time.RFC3339),
			SampleRate:    airplaySampleRate,
			BitDepth:      airplayBitDepth,
			ArtworkPath:   m.artworkPath,
			PhysicalMatch: m.streamingPhysicalMatch,
		}
	case "Bluetooth":
		track = &TrackInfo{
			Title:         m.bluetoothTitle,
			Artist:        m.bluetoothArtist,
			Album:         m.bluetoothAlbum,
			Codec:         m.bluetoothCodec,
			PhysicalMatch: m.streamingPhysicalMatch,
		}
	case "Physical":
		if r := m.recognitionResult; r != nil {
			var sampleRate, bitDepth string
			if strings.EqualFold(strings.TrimSpace(r.Format), "cd") {
				sampleRate = airplaySampleRate
				bitDepth = airplayBitDepth
			}
			track = &TrackInfo{
				Title:       r.Title,
				Artist:      r.Artist,
				Album:       r.Album,
				TrackNumber: r.TrackNumber,
				SampleRate:  sampleRate,
				BitDepth:    bitDepth,
				ArtworkPath: m.physicalArtworkPath,
			}
		}
		// track remains nil until recognition identifies the track.
	}

	return PlayerState{
		Source:    displaySource,
		Format:    physFmt,
		State:     state,
		Track:     track,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// runLibrarySync periodically refreshes the in-memory physical track metadata
// from the library DB. This makes UI edits visible in state.json without
// waiting for a new recognition cycle.
func (m *mgr) runLibrarySync(ctx context.Context, lib *internallibrary.Library) {
	if lib == nil {
		return
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.syncFromLibrary(lib)
		}
	}
}

// syncFromLibrary updates recognitionResult from the DB when a row exists for
// the current track (matched by ACRID or ShazamID) and user-edited fields differ
// from in-memory values. This makes UI edits visible in state.json without waiting
// for a new recognition cycle — including Shazam-only tracks (no ACRID).
//
// When AirPlay is the active source, it also checks whether the current streaming
// track exists in the local physical library and caches the result in
// m.streamingPhysicalMatch for display in the now-playing UI.
func (m *mgr) syncFromLibrary(lib *internallibrary.Library) {
	// --- Streaming → physical library match (AirPlay and Bluetooth) ---
	m.mu.Lock()
	var streamTitle, streamArtist string
	streamingActive := false
	switch {
	case m.airplayPlaying:
		streamTitle, streamArtist = m.title, m.artist
		streamingActive = true
	case m.bluetoothPlaying:
		streamTitle, streamArtist = m.bluetoothTitle, m.bluetoothArtist
		streamingActive = true
	}
	currentMatchKey := m.streamingMatchKey
	m.mu.Unlock()

	if streamingActive {
		newKey := streamTitle + "\x00" + streamArtist
		if newKey != currentMatchKey {
			var match *PhysicalMatchInfo
			if streamTitle != "" && streamArtist != "" {
				if entry, err := lib.FindPhysicalMatch(streamTitle, streamArtist); err == nil && entry != nil {
					match = &PhysicalMatchInfo{
						Format:      entry.Format,
						TrackNumber: entry.TrackNumber,
						Album:       entry.Album,
					}
					log.Printf("physical match: streaming track %q by %q found in library — format=%s", streamTitle, streamArtist, match.Format)
				}
			}
			m.mu.Lock()
			m.streamingMatchKey = newKey
			m.streamingPhysicalMatch = match
			m.mu.Unlock()
			m.markDirty()
		}
	} else {
		m.mu.Lock()
		hadMatch := m.streamingPhysicalMatch != nil || m.streamingMatchKey != ""
		m.streamingPhysicalMatch = nil
		m.streamingMatchKey = ""
		m.mu.Unlock()
		if hadMatch {
			m.markDirty()
		}
	}

	// --- Physical source sync (existing logic) ---
	m.mu.Lock()
	if m.physicalSource != "Physical" {
		m.mu.Unlock()
		return
	}
	r := m.recognitionResult
	acrid := ""
	shazamID := ""
	if r != nil {
		acrid = r.ACRID
		shazamID = r.ShazamID
	}
	entryID := m.physicalLibraryEntryID
	pendingStubID := m.pendingStubID
	var capturedFPs []Fingerprint
	if m.recognitionResult == nil && pendingStubID > 0 {
		capturedFPs = m.lastCapturedFPs
	}
	m.mu.Unlock()

	var entry *internallibrary.CollectionEntry
	var err error
	if acrid != "" || shazamID != "" {
		entry, err = lib.LookupByIDs(acrid, shazamID)
	} else if entryID > 0 {
		entry, err = lib.GetByID(entryID)
	} else if pendingStubID > 0 {
		entry, err = lib.GetByID(pendingStubID)
		if (err != nil || entry == nil) && len(capturedFPs) > 0 {
			// Stub is gone! Maybe it was resolved. Try finding by fingerprints.
			entry, err = lib.FindByFingerprints(capturedFPs, m.cfg.FingerprintThreshold, 30)
		}
	}
	if err != nil || entry == nil {
		if pendingStubID > 0 && (err != nil || entry == nil) {
			// Stub no longer exists and no replacement found.
			m.mu.Lock()
			if m.pendingStubID == pendingStubID {
				m.pendingStubID = 0
			}
			m.mu.Unlock()
		}
		return
	}

	m.mu.Lock()
	changed := false

	// When recognitionResult is nil but the pending stub has been user-enriched
	// (title/artist filled in by the user), promote it to recognitionResult so
	// the display shows the track info without waiting for a new recognition cycle.
	if m.recognitionResult == nil && m.pendingStubID == entry.ID &&
		(entry.Title != "" || entry.Artist != "") {
		m.recognitionResult = &RecognitionResult{
			ACRID:       entry.ACRID,
			ShazamID:    entry.ShazamID,
			Title:       entry.Title,
			Artist:      entry.Artist,
			Album:       entry.Album,
			Label:       entry.Label,
			Released:    entry.Released,
			Score:       entry.Score,
			Format:      entry.Format,
			TrackNumber: entry.TrackNumber,
		}
		m.physicalLibraryEntryID = entry.ID
		m.physicalArtworkPath = entry.ArtworkPath
		if f := strings.ToLower(strings.TrimSpace(entry.Format)); f == "cd" || f == "vinyl" {
			m.physicalFormat = entry.Format
		}
		m.mu.Unlock()
		m.markDirty()
		return
	}

	if m.recognitionResult != nil {
		// Match by whichever ID is available, or by DB entry ID as a final fallback.
		entryMatchesCurrent := (acrid != "" && m.recognitionResult.ACRID == acrid) ||
			(shazamID != "" && m.recognitionResult.ShazamID == shazamID) ||
			(m.physicalLibraryEntryID > 0 && m.physicalLibraryEntryID == entry.ID)
		if entryMatchesCurrent {
			if m.recognitionResult.Title != entry.Title {
				m.recognitionResult.Title = entry.Title
				changed = true
			}
			if m.recognitionResult.Artist != entry.Artist {
				m.recognitionResult.Artist = entry.Artist
				changed = true
			}
			if m.recognitionResult.Album != entry.Album {
				m.recognitionResult.Album = entry.Album
				changed = true
			}
			if m.recognitionResult.Format != entry.Format {
				m.recognitionResult.Format = entry.Format
				if f := strings.ToLower(strings.TrimSpace(entry.Format)); f == "cd" || f == "vinyl" {
					m.physicalFormat = entry.Format
				}
				changed = true
			}
			if m.recognitionResult.TrackNumber != entry.TrackNumber {
				m.recognitionResult.TrackNumber = entry.TrackNumber
				changed = true
			}
			if m.physicalArtworkPath != entry.ArtworkPath {
				m.physicalArtworkPath = entry.ArtworkPath
				changed = true
			}
		}
	}
	m.mu.Unlock()

	if changed {
		if m.cfg.Verbose {
			log.Printf("library sync: metadata updated for acrid=%s shazam_id=%s", acrid, shazamID)
		}
		m.markDirty()
	}
}

// runWriter consumes change notifications and atomically writes the state JSON file.
// It also re-evaluates state on a 5-second tick so that the idle delay expiry is
// reflected in the output file without waiting for another event.
// Runs in the main goroutine.
func (m *mgr) runWriter(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	write := func() {
		ps := m.buildState()
		if err := writeStateFile(m.cfg.OutputFile, ps); err != nil {
			log.Printf("failed to write state: %v", err)
			return
		}
		if m.cfg.Verbose {
			log.Printf("state written: source=%s state=%s", ps.Source, ps.State)
		}
	}

	for {
		select {
		case <-ctx.Done():
			_ = writeStateFile(m.cfg.OutputFile, PlayerState{
				Source:    "None",
				State:     "stopped",
				UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			})
			return
		case <-m.notify:
			write()
		case <-ticker.C:
			// Re-evaluate periodically so idle delay expiry is written promptly.
			// Also write once just after the window expires so state=stopped is pushed.
			m.mu.Lock()
			physNone := m.physicalSource != "Physical"
			wasPhysical := !m.lastPhysicalAt.IsZero()
			elapsed := time.Since(m.lastPhysicalAt)
			inIdleWindow := physNone && wasPhysical && elapsed < m.cfg.IdleDelay
			justExpired := physNone && wasPhysical && elapsed >= m.cfg.IdleDelay && elapsed < m.cfg.IdleDelay+10*time.Second
			m.mu.Unlock()
			if inIdleWindow || justExpired {
				write()
			}
		}
	}
}

func writeStateFile(path string, ps PlayerState) error {
	b, _ := json.MarshalIndent(ps, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
