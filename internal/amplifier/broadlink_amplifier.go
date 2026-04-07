package amplifier

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// InputSelectionMode controls how SetInput sends IR commands to the amplifier.
type InputSelectionMode string

const (
	// InputSelectionCycle sends next_input repeatedly until the target input
	// is reached. Required for amplifiers without direct IR input codes
	// (e.g. Magnat MR 780, which uses a physical rotary knob).
	InputSelectionCycle InputSelectionMode = "cycle"

	// InputSelectionDirect sends a single IR code specific to the target input
	// (keyed as "input_<ID>" in IRCodes). Supported by most modern amplifiers.
	InputSelectionDirect InputSelectionMode = "direct"
)

// AmplifierSettings holds all configuration needed to construct a BroadlinkAmplifier.
// Values are populated from AmplifierConfig in cmd/oceano-web/config.go.
type AmplifierSettings struct {
	Maker           string
	Model           string
	Inputs          []Input
	DefaultInputID  string
	WarmupSecs      int
	SwitchDelaySecs int
	InputMode       InputSelectionMode
	// IRCodes maps command names to base64-encoded Broadlink IR codes.
	// Cycle mode keys: "power_on", "power_off", "volume_up", "volume_down", "next_input"
	// Direct mode adds: "input_<ID>" for each input (e.g. "input_USB", "input_PHONO")
	IRCodes map[string]string

	// VUSocketPath is the Unix socket path for the VU frame stream produced by
	// oceano-source-detector (default /tmp/oceano-vu.sock). Used by
	// DetectPowerState to read the REC-OUT noise floor (Check 2).
	// Leave empty to skip noise floor analysis.
	VUSocketPath string
}

// BroadlinkAmplifier implements Amplifier for any IR-controlled amplifier
// reachable via a Broadlink RM4 Mini. Device identity and behaviour are
// driven entirely by AmplifierSettings — no code changes are needed when
// switching to a different amplifier model.
type BroadlinkAmplifier struct {
	mu       sync.Mutex
	client   BroadlinkClient
	settings AmplifierSettings

	defaultInput Input

	// mutable state — always accessed under mu
	powerOn      bool
	currentInput Input
	audioReady   bool
	audioReadyAt time.Time
	cancelReady  context.CancelFunc
}

