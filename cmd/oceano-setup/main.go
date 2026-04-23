package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const (
	configPath     = "/etc/oceano/config.json"
	shairportConf  = "/etc/shairport-sync.conf"
	btConf         = "/etc/bluetooth/main.conf"
	btAgentService = "/etc/systemd/system/bt-agent.service"
)

const (
	bold   = "\033[1m"
	cyan   = "\033[0;36m"
	green  = "\033[0;32m"
	yellow = "\033[1;33m"
	red    = "\033[0;31m"
	reset  = "\033[0m"
)

var stdin = bufio.NewReader(os.Stdin)

func section(title string) { fmt.Printf("\n%s━━━ %s ━━━%s\n", bold, title, reset) }
func logOK(msg string)     { fmt.Printf("%s✓%s %s\n", green, reset, msg) }
func logWarn(msg string)   { fmt.Printf("%s!%s %s\n", yellow, reset, msg) }
func fatalf(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, red+"ERROR"+reset+" "+f+"\n", a...)
	os.Exit(1)
}

func prompt(label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := stdin.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// ── ALSA device detection ────────────────────────────────────────────────────

type alsaDevice struct {
	Label  string
	Device string // e.g. plughw:CARD=M780,DEV=0
}

func listALSADevices(tool string) []alsaDevice {
	out, err := exec.Command(tool, "-l").Output()
	if err != nil {
		return nil
	}
	re := regexp.MustCompile(`card (\d+): (\S+) \[([^\]]+)\]`)
	seen := map[string]bool{}
	var devices []alsaDevice
	for _, line := range strings.Split(string(out), "\n") {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		dev := "plughw:CARD=" + m[2] + ",DEV=0"
		if !seen[dev] {
			seen[dev] = true
			devices = append(devices, alsaDevice{Label: m[3], Device: dev})
		}
	}
	return devices
}

func pickDevice(label string, devices []alsaDevice) string {
	for i, d := range devices {
		fmt.Printf("  %d. %-46s (%s)\n", i+1, d.Device, d.Label)
	}
	fmt.Printf("  %d. Enter manually\n", len(devices)+1)

	choice := prompt(fmt.Sprintf("Select %s", label), "1")

	for i, d := range devices {
		if choice == fmt.Sprintf("%d", i+1) {
			return d.Device
		}
	}
	if choice == fmt.Sprintf("%d", len(devices)+1) || strings.HasPrefix(choice, "plughw:") || strings.HasPrefix(choice, "hw:") {
		if strings.HasPrefix(choice, "plughw:") || strings.HasPrefix(choice, "hw:") {
			return choice
		}
		return prompt(label+" (e.g. plughw:2,0)", "")
	}
	if len(devices) > 0 {
		return devices[0].Device
	}
	return ""
}

// ── Config.json (preserve all existing fields) ───────────────────────────────

func readConfig() map[string]interface{} {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return map[string]interface{}{}
	}
	var cfg map[string]interface{}
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

func setKey(cfg map[string]interface{}, section, key string, value interface{}) {
	sec, ok := cfg[section].(map[string]interface{})
	if !ok {
		sec = map[string]interface{}{}
	}
	sec[key] = value
	cfg[section] = sec
}

func getString(cfg map[string]interface{}, section, key, def string) string {
	if sec, ok := cfg[section].(map[string]interface{}); ok {
		if v, ok := sec[key].(string); ok && v != "" {
			return v
		}
	}
	return def
}

func writeConfig(cfg map[string]interface{}) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}

// ── shairport-sync.conf (PipeWire mode — default on Raspberry Pi OS Bookworm) ─

func writeShairportConf(airplayName string) error {
	content := fmt.Sprintf(`general =
{
  name = %q;
  output_backend = "pa";
  interpolation = "soxr";
};

pa =
{
  application_name = "Shairport Sync";
  sink = "";
};

metadata =
{
  enabled = "yes";
  include_cover_art = "yes";
  pipe_name = "/tmp/shairport-sync-metadata";
  pipe_timeout = 5000;
  cover_art_cache_directory = "/tmp/shairport-sync/.cache/coverart";
};

sessioncontrol =
{
  wait_for_completion = "yes";
};
`, airplayName)

	// Back up the original file once
	if _, err := os.Stat(shairportConf); err == nil {
		bak := shairportConf + ".oceano.bak"
		if _, err := os.Stat(bak); os.IsNotExist(err) {
			orig, _ := os.ReadFile(shairportConf)
			_ = os.WriteFile(bak, orig, 0644)
		}
	}

	tmp := shairportConf + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, shairportConf)
}

// ── Bluetooth ─────────────────────────────────────────────────────────────────

