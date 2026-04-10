package amplifier

import (
	"context"
	"fmt"
	"sync"
)

// AmplifierSettings holds all configuration needed to construct a BroadlinkAmplifier.
type AmplifierSettings struct {
	Maker  string
	Model  string
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

// Transport commands are not supported on amplifiers.
func (a *BroadlinkAmplifier) Play() error     { return ErrNotSupported }
func (a *BroadlinkAmplifier) Pause() error    { return ErrNotSupported }
func (a *BroadlinkAmplifier) Stop() error     { return ErrNotSupported }
func (a *BroadlinkAmplifier) Next() error     { return ErrNotSupported }
func (a *BroadlinkAmplifier) Previous() error { return ErrNotSupported }

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
			if s := classifyNoiseFloor(rms); s != PowerStateUnknown {
				return s, nil
			}
		}
	}

	return PowerStateUnknown, nil
}
