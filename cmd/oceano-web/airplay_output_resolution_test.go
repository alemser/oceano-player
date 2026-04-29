package main

import "testing"

func TestResolveAirplayOutputDevice_ExplicitDeviceWins(t *testing.T) {
	cfg := AudioOutputConfig{
		Device:      "plughw:3,0",
		DeviceMatch: "USB Audio Device",
	}
	got := resolveAirplayOutputDevice(cfg)
	if got != "plughw:3,0" {
		t.Fatalf("got %q, want explicit device", got)
	}
}

func TestResolveAirplayOutputDevice_DeviceMatchFound(t *testing.T) {
	scan := func() []ALSADevice {
		return []ALSADevice{
			{Card: 1, Name: "vc4hdmi0", Desc: "vc4-hdmi-0"},
			{Card: 3, Name: "Device", Desc: "USB Audio Device"},
		}
	}

	cfg := AudioOutputConfig{DeviceMatch: "USB Audio Device"}
	got := resolveAirplayOutputDeviceWithScanner(cfg, scan)
	if got != "plughw:3,0" {
		t.Fatalf("got %q, want %q", got, "plughw:3,0")
	}
}

func TestResolveAirplayOutputDevice_DeviceMatchMissingUsesSilentFallback(t *testing.T) {
	scan := func() []ALSADevice {
		return []ALSADevice{
			{Card: 1, Name: "vc4hdmi0", Desc: "vc4-hdmi-0"},
		}
	}

	cfg := AudioOutputConfig{DeviceMatch: "USB Audio Device"}
	got := resolveAirplayOutputDeviceWithScanner(cfg, scan)
	if got != shairportSilentFallbackDevice {
		t.Fatalf("got %q, want %q", got, shairportSilentFallbackDevice)
	}
}

