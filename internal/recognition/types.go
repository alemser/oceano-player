package recognition

import (
	"context"
	"errors"
)

// ErrRateLimit is returned by a Recognizer when the provider signals that the
// request quota has been exceeded. The caller should back off before retrying
// or fall through to a fallback provider.
var ErrRateLimit = errors.New("recognition: rate limit exceeded")

// Result holds the identified track metadata.
type Result struct {
	ACRID    string
	ShazamID string
	ISRC     string // International Standard Recording Code; populated by ACRCloud when available
	Title    string
	Artist   string
	Album    string
	Label    string
	Released string
	Score    int
	Format   string
	// DurationMs is the track duration in milliseconds as reported by the
	// recognition provider (ACRCloud: duration_ms; Shazamio wire: matches[0].length).
	// Zero means the provider did not return a duration.
	DurationMs int
	// TrackNumber is the position of the track on the release (e.g. "3" for CD
	// track 3, "A2" for vinyl side A track 2). Populated from the library DB,
	// Discogs release tracklist enrichment, or user edits — not from primary
	// clip recognizers (ACR/Shazam/AudD) alone.
	TrackNumber string
	// MatchSource is a stable lowercase id for the API that produced this result
	// (e.g. "acrcloud", "shazam", "audd"). Used for UI state when ACRID/ShazamID
	// are empty. Not serialized on track payloads today.
	MatchSource string `json:"-"`
	// DiscogsURL is the Discogs release resource URL, populated by async
	// post-recognition Discogs enrichment. Empty until enrichment completes.
	DiscogsURL string
}

// Recognizer identifies a track from a WAV audio file.
// Implementations must be safe for concurrent use.
type Recognizer interface {
	Name() string
	Recognize(ctx context.Context, wavPath string) (*Result, error)
}
