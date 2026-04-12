package main

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// runShairportReader loops, opening and reading the metadata FIFO. On EOF or error,
// it clears the AirPlay playing state and retries after a short delay.
func (m *mgr) runShairportReader(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := m.readPipe(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("shairport pipe error: %v — retrying in 2s", err)

			m.mu.Lock()
			wasPlaying := m.airplayPlaying
			m.airplayPlaying = false
			m.mu.Unlock()
			if wasPlaying {
				m.markDirty()
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}
}

// readPipe opens the FIFO (may block until shairport-sync is running), reads items,
// and returns when the pipe closes or context is cancelled.
func (m *mgr) readPipe(ctx context.Context) error {
	// os.Open on a FIFO blocks until a writer is present. Run in a goroutine
	// so we can respect context cancellation.
	type openResult struct {
		f   *os.File
		err error
	}
	ch := make(chan openResult, 1)
	go func() {
		f, err := os.Open(m.cfg.MetadataPipe)
		ch <- openResult{f, err}
	}()

	var f *os.File
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		f = r.f
	case <-ctx.Done():
		return ctx.Err()
	}
	defer f.Close()

	log.Printf("shairport-sync metadata pipe connected")

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 16384)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			buf = m.drainItems(buf)
			if len(buf) > maxBufSize {
				// Trim to last 8 KB on malformed stream to prevent unbounded growth.
				buf = buf[len(buf)-8192:]
			}
		}
		if err != nil {
			if err == io.EOF {
				log.Printf("shairport-sync metadata pipe closed")
				return nil
			}
			return err
		}
	}
}

// drainItems parses all complete metadata items from buf, applies each to state,
// and returns the unconsumed remainder.
func (m *mgr) drainItems(buf []byte) []byte {
	locs := itemRE.FindAllSubmatchIndex(buf, -1)
	if len(locs) == 0 {
		return buf
	}

	for _, loc := range locs {
		typeHex := string(buf[loc[2]:loc[3]])
		codeHex := string(buf[loc[4]:loc[5]])

		var rawData []byte
		if loc[6] >= 0 {
			b64 := strings.TrimSpace(string(buf[loc[6]:loc[7]]))
			if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
				rawData = decoded
			}
		}

		m.applyItem(decodeTag(typeHex), decodeTag(codeHex), rawData)
	}

	return buf[locs[len(locs)-1][1]:]
}

