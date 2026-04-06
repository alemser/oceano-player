package main

import "context"

// localOnlyRecognizer bypasses remote providers while still driving the
// recognition loop so local fingerprint fallback can identify tracks.
type localOnlyRecognizer struct{}

func (localOnlyRecognizer) Name() string { return "Fingerprint" }

func (localOnlyRecognizer) Recognize(context.Context, string) (*RecognitionResult, error) {
	return nil, nil
}