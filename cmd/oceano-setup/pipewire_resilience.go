package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

// aplayLongNameForPlughw returns the long bracket name from aplay (e.g. "MR 780")
// for plughw:CARD=M780,DEV=0 so we can match the PipeWire sink label.
func aplayLongNameForPlughw(aplayL, plughw string) string {
	if strings.TrimSpace(plughw) == "" {
		return ""
	}
	reCard := regexp.MustCompile(`plughw:CARD=([^,]+),`)
	m := reCard.FindStringSubmatch(plughw)
	if m == nil {
		return ""
	}
	short := m[1]
	lineRe := regexp.MustCompile(`card [0-9]+:\s*(\S+)\s+\[([^\]]+)\]`)
	for _, line := range strings.Split(aplayL, "\n") {
		lm := lineRe.FindStringSubmatch(line)
		if lm == nil {
			continue
		}
		if strings.EqualFold(lm[1], short) {
			return lm[2]
		}
	}
	if short != "" {
		return short
	}
	return ""
}

// dacDescriptionForWirePlumber picks a substring that appears in `wpctl status` under Sinks.
func dacDescriptionForWirePlumber(outputPlughw, matchHint string) string {
	out, _ := exec.Command("aplay", "-l").Output()
	if long := aplayLongNameForPlughw(string(out), outputPlughw); long != "" {
		return long
	}
	if s := strings.TrimSpace(matchHint); s != "" {
		return s
	}
	if m := regexp.MustCompile(`plughw:CARD=([^,]+),`).FindStringSubmatch(outputPlughw); len(m) > 1 {
		return m[1]
	}
	return "USB"
}

// installWirePlumberBTResilience sets the Oceano output DAC as the default PipeWire sink
// so Bluetooth audio goes to the amplifier when multiple USB sound devices exist.
func installWirePlumberBTResilience(guiUser, outputPlughw, matchHint string) {
	if strings.TrimSpace(guiUser) == "" || strings.TrimSpace(outputPlughw) == "" {
		return
	}
	if _, err := user.Lookup(guiUser); err != nil {
		logWarn("PipeWire routing: user not found: " + guiUser)
		return
	}
	dac := dacDescriptionForWirePlumber(outputPlughw, matchHint)
	if strings.TrimSpace(dac) == "" {
		return
	}

	// Bash script: extract sink id from wpctl Sinks: section only; match DAC label.
	script := fmt.Sprintf(`#!/usr/bin/env bash
# Oceano: PipeWire default sink = Oceano ALSA output (so Bluetooth and AirPlay use the same hardware).
# Match from aplay long name: %q
set -e
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
DAC_DESC=%q
for attempt in $(seq 1 12); do
  line=$(wpctl status 2>/dev/null | sed -n '/Sinks:/,/Sources:/p' | grep -F "$DAC_DESC" | head -1)
  if [ -n "$line" ]; then
    node_id=$(echo "$line" | sed -E 's/^[|[:space:]*]*([0-9]+).*/\1/' | tr -d ' \t')
    if [ -n "$node_id" ] && [ "$node_id" -gt 0 ] 2>/dev/null; then
      wpctl set-default "$node_id"
      wpctl set-volume "$node_id" 1.0
      echo "oceano-pipewire: default sink $node_id ($DAC_DESC)"
      exit 0
    fi
  fi
  echo "oceano-pipewire: attempt $attempt — no sink line for '$DAC_DESC', sleep 3s"
  sleep 3
done
echo "oceano-pipewire: re-run oceano-setup after the USB DAC/amp is on" >&2
exit 0
`, dac, dac)

	const scriptPath = "/usr/local/bin/oceano-pipewire-default-sink"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		logWarn("Could not write " + scriptPath + ": " + err.Error())
		return
	}
	logOK("Wrote " + scriptPath + " (Bluetooth default sink match: " + dac + ")")

	home, err := homeDirFor(guiUser)
	if err != nil {
		return
	}
	wpdir := filepath.Join(home, ".config", "wireplumber", "wireplumber.conf.d")
	if err := os.MkdirAll(wpdir, 0o755); err == nil {
		codec := `monitor.bluez.properties = {
  bluez5.codecs = [ sbc sbc_xq aac ldac aptx aptx_hd ]
}
`
		_ = os.WriteFile(filepath.Join(wpdir, "51-oceano-bluez-codec.conf"), []byte(codec), 0o644)
		_ = exec.Command("chown", "-R", guiUser+":"+guiUser, filepath.Join(home, ".config", "wireplumber")).Run()
		logOK("WirePlumber: BlueZ codecs (AAC, LDAC, …) in " + guiUser + " profile")
	}

	unit := `[Unit]
Description=Set Oceano USB DAC as default PipeWire sink
After=wireplumber.service pipewire.service
Wants=wireplumber.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/oceano-pipewire-default-sink
RemainAfterExit=yes

[Install]
WantedBy=default.target
`
	sdir := filepath.Join(home, ".config", "systemd", "user")
	_ = os.MkdirAll(sdir, 0o755)
	svcPath := filepath.Join(sdir, "oceano-pipewire-default-sink.service")
	if err := os.WriteFile(svcPath, []byte(unit), 0o644); err != nil {
		logWarn("user systemd: " + err.Error())
		return
	}
	_ = exec.Command("chown", "-R", guiUser+":"+guiUser, filepath.Join(home, ".config", "systemd")).Run()
	_ = exec.Command("loginctl", "enable-linger", guiUser).Run()
	logOK("Linger: enabled for " + guiUser + " (user PipeWire at boot)")

	machine := guiUser + "@.host"
	_ = exec.Command("systemctl", "--user", "-M", machine, "daemon-reload").Run()
	_ = exec.Command("systemctl", "--user", "-M", machine, "enable", "oceano-pipewire-default-sink.service").Run()
	_ = exec.Command("systemctl", "--user", "-M", machine, "start", "oceano-pipewire-default-sink.service").Run()
	logOK("PipeWire default-sink oneshot enabled/started (Bluetooth → " + dac + ")")
}

// warnIfMultipleUSBAudioPlayback warns when more than one playback card is listed (USB confusion).
func warnIfMultipleUSBAudioPlayback() {
	out, err := exec.Command("aplay", "-l").Output()
	if err != nil {
		return
	}
	c := 0
	cardRe := regexp.MustCompile(`^card [0-9]+:`)
	for _, line := range strings.Split(string(out), "\n") {
		if cardRe.MatchString(strings.TrimSpace(line)) {
			c++
		}
	}
	if c > 1 {
		logWarn("Multiple playback cards on the Pi — in the *output* step pick your amplifier/DAC, not a capture card; setup routes PipeWire (Bluetooth) and shairport (AirPlay) to the same output.")
	}
}
