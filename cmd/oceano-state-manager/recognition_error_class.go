package main

import (
	"context"
	"errors"
	"net"

	internalrecognition "github.com/alemser/oceano-player/internal/recognition"
)

// recognitionErrorClass maps errors to a short stable label for SQLite telemetry.
func recognitionErrorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, internalrecognition.ErrRateLimit) {
		return "rate_limit"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	var ne net.Error
	if errors.As(err, &ne) {
		if ne.Timeout() {
			return "timeout"
		}
		return "network"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	return "other"
}
