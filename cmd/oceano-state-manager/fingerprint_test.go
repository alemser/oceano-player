package main

import (
	"fmt"
	"math"
	"testing"
)

// ── BER unit tests ────────────────────────────────────────────────────────────

func TestBER_identical(t *testing.T) {
	a := Fingerprint{0xDEADBEEF, 0x12345678, 0xABCDEF01}
	if got := BER(a, a, 0); got != 0.0 {
		t.Errorf("identical fingerprints: BER = %v, want 0.0", got)
	}
}

func TestBER_allDifferent(t *testing.T) {
	a := Fingerprint{0x00000000}
	b := Fingerprint{0xFFFFFFFF}
	if got := BER(a, b, 0); got != 1.0 {
		t.Errorf("all-bits-different: BER = %v, want 1.0", got)
	}
}

func TestBER_halfDifferent(t *testing.T) {
	// 0xAAAAAAAA has 16 bits set; XOR with 0x00000000 gives 16 differing bits → BER = 0.5
	a := Fingerprint{0xAAAAAAAA}
	b := Fingerprint{0x00000000}
	got := BER(a, b, 0)
	if math.Abs(got-0.5) > 1e-9 {
		t.Errorf("half-different: BER = %v, want 0.5", got)
	}
}

func TestBER_belowThreshold(t *testing.T) {
	// Build two fingerprints where ~10% of bits differ — well below 0.35.
	n := 100
	a := make(Fingerprint, n)
	b := make(Fingerprint, n)
	for i := range a {
		a[i] = 0x00000000
		b[i] = 0x00000000
	}
	// Flip 3 bits in one value → 3/(100*32) ≈ 0.0009
	b[0] = 0x00000007
	got := BER(a, b, 0)
	if got >= 0.35 {
		t.Errorf("near-identical fingerprints: BER = %v, want < 0.35", got)
	}
}

func TestBER_shiftedMatch(t *testing.T) {
	// a = [0x33, 0x44, 0x55], b = [0x11, 0x22, 0x33]
	// Overlap at shift=+2 (b shifted right by 2): a[0..] vs b[2..] → both are [0x33] → BER=0.
	base := Fingerprint{0x11111111, 0x22222222, 0x33333333, 0x44444444, 0x55555555}
	a := base[2:] // [0x33, 0x44, 0x55]
	b := base[:3] // [0x11, 0x22, 0x33]
	got := BER(a, b, 3)
	if got != 0.0 {
		t.Errorf("shifted match: BER = %v, want 0.0", got)
	}
}

func TestBER_noOverlap(t *testing.T) {
	a := Fingerprint{0x11111111}
	b := Fingerprint{0x22222222}
	// maxShift=0 means only exact alignment; overlap is 1 value.
	got := BER(a, b, 0)
	if got >= 1.0 {
		t.Errorf("single-value mismatch should be < 1.0, got %v", got)
	}
	// maxShift large enough to push all overlap away → returns 1.0
	got2 := BER(a, b, 2)
	if got2 > got {
		t.Errorf("larger maxShift should not make BER worse: got2=%v got=%v", got2, got)
	}
}

func TestBER_empty(t *testing.T) {
	if got := BER(nil, Fingerprint{1, 2, 3}, 5); got != 1.0 {
		t.Errorf("empty a: BER = %v, want 1.0", got)
	}
	if got := BER(Fingerprint{1, 2, 3}, nil, 5); got != 1.0 {
		t.Errorf("empty b: BER = %v, want 1.0", got)
	}
}

// ── ParseFingerprint ──────────────────────────────────────────────────────────

func TestParseFingerprint(t *testing.T) {
	fp, err := ParseFingerprint("1234567890,987654321,0,4294967295")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Fingerprint{1234567890, 987654321, 0, 4294967295}
	if len(fp) != len(want) {
		t.Fatalf("len = %d, want %d", len(fp), len(want))
	}
	for i := range want {
		if fp[i] != want[i] {
			t.Errorf("fp[%d] = %d, want %d", i, fp[i], want[i])
		}
	}
}

func TestParseFingerprint_empty(t *testing.T) {
	if _, err := ParseFingerprint(""); err == nil {
		t.Error("expected error for empty input")
	}
}

func TestParseFingerprint_invalid(t *testing.T) {
	if _, err := ParseFingerprint("123,abc,456"); err == nil {
		t.Error("expected error for non-numeric field")
	}
}

// ── GenerateFingerprints ──────────────────────────────────────────────────────

type mockFingerprinter struct {
	// calls records (offset, length) pairs passed to Generate.
	calls  [][2]int
	result Fingerprint
	err    error
}

func (m *mockFingerprinter) Generate(wavPath string, offsetSec, lengthSec int) (Fingerprint, error) {
	m.calls = append(m.calls, [2]int{offsetSec, lengthSec})
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func TestGenerateFingerprints_windowCount(t *testing.T) {
	fp := &mockFingerprinter{result: Fingerprint{1, 2, 3}}
	results := GenerateFingerprints(fp, "test.wav", 3, 4, 8, 20)
	// captureSec=20, stride=4: offsets 0, 4, 8 — all < 20 → 3 windows
	if len(results) != 3 {
		t.Errorf("got %d fingerprints, want 3", len(results))
	}
	wantOffsets := [][2]int{{0, 8}, {4, 8}, {8, 8}}
	for i, got := range fp.calls {
		if got != wantOffsets[i] {
			t.Errorf("call[%d] = %v, want %v", i, got, wantOffsets[i])
		}
	}
}

func TestGenerateFingerprints_capsAtCaptureDuration(t *testing.T) {
	fp := &mockFingerprinter{result: Fingerprint{1}}
	// captureSec=10, stride=4: offset 0 ok, offset 4 ok, offset 8 < 10 ok,
	// offset 12 >= 10 → stops at 3 windows even though maxWindows=5.
	results := GenerateFingerprints(fp, "test.wav", 5, 4, 8, 10)
	if len(results) != 3 {
		t.Errorf("got %d fingerprints, want 3", len(results))
	}
}

func TestGenerateFingerprints_nilFingerprinter(t *testing.T) {
	results := GenerateFingerprints(nil, "test.wav", 3, 4, 8, 20)
	if results != nil {
		t.Errorf("nil fingerprinter should return nil, got %v", results)
	}
}

func TestGenerateFingerprints_errorStops(t *testing.T) {
	fp := &mockFingerprinter{err: fmt.Errorf("fpcalc: not found")}
	results := GenerateFingerprints(fp, "test.wav", 3, 4, 8, 20)
	// First call errors → stops immediately.
	if len(results) != 0 {
		t.Errorf("got %d fingerprints after error, want 0", len(results))
	}
}
