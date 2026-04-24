package main

import (
	"testing"
)

// --- extractDBusStringValue ---

func TestExtractDBusStringValue(t *testing.T) {
	tests := []struct {
		line   string
		want   string
		wantOK bool
	}{
		{`   string "Miles Davis"`, "Miles Davis", true},
		{`variant                   string "Kind Of Blue"`, "Kind Of Blue", true},
		{`string ""`, "", true},
		{`string "has \"quoted\" inside"`, `has \"quoted\" inside`, true},
		{`uint32 4`, "", false},
		{`array [`, "", false},
		{``, "", false},
		{`string "only open`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got, ok := extractDBusStringValue(tt.line)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("extractDBusStringValue(%q) = (%q, %v), want (%q, %v)",
					tt.line, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// --- mapBluetoothCodec ---

func TestMapBluetoothCodec(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/MediaEndpoint/A2DPSink/sbc", "SBC"},
		{"/MediaEndpoint/A2DPSink/sbc_xq", "SBC"},
		{"/MediaEndpoint/A2DPSink/aac", "AAC"},
		{"/MediaEndpoint/A2DPSink/ldac", "LDAC"},
		{"/MediaEndpoint/A2DPSink/aptx_hd", "AptX HD"},
		{"/MediaEndpoint/A2DPSink/aptx_ll", "AptX LL"},
		{"/MediaEndpoint/A2DPSink/aptx_ll_duplex", "AptX LL"},
		{"/MediaEndpoint/A2DPSink/aptx", "AptX"},
		{"/MediaEndpoint/A2DPSink/opus_05", "Opus"},
		{"/MediaEndpoint/A2DPSink/opus_05_duplex", "Opus"},
		{"/MediaEndpoint/A2DPSink/faststream", "FastStream"},
		{"/MediaEndpoint/A2DPSink/faststream_duplex", "FastStream"},
		{"/MediaEndpoint/A2DPSink/unknown_codec", ""},
		{"no_slash", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := mapBluetoothCodec(tt.path)
			if got != tt.want {
				t.Errorf("mapBluetoothCodec(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// --- extractSignalPath ---

func TestExtractSignalPath(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{
			`signal time=1234.567890 sender=:1.42 -> destination=(null destination) serial=99 path=/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/fd0; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged`,
			"/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/fd0",
		},
		{
			`signal time=1234.5 sender=:1.1 -> destination=(null destination) serial=1 path=/org/bluez/hci0; interface=org.bluez.Adapter1; member=PropertiesChanged`,
			"/org/bluez/hci0",
		},
		// path at end of line (no semicolon, no space after)
		{
			`signal time=1.0 sender=:1.1 path=/org/bluez/hci0/fd1`,
			"/org/bluez/hci0/fd1",
		},
		{"no path here", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := extractSignalPath(tt.line)
			if got != tt.want {
				t.Errorf("extractSignalPath(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

// --- parseTransportState ---

func TestParseTransportState(t *testing.T) {
	// Realistic dbus-monitor block for a MediaTransport1 PropertiesChanged signal
	// when transport becomes active.
	activeBlock := []string{
		`signal time=1234.567890 sender=:1.42 -> destination=(null destination) serial=5 path=/org/bluez/hci0/dev_AA_BB_CC/fd0; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged`,
		`   string "org.bluez.MediaTransport1"`,
		`   array [`,
		`      dict entry(`,
		`         string "State"`,
		`         variant             string "active"`,
		`      )`,
		`   ]`,
		`   array [`,
		`   ]`,
	}

	idleBlock := []string{
		`signal time=1234.0 sender=:1.42 -> destination=(null destination) serial=6 path=/org/bluez/hci0/dev_AA_BB_CC/fd0; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged`,
		`   string "org.bluez.MediaTransport1"`,
		`   array [`,
		`      dict entry(`,
		`         string "State"`,
		`         variant             string "idle"`,
		`      )`,
		`   ]`,
		`   array [`,
		`   ]`,
	}

	noStateBlock := []string{
		`signal time=1234.0 sender=:1.42 serial=1 path=/org/bluez/hci0/fd0; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged`,
		`   string "org.bluez.MediaTransport1"`,
		`   array [`,
		`      dict entry(`,
		`         string "Volume"`,
		`         variant             uint16 127`,
		`      )`,
		`   ]`,
		`   array [`,
		`   ]`,
	}

	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{"active", activeBlock, "active"},
		{"idle", idleBlock, "idle"},
		{"no State key", noStateBlock, ""},
		{"empty", []string{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTransportState(tt.lines)
			if got != tt.want {
				t.Errorf("parseTransportState() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- codec config parsers ---

func TestParseDBusByteArray(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []byte
	}{
		{
			name:  "SBC 44.1 kHz joint stereo",
			input: "method return ...\n   variant       array of bytes [\n         33 2 2 53\n      ]\n",
			want:  []byte{33, 2, 2, 53},
		},
		{
			name:  "single byte",
			input: "   variant   array of bytes [\n  5\n]\n",
			want:  []byte{5},
		},
		{
			name:  "no brackets",
			input: "no data here",
			want:  nil,
		},
		{
			name:  "empty brackets",
			input: "array of bytes []",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDBusByteArray(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("parseDBusByteArray() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("byte[%d] = %d, want %d", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseSBCConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    []byte
		wantRate  string
		wantDepth string
	}{
		{"44.1 kHz joint stereo", []byte{0x21, 0x02, 0x02, 0x35}, "44.1 kHz", "16 bit"},
		{"48 kHz stereo", []byte{0x12, 0x12, 0x02, 0x35}, "48 kHz", "16 bit"},
		{"32 kHz mono", []byte{0x48, 0x11, 0x02, 0x35}, "32 kHz", "16 bit"},
		{"16 kHz", []byte{0x88, 0x11, 0x02, 0x35}, "16 kHz", "16 bit"},
		{"empty config", []byte{}, "", "16 bit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, depth := parseSBCConfig(tt.config)
			if rate != tt.wantRate || depth != tt.wantDepth {
				t.Errorf("parseSBCConfig() = (%q, %q), want (%q, %q)", rate, depth, tt.wantRate, tt.wantDepth)
			}
		})
	}
}

func TestParseAACConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    []byte
		wantRate  string
		wantDepth string
	}{
		// Byte 1 bit 0 = 44.1 kHz
		{"44.1 kHz", []byte{0x80, 0x01, 0x00, 0x00, 0x00}, "44.1 kHz", "16 bit"},
		// Byte 2 bit 7 = 48 kHz
		{"48 kHz", []byte{0x80, 0x00, 0x80, 0x00, 0x00}, "48 kHz", "16 bit"},
		{"too short", []byte{0x80}, "", "16 bit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, depth := parseAACConfig(tt.config)
			if rate != tt.wantRate || depth != tt.wantDepth {
				t.Errorf("parseAACConfig() = (%q, %q), want (%q, %q)", rate, depth, tt.wantRate, tt.wantDepth)
			}
		})
	}
}

func TestParseLDACConfig(t *testing.T) {
	// LDAC config: 4 bytes vendor ID + 2 bytes codec ID + 1 byte freq + 1 byte channel
	// freq byte: bit5=44.1, bit4=48, bit2=88.2, bit1=96
	makeConfig := func(freqByte byte) []byte {
		return []byte{0x2D, 0x01, 0x00, 0x00, 0xAA, 0x00, freqByte, 0x04}
	}
	tests := []struct {
		name      string
		config    []byte
		wantRate  string
		wantDepth string
	}{
		{"44.1 kHz", makeConfig(1 << 5), "44.1 kHz", "24 bit"},
		{"48 kHz", makeConfig(1 << 4), "48 kHz", "24 bit"},
		{"88.2 kHz", makeConfig(1 << 2), "88.2 kHz", "24 bit"},
		{"96 kHz", makeConfig(1 << 1), "96 kHz", "24 bit"},
		{"too short", []byte{0x2D, 0x01}, "", "24 bit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, depth := parseLDACConfig(tt.config)
			if rate != tt.wantRate || depth != tt.wantDepth {
				t.Errorf("parseLDACConfig() = (%q, %q), want (%q, %q)", rate, depth, tt.wantRate, tt.wantDepth)
			}
		})
	}
}

func TestParseCodecConfig(t *testing.T) {
	tests := []struct {
		codec     string
		config    []byte
		wantRate  string
		wantDepth string
	}{
		{"SBC", []byte{0x20, 0x02, 0x02, 0x35}, "44.1 kHz", "16 bit"},
		{"AAC", []byte{0x80, 0x01, 0x00, 0x00, 0x00}, "44.1 kHz", "16 bit"},
		{"AptX HD", nil, "48 kHz", "24 bit"},
		{"AptX", nil, "44.1 kHz", "16 bit"},
		{"AptX LL", nil, "44.1 kHz", "16 bit"},
		{"Opus", nil, "48 kHz", "16 bit"},
		{"FastStream", nil, "48 kHz", "16 bit"},
		{"unknown", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.codec, func(t *testing.T) {
			rate, depth := parseCodecConfig(tt.codec, tt.config)
			if rate != tt.wantRate || depth != tt.wantDepth {
				t.Errorf("parseCodecConfig(%q) = (%q, %q), want (%q, %q)", tt.codec, rate, depth, tt.wantRate, tt.wantDepth)
			}
		})
	}
}

// --- parseBluetoothBlock ---

func TestParseBluetoothBlock(t *testing.T) {
	// Full track + status update block (typical AVRCP metadata signal).
	fullBlock := []string{
		`signal time=1234.0 sender=:1.42 serial=10 path=/org/bluez/hci0/dev_AA/player0; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged`,
		`   string "org.bluez.MediaPlayer1"`,
		`   array [`,
		`      dict entry(`,
		`         string "Track"`,
		`         variant             array [`,
		`            dict entry(`,
		`               string "Title"`,
		`               variant                   string "So What"`,
		`            )`,
		`            dict entry(`,
		`               string "Artist"`,
		`               variant                   string "Miles Davis"`,
		`            )`,
		`            dict entry(`,
		`               string "Album"`,
		`               variant                   string "Kind Of Blue"`,
		`            )`,
		`            dict entry(`,
		`               string "Duration"`,
		`               variant                   uint32 564000`,
		`            )`,
		`         ]`,
		`      )`,
		`      dict entry(`,
		`         string "Status"`,
		`         variant             string "playing"`,
		`      )`,
		`   ]`,
		`   array [`,
		`   ]`,
	}

	// Status-only update (pause event, no track dict).
	pauseBlock := []string{
		`signal time=1235.0 sender=:1.42 serial=11 path=/org/bluez/hci0/dev_AA/player0; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged`,
		`   string "org.bluez.MediaPlayer1"`,
		`   array [`,
		`      dict entry(`,
		`         string "Status"`,
		`         variant             string "paused"`,
		`      )`,
		`   ]`,
		`   array [`,
		`   ]`,
	}

	// Track-only update (track changed, no status).
	trackOnlyBlock := []string{
		`signal time=1236.0 sender=:1.42 serial=12 path=/org/bluez/hci0/dev_AA/player0; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged`,
		`   string "org.bluez.MediaPlayer1"`,
		`   array [`,
		`      dict entry(`,
		`         string "Track"`,
		`         variant             array [`,
		`            dict entry(`,
		`               string "Title"`,
		`               variant                   string "Blue in Green"`,
		`            )`,
		`            dict entry(`,
		`               string "Artist"`,
		`               variant                   string "Miles Davis"`,
		`            )`,
		`         ]`,
		`      )`,
		`   ]`,
		`   array [`,
		`   ]`,
	}

	// Position-only update (sent as a separate top-level property change).
	positionBlock := []string{
		`signal time=1237.0 sender=:1.42 serial=13 path=/org/bluez/hci0/dev_AA/player0; interface=org.freedesktop.DBus.Properties; member=PropertiesChanged`,
		`   string "org.bluez.MediaPlayer1"`,
		`   array [`,
		`      dict entry(`,
		`         string "Position"`,
		`         variant             uint32 120000`,
		`      )`,
		`   ]`,
		`   array [`,
		`   ]`,
	}

	tests := []struct {
		name            string
		lines           []string
		wantTitle       string
		wantArtist      string
		wantAlbum       string
		wantStatus      string
		wantTrack       bool
		wantStat        bool
		wantDurationMS  int64
		wantHasDuration bool
		wantPositionMS  int64
		wantHasPosition bool
	}{
		{
			name:            "full block",
			lines:           fullBlock,
			wantTitle:       "So What",
			wantArtist:      "Miles Davis",
			wantAlbum:       "Kind Of Blue",
			wantStatus:      "playing",
			wantTrack:       true,
			wantStat:        true,
			wantDurationMS:  564000,
			wantHasDuration: true,
		},
		{
			name:       "pause only",
			lines:      pauseBlock,
			wantStatus: "paused",
			wantTrack:  false,
			wantStat:   true,
		},
		{
			name:       "track only",
			lines:      trackOnlyBlock,
			wantTitle:  "Blue in Green",
			wantArtist: "Miles Davis",
			wantTrack:  true,
			wantStat:   false,
		},
		{
			name:            "position only",
			lines:           positionBlock,
			wantHasPosition: true,
			wantPositionMS:  120000,
		},
		{
			name:  "empty",
			lines: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := parseBluetoothBlock(tt.lines)
			if b.title != tt.wantTitle {
				t.Errorf("title = %q, want %q", b.title, tt.wantTitle)
			}
			if b.artist != tt.wantArtist {
				t.Errorf("artist = %q, want %q", b.artist, tt.wantArtist)
			}
			if b.album != tt.wantAlbum {
				t.Errorf("album = %q, want %q", b.album, tt.wantAlbum)
			}
			if b.status != tt.wantStatus {
				t.Errorf("status = %q, want %q", b.status, tt.wantStatus)
			}
			if b.hasTrack != tt.wantTrack {
				t.Errorf("hasTrack = %v, want %v", b.hasTrack, tt.wantTrack)
			}
			if b.hasStatus != tt.wantStat {
				t.Errorf("hasStatus = %v, want %v", b.hasStatus, tt.wantStat)
			}
			if b.hasDuration != tt.wantHasDuration {
				t.Errorf("hasDuration = %v, want %v", b.hasDuration, tt.wantHasDuration)
			}
			if b.durationMS != tt.wantDurationMS {
				t.Errorf("durationMS = %d, want %d", b.durationMS, tt.wantDurationMS)
			}
			if b.hasPosition != tt.wantHasPosition {
				t.Errorf("hasPosition = %v, want %v", b.hasPosition, tt.wantHasPosition)
			}
			if b.positionMS != tt.wantPositionMS {
				t.Errorf("positionMS = %d, want %d", b.positionMS, tt.wantPositionMS)
			}
		})
	}
}
