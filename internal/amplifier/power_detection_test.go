package amplifier

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"os"
	"testing"
	"time"
)

// shortSockPath returns a short Unix socket path under /tmp to stay within
// the 104-character macOS limit. t.TempDir() produces paths that are too long.
func shortSockPath(t *testing.T, name string) string {
	t.Helper()
	p := fmt.Sprintf("/tmp/vu-%d-%s.sock", os.Getpid(), name)
	t.Cleanup(func() { os.Remove(p) })
	return p
}

// --- classifyNoiseFloor ---

func TestClassifyNoiseFloor_On(t *testing.T) {
	cases := []float64{noiseFloorOnThreshold, noiseFloorOnThreshold + 0.001, 0.05, 0.5}
	for _, rms := range cases {
		if got := classifyNoiseFloor(rms); got != PowerStateOn {
			t.Errorf("classifyNoiseFloor(%.4f) = %q, want %q", rms, got, PowerStateOn)
		}
	}
}

func TestClassifyNoiseFloor_Off(t *testing.T) {
	cases := []float64{
		noiseFloorOffThreshold,
		noiseFloorOffThreshold + 0.001,
		noiseFloorOnThreshold - 0.001,
	}
	for _, rms := range cases {
		if got := classifyNoiseFloor(rms); got != PowerStateOff {
			t.Errorf("classifyNoiseFloor(%.4f) = %q, want %q", rms, got, PowerStateOff)
		}
	}
}

func TestClassifyNoiseFloor_Unknown(t *testing.T) {
	cases := []float64{0.0, noiseFloorOffThreshold - 0.001, 0.0001}
	for _, rms := range cases {
		if got := classifyNoiseFloor(rms); got != PowerStateUnknown {
			t.Errorf("classifyNoiseFloor(%.4f) = %q, want %q", rms, got, PowerStateUnknown)
		}
	}
}

// --- checkNoiseFloor via mock VU socket ---

// startMockVUSocket starts a Unix socket server that sends frames with the
// given constant RMS value (same for L and R) until the client disconnects.
func startMockVUSocket(t *testing.T, rms float32) string {
	t.Helper()
	sockPath := shortSockPath(t, fmt.Sprintf("mock%d", time.Now().UnixNano()))

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint32(buf[0:4], math.Float32bits(rms))
		binary.LittleEndian.PutUint32(buf[4:8], math.Float32bits(rms))
		for {
			if _, err := conn.Write(buf); err != nil {
				return
			}
		}
	}()

	// Give the goroutine time to start listening.
	time.Sleep(5 * time.Millisecond)
	return sockPath
}

func TestCheckNoiseFloor_ReturnsApproximateRMS(t *testing.T) {
	want := float32(0.015)
	sockPath := startMockVUSocket(t, want)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	got, err := checkNoiseFloor(ctx, sockPath)
	if err != nil {
		t.Fatalf("checkNoiseFloor: %v", err)
	}

	// The mock sends identical L and R values so avg = want exactly.
	diff := got - float64(want)
	if diff < -0.001 || diff > 0.001 {
		t.Errorf("checkNoiseFloor = %.4f, want ≈ %.4f", got, want)
	}
}

func TestCheckNoiseFloor_UnavailableSocket(t *testing.T) {
	ctx := context.Background()
	_, err := checkNoiseFloor(ctx, shortSockPath(t, "missing"))
	if err == nil {
		t.Error("expected error for missing socket")
	}
}

func TestCheckNoiseFloor_EmptyPath(t *testing.T) {
	_, err := checkNoiseFloor(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty socket path")
	}
}

// --- DetectPowerState integration (Check 2 path via mock VU socket) ---

func newAmpWithVUSocket(t *testing.T, sockPath string) *BroadlinkAmplifier {
	t.Helper()
	origProbe := usbDACProbe
	usbDACProbe = func(context.Context, string) bool { return false }
	t.Cleanup(func() { usbDACProbe = origProbe })

	amp, err := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		Maker:          "Magnat",
		Model:          "NOMATCH-DEVICE-XYZZY", // ensure Check 1 (USB DAC) always fails
		Inputs:         testInputs,
		DefaultInputID: "USB",
		InputMode:      InputSelectionCycle,
		IRCodes:        cycleIRCodes,
		VUSocketPath:   sockPath,
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}
	return amp
}

