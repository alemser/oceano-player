package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// runBluetoothMonitor subscribes to BlueZ events via dbus-monitor and updates
// Bluetooth playback state (title/artist/album/codec/playing) in mgr.
// It is a no-op when dbus-monitor is not installed.
//
// Two event types are monitored:
//   - org.bluez.MediaPlayer1 PropertiesChanged → track metadata and play/pause status
//   - org.bluez.MediaTransport1 PropertiesChanged → transport state (active/idle)
//     used to detect which codec is in use (AAC, SBC, LDAC, etc.)
func (m *mgr) runBluetoothMonitor(ctx context.Context) {
	if _, err := exec.LookPath("dbus-monitor"); err != nil {
		log.Printf("bluetooth: dbus-monitor not found — bluetooth monitoring disabled")
		return
	}

	const retryDelay = 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := m.readBluetoothDBus(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("bluetooth: dbus-monitor error: %v — retrying in %s", err, retryDelay)
		}

		// Clear Bluetooth state when the dbus connection drops.
		m.mu.Lock()
		wasPlaying := m.bluetoothPlaying
		m.bluetoothPlaying = false
		m.bluetoothCodec = ""
		m.bluetoothSampleRate = ""
		m.bluetoothBitDepth = ""
		m.bluetoothArtworkPath = ""
		m.bluetoothArtworkKey = ""
		if m.bluetoothStopTimer != nil {
			m.bluetoothStopTimer.Stop()
			m.bluetoothStopTimer = nil
		}
		m.mu.Unlock()
		if wasPlaying {
			m.markDirty()
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
	}
}

// readBluetoothDBus starts a single dbus-monitor subprocess that listens to
// both MediaPlayer1 (track metadata + status) and MediaTransport1 (codec
// negotiation) PropertiesChanged signals.
func (m *mgr) readBluetoothDBus(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "dbus-monitor", "--system",
		"type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',arg0='org.bluez.MediaPlayer1'",
		"type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',arg0='org.bluez.MediaTransport1'",
		"type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',arg0='org.bluez.Device1'")

	cmd.Stderr = io.Discard // prevent stderr pipe buffer from blocking the process
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Wait() //nolint:errcheck

	log.Printf("bluetooth: dbus-monitor connected (MediaPlayer1 + MediaTransport1 + Device1)")

	// Probe for any already-active transport when dbus-monitor first connects.
	// This handles the case where a device was connected and playing before the
	// state manager started, so no PropertiesChanged events were fired.
	go m.queryStartupBluetoothState()

	scanner := bufio.NewScanner(stdout)
	var block []string

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		line := scanner.Text()
		if strings.HasPrefix(line, "signal time=") {
			if len(block) > 0 {
				m.applyBluetoothBlock(block)
			}
			block = []string{line}
		} else {
			block = append(block, line)
		}
	}

	if len(block) > 0 {
		m.applyBluetoothBlock(block)
	}

	return scanner.Err()
}

// applyBluetoothBlock dispatches a parsed dbus-monitor signal block to the
// appropriate handler based on which BlueZ interface the signal is for.
func (m *mgr) applyBluetoothBlock(lines []string) {
	// The interface name is always the first string literal in a
	// PropertiesChanged block (it is arg0 of the signal).
	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		strVal, ok := extractDBusStringValue(t)
		if !ok {
			continue
		}
		switch strVal {
		case "org.bluez.MediaPlayer1":
			m.applyMediaPlayerUpdate(lines)
			return
		case "org.bluez.MediaTransport1":
			m.applyTransportUpdate(lines)
			return
		case "org.bluez.Device1":
			m.applyDeviceUpdate(lines)
			return
		}
	}
}

