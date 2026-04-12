package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

// File map for this service:
// - main.go: config/types and process wiring
// - shairport_metadata.go: shairport metadata pipe parsing + AirPlay state updates
// - bluetooth_monitor.go: dbus-monitor subprocess + BlueZ AVRCP event parsing
// - source_vu_monitor.go: physical source polling + VU boundary detection
// - recognition_setup.go: recognizer composition (order + roles)
// - recognition_coordinator.go: recognition workflow and persistence policies
// - state_output.go: state projection, library sync, JSON writer loop
// - track_helpers.go: track/artist normalization + cross-provider matching helpers

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
	Source    string     `json:"source"`           // AirPlay | Vinyl | CD | Physical | None
	Format    string     `json:"format,omitempty"` // CD | Vinyl — only present when source is Physical with identified format
	State     string     `json:"state"`            // playing | stopped
	Track     *TrackInfo `json:"track"`            // null when not playing or source is physical without metadata
	UpdatedAt string     `json:"updated_at"`
}

// TrackInfo holds per-track metadata. SeekMS + SeekUpdatedAt allow the UI to
// interpolate playback position without polling: pos = SeekMS + (now - SeekUpdatedAt).
type TrackInfo struct {
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
	// TrackNumber is the track position on the release. For CD it is a numeric
	// string ("3"); for vinyl it may encode side and position ("A2"). Empty when
	// unknown. Set from the library and not populated by recognition providers.
	TrackNumber   string             `json:"track_number,omitempty"`
	DurationMS    int64              `json:"duration_ms"`
	SeekMS        int64              `json:"seek_ms"`
	SeekUpdatedAt string             `json:"seek_updated_at"`
	SampleRate    string             `json:"samplerate"`
	BitDepth      string             `json:"bitdepth"`
	ArtworkPath   string             `json:"artwork_path,omitempty"`
	PhysicalMatch *PhysicalMatchInfo `json:"physical_match,omitempty"`
	// Codec is the audio codec in use. Populated for Bluetooth (e.g. "SBC", "AAC",
	// "LDAC", "AptX") and may be used by other sources in the future.
	Codec string `json:"codec,omitempty"`
}