func TestDetectPowerState_OnViaNoiseFloor(t *testing.T) {
	sockPath := startMockVUSocket(t, float32(noiseFloorOnThreshold+0.005))
	amp := newAmpWithVUSocket(t, sockPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	state, err := amp.DetectPowerState(ctx)
	if err != nil {
		t.Fatalf("DetectPowerState: %v", err)
	}
	if state != PowerStateOn {
		t.Errorf("DetectPowerState = %q, want %q", state, PowerStateOn)
	}
}

func TestDetectPowerState_OffViaNoiseFloor(t *testing.T) {
	// RMS between offThreshold and onThreshold → amp is off/standby.
	rms := float32((noiseFloorOffThreshold + noiseFloorOnThreshold) / 2)
	sockPath := startMockVUSocket(t, rms)
	amp := newAmpWithVUSocket(t, sockPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	state, err := amp.DetectPowerState(ctx)
	if err != nil {
		t.Fatalf("DetectPowerState: %v", err)
	}
	if state != PowerStateOff {
		t.Errorf("DetectPowerState = %q, want %q", state, PowerStateOff)
	}
}

func TestDetectPowerState_UnknownWhenNoSocket(t *testing.T) {
	amp := newAmpWithVUSocket(t, "") // no VU socket → Check 2 skipped

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	state, err := amp.DetectPowerState(ctx)
	if err != nil {
		t.Fatalf("DetectPowerState: %v", err)
	}
	if state != PowerStateUnknown {
		t.Errorf("DetectPowerState = %q, want %q", state, PowerStateUnknown)
	}
}

func TestDetectPowerState_UnknownWhenSocketMissing(t *testing.T) {
	sockPath := shortSockPath(t, "absent")
	amp := newAmpWithVUSocket(t, sockPath)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	state, err := amp.DetectPowerState(ctx)
	if err != nil {
		t.Fatalf("DetectPowerState: %v", err)
	}
	if state != PowerStateUnknown {
		t.Errorf("DetectPowerState = %q, want %q", state, PowerStateUnknown)
	}
}

// --- PowerStateMonitor ---

func TestPowerStateMonitor_InitialStateUnknown(t *testing.T) {
	amp := newAmpWithVUSocket(t, "") // no socket → always Unknown
	m := NewPowerStateMonitor(amp, time.Hour)
	state, at := m.Current()
	if state != PowerStateUnknown {
		t.Errorf("initial state = %q, want unknown", state)
	}
	if !at.IsZero() {
		t.Error("updatedAt should be zero before first detection")
	}
}

func TestPowerStateMonitor_BroadcastsOnChange(t *testing.T) {
	// First detection: socket missing → Unknown.
	// We use a socket path that exists only after we create it.
	sockPath := shortSockPath(t, "monitor")
	amp := newAmpWithVUSocket(t, sockPath)

	m := NewPowerStateMonitor(amp, 50*time.Millisecond)
	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	// monCtx drives the monitor; give it well more than vuSampleDuration (3 s)
	// so at least one full detection cycle completes before cancellation.
	monCtx, monCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer monCancel()

	// Start in background; first detection will fire immediately (socket missing → Unknown).
	go m.Start(monCtx)

	// Now bring up the socket serving an "on" RMS so the next poll transitions state.
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 8)
		rms := float32(noiseFloorOnThreshold + 0.01)
		binary.LittleEndian.PutUint32(buf[0:4], math.Float32bits(rms))
		binary.LittleEndian.PutUint32(buf[4:8], math.Float32bits(rms))
		for {
			if _, err := conn.Write(buf); err != nil {
				return
			}
		}
	}()

	// Use time.After rather than ctx.Done() so the assertion deadline is
	// independent of the monitor context lifetime. Detection takes up to
	// vuSampleDuration (3 s); allow 6 s for a generous margin.
	select {
	case got := <-ch:
		if got != PowerStateOn {
			t.Errorf("broadcast state = %q, want %q", got, PowerStateOn)
		}
	case <-time.After(6 * time.Second):
		t.Error("timed out waiting for power state broadcast")
	}
}
