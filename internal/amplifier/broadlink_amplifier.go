package amplifier

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// InputCyclingSettings controls the optional input-cycling probe used as a
// last resort to confirm power-on when USB and RMS are both inconclusive.
type InputCyclingSettings struct {
	// Enabled controls whether input cycling is attempted.
	Enabled bool
	// Direction is "prev" or "next" — which navigation command to send each step.
	Direction string
	// MaxCycles is the maximum number of input steps before giving up.
	MaxCycles int
	// StepWait is how long to wait after each step before checking USB DAC.
	StepWait time.Duration
	// MinSilence is the minimum duration RMS must have been near zero before
	// cycling is permitted. Prevents interrupting active playback.
	MinSilence time.Duration
}

// NoiseFloorCalibration stores OFF/ON RMS references for one configured input.
// It is used by DetectPowerState to classify "amp on, silent" more reliably
// than a single global threshold.
type NoiseFloorCalibration struct {
	InputID string
	OffRMS  float64
	OnRMS   float64
}

// AmplifierSettings holds all configuration needed to construct a BroadlinkAmplifier.
type AmplifierSettings struct {
	Maker string
	Model string
	// IRCodes maps command names to base64-encoded Broadlink IR codes.
	// Keys: "power_on", "power_off", "volume_up", "volume_down", "next_input", "prev_input"
	IRCodes map[string]string

	// VUSocketPath is the Unix socket path for the VU frame stream produced by
	// oceano-source-detector. Used by DetectPowerState to read the REC-OUT
	// noise floor. Leave empty to skip noise floor analysis.
	VUSocketPath string

	// DACMatchString is the substring searched in "aplay -l" to detect the
	// amplifier's USB DAC (Check 1 of DetectPowerState). If empty, Model is used.
	DACMatchString string

	// WarmUp is how long to wait after a power-on command before the amp is ready.
	WarmUp time.Duration

	// StandbyTimeout is the amp's auto-standby delay. After this much silence the
	// monitor infers PowerStateStandby.
	StandbyTimeout time.Duration

	// InputCycling controls the optional last-resort cycling probe.
	InputCycling InputCyclingSettings

	// PowerNoiseFloor optionally overrides the fixed RMS threshold with a
	// calibration derived from the selected amplifier input.
	PowerNoiseFloor *NoiseFloorCalibration
}

// BroadlinkAmplifier implements Amplifier for any IR-controlled amplifier
// reachable via a Broadlink RM4 Mini. All IR commands are stateless — no
// position or power state is tracked in software.
type BroadlinkAmplifier struct {
	mu       sync.Mutex
	client   BroadlinkClient
	settings AmplifierSettings
}

// NewBroadlinkAmplifier constructs a BroadlinkAmplifier ready for use.
func NewBroadlinkAmplifier(client BroadlinkClient, settings AmplifierSettings) (*BroadlinkAmplifier, error) {
	if settings.Maker == "" || settings.Model == "" {
		return nil, fmt.Errorf("amplifier: maker and model are required")
	}
	return &BroadlinkAmplifier{client: client, settings: settings}, nil
}

// NewBroadlinkAmplifierForDetection constructs a minimal BroadlinkAmplifier
// used only for hardware power detection (no IR commands needed).
func NewBroadlinkAmplifierForDetection(settings AmplifierSettings) *BroadlinkAmplifier {
	return &BroadlinkAmplifier{settings: settings}
}

func (a *BroadlinkAmplifier) Maker() string { return a.settings.Maker }
func (a *BroadlinkAmplifier) Model() string { return a.settings.Model }

func (a *BroadlinkAmplifier) irCode(name string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	code := a.settings.IRCodes[name]
	if code == "" {
		return "", fmt.Errorf("IR code for %q not configured", name)
	}
	return code, nil
}

func (a *BroadlinkAmplifier) sendIR(name string) error {
	code, err := a.irCode(name)
	if err != nil {
		return err
	}
	return a.client.SendIRCode(code)
}

func (a *BroadlinkAmplifier) PowerOn() error  { return a.sendIR("power_on") }
func (a *BroadlinkAmplifier) PowerOff() error { return a.sendIR("power_off") }