func configureBluetooth(deviceName string) {
	data, err := os.ReadFile(btConf)
	if err != nil {
		logWarn("Bluetooth config not found at " + btConf + " — skipping")
		return
	}

	content := string(data)
	re := regexp.MustCompile(`(?m)^\s*#?\s*DiscoverableTimeout\s*=.*`)
	if re.MatchString(content) {
		content = re.ReplaceAllString(content, "DiscoverableTimeout = 0")
	} else {
		content = regexp.MustCompile(`(?m)^\[General\]`).
			ReplaceAllString(content, "[General]\nDiscoverableTimeout = 0")
	}

	tmp := btConf + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		logWarn("Could not write " + btConf + ": " + err.Error())
		return
	}
	_ = os.Rename(tmp, btConf)

	_ = exec.Command("systemctl", "restart", "bluetooth.service").Run()

	// Set adapter alias (non-fatal — adapter may need a moment to start)
	err = exec.Command("dbus-send", "--system", "--print-reply",
		"--dest=org.bluez", "/org/bluez/hci0",
		"org.freedesktop.DBus.Properties.Set",
		"string:org.bluez.Adapter1", "string:Alias",
		"variant:string:"+deviceName).Run()
	if err != nil {
		logWarn("Could not set Bluetooth adapter alias (adapter may not be ready yet)")
	}

	if _, err := exec.LookPath("bt-agent"); err == nil {
		svc := `[Unit]
Description=Bluetooth Auto-pair Agent
After=bluetooth.service
Requires=bluetooth.service

[Service]
Type=simple
ExecStart=/usr/bin/bt-agent -c NoInputNoOutput
Restart=on-failure

[Install]
WantedBy=multi-user.target
`
		_ = os.WriteFile(btAgentService, []byte(svc), 0644)
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("systemctl", "enable", "--now", "bt-agent.service").Run()
		logOK("Bluetooth auto-pairing agent enabled (bt-agent)")
	} else {
		logWarn("bt-agent not found — manual pairing required (install bluez-tools to fix)")
	}
}

func run(name string, args ...string) {
	if err := exec.Command(name, args...).Run(); err != nil {
		logWarn(fmt.Sprintf("%s %v: %v", name, args, err))
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	if os.Getuid() != 0 {
		fatalf("Please run as root: sudo oceano-setup")
	}

	fmt.Printf("\n%s╔═══════════════════════════════╗%s\n", bold, reset)
	fmt.Printf("%s║     Oceano Setup Wizard       ║%s\n", bold, reset)
	fmt.Printf("%s╚═══════════════════════════════╝%s\n\n", bold, reset)
	fmt.Println("Configures AirPlay (shairport-sync) and Bluetooth.")
	fmt.Println("Press Enter to accept defaults shown in [brackets].")

	cfg := readConfig()

	// ── AirPlay ──────────────────────────────────────────────────────────────
	section("AirPlay")

	airplayName := prompt("AirPlay receiver name",
		getString(cfg, "audio_output", "airplay_name", "Oceano"))

	fmt.Println("Detecting ALSA output devices...")
	outputDevs := listALSADevices("aplay")
	outputDevice := pickDevice("output device (DAC)", outputDevs)

	// ── Capture device ───────────────────────────────────────────────────────
	section("Capture Device (REC OUT → track recognition)")

	fmt.Println("Detecting ALSA capture devices...")
	captureDevs := listALSADevices("arecord")
	captureDevice := pickDevice("capture device", captureDevs)

	// ── Bluetooth ────────────────────────────────────────────────────────────
	section("Bluetooth")

	btName := prompt("Bluetooth device name", airplayName)

	// ── Apply ────────────────────────────────────────────────────────────────
	section("Applying configuration")

	if err := writeShairportConf(airplayName); err != nil {
		logWarn("Could not write shairport-sync.conf: " + err.Error())
	} else {
		logOK("Written " + shairportConf)
	}

	setKey(cfg, "audio_output", "airplay_name", airplayName)
	if outputDevice != "" {
		setKey(cfg, "audio_output", "device", outputDevice)
	}
	if captureDevice != "" {
		setKey(cfg, "audio_input", "device", captureDevice)
	}
	if err := writeConfig(cfg); err != nil {
		logWarn("Could not update config.json: " + err.Error())
	} else {
		logOK("Updated " + configPath)
	}

	configureBluetooth(btName)
	logOK(fmt.Sprintf("Bluetooth configured: name=%q, always discoverable", btName))

	// ── Apply ────────────────────────────────────────────────────────────────
	section("Applying configuration")

	run("systemctl", "daemon-reload")
	run("systemctl", "restart", "shairport-sync.service")
	run("systemctl", "restart", "oceano-source-detector.service")
	run("systemctl", "restart", "oceano-state-manager.service")
	run("systemctl", "restart", "oceano-web.service")
	logOK("Services restarted")

	fmt.Printf("\n%sSetup complete!%s\n", bold, reset)
	out, _ := exec.Command("hostname", "-I").Output()
	if fields := strings.Fields(string(out)); len(fields) > 0 {
		fmt.Printf("Open %shttp://%s:8080%s to review your configuration.\n",
			cyan, fields[0], reset)
	}
}
