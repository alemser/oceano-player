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
		// Streaming source takes priority — physical media detection is ignored
		// when AirPlay (or future Bluetooth/UPnP) is active.
		source = "AirPlay"
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
func (m *mgr) syncFromLibrary(lib *internallibrary.Library) {
	m.mu.Lock()
	r := m.recognitionResult
	if r == nil || (r.ACRID == "" && r.ShazamID == "") || m.physicalSource != "Physical" {
		m.mu.Unlock()
		return
	}
	acrid := r.ACRID
	shazamID := r.ShazamID
	m.mu.Unlock()

	entry, err := lib.LookupByIDs(acrid, shazamID)
	if err != nil || entry == nil {
		return
	}

	m.mu.Lock()
	changed := false
	currentACRID := ""
	currentShazamID := ""
	if m.recognitionResult != nil {
		currentACRID = m.recognitionResult.ACRID
		currentShazamID = m.recognitionResult.ShazamID
	}
	// Match by whichever ID is available — same logic as LookupByIDs.
	entryMatchesCurrent := (acrid != "" && currentACRID == acrid) ||
		(shazamID != "" && currentShazamID == shazamID)
	if m.recognitionResult != nil && entryMatchesCurrent {
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
