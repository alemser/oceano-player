package main

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// airplaySampleRate and airplayBitDepth are fixed transport characteristics for AirPlay/shairport-sync.
const (
	airplaySampleRate = "44.1 kHz"
	airplayBitDepth   = "16 bit"
	maxBufSize        = 262144 // 256 KB: prevent unbounded growth on malformed streams
)

// itemRE extracts metadata items from the shairport-sync binary XML-ish pipe stream.
// Format: <item><type>HEX</type><code>HEX</code><length>N</length><data encoding="base64">B64</data></item>
var itemRE = regexp.MustCompile(
	`(?s)<item>\s*<type>([0-9a-fA-F]{8})</type>\s*<code>([0-9a-fA-F]{8})</code>\s*<length>\d+</length>\s*(?:<data encoding="base64">(.*?)</data>)?\s*</item>`,
)

// --- Output schema ---

// PlayerState is the unified state written to /tmp/oceano-state.json.
type PlayerState struct {
	Source    string     `json:"source"` // AirPlay | Vinyl | CD | None
	State     string     `json:"state"`  // playing | stopped
	Track     *TrackInfo `json:"track"`  // null when not playing or source is physical without metadata
	UpdatedAt string     `json:"updated_at"`
}

// TrackInfo holds per-track metadata. SeekMS + SeekUpdatedAt allow the UI to
// interpolate playback position without polling: pos = SeekMS + (now - SeekUpdatedAt).
type TrackInfo struct {
	Title         string `json:"title,omitempty"`
	Artist        string `json:"artist,omitempty"`
	Album         string `json:"album,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
	SeekMS        int64  `json:"seek_ms"`
	SeekUpdatedAt string `json:"seek_updated_at"`
	SampleRate    string `json:"samplerate"`
	BitDepth      string `json:"bitdepth"`
	ArtworkPath   string `json:"artwork_path,omitempty"`
}

// detectorOutput matches /tmp/oceano-source.json written by oceano-source-detector.
type detectorOutput struct {
	Source string `json:"source"`
}

// --- Config ---

type Config struct {
	MetadataPipe string
	SourceFile   string
	OutputFile   string
	ArtworkDir   string
	Verbose      bool

	// Recognition — all optional; recognition is disabled when ACRCloudHost is empty.
	ACRCloudHost      string
	ACRCloudAccessKey string
	ACRCloudSecretKey string
	// ShazamPythonBin is the path to the Python binary in the shazam-env virtualenv.
	// When set and shazamio is importable, Shazam is used as a fallback after ACRCloud.
	ShazamPythonBin string
	// PCMSocket is the Unix socket path exposed by oceano-source-detector for raw PCM relay.
	// The recognizer reads from this socket so it never opens the ALSA device directly.
	PCMSocket                 string
	RecognizerCaptureDuration time.Duration
	// RecognizerMaxInterval is the periodic fallback re-recognition interval.
	// On timer-based fires the previous result is kept on a no-match, so the
	// display is not blanked mid-track; only boundary-triggered attempts clear it.
	RecognizerMaxInterval time.Duration
	// VUSocket is the Unix socket path for VU frames from oceano-source-detector.
	// The state manager subscribes to detect silence→audio transitions (track boundaries)
	// and uses them to trigger recognition at the right moment.
	VUSocket string
	// IdleDelay is how long to keep showing the last physical track after audio stops
	// before switching to the idle screen. Defaults to 60 seconds.
	IdleDelay time.Duration
	// LibraryDB is the path to the SQLite database used to record physical-media plays.
	// Set to empty string to disable library recording.
	LibraryDB string

	// Fingerprint cache — requires fpcalc(1) from chromaprint-tools.
	// FingerprintWindows is the number of overlapping windows generated per capture.
	FingerprintWindows int
	// FingerprintStrideSec is the offset in seconds between consecutive windows.
	FingerprintStrideSec int
	// FingerprintLengthSec is the duration in seconds of each fingerprint window.
	FingerprintLengthSec int
	// FingerprintThreshold is the maximum BER for a fingerprint to be considered a match.
	// 0.35 is the threshold used by AcoustID; lower values are stricter.
	FingerprintThreshold float64

	// ConfirmationDelay is how long to wait before making a second ACRCloud call
	// to confirm a track change. When a recognition result differs from the current
	// track, the system waits this duration and captures again; only if both results
	// agree is the display updated. Set to 0 to disable confirmation (update immediately).
	ConfirmationDelay time.Duration
}

func defaultConfig() Config {
	return Config{
		MetadataPipe:              "/tmp/shairport-sync-metadata",
		SourceFile:                "/tmp/oceano-source.json",
		OutputFile:                "/tmp/oceano-state.json",
		ArtworkDir:                "/var/lib/oceano/artwork",
		PCMSocket:                 "/tmp/oceano-pcm.sock",
		VUSocket:                  "/tmp/oceano-vu.sock",
		RecognizerCaptureDuration: 10 * time.Second,
		RecognizerMaxInterval:     5 * time.Minute,
		IdleDelay:                 10 * time.Second,
		LibraryDB:                 "/var/lib/oceano/library.db",
		FingerprintWindows:        2,
		FingerprintStrideSec:      4,
		FingerprintLengthSec:      8,
		FingerprintThreshold:      0.25,
		ConfirmationDelay:         10 * time.Second,
		ShazamPythonBin:           "/opt/shazam-env/bin/python",
	}
}

// --- Manager ---

type mgr struct {
	cfg    Config
	mu     sync.Mutex
	notify chan struct{}

	// AirPlay state (updated by shairport reader goroutine)
	airplayPlaying bool
	title          string
	artist         string
	album          string
	durationMS     int64
	seekMS         int64
	seekUpdatedAt  time.Time
	artworkPath    string

	// Physical source (updated by source watcher goroutine)
	physicalSource      string             // "Physical" or "None"
	lastPhysicalAt      time.Time          // last time physicalSource was "Physical"
	recognitionResult   *RecognitionResult // last successful recognition; nil until identified
	physicalArtworkPath string             // artwork path for current physical track (from library or fetch)

	// recognizeTrigger is sent to when a new recognition attempt should start:
	// on Physical source activation and on track-boundary events from runVUMonitor.
	recognizeTrigger chan struct{}

	// lastBoundaryAt is the time of the most recent boundary trigger. Used to
	// prune stubs that were created before ACRCloud had a chance to match the
	// same track on a subsequent retry.
	lastBoundaryAt time.Time
	// lastStubAt is the time the most recent unrecognised-track stub was stored.
	// A cooldown prevents creating duplicate stubs when the VU monitor fires
	// multiple boundary triggers within the same track (brief musical pauses,
	// run-out groove noise at end of side, etc.).
	lastStubAt time.Time
}

func newMgr(cfg Config) *mgr {
	return &mgr{
		cfg:              cfg,
		notify:           make(chan struct{}, 1),
		physicalSource:   "None",
		seekUpdatedAt:    time.Now(),
		recognizeTrigger: make(chan struct{}, 1),
	}
}

// markDirty signals the writer to flush the current state to disk.
// Non-blocking: if a write is already pending, the signal is dropped (the pending
// write will use the latest state anyway).
func (m *mgr) markDirty() {
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

// --- Shairport-sync pipe reader ---

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
		if len(data) == 0 {
			return
		}
		path := m.saveArtwork(data)
		if path == "" {
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

// saveArtwork writes raw image bytes to a content-addressed file and returns the path.
// If the file already exists (same content hash), it is reused without rewriting.
func (m *mgr) saveArtwork(data []byte) string {
	h := sha1.Sum(data)
	path := filepath.Join(m.cfg.ArtworkDir, fmt.Sprintf("oceano-artwork-%x.jpg", h[:8]))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("failed to save artwork: %v", err)
		return ""
	}
	return path
}

// --- Source detector watcher ---

// runSourceWatcher polls the source detector JSON file every 2 seconds.
// Changes are rare (user switches from vinyl to CD), so polling is sufficient.
func (m *mgr) runSourceWatcher(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pollSourceFile()
		}
	}
}

func (m *mgr) pollSourceFile() {
	data, err := os.ReadFile(m.cfg.SourceFile)
	if err != nil {
		return
	}
	var det detectorOutput
	if err := json.Unmarshal(data, &det); err != nil {
		return
	}
	src := det.Source
	if src == "" {
		src = "None"
	}

	m.mu.Lock()
	changed := m.physicalSource != src
	newSession := false
	if src == "Physical" {
		// A new session is one where we've been silent longer than the idle delay —
		// meaning the user stopped the record and started a new one, not just a
		// gap between tracks or brief oscillation around the threshold.
		if m.lastPhysicalAt.IsZero() || time.Since(m.lastPhysicalAt) > m.cfg.IdleDelay {
			newSession = true
		}
		m.lastPhysicalAt = time.Now()
	}
	if newSession {
		m.recognitionResult = nil
		m.physicalArtworkPath = ""
	}
	needsTrigger := src == "Physical" && m.recognitionResult == nil
	m.physicalSource = src
	m.mu.Unlock()

	if changed {
		log.Printf("physical source: %s", src)
		m.markDirty()
	} else if src == "Physical" {
		// Even without a state change, mark dirty so the idle screen wakes
		// promptly if the writer missed a previous Physical transition.
		m.markDirty()
	}

	if needsTrigger {
		select {
		case m.recognizeTrigger <- struct{}{}:
		default:
		}
	}
}

// --- State builder and writer ---

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
	// Default: Physical
	displaySource := source
	log.Printf("Display source: %s", displaySource)
	if source == "Physical" && m.recognitionResult != nil {
		format := strings.ToLower(strings.TrimSpace(m.recognitionResult.Format))
		log.Printf("Classified format: %s", format)
		switch format {
		case "cd":
			displaySource = "CD"
		case "vinyl":
			displaySource = "Vinyl"
		}
		log.Printf("Classified source updated to: %s", displaySource)
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
				SampleRate:  sampleRate,
				BitDepth:    bitDepth,
				ArtworkPath: m.physicalArtworkPath,
			}
		}
		// track remains nil until recognition identifies the track.
	}

	return PlayerState{
		Source:    displaySource,
		State:     state,
		Track:     track,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// runVUMonitor subscribes to the VU socket from oceano-source-detector and detects
// silence→audio transitions that signal a new track starting. On each transition it
// sends to m.recognizeTrigger so the recognizer runs at the right moment rather than
// on a blind timer.
//
// VU frames are 8 bytes each (float32 L + float32 R, little-endian) at ~21.5 Hz.
// A track boundary is: avg RMS below silenceThreshold for silenceFrames consecutive
// frames, followed by avg RMS above silenceThreshold for activeFrames frames.
func (m *mgr) runVUMonitor(ctx context.Context) {
	const (
		silenceThreshold = float32(0.01)
		silenceFrames    = 22 // ~1 s of silence (vinyl inter-track gaps can be < 2 s)
		activeFrames     = 11 // ~0.5 s of audio resumption
		retryDelay       = 5 * time.Second
	)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.Dial("unix", m.cfg.VUSocket)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}

		log.Printf("VU monitor: connected to %s", m.cfg.VUSocket)
		m.readVUFrames(ctx, conn, silenceThreshold, silenceFrames, activeFrames)
		conn.Close()

		if ctx.Err() != nil {
			return
		}
		log.Printf("VU monitor: disconnected — reconnecting in %s", retryDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
	}
}

func (m *mgr) readVUFrames(ctx context.Context, conn net.Conn, silenceThreshold float32, silenceFrames, activeFrames int) {
	const (
		// Energy-change detection for track boundaries without silence gaps
		// (e.g. vinyl with residual hum, CD crossfades).
		//
		// Two EMAs track the signal energy: a slow baseline (~9 s time constant)
		// and a fast current level (~0.3 s). When the fast EMA dips well below the
		// baseline and then recovers, it indicates a track transition.
		energySlowAlpha      = float32(0.005) // ~200-frame / ~9 s time constant
		energyFastAlpha      = float32(0.15)  // ~7-frame  / ~0.3 s time constant
		energyDipRatio       = float32(0.45)  // fast < slow*0.45 → dip in progress
		energyRecoverRatio   = float32(0.75)  // fast > slow*0.75 after dip → boundary
		energyDipMinFrames   = 7              // dip must sustain ~0.3 s before committing
		energyWarmupFrames   = 200            // frames before detection is active (~9 s)
		energyChangeCooldown = 3 * time.Minute
	)

	buf := make([]byte, 8)
	silenceCount := 0
	activeCount := 0
	inSilence := false

	// Energy-change detection state.
	var slowEMA, fastEMA float32
	energyFrameCount := 0 // counts only non-silent frames; resets after any boundary
	dipCount := 0
	hadDip := false
	lastEnergyTrigger := time.Time{}

	fireBoundaryTrigger := func(reason string) {
		// Do not clear current metadata/artwork here: boundary detection can fire
		// on false positives, and clearing preemptively causes UI flicker/regressions.
		// State is cleared later only when a boundary-triggered recognition confirms no match.
		log.Printf("VU monitor: track boundary detected (%s) — triggering recognition", reason)
		select {
		case m.recognizeTrigger <- struct{}{}:
		default:
		}
	}

	go func() {
		<-ctx.Done()
		conn.SetDeadline(time.Now())
	}()

	for {
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		left := math.Float32frombits(binary.LittleEndian.Uint32(buf[0:4]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(buf[4:8]))
		avg := (left + right) / 2

		// --- Silence-based boundary detection ---
		if avg < silenceThreshold {
			silenceCount++
			activeCount = 0
			if silenceCount >= silenceFrames && !inSilence {
				inSilence = true
				log.Printf("VU monitor: silence detected")
				// Freeze energy model during silence; it will restart fresh on resumption.
				energyFrameCount = 0
				dipCount = 0
				hadDip = false
			}
		} else {
			activeCount++
			silenceCount = 0
			if inSilence && activeCount >= activeFrames {
				inSilence = false
				fireBoundaryTrigger("silence→audio")
				lastEnergyTrigger = time.Now()
			}
		}

		// --- Energy-change detection (seamless transitions without silence) ---
		// Don't run during silence or while still in the active-count phase after silence.
		if avg < silenceThreshold || inSilence {
			continue
		}

		energyFrameCount++
		if energyFrameCount == 1 {
			slowEMA = avg
			fastEMA = avg
		} else {
			slowEMA = energySlowAlpha*avg + (1-energySlowAlpha)*slowEMA
			fastEMA = energyFastAlpha*avg + (1-energyFastAlpha)*fastEMA
		}

		if energyFrameCount < energyWarmupFrames {
			continue // EMA not yet converged; avoid false positives on startup
		}

		if fastEMA < slowEMA*energyDipRatio {
			dipCount++
			if dipCount >= energyDipMinFrames {
				hadDip = true
			}
		} else {
			dipCount = 0
			if hadDip && fastEMA > slowEMA*energyRecoverRatio {
				hadDip = false
				if time.Since(lastEnergyTrigger) >= energyChangeCooldown {
					lastEnergyTrigger = time.Now()
					energyFrameCount = 0 // restart model for the new track
					fireBoundaryTrigger("energy-change")
				} else {
					log.Printf("VU monitor: energy-change boundary suppressed (cooldown active)")
				}
			}
		}
	}
}

// runRecognizer waits for triggers from m.recognizeTrigger (sent on Physical source
// activation and on track boundaries detected by runVUMonitor) and identifies the
// playing track via the given Recognizer. It is a no-op when rec is nil.
//
// Backoff strategy:
//   - rate limit  → wait rateLimitBackoff before next attempt
//   - no match    → wait noMatchBackoff before next attempt
//   - other error → wait errorBackoff before next attempt
//   - success     → wait until next trigger (track boundary) or RecognizerMaxInterval
//
// isBoundaryTrigger distinguishes explicit boundary events (silence/energy-change)
// from periodic timer fires. On a periodic no-match, the existing result is kept.
//
// confirmRec is used to cross-validate a new-track candidate before updating the
// display. When confirmRec differs from rec (e.g. Shazam confirming an ACRCloud
// result), agreement from two independent services is required. When confirmRec
// is nil, rec itself is used for the second call (same-provider confirmation).
func (m *mgr) runRecognizer(ctx context.Context, rec Recognizer, confirmRec Recognizer, fpr Fingerprinter, lib *Library) {
	if rec == nil {
		return
	}

	const (
		rateLimitBackoff = 5 * time.Minute
		noMatchBackoff   = 90 * time.Second
		errorBackoff     = 30 * time.Second
	)

	var backoffUntil time.Time

	fallbackTimer := time.NewTimer(m.cfg.RecognizerMaxInterval)
	defer fallbackTimer.Stop()

	for {
		// Wait for an explicit boundary trigger or the periodic fallback timer.
		// isBoundaryTrigger distinguishes the two: on a periodic no-match the
		// existing result is kept so the display is not blanked mid-track.
		isBoundaryTrigger := false
		select {
		case <-ctx.Done():
			return
		case <-m.recognizeTrigger:
			isBoundaryTrigger = true
			// Stop and drain so the timer doesn't fire spuriously on the next iteration.
			if !fallbackTimer.Stop() {
				select {
				case <-fallbackTimer.C:
				default:
				}
			}
			fallbackTimer.Reset(m.cfg.RecognizerMaxInterval)
		case <-fallbackTimer.C:
			fallbackTimer.Reset(m.cfg.RecognizerMaxInterval)
			m.mu.Lock()
			isPhysical := m.physicalSource == "Physical"
			m.mu.Unlock()
			if !isPhysical {
				continue
			}
		}

		if wait := time.Until(backoffUntil); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}

		m.mu.Lock()
		isPhysical := m.physicalSource == "Physical"
		isAirPlay := m.airplayPlaying
		m.mu.Unlock()
		if !isPhysical || isAirPlay {
			if isAirPlay {
				log.Printf("recognizer [%s]: skipping — AirPlay is active", rec.Name())
			}
			continue
		}

		// After a track-boundary trigger, skip the first 2 s of PCM to flush
		// buffered audio from the previous track and avoid fingerprint false-positives.
		const boundarySkip = 2 * time.Second
		var skip time.Duration
		if isBoundaryTrigger {
			skip = boundarySkip
			m.mu.Lock()
			m.lastBoundaryAt = time.Now()
			m.mu.Unlock()
		}

		log.Printf("recognizer [%s]: capturing %s from %s (skip=%s)",
			rec.Name(), m.cfg.RecognizerCaptureDuration, m.cfg.PCMSocket, skip)

		captureCtx, cancel := context.WithTimeout(ctx, skip+m.cfg.RecognizerCaptureDuration+10*time.Second)
		wavPath, err := captureFromPCMSocket(captureCtx, m.cfg.PCMSocket, m.cfg.RecognizerCaptureDuration, skip, os.TempDir())
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("recognizer [%s]: capture error: %v", rec.Name(), err)
			backoffUntil = time.Now().Add(errorBackoff)
			continue
		}

		// Generate fingerprints from the captured WAV for local cache lookup.
		captureSec := int(m.cfg.RecognizerCaptureDuration.Seconds())
		capturedFPs := GenerateFingerprints(fpr, wavPath,
			m.cfg.FingerprintWindows, m.cfg.FingerprintStrideSec,
			m.cfg.FingerprintLengthSec, captureSec)

		// Check local fingerprint cache before querying ACRCloud.
		// localEntry is declared here so it is visible in the ACRCloud block below
		// for stub pruning after a successful recognition.
		var localEntry *CollectionEntry
		if len(capturedFPs) > 0 && lib != nil {
			var fpErr error
			localEntry, fpErr = lib.FindByFingerprints(capturedFPs, m.cfg.FingerprintThreshold, 30)
			if fpErr != nil {
				log.Printf("recognizer: fingerprint lookup error: %v", fpErr)
			} else if localEntry != nil && localEntry.UserConfirmed {
				log.Printf("recognizer: local fingerprint match (id=%d %s — %s) — skipping ACRCloud",
					localEntry.ID, localEntry.Artist, localEntry.Title)
				// Only store fingerprints on first encounter — accumulating more
				// vectors per entry increases false-positive probability on lookup.
				if isBoundaryTrigger && !lib.HasFingerprints(localEntry.ID) {
					if fpSaveErr := lib.SaveFingerprints(localEntry.ID, capturedFPs); fpSaveErr != nil {
						log.Printf("recognizer: save fingerprints (cache hit): %v", fpSaveErr)
					}
				}
				os.Remove(wavPath)
				backoffUntil = time.Time{}
				m.mu.Lock()
				m.recognitionResult = &RecognitionResult{
					ACRID:    localEntry.ACRID,
					Title:    localEntry.Title,
					Artist:   localEntry.Artist,
					Album:    localEntry.Album,
					Label:    localEntry.Label,
					Released: localEntry.Released,
					Score:    localEntry.Score,
					Format:   localEntry.Format,
				}
				m.physicalArtworkPath = localEntry.ArtworkPath
				m.mu.Unlock()
				m.markDirty()
				for {
					select {
					case <-m.recognizeTrigger:
					default:
						goto fpDrained
					}
				}
			fpDrained:
				continue
			}
		}

		result, err := rec.Recognize(ctx, wavPath)
		os.Remove(wavPath)

		if ctx.Err() != nil {
			return
		}

		if err != nil {
			if errors.Is(err, ErrRateLimit) {
				log.Printf("recognizer [%s]: rate limited — backing off %s", rec.Name(), rateLimitBackoff)
				backoffUntil = time.Now().Add(rateLimitBackoff)
			} else {
				log.Printf("recognizer [%s]: error: %v", rec.Name(), err)
				backoffUntil = time.Now().Add(errorBackoff)
			}
			continue
		}

		backoffUntil = time.Time{} // reset backoff on any successful API response

		if result != nil {
			log.Printf("recognizer [%s]: score=%d  %s — %s", rec.Name(), result.Score, result.Artist, result.Title)

			// Confirmation: if the result differs from the current track and a
			// confirmation delay is configured, make a second capture to verify.
			// This prevents a false-positive on a single recognition call from
			// switching the display mid-track (e.g. Dire Straits / Exodus albums
			// where continuous audio bleeds between tracks at a boundary trigger).
			if m.cfg.ConfirmationDelay > 0 {
				m.mu.Lock()
				currentACRID := ""
				if m.recognitionResult != nil {
					currentACRID = m.recognitionResult.ACRID
				}
				m.mu.Unlock()

				isNewTrack := result.ACRID == "" || result.ACRID != currentACRID
				if isNewTrack {
					log.Printf("recognizer [%s]: new track candidate — confirming in %s", rec.Name(), m.cfg.ConfirmationDelay)
					select {
					case <-ctx.Done():
						return
					case <-time.After(m.cfg.ConfirmationDelay):
					}

					// Use a dedicated confirmer when available (e.g. Shazam cross-checking
					// an ACRCloud result). Fall back to re-querying rec itself.
					confirmer := confirmRec
					if confirmer == nil {
						confirmer = rec
					}

					confCtx, confCancel := context.WithTimeout(ctx, m.cfg.RecognizerCaptureDuration+10*time.Second)
					confWav, confErr := captureFromPCMSocket(confCtx, m.cfg.PCMSocket, m.cfg.RecognizerCaptureDuration, 0, os.TempDir())
					confCancel()

					if confErr != nil {
						if ctx.Err() != nil {
							return
						}
						log.Printf("recognizer [%s]: confirmation capture error — accepting original result: %v", rec.Name(), confErr)
					} else {
						conf, confRecErr := confirmer.Recognize(ctx, confWav)
						os.Remove(confWav)
						if ctx.Err() != nil {
							return
						}
						if confRecErr != nil {
							log.Printf("recognizer [%s]: confirmation (%s) error — accepting original result: %v", rec.Name(), confirmer.Name(), confRecErr)
						} else if conf == nil {
							log.Printf("recognizer [%s]: confirmation (%s) returned no match — discarding candidate %s — %s",
								rec.Name(), confirmer.Name(), result.Artist, result.Title)
							backoffUntil = time.Now().Add(noMatchBackoff)
							continue
						} else {
							// Cross-service confirmation: compare by title+artist (normalised)
							// since ACRCloud and Shazam use different ID spaces.
							sameTrack := confirmer == rec &&
								conf.ACRID != "" && conf.ACRID == result.ACRID
							if !sameTrack {
								// Normalise: lowercase title+artist match is sufficient.
								sameTrack = strings.EqualFold(conf.Title, result.Title) &&
									strings.EqualFold(conf.Artist, result.Artist)
							}
							if sameTrack {
								log.Printf("recognizer [%s]: confirmed by %s — %s — %s",
									rec.Name(), confirmer.Name(), result.Artist, result.Title)
							} else {
								log.Printf("recognizer [%s]: confirmation (%s) disagrees (got %s — %s) — discarding candidate %s — %s",
									rec.Name(), confirmer.Name(), conf.Artist, conf.Title, result.Artist, result.Title)
								// Use the confirmation result — it's the more recent capture.
								result = conf
							}
						}
					}
				}
			}

			if lib != nil {
				artworkPath := ""

				// Check if we already have this track with user-edited metadata.
				if entry, lookupErr := lib.Lookup(result.ACRID); lookupErr != nil {
					log.Printf("recognizer: library lookup error: %v", lookupErr)
				} else if entry != nil {
					log.Printf("recognizer: known track (plays: %d) — using saved metadata", entry.PlayCount)
					// Prefer user-corrected metadata over ACRCloud result.
					result.Title = entry.Title
					result.Artist = entry.Artist
					result.Album = entry.Album
					result.Format = entry.Format
					artworkPath = entry.ArtworkPath
				}

				// Fetch artwork if not already stored for this track.
				if artworkPath == "" && result.Album != "" {
					if ap, artErr := fetchArtwork(result.Artist, result.Album, m.cfg.ArtworkDir); artErr != nil {
						log.Printf("recognizer: artwork fetch error: %v", artErr)
					} else if ap != "" {
						log.Printf("recognizer: artwork saved at %s", ap)
						artworkPath = ap
					}
				}

				entryID, recErr := lib.RecordPlay(result, artworkPath)
				if recErr != nil {
					log.Printf("recognizer: library record error: %v", recErr)
				} else if entryID > 0 {
					// Only store fingerprints from boundary-triggered captures (start of
					// track), and only on the first recognition — accumulating more vectors
					// per entry on repeated plays increases false-positive probability.
					if len(capturedFPs) > 0 && isBoundaryTrigger && !lib.HasFingerprints(entryID) {
						if fpErr := lib.SaveFingerprints(entryID, capturedFPs); fpErr != nil {
							log.Printf("recognizer: save fingerprints error: %v", fpErr)
						}
					}
					// Prune stubs created since the last boundary trigger — they were
					// created when ACRCloud returned no-match on the first attempt for
					// this same track. Use time-based pruning (not BER) because the
					// successful recognition may have come from a timer retry whose
					// mid-song fingerprints won't BER-match the boundary-captured stubs.
					m.mu.Lock()
					lastBoundary := m.lastBoundaryAt
					m.mu.Unlock()
					if !lastBoundary.IsZero() {
						lib.PruneRecentStubs(lastBoundary, entryID)
					}
				}

				m.mu.Lock()
				m.recognitionResult = result
				// Re-read from DB so we get the path actually stored (handles dedup and
				// cases where RecordPlay preserved an existing artwork_path we didn't have).
				if entry, _ := lib.Lookup(result.ACRID); entry != nil && entry.ArtworkPath != "" {
					m.physicalArtworkPath = entry.ArtworkPath
				} else {
					m.physicalArtworkPath = artworkPath
				}
				m.mu.Unlock()
			} else {
				m.mu.Lock()
				m.recognitionResult = result
				m.mu.Unlock()
			}

			// Drain triggers that arrived during capture so we don't immediately re-recognise.
			for {
				select {
				case <-m.recognizeTrigger:
				default:
					goto drained
				}
			}
		drained:
		} else {
			log.Printf("recognizer [%s]: no match — retrying in %s", rec.Name(), noMatchBackoff)
			// Store a fingerprint stub only on boundary-triggered captures (start of
			// track). Mid-song retries would store fingerprints at arbitrary positions
			// that won't match future boundary captures, creating noisy stubs.
			// Additionally, only create one stub per track: if a stub was already
			// created since the last boundary trigger, skip — this prevents duplicate
			// stubs from multiple boundary events within the same track (brief musical
			// pauses, run-out groove noise at end of side, etc.).
			if len(capturedFPs) > 0 && lib != nil && isBoundaryTrigger {
				m.mu.Lock()
				lastStub := m.lastStubAt
				lastBoundary := m.lastBoundaryAt
				stillPhysical := m.physicalSource == "Physical"
				m.mu.Unlock()
				stubAlreadyCreated := !lastStub.IsZero() && lastStub.After(lastBoundary)
				if !stubAlreadyCreated && stillPhysical {
					if stub, stubErr := lib.UpsertStub(capturedFPs, m.cfg.FingerprintThreshold, 30); stubErr != nil {
						log.Printf("recognizer: stub upsert error: %v", stubErr)
					} else {
						log.Printf("recognizer: fingerprint stub stored (id=%d)", stub.ID)
						m.mu.Lock()
						m.lastStubAt = time.Now()
						m.mu.Unlock()
					}
				} else if !stillPhysical {
					log.Printf("recognizer: stub skipped — source is no longer Physical (run-out groove or disc removed)")
				} else {
					log.Printf("recognizer: stub skipped — already created for this boundary (lastStub=%s)", lastStub.Format(time.RFC3339))
				}
			}
			if isBoundaryTrigger {
				// Clear the previous track only on an explicit boundary (silence/energy-change).
				// On periodic timer fires keep the current result so the display is not blanked mid-track.
				m.mu.Lock()
				m.recognitionResult = nil
				m.physicalArtworkPath = ""
				m.mu.Unlock()
			}
			backoffUntil = time.Now().Add(noMatchBackoff)
		}
		m.markDirty()
	}
}

// runLibrarySync periodically refreshes the in-memory physical track metadata
// from the library DB. This makes UI edits visible in state.json without
// waiting for a new recognition cycle.
func (m *mgr) runLibrarySync(ctx context.Context, lib *Library) {
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
// the current ACRID and user-edited fields differ from in-memory values.
func (m *mgr) syncFromLibrary(lib *Library) {
	m.mu.Lock()
	r := m.recognitionResult
	if r == nil || r.ACRID == "" || m.physicalSource != "Physical" {
		m.mu.Unlock()
		return
	}
	acrid := r.ACRID
	m.mu.Unlock()

	entry, err := lib.Lookup(acrid)
	if err != nil || entry == nil {
		return
	}

	m.mu.Lock()
	changed := false
	if m.recognitionResult != nil && m.recognitionResult.ACRID == acrid {
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
			log.Printf("library sync: metadata updated for acrid=%s", acrid)
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

// --- Helpers ---

func decodeTag(hexStr string) string {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return ""
	}
	return string(b)
}

// --- Main ---

func main() {
	cfg := defaultConfig()
	flag.StringVar(&cfg.MetadataPipe, "metadata-pipe", cfg.MetadataPipe, "shairport-sync metadata FIFO path")
	flag.StringVar(&cfg.SourceFile, "source-file", cfg.SourceFile, "oceano-source-detector output JSON")
	flag.StringVar(&cfg.OutputFile, "output", cfg.OutputFile, "output state JSON file")
	flag.StringVar(&cfg.ArtworkDir, "artwork-dir", cfg.ArtworkDir, "directory for artwork cache files")
	flag.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "verbose logging")
	flag.StringVar(&cfg.ACRCloudHost, "acrcloud-host", cfg.ACRCloudHost, "ACRCloud API host (e.g. identify-eu-west-1.acrcloud.com)")
	flag.StringVar(&cfg.ACRCloudAccessKey, "acrcloud-access-key", cfg.ACRCloudAccessKey, "ACRCloud access key")
	flag.StringVar(&cfg.ACRCloudSecretKey, "acrcloud-secret-key", cfg.ACRCloudSecretKey, "ACRCloud secret key")
	flag.StringVar(&cfg.PCMSocket, "pcm-socket", cfg.PCMSocket, "Unix socket for raw PCM from oceano-source-detector")
	flag.StringVar(&cfg.VUSocket, "vu-socket", cfg.VUSocket, "Unix socket for VU frames from oceano-source-detector")
	flag.DurationVar(&cfg.RecognizerCaptureDuration, "recognizer-capture-duration", cfg.RecognizerCaptureDuration, "audio capture duration per recognition attempt")
	flag.DurationVar(&cfg.RecognizerMaxInterval, "recognizer-max-interval", cfg.RecognizerMaxInterval, "fallback re-recognition interval when no track boundary is detected and no result is held")
	flag.DurationVar(&cfg.IdleDelay, "idle-delay", cfg.IdleDelay, "how long to keep showing the last track after audio stops before switching to idle screen")
	flag.StringVar(&cfg.LibraryDB, "library-db", cfg.LibraryDB, "path to SQLite library database (empty to disable)")
	flag.IntVar(&cfg.FingerprintWindows, "fingerprint-windows", cfg.FingerprintWindows, "number of fingerprint windows to generate per recognition capture")
	flag.IntVar(&cfg.FingerprintStrideSec, "fingerprint-stride", cfg.FingerprintStrideSec, "stride in seconds between fingerprint windows")
	flag.IntVar(&cfg.FingerprintLengthSec, "fingerprint-length", cfg.FingerprintLengthSec, "length in seconds of each fingerprint window")
	flag.Float64Var(&cfg.FingerprintThreshold, "fingerprint-threshold", cfg.FingerprintThreshold, "maximum BER for a local fingerprint match (0.35 = AcoustID default)")
	flag.DurationVar(&cfg.ConfirmationDelay, "confirmation-delay", cfg.ConfirmationDelay, "wait before second recognition call to confirm a track change (0 = disabled)")
	flag.StringVar(&cfg.ShazamPythonBin, "shazam-python", cfg.ShazamPythonBin, "path to Python binary with shazamio installed (empty to disable Shazam fallback)")
	flag.Parse()

	log.Printf("oceano-state-manager starting")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	_ = os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755)

	m := newMgr(cfg)
	m.markDirty() // write initial stopped state immediately

	var lib *Library
	if cfg.LibraryDB != "" {
		var err error
		lib, err = Open(cfg.LibraryDB)
		if err != nil {
			log.Printf("library: failed to open %s: %v — library recording disabled", cfg.LibraryDB, err)
		} else {
			defer lib.Close()
			log.Printf("library: opened at %s", cfg.LibraryDB)
		}
	}

	var acrRec Recognizer
	if cfg.ACRCloudHost != "" && cfg.ACRCloudAccessKey != "" && cfg.ACRCloudSecretKey != "" {
		acrRec = NewACRCloudRecognizer(ACRCloudConfig{
			Host:      cfg.ACRCloudHost,
			AccessKey: cfg.ACRCloudAccessKey,
			SecretKey: cfg.ACRCloudSecretKey,
		})
		log.Printf("recognizer: ACRCloud enabled (host=%s)", cfg.ACRCloudHost)
	}

	var shazamRec Recognizer
	if cfg.ShazamPythonBin != "" {
		if s := NewShazamRecognizer(cfg.ShazamPythonBin); s != nil {
			shazamRec = s
			log.Printf("recognizer: Shazam enabled (python=%s)", cfg.ShazamPythonBin)
		} else {
			log.Printf("recognizer: Shazam unavailable — %s not found or shazamio not installed", cfg.ShazamPythonBin)
		}
	}

	rec := NewChainRecognizer(acrRec, shazamRec)

	// confirmRec is the recognizer used to cross-validate a new-track candidate.
	// When both ACRCloud and Shazam are available, Shazam confirms ACRCloud results
	// (and vice-versa) — two independent services must agree before updating the display.
	// When only one service is available, it confirms itself (same-provider second call).
	var confirmRec Recognizer
	if acrRec != nil && shazamRec != nil {
		// ACRCloud is primary; Shazam is the cross-validator.
		confirmRec = shazamRec
	}
	// confirmRec=nil → runRecognizer falls back to using rec for confirmation.

	if rec != nil {
		log.Printf("recognizer: chain=%s pcm-socket=%s max-interval=%s",
			rec.Name(), cfg.PCMSocket, cfg.RecognizerMaxInterval)
	}

	fpr := newFingerprinter()
	if fpr != nil && rec != nil {
		log.Printf("recognizer: local fingerprint cache enabled (windows=%d stride=%ds length=%ds threshold=%.2f)",
			cfg.FingerprintWindows, cfg.FingerprintStrideSec, cfg.FingerprintLengthSec, cfg.FingerprintThreshold)
	} else if fpr != nil {
		log.Printf("recognizer: fpcalc found but ACRCloud not configured — fingerprint cache inactive")
	} else {
		log.Printf("recognizer: fpcalc not found — local fingerprint cache disabled")
	}

	go m.runShairportReader(ctx)
	go m.runSourceWatcher(ctx)
	go m.runVUMonitor(ctx)
	go m.runRecognizer(ctx, rec, confirmRec, fpr, lib)
	go m.runLibrarySync(ctx, lib)
	m.runWriter(ctx)
}