// applyMediaPlayerUpdate handles track metadata and play/pause status changes
// from org.bluez.MediaPlayer1 PropertiesChanged signals.
func (m *mgr) applyMediaPlayerUpdate(lines []string) {
	title, artist, album, status, hasTrack, hasStatus := parseBluetoothBlock(lines)

	if !hasTrack && !hasStatus {
		return
	}

	m.mu.Lock()
	changed := false

	if hasTrack {
		if m.bluetoothTitle != title || m.bluetoothArtist != artist || m.bluetoothAlbum != album {
			m.bluetoothTitle = title
			m.bluetoothArtist = artist
			m.bluetoothAlbum = album
			changed = true
			if m.cfg.Verbose {
				log.Printf("bluetooth: track: artist=%q title=%q album=%q", artist, title, album)
			}
			// Fetch artwork in background when track changes.
			if artist != "" && album != "" {
				go m.fetchBluetoothArtwork(artist, album)
			}
		}
	}

	if hasStatus {
		playing := status == "playing"
		if playing {
			// Cancel any pending stop debounce and mark as playing immediately.
			if m.bluetoothStopTimer != nil {
				m.bluetoothStopTimer.Stop()
				m.bluetoothStopTimer = nil
			}
			needsCodecProbe := !m.bluetoothPlaying && m.bluetoothCodec == ""
			savedDevicePath := m.bluetoothDevicePath
			if !m.bluetoothPlaying {
				m.bluetoothPlaying = true
				changed = true
				log.Printf("bluetooth: status=playing")
			}
			if needsCodecProbe {
				// BT just started playing and we have no codec info yet.
				// Probe via PipeWire immediately (no delay needed), and also
				// schedule the D-Bus transport probe as a fallback.
				go m.applyPipeWireCodec()
				if savedDevicePath != "" {
					go m.queryInitialBluetoothTransport(savedDevicePath)
				} else {
					go m.queryStartupBluetoothState()
				}
			}
		} else {
			// Debounce stopped: wait 2 s before marking as stopped.
			// AVRCP often sends playing→stopped during connection setup.
			if m.bluetoothPlaying && m.bluetoothStopTimer == nil {
				log.Printf("bluetooth: status=stopped (debouncing)")
				m.bluetoothStopTimer = time.AfterFunc(2*time.Second, func() {
					m.mu.Lock()
					m.bluetoothPlaying = false
					m.bluetoothStopTimer = nil
					m.mu.Unlock()
					log.Printf("bluetooth: status=stopped (confirmed)")
					m.markDirty()
				})
			}
		}
	}

	m.mu.Unlock()

	if changed {
		m.markDirty()
	}
}

// applyTransportUpdate handles org.bluez.MediaTransport1 PropertiesChanged
// signals. When the transport becomes active it queries the negotiated codec,
// sample rate, and bit depth via dbus-send and caches the result.
func (m *mgr) applyTransportUpdate(lines []string) {
	state := parseTransportState(lines)
	if state == "" {
		return
	}

	if state == "active" {
		objectPath := extractSignalPath(lines[0])
		var codec, sampleRate, bitDepth string
		if objectPath != "" {
			codec = m.queryBluetoothCodec(objectPath)
			config := m.queryTransportConfiguration(objectPath)
			sampleRate, bitDepth = parseCodecConfig(codec, config)
		}
		m.mu.Lock()
		changed := m.bluetoothCodec != codec || m.bluetoothSampleRate != sampleRate || m.bluetoothBitDepth != bitDepth
		m.bluetoothCodec = codec
		m.bluetoothSampleRate = sampleRate
		m.bluetoothBitDepth = bitDepth
		m.mu.Unlock()
		if changed {
			log.Printf("bluetooth: transport active, codec=%s rate=%s depth=%s", codec, sampleRate, bitDepth)
			m.markDirty()
		}
	} else {
		// idle or any other state → clear codec and format info
		m.mu.Lock()
		changed := m.bluetoothCodec != "" || m.bluetoothSampleRate != "" || m.bluetoothBitDepth != ""
		m.bluetoothCodec = ""
		m.bluetoothSampleRate = ""
		m.bluetoothBitDepth = ""
		m.mu.Unlock()
		if changed {
			m.markDirty()
		}
	}
}

// queryBluetoothCodec reads the Endpoint property of a MediaTransport1 object
// via dbus-send and maps the endpoint path to a human-readable codec name.
// Returns "" when the codec cannot be determined.
func (m *mgr) queryBluetoothCodec(transportPath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dbus-send",
		"--system", "--print-reply", "--dest=org.bluez",
		transportPath,
		"org.freedesktop.DBus.Properties.Get",
		"string:org.bluez.MediaTransport1",
		"string:Endpoint")

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Output contains a line like:
	//   variant  object path "/MediaEndpoint/A2DPSink/aac"
	for _, line := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(line)
		if strings.Contains(t, "object path") {
			start := strings.Index(t, `"`)
			end := strings.LastIndex(t, `"`)
			if start >= 0 && end > start {
				return mapBluetoothCodec(t[start+1 : end])
			}
		}
	}
	return ""
}

