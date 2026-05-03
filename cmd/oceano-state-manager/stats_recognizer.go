package main

import (
	"context"
	"time"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

// statsRecognizer wraps any Recognizer to record attempts and results in the library DB.
type statsRecognizer struct {
	inner        Recognizer
	lib          *internallibrary.Library
	nameOverride string // if non-empty, used instead of inner.Name() for stat recording and Name()
}

func (s *statsRecognizer) Name() string {
	if s.nameOverride != "" {
		return s.nameOverride
	}
	return s.inner.Name()
}

func (s *statsRecognizer) Recognize(ctx context.Context, wavPath string) (*RecognitionResult, error) {
	if s.lib == nil {
		return s.inner.Recognize(ctx, wavPath)
	}

	s.lib.RecordRecognitionEvent(s.Name(), "attempt")
	start := time.Now()
	res, err := s.inner.Recognize(ctx, wavPath)
	latency := time.Since(start)
	if meta := internallibrary.RecognitionAttemptContextFrom(ctx); meta != nil {
		outcome := "success"
		errClass := ""
		if err != nil {
			outcome = "error"
			errClass = recognitionErrorClass(err)
		} else if res == nil {
			outcome = "no_match"
		}
		s.lib.InsertRecognitionAttempt(meta, s.Name(), outcome, errClass, latency)
	}
	if err != nil {
		s.lib.RecordRecognitionEvent(s.Name(), "error")
		return nil, err
	}
	if res != nil {
		s.lib.RecordRecognitionEvent(s.Name(), "success")
	} else {
		s.lib.RecordRecognitionEvent(s.Name(), "no_match")
	}
	return res, nil
}

func wrapWithStats(r Recognizer, lib *internallibrary.Library) Recognizer {
	if r == nil || lib == nil {
		return r
	}
	return &statsRecognizer{inner: r, lib: lib}
}

// wrapWithStatsAs is like wrapWithStats but records events under name instead of r.Name().
// Use this when the same underlying recognizer is used in two distinct roles and you want
// separate counters per role (e.g. "Shazamio" for chain calls vs "ShazamioContinuity" for
// the continuity monitor).
func wrapWithStatsAs(r Recognizer, lib *internallibrary.Library, name string) Recognizer {
	if r == nil || lib == nil {
		return r
	}
	return &statsRecognizer{inner: r, lib: lib, nameOverride: name}
}