func (a *BroadlinkAmplifier) VolumeUp() error   { return a.sendIR("volume_up") }
func (a *BroadlinkAmplifier) VolumeDown() error { return a.sendIR("volume_down") }

func (a *BroadlinkAmplifier) NextInput() error { return a.sendIR("next_input") }
func (a *BroadlinkAmplifier) PrevInput() error { return a.sendIR("prev_input") }

// IsUSBDACPresent returns true when the configured USB DAC match string is
// visible in the current ALSA playback device list.
func (a *BroadlinkAmplifier) IsUSBDACPresent(ctx context.Context) bool {
	dacMatch := a.settings.DACMatchString
	if dacMatch == "" {
		dacMatch = a.settings.Model
	}
	return usbDACProbe(ctx, dacMatch)
}

// Transport commands are not supported on amplifiers.
func (a *BroadlinkAmplifier) Play() error     { return ErrNotSupported }
func (a *BroadlinkAmplifier) Pause() error    { return ErrNotSupported }
func (a *BroadlinkAmplifier) Stop() error     { return ErrNotSupported }
func (a *BroadlinkAmplifier) Next() error     { return ErrNotSupported }
func (a *BroadlinkAmplifier) Previous() error { return ErrNotSupported }

// ProbeWithInputCycling implements InputCycler. It cycles through inputs using
// IR commands until the USB DAC appears or the context is cancelled/MaxCycles
// is exhausted. Each step sends one navigation IR command, waits StepWait, then
// checks for USB DAC presence.
//
// Only call this when nothing is playing (silence confirmed by the caller) to
// avoid interrupting the user's listening session.
func (a *BroadlinkAmplifier) ProbeWithInputCycling(ctx context.Context) (PowerState, error) {
	cyc := a.settings.InputCycling
	if !cyc.Enabled || cyc.MaxCycles <= 0 {
		return PowerStateUnknown, nil
	}
	if a.client == nil {
		return PowerStateUnknown, fmt.Errorf("no IR client configured for input cycling")
	}

	dacMatch := a.settings.DACMatchString
	if dacMatch == "" {
		dacMatch = a.settings.Model
	}

	irCmd := "prev_input"
	if cyc.Direction == "next" {
		irCmd = "next_input"
	}

	stepWait := cyc.StepWait
	if stepWait <= 0 {
		stepWait = 3 * time.Second
	}

	for i := 0; i < cyc.MaxCycles; i++ {
		if ctx.Err() != nil {
			return PowerStateUnknown, ctx.Err()
		}
		if err := a.sendIR(irCmd); err != nil {
			return PowerStateUnknown, fmt.Errorf("input cycling IR send: %w", err)
		}
		select {
		case <-ctx.Done():
			return PowerStateUnknown, ctx.Err()
		case <-time.After(stepWait):
		}
		if usbDACProbe(ctx, dacMatch) {
			log.Printf("amplifier: input cycling found USB DAC after %d step(s)", i+1)
			return PowerStateOn, nil
		}
	}

	log.Printf("amplifier: input cycling exhausted %d steps without finding USB DAC", cyc.MaxCycles)
	return PowerStateUnknown, nil
}

// DetectPowerState probes hardware through a two-check cascade:
//  1. USB DAC discovery — if the amplifier appears in "aplay -l", it is on.
//  2. REC-OUT noise floor — read VU socket RMS and classify against thresholds.
//
// Returns PowerStateUnknown when neither check is conclusive.
func (a *BroadlinkAmplifier) DetectPowerState(ctx context.Context) (PowerState, error) {
	// Check 1: USB DAC presence.
	dacMatch := a.settings.DACMatchString
	if dacMatch == "" {
		dacMatch = a.settings.Model
	}
	if usbDACProbe(ctx, dacMatch) {
		return PowerStateOn, nil
	}

	// Check 2: REC-OUT noise floor via VU socket.
	if a.settings.VUSocketPath != "" {
		rms, err := checkNoiseFloor(ctx, a.settings.VUSocketPath)
		if err == nil {
			if s := classifyNoiseFloorWithCalibration(rms, a.settings.PowerNoiseFloor); s != PowerStateUnknown {
				return s, nil
			}
		}
	}

	return PowerStateUnknown, nil
}
