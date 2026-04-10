package amplifier

import (
	"errors"
	"testing"
	"time"
)

// --- shared fixtures ---

var testInputs = []Input{
	{Label: "USB Audio", ID: "USB"},
	{Label: "Phono", ID: "PHONO"},
	{Label: "CD", ID: "CD"},
	{Label: "AUX", ID: "AUX"},
}

// cycleIRCodes simulates an amplifier without direct input selection (e.g. Magnat MR 780).
var cycleIRCodes = map[string]string{
	"power_on":    "IR_POWER_ON",
	"power_off":   "IR_POWER_OFF",
	"volume_up":   "IR_VOL_UP",
	"volume_down": "IR_VOL_DOWN",
	"next_input":  "IR_NEXT_INPUT",
}

// bidirCycleIRCodes extends cycleIRCodes with a prev_input code, enabling
// shortest-path input selection.
var bidirCycleIRCodes = map[string]string{
	"power_on":    "IR_POWER_ON",
	"power_off":   "IR_POWER_OFF",
	"volume_up":   "IR_VOL_UP",
	"volume_down": "IR_VOL_DOWN",
	"next_input":  "IR_NEXT_INPUT",
	"prev_input":  "IR_PREV_INPUT",
}

func newBidirCycleAmp(t *testing.T, client BroadlinkClient) *BroadlinkAmplifier {
	t.Helper()
	amp, err := NewBroadlinkAmplifier(client, AmplifierSettings{
		Maker:               "Magnat",
		Model:               "MR 780",
		Inputs:              testInputs,
		DefaultInputID:      "USB",
		InputMode:           InputSelectionCycle,
		IRCodes:             bidirCycleIRCodes,
		SelectorTimeoutSecs: -1, // disabled for pure cycling tests
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}
	return amp
}

// directIRCodes simulates an amplifier with direct IR input selection.
var directIRCodes = map[string]string{
	"power_on":    "IR_POWER_ON",
	"power_off":   "IR_POWER_OFF",
	"volume_up":   "IR_VOL_UP",
	"volume_down": "IR_VOL_DOWN",
	"input_USB":   "IR_INPUT_USB",
	"input_PHONO": "IR_INPUT_PHONO",
	"input_CD":    "IR_INPUT_CD",
	"input_AUX":   "IR_INPUT_AUX",
}

var cdIRCodes = map[string]string{
	"power_on":  "IR_Y_POWER_ON",
	"power_off": "IR_Y_POWER_OFF",
	"play":      "IR_Y_PLAY",
	"pause":     "IR_Y_PAUSE",
	"stop":      "IR_Y_STOP",
	"next":      "IR_Y_NEXT",
	"previous":  "IR_Y_PREV",
}

func newCycleAmp(t *testing.T, client BroadlinkClient, warmup, switchDelay int) *BroadlinkAmplifier {
	t.Helper()
	amp, err := NewBroadlinkAmplifier(client, AmplifierSettings{
		Maker:              "Magnat",
		Model:              "MR 780",
		Inputs:             testInputs,
		DefaultInputID:     "USB",
		WarmupSecs:         warmup,
		SwitchDelaySecs:    switchDelay,
		InputMode:          InputSelectionCycle,
		IRCodes:            cycleIRCodes,
		SelectorTimeoutSecs: -1, // disabled: tests exercise pure cycling without activation press
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}
	return amp
}

func newDirectAmp(t *testing.T, client BroadlinkClient) *BroadlinkAmplifier {
	t.Helper()
	amp, err := NewBroadlinkAmplifier(client, AmplifierSettings{
		Maker:           "Denon",
		Model:           "PMA-600NE",
		Inputs:          testInputs,
		DefaultInputID:  "USB",
		WarmupSecs:      0,
		SwitchDelaySecs: 0,
		InputMode:       InputSelectionDirect,
		IRCodes:         directIRCodes,
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}
	return amp
}

// --- BroadlinkAmplifier: identity ---

func TestAmplifier_Identity(t *testing.T) {
	amp := newCycleAmp(t, &MockBroadlinkClient{}, 1, 1)
	if amp.Maker() != "Magnat" {
		t.Errorf("Maker = %q, want %q", amp.Maker(), "Magnat")
	}
	if amp.Model() != "MR 780" {
		t.Errorf("Model = %q, want %q", amp.Model(), "MR 780")
	}
}

func TestAmplifier_DirectMode_Identity(t *testing.T) {
	amp := newDirectAmp(t, &MockBroadlinkClient{})
	if amp.Maker() != "Denon" || amp.Model() != "PMA-600NE" {
		t.Errorf("unexpected identity: %s %s", amp.Maker(), amp.Model())
	}
}

// --- BroadlinkAmplifier: power ---

func TestAmplifier_PowerOn_SetsNotReady(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newCycleAmp(t, mock, 1, 1)

	if err := amp.PowerOn(); err != nil {
		t.Fatalf("PowerOn: %v", err)
	}

	on, _ := amp.CurrentState()
	if !on {
		t.Error("expected powerOn=true after PowerOn()")
	}
	if amp.AudioReady() {
		t.Error("expected audioReady=false immediately after PowerOn()")
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_POWER_ON" {
		t.Errorf("expected [IR_POWER_ON], got %v", mock.Sent)
	}
}

func TestAmplifier_PowerOn_AudioReadyAfterWarmup(t *testing.T) {
	amp := newCycleAmp(t, &MockBroadlinkClient{}, 0, 0)

	if err := amp.PowerOn(); err != nil {
		t.Fatalf("PowerOn: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	if !amp.AudioReady() {
		t.Error("expected audioReady=true after warmup elapsed")
	}
}

func TestAmplifier_PowerOff_CancelsWarmup(t *testing.T) {
	amp := newCycleAmp(t, &MockBroadlinkClient{}, 1, 1)

	_ = amp.PowerOn()
	_ = amp.PowerOff()

	on, _ := amp.CurrentState()
	if on {
		t.Error("expected powerOn=false after PowerOff()")
	}

	time.Sleep(1100 * time.Millisecond)
	if amp.AudioReady() {
		t.Error("warmup timer was not cancelled by PowerOff()")
	}
}

func TestAmplifier_ClientError_PropagatesOnPowerOn(t *testing.T) {
	boom := errors.New("device offline")
	amp := newCycleAmp(t, &MockBroadlinkClient{Err: boom}, 1, 1)

	err := amp.PowerOn()
	if !errors.Is(err, boom) {
		t.Errorf("expected %v, got %v", boom, err)
	}
	on, _ := amp.CurrentState()
	if on {
		t.Error("powerOn should remain false when IR send fails")
	}
}

// --- BroadlinkAmplifier: cycle input mode ---

func TestAmplifier_Cycle_SetInput_CyclesCorrectly(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newCycleAmp(t, mock, 0, 0) // starts at USB (index 0)

	// USB → CD is 2 steps: USB→PHONO, PHONO→CD
	if err := amp.SetInput("CD"); err != nil {
		t.Fatalf("SetInput: %v", err)
	}

	if len(mock.Sent) != 2 {
		t.Errorf("expected 2 IR codes sent, got %d: %v", len(mock.Sent), mock.Sent)
	}
	for _, code := range mock.Sent {
		if code != "IR_NEXT_INPUT" {
			t.Errorf("expected IR_NEXT_INPUT, got %q", code)
		}
	}

	cur, _ := amp.CurrentInput()
	if cur.ID != "CD" {
		t.Errorf("currentInput = %q, want %q", cur.ID, "CD")
	}
}

func TestAmplifier_Cycle_SetInput_SameInput_NoIRSent(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newCycleAmp(t, mock, 0, 0)

	if err := amp.SetInput("USB"); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	if len(mock.Sent) != 0 {
		t.Errorf("expected no IR codes for same input, got %v", mock.Sent)
	}
}

func TestAmplifier_Cycle_SetInput_WrapAround(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newCycleAmp(t, mock, 0, 0) // starts at USB (index 0)

	// USB(0) → AUX(3): 3 forward steps
	if err := amp.SetInput("AUX"); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	if len(mock.Sent) != 3 {
		t.Errorf("expected 3 steps to reach AUX, got %d", len(mock.Sent))
	}
}

func TestAmplifier_Cycle_SetInput_ResetsAudioReady(t *testing.T) {
	amp := newCycleAmp(t, &MockBroadlinkClient{}, 0, 0)

	_ = amp.PowerOn()
	time.Sleep(20 * time.Millisecond)
	if !amp.AudioReady() {
		t.Fatal("precondition: audioReady should be true before SetInput")
	}

	_ = amp.SetInput("PHONO")
	if amp.AudioReady() {
		t.Error("expected audioReady=false immediately after SetInput")
	}
	time.Sleep(20 * time.Millisecond)
	if !amp.AudioReady() {
		t.Error("expected audioReady=true after switch delay elapsed")
	}
}

func TestAmplifier_Cycle_SetInput_UnknownID(t *testing.T) {
	amp := newCycleAmp(t, &MockBroadlinkClient{}, 0, 0)
	if err := amp.SetInput("HDMI"); err == nil {
		t.Error("expected error for unknown input ID")
	}
}

func TestAmplifier_Cycle_NextInput_Cycles(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newCycleAmp(t, mock, 0, 0)

	_ = amp.NextInput() // USB → PHONO
	cur, _ := amp.CurrentInput()
	if cur.ID != "PHONO" {
		t.Errorf("after NextInput: currentInput = %q, want %q", cur.ID, "PHONO")
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_NEXT_INPUT" {
		t.Errorf("expected [IR_NEXT_INPUT], got %v", mock.Sent)
	}
}

func TestAmplifier_Cycle_NextInput_WrapsAround(t *testing.T) {
	amp := newCycleAmp(t, &MockBroadlinkClient{}, 0, 0)

	for range len(testInputs) {
		_ = amp.NextInput()
	}
	cur, _ := amp.CurrentInput()
	if cur.ID != "USB" {
		t.Errorf("after full cycle: currentInput = %q, want %q", cur.ID, "USB")
	}
}

// --- BroadlinkAmplifier: dormant selector (activation press) ---

// TestAmplifier_Cycle_DormantSelector_AddsActivationPress verifies that when
// SelectorTimeoutSecs > 0 and no IR has been sent yet (selector dormant), the
// first SetInput call sends an extra activation press before the step presses.
// On the Magnat MR 780 the first press only highlights the current input without
// advancing; the extra press compensates for that.
func TestAmplifier_Cycle_DormantSelector_AddsActivationPress(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp, err := NewBroadlinkAmplifier(mock, AmplifierSettings{
		Maker:               "Magnat",
		Model:               "MR 780",
		Inputs:              testInputs,
		DefaultInputID:      "USB",
		InputMode:           InputSelectionCycle,
		IRCodes:             cycleIRCodes,
		SelectorTimeoutSecs: 5, // explicit 5s timeout → dormant on first call
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}

	// USB → CD = 2 real steps; selector is dormant so 1 activation press is prepended → 3 total
	if err := amp.SetInput("CD"); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	if len(mock.Sent) != 3 {
		t.Errorf("expected 3 IR codes (1 activation + 2 steps), got %d: %v", len(mock.Sent), mock.Sent)
	}
	cur, _ := amp.CurrentInput()
	if cur.ID != "CD" {
		t.Errorf("currentInput = %q, want %q", cur.ID, "CD")
	}
}

// TestAmplifier_Cycle_ActiveSelector_NoActivationPress verifies that a second
// SetInput call within the timeout does NOT prepend an activation press.
func TestAmplifier_Cycle_ActiveSelector_NoActivationPress(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp, err := NewBroadlinkAmplifier(mock, AmplifierSettings{
		Maker:               "Magnat",
		Model:               "MR 780",
		Inputs:              testInputs,
		DefaultInputID:      "USB",
		InputMode:           InputSelectionCycle,
		IRCodes:             cycleIRCodes,
		SelectorTimeoutSecs: 5,
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}

	// First call: dormant → activation + 1 step (USB → PHONO) = 2 codes
	_ = amp.NextInput()
	mock.Sent = nil // reset

	// Second call immediately after: selector still active → no activation press, just 1 step
	_ = amp.NextInput()
	if len(mock.Sent) != 1 {
		t.Errorf("expected 1 IR code (no activation press), got %d: %v", len(mock.Sent), mock.Sent)
	}
}

// --- BroadlinkAmplifier: bidirectional shortest-path ---

// magnatInputs is the full Magnat MR 780 input ring (0–14).
//
//	0:USB Audio  1:Bluetooth  2:Phono  3:CD  4:DVD
//	5:Aux1       6:Aux2       7:Tape   8:LineIn
//	9:FM        10:DAB       11:Opt1  12:Opt2  13:Coax1  14:Coax2
var magnatInputs = []Input{
	{Label: "USB Audio", ID: "USB_AUDIO"},
	{Label: "Bluetooth", ID: "BLUETOOTH"},
	{Label: "Phono", ID: "PHONO"},
	{Label: "CD", ID: "CD"},
	{Label: "DVD", ID: "DVD"},
	{Label: "Aux 1", ID: "AUX1"},
	{Label: "Aux 2", ID: "AUX2"},
	{Label: "Tape", ID: "TAPE"},
	{Label: "Line In", ID: "LINE_IN"},
	{Label: "FM", ID: "FM"},
	{Label: "DAB", ID: "DAB"},
	{Label: "Optical 1", ID: "OPTICAL1"},
	{Label: "Optical 2", ID: "OPTICAL2"},
	{Label: "Coax 1", ID: "COAX1"},
	{Label: "Coax 2", ID: "COAX2"},
}

// newMagnatAmp builds a bidirectional cycle amp over magnatInputs.
// SelectorTimeoutSecs is disabled (-1) so these tests focus on path selection.
func newMagnatAmp(t *testing.T, client BroadlinkClient) *BroadlinkAmplifier {
	t.Helper()
	amp, err := NewBroadlinkAmplifier(client, AmplifierSettings{
		Maker:               "Magnat",
		Model:               "MR 780",
		Inputs:              magnatInputs,
		DefaultInputID:      "USB_AUDIO",
		InputMode:           InputSelectionCycle,
		IRCodes:             bidirCycleIRCodes,
		SelectorTimeoutSecs: -1,
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}
	return amp
}

// TestAmplifier_Bidir_ShortestPath is a table-driven test covering every
// direction category across the full 15-input Magnat MR 780 ring.
//
// With n=15 (odd) there are never ties: for any pair the two route lengths
// always differ by an odd number, so one direction is strictly shorter.
//
//	forward steps  = (targetIdx - fromIdx + 15) % 15
//	backward steps = (fromIdx - targetIdx + 15) % 15
//	→ shortest wins; tie (impossible with n=15) would go forward
func TestAmplifier_Bidir_ShortestPath(t *testing.T) {
	cases := []struct {
		from      string
		to        string
		wantCode  string // IR_NEXT_INPUT or IR_PREV_INPUT
		wantSteps int    // 0 = same input, no IR expected
	}{
		// ── Forward is shorter ────────────────────────────────────────────────
		// USB_AUDIO(0) → neighbours and beyond up to the midpoint
		{"USB_AUDIO", "BLUETOOTH", "IR_NEXT_INPUT", 1},  // fwd=1  bwd=14
		{"USB_AUDIO", "PHONO", "IR_NEXT_INPUT", 2},      // fwd=2  bwd=13
		{"USB_AUDIO", "CD", "IR_NEXT_INPUT", 3},         // fwd=3  bwd=12
		{"USB_AUDIO", "DVD", "IR_NEXT_INPUT", 4},        // fwd=4  bwd=11
		{"USB_AUDIO", "AUX1", "IR_NEXT_INPUT", 5},       // fwd=5  bwd=10
		{"USB_AUDIO", "AUX2", "IR_NEXT_INPUT", 6},       // fwd=6  bwd=9
		{"USB_AUDIO", "TAPE", "IR_NEXT_INPUT", 7},       // fwd=7  bwd=8  ← last forward winner
		// Wrap-around forward: Coax2 is just one next_input from USB_AUDIO
		{"COAX2", "USB_AUDIO", "IR_NEXT_INPUT", 1},      // fwd=1  bwd=14
		// Consecutive forward steps
		{"FM", "DAB", "IR_NEXT_INPUT", 1},               // fwd=1  bwd=14
		{"OPTICAL1", "OPTICAL2", "IR_NEXT_INPUT", 1},    // fwd=1  bwd=14
		{"COAX1", "COAX2", "IR_NEXT_INPUT", 1},          // fwd=1  bwd=14
		{"DAB", "COAX1", "IR_NEXT_INPUT", 3},            // fwd=3  bwd=12
		// Wrap-around forward: COAX2(14)→BLUETOOTH(1) = 2 steps fwd vs 13 bwd
		{"COAX2", "BLUETOOTH", "IR_NEXT_INPUT", 2},      // fwd=2  bwd=13

		// ── Backward is shorter ───────────────────────────────────────────────
		// USB_AUDIO(0) → inputs past the midpoint (closer going backwards)
		{"USB_AUDIO", "LINE_IN", "IR_PREV_INPUT", 7},    // fwd=8  bwd=7  ← first backward winner
		{"USB_AUDIO", "FM", "IR_PREV_INPUT", 6},         // fwd=9  bwd=6
		{"USB_AUDIO", "DAB", "IR_PREV_INPUT", 5},        // fwd=10 bwd=5
		{"USB_AUDIO", "OPTICAL1", "IR_PREV_INPUT", 4},   // fwd=11 bwd=4
		{"USB_AUDIO", "OPTICAL2", "IR_PREV_INPUT", 3},   // fwd=12 bwd=3
		{"USB_AUDIO", "COAX1", "IR_PREV_INPUT", 2},      // fwd=13 bwd=2
		{"USB_AUDIO", "COAX2", "IR_PREV_INPUT", 1},      // fwd=14 bwd=1  ← one step back
		// Adjacent backward steps across the ring
		{"PHONO", "BLUETOOTH", "IR_PREV_INPUT", 1},      // fwd=14 bwd=1
		{"CD", "PHONO", "IR_PREV_INPUT", 1},             // fwd=14 bwd=1
		{"DVD", "CD", "IR_PREV_INPUT", 1},               // fwd=14 bwd=1
		// Multi-step backward: DVD(4)→BLUETOOTH(1) = fwd 12, bwd 3
		{"DVD", "BLUETOOTH", "IR_PREV_INPUT", 3},        // fwd=12 bwd=3
		// Wrap-around backward: BLUETOOTH(1)→COAX2(14) = fwd 13, bwd 2
		{"BLUETOOTH", "COAX2", "IR_PREV_INPUT", 2},      // fwd=13 bwd=2

		// ── Multi-hop backward (user's specific examples) ────────────────────
		// "USB Audio → CD goes increasing; CD → USB Audio goes decreasing"
		// USB_AUDIO(0) → CD(3): fwd=3, bwd=12 → forward 3 steps ✓
		{"USB_AUDIO", "CD", "IR_NEXT_INPUT", 3},
		// CD(3) → USB_AUDIO(0): fwd=12, bwd=3 → backward 3 steps (CD→Phono→BT→USB)
		{"CD", "USB_AUDIO", "IR_PREV_INPUT", 3},
		// PHONO(2) → USB_AUDIO(0): fwd=13, bwd=2 → backward 2 steps
		{"PHONO", "USB_AUDIO", "IR_PREV_INPUT", 2},
		// DVD(4) → USB_AUDIO(0): fwd=11, bwd=4 → backward 4 steps
		{"DVD", "USB_AUDIO", "IR_PREV_INPUT", 4},
		// AUX1(5) → CD(3): fwd=13, bwd=2 → backward 2 steps
		{"AUX1", "CD", "IR_PREV_INPUT", 2},
		// AUX2(6) → PHONO(2): fwd=11, bwd=4 → backward 4 steps
		{"AUX2", "PHONO", "IR_PREV_INPUT", 4},

		// ── Same input — no IR expected ───────────────────────────────────────
		{"USB_AUDIO", "USB_AUDIO", "", 0},
		{"OPTICAL1", "OPTICAL1", "", 0},
	}

	for _, tc := range cases {
		t.Run(tc.from+"→"+tc.to, func(t *testing.T) {
			mock := &MockBroadlinkClient{}
			amp := newMagnatAmp(t, mock)

			if tc.from != "USB_AUDIO" {
				if err := amp.SyncInput(tc.from); err != nil {
					t.Fatalf("SyncInput(%q): %v", tc.from, err)
				}
			}

			if err := amp.SetInput(tc.to); err != nil {
				t.Fatalf("SetInput(%q): %v", tc.to, err)
			}

			if tc.wantSteps == 0 {
				if len(mock.Sent) != 0 {
					t.Errorf("same input: expected no IR codes, got %v", mock.Sent)
				}
				return
			}

			if len(mock.Sent) != tc.wantSteps {
				t.Errorf("steps: want %d, got %d (%v)", tc.wantSteps, len(mock.Sent), mock.Sent)
			}
			for i, code := range mock.Sent {
				if code != tc.wantCode {
					t.Errorf("code[%d]: want %q, got %q", i, tc.wantCode, code)
				}
			}

			cur, _ := amp.CurrentInput()
			if cur.ID != tc.to {
				t.Errorf("currentInput: want %q, got %q", tc.to, cur.ID)
			}
		})
	}
}

// TestAmplifier_Bidir_NoFallback_ForwardOnly verifies that when prev_input is
// not configured the amp always takes the forward route, even when backward
// would be shorter.
func TestAmplifier_Bidir_NoFallback_ForwardOnly(t *testing.T) {
	cases := []struct {
		from      string
		to        string
		wantSteps int // forward steps (longer route)
	}{
		{"USB_AUDIO", "COAX2", 14},    // backward=1 but no prev_input → forced fwd
		{"USB_AUDIO", "LINE_IN", 8},   // backward=7 but no prev_input → forced fwd
		{"PHONO", "BLUETOOTH", 14},    // backward=1 → forced fwd
	}

	for _, tc := range cases {
		t.Run(tc.from+"→"+tc.to+"_fwdOnly", func(t *testing.T) {
			mock := &MockBroadlinkClient{}
			amp, err := NewBroadlinkAmplifier(mock, AmplifierSettings{
				Inputs:              magnatInputs,
				DefaultInputID:      "USB_AUDIO",
				InputMode:           InputSelectionCycle,
				IRCodes:             cycleIRCodes, // no prev_input key
				SelectorTimeoutSecs: -1,
			})
			if err != nil {
				t.Fatalf("NewBroadlinkAmplifier: %v", err)
			}

			if tc.from != "USB_AUDIO" {
				if err := amp.SyncInput(tc.from); err != nil {
					t.Fatalf("SyncInput(%q): %v", tc.from, err)
				}
			}

			if err := amp.SetInput(tc.to); err != nil {
				t.Fatalf("SetInput(%q): %v", tc.to, err)
			}

			if len(mock.Sent) != tc.wantSteps {
				t.Errorf("fwd-only steps: want %d, got %d (%v)", tc.wantSteps, len(mock.Sent), mock.Sent)
			}
			for i, code := range mock.Sent {
				if code != "IR_NEXT_INPUT" {
					t.Errorf("code[%d]: want IR_NEXT_INPUT, got %q", i, code)
				}
			}
		})
	}
}

// --- BroadlinkAmplifier: direct input mode ---

func TestAmplifier_Direct_SetInput_SendsCorrectCode(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newDirectAmp(t, mock)

	if err := amp.SetInput("PHONO"); err != nil {
		t.Fatalf("SetInput: %v", err)
	}

	if len(mock.Sent) != 1 || mock.Sent[0] != "IR_INPUT_PHONO" {
		t.Errorf("expected [IR_INPUT_PHONO], got %v", mock.Sent)
	}

	cur, _ := amp.CurrentInput()
	if cur.ID != "PHONO" {
		t.Errorf("currentInput = %q, want %q", cur.ID, "PHONO")
	}
}

func TestAmplifier_Direct_SetInput_UnknownID(t *testing.T) {
	amp := newDirectAmp(t, &MockBroadlinkClient{})
	if err := amp.SetInput("HDMI"); err == nil {
		t.Error("expected error for unknown input ID")
	}
}

func TestAmplifier_Direct_SetInput_MissingIRCode(t *testing.T) {
	// directIRCodes has input_USB but we'll use an amp with empty ir_codes
	amp, _ := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		Inputs:         testInputs,
		DefaultInputID: "USB",
		InputMode:      InputSelectionDirect,
		IRCodes:        map[string]string{},
	})
	if err := amp.SetInput("PHONO"); err == nil {
		t.Error("expected error when input IR code is missing")
	}
}

// --- BroadlinkAmplifier: transport commands ---

func TestAmplifier_TransportCommands_NotSupported(t *testing.T) {
	amp := newCycleAmp(t, &MockBroadlinkClient{}, 0, 0)
	for _, fn := range []func() error{amp.Play, amp.Pause, amp.Stop, amp.Next, amp.Previous} {
		if err := fn(); !errors.Is(err, ErrNotSupported) {
			t.Errorf("expected ErrNotSupported, got %v", err)
		}
	}
}

// --- BroadlinkAmplifier: constructor validation ---

func TestAmplifier_Constructor_EmptyInputs(t *testing.T) {
	_, err := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		DefaultInputID: "USB",
		IRCodes:        cycleIRCodes,
	})
	if err == nil {
		t.Error("expected error for empty inputs")
	}
}

func TestAmplifier_Constructor_UnknownDefaultInput(t *testing.T) {
	_, err := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		Inputs:         testInputs,
		DefaultInputID: "HDMI",
		IRCodes:        cycleIRCodes,
	})
	if err == nil {
		t.Error("expected error for unknown defaultInputID")
	}
}

func TestAmplifier_MissingIRCode(t *testing.T) {
	amp, _ := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		Inputs:         testInputs,
		DefaultInputID: "USB",
		InputMode:      InputSelectionCycle,
		IRCodes:        map[string]string{},
	})
	if err := amp.PowerOn(); err == nil {
		t.Error("expected error when power_on IR code is missing")
	}
}

