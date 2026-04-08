package amplifier

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"os/exec"
	"strings"
	"time"
)

// noiseFloorOnThreshold is the minimum average RMS from the VU socket that
// indicates the amplifier is powered on (noise floor is audible on REC-OUT).
// Derived from Magnat MR 780 measurements: "on and silent" ≈ 0.0145.
const noiseFloorOnThreshold = 0.010

// noiseFloorOffThreshold is the RMS level below which the REC-OUT has no
// meaningful signal — amp is off/standby or capture card is disconnected.
// Derived from Magnat MR 780 measurements: "off" ≈ 0.0074.
const noiseFloorOffThreshold = 0.002

// vuSampleDuration is how long checkNoiseFloor reads frames before computing
// the average. Long enough for ~64 frames at the ~21.5 Hz VU frame rate.
const vuSampleDuration = 3 * time.Second

const usbDACProbeTimeout = 2 * time.Second

var usbDACProbe = checkUSBDACWithContext

// checkUSBDAC runs "aplay -l" and returns true if any playback device line
// contains model as a case-insensitive substring. This confirms the amplifier
// is powered on with its USB Audio input selected (DAC enumerated by the OS).
func checkUSBDACWithContext(ctx context.Context, model string) bool {
	if model == "" {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, usbDACProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, "aplay", "-l").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(model))
}

func checkUSBDAC(model string) bool {
	return checkUSBDACWithContext(context.Background(), model)
}

// checkNoiseFloor connects to the VU Unix socket, reads frames for
// vuSampleDuration (or until ctx is cancelled), and returns the average RMS.
// Returns an error if the socket cannot be reached or yields no frames.
func checkNoiseFloor(ctx context.Context, socketPath string) (float64, error) {
	if socketPath == "" {
		return 0, fmt.Errorf("VU socket path not configured")
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return 0, fmt.Errorf("connect to VU socket %s: %w", socketPath, err)
	}
	defer conn.Close()

	// Honour ctx deadline but cap at vuSampleDuration from now.
	deadline := time.Now().Add(vuSampleDuration)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetReadDeadline(deadline) //nolint:errcheck

	var sum float64
	var count int
	buf := make([]byte, 8)

	for {
		_, err := io.ReadFull(conn, buf)
		if err != nil {
			break // deadline reached or socket closed — normal exit
		}
		left := math.Float32frombits(binary.LittleEndian.Uint32(buf[0:4]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(buf[4:8]))
		sum += float64(left+right) / 2.0
		count++
	}

	if count == 0 {
		return 0, fmt.Errorf("no VU frames received from %s", socketPath)
	}
	return sum / float64(count), nil
}

// classifyNoiseFloor maps an average RMS value to a PowerState.
// Exposed for unit testing without requiring a live VU socket.
func classifyNoiseFloor(rms float64) PowerState {
	if rms >= noiseFloorOnThreshold {
		return PowerStateOn
	}
	if rms >= noiseFloorOffThreshold {
		return PowerStateOff
	}
	// Below noiseFloorOffThreshold: no signal at all — capture card may be
	// disconnected; cannot distinguish from amp-off.
	return PowerStateUnknown
}
