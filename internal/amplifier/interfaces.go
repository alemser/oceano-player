package amplifier

import (
	"context"
	"errors"
)

// PowerState represents the detected hardware power state of a device.
type PowerState string

const (
	PowerStateOn      PowerState = "on"
	PowerStateOff     PowerState = "off"
	PowerStateUnknown PowerState = "unknown"
)

// ErrNotSupported is returned by RemoteDevice methods that are not available
// on a particular device (e.g. CurrentTrack() on an amplifier).
var ErrNotSupported = errors.New("operation not supported on this device")

// RemoteDevice defines common IR remote operations available on various devices.
// Methods return ErrNotSupported if the operation is not available for this device.
type RemoteDevice interface {
	// Maker returns the manufacturer name (e.g. "Magnat", "Yamaha").
	Maker() string
	// Model returns the model name (e.g. "MR 780", "CD-S300").
	Model() string

	// Volume control — optional; return ErrNotSupported if unavailable.
	VolumeUp() error
	VolumeDown() error

	// Transport controls — optional on amplifiers, standard on CD players.
	Play() error
	Pause() error
	Stop() error
	Next() error
	Previous() error

	// Power operations — optional; return ErrNotSupported if unavailable.
	PowerOn() error
	PowerOff() error
}

// Amplifier extends RemoteDevice with input navigation and hardware power
// detection.
type Amplifier interface {
	RemoteDevice

	// NextInput sends a single next_input IR command.
	NextInput() error
	// PrevInput sends a single prev_input IR command.
	PrevInput() error

	// DetectPowerState probes hardware to determine the actual power state.
	// The context controls the total detection timeout.
	DetectPowerState(ctx context.Context) (PowerState, error)
}

// CDPlayer extends RemoteDevice with CD-specific state queries.
// Query methods return ErrNotSupported if the IR protocol does not expose them.
type CDPlayer interface {
	RemoteDevice

	// CurrentTrack returns the 1-based track number currently playing.
	CurrentTrack() (int, error)
	// TotalTracks returns the total number of tracks on the disc.
	TotalTracks() (int, error)
	// IsPlaying returns true when the player is actively playing.
	IsPlaying() (bool, error)
	// CurrentTimeSeconds returns elapsed playback time in seconds.
	CurrentTimeSeconds() (int, error)
	// TotalTimeSeconds returns the total disc duration in seconds.
	TotalTimeSeconds() (int, error)
	// Eject opens/closes the tray when supported by the IR profile.
	Eject() error
}