// applyDeviceUpdate handles org.bluez.Device1 PropertiesChanged signals.
// Tracks the Connected property to know when a device connects or disconnects,
// independent of AVRCP playback state.
func (m *mgr) applyDeviceUpdate(lines []string) {
	var pendingKey string
	connected := false
	found := false

	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		strVal, hasStr := extractDBusStringValue(t)
		if hasStr {
			if pendingKey == "Connected" {
				// Connected is a boolean, not a string — skip
				pendingKey = ""
				continue
			}
			switch strVal {
			case "Connected":
				pendingKey = "Connected"
			default:
				pendingKey = ""
			}
			continue
		}
		// Look for boolean value after Connected key.
		if pendingKey == "Connected" {
			t2 := strings.TrimSpace(t)
			if strings.Contains(t2, "boolean true") {
				connected = true
				found = true
				pendingKey = ""
			} else if strings.Contains(t2, "boolean false") {
				connected = false
				found = true
				pendingKey = ""
			}
		}
	}

	if !found {
		return
	}

	var devicePath string
	if connected {
		devicePath = extractSignalPath(lines[0])
	}

	m.mu.Lock()
	changed := m.bluetoothConnected != connected
	m.bluetoothConnected = connected
	if connected {
		m.bluetoothDevicePath = devicePath
	} else {
		// Device disconnected — clear all Bluetooth state immediately.
		m.bluetoothDevicePath = ""
		m.bluetoothPlaying = false
		m.bluetoothTitle = ""
		m.bluetoothArtist = ""
		m.bluetoothAlbum = ""
		m.bluetoothCodec = ""
		m.bluetoothSampleRate = ""
		m.bluetoothBitDepth = ""
		m.bluetoothArtworkPath = ""
		m.bluetoothArtworkKey = ""
		if m.bluetoothStopTimer != nil {
			m.bluetoothStopTimer.Stop()
			m.bluetoothStopTimer = nil
		}
	}
	m.mu.Unlock()

	if changed {
		log.Printf("bluetooth: device connected=%v", connected)
		m.markDirty()
		// When a device connects, the transport may already be active (e.g. the
		// device was playing before the state manager started). Query it directly
		// so the codec/rate/depth chips appear without waiting for a state change.
		if connected && devicePath != "" {
			go m.queryInitialBluetoothTransport(devicePath)
		}
	}
}

// fetchBluetoothArtwork fetches album artwork for a Bluetooth track via the
// iTunes Search API and caches the result in m.bluetoothArtworkPath.
// Uses a key to avoid re-fetching the same artist+album combination.
func (m *mgr) fetchBluetoothArtwork(artist, album string) {
	key := artist + "\x00" + album

	m.mu.Lock()
	if m.bluetoothArtworkKey == key {
		m.mu.Unlock()
		return
	}
	artworkDir := m.cfg.ArtworkDir
	m.mu.Unlock()

	path, err := fetchArtwork(artist, album, artworkDir)
	if err != nil {
		log.Printf("bluetooth: artwork fetch error: %v", err)
		return
	}

	m.mu.Lock()
	// Only apply if the track hasn't changed while we were fetching.
	if m.bluetoothArtist == artist && m.bluetoothAlbum == album {
		m.bluetoothArtworkPath = path
		m.bluetoothArtworkKey = key
	}
	m.mu.Unlock()

	if path != "" {
		log.Printf("bluetooth: artwork fetched for %q by %q", album, artist)
		m.markDirty()
	}
}

