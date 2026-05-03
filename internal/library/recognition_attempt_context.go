package library

import "context"

type recognitionAttemptCtxKey struct{}

// RecognitionAttemptContext carries per-session fields for append-only provider
// attempt telemetry (trigger, capture geometry, optional RMS of the WAV sent to
// providers). The same format_key normalization as rms_learning applies to
// PhysicalFormat so aggregates can join histogram rows.
type RecognitionAttemptContext struct {
	Trigger             string // boundary | fallback_timer
	BoundaryEventID     int64
	IsHardBoundary      bool
	Phase               string // primary | confirmation
	SkipMs              int
	CaptureDurationMs   int
	RMSMean             float64
	RMSPeak             float64
	PhysicalFormat      string // normalized key: vinyl | cd | physical
}

// WithRecognitionAttemptContext attaches meta for statsRecognizer / InsertRecognitionAttempt.
func WithRecognitionAttemptContext(ctx context.Context, meta *RecognitionAttemptContext) context.Context {
	if meta == nil {
		return ctx
	}
	return context.WithValue(ctx, recognitionAttemptCtxKey{}, meta)
}

// RecognitionAttemptContextFrom returns attached attempt context, or nil.
func RecognitionAttemptContextFrom(ctx context.Context) *RecognitionAttemptContext {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(recognitionAttemptCtxKey{}).(*RecognitionAttemptContext)
	return v
}
