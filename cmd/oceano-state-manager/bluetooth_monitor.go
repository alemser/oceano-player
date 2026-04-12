package main

import (
	"bufio"
	"context"
	"log"
	"os/exec"
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
		m.bluetoothArtworkPath = ""
		m.bluetoothArtworkKey = ""
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
		"type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',arg0='org.bluez.MediaTransport1'")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Wait() //nolint:errcheck

	log.Printf("bluetooth: dbus-monitor connected (MediaPlayer1 + MediaTransport1)")

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
		if m.bluetoothPlaying != playing {
			m.bluetoothPlaying = playing
			changed = true
			log.Printf("bluetooth: status=%s", status)
		}
	}

	m.mu.Unlock()

	if changed {
		m.markDirty()
	}
}

// applyTransportUpdate handles org.bluez.MediaTransport1 PropertiesChanged
// signals. When the transport becomes active it queries the negotiated codec
// (AAC, SBC, LDAC, etc.) via dbus-send and caches the result.
func (m *mgr) applyTransportUpdate(lines []string) {
	state := parseTransportState(lines)
	if state == "" {
		return
	}

	if state == "active" {
		objectPath := extractSignalPath(lines[0])
		codec := ""
		if objectPath != "" {
			codec = m.queryBluetoothCodec(objectPath)
		}
		m.mu.Lock()
		changed := m.bluetoothCodec != codec
		m.bluetoothCodec = codec
		m.mu.Unlock()
		if changed {
			log.Printf("bluetooth: transport active, codec=%s", codec)
			m.markDirty()
		}
	} else {
		// idle or any other state → clear codec
		m.mu.Lock()
		changed := m.bluetoothCodec != ""
		m.bluetoothCodec = ""
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
