package amplifier

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// irCodesWithCycling returns a code map that includes input navigation keys.
var irCodesWithCycling = map[string]string{
	"power_on":    "IR_ON",
	"power_off":   "IR_OFF",
	"volume_up":   "IR_VOL_UP",
	"volume_down": "IR_VOL_DOWN",
	"next_input":  "IR_NEXT",
	"prev_input":  "IR_PREV",
}

// newCyclingAmp builds an amp with InputCycling configured for tests.
// usbFoundAfter: how many IR sends before usbDACProbe returns true (0 = never).
func newCyclingAmp(t *testing.T, maxCycles int, usbFoundAfter int) (*BroadlinkAmplifier, *MockBroadlinkClient) {
	t.Helper()

	var callCount int32
	orig := usbDACProbe
	usbDACProbe = func(_ context.Context, _ string) bool {
		n := int(atomic.AddInt32(&callCount, 1))
		return usbFoundAfter > 0 && n >= usbFoundAfter
	}
	t.Cleanup(func() { usbDACProbe = orig })

	mock := &MockBroadlinkClient{}
	amp, err := NewBroadlinkAmplifier(mock, AmplifierSettings{
		Maker:   "Magnat",
		Model:   "MR 780",
		IRCodes: irCodesWithCycling,
		InputCycling: InputCyclingSettings{
			Enabled:   true,
			Direction: "prev",
			MaxCycles: maxCycles,
			StepWait:  1 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("NewBroadlinkAmplifier: %v", err)
	}
	return amp, mock
}

// --- ProbeWithInputCycling ---

func TestProbeWithInputCycling_FindsUSBAfterSteps(t *testing.T) {
	// USB probe returns true on the 3rd call → 3 input steps needed.
	amp, mock := newCyclingAmp(t, 8, 3)

	got, err := amp.ProbeWithInputCycling(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != PowerStateOn {
		t.Errorf("ProbeWithInputCycling = %q, want %q", got, PowerStateOn)
	}
	// 3 IR sends for 3 input steps.
	if len(mock.Sent) != 3 {
		t.Errorf("IR sends = %d, want 3", len(mock.Sent))
	}
	for _, code := range mock.Sent {
		if code != "IR_PREV" {
			t.Errorf("unexpected IR code %q, want IR_PREV", code)
		}
	}
}

func TestProbeWithInputCycling_ExhaustsMaxCycles(t *testing.T) {
	// USB probe never returns true.
	amp, mock := newCyclingAmp(t, 5, 0)

	got, err := amp.ProbeWithInputCycling(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != PowerStateUnknown {
		t.Errorf("ProbeWithInputCycling = %q, want %q", got, PowerStateUnknown)
	}
	if len(mock.Sent) != 5 {
		t.Errorf("IR sends = %d, want 5 (MaxCycles)", len(mock.Sent))
	}
}

func TestProbeWithInputCycling_DisabledReturnsUnknown(t *testing.T) {
	orig := usbDACProbe
	usbDACProbe = func(_ context.Context, _ string) bool { return true }
	t.Cleanup(func() { usbDACProbe = orig })

	mock := &MockBroadlinkClient{}
	amp, _ := NewBroadlinkAmplifier(mock, AmplifierSettings{
		Maker:   "Magnat",
		Model:   "MR 780",
		IRCodes: irCodesWithCycling,
		InputCycling: InputCyclingSettings{
			Enabled:   false, // disabled
			MaxCycles: 8,
			StepWait:  1 * time.Millisecond,
		},
	})

	got, err := amp.ProbeWithInputCycling(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != PowerStateUnknown {
		t.Errorf("ProbeWithInputCycling (disabled) = %q, want %q", got, PowerStateUnknown)
	}
	if len(mock.Sent) != 0 {
		t.Errorf("expected no IR sends when disabled, got %d", len(mock.Sent))
	}
}

func TestProbeWithInputCycling_UsesNextDirection(t *testing.T) {
	var callCount int32
	orig := usbDACProbe
	usbDACProbe = func(_ context.Context, _ string) bool {
		return int(atomic.AddInt32(&callCount, 1)) >= 2
	}
	t.Cleanup(func() { usbDACProbe = orig })

	mock := &MockBroadlinkClient{}
	amp, _ := NewBroadlinkAmplifier(mock, AmplifierSettings{
		Maker:   "Magnat",
		Model:   "MR 780",
		IRCodes: irCodesWithCycling,
		InputCycling: InputCyclingSettings{
			Enabled:   true,
			Direction: "next",
			MaxCycles: 5,
			StepWait:  1 * time.Millisecond,
		},
	})

	amp.ProbeWithInputCycling(context.Background()) //nolint:errcheck
	for _, code := range mock.Sent {
		if code != "IR_NEXT" {
			t.Errorf("unexpected IR code %q, want IR_NEXT", code)
		}
	}
}

func TestProbeWithInputCycling_ContextCancelled(t *testing.T) {
	orig := usbDACProbe
	usbDACProbe = func(_ context.Context, _ string) bool { return false }
	t.Cleanup(func() { usbDACProbe = orig })

	amp, _ := newCyclingAmp(t, 100, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	got, _ := amp.ProbeWithInputCycling(ctx)
	if got != PowerStateUnknown {
		t.Errorf("ProbeWithInputCycling (cancelled ctx) = %q, want %q", got, PowerStateUnknown)
	}
}

// --- PowerStateMonitor.infer() ---

// newMonitorForInfer builds a monitor backed by a minimal amp (no VU socket).
// Detection will always return Unknown, so infer() drives the final state.
func newMonitorForInfer(t *testing.T, cfg MonitorConfig) *PowerStateMonitor {
	t.Helper()
	orig := usbDACProbe
	usbDACProbe = func(_ context.Context, _ string) bool { return false }
	t.Cleanup(func() { usbDACProbe = orig })

	amp, _ := NewBroadlinkAmplifier(&MockBroadlinkClient{}, AmplifierSettings{
		Maker:   "Magnat",
		Model:   "NOMATCH",
		IRCodes: irCodesWithCycling,
	})
	return NewPowerStateMonitor(amp, time.Hour, cfg)
}

func TestInfer_DetectedOnAlwaysWins(t *testing.T) {
	m := newMonitorForInfer(t, MonitorConfig{WarmUp: time.Hour})
	m.NotifyPowerOff() // last command was off

	got := m.infer(PowerStateOn, "off", time.Now(), time.Time{}, m.config)
	if got != PowerStateOn {
		t.Errorf("infer(On, lastCmd=off) = %q, want %q", got, PowerStateOn)
	}
}

func TestInfer_WarmingUpAfterPowerOn(t *testing.T) {
	m := newMonitorForInfer(t, MonitorConfig{WarmUp: time.Hour})

	// Power-on just sent, detected Unknown (DAC not yet enumerated).
	got := m.infer(PowerStateUnknown, "on", time.Now(), time.Time{}, m.config)
	if got != PowerStateWarmingUp {
		t.Errorf("infer = %q, want %q", got, PowerStateWarmingUp)
	}
}

func TestInfer_WarmingUpWindowExpired(t *testing.T) {
	m := newMonitorForInfer(t, MonitorConfig{WarmUp: 1 * time.Millisecond})

	// Power-on sent long enough ago that WarmUp has passed.
	time.Sleep(5 * time.Millisecond)
	got := m.infer(PowerStateUnknown, "on", time.Now().Add(-1*time.Hour), time.Time{}, m.config)
	if got == PowerStateWarmingUp {
		t.Errorf("infer = %q but warm-up window expired", got)
	}
}

func TestInfer_UnknownAfterPowerOffCommand(t *testing.T) {
	m := newMonitorForInfer(t, MonitorConfig{})

	got := m.infer(PowerStateUnknown, "off", time.Now(), time.Time{}, m.config)
	if got != PowerStateUnknown {
		t.Errorf("infer(Unknown, lastCmd=off) = %q, want %q", got, PowerStateUnknown)
	}
}

func TestInfer_UnknownWhenDetectedOff(t *testing.T) {
	m := newMonitorForInfer(t, MonitorConfig{})

	got := m.infer(PowerStateOff, "", time.Time{}, time.Time{}, m.config)
	if got != PowerStateUnknown {
		t.Errorf("infer(Off, no history) = %q, want %q", got, PowerStateUnknown)
	}
}

func TestInfer_UnknownAfterLongSilence(t *testing.T) {
	cfg := MonitorConfig{StandbyTimeout: 1 * time.Millisecond}
	m := newMonitorForInfer(t, cfg)

	// Last audio detected long ago.
	got := m.infer(PowerStateUnknown, "", time.Time{}, time.Time{}, cfg)
	if got != PowerStateUnknown {
		t.Errorf("infer = %q, want %q", got, PowerStateUnknown)
	}
}

func TestInfer_StandbyNotTriggeredWhenRecentAudio(t *testing.T) {
	cfg := MonitorConfig{StandbyTimeout: 1 * time.Hour}
	m := newMonitorForInfer(t, cfg)

	got := m.infer(PowerStateUnknown, "", time.Time{}, time.Time{}, cfg)
	if got != PowerStateUnknown {
		t.Errorf("infer = %q, want %q", got, PowerStateUnknown)
	}
}

func TestInfer_StandbyAfterTimeout(t *testing.T) {
	cfg := MonitorConfig{StandbyTimeout: 1 * time.Millisecond}
	m := newMonitorForInfer(t, cfg)

	// Simulate: amp was confirmed On, then silence for longer than StandbyTimeout.
	lastAudioAt := time.Now().Add(-1 * time.Second)
	time.Sleep(2 * time.Millisecond)

	got := m.infer(PowerStateUnknown, "", time.Time{}, lastAudioAt, cfg)
	if got != PowerStateStandby {
		t.Errorf("infer = %q, want %q (standby timeout elapsed)", got, PowerStateStandby)
	}
}

func TestInfer_StandbyNotTriggeredWithZeroLastAudio(t *testing.T) {
	// Never confirmed On (lastAudioAt zero) — cannot infer Standby, must stay Unknown.
	cfg := MonitorConfig{StandbyTimeout: 1 * time.Millisecond}
	m := newMonitorForInfer(t, cfg)
	time.Sleep(2 * time.Millisecond)

	got := m.infer(PowerStateUnknown, "", time.Time{}, time.Time{}, cfg)
	if got != PowerStateUnknown {
		t.Errorf("infer = %q, want %q (never confirmed On, cannot infer Standby)", got, PowerStateUnknown)
	}
}

func TestInfer_UnknownWhenNoHistory(t *testing.T) {
	m := newMonitorForInfer(t, MonitorConfig{})

	got := m.infer(PowerStateUnknown, "", time.Time{}, time.Time{}, m.config)
	if got != PowerStateUnknown {
		t.Errorf("infer(Unknown, no history) = %q, want %q", got, PowerStateUnknown)
	}
}

func TestInfer_CyclingProbeDisabledEvenWhenConfigured(t *testing.T) {
	var callCount int32
	orig := usbDACProbe
	usbDACProbe = func(_ context.Context, _ string) bool {
		return int(atomic.AddInt32(&callCount, 1)) >= 2
	}
	t.Cleanup(func() { usbDACProbe = orig })

	mock := &MockBroadlinkClient{}
	amp, _ := NewBroadlinkAmplifier(mock, AmplifierSettings{
		Maker:   "Magnat",
		Model:   "MR 780",
		IRCodes: irCodesWithCycling,
		InputCycling: InputCyclingSettings{
			Enabled:   true,
			Direction: "prev",
			MaxCycles: 5,
			StepWait:  1 * time.Millisecond,
		},
	})

	cfg := MonitorConfig{}
	m := NewPowerStateMonitor(amp, time.Hour, cfg)

	// Even with cycling configured on the amplifier, monitor inference must
	// never emit IR commands after automatic probing was removed.
	time.Sleep(2 * time.Millisecond)

	got := m.infer(PowerStateUnknown, "", time.Time{}, time.Time{}, cfg)
	if got != PowerStateUnknown {
		t.Errorf("infer with cycling config = %q, want %q", got, PowerStateUnknown)
	}
	if len(mock.Sent) != 0 {
		t.Errorf("expected no IR sends, got %d", len(mock.Sent))
	}
}

func TestInfer_DoesNotCycleEvenWhenUSBDACPresent(t *testing.T) {
	orig := usbDACProbe
	usbDACProbe = func(_ context.Context, _ string) bool { return true }
	t.Cleanup(func() { usbDACProbe = orig })

	mock := &MockBroadlinkClient{}
	amp, _ := NewBroadlinkAmplifier(mock, AmplifierSettings{
		Maker:   "Magnat",
		Model:   "MR 780",
		IRCodes: irCodesWithCycling,
		InputCycling: InputCyclingSettings{
			Enabled:   true,
			Direction: "prev",
			MaxCycles: 5,
			StepWait:  1 * time.Millisecond,
		},
	})

	cfg := MonitorConfig{}
	m := NewPowerStateMonitor(amp, time.Hour, cfg)

	got := m.infer(PowerStateUnknown, "", time.Time{}, time.Time{}, cfg)

	// Automatic cycling is disabled, so no IR should be sent and state remains unknown.
	if len(mock.Sent) != 0 {
		t.Errorf("automatic cycling should not fire: %d IR sends", len(mock.Sent))
	}
	if got != PowerStateUnknown {
		t.Errorf("infer = %q, want %q", got, PowerStateUnknown)
	}
}

// --- NotifyPowerOn / NotifyPowerOff ---

func TestNotifyPowerOn_SetsWarmingUp(t *testing.T) {
	m := newMonitorForInfer(t, MonitorConfig{WarmUp: time.Hour})
	m.NotifyPowerOn()

	m.mu.RLock()
	cmd := m.lastCommand
	m.mu.RUnlock()

	if cmd != "on" {
		t.Errorf("lastCommand = %q, want %q", cmd, "on")
	}
}

func TestNotifyPowerOff_SetsOff(t *testing.T) {
	m := newMonitorForInfer(t, MonitorConfig{})
	m.NotifyPowerOff()

	m.mu.RLock()
	cmd := m.lastCommand
	m.mu.RUnlock()

	if cmd != "off" {
		t.Errorf("lastCommand = %q, want %q", cmd, "off")
	}
}
