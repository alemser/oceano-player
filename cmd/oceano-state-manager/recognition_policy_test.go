package main

import "testing"

func TestPickLongerDurationMs(t *testing.T) {
	if got := pickLongerDurationMs(0, 720000); got != 720000 {
		t.Fatalf("got %d want 720000", got)
	}
	if got := pickLongerDurationMs(240000, 0); got != 240000 {
		t.Fatalf("got %d want 240000", got)
	}
	if got := pickLongerDurationMs(240000, 720000); got != 720000 {
		t.Fatalf("got %d want longer 720000", got)
	}
	if got := pickLongerDurationMs(720000, 240000); got != 720000 {
		t.Fatalf("got %d want longer 720000", got)
	}
	if got := pickLongerDurationMs(0, 0); got != 0 {
		t.Fatalf("got %d want 0", got)
	}
}