// PhysicalMatchInfo describes a physical-media library entry that corresponds
// to a track currently playing via a streaming source (AirPlay, Bluetooth, etc.).
type PhysicalMatchInfo struct {
	Format      string `json:"format"`                 // "Vinyl" | "CD"
	TrackNumber string `json:"track_number,omitempty"` // e.g. "A2", "3"
	Album       string `json:"album,omitempty"`
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
	// RecognizerChain controls which API providers are included and their order.
	// Valid values: "acrcloud_first" | "shazam_first" | "acrcloud_only" | "shazam_only" | "fingerprint_only".
	// Local fingerprint cache is always active as a final fallback. If the selected
	// policy resolves to no available API provider, recognition automatically
	// falls back to fingerprint-only mode.
	// Continuity monitoring always uses Shazam when available, independent of this setting.
	RecognizerChain string
	// ShazamContinuityInterval controls how often Shazam re-checks if the
	// current track is still playing (for soft/gapless transitions).
	ShazamContinuityInterval time.Duration
	// ShazamContinuityCaptureDuration is the capture duration used by periodic
	// Shazam continuity checks.
	ShazamContinuityCaptureDuration time.Duration
	// PCMSocket is the Unix socket path exposed by oceano-source-detector for raw PCM relay.
	// The recognizer reads from this socket so it never opens the ALSA device directly.
	PCMSocket                 string
	RecognizerCaptureDuration time.Duration
	// RecognizerMaxInterval is the periodic fallback re-recognition interval used
	// when no track has been identified yet. On timer-based fires the previous
	// result is kept on a no-match so the display is not blanked mid-track.
	RecognizerMaxInterval time.Duration
	// RecognizerRefreshInterval is how soon to re-check after a successful
	// recognition. Shorter than RecognizerMaxInterval so gapless track changes
	// (no silence gap) are caught within a reasonable time. The timer only
	// triggers if the full interval has elapsed since the last recognition.
	// Set to 0 to disable refresh (only boundary triggers will re-recognise).
	RecognizerRefreshInterval time.Duration
	// NoMatchBackoff is how long to wait before retrying after the recognition
	// provider returns no result. Lower values identify tracks faster at the
	// cost of more API calls. Default is 15s.
	NoMatchBackoff time.Duration
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
	// Constraint: (FingerprintWindows-1)*FingerprintStrideSec + FingerprintLengthSec <= RecognizerCaptureDuration.
	FingerprintStrideSec int
	// FingerprintLengthSec is the duration in seconds of each fingerprint window.
	// Must satisfy: FingerprintLengthSec <= RecognizerCaptureDuration - (FingerprintWindows-1)*FingerprintStrideSec.
	FingerprintLengthSec int
	// FingerprintBoundaryLeadSkipSecs is how many seconds to discard from the
	// start of a boundary-triggered capture. On vinyl, the stylus drop and
	// surface crackle precede the music; skipping a few seconds prevents a
	// crackle-only fingerprint from being stored as the track stub.
	FingerprintBoundaryLeadSkipSecs int
	// FingerprintThreshold is the maximum BER for a fingerprint to be considered a match.
	// 0.35 is the threshold used by AcoustID; lower values are stricter.
	FingerprintThreshold float64
	// FingerprintLocalFirst enables a conservative local-first lookup before
	// calling online providers. Only confirmed library entries are considered,
	// and matching uses FingerprintLocalFirstThreshold.
	FingerprintLocalFirst bool
	// FingerprintLocalFirstThreshold is the maximum BER for local-first matches.
	// Keep this stricter (lower) than FingerprintThreshold to avoid false positives.
	FingerprintLocalFirstThreshold float64

	// ConfirmationDelay is how long to wait before making a second ACRCloud call
	// to confirm a track change. When a recognition result differs from the current
	// track, the system waits this duration and captures again; only if both results
	// agree is the display updated. Set to 0 to disable confirmation (update immediately).
	ConfirmationDelay time.Duration
	// ConfirmationCaptureDuration is the capture length for the second (confirmation)
	// recognition call. Keep this shorter than RecognizerCaptureDuration to reduce
	// end-to-end latency on track changes.
	ConfirmationCaptureDuration time.Duration
	// ConfirmationBypassScore skips the second confirmation call when the initial
	// provider score is already very high. Set to 0 to always require confirmation.
	ConfirmationBypassScore int
}

func defaultConfig() Config {
	return Config{
		MetadataPipe:                    "/tmp/shairport-sync-metadata",
		SourceFile:                      "/tmp/oceano-source.json",
		OutputFile:                      "/tmp/oceano-state.json",
		ArtworkDir:                      "/var/lib/oceano/artwork",
		PCMSocket:                       "/tmp/oceano-pcm.sock",
		VUSocket:                        "/tmp/oceano-vu.sock",
		RecognizerCaptureDuration:       10 * time.Second,
		RecognizerMaxInterval:           5 * time.Minute,
		RecognizerRefreshInterval:       2 * time.Minute,
		NoMatchBackoff:                  15 * time.Second,
		IdleDelay:                       10 * time.Second,
		LibraryDB:                       "/var/lib/oceano/library.db",
		FingerprintWindows:              5,
		FingerprintStrideSec:            1,
		FingerprintLengthSec:            6,
		FingerprintBoundaryLeadSkipSecs: 2,
		FingerprintThreshold:            0.30,
		FingerprintLocalFirst:           true,
		FingerprintLocalFirstThreshold:  0.28,
		ConfirmationDelay:               0,
		ConfirmationCaptureDuration:     4 * time.Second,
		ConfirmationBypassScore:         95,
		ShazamPythonBin:                 "/opt/shazam-env/bin/python",
		ShazamContinuityInterval:        8 * time.Second,
		ShazamContinuityCaptureDuration: 4 * time.Second,
		RecognizerChain:                 "acrcloud_first",
	}
}

