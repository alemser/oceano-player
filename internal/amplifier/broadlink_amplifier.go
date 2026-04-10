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
	// Optional cycle key: "prev_input" — when present, SetInput chooses the shortest
	// direction (forward or backward) rather than always going forward.
	// Direct mode adds: "input_<ID>" for each input (e.g. "input_USB", "input_PHONO")
	IRCodes map[string]string

	// VUSocketPath is the Unix socket path for the VU frame stream produced by
	// oceano-source-detector (default /tmp/oceano-vu.sock). Used by
	// DetectPowerState to read the REC-OUT noise floor (Check 2).
	// Leave empty to skip noise floor analysis.
	VUSocketPath string

	// SelectorTimeoutSecs is the number of seconds the amplifier's input selector
	// remains "active" after the last next_input press. When the selector goes
	// dormant, the very first press only highlights the current input without
	// advancing it (e.g. Magnat MR 780 shows "< CD >" on first press).
	// 0 = use default (5 s). Set to a negative value to disable the activation press.
	SelectorTimeoutSecs int
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
	powerOn       bool
	currentInput  Input
	audioReady    bool
	audioReadyAt  time.Time
	cancelReady   context.CancelFunc
	readyGen      uint64
	lastInputSent time.Time // when the last next_input IR code was sent
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

// NewBroadlinkAmplifierForDetection constructs a minimal BroadlinkAmplifier
// used exclusively for power state detection (CheckUSBDAC + noise floor).
// Input list and IR codes are not required — all IR command methods will
// return ErrNotSupported until the full config is provided.
func NewBroadlinkAmplifierForDetection(settings AmplifierSettings) *BroadlinkAmplifier {
	return &BroadlinkAmplifier{
		client:   &MockBroadlinkClient{}, // no IR commands are sent during detection
		settings: settings,
	}
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
	a.readyGen++
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
	forwardSteps := (targetIdx - currentIdx + n) % n
	backwardSteps := (currentIdx - targetIdx + n) % n
	nextCode := a.settings.IRCodes["next_input"]
	prevCode := a.settings.IRCodes["prev_input"]
	needsActivation := a.selectorDormantLocked()
	targetInput := a.settings.Inputs[targetIdx]
	a.mu.Unlock()

	if forwardSteps == 0 {
		return nil
	}
	if nextCode == "" {
		return fmt.Errorf("IR code for %q not configured", "next_input")
	}

	// Choose the shortest path. Use prev_input only if the code is configured
	// and the backward route is strictly shorter.
	code := nextCode
	steps := forwardSteps
	if prevCode != "" && backwardSteps < forwardSteps {
		code = prevCode
		steps = backwardSteps
	}

	// The Magnat MR 780 (and similar amps) ignore the first press after the
	// selector has gone dormant — it only highlights the current input without
	// advancing. Send one extra "wake-up" press (always next_input, which just
	// highlights in place) before the actual step presses.
	if needsActivation {
		if err := a.client.SendIRCode(nextCode); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}

	for range steps {
		if err := a.client.SendIRCode(code); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.currentInput = targetInput
	a.lastInputSent = time.Now()
	a.audioReady = false
	a.startReadyTimerLocked(time.Duration(a.settings.SwitchDelaySecs) * time.Second)
	return nil
}

// PrevInput cycles to the previous input in InputList order with a single IR command.
func (a *BroadlinkAmplifier) PrevInput() error {
	code, err := a.irCode("prev_input")
	if err != nil {
		return err
	}

	a.mu.Lock()
	needsActivation := a.selectorDormantLocked()
	a.mu.Unlock()

	if needsActivation {
		// Wake-up press always uses next_input (highlights in place).
		wakeCode := a.settings.IRCodes["next_input"]
		if wakeCode != "" {
			if err := a.client.SendIRCode(wakeCode); err != nil {
				return err
			}
			time.Sleep(300 * time.Millisecond)
		}
	}

	if err := a.client.SendIRCode(code); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for i, inp := range a.settings.Inputs {
		if inp.ID == a.currentInput.ID {
			n := len(a.settings.Inputs)
			a.currentInput = a.settings.Inputs[(i-1+n)%n]
			break
		}
	}
	a.lastInputSent = time.Now()
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

	a.mu.Lock()
	needsActivation := a.selectorDormantLocked()
	a.mu.Unlock()

	if needsActivation {
		if err := a.client.SendIRCode(code); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
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
	a.lastInputSent = time.Now()
	a.audioReady = false
	a.startReadyTimerLocked(time.Duration(a.settings.SwitchDelaySecs) * time.Second)
	return nil
}

// selectorDormantLocked reports whether the amplifier's input selector has timed
// out since the last next_input press. Must be called with a.mu held.
//
// A negative SelectorTimeoutSecs disables the activation press entirely.
// Zero uses a sensible default of 5 s (covers most amps including Magnat MR 780).
func (a *BroadlinkAmplifier) selectorDormantLocked() bool {
	if a.settings.SelectorTimeoutSecs < 0 {
		return false // explicitly disabled
	}
	secs := a.settings.SelectorTimeoutSecs
	if secs == 0 {
		secs = 5 // default: 5 s covers Magnat MR 780 and similar amps
	}
	timeout := time.Duration(secs) * time.Second
	return a.lastInputSent.IsZero() || time.Since(a.lastInputSent) > timeout
}

// SyncInput updates the assumed current input in software without sending any
// IR command. Use this when the amp was changed manually (physical knob or
// original remote) and the software state needs to catch up.
func (a *BroadlinkAmplifier) SyncInput(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	inp, err := a.findInputLocked(id)
	if err != nil {
		return err
	}
	a.currentInput = inp
	// Mark as synced so selectorDormantLocked uses the timeout from now,
	// not the "zero = always dormant" path.
	a.lastInputSent = time.Now()
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
	if usbDACProbe(ctx, a.settings.Model) {
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

// InputSynced reports whether at least one IR input command has been sent in
// this session. When false the assumed current input equals the configured
// default and may not match the physical amplifier — the caller should prompt
// the user to sync.
func (a *BroadlinkAmplifier) InputSynced() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.lastInputSent.IsZero()
}

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
		return 0, 0, fmt.Errorf("%w %q", ErrUnknownInputID, targetID)
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
	return Input{}, fmt.Errorf("%w %q", ErrUnknownInputID, id)
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
	a.readyGen++
	gen := a.readyGen
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
			defer a.mu.Unlock()
			if gen != a.readyGen || !a.powerOn {
				return
			}
			a.audioReady = true
			a.audioReadyAt = time.Time{}
			a.cancelReady = nil
		case <-ctx.Done():
		}
	}()
}