// --- BroadlinkCDPlayer ---

func newTestCDPlayer(client BroadlinkClient) *BroadlinkCDPlayer {
	return NewBroadlinkCDPlayer(client, CDPlayerSettings{
		Maker:   "Yamaha",
		Model:   "CD-S300",
		IRCodes: cdIRCodes,
	})
}

func TestCDPlayer_Identity(t *testing.T) {
	cd := newTestCDPlayer(&MockBroadlinkClient{})
	if cd.Maker() != "Yamaha" {
		t.Errorf("Maker = %q", cd.Maker())
	}
	if cd.Model() != "CD-S300" {
		t.Errorf("Model = %q", cd.Model())
	}
}

func TestCDPlayer_TransportCommands_SendCorrectCode(t *testing.T) {
	cases := []struct {
		name    string
		fn      func(*BroadlinkCDPlayer) error
		wantKey string
	}{
		{"Play", (*BroadlinkCDPlayer).Play, "play"},
		{"Pause", (*BroadlinkCDPlayer).Pause, "pause"},
		{"Stop", (*BroadlinkCDPlayer).Stop, "stop"},
		{"Next", (*BroadlinkCDPlayer).Next, "next"},
		{"Previous", (*BroadlinkCDPlayer).Previous, "previous"},
		{"PowerOn", (*BroadlinkCDPlayer).PowerOn, "power_on"},
		{"PowerOff", (*BroadlinkCDPlayer).PowerOff, "power_off"},
	}

	for _, tc := range cases {
		mock := &MockBroadlinkClient{}
		cd := newTestCDPlayer(mock)

		if err := tc.fn(cd); err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
		if len(mock.Sent) != 1 || mock.Sent[0] != cdIRCodes[tc.wantKey] {
			t.Errorf("%s: sent %v, want [%q]", tc.name, mock.Sent, cdIRCodes[tc.wantKey])
		}
	}
}