// parseBluetoothBlock extracts track metadata and status from one dbus-monitor
// signal block for org.bluez.MediaPlayer1. See bluetooth_monitor.go for the
// state machine description.
func parseBluetoothBlock(lines []string) (title, artist, album, status string, hasTrack, hasStatus bool) {
	var pendingKey string

	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}

		strVal, hasStr := extractDBusStringValue(t)

		if !hasStr {
			switch {
			case t == "array [", t == "]", strings.HasPrefix(t, "dict entry"), t == ")":
				// structural — preserve pendingKey
			case strings.HasPrefix(t, "variant"):
				// variant without inline string — preserve pendingKey
			default:
				pendingKey = ""
			}
			continue
		}

		if pendingKey != "" && pendingKey != "Track" {
			switch pendingKey {
			case "Title":
				title = strVal
				hasTrack = true
			case "Artist":
				artist = strVal
				hasTrack = true
			case "Album":
				album = strVal
				hasTrack = true
			case "Status":
				status = strVal
				hasStatus = true
			}
			pendingKey = ""
		} else {
			switch strVal {
			case "Title", "Artist", "Album", "Status", "Track":
				pendingKey = strVal
			default:
				pendingKey = ""
			}
		}
	}
	return
}

// parseTransportState extracts the State value from an org.bluez.MediaTransport1
// PropertiesChanged block. Returns "" when no State key is found.
func parseTransportState(lines []string) string {
	var pendingKey string
	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		strVal, hasStr := extractDBusStringValue(t)
		if !hasStr {
			if !strings.HasPrefix(t, "variant") && t != "array [" && t != "]" &&
				!strings.HasPrefix(t, "dict entry") && t != ")" {
				pendingKey = ""
			}
			continue
		}
		if pendingKey == "State" {
			return strVal
		}
		if strVal == "State" {
			pendingKey = "State"
		} else {
			pendingKey = ""
		}
	}
	return ""
}

