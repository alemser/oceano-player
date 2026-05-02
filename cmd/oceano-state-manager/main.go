package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

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
	// AirPlay DACP context extracted from shairport metadata (ssnc acre/daid).
	// Used for transport capability readiness only (Phase 1).
	airplayDACPActiveRemote string
	airplayDACPID           string
	airplayDACPClientIP     string
	airplayDACPUpdatedAt    time.Time

	// Bluetooth state (updated by runBluetoothMonitor goroutine)
	bluetoothConnected     bool // true while a device is Connected=true via Device1
	bluetoothPlaying       bool // true while AVRCP status=playing
	bluetoothTitle         string
	bluetoothArtist        string
	bluetoothAlbum         string
	bluetoothDevicePath    string      // D-Bus path of the connected device, e.g. /org/bluez/hci0/dev_XX
	bluetoothCodec         string      // e.g. "SBC", "AAC", "LDAC", "AptX", "Opus"
	bluetoothSampleRate    string      // e.g. "44.1 kHz", "48 kHz", "96 kHz" — parsed from transport config
	bluetoothBitDepth      string      // e.g. "16 bit", "24 bit" — parsed from transport config
	bluetoothArtworkPath   string      // fetched via iTunes API when track changes
	bluetoothArtworkKey    string      // "artist\x00album" — avoids re-fetching same track
	bluetoothStopTimer     *time.Timer // debounce: delays stopped→false by 2 s
	bluetoothDurationMS    int64       // track duration from AVRCP (0 = unknown)
	bluetoothSeekMS        int64       // last known position from AVRCP Position property
	bluetoothSeekUpdatedAt time.Time   // wall-clock time when bluetoothSeekMS was last set

	// Physical source (updated by source watcher goroutine)
	physicalSource         string             // "Physical" or "None"
	lastPhysicalAt         time.Time          // last time physicalSource was "Physical"
	recognitionResult      *RecognitionResult // last successful recognition; nil until identified
	physicalArtworkPath    string             // artwork path for current physical track (from library or fetch)
	physicalFormat         string             // "CD" | "Vinyl" — set on recognition success; cleared only on new session
	physicalLibraryEntryID int64              // library DB row ID for the current physical track; 0 when unknown
	// physicalBoundarySensitive is the R8 library hint for the current track: nudges
	// duration-based VU boundary guards when true.
	physicalBoundarySensitive bool

	// streamingPhysicalMatch is set when a streaming track (AirPlay, etc.) matches
	// an entry in the local physical library. Cleared when the track changes or
	// streaming stops. Populated by syncFromLibrary on its 3-second ticker.
	streamingPhysicalMatch *PhysicalMatchInfo
	// streamingMatchKey is the "title\x00artist" key of the last lookup so we
	// avoid re-querying the library on every tick when the track hasn't changed.
	streamingMatchKey string

	// telemetryDurationPessimismDelta is optional telemetry-derived adjustment (R3).
	telemetryDurationPessimismDelta float64

	// recognizeTrigger is sent to when a new recognition attempt should start:
	// on Physical source activation and on track-boundary events from runVUMonitor.
	recognizeTrigger chan recognizeTrigger

	// lastBoundaryAt is the time of the most recent boundary trigger. Used to
	// prune stubs that were created before ACRCloud had a chance to match the
	// same track on a subsequent retry.
	lastBoundaryAt time.Time
	// lastStubAt is the time the most recent unrecognised-track stub was stored.
	// lastRecognizedAt is the time of the most recent successful recognition.
	// Used by the fallback timer to allow periodic re-checks when no VU boundary
	// trigger fires (e.g. gapless albums with no audible silence between tracks).
	lastRecognizedAt time.Time
	// shazamContinuityReady becomes true when the current track is a Shazamio
	// fallback match, or when Shazamio has confirmed the current ACR track.
	shazamContinuityReady bool
	// shazamContinuityAbandoned is set after the calibration deadline passes
	// without Shazamio ever agreeing with ACRCloud on the current track. When true,
	// the continuity monitor is skipped for the rest of the track's lifetime so
	// that systematic Shazamio mis-identification cannot fire spurious triggers.
	// Reset to false whenever shazamContinuityReady is reset (new track / boundary).
	shazamContinuityAbandoned bool
	// physicalStartedAt records approximately when the current track began
	// playing. Set to time.Now() when a new physical session starts (source
	// goes from None → Physical after idle delay). Updated to lastBoundaryAt
	// when a boundary-triggered recognition succeeds, so fallback timer
	// recognitions on the same track also get an accurate seek estimate.
	physicalStartedAt time.Time
	// vuInSilence is true while the VU boundary detector is in silence state.
	// Set/cleared by readVUFrames on enteredSilence/resumedFromSilence events.
	// Used by buildState to set state="idle" during inter-track gaps, and by
	// the recognition coordinator to skip recognition attempts during silence.
	vuInSilence bool
	// physicalSeekMS and physicalSeekUpdatedAt provide a best-effort seek
	// position for the Physical source progress bar. Set when recognition
	// completes (boundary or fallback), using the elapsed time since capture
	// start as a proxy for how far into the track we are. Reset on boundary.
	physicalSeekMS        int64
	physicalSeekUpdatedAt time.Time
	// lib is the library database, shared with the recognition coordinator.
	// Set once at startup; nil when library recording is disabled.
	lib *internallibrary.Library
	// currentPlayHistoryID is the open play_history row ID for the currently
	// playing track. 0 means no play is open. Protected by mu.
	currentPlayHistoryID int64
	// currentPlayKey is "source\x00title\x00artist" of the last opened play,
	// used to detect track changes without reopening on every poll.
	currentPlayKey string

	// recognizerBusyUntil suppresses continuity checks while the main recognizer
	// is already capturing/identifying, avoiding stale duplicate triggers.
	recognizerBusyUntil time.Time
	// recognitionPhase tracks the last terminal recognition outcome so the UI can
	// distinguish "no_match" and "off" from the initial "identifying" state.
	// "matched" is derived from recognitionResult != nil; "identifying" is derived
	// from recognizerBusyUntil; this field only needs to carry "no_match" and "off".
	recognitionPhase string
	// Continuity mismatch confirmation: a mismatch must be observed twice within
	// continuityMismatchConfirmWindow before a re-recognition trigger fires.
	// This prevents a single Shazamio mis-identification (common when running
	// without prior alignment confirmation) from causing a spurious track change.
	// While not calibrated, the monitor requires 3 sightings of the same pair.
	// lastContinuityMismatchAt records when the *first* sighting of the current
	// from→to pair occurred; the pair is stored in the two string fields below.
	lastContinuityMismatchAt    time.Time
	lastContinuityMismatchFrom  string
	lastContinuityMismatchTo    string
	lastContinuityMismatchCount int
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
// display. When confirmRec differs from rec (e.g. Shazamio confirming an ACRCloud
// result), agreement from two independent services is required. When confirmRec
// is nil, rec itself is used for the second call (same-provider confirmation).
func (m *mgr) runRecognizer(ctx context.Context, rec Recognizer, confirmRec Recognizer, shazamRec Recognizer, lib *internallibrary.Library) {
	newRecognitionCoordinator(m, rec, confirmRec, shazamRec, lib).run(ctx)
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
	log.Printf("shazamio continuity: alignment confirmed for current ACR track (%s — %s)", current.Artist, current.Title)
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
	// Use config values for calibration grace and mismatch confirmation window.
	calibrationGrace := m.cfg.ContinuityCalibrationGrace
	if calibrationGrace <= 0 {
		calibrationGrace = 45 * time.Second // fallback if not set
	}
	mismatchConfirmWindow := m.cfg.ContinuityMismatchConfirmWindow
	if mismatchConfirmWindow <= 0 {
		mismatchConfirmWindow = 3 * time.Minute // fallback if not set
	}
	earlyCheckMargin := m.cfg.EarlyCheckMargin
	if earlyCheckMargin <= 0 {
		earlyCheckMargin = 20 * time.Second // fallback if not set
	}
	requiredSightingsCal := m.cfg.ContinuityRequiredSightingsCalibrated
	if requiredSightingsCal <= 0 {
		requiredSightingsCal = 2 // fallback if not set
	}
	requiredSightingsUncal := m.cfg.ContinuityRequiredSightingsUncalibrated
	if requiredSightingsUncal <= 0 {
		requiredSightingsUncal = 3 // fallback if not set
	}
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
		abandoned := m.shazamContinuityAbandoned
		lastRecognizedAt := m.lastRecognizedAt
		m.mu.Unlock()

		// If calibration was already abandoned for this track, skip all polls.
		if abandoned {
			continue
		}

		// Duration-based skip: when the provider returned a track duration and
		// we are still comfortably within the expected track window, there is no
		// need to poll Shazamio — we are certain the same track is still playing.
		// Only activate this optimisation once the monitor is calibrated (ready),
		// so the calibration phase still runs at the normal cadence.
		if ready && current.DurationMs > 0 {
			trackDuration := time.Duration(current.DurationMs) * time.Millisecond
			elapsed := time.Since(lastRecognizedAt)
			if elapsed < trackDuration-earlyCheckMargin {
				continue
			}
		}

		// While not calibrated, wait a short grace period for a clean alignment.
		// After grace expires, continue monitoring in uncalibrated mode (stricter
		// mismatch confirmation) so gapless transitions are still detectable.
		if !ready && time.Since(lastRecognizedAt) < calibrationGrace {
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
				m.lastContinuityMismatchAt = time.Time{}
				m.lastContinuityMismatchFrom = ""
				m.lastContinuityMismatchTo = ""
				m.lastContinuityMismatchCount = 0
				if m.recognitionResult.ShazamID == "" && shRes.ShazamID != "" {
					m.recognitionResult.ShazamID = shRes.ShazamID
				}
				// Opportunistically confirm alignment: Shazamio agrees with the current
				// track, so the continuity monitor is now calibrated even if
				// tryEnableShazamContinuity previously failed.
				m.shazamContinuityReady = true
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
		// Require repeated sightings of the same from→to mismatch pair before
		// firing a trigger. In calibrated mode we require fewer sightings; while
		// uncalibrated we require more sightings to reduce false positives. On the
		// first sighting we record the pair and wait; subsequent sightings within
		// the confirm window increment the confirmation count. This prevents a
		// single Shazamio mis-identification — especially likely when running without
		// prior alignment confirmation (grace period) — from causing a spurious
		// track-change event.
		requiredSightings := requiredSightingsCal
		if !ready {
			requiredSightings = requiredSightingsUncal
		}
		samePair := m.lastContinuityMismatchFrom == fromKey && m.lastContinuityMismatchTo == toKey
		withinWindow := now.Sub(m.lastContinuityMismatchAt) < mismatchConfirmWindow
		if samePair && withinWindow {
			m.lastContinuityMismatchCount++
		} else {
			m.lastContinuityMismatchAt = now
			m.lastContinuityMismatchFrom = fromKey
			m.lastContinuityMismatchTo = toKey
			m.lastContinuityMismatchCount = 1
		}
		if m.lastContinuityMismatchCount < requiredSightings {
			m.mu.Unlock()
			log.Printf("shazamio continuity: mismatch candidate (%s — %s vs %s — %s) — awaiting confirmation (%d/%d)",
				current.Artist, current.Title, shRes.Artist, shRes.Title, m.lastContinuityMismatchCount, requiredSightings)
			continue
		}
		// Required sightings confirmed — reset so the next change starts clean.
		// Save the first-sighting time before clearing it: the coordinator uses
		// it as the seek anchor so the new track's elapsed time is not inflated
		// by the confirmation delay (1–2 poll intervals = 8–16 s).
		firstDetectedAt := m.lastContinuityMismatchAt
		m.lastContinuityMismatchAt = time.Time{}
		m.lastContinuityMismatchFrom = ""
		m.lastContinuityMismatchTo = ""
		m.lastContinuityMismatchCount = 0
		m.mu.Unlock()

		log.Printf("shazamio continuity: mismatch confirmed (%s — %s vs %s — %s) — triggering immediate re-recognition",
			current.Artist, current.Title, shRes.Artist, shRes.Title)
		select {
		case m.recognizeTrigger <- recognizeTrigger{isBoundary: true, detectedAt: firstDetectedAt}:
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
	flag.StringVar(&cfg.AudDAPIToken, "audd-api-token", cfg.AudDAPIToken, "AudD API token (optional; https://api.audd.io BYOK)")
	flag.StringVar(&cfg.PCMSocket, "pcm-socket", cfg.PCMSocket, "Unix socket for raw PCM from oceano-source-detector")
	flag.StringVar(&cfg.VUSocket, "vu-socket", cfg.VUSocket, "Unix socket for VU frames from oceano-source-detector")
	flag.Float64Var(&cfg.VUSilenceThreshold, "vu-silence-threshold", cfg.VUSilenceThreshold, "RMS threshold for VU monitor silence detection (track-boundary detection)")
	flag.StringVar(&cfg.CalibrationConfigPath, "calibration-config", cfg.CalibrationConfigPath, "path to oceano-web config JSON used to load calibration profiles")
	flag.DurationVar(&cfg.RecognizerCaptureDuration, "recognizer-capture-duration", cfg.RecognizerCaptureDuration, "audio capture duration per recognition attempt")
	flag.DurationVar(&cfg.RecognizerMaxInterval, "recognizer-max-interval", cfg.RecognizerMaxInterval, "fallback re-recognition interval when no track boundary is detected and no result is held")
	flag.DurationVar(&cfg.RecognizerRefreshInterval, "recognizer-refresh-interval", cfg.RecognizerRefreshInterval, "how soon to re-check after a successful recognition to catch gapless track changes (0 = disabled)")
	flag.DurationVar(&cfg.NoMatchBackoff, "recognizer-no-match-backoff", cfg.NoMatchBackoff, "wait before retrying after a no-match response from the recognition provider")
	flag.DurationVar(&cfg.IdleDelay, "idle-delay", cfg.IdleDelay, "how long to keep showing the last track after audio stops before switching to idle screen")
	flag.DurationVar(&cfg.SessionGapThreshold, "session-gap-threshold", cfg.SessionGapThreshold, "max silence gap treated as inter-track pause; gaps longer than this start a new recognition session")
	flag.StringVar(&cfg.LibraryDB, "library-db", cfg.LibraryDB, "path to SQLite library database (empty to disable)")
	flag.DurationVar(&cfg.ConfirmationDelay, "confirmation-delay", cfg.ConfirmationDelay, "wait before second recognition call to confirm a track change (0 = disabled)")
	flag.DurationVar(&cfg.ConfirmationCaptureDuration, "confirmation-capture-duration", cfg.ConfirmationCaptureDuration, "audio capture duration for confirmation call")
	flag.IntVar(&cfg.ConfirmationBypassScore, "confirmation-bypass-score", cfg.ConfirmationBypassScore, "skip confirmation when initial provider score is >= this value (0 = always confirm)")
	flag.StringVar(&cfg.ShazamPythonBin, "shazam-python", cfg.ShazamPythonBin, "path to Python binary with shazamio installed (empty to disable Shazamio / community client)")
	flag.DurationVar(&cfg.ShazamContinuityInterval, "shazam-continuity-interval", cfg.ShazamContinuityInterval, "how often to run Shazamio continuity checks for the current track")
	flag.DurationVar(&cfg.ShazamContinuityCaptureDuration, "shazam-continuity-capture-duration", cfg.ShazamContinuityCaptureDuration, "audio capture duration per periodic Shazamio continuity check")
	flag.DurationVar(&cfg.ContinuityCalibrationGrace, "continuity-calibration-grace", cfg.ContinuityCalibrationGrace, "grace period (after recognition) during which continuity monitor is in learning mode")
	flag.DurationVar(&cfg.ContinuityMismatchConfirmWindow, "continuity-mismatch-confirm-window", cfg.ContinuityMismatchConfirmWindow, "time window for counting repeated track-change sightings toward confirmation")
	flag.IntVar(&cfg.ContinuityRequiredSightingsCalibrated, "continuity-required-sightings-calibrated", cfg.ContinuityRequiredSightingsCalibrated, "number of repeated sightings of same track change (when calibrated) before re-recognition triggers")
	flag.IntVar(&cfg.ContinuityRequiredSightingsUncalibrated, "continuity-required-sightings-uncalibrated", cfg.ContinuityRequiredSightingsUncalibrated, "stricter threshold during calibration grace period to prevent false positives")
	flag.DurationVar(&cfg.EarlyCheckMargin, "early-check-margin", cfg.EarlyCheckMargin, "how close to track end the continuity monitor becomes more sensitive")
	flag.DurationVar(&cfg.DurationGuardBypassWindow, "duration-guard-bypass-window", cfg.DurationGuardBypassWindow, "time window after potential false boundary during which duration suppression guard is armed")
	flag.Float64Var(&cfg.DurationPessimism, "duration-pessimism", cfg.DurationPessimism, "temporal threshold (0.0–1.0): below threshold VU boundaries are guarded, at/above threshold VU boundaries are ignored")
	flag.DurationVar(&cfg.BoundaryRestoreMinSeek, "boundary-restore-min-seek", cfg.BoundaryRestoreMinSeek, "minimum pre-boundary seek required before restoring pre-boundary track metadata after same-track re-confirmation")
	flag.StringVar(&cfg.RecognizerChain, "recognizer-chain", cfg.RecognizerChain, "recognition chain: acrcloud_first | shazam_first | acrcloud_only | shazam_only | audd_first | audd_only (optional AudD token inserts AudD into mixed chains; continuity uses shazamio when available)")
	flag.Parse()

	applyRecognitionProvidersFromConfigFile(&cfg)

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
			m.lib = lib
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

	// SIGUSR1 forces an immediate boundary-type recognition attempt — useful when
	// the VU monitor misses a track change (e.g. very short inter-track silence).
	// oceano-web sends this via: systemctl kill --kill-who=main --signal=SIGUSR1 oceano-state-manager.service
	usr1Ch := make(chan os.Signal, 1)
	signal.Notify(usr1Ch, syscall.SIGUSR1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-usr1Ch:
				log.Printf("recognizer: SIGUSR1 received — forcing immediate recognition")
				select {
				case m.recognizeTrigger <- triggerBoundaryRecognition(false):
				default:
				}
			}
		}
	}()

	go m.runShairportReader(ctx)
	go m.runBluetoothMonitor(ctx)
	go m.runSourceWatcher(ctx)
	go m.runVUMonitor(ctx)
	go m.runRecognizer(ctx, rec, confirmRec, shazamRec, lib)
	go m.runShazamContinuityMonitor(ctx, shazamRec)
	go m.runLibrarySync(ctx, lib)
	go m.runStatsLogger(ctx, lib)
	go m.runPlayHistoryRecorder(ctx)
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
