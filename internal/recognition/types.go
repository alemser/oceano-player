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
	// TrackNumber is the position of the track on the release (e.g. "3" for CD
	// track 3, "A2" for vinyl side A track 2). Populated from the library DB;
	// not currently returned by recognition providers.
	TrackNumber string
}

// Recognizer identifies a track from a WAV audio file.
// Implementations must be safe for concurrent use.
type Recognizer interface {
	Name() string
	Recognize(ctx context.Context, wavPath string) (*Result, error)
}

// Fingerprinter generates a raw Chromaprint fingerprint from a WAV file.
// Implementations must be safe for concurrent use.
type Fingerprinter interface {
	Generate(wavPath string, offsetSec, lengthSec int) (Fingerprint, error)
}