// extractSignalPath extracts the D-Bus object path from a dbus-monitor signal
// header line, e.g.: "signal time=... path=/org/bluez/.../fd0; interface=..."
func extractSignalPath(headerLine string) string {
	idx := strings.Index(headerLine, " path=")
	if idx < 0 {
		return ""
	}
	rest := headerLine[idx+6:]
	end := strings.IndexByte(rest, ';')
	if end < 0 {
		end = strings.IndexByte(rest, ' ')
	}
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// mapBluetoothCodec maps a BlueZ MediaEndpoint object path to a human-readable
// codec name. The path is typically set by PipeWire and encodes the codec name
// as the last path component (e.g. "/MediaEndpoint/A2DPSink/aac" → "AAC").
func mapBluetoothCodec(endpointPath string) string {
	idx := strings.LastIndex(endpointPath, "/")
	if idx < 0 {
		return ""
	}
	name := strings.ToLower(endpointPath[idx+1:])
	switch {
	case name == "sbc", name == "sbc_xq":
		return "SBC"
	case name == "aac":
		return "AAC"
	case name == "ldac":
		return "LDAC"
	case name == "aptx_hd":
		return "AptX HD"
	case strings.HasPrefix(name, "aptx_ll"):
		return "AptX LL"
	case strings.HasPrefix(name, "aptx"):
		return "AptX"
	case strings.HasPrefix(name, "opus"):
		return "Opus"
	case strings.HasPrefix(name, "faststream"):
		return "FastStream"
	default:
		return ""
	}
}

// ─── Transport configuration ─────────────────────────────────────────────────

// queryStartupBluetoothState enumerates all BlueZ managed objects at startup to
// find any MediaTransport1 that is already active. This handles the common case
// where the BT device was connected and playing before the state manager started,
// so no PropertiesChanged signal was ever fired for the transport state.
func (m *mgr) queryStartupBluetoothState() {
	// Brief delay so BlueZ has time to finish A2DP negotiation.
	time.Sleep(1 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Enumerate all managed BlueZ objects to find transport paths.
	listCmd := exec.CommandContext(ctx, "dbus-send",
		"--system", "--print-reply", "--dest=org.bluez",
		"/", "org.freedesktop.DBus.ObjectManager.GetManagedObjects")
	listOut, err := listCmd.Output()
	if err != nil {
		return
	}

	// Transport objects are named fdN under a device path,
	// e.g. /org/bluez/hci0/dev_XX_XX_XX_XX_XX_XX/fd0.
	for _, line := range strings.Split(string(listOut), "\n") {
		t := strings.TrimSpace(line)
		if !strings.Contains(t, "object path") {
			continue
		}
		start := strings.Index(t, `"`)
		end := strings.LastIndex(t, `"`)
		if start < 0 || end <= start {
			continue
		}
		path := t[start+1 : end]
		parts := strings.Split(path, "/")
		if len(parts) == 0 {
			continue
		}
		last := parts[len(parts)-1]
		if !strings.HasPrefix(last, "fd") {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimPrefix(last, "fd")); err != nil {
			continue
		}

		// Query the transport state.
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		stateCmd := exec.CommandContext(ctx2, "dbus-send",
			"--system", "--print-reply", "--dest=org.bluez",
			path,
			"org.freedesktop.DBus.Properties.Get",
			"string:org.bluez.MediaTransport1",
			"string:State")
		stateOut, err := stateCmd.Output()
		cancel2()
		if err != nil {
			continue
		}
		if !strings.Contains(string(stateOut), `string "active"`) {
			continue
		}

		// Active transport found — query codec and configuration.
		codec := m.queryBluetoothCodec(path)
		config := m.queryTransportConfiguration(path)
		sampleRate, bitDepth := parseCodecConfig(codec, config)

		m.mu.Lock()
		changed := m.bluetoothCodec != codec || m.bluetoothSampleRate != sampleRate || m.bluetoothBitDepth != bitDepth
		m.bluetoothCodec = codec
		m.bluetoothSampleRate = sampleRate
		m.bluetoothBitDepth = bitDepth
		m.mu.Unlock()

		if changed && codec != "" {
			log.Printf("bluetooth: startup transport %s: codec=%s rate=%s depth=%s", path, codec, sampleRate, bitDepth)
			m.markDirty()
		}
		return // use first active transport found
	}

	// No active D-Bus transport found — fall back to PipeWire.
	m.applyPipeWireCodec()
}

// queryTransportConfiguration reads the raw Configuration byte array from a
// BlueZ MediaTransport1 object. Returns nil when the object does not exist or
// the property is unavailable (e.g. transport is idle).
func (m *mgr) queryTransportConfiguration(transportPath string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dbus-send",
		"--system", "--print-reply", "--dest=org.bluez",
		transportPath,
		"org.freedesktop.DBus.Properties.Get",
		"string:org.bluez.MediaTransport1",
		"string:Configuration")

	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseDBusByteArray(string(out))
}

// queryInitialBluetoothTransport looks for an already-active MediaTransport1
// object under devicePath (e.g. /org/bluez/hci0/dev_XX_XX_XX_XX_XX_XX) and
// populates codec, sample rate, and bit depth when found. Called when a device
// connects to catch transports that became active before dbus-monitor started.
func (m *mgr) queryInitialBluetoothTransport(devicePath string) {
	// Wait briefly for the A2DP transport to be negotiated.
	time.Sleep(3 * time.Second)

	for _, fd := range []string{"fd0", "fd1", "fd2", "fd3", "fd4"} {
		transportPath := devicePath + "/" + fd

		// Check if this object has an active/pending transport state.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, "dbus-send",
			"--system", "--print-reply", "--dest=org.bluez",
			transportPath,
			"org.freedesktop.DBus.Properties.Get",
			"string:org.bluez.MediaTransport1",
			"string:State")
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		state := ""
		if strings.Contains(string(out), `string "active"`) {
			state = "active"
		} else if strings.Contains(string(out), `string "pending"`) {
			state = "pending"
		}
		if state == "" {
			continue
		}

		codec := m.queryBluetoothCodec(transportPath)
		if codec == "" {
			continue
		}
		config := m.queryTransportConfiguration(transportPath)
		sampleRate, bitDepth := parseCodecConfig(codec, config)

		m.mu.Lock()
		changed := m.bluetoothCodec != codec || m.bluetoothSampleRate != sampleRate || m.bluetoothBitDepth != bitDepth
		m.bluetoothCodec = codec
		m.bluetoothSampleRate = sampleRate
		m.bluetoothBitDepth = bitDepth
		m.mu.Unlock()

		if changed {
			log.Printf("bluetooth: initial transport %s: codec=%s rate=%s depth=%s", transportPath, codec, sampleRate, bitDepth)
			m.markDirty()
		}
		return
	}

	// No D-Bus transport found for device — fall back to PipeWire.
	m.applyPipeWireCodec()
}