type recognizeTrigger struct {
	isBoundary bool
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

	// Bluetooth state (updated by runBluetoothMonitor goroutine)
	bluetoothPlaying     bool
	bluetoothTitle       string
	bluetoothArtist      string
	bluetoothAlbum       string
	bluetoothCodec       string // e.g. "SBC", "AAC", "LDAC", "AptX", "Opus"
	bluetoothArtworkPath string // fetched via iTunes API when track changes
	bluetoothArtworkKey  string // "artist\x00album" — avoids re-fetching same track
	bluetoothStopTimer   *time.Timer // debounce: delays stopped→false by 2 s

	// Physical source (updated by source watcher goroutine)
	physicalSource      string             // "Physical" or "None"
	lastPhysicalAt      time.Time          // last time physicalSource was "Physical"
	recognitionResult       *RecognitionResult // last successful recognition; nil until identified
	physicalArtworkPath     string             // artwork path for current physical track (from library or fetch)
	physicalFormat          string             // "CD" | "Vinyl" — set on recognition success; cleared only on new session
	physicalLibraryEntryID  int64              // library DB row ID for the current physical track; 0 when unknown

	// streamingPhysicalMatch is set when a streaming track (AirPlay, etc.) matches
	// an entry in the local physical library. Cleared when the track changes or
	// streaming stops. Populated by syncFromLibrary on its 3-second ticker.
	streamingPhysicalMatch *PhysicalMatchInfo
	// streamingMatchKey is the "title\x00artist" key of the last lookup so we
	// avoid re-querying the library on every tick when the track hasn't changed.
	streamingMatchKey string

	// recognizeTrigger is sent to when a new recognition attempt should start:
	// on Physical source activation and on track-boundary events from runVUMonitor.
	recognizeTrigger chan recognizeTrigger

	// lastBoundaryAt is the time of the most recent boundary trigger. Used to
	// prune stubs that were created before ACRCloud had a chance to match the
	// same track on a subsequent retry.
	lastBoundaryAt time.Time
	// lastStubAt is the time the most recent unrecognised-track stub was stored.
	// A cooldown prevents creating duplicate stubs when the VU monitor fires
	// multiple boundary triggers within the same track (brief musical pauses,
	// run-out groove noise at end of side, etc.).
	lastStubAt time.Time
	// pendingStubID tracks the unresolved stub currently being enriched by
	// retry attempts within the same boundary/session. Subsequent no-match
	// retries append fingerprints to this stub instead of creating new rows.
	pendingStubID int64
	// lastCapturedFPs is the most recent fingerprint capture. Used by the library
	// sync to find tracks that were manually associated by the user while
	// the state manager was in "Identifying" state.
	lastCapturedFPs []Fingerprint
	// lastRecognizedAt is the time of the most recent successful recognition.
	// Used by the fallback timer to allow periodic re-checks when no VU boundary
	// trigger fires (e.g. gapless albums with no audible silence between tracks).
	lastRecognizedAt time.Time
	// shazamContinuityReady becomes true when the current track is a Shazam
	// fallback match, or when Shazam has confirmed the current ACR track.
	shazamContinuityReady bool
	// recognizerBusyUntil suppresses continuity checks while the main recognizer
	// is already capturing/identifying, avoiding stale duplicate triggers.
	recognizerBusyUntil time.Time
	// Last continuity mismatch signature, used to dedupe repeated triggers for
	// the same from->to mismatch within a short cooldown window.
	lastContinuityMismatchAt   time.Time
	lastContinuityMismatchFrom string
	lastContinuityMismatchTo   string
}

