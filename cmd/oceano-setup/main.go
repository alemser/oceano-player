package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	configPath     = "/etc/oceano/config.json"
	shairportConf  = "/etc/shairport-sync.conf"
	btConf         = "/etc/bluetooth/main.conf"
	btAgentService = "/etc/systemd/system/bt-agent.service"
)

const (
	displayService         = "/etc/systemd/system/oceano-display.service"
	displayCheckBin        = "/usr/local/bin/oceano-display-check"
	displayLaunchBin       = "/usr/local/bin/oceano-display-launch"
	xsessionsDir           = "/usr/share/xsessions"
	oceanoKioskDesktop     = "/usr/share/xsessions/oceano-kiosk.desktop"
	lightdmKioskConf       = "/etc/lightdm/lightdm.conf.d/oceano-kiosk.conf"
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

	// Use --no-block: synchronous "systemctl restart bluetooth" can hang
	// for minutes on some Pi/BlueZ setups while D-Bus waits for the job.
	_ = exec.Command("systemctl", "restart", "--no-block", "bluetooth.service").Run()
	time.Sleep(2 * time.Second)

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
		_ = exec.Command("systemctl", "enable", "bt-agent.service").Run()
		_ = exec.Command("systemctl", "start", "--no-block", "bt-agent.service").Run()
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

func withDebianNoninteractive() []string {
	return append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
}

// installDisplayAptStack mirrors install-oceano-display.sh: Xvfb and minimal X
// packages so Chromium can start under systemd with DISPLAY=:99.
func installDisplayAptStack() {
	cmd := exec.Command("apt-get", "update", "-qq")
	cmd.Env = withDebianNoninteractive()
	_ = cmd.Run()

	install := exec.Command("apt-get", append(
		[]string{"install", "-y", "--no-install-recommends"},
		"xserver-xorg-core", "xserver-xorg", "xinit", "xvfb", "x11-utils", "xauth",
	)...)
	install.Env = withDebianNoninteractive()
	if err := install.Run(); err != nil {
		logWarn(fmt.Sprintf("X / Xvfb metapackage install: %v — trying xvfb only", err))
		fallback := exec.Command("apt-get", "install", "-y", "xvfb")
		fallback.Env = withDebianNoninteractive()
		if err2 := fallback.Run(); err2 != nil {
			logWarn("Could not install xvfb: " + err2.Error())
		}
	}
}

func findXvfb() string {
	if p, err := exec.LookPath("Xvfb"); err == nil {
		return p
	}
	return ""
}

func homeDirFor(username string) (string, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}

func chownToUser(path, username string) {
	_ = exec.Command("chown", username+":"+username, path).Run()
}

func haveLightdm() bool {
	_, err := os.Stat("/usr/sbin/lightdm")
	return err == nil
}

// tryInstallLightDM installs the display manager used for autologin to oceano-kiosk.
func tryInstallLightDM() {
	cmd := exec.Command("apt-get", "install", "-y", "lightdm")
	cmd.Env = withDebianNoninteractive()
	if err := cmd.Run(); err != nil {
		logWarn("Could not install lightdm: " + err.Error())
		return
	}
	logOK("lightdm package installed")
}

// nowPlayingAppURL turns "http://host:8080" or "http://host:8080/" into
// "http://host:8080/nowplaying.html" for the kiosk --app= URL.
func nowPlayingAppURL(webAddr string) string {
	s := strings.TrimSpace(webAddr)
	s = strings.TrimRight(s, "/")
	if s == "" {
		return "http://127.0.0.1:8080/nowplaying.html"
	}
	return s + "/nowplaying.html"
}

// ── Display (HDMI/DSI kiosk) ───────────────────────────────────────────────────

func findChromium() string {
	if st, err := os.Stat("/usr/lib/chromium/chromium"); err == nil && !st.IsDir() {
		return "/usr/lib/chromium/chromium"
	}
	for _, bin := range []string{"chromium", "chromium-browser"} {
		if path, err := exec.LookPath(bin); err == nil {
			return path
		}
	}
	return ""
}

func configureDisplay(kioskUser, webAddr string, lightAutologin bool) {
	_, err := user.Lookup(kioskUser)
	if err != nil {
		logWarn("Display user not found: " + kioskUser)
		return
	}

	chromium := findChromium()
	if chromium == "" {
		logWarn("Chromium not found — installing chromium (Bookworm) or chromium-browser (transitional)...")
		// With recommends so font/GTK/GBM stacks are present (matches install-oceano-display.sh).
		a := exec.Command("apt-get", "install", "-y", "chromium")
		a.Env = withDebianNoninteractive()
		if err2 := a.Run(); err2 != nil {
			b := exec.Command("apt-get", "install", "-y", "chromium-browser")
			b.Env = withDebianNoninteractive()
			_ = b.Run()
		}
		chromium = findChromium()
	}
	if chromium == "" {
		logWarn("Chromium not found — skipping display setup")
		return
	}
	logOK("Chromium: " + chromium)

	logOK("Installing Xvfb and minimal X stack (official kiosk stack)...")
	installDisplayAptStack()
	if xvfb := findXvfb(); xvfb != "" {
		logOK("Xvfb: " + xvfb)
	} else {
		logWarn("Xvfb not found in PATH after apt — kiosk is unlikely to work until xvfb is installed")
	}

	home, err := homeDirFor(kioskUser)
	if err != nil {
		logWarn("Could not resolve home directory: " + err.Error())
		home = ""
	}

	// Aligned with install-oceano-display.sh: require HDMI, DSI, or DP connector
	displayCheck := `#!/bin/bash
set -e
FOUND=false
shopt -s nullglob
for status_file in /sys/class/drm/card*/status; do
  [[ -f "$status_file" ]] || continue
  connector=$(basename "$(dirname "$status_file")")
  if [[ "$connector" == *HDMI* || "$connector" == *DSI* || "$connector" == *DP* ]]; then
    if [[ "$(cat "$status_file")" == "connected" ]]; then
      FOUND=true
      break
    fi
  fi
done
shopt -u nullglob
[[ "$FOUND" == "true" ]] && exit 0 || exit 1
`
	if err := os.WriteFile(displayCheckBin, []byte(displayCheck), 0755); err != nil {
		logWarn("Could not write " + displayCheckBin + ": " + err.Error())
		return
	}

	now := nowPlayingAppURL(webAddr)
	nowBash := strconv.Quote(now)
	// Kiosk: Xvfb on :99 + Chromium app mode. Same as install-oceano-display.sh.
	displayLaunch := fmt.Sprintf(`#!/bin/bash
set -e
NOWPLAYING_URL=%s
CHROME_DATA=${HOME}/.config/chromium
[[ -d "${CHROME_DATA}" ]] && rm -f "${CHROME_DATA}/SingletonLock"
cleanup() { [[ -n "${XVFB_PID:-}" ]] && kill "${XVFB_PID}" 2>/dev/null; }
trap cleanup EXIT
Xvfb :99 -screen 0 1024x600x24 -nolisten tcp &
XVFB_PID=$!
export DISPLAY=:99
sleep 2
exec %s \
  --kiosk \
  --no-sandbox \
  --disable-dev-shm-usage \
  --noerrdialogs \
  --disable-infobars \
  --no-first-run \
  --disable-session-crashed-bubble \
  --disable-features=TranslateUI \
  --check-for-update-interval=315360000 \
  --disable-background-networking \
  --disable-sync \
  --password-store=basic \
  --use-mock-keychain \
  --window-size=1024,600 \
  --hide-cursor \
  --app="${NOWPLAYING_URL}"
`, nowBash, chromium)
	if err := os.WriteFile(displayLaunchBin, []byte(displayLaunch), 0755); err != nil {
		logWarn("Could not write " + displayLaunchBin + ": " + err.Error())
		return
	}

	// .xinitrc, xsessions, and LightDM: same as install-oceano-display.sh (kiosk = official path in oceano-setup).
	if home != "" {
		xinit := "#!/bin/sh\nexec " + displayLaunchBin + "\n"
		p := filepath.Join(home, ".xinitrc")
		if err := os.WriteFile(p, []byte(xinit), 0755); err != nil {
			logWarn("Could not write .xinitrc: " + err.Error())
		} else {
			chownToUser(p, kioskUser)
			logOK("Wrote " + p)
		}

		if err := os.MkdirAll(xsessionsDir, 0755); err != nil {
			logWarn("Could not create " + xsessionsDir + ": " + err.Error())
		} else {
			desktop := []byte(`[Desktop Entry]
Name=Oceano Kiosk
Comment=Oceano Now Playing Display
Exec=` + displayLaunchBin + `
Type=Application
`)
			if err := os.WriteFile(oceanoKioskDesktop, desktop, 0644); err != nil {
				logWarn("Could not write xsessions desktop: " + err.Error())
			} else {
				logOK("Wrote " + oceanoKioskDesktop)
			}
		}
	}

	if lightAutologin {
		if !haveLightdm() {
			logWarn("LightDM not installed — installing (needed for autologin to the kiosk session)")
			tryInstallLightDM()
		}
		if haveLightdm() {
			_ = os.MkdirAll(filepath.Dir(lightdmKioskConf), 0755)
			ldm := fmt.Sprintf(`[Seat:*]
autologin-user=%s
autologin-user-timeout=0
autologin-session=oceano-kiosk
user-session=oceano-kiosk
`, kioskUser)
			if err := os.WriteFile(lightdmKioskConf, []byte(ldm), 0644); err != nil {
				logWarn("Could not write LightDM conf: " + err.Error())
			} else {
				logOK("Wrote " + lightdmKioskConf)
			}
			if home != "" {
				p := filepath.Join(home, ".dmrc")
				dmrc := "[Desktop]\nSession=oceano-kiosk\n"
				if err := os.WriteFile(p, []byte(dmrc), 0644); err != nil {
					logWarn("Could not write .dmrc: " + err.Error())
				} else {
					chownToUser(p, kioskUser)
					logOK("Wrote " + p)
				}
			}
			run("systemctl", "enable", "--no-block", "lightdm")
			logOK("LightDM autologin to oceano-kiosk; reboot to apply: sudo reboot")
		} else {
			logWarn("LightDM unavailable — set up autologin manually, or: sudo apt install lightdm && re-run oceano-setup (display = y).")
		}
	} else {
		logWarn("LightDM autologin not enabled. The oceano-display systemd service uses a virtual X server; " +
			"re-run oceano-setup and answer Y to LightDM if you use a local HDMI/DSI screen.")
	}

	displaySvc := fmt.Sprintf(`[Unit]
Description=Oceano Display — now playing kiosk (HDMI/DSI, Xvfb)
After=network.target oceano-web.service
Wants=oceano-web.service
ConditionPathExists=/sys/class/drm

[Service]
Type=simple
User=%s
ExecCondition=%s
ExecStartPre=/bin/sleep 2
ExecStart=%s
Restart=on-failure
RestartSec=5
TimeoutStartSec=30

[Install]
WantedBy=multi-user.target
`, kioskUser, displayCheckBin, displayLaunchBin)
	if err := os.WriteFile(displayService, []byte(displaySvc), 0644); err != nil {
		logWarn("Could not write " + displayService + ": " + err.Error())
		return
	}

	run("systemctl", "daemon-reload")
	run("systemctl", "enable", "--no-block", "oceano-display.service")
	run("systemctl", "start", "--no-block", "oceano-display.service")
	logOK("Display service enabled and started (oceano-display.service) — see journalctl -u oceano-display -b")
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

	// ── Display ────────────────────────────────────────────────────────────────
	section("Display (HDMI/DSI kiosk)")

	displayEnabled := prompt("Install display service", "n")
	displayUser := "pi"
	webAddr := "http://localhost:8080"
	lightAutologin := "n"
	if strings.ToLower(displayEnabled) == "y" {
		displayUser = prompt("Display user", displayUser)
		webAddr = prompt("Web address", webAddr)
		// Same wiring as install-oceano-display.sh (LightDM + oceano-kiosk session) for a physical panel.
		lightAutologin = prompt("Enable LightDM autologin to oceano-kiosk (for HDMI/DSI) [Y/n]:", "y")
	}

	// ── Apply ────────────────────────────────────────────────────────────────
	section("Applying configuration")

	run("systemctl", "daemon-reload")
	run("systemctl", "restart", "shairport-sync.service")
	run("systemctl", "restart", "oceano-source-detector.service")
	run("systemctl", "restart", "oceano-state-manager.service")
	run("systemctl", "restart", "oceano-web.service")

	if strings.ToLower(displayEnabled) == "y" {
		configureDisplay(displayUser, webAddr, strings.ToLower(strings.TrimSpace(lightAutologin)) == "y")
		run("systemctl", "restart", "oceano-display.service")
	}

	logOK("Services restarted")

	fmt.Printf("\n%sSetup complete!%s\n", bold, reset)
	out, _ := exec.Command("hostname", "-I").Output()
	if fields := strings.Fields(string(out)); len(fields) > 0 {
		fmt.Printf("Open %shttp://%s:8080%s to review your configuration.\n",
			cyan, fields[0], reset)
	}
}
