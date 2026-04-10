package amplifier

import (
	"context"
	"log"
	"sync"
	"time"
)

const defaultPowerMonitorInterval = 30 * time.Second

// PowerStateMonitor polls DetectPowerState on a fixed interval and broadcasts
// state changes to subscribers. It is the single source of truth for detected
// amp power state within a process — all consumers (REST handlers, SSE stream,
// auto-switch logic) read from the cache or subscribe to change notifications.
//
// Usage:
//
//	m := NewPowerStateMonitor(amp, 30*time.Second)
//	go m.Start(ctx)
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

	mu        sync.RWMutex
	current   PowerState
	updatedAt time.Time

	subsMu sync.Mutex
	subs   []chan PowerState
}

// NewPowerStateMonitor creates a monitor that polls amp every interval.
// The initial state is PowerStateUnknown until the first detection completes.
// Call Start(ctx) in a goroutine to begin polling.
func NewPowerStateMonitor(amp Amplifier, interval time.Duration) *PowerStateMonitor {
	if interval <= 0 {
		interval = defaultPowerMonitorInterval
	}
	return &PowerStateMonitor{
		amp:      amp,
		interval: interval,
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

// Current returns the last detected power state and the time it was last
// checked. Returns (PowerStateUnknown, zero) before the first detection.
func (m *PowerStateMonitor) Current() (PowerState, time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current, m.updatedAt
}

// Subscribe returns a buffered channel that receives a value whenever the
// power state changes. The caller must call Unsubscribe when done to avoid
// a goroutine leak.
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
	// Base timeout covers Check 1 (USB probe 2s) + Check 2 (VU sample 3s)
	// with margin.
	timeout := 10 * time.Second
	detCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	state, err := m.amp.DetectPowerState(detCtx)
	if err != nil {
		log.Printf("amplifier: power detection error: %v", err)
		state = PowerStateUnknown
	}

	m.mu.Lock()
	changed := state != m.current
	m.current = state
	m.updatedAt = time.Now()
	m.mu.Unlock()

	if changed {
		log.Printf("amplifier: power state changed → %s", state)
		m.broadcast(state)
	}
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
