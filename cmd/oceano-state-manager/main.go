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
	Source    string     `json:"source"`    // AirPlay | Vinyl | CD | None
	State     string     `json:"state"`     // playing | stopped
	Track     *TrackInfo `json:"track"`     // null when not playing or source is physical without metadata
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
	// PCMSocket is the Unix socket path exposed by oceano-source-detector for raw PCM relay.
	// The recognizer reads from this socket so it never opens the ALSA device directly.
	PCMSocket                 string
	RecognizerCaptureDuration time.Duration
	// RecognizerMaxInterval is the fallback re-recognition interval when no track
	// boundary is detected (e.g. long continuous playback without a silence gap).
	RecognizerMaxInterval time.Duration
	// VUSocket is the Unix socket path for VU frames from oceano-source-detector.
	// The state manager subscribes to detect silence→audio transitions (track boundaries)
	// and uses them to trigger recognition at the right moment.
	VUSocket string
}

func defaultConfig() Config {
	return Config{
		MetadataPipe:              "/tmp/shairport-sync-metadata",
		SourceFile:                "/tmp/oceano-source.json",
		OutputFile:                "/tmp/oceano-state.json",
		ArtworkDir:                "/tmp",
		PCMSocket:                 "/tmp/oceano-pcm.sock",
		VUSocket:                  "/tmp/oceano-vu.sock",
		RecognizerCaptureDuration: 10 * time.Second,
		RecognizerMaxInterval:     5 * time.Minute,
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
	physicalSource    string             // "Physical" or "None"
	recognitionResult *RecognitionResult // last successful recognition; nil until identified

	// recognizeTrigger is sent to when a new recognition attempt should start:
	// on Physical source activation and on track-boundary events from runVUMonitor.
	recognizeTrigger chan struct{}
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
	if changed && src != "Physical" {
		m.recognitionResult = nil
	}
	m.physicalSource = src
	m.mu.Unlock()

	if changed {
		log.Printf("physical source: %s", src)
		m.markDirty()
		if src == "Physical" {
			// New physical session — trigger recognition immediately.
			select {
			case m.recognizeTrigger <- struct{}{}:
			default:
			}
		}
	}
}

// --- State builder and writer ---

// buildState merges AirPlay and physical source state into the output schema.
// Source priority: AirPlay (active session) > physical detector (Vinyl/CD) > None.
func (m *mgr) buildState() PlayerState {
	m.mu.Lock()
	defer m.mu.Unlock()

	source := "None"
	state := "stopped"

	switch {
	case m.airplayPlaying:
		// Streaming source takes priority — physical media detection is ignored
		// when AirPlay (or future Bluetooth/UPnP) is active.
		source = "AirPlay"
		state = "playing"
	case m.physicalSource == "Physical":
		source = "Physical"
		state = "playing"
	}

	var track *TrackInfo
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
			track = &TrackInfo{
				Title:  r.Title,
				Artist: r.Artist,
				Album:  r.Album,
			}
		}
		// track remains nil until recognition identifies the track.
	}

	return PlayerState{
		Source:    source,
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
		silenceFrames    = 43  // ~2 s of silence
		activeFrames     = 11  // ~0.5 s of audio resumption
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
	buf := make([]byte, 8)
	silenceCount := 0
	activeCount := 0
	inSilence := false

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

		if avg < silenceThreshold {
			silenceCount++
			activeCount = 0
			if silenceCount >= silenceFrames && !inSilence {
				inSilence = true
				// Silence confirmed — clear the recognition result immediately so
				// the UI shows nothing rather than the previous track.
				m.mu.Lock()
				m.recognitionResult = nil
				m.mu.Unlock()
				m.markDirty()
				log.Printf("VU monitor: silence detected — cleared recognition result")
			}
		} else {
			activeCount++
			silenceCount = 0
			if inSilence && activeCount >= activeFrames {
				inSilence = false
				log.Printf("VU monitor: track boundary detected — triggering recognition")
				select {
				case m.recognizeTrigger <- struct{}{}:
				default:
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
func (m *mgr) runRecognizer(ctx context.Context, rec Recognizer) {
	if rec == nil {
		return
	}

	const (
		rateLimitBackoff = 5 * time.Minute
		noMatchBackoff   = 90 * time.Second
		errorBackoff     = 30 * time.Second
	)

	var backoffUntil time.Time

	for {
		// Wait for a trigger or the max interval fallback.
		select {
		case <-ctx.Done():
			return
		case <-m.recognizeTrigger:
		case <-time.After(m.cfg.RecognizerMaxInterval):
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
		m.mu.Unlock()
		if !isPhysical {
			continue
		}

		log.Printf("recognizer [%s]: capturing %s from %s",
			rec.Name(), m.cfg.RecognizerCaptureDuration, m.cfg.PCMSocket)

		captureCtx, cancel := context.WithTimeout(ctx, m.cfg.RecognizerCaptureDuration+10*time.Second)
		wavPath, err := captureFromPCMSocket(captureCtx, m.cfg.PCMSocket, m.cfg.RecognizerCaptureDuration, os.TempDir())
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("recognizer [%s]: capture error: %v", rec.Name(), err)
			backoffUntil = time.Now().Add(errorBackoff)
			continue
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

		m.mu.Lock()
		m.recognitionResult = result
		m.mu.Unlock()
		m.markDirty()

		if result != nil {
			log.Printf("recognizer [%s]: score=%d  %s — %s", rec.Name(), result.Score, result.Artist, result.Title)
		} else {
			log.Printf("recognizer [%s]: no match — retrying in %s", rec.Name(), noMatchBackoff)
			backoffUntil = time.Now().Add(noMatchBackoff)
		}
	}
}

// runWriter consumes change notifications and atomically writes the state JSON file.
// Runs in the main goroutine.
func (m *mgr) runWriter(ctx context.Context) {
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
			ps := m.buildState()
			if err := writeStateFile(m.cfg.OutputFile, ps); err != nil {
				log.Printf("failed to write state: %v", err)
				continue
			}
			if m.cfg.Verbose {
				log.Printf("state written: source=%s state=%s", ps.Source, ps.State)
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
	flag.DurationVar(&cfg.RecognizerMaxInterval, "recognizer-max-interval", cfg.RecognizerMaxInterval, "fallback re-recognition interval when no track boundary is detected")
	flag.Parse()

	log.Printf("oceano-state-manager starting")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	_ = os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755)

	m := newMgr(cfg)
	m.markDirty() // write initial stopped state immediately

	var rec Recognizer
	if cfg.ACRCloudHost != "" && cfg.ACRCloudAccessKey != "" && cfg.ACRCloudSecretKey != "" {
		rec = NewACRCloudRecognizer(ACRCloudConfig{
			Host:      cfg.ACRCloudHost,
			AccessKey: cfg.ACRCloudAccessKey,
			SecretKey: cfg.ACRCloudSecretKey,
		})
		log.Printf("recognizer: ACRCloud enabled (host=%s, pcm-socket=%s, max-interval=%s)",
			cfg.ACRCloudHost, cfg.PCMSocket, cfg.RecognizerMaxInterval)
	}

	go m.runShairportReader(ctx)
	go m.runSourceWatcher(ctx)
	go m.runVUMonitor(ctx)
	go m.runRecognizer(ctx, rec)
	m.runWriter(ctx)
}
