package main

import (
	"context"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

// statsRecognizer wraps any Recognizer to record attempts and results in the library DB.
type statsRecognizer struct {
	inner Recognizer
	lib   *internallibrary.Library
}

func (s *statsRecognizer) Name() string {
	return s.inner.Name()
}

func (s *statsRecognizer) Recognize(ctx context.Context, wavPath string) (*RecognitionResult, error) {
	if s.lib == nil {
		return s.inner.Recognize(ctx, wavPath)
	}

	s.lib.RecordRecognitionEvent(s.inner.Name(), "attempt")
	res, err := s.inner.Recognize(ctx, wavPath)
	if err != nil {
		s.lib.RecordRecognitionEvent(s.inner.Name(), "error")
		return nil, err
	}
	if res != nil {
		s.lib.RecordRecognitionEvent(s.inner.Name(), "success")
	} else {
		s.lib.RecordRecognitionEvent(s.inner.Name(), "no_match")
	}
	return res, nil
}

func wrapWithStats(r Recognizer, lib *internallibrary.Library) Recognizer {
	if r == nil || lib == nil {
		return r
	}
	return &statsRecognizer{inner: r, lib: lib}
}
