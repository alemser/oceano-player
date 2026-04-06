package amplifier

import (
	"fmt"
	"sync"
)

// CDPlayerSettings holds all configuration needed to construct a BroadlinkCDPlayer.
// Values are populated from CDPlayerConfig in cmd/oceano-web/config.go.
type CDPlayerSettings struct {
	Maker string
	Model string
	// IRCodes maps command names to base64-encoded Broadlink IR codes.
	// Keys: "power_on", "power_off", "play", "pause", "stop", "next", "previous"
	IRCodes map[string]string
}

// BroadlinkCDPlayer implements RemoteDevice for any IR-controlled CD player
// reachable via a Broadlink RM4 Mini. Device identity is driven entirely by
// CDPlayerSettings — no code changes are needed when switching to a different
// CD player model.
//
// Volume control is not applicable to CD players and returns ErrNotSupported.
// Track and time queries (CDPlayer interface) are not implemented here as most
// IR protocols do not support them; they are deferred to a future milestone.
type BroadlinkCDPlayer struct {
	mu       sync.Mutex
	client   BroadlinkClient
	settings CDPlayerSettings
}

// NewBroadlinkCDPlayer constructs a BroadlinkCDPlayer ready for use.
func NewBroadlinkCDPlayer(client BroadlinkClient, settings CDPlayerSettings) *BroadlinkCDPlayer {
	return &BroadlinkCDPlayer{
		client:   client,
		settings: settings,
	}
}

func (c *BroadlinkCDPlayer) Maker() string { return c.settings.Maker }
func (c *BroadlinkCDPlayer) Model() string { return c.settings.Model }

func (c *BroadlinkCDPlayer) PowerOn() error  { return c.send("power_on") }
func (c *BroadlinkCDPlayer) PowerOff() error { return c.send("power_off") }
func (c *BroadlinkCDPlayer) Play() error     { return c.send("play") }
func (c *BroadlinkCDPlayer) Pause() error    { return c.send("pause") }
func (c *BroadlinkCDPlayer) Stop() error     { return c.send("stop") }
func (c *BroadlinkCDPlayer) Next() error     { return c.send("next") }
func (c *BroadlinkCDPlayer) Previous() error { return c.send("previous") }

// VolumeUp and VolumeDown are not applicable to CD players.
func (c *BroadlinkCDPlayer) VolumeUp() error   { return ErrNotSupported }
func (c *BroadlinkCDPlayer) VolumeDown() error { return ErrNotSupported }

func (c *BroadlinkCDPlayer) send(command string) error {
	c.mu.Lock()
	code := c.settings.IRCodes[command]
	c.mu.Unlock()
	if code == "" {
		return fmt.Errorf("IR code for %q not configured", command)
	}
	return c.client.SendIRCode(code)
}
