package amplifier

import (
	"context"
	"errors"
)

// PowerState represents the detected hardware power state of a device.
type PowerState string

const (
	// PowerStateOn indicates the device is powered on and responsive.
	// Confirmed by USB DAC presence or REC-OUT noise floor above the on-threshold.
	PowerStateOn PowerState = "on"

	// PowerStateOff indicates the device is powered off or in standby.
	// Inferred from a low-but-present REC-OUT noise floor (amp noise floor absent).
	PowerStateOff PowerState = "off"

	// PowerStateUnknown means no check could conclusively determine the state.
	// Either no detection method is available, or all checks were inconclusive.
	PowerStateUnknown PowerState = "unknown"
)

// ErrNotSupported is returned by RemoteDevice methods that are not available
// on a particular device (e.g. CurrentTrack() on an amplifier).
var ErrNotSupported = errors.New("operation not supported on this device")

// Input represents a selectable source input on an amplifier.
type Input struct {
	// Label is the user-facing name shown in the UI (e.g. "USB Audio", "Phono").
	Label string `json:"label"`
	// ID is the internal identifier used to address IR commands (e.g. "USB", "PHONO").
	ID string `json:"id"`
}

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

// Amplifier extends RemoteDevice with amplifier-specific operations: input
// management, warm-up timing, and audio readiness signalling.
type Amplifier interface {
	RemoteDevice

	// Input management.
	CurrentInput() (Input, error)
	InputList() []Input
	DefaultInput() Input
	// SetInput switches to the input identified by Input.ID.
	SetInput(id string) error
	// NextInput cycles to the next input in InputList order.
	NextInput() error

	// CurrentState returns whether the amplifier is powered on according to
	// the internal software state machine (based on IR commands sent).
	CurrentState() (powerOn bool, err error)

	// DetectPowerState probes the hardware to determine the actual power state.
	// Unlike CurrentState, this performs real I/O: USB device scan, REC-OUT
	// noise floor analysis, and (in Milestone 5) a blind IR probe.
	// The context controls the total detection timeout.
	DetectPowerState(ctx context.Context) (PowerState, error)

	// WarmupTimeSeconds is the delay (in seconds) after PowerOn before audio
	// is available (e.g. 30 for the Magnat MR 780 tube pre-amp).
	WarmupTimeSeconds() int
	// InputSwitchDelaySeconds is the settling time (in seconds) after SetInput
	// before audio resumes on the new input.
	InputSwitchDelaySeconds() int

	// AudioReady returns false immediately after PowerOn or SetInput, and
	// becomes true once the relevant delay has elapsed.
	AudioReady() bool
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
}
