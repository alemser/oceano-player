package amplifier

import (
	"errors"
	"testing"
)

// --- shared fixtures ---

var irCodes = map[string]string{
	"power_on":    "IR_POWER_ON",
	"power_off":   "IR_POWER_OFF",
	"volume_up":   "IR_VOL_UP",
	"volume_down": "IR_VOL_DOWN",
	"next_input":  "IR_NEXT_INPUT",
	"prev_input":  "IR_PREV_INPUT",
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

func newTestAmp(t *testing.T, client BroadlinkClient) *BroadlinkAmplifier {
	t.Helper()
	amp, err := NewBroadlinkAmplifier(client, AmplifierSettings{
		Maker:   "Magnat",
		Model:   "MR 780",
		IRCodes: irCodes,
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}
	return amp
}

// --- BroadlinkAmplifier: identity ---

func TestAmplifier_Identity(t *testing.T) {
	amp := newTestAmp(t, &MockBroadlinkClient{})
	if amp.Maker() != "Magnat" {
		t.Errorf("Maker = %q", amp.Maker())
	}
	if amp.Model() != "MR 780" {
		t.Errorf("Model = %q", amp.Model())
	}
}

// --- BroadlinkAmplifier: power ---

func TestAmplifier_PowerOn_SendsIR(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newTestAmp(t, mock)
	if err := amp.PowerOn(); err != nil {
		t.Fatalf("PowerOn: %v", err)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != irCodes["power_on"] {
		t.Errorf("sent %v, want [%q]", mock.Sent, irCodes["power_on"])
	}
}

func TestAmplifier_PowerOff_SendsIR(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newTestAmp(t, mock)
	if err := amp.PowerOff(); err != nil {
		t.Fatalf("PowerOff: %v", err)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != irCodes["power_off"] {
		t.Errorf("sent %v, want [%q]", mock.Sent, irCodes["power_off"])
	}
}

// --- BroadlinkAmplifier: volume ---

func TestAmplifier_VolumeUp_SendsIR(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newTestAmp(t, mock)
	if err := amp.VolumeUp(); err != nil {
		t.Fatalf("VolumeUp: %v", err)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != irCodes["volume_up"] {
		t.Errorf("sent %v, want [%q]", mock.Sent, irCodes["volume_up"])
	}
}

func TestAmplifier_VolumeDown_SendsIR(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newTestAmp(t, mock)
	if err := amp.VolumeDown(); err != nil {
		t.Fatalf("VolumeDown: %v", err)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != irCodes["volume_down"] {
		t.Errorf("sent %v, want [%q]", mock.Sent, irCodes["volume_down"])
	}
}

// --- BroadlinkAmplifier: input navigation ---

func TestAmplifier_NextInput_SendsIR(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newTestAmp(t, mock)
	if err := amp.NextInput(); err != nil {
		t.Fatalf("NextInput: %v", err)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != irCodes["next_input"] {
		t.Errorf("sent %v, want [%q]", mock.Sent, irCodes["next_input"])
	}
}

func TestAmplifier_PrevInput_SendsIR(t *testing.T) {
	mock := &MockBroadlinkClient{}
	amp := newTestAmp(t, mock)
	if err := amp.PrevInput(); err != nil {
		t.Fatalf("PrevInput: %v", err)
	}
	if len(mock.Sent) != 1 || mock.Sent[0] != irCodes["prev_input"] {
		t.Errorf("sent %v, want [%q]", mock.Sent, irCodes["prev_input"])
	}
}

func TestAmplifier_NextInput_MissingCode(t *testing.T) {
	amp, _ := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		Maker: "Magnat", Model: "MR 780", IRCodes: map[string]string{},
	})
	if err := amp.NextInput(); err == nil {
		t.Error("expected error when next_input IR code is missing")
	}
}

// --- BroadlinkAmplifier: transport not supported ---

func TestAmplifier_TransportCommands_NotSupported(t *testing.T) {
	amp := newTestAmp(t, &MockBroadlinkClient{})
	for _, fn := range []func() error{amp.Play, amp.Pause, amp.Stop, amp.Next, amp.Previous} {
		if err := fn(); !errors.Is(err, ErrNotSupported) {
			t.Errorf("expected ErrNotSupported, got %v", err)
		}
	}
}

// --- BroadlinkAmplifier: constructor validation ---

func TestAmplifier_Constructor_MissingMaker(t *testing.T) {
	_, err := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		Model: "MR 780", IRCodes: irCodes,
	})
	if err == nil {
		t.Error("expected error for missing maker")
	}
}

func TestAmplifier_Constructor_MissingModel(t *testing.T) {
	_, err := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		Maker: "Magnat", IRCodes: irCodes,
	})
	if err == nil {
		t.Error("expected error for missing model")
	}
}

func TestAmplifier_MissingIRCode(t *testing.T) {
	amp, _ := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		Maker: "Magnat", Model: "MR 780", IRCodes: map[string]string{},
	})
	if err := amp.PowerOn(); err == nil {
		t.Error("expected error when power_on IR code is missing")
	}
}

func TestAmplifier_ClientError_Propagates(t *testing.T) {
	boom := errors.New("offline")
	amp := newTestAmp(t, &MockBroadlinkClient{Err: boom})
	if err := amp.PowerOn(); !errors.Is(err, boom) {
		t.Errorf("expected %v, got %v", boom, err)
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
