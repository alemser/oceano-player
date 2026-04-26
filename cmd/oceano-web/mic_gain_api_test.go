package main

import "testing"

func TestResolveCardFromPlughwNamedDevice(t *testing.T) {
	devs := []ALSADevice{
		{Card: 0, Name: "vc4hdmi0", Desc: "vc4-hdmi-0"},
		{Card: 1, Name: "vc4hdmi1", Desc: "vc4-hdmi-1"},
		{Card: 3, Name: "Device", Desc: "USB Audio Device"},
	}
	tests := []struct {
		device string
		want   int
		ok     bool
	}{
		{"plughw:CARD=Device,DEV=0", 3, true},
		{"plughw:CARD=device,DEV=0", 3, true},
		{"hw:CARD=Device,DEV=0", 3, true},
		{"plughw:CARD=vc4hdmi0,DEV=0", 0, true},
		{"plughw:CARD=Unknown,DEV=0", 0, false},
	}
	for _, tt := range tests {
		got, ok := resolveCardFromPlughwNamedDevice(tt.device, devs)
		if ok != tt.ok {
			t.Errorf("%q: ok = %v, want %v", tt.device, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("%q: card = %d, want %d", tt.device, got, tt.want)
		}
	}
}
