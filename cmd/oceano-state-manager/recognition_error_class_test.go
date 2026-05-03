package main

import (
	"context"
	"errors"
	"testing"

	internalrecognition "github.com/alemser/oceano-player/internal/recognition"
)

func TestRecognitionErrorClass(t *testing.T) {
	if g := recognitionErrorClass(internalrecognition.ErrRateLimit); g != "rate_limit" {
		t.Fatalf("got %q", g)
	}
	if g := recognitionErrorClass(context.Canceled); g != "canceled" {
		t.Fatalf("got %q", g)
	}
	if g := recognitionErrorClass(context.DeadlineExceeded); g != "deadline" {
		t.Fatalf("got %q", g)
	}
	if g := recognitionErrorClass(errors.New("random")); g != "other" {
		t.Fatalf("got %q", g)
	}
}
