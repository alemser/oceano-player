package main

import "testing"

// --- extractDBusStringValue ---

func TestExtractDBusStringValue(t *testing.T) {
	tests := []struct {
		line    string
		want    string
		wantOK  bool
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

	tests := []struct {
		name      string
		lines     []string
		wantTitle  string
		wantArtist string
		wantAlbum  string
		wantStatus string
		wantTrack  bool
		wantStat   bool
	}{
		{
			name:       "full block",
			lines:      fullBlock,
			wantTitle:  "So What",
			wantArtist: "Miles Davis",
			wantAlbum:  "Kind Of Blue",
			wantStatus: "playing",
			wantTrack:  true,
			wantStat:   true,
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
			name:  "empty",
			lines: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, artist, album, status, hasTrack, hasStatus := parseBluetoothBlock(tt.lines)
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			if artist != tt.wantArtist {
				t.Errorf("artist = %q, want %q", artist, tt.wantArtist)
			}
			if album != tt.wantAlbum {
				t.Errorf("album = %q, want %q", album, tt.wantAlbum)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if hasTrack != tt.wantTrack {
				t.Errorf("hasTrack = %v, want %v", hasTrack, tt.wantTrack)
			}
			if hasStatus != tt.wantStat {
				t.Errorf("hasStatus = %v, want %v", hasStatus, tt.wantStat)
			}
		})
	}
}
