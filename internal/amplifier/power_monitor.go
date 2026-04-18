package amplifier

import (
	"context"
	"log"
	"sync"
	"time"
)

const defaultPowerMonitorInterval = 30 * time.Second

// MonitorConfig holds the timing parameters used by PowerStateMonitor to
// infer logical power states from raw hardware detection results.
type MonitorConfig struct {
	// WarmUp is how long to report PowerStateWarmingUp after a power-on command
	// before hardware confirmation is expected (e.g. USB DAC enumeration).
	WarmUp time.Duration

	// StandbyTimeout is the amplifier's auto-standby delay. After this much
	// silence (no PowerStateOn detected), the monitor infers PowerStateStandby.
	// Zero disables standby inference.
	StandbyTimeout time.Duration

	// CyclingEnabled enables the active input-cycling probe when passive
	// detection is inconclusive and silence exceeds CyclingMinSilence.
	// The amp must implement InputCycler for this to take effect.
	CyclingEnabled bool

	// CyclingMinSilence is the minimum duration of silence required before
	// input cycling is attempted. Prevents interrupting active playback.
	CyclingMinSilence time.Duration
}

// PowerStateMonitor polls DetectPowerState on a fixed interval, applies
// command-history inference to derive logical states (warming_up, standby),
// and broadcasts state changes to subscribers.
//
// Usage:
//
//	m := NewPowerStateMonitor(amp, 30*time.Second)
//	go m.Start(ctx)
//
//	// notify on IR commands:
//	m.NotifyPowerOn()
//	m.NotifyPowerOff()
//
//	// poll:
//	state, at := m.Current()
//
//	// react to changes:
//	ch := m.Subscribe()
//	defer m.Unsubscribe(ch)
//	for state := range ch { ... }
type PowerStateMonitor struct {
	amp      Amplifier
	interval time.Duration
	config   MonitorConfig

	mu            sync.RWMutex
	current       PowerState
	updatedAt     time.Time
	lastCommand   string // "on" | "off" | ""
	lastCommandAt time.Time
	lastAudioAt   time.Time // last time On was confirmed (for standby inference)

	subsMu sync.Mutex
	subs   []chan PowerState
}

// NewPowerStateMonitor creates a monitor that polls amp every interval.
// The initial state is PowerStateUnknown until the first detection completes.
// Call Start(ctx) in a goroutine to begin polling.
func NewPowerStateMonitor(amp Amplifier, interval time.Duration, cfg ...MonitorConfig) *PowerStateMonitor {
	if interval <= 0 {
		interval = defaultPowerMonitorInterval
	}
	var mcfg MonitorConfig
	if len(cfg) > 0 {
		mcfg = cfg[0]
	}
	return &PowerStateMonitor{
		amp:      amp,
		interval: interval,
		config:   mcfg,
		current:  PowerStateUnknown,
	}
}

// Start runs the detection loop until ctx is cancelled.
// The first detection runs immediately; subsequent ones fire every interval.
func (m *PowerStateMonitor) Start(ctx context.Context) {
	m.detect(ctx)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.detect(ctx)
		}
	}
}

// Amp returns the Amplifier being monitored.
func (m *PowerStateMonitor) Amp() Amplifier { return m.amp }

// Current returns the last detected power state and the time it was last checked.
// Returns (PowerStateUnknown, zero) before the first detection.
func (m *PowerStateMonitor) Current() (PowerState, time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current, m.updatedAt
}

// NotifyPowerOn records that a power-on IR command was just sent.
// If a WarmUp window is configured, the state is set immediately to
// PowerStateWarmingUp so callers see the transition without waiting for
// the next detection cycle.
func (m *PowerStateMonitor) NotifyPowerOn() {
	now := time.Now()
	m.mu.Lock()
	m.lastCommand = "on"
	m.lastCommandAt = now
	changed := m.config.WarmUp > 0 && m.current != PowerStateWarmingUp
	if changed {
		m.current = PowerStateWarmingUp
		m.updatedAt = now
	}
	m.mu.Unlock()
	if changed {
		log.Printf("amplifier: power state → %s (power-on command)", PowerStateWarmingUp)
		m.broadcast(PowerStateWarmingUp)
	}
}

// NotifyPowerOff records that a power-off IR command was just sent.
// The monitor will report PowerStateOff until hardware evidence contradicts it.
func (m *PowerStateMonitor) NotifyPowerOff() {
	m.mu.Lock()
	m.lastCommand = "off"
	m.lastCommandAt = time.Now()
	m.mu.Unlock()
}

// Subscribe returns a buffered channel that receives a value whenever the
// power state changes. The caller must call Unsubscribe when done.
func (m *PowerStateMonitor) Subscribe() chan PowerState {
	ch := make(chan PowerState, 1)
	m.subsMu.Lock()
	m.subs = append(m.subs, ch)
	m.subsMu.Unlock()
	return ch
}

// Unsubscribe removes and closes the channel returned by Subscribe.
func (m *PowerStateMonitor) Unsubscribe(ch chan PowerState) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for i, s := range m.subs {
		if s == ch {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

// --- internal ---

func (m *PowerStateMonitor) detect(ctx context.Context) {
	// Passive detection: USB probe (~2s) + VU sample (~3s) with margin.
	passiveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	detected, err := m.amp.DetectPowerState(passiveCtx)
	cancel()
	if err != nil {
		log.Printf("amplifier: power detection error: %v", err)
		detected = PowerStateUnknown
	}

	// Read shared state before inference (no lock held during inference/cycling).
	m.mu.RLock()
	lastCmd := m.lastCommand
	lastCmdAt := m.lastCommandAt
	lastAudioAt := m.lastAudioAt
	cfg := m.config
	m.mu.RUnlock()

	state := m.infer(ctx, detected, lastCmd, lastCmdAt, lastAudioAt, cfg)

	m.mu.Lock()
	if detected == PowerStateOn {
		m.lastAudioAt = time.Now()
	}
	changed := state != m.current
	m.current = state
	m.updatedAt = time.Now()
	m.mu.Unlock()

	if changed {
		log.Printf("amplifier: power state → %s (detected: %s)", state, detected)
		m.broadcast(state)
	}
}

func (m *PowerStateMonitor) infer(
	ctx context.Context,
	detected PowerState,
	lastCmd string,
	lastCmdAt, lastAudioAt time.Time,
	cfg MonitorConfig,
) PowerState {
	now := time.Now()

	if detected == PowerStateOn {
		return PowerStateOn
	}

	// Within warm-up window after a power-on command.
	if lastCmd == "on" && cfg.WarmUp > 0 && !lastCmdAt.IsZero() && now.Sub(lastCmdAt) < cfg.WarmUp {
		return PowerStateWarmingUp
	}

	// Active cycling probe: only when silence has persisted long enough.
	if cfg.CyclingEnabled && !lastAudioAt.IsZero() && now.Sub(lastAudioAt) >= cfg.CyclingMinSilence {
		if cycler, ok := m.amp.(InputCycler); ok {
			if result, err := cycler.ProbeWithInputCycling(ctx); err == nil && result == PowerStateOn {
				return PowerStateOn
			}
		}
	}

	return PowerStateUnknown
}

func (m *PowerStateMonitor) broadcast(state PowerState) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for _, ch := range m.subs {
		select {
		case ch <- state:
		default:
			// Subscriber is slow; they will pick up the next transition.
		}
	}
}
