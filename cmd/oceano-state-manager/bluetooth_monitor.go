package main

import (
	"bufio"
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

// runBluetoothMonitor subscribes to BlueZ AVRCP events via dbus-monitor and
// updates Bluetooth playback state (title/artist/album/playing) in mgr.
// It is a no-op when dbus-monitor is not installed.
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

// readBluetoothDBus starts dbus-monitor filtered to BlueZ MediaPlayer1
// PropertiesChanged signals, buffers output into per-signal blocks, and
// applies each block to mgr state. Returns when the subprocess exits or
// the context is cancelled.
func (m *mgr) readBluetoothDBus(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "dbus-monitor", "--system",
		"type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',arg0='org.bluez.MediaPlayer1'")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Wait() //nolint:errcheck

	log.Printf("bluetooth: dbus-monitor connected (listening for BlueZ AVRCP events)")

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

	// Process the final block.
	if len(block) > 0 {
		m.applyBluetoothBlock(block)
	}

	return scanner.Err()
}

// applyBluetoothBlock parses a single dbus-monitor signal block and updates
// mgr state when track metadata or playback status has changed.
func (m *mgr) applyBluetoothBlock(lines []string) {
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

// parseBluetoothBlock extracts track metadata and status from the lines of one
// dbus-monitor signal block. It uses a simple key-tracking state machine:
//
//   - When a string literal matches a known key name (Title/Artist/Album/Status/Track)
//     it sets pendingKey.
//   - The next string literal is consumed as the value for pendingKey.
//   - Structural dbus-monitor lines (array [, dict entry, variant ...) preserve
//     pendingKey so multi-line variant values are handled correctly.
func parseBluetoothBlock(lines []string) (title, artist, album, status string, hasTrack, hasStatus bool) {
	var pendingKey string

	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}

		strVal, hasStr := extractDBusStringValue(t)

		if !hasStr {
			// Non-string line: preserve pendingKey for structural/variant lines.
			switch {
			case t == "array [", t == "]", strings.HasPrefix(t, "dict entry"), t == ")":
				// structural — keep pendingKey
			case strings.HasPrefix(t, "variant"):
				// variant header without inline string — keep pendingKey for next line
			default:
				pendingKey = ""
			}
			continue
		}

		// This line contains a string value.
		if pendingKey != "" && pendingKey != "Track" {
			// Consume the string as the value for the pending key.
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
			// Treat the string as a key name.
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