// parseDBusByteArray extracts decimal byte values from the dbus-send output of
// a property of type "array of bytes", e.g.:
//
//	variant   array of bytes [
//	    33 2 2 53
//	]
func parseDBusByteArray(output string) []byte {
	start := strings.Index(output, "[")
	end := strings.LastIndex(output, "]")
	if start < 0 || end <= start {
		return nil
	}
	var result []byte
	for _, field := range strings.Fields(output[start+1 : end]) {
		n, err := strconv.ParseUint(field, 10, 8)
		if err != nil {
			continue
		}
		result = append(result, byte(n))
	}
	return result
}

// parseCodecConfig derives sample rate and bit depth from the raw A2DP
// Configuration bytes for the given codec name. Returns ("", "") when the
// codec is unknown or the configuration is too short to parse.
func parseCodecConfig(codec string, config []byte) (sampleRate, bitDepth string) {
	switch codec {
	case "SBC":
		return parseSBCConfig(config)
	case "AAC":
		return parseAACConfig(config)
	case "LDAC":
		return parseLDACConfig(config)
	case "AptX HD":
		return "48 kHz", "24 bit"
	case "AptX", "AptX LL":
		return "44.1 kHz", "16 bit"
	case "Opus", "FastStream":
		return "48 kHz", "16 bit"
	}
	return "", ""
}

// parseSBCConfig decodes the A2DP SBC configuration octet.
// Octet 0 bits 7-4: one bit set per chosen sampling frequency (16/32/44.1/48 kHz).
// SBC PCM depth is always 16 bit.
func parseSBCConfig(config []byte) (string, string) {
	if len(config) < 1 {
		return "", "16 bit"
	}
	b := config[0]
	switch {
	case b&0x80 != 0:
		return "16 kHz", "16 bit"
	case b&0x40 != 0:
		return "32 kHz", "16 bit"
	case b&0x20 != 0:
		return "44.1 kHz", "16 bit"
	case b&0x10 != 0:
		return "48 kHz", "16 bit"
	}
	return "", "16 bit"
}

// parseAACConfig decodes the A2DP MPEG-2,4 AAC configuration octets.
// Octet 1 bit 0: 44100 Hz; octet 2 bit 7: 48000 Hz.
// AAC via Bluetooth A2DP is always 16 bit.
func parseAACConfig(config []byte) (string, string) {
	if len(config) < 2 {
		return "", "16 bit"
	}
	switch {
	case config[1]&0x01 != 0:
		return "44.1 kHz", "16 bit"
	case len(config) >= 3 && config[2]&0x80 != 0:
		return "48 kHz", "16 bit"
	}
	return "", "16 bit"
}

// parseLDACConfig decodes the vendor-specific LDAC A2DP configuration.
// Layout: 4 bytes vendor ID + 2 bytes codec ID + 1 byte freq + 1 byte channel.
// Frequency byte bits: 5=44.1 kHz, 4=48 kHz, 2=88.2 kHz, 1=96 kHz.
// LDAC PCM depth in PipeWire is always 24 bit.
func parseLDACConfig(config []byte) (string, string) {
	if len(config) < 7 {
		return "", "24 bit"
	}
	freq := config[6]
	switch {
	case freq&(1<<5) != 0:
		return "44.1 kHz", "24 bit"
	case freq&(1<<4) != 0:
		return "48 kHz", "24 bit"
	case freq&(1<<2) != 0:
		return "88.2 kHz", "24 bit"
	case freq&(1<<1) != 0:
		return "96 kHz", "24 bit"
	}
	return "", "24 bit"
}

// ─── PipeWire codec probe ─────────────────────────────────────────────────────