func TestCDPlayer_VolumeCommands_NotSupported(t *testing.T) {
	cd := newTestCDPlayer(&MockBroadlinkClient{})
	if err := cd.VolumeUp(); !errors.Is(err, ErrNotSupported) {
		t.Errorf("VolumeUp: expected ErrNotSupported, got %v", err)
	}
	if err := cd.VolumeDown(); !errors.Is(err, ErrNotSupported) {
		t.Errorf("VolumeDown: expected ErrNotSupported, got %v", err)
	}
}

func TestCDPlayer_MissingIRCode(t *testing.T) {
	cd := NewBroadlinkCDPlayer(&MockBroadlinkClient{}, CDPlayerSettings{
		Maker: "Yamaha", Model: "CD-S300", IRCodes: map[string]string{},
	})
	if err := cd.Play(); err == nil {
		t.Error("expected error when play IR code is missing")
	}
}

func TestCDPlayer_ClientError_Propagates(t *testing.T) {
	boom := errors.New("offline")
	cd := newTestCDPlayer(&MockBroadlinkClient{Err: boom})
	if err := cd.Play(); !errors.Is(err, boom) {
		t.Errorf("expected %v, got %v", boom, err)
	}
}

func TestCDPlayer_DifferentModel(t *testing.T) {
	cd := NewBroadlinkCDPlayer(&MockBroadlinkClient{}, CDPlayerSettings{
		Maker: "Sony", Model: "CDP-CE500", IRCodes: cdIRCodes,
	})
	if cd.Maker() != "Sony" || cd.Model() != "CDP-CE500" {
		t.Errorf("unexpected identity: %s %s", cd.Maker(), cd.Model())
	}
}

// --- MockBroadlinkClient ---

func TestMock_RecordsSentCodes(t *testing.T) {
	mock := &MockBroadlinkClient{}
	_ = mock.SendIRCode("A")
	_ = mock.SendIRCode("B")
	if len(mock.Sent) != 2 || mock.Sent[0] != "A" || mock.Sent[1] != "B" {
		t.Errorf("Sent = %v", mock.Sent)
	}
}

func TestMock_ReturnsConfiguredError(t *testing.T) {
	boom := errors.New("boom")
	mock := &MockBroadlinkClient{Err: boom}
	if err := mock.SendIRCode("X"); !errors.Is(err, boom) {
		t.Errorf("expected %v, got %v", boom, err)
	}
}