func newMgr(cfg Config) *mgr {
	return &mgr{
		cfg:              cfg,
		notify:           make(chan struct{}, 1),
		physicalSource:   "None",
		seekUpdatedAt:    time.Now(),
		recognizeTrigger: make(chan recognizeTrigger, 1),
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
func (m *mgr) runRecognizer(ctx context.Context, rec Recognizer, confirmRec Recognizer, shazamRec Recognizer, fpr Fingerprinter, lib *internallibrary.Library) {
	newRecognitionCoordinator(m, rec, confirmRec, shazamRec, fpr, lib).run(ctx)
}

func (m *mgr) tryEnableShazamContinuity(ctx context.Context, shazamRec Recognizer, current *RecognitionResult) {
	if shazamRec == nil || current == nil || current.ACRID == "" {
		return
	}
	// Skip if already aligned for this track — avoids duplicate goroutines when
	// the same ACRCloud result is recorded twice (retry after confirmation, etc.).
	m.mu.Lock()
	alreadyReady := m.shazamContinuityReady && m.recognitionResult != nil && m.recognitionResult.ACRID == current.ACRID
	m.mu.Unlock()
	if alreadyReady {
		return
	}
	dur := m.cfg.ShazamContinuityCaptureDuration
	if dur <= 0 {
		dur = 6 * time.Second
	}
	capCtx, cancel := context.WithTimeout(ctx, dur+8*time.Second)
	wavPath, err := captureFromPCMSocket(capCtx, m.cfg.PCMSocket, dur, 0, os.TempDir())
	cancel()
	if err != nil {
		return
	}
	defer os.Remove(wavPath)

	shRes, err := shazamRec.Recognize(ctx, wavPath)
	if err != nil || shRes == nil {
		return
	}
	if !tracksEquivalent(current.Title, current.Artist, shRes.Title, shRes.Artist) {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recognitionResult == nil {
		return
	}
	if m.recognitionResult.ACRID != current.ACRID {
		return
	}
	m.shazamContinuityReady = true
	if m.recognitionResult.ShazamID == "" && shRes.ShazamID != "" {
		m.recognitionResult.ShazamID = shRes.ShazamID
	}
	log.Printf("shazam continuity: alignment confirmed for current ACR track (%s — %s)", current.Artist, current.Title)
}

func (m *mgr) runShazamContinuityMonitor(ctx context.Context, shazamRec Recognizer) {
	if shazamRec == nil {
		return
	}
	interval := m.cfg.ShazamContinuityInterval
	if interval <= 0 {
		interval = 8 * time.Second
	}
	captureDur := m.cfg.ShazamContinuityCaptureDuration
	if captureDur <= 0 {
		captureDur = 4 * time.Second
	}
	const continuityMismatchCooldown = 20 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		m.mu.Lock()
		if m.physicalSource != "Physical" || m.recognitionResult == nil {
			m.mu.Unlock()
			continue
		}
		if time.Now().Before(m.recognizerBusyUntil) {
			m.mu.Unlock()
			continue
		}
		current := *m.recognitionResult
		ready := m.shazamContinuityReady
		m.mu.Unlock()

		if !ready {
			continue
		}

		capCtx, cancel := context.WithTimeout(ctx, captureDur+8*time.Second)
		wavPath, err := captureFromPCMSocket(capCtx, m.cfg.PCMSocket, captureDur, 0, os.TempDir())
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}

		shRes, recErr := shazamRec.Recognize(ctx, wavPath)
		os.Remove(wavPath)
		if recErr != nil || shRes == nil {
			continue
		}

		if tracksEquivalent(current.Title, current.Artist, shRes.Title, shRes.Artist) {
			m.mu.Lock()
			if m.recognitionResult != nil && sameTrackByProviderIDs(m.recognitionResult, &current) {
				m.lastRecognizedAt = time.Now()
				if m.recognitionResult.ShazamID == "" && shRes.ShazamID != "" {
					m.recognitionResult.ShazamID = shRes.ShazamID
				}
			}
			m.mu.Unlock()
			continue
		}

		fromKey := canonicalTrackKey(&current)
		toKey := canonicalTrackKey(shRes)
		now := time.Now()
		m.mu.Lock()
		if m.recognitionResult == nil || !sameTrackByProviderIDs(m.recognitionResult, &current) {
			// Current track changed while the continuity check was running.
			m.mu.Unlock()
			continue
		}
		duplicateMismatch := m.lastContinuityMismatchFrom == fromKey &&
			m.lastContinuityMismatchTo == toKey &&
			now.Sub(m.lastContinuityMismatchAt) < continuityMismatchCooldown
		if duplicateMismatch {
			m.mu.Unlock()
			continue
		}
		m.lastContinuityMismatchAt = now
		m.lastContinuityMismatchFrom = fromKey
		m.lastContinuityMismatchTo = toKey
		m.mu.Unlock()

		log.Printf("shazam continuity: mismatch detected (%s — %s vs %s — %s) — triggering immediate re-recognition",
			current.Artist, current.Title, shRes.Artist, shRes.Title)
		select {
		case m.recognizeTrigger <- recognizeTrigger{isBoundary: true}:
		default:
		}
	}
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
	flag.DurationVar(&cfg.RecognizerRefreshInterval, "recognizer-refresh-interval", cfg.RecognizerRefreshInterval, "how soon to re-check after a successful recognition to catch gapless track changes (0 = disabled)")
	flag.DurationVar(&cfg.NoMatchBackoff, "recognizer-no-match-backoff", cfg.NoMatchBackoff, "wait before retrying after a no-match response from the recognition provider")
	flag.DurationVar(&cfg.IdleDelay, "idle-delay", cfg.IdleDelay, "how long to keep showing the last track after audio stops before switching to idle screen")
	flag.StringVar(&cfg.LibraryDB, "library-db", cfg.LibraryDB, "path to SQLite library database (empty to disable)")
	flag.IntVar(&cfg.FingerprintWindows, "fingerprint-windows", cfg.FingerprintWindows, "number of fingerprint windows to generate per recognition capture")
	flag.IntVar(&cfg.FingerprintStrideSec, "fingerprint-stride", cfg.FingerprintStrideSec, "stride in seconds between fingerprint windows")
	flag.IntVar(&cfg.FingerprintLengthSec, "fingerprint-length", cfg.FingerprintLengthSec, "length in seconds of each fingerprint window")
	flag.Float64Var(&cfg.FingerprintThreshold, "fingerprint-threshold", cfg.FingerprintThreshold, "maximum BER for a local fingerprint match (0.35 = AcoustID default)")
	flag.BoolVar(&cfg.FingerprintLocalFirst, "fingerprint-local-first", cfg.FingerprintLocalFirst, "attempt a conservative local fingerprint lookup before online providers")
	flag.Float64Var(&cfg.FingerprintLocalFirstThreshold, "fingerprint-local-first-threshold", cfg.FingerprintLocalFirstThreshold, "maximum BER for conservative local-first fingerprint matches")
	flag.IntVar(&cfg.FingerprintBoundaryLeadSkipSecs, "fingerprint-boundary-lead-skip", cfg.FingerprintBoundaryLeadSkipSecs, "seconds to skip at the start of a boundary-triggered capture (helps avoid vinyl crackle in the stored stub)")
	flag.DurationVar(&cfg.ConfirmationDelay, "confirmation-delay", cfg.ConfirmationDelay, "wait before second recognition call to confirm a track change (0 = disabled)")
	flag.DurationVar(&cfg.ConfirmationCaptureDuration, "confirmation-capture-duration", cfg.ConfirmationCaptureDuration, "audio capture duration for confirmation call")
	flag.IntVar(&cfg.ConfirmationBypassScore, "confirmation-bypass-score", cfg.ConfirmationBypassScore, "skip confirmation when initial provider score is >= this value (0 = always confirm)")
	flag.StringVar(&cfg.ShazamPythonBin, "shazam-python", cfg.ShazamPythonBin, "path to Python binary with shazamio installed (empty to disable Shazam fallback)")
	flag.DurationVar(&cfg.ShazamContinuityInterval, "shazam-continuity-interval", cfg.ShazamContinuityInterval, "how often to run Shazam continuity checks for the current track")
	flag.DurationVar(&cfg.ShazamContinuityCaptureDuration, "shazam-continuity-capture-duration", cfg.ShazamContinuityCaptureDuration, "audio capture duration per periodic Shazam continuity check")
	flag.StringVar(&cfg.RecognizerChain, "recognizer-chain", cfg.RecognizerChain, "recognition chain order: acrcloud_first | shazam_first | acrcloud_only | shazam_only | fingerprint_only (continuity always uses Shazam when available)")
	flag.Parse()

	log.Printf("oceano-state-manager starting")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	_ = os.MkdirAll(filepath.Dir(cfg.OutputFile), 0o755)

	m := newMgr(cfg)
	m.markDirty() // write initial stopped state immediately

	var lib *internallibrary.Library
	if cfg.LibraryDB != "" {
		var err error
		lib, err = internallibrary.Open(cfg.LibraryDB)
		if err != nil {
			log.Printf("library: failed to open %s: %v — library recording disabled", cfg.LibraryDB, err)
		} else {
			defer lib.Close()
			log.Printf("library: opened at %s", cfg.LibraryDB)
		}
	}
	components := buildRecognitionComponents(cfg, lib)
	rec := components.chain
	confirmRec := components.confirmer
	shazamRec := components.continuity

	if rec != nil {
		log.Printf("recognizer: chain=%s pcm-socket=%s max-interval=%s refresh-interval=%s confirm-delay=%s shazam-continuity=%s",
			rec.Name(), cfg.PCMSocket, cfg.RecognizerMaxInterval, cfg.RecognizerRefreshInterval, cfg.ConfirmationDelay, cfg.ShazamContinuityInterval)
	}

	fpr := components.fingerprint
	if fpr != nil && rec != nil {
		log.Printf("recognizer: local fingerprint cache enabled (windows=%d stride=%ds length=%ds threshold=%.2f local-first=%v local-first-threshold=%.2f boundary-lead-skip=%ds)",
			cfg.FingerprintWindows, cfg.FingerprintStrideSec, cfg.FingerprintLengthSec, cfg.FingerprintThreshold, cfg.FingerprintLocalFirst, cfg.FingerprintLocalFirstThreshold, cfg.FingerprintBoundaryLeadSkipSecs)
		captureSec := int(cfg.RecognizerCaptureDuration.Seconds())
		maxOffset := (cfg.FingerprintWindows - 1) * cfg.FingerprintStrideSec
		if maxOffset+cfg.FingerprintLengthSec > captureSec {
			log.Printf("WARN: fingerprint window clipping detected — window at offset %ds requests %ds but capture is only %ds; last window(s) will be truncated. Reduce fingerprint-length or fingerprint-stride.",
				maxOffset, cfg.FingerprintLengthSec, captureSec)
		}
	} else if fpr != nil {
		log.Printf("recognizer: fpcalc found but ACRCloud not configured — fingerprint cache inactive")
	} else {
		log.Printf("recognizer: fpcalc not found — local fingerprint cache disabled")
	}

	go m.runShairportReader(ctx)
	go m.runBluetoothMonitor(ctx)
	go m.runSourceWatcher(ctx)
	go m.runVUMonitor(ctx)
	go m.runRecognizer(ctx, rec, confirmRec, shazamRec, fpr, lib)
	go m.runShazamContinuityMonitor(ctx, shazamRec)
	go m.runLibrarySync(ctx, lib)
	go m.runStatsLogger(ctx, lib)
	m.runWriter(ctx)
}

func (m *mgr) runStatsLogger(ctx context.Context, lib *internallibrary.Library) {
	if lib == nil {
		return
	}
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats, err := lib.GetRecognitionStats()
			if err != nil {
				log.Printf("stats: failed to get recognition stats: %v", err)
				continue
			}
			if len(stats) == 0 {
				continue
			}
			log.Printf("--- Recognition Stats Summary ---")
			for p, evs := range stats {
				log.Printf("  [%s]: attempts=%d successes=%d no_match=%d errors=%d",
					p, evs["attempt"], evs["success"], evs["no_match"], evs["error"])
			}
		}
	}
}