// applyPipeWireCodec queries PipeWire for the active Bluetooth A2DP sink and
// applies the codec, sample rate, and bit depth to the manager state.
// Called as a goroutine when BT starts playing and no codec is known yet.
func (m *mgr) applyPipeWireCodec() {
	codec, sampleRate, bitDepth := queryPipeWireBluetoothCodec()
	if codec == "" {
		return
	}
	m.mu.Lock()
	changed := m.bluetoothCodec != codec || m.bluetoothSampleRate != sampleRate || m.bluetoothBitDepth != bitDepth
	m.bluetoothCodec = codec
	m.bluetoothSampleRate = sampleRate
	m.bluetoothBitDepth = bitDepth
	m.mu.Unlock()
	if changed {
		log.Printf("bluetooth: pipewire codec=%s rate=%s depth=%s", codec, sampleRate, bitDepth)
		m.markDirty()
	}
}

// queryPipeWireBluetoothCodec queries pw-dump for the active Bluetooth A2DP
// sink node and extracts the codec name, sample rate, and bit depth from its
// PipeWire node properties. Returns empty strings when pw-dump is not
// installed or no Bluetooth A2DP sink is active.
func queryPipeWireBluetoothCodec() (codec, sampleRate, bitDepth string) {
	if _, err := exec.LookPath("pw-dump"); err != nil {
		return "", "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "pw-dump").Output()
	if err != nil {
		return "", "", ""
	}

	// pw-dump emits a JSON array of PipeWire objects.
	var objects []struct {
		Type string `json:"type"`
		Info struct {
			Props map[string]interface{} `json:"props"`
		} `json:"info"`
	}
	if err := json.Unmarshal(out, &objects); err != nil {
		return "", "", ""
	}

	for _, obj := range objects {
		if obj.Type != "PipeWire:Interface:Node" {
			continue
		}
		props := obj.Info.Props
		if props == nil {
			continue
		}
		mediaClass, _ := props["media.class"].(string)
		if mediaClass != "Audio/Sink" {
			continue
		}
		// Only Bluetooth A2DP sinks have api.bluez5.address.
		btAddr, _ := props["api.bluez5.address"].(string)
		if btAddr == "" {
			continue
		}
		rawCodec, _ := props["api.bluez5.codec"].(string)
		if rawCodec == "" {
			continue
		}
		codec = mapPWBluetoothCodec(rawCodec)

		if rate, ok := props["audio.rate"].(float64); ok {
			switch int(rate) {
			case 16000:
				sampleRate = "16 kHz"
			case 32000:
				sampleRate = "32 kHz"
			case 44100:
				sampleRate = "44.1 kHz"
			case 48000:
				sampleRate = "48 kHz"
			case 88200:
				sampleRate = "88.2 kHz"
			case 96000:
				sampleRate = "96 kHz"
			default:
				sampleRate = strconv.Itoa(int(rate)) + " Hz"
			}
		}

		if format, ok := props["audio.format"].(string); ok {
			switch strings.ToUpper(format) {
			case "S16LE", "S16BE", "S16":
				bitDepth = "16 bit"
			case "S24LE", "S24BE", "S24", "S24_32LE", "S24_32BE":
				bitDepth = "24 bit"
			case "S32LE", "S32BE", "S32", "F32LE", "F32BE":
				bitDepth = "32 bit"
			}
		}
		return codec, sampleRate, bitDepth
	}
	return "", "", ""
}

// mapPWBluetoothCodec maps a PipeWire api.bluez5.codec property value to the
// same display names used by mapBluetoothCodec (from BlueZ endpoint paths).
func mapPWBluetoothCodec(pwCodec string) string {
	switch strings.ToLower(pwCodec) {
	case "sbc", "sbc_xq":
		return "SBC"
	case "aac":
		return "AAC"
	case "ldac":
		return "LDAC"
	case "aptx_hd":
		return "AptX HD"
	case "aptx_ll":
		return "AptX LL"
	case "aptx":
		return "AptX"
	case "opus":
		return "Opus"
	case "faststream":
		return "FastStream"
	default:
		return ""
	}
}

// extractDBusStringValue extracts the string value from a dbus-monitor output
// line that contains a string literal, e.g.:
//
//	string "Miles Davis"
//	variant                   string "Kind Of Blue"
//
// Returns ("", false) when no string literal is found on the line.
func extractDBusStringValue(line string) (string, bool) {
	idx := strings.Index(line, `string "`)
	if idx < 0 {
		return "", false
	}
	rest := line[idx+len(`string "`):]
	end := strings.LastIndex(rest, `"`)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}