// NewBroadlinkAmplifier constructs a BroadlinkAmplifier ready for use.
// Returns an error if settings are invalid (empty inputs, unknown default input).
func NewBroadlinkAmplifier(client BroadlinkClient, settings AmplifierSettings) (*BroadlinkAmplifier, error) {
	if len(settings.Inputs) == 0 {
		return nil, fmt.Errorf("amplifier inputs must not be empty")
	}
	var defaultInput Input
	found := false
	for _, inp := range settings.Inputs {
		if inp.ID == settings.DefaultInputID {
			defaultInput = inp
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("defaultInputID %q not found in inputs", settings.DefaultInputID)
	}
	return &BroadlinkAmplifier{
		client:       client,
		settings:     settings,
		defaultInput: defaultInput,
		currentInput: defaultInput,
	}, nil
}

func (a *BroadlinkAmplifier) Maker() string { return a.settings.Maker }
func (a *BroadlinkAmplifier) Model() string { return a.settings.Model }

func (a *BroadlinkAmplifier) PowerOn() error {
	code, err := a.irCode("power_on")
	if err != nil {
		return err
	}
	if err := a.client.SendIRCode(code); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.powerOn = true
	a.audioReady = false
	a.startReadyTimerLocked(time.Duration(a.settings.WarmupSecs) * time.Second)
	return nil
}

func (a *BroadlinkAmplifier) PowerOff() error {
	code, err := a.irCode("power_off")
	if err != nil {
		return err
	}
	if err := a.client.SendIRCode(code); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.powerOn = false
	a.audioReady = false
	if a.cancelReady != nil {
		a.cancelReady()
		a.cancelReady = nil
	}
	return nil
}

func (a *BroadlinkAmplifier) VolumeUp() error {
	code, err := a.irCode("volume_up")
	if err != nil {
		return err
	}
	return a.client.SendIRCode(code)
}

func (a *BroadlinkAmplifier) VolumeDown() error {
	code, err := a.irCode("volume_down")
	if err != nil {
		return err
	}
	return a.client.SendIRCode(code)
}

// Play, Pause, Stop, Next, Previous are transport controls not applicable to amplifiers.
func (a *BroadlinkAmplifier) Play() error     { return ErrNotSupported }
func (a *BroadlinkAmplifier) Pause() error    { return ErrNotSupported }
func (a *BroadlinkAmplifier) Stop() error     { return ErrNotSupported }
func (a *BroadlinkAmplifier) Next() error     { return ErrNotSupported }
func (a *BroadlinkAmplifier) Previous() error { return ErrNotSupported }

func (a *BroadlinkAmplifier) InputList() []Input {
	out := make([]Input, len(a.settings.Inputs))
	copy(out, a.settings.Inputs)
	return out
}

func (a *BroadlinkAmplifier) DefaultInput() Input { return a.defaultInput }

func (a *BroadlinkAmplifier) CurrentInput() (Input, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentInput, nil
}

// SetInput switches to the input identified by id (Input.ID).
//
// In cycle mode the amplifier is cycled forward from the current input using
// next_input IR codes. In direct mode a single input-specific IR code is sent.
func (a *BroadlinkAmplifier) SetInput(id string) error {
	switch a.settings.InputMode {
	case InputSelectionDirect:
		return a.setInputDirect(id)
	default:
		return a.setInputCycle(id)
	}
}

func (a *BroadlinkAmplifier) setInputDirect(id string) error {
	a.mu.Lock()
	_, err := a.findInputLocked(id)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	target := a.inputByIDLocked(id)
	code := a.settings.IRCodes["input_"+id]
	a.mu.Unlock()

	if code == "" {
		return fmt.Errorf("IR code for input %q not configured", id)
	}
	if err := a.client.SendIRCode(code); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.currentInput = target
	a.audioReady = false
	a.startReadyTimerLocked(time.Duration(a.settings.SwitchDelaySecs) * time.Second)
	return nil
}

func (a *BroadlinkAmplifier) setInputCycle(id string) error {
	a.mu.Lock()
	targetIdx, currentIdx, err := a.findInputIndicesLocked(id)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	n := len(a.settings.Inputs)
	steps := (targetIdx - currentIdx + n) % n
	targetInput := a.settings.Inputs[targetIdx]
	code := a.settings.IRCodes["next_input"]
	a.mu.Unlock()

	if steps == 0 {
		return nil
	}
	if code == "" {
		return fmt.Errorf("IR code for %q not configured", "next_input")
	}
	for range steps {
		if err := a.client.SendIRCode(code); err != nil {
			return err
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.currentInput = targetInput
	a.audioReady = false
	a.startReadyTimerLocked(time.Duration(a.settings.SwitchDelaySecs) * time.Second)
	return nil
}

// NextInput cycles to the next input in InputList order with a single IR command.
func (a *BroadlinkAmplifier) NextInput() error {
	code, err := a.irCode("next_input")
	if err != nil {
		return err
	}
	if err := a.client.SendIRCode(code); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for i, inp := range a.settings.Inputs {
		if inp.ID == a.currentInput.ID {
			a.currentInput = a.settings.Inputs[(i+1)%len(a.settings.Inputs)]
			break
		}
	}
	a.audioReady = false
	a.startReadyTimerLocked(time.Duration(a.settings.SwitchDelaySecs) * time.Second)
	return nil
}

func (a *BroadlinkAmplifier) CurrentState() (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.powerOn, nil
}

// DetectPowerState probes the hardware through a three-check cascade:
//
//  1. USB DAC discovery — if the amplifier model appears in "aplay -l", the amp
//     is on with its USB Audio input selected.
//  2. REC-OUT noise floor — reads the VU socket for ~3 s and classifies the
//     average RMS against calibrated thresholds for the Magnat MR 780.
//  3. Blind IR probe — stub; requires Broadlink RM4 Mini (Milestone 5).
func (a *BroadlinkAmplifier) DetectPowerState(ctx context.Context) (PowerState, error) {
	// Check 1: USB DAC presence.
	if checkUSBDAC(a.settings.Model) {
		return PowerStateOn, nil
	}

	// Check 2: REC-OUT noise floor via VU socket.
	if a.settings.VUSocketPath != "" {
		rms, err := checkNoiseFloor(ctx, a.settings.VUSocketPath)
		if err == nil {
			if state := classifyNoiseFloor(rms); state != PowerStateUnknown {
				return state, nil
			}
		}
	}

	// Check 3: Blind IR probe — TODO(M5): send USB-input IR code, wait warmup, re-run Check 1.

	return PowerStateUnknown, nil
}

func (a *BroadlinkAmplifier) WarmupTimeSeconds() int       { return a.settings.WarmupSecs }
func (a *BroadlinkAmplifier) InputSwitchDelaySeconds() int { return a.settings.SwitchDelaySecs }

func (a *BroadlinkAmplifier) AudioReady() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.audioReady
}

// AudioReadyAt returns the time at which AudioReady() is expected to become true.
// Returns zero time when no timer is running.
func (a *BroadlinkAmplifier) AudioReadyAt() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.audioReadyAt
}

// --- internal helpers ---

func (a *BroadlinkAmplifier) irCode(command string) (string, error) {
	a.mu.Lock()
	code := a.settings.IRCodes[command]
	a.mu.Unlock()
	if code == "" {
		return "", fmt.Errorf("IR code for %q not configured", command)
	}
	return code, nil
}

// findInputIndicesLocked returns the indices of the target and current inputs.
// Must be called with a.mu held.
func (a *BroadlinkAmplifier) findInputIndicesLocked(targetID string) (targetIdx, currentIdx int, err error) {
	targetIdx = -1
	currentIdx = 0
	for i, inp := range a.settings.Inputs {
		if inp.ID == targetID {
			targetIdx = i
		}
		if inp.ID == a.currentInput.ID {
			currentIdx = i
		}
	}
	if targetIdx == -1 {
		return 0, 0, fmt.Errorf("unknown input ID %q", targetID)
	}
	return targetIdx, currentIdx, nil
}

// findInputLocked returns an error if id is not in the input list.
// Must be called with a.mu held.
func (a *BroadlinkAmplifier) findInputLocked(id string) (Input, error) {
	for _, inp := range a.settings.Inputs {
		if inp.ID == id {
			return inp, nil
		}
	}
	return Input{}, fmt.Errorf("unknown input ID %q", id)
}

// inputByIDLocked returns the Input for a known id (call after findInputLocked).
// Must be called with a.mu held.
func (a *BroadlinkAmplifier) inputByIDLocked(id string) Input {
	for _, inp := range a.settings.Inputs {
		if inp.ID == id {
			return inp
		}
	}
	return Input{}
}

// startReadyTimerLocked starts (or restarts) the audio-ready countdown.
// Must be called with a.mu held.
func (a *BroadlinkAmplifier) startReadyTimerLocked(d time.Duration) {
	if a.cancelReady != nil {
		a.cancelReady()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelReady = cancel
	a.audioReadyAt = time.Now().Add(d)
	go func() {
		select {
		case <-time.After(d):
			a.mu.Lock()
			a.audioReady = true
			a.audioReadyAt = time.Time{}
			a.mu.Unlock()
		case <-ctx.Done():
		}
	}()
}