// applyItem updates internal state based on one shairport-sync metadata item.
// Mirrors the OceanoClient._apply_item() logic from oceano-now-playing.
func (m *mgr) applyItem(itemType, code string, data []byte) {
	strVal := strings.TrimSpace(string(data))

	if itemType == "core" {
		if strVal == "" {
			return
		}
		// Temporary: log all core fields to discover what Apple Music sends via AirPlay
		log.Printf("AirPlay core field: code=%q val=%q", code, strVal)
		var changed bool
		m.mu.Lock()
		switch code {
		case "minm": // track title
			changed = m.title != strVal
			m.title = strVal
		case "asar": // artist
			changed = m.artist != strVal
			m.artist = strVal
		case "asal": // album
			changed = m.album != strVal
			m.album = strVal
		}
		m.mu.Unlock()
		if changed {
			if m.cfg.Verbose {
				log.Printf("AirPlay: %s = %q", code, strVal)
			}
			// Try fetching artwork from iTunes if we have artist+album but no artwork yet
			if code == "asal" && m.artworkPath == "" {
				go m.tryFetchAirPlayArtwork()
			}
			m.markDirty()
		}
		return
	}

	if itemType != "ssnc" {
		return
	}

	switch code {
	case "pbeg": // play session begin
		m.mu.Lock()
		m.airplayPlaying = true
		m.seekMS = 0
		m.durationMS = 0
		m.seekUpdatedAt = time.Now()
		m.mu.Unlock()
		m.markDirty()
		log.Printf("AirPlay: play begin")

	case "prsm": // play resume
		m.mu.Lock()
		wasPlaying := m.airplayPlaying
		m.airplayPlaying = true
		m.mu.Unlock()
		if !wasPlaying {
			m.markDirty()
		}
		log.Printf("AirPlay: play resume")

	case "pend", "pfls", "stop": // play end / flush / stop
		m.mu.Lock()
		wasPlaying := m.airplayPlaying
		m.airplayPlaying = false
		m.artworkPath = "" // clear artwork when stopping
		m.mu.Unlock()
		if wasPlaying {
			m.markDirty()
			log.Printf("AirPlay: stopped (%s)", code)
		}

	case "prgr": // progress: "start/current/end" as 32-bit RTP ticks at 44100 Hz
		parts := strings.Split(strVal, "/")
		if len(parts) < 3 {
			return
		}
		start, e1 := strconv.ParseInt(parts[0], 10, 64)
		current, e2 := strconv.ParseInt(parts[1], 10, 64)
		end, e3 := strconv.ParseInt(parts[2], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil {
			return
		}
		seekMS := max(ticksDiff(start, current)*1000/44100, 0)
		durMS := max(ticksDiff(start, end)*1000/44100, 0)

		m.mu.Lock()
		m.airplayPlaying = true
		m.seekMS = seekMS
		m.durationMS = durMS
		m.seekUpdatedAt = time.Now()
		m.mu.Unlock()
		m.markDirty()

	case "PICT": // embedded album artwork (JPEG/PNG bytes)
		log.Printf("AirPlay: PICT received, bytes=%d", len(data))
		if len(data) == 0 {
			log.Printf("AirPlay: PICT empty, skipping")
			return
		}
		path := m.saveArtwork(data)
		if path == "" {
			log.Printf("AirPlay: saveArtwork returned empty path")
			return
		}
		m.mu.Lock()
		m.artworkPath = path
		m.mu.Unlock()
		m.markDirty()
		log.Printf("AirPlay: artwork saved → %s", path)
	}
}

// ticksDiff returns the difference between two 32-bit RTP timestamps, handling wraparound.
func ticksDiff(start, end int64) int64 {
	if end >= start {
		return end - start
	}
	return (1 << 32) - start + end
}

// tryFetchAirPlayArtwork attempts to fetch artwork from iTunes API for the current AirPlay track.
// This is a fallback for when PICT (embedded artwork) is not provided by the AirPlay client.
// It waits briefly for PICT to arrive, then falls back to iTunes search if not received.
func (m *mgr) tryFetchAirPlayArtwork() {
	// Wait a short time to see if PICT arrives
	time.Sleep(2 * time.Second)

	m.mu.Lock()
	// Check if PICT arrived in the meantime
	if m.artworkPath != "" {
		m.mu.Unlock()
		log.Printf("AirPlay: artwork already set (PICT arrived)")
		return
	}
	artist := m.artist
	album := m.album
	m.mu.Unlock()

	if artist == "" || album == "" {
		log.Printf("AirPlay: insufficient metadata for iTunes fallback (artist=%q, album=%q)", artist, album)
		return
	}

	// Use the same artwork fetching logic as Physical tracks
	path, err := fetchArtwork(artist, album, m.cfg.ArtworkDir)
	if err != nil {
		log.Printf("AirPlay: artwork fetch failed: %v", err)
		return
	}
	if path == "" {
		log.Printf("AirPlay: no artwork found for %q / %q", artist, album)
		return
	}

	m.mu.Lock()
	m.artworkPath = path
	m.mu.Unlock()
	m.markDirty()
	log.Printf("AirPlay: artwork fetched from iTunes → %s", path)
}

// saveArtwork writes raw image bytes to a content-addressed file and returns the path.
// If the file already exists (same content hash), it is reused without rewriting.
func (m *mgr) saveArtwork(data []byte) string {
	h := sha1.Sum(data)
	path := filepath.Join(m.cfg.ArtworkDir, fmt.Sprintf("oceano-artwork-%x.jpg", h[:8]))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if err := os.MkdirAll(m.cfg.ArtworkDir, 0o755); err != nil {
		log.Printf("failed to create artwork dir: %v", err)
		return ""
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("failed to save artwork: %v", err)
		return ""
	}
	return path
}

func decodeTag(hexStr string) string {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return ""
	}
	return string(b)
}
