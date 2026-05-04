package main

import (
	"regexp"
	"time"
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

// RecognitionStatus describes the current state of the physical track recognizer.
// Only present in the state JSON when source is Physical (including CD/Vinyl).
type RecognitionStatus struct {
	// Phase is one of: "identifying" (capture in progress or first trigger pending),
	// "matched" (recognition succeeded), "no_match" (last attempt returned no result),
	// "off" (recognition disabled for the active input),
	// "not_configured" (no recognition.providers in config — operator setup required).
	Phase    string `json:"phase"`
	Provider string `json:"provider,omitempty"` // "acrcloud" | "shazam" | "audd" — set when phase is "matched"
	Score    int    `json:"score,omitempty"`    // provider confidence score; 0 when unavailable
	// Detail is a stable machine-readable reason for the current phase, for example:
	// "input_policy_off", "no_match", "capturing", "waiting_trigger".
	Detail string `json:"detail,omitempty"`
	// ActiveInputID / ActiveInputName describe the amplifier input used for per-input
	// recognition policy (from calibration config). Empty when unknown.
	ActiveInputID   string `json:"active_input_id,omitempty"`
	ActiveInputName string `json:"active_input_name,omitempty"`
	// RateLimitedProviders lists canonical provider IDs currently in rate-limit
	// backoff: "acrcloud", "shazam", "audd". Omitted when empty.
	RateLimitedProviders []string `json:"rate_limited_providers,omitempty"`
	// BackoffExpires maps each rate-limited provider ID to its backoff expiry as a
	// Unix epoch second (UTC). Omitted when no providers are rate-limited. iOS
	// clients should use this to compute relative durations ("disponível em ~12 min")
	// without timezone ambiguity.
	BackoffExpires map[string]int64 `json:"backoff_expires,omitempty"`
}

// VuLevels holds the latest stereo meter snapshot from oceano-source-detector
// (float32 RMS-ish levels, same units as the VU socket). Written to unified state
// for HDMI / optional clients; lightweight clients should use GET /api/stream?vu=0
// or GET /api/player/summary to skip this payload.
type VuLevels struct {
	Left  float64 `json:"left"`
	Right float64 `json:"right"`
}

// AirPlayTransportStatus reports DACP transport readiness for the current AirPlay
// session. This allows clients to render transport controls only when the backend
// has a fresh and complete remote-control context.
type AirPlayTransportStatus struct {
	Available    bool   `json:"available"`
	SessionState string `json:"session_state"`    // ready | no_airplay_session | missing_dacp_context | session_stale
	Reason       string `json:"reason,omitempty"` // machine-readable explanation when unavailable
	ActiveRemote string `json:"active_remote,omitempty"`
	DACPID       string `json:"dacp_id,omitempty"`
	ClientIP     string `json:"client_ip,omitempty"`
}

// PlayerState is the unified state written to /tmp/oceano-state.json.
type PlayerState struct {
	Source string     `json:"source"`           // AirPlay | Vinyl | CD | Physical | None
	Format string     `json:"format,omitempty"` // CD | Vinyl — only present when source is Physical with identified format
	State  string     `json:"state"`            // playing | idle | stopped
	Track  *TrackInfo `json:"track"`            // null when not playing or source is physical without metadata
	// Recognition is only present when source is Physical and the physical
	// detector is currently active. It exposes recognizer phase so the UI can
	// distinguish "identifying", "matched", "no_match", and "off" states.
	Recognition *RecognitionStatus `json:"recognition,omitempty"`
	// ProviderBackoff maps canonical provider IDs ("acrcloud", "shazam", "audd")
	// to their rate-limit backoff expiry as a Unix epoch second (UTC). Present
	// regardless of the active source — a provider can be rate-limited from a
	// Physical session even while AirPlay or Bluetooth is playing. Omitted when
	// no providers are in backoff. Clients should prefer this over
	// recognition.backoff_expires for config-screen health views, as recognition
	// is omitted when source is not Physical.
	ProviderBackoff map[string]int64 `json:"provider_backoff,omitempty"`
	// PhysicalDetectorActive is true only while /tmp/oceano-source.json reports
	// Physical. False during the post-Physical idle-delay tail when source is
	// still promoted to CD/Vinyl for UI grace — lets the display avoid "Identifying…"
	// from REC noise after the amp left the physical path.
	PhysicalDetectorActive bool `json:"physical_detector_active"`
	// AirPlayTransport reports whether DACP transport commands are currently
	// possible for the active AirPlay session.
	AirPlayTransport *AirPlayTransportStatus `json:"airplay_transport,omitempty"`
	Vu               *VuLevels               `json:"vu,omitempty"`
	UpdatedAt        string                  `json:"updated_at"`
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

// RecognitionProviderSpec mirrors one entry in recognition.providers[] in config.json.
// Physical recognition runs only when this list contains at least one enabled
// primary provider backed by credentials / install (see recognition_setup.go).
// CredentialRef is parsed for forward compatibility (iOS relay / secrets); ignored at runtime for now.
type RecognitionProviderSpec struct {
	ID            string   `json:"id"`
	Enabled       bool     `json:"enabled"`
	Roles         []string `json:"roles"`
	CredentialRef string   `json:"credential_ref,omitempty"`
}

// --- Config ---

type Config struct {
	MetadataPipe string
	SourceFile   string
	OutputFile   string
	ArtworkDir   string
	Verbose      bool

	// Recognition — providers optional; disabled when no chain member is configured
	// (e.g. AudD-only needs audd_api_token; ACRCloud needs host + keys).
	ACRCloudHost      string
	ACRCloudAccessKey string
	ACRCloudSecretKey string
	// AudDAPIToken is the AudD API token (BYOK, https://docs.audd.io/). When non-empty,
	// AudD is available for chain policies that include it.
	AudDAPIToken string
	// ShazamioPythonBin is an optional CLI override for the Shazamio interpreter.
	// When empty, recognition_setup uses internal/recognition.BundledShazamioPythonBin.
	// Shazamio is constructed only when recognition.providers includes an enabled shazam entry.
	ShazamioPythonBin string
	// RecognizerChain is deprecated for runtime ordering (kept for systemd flag
	// compatibility and old JSON). Recognition is configured only via
	// recognition.providers in calibration-config JSON.
	// Valid values: "acrcloud_first" | "shazam_first" | "acrcloud_only" | "shazam_only" |
	// "audd_first" | "audd_only".
	// Deprecated for ordering; kept for systemd/JSON compatibility.
	RecognizerChain string
	// ShazamioContinuityInterval controls how often the Shazamio continuity path re-checks if the
	// current track is still playing (for soft/gapless transitions).
	ShazamioContinuityInterval time.Duration
	// ShazamioContinuityCaptureDuration is the capture duration used by periodic
	// Shazamio continuity checks.
	ShazamioContinuityCaptureDuration time.Duration
	// PCMSocket is the Unix socket path exposed by oceano-source-detector for raw PCM relay.
	// The recognizer reads from this socket so it never opens the ALSA device directly.
	PCMSocket string
	// RecognizerCaptureDuration is seconds of PCM per WAV for each recognition
	// attempt (one file for the full provider chain). Default matches
	// RecognitionConfig.CaptureDurationSecs in cmd/oceano-web/config.go; deployed
	// units normally pass --recognizer-capture-duration from that JSON via oceano-web.
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
	// VUSilenceThreshold is the RMS threshold used by the VU monitor to classify
	// frames as silence vs active audio for boundary detection.
	VUSilenceThreshold float64
	// CalibrationConfigPath points to oceano-web config.json containing
	// advanced.calibration_profiles and amplifier_runtime.last_known_input_id.
	CalibrationConfigPath string
	// IdleDelay is how long to keep showing the last physical track after audio stops
	// before switching to the idle screen. Defaults to 10 seconds.
	IdleDelay time.Duration
	// SessionGapThreshold is the maximum silence gap that is treated as an
	// inter-track pause rather than end of record. If the source goes None and
	// comes back Physical within this window, the existing recognition result is
	// kept and no new session is started. Set this longer than the longest expected
	// silence between tracks on your records. Defaults to 45 seconds.
	SessionGapThreshold time.Duration
	// LibraryDB is the path to the SQLite database used to record physical-media plays.
	// Set to empty string to disable library recording.
	LibraryDB string

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
	// ContinuityCalibrationGrace is the duration to wait before the Shazamio continuity
	// monitor starts checking for track changes. During this grace period after a
	// successful recognition, the monitor is in "learning" mode. Lower values = faster
	// gapless detection but more false positives. Default: 45 seconds.
	ContinuityCalibrationGrace time.Duration
	// ContinuityMismatchConfirmWindow is the time window during which repeated sightings
	// of the same track change (from→to pair) are counted toward confirmation. Default: 3 minutes.
	ContinuityMismatchConfirmWindow time.Duration
	// ContinuityRequiredSightingsCalibrated is the number of repeated sightings of the
	// same track change that must be observed (when calibrated) before re-recognition
	// is triggered. Default: 2 sightings.
	ContinuityRequiredSightingsCalibrated int
	// ContinuityRequiredSightingsUncalibrated is the stricter threshold used during the
	// grace period (when the monitor is still learning). Default: 3 sightings.
	ContinuityRequiredSightingsUncalibrated int
	// EarlyCheckMargin is how close to the end of the known track duration the continuity
	// monitor becomes more sensitive. When within this margin, the next Shazamio poll is
	// more sensitive to detect an upcoming track change. Default: 20 seconds.
	EarlyCheckMargin time.Duration
	// DurationGuardBypassWindow is the time window (after a potential false boundary is
	// detected) during which the duration-based suppression guard is armed. If a new
	// boundary is detected within this window, it is suppressed. Default: 20 seconds.
	DurationGuardBypassWindow time.Duration
	// DurationPessimism is the temporal threshold (0.0–1.0) used to guard against
	// false positive boundaries during quiet passages. If the detected duration since
	// the last boundary is < DurationPessimism * KnownTrackDuration, the boundary is
	// suppressed. Default: 0.75 (suppress if < 75% of known duration elapsed).
	DurationPessimism float64
	// BoundaryRestoreMinSeek is the minimum pre-boundary seek position required
	// before the coordinator is allowed to restore a pre-boundary recognition
	// result after a same-track re-confirmation. Lower values favor continuity;
	// higher values reduce false positives after manual needle repositioning.
	BoundaryRestoreMinSeek time.Duration

	// RecognitionProviders is loaded from recognition.providers in CalibrationConfigPath.
	// Empty or missing list disables physical recognition until configured.
	RecognitionProviders []RecognitionProviderSpec
	// RecognitionMergePolicy is recognition.merge_policy from config (default first_success).
	// Only first_success is implemented; other values are logged and treated as first_success until additional merge_policy modes exist.
	RecognitionMergePolicy string
}

func defaultConfig() Config {
	return Config{
		MetadataPipe:                            "/tmp/shairport-sync-metadata",
		SourceFile:                              "/tmp/oceano-source.json",
		OutputFile:                              "/tmp/oceano-state.json",
		ArtworkDir:                              "/var/lib/oceano/artwork",
		PCMSocket:                               "/tmp/oceano-pcm.sock",
		VUSocket:                                "/tmp/oceano-vu.sock",
		VUSilenceThreshold:                      0.0095,
		CalibrationConfigPath:                   "/etc/oceano/config.json",
		RecognizerCaptureDuration:               7 * time.Second,
		RecognizerMaxInterval:                   5 * time.Minute,
		RecognizerRefreshInterval:               2 * time.Minute,
		NoMatchBackoff:                          15 * time.Second,
		IdleDelay:                               10 * time.Second,
		SessionGapThreshold:                     45 * time.Second,
		LibraryDB:                               "/var/lib/oceano/library.db",
		ConfirmationDelay:                       0,
		ConfirmationCaptureDuration:             4 * time.Second,
		ConfirmationBypassScore:                 95,
		ShazamioPythonBin:                       "",
		ShazamioContinuityInterval:              8 * time.Second,
		ShazamioContinuityCaptureDuration:       4 * time.Second,
		BoundaryRestoreMinSeek:                  60 * time.Second,
		RecognizerChain:                         "acrcloud_first",
		ContinuityCalibrationGrace:              45 * time.Second,
		ContinuityMismatchConfirmWindow:         3 * time.Minute,
		ContinuityRequiredSightingsCalibrated:   2,
		ContinuityRequiredSightingsUncalibrated: 3,
		EarlyCheckMargin:                        20 * time.Second,
		DurationGuardBypassWindow:               20 * time.Second,
		DurationPessimism:                       0.75,
	}
}

type recognizeTrigger struct {
	isBoundary     bool
	isHardBoundary bool
	// detectedAt is the time the transition was first observed. Non-zero only
	// for continuity-monitor triggers, where the track change happened one or
	// more poll intervals before the confirmation fires. The coordinator uses
	// it as the seek anchor instead of time.Now() to avoid over-estimating
	// elapsed time in the new track.
	detectedAt time.Time
	// boundaryEventID is the SQLite row id from RecordBoundaryEvent when the VU
	// path inserts a "fired" row before enqueueing recognition (R7 follow-up).
	boundaryEventID int64
}

func triggerPeriodicRecognition() recognizeTrigger {
	return recognizeTrigger{}
}

func triggerBoundaryRecognition(isHard bool) recognizeTrigger {
	return recognizeTrigger{isBoundary: true, isHardBoundary: isHard}
}
