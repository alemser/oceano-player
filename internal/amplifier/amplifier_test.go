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
		Maker:           "Magnat",
		Model:           "MR 780",
		Inputs:          testInputs,
		DefaultInputID:  "USB",
		WarmupSecs:      warmup,
		SwitchDelaySecs: switchDelay,
		InputMode:       InputSelectionCycle,
		IRCodes:         cycleIRCodes,
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
