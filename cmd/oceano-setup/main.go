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

	"github.com/alemser/oceano-player/internal/shairport"
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
	// Override Pi OS defaults: files after "o" (e.g. rpd-labwc) must not win; zz- is loaded last.
	lightdmKioskOverride = "/etc/lightdm/lightdm.conf.d/zz-oceano-override.conf"
	accountsServiceUsers  = "/var/lib/AccountsService/users"
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

// printDisplayResolutionHints is shown at the end of setup so the user can fix wrong aspect
// ratio or mode on the spot. The web UI does not control HDMI timing; the firmware/config does.
func printDisplayResolutionHints() {
	section("If the panel looks wrong (resolution / aspect)")
	fmt.Println("The now-playing view fills the browser; it does not set HDMI timing. On the Pi, set the video mode, then reboot.")
	fmt.Println()
	fmt.Println("  1)  Easiest:  sudo raspi-config  →  Display Options  →  Resolution / “best” mode / (if offered) Waveshare or screen driver.")
	fmt.Println("     Finish and allow reboot when the tool asks.")
	fmt.Println()
	fmt.Println("  2)  Direct edit (back up first:  sudo cp /boot/firmware/config.txt{,.bak}  — use /boot/config.txt on some older images).")
	fmt.Println("      Relevant keys (see https://www.raspberrypi.com/documentation/computers/config-txt.html#video-options ):")
	fmt.Println("     •  disable_overscan=1  —  removes extra black border on many HDMI TVs.")
	fmt.Println("     •  hdmi_safe=1  —  conservative mode if you get a black or unstable picture.")
	fmt.Println("     •  hdmi_force_hotplug=1  —  if the Pi thinks no display is connected.")
	fmt.Println("     •  config_hdmi_boost=7  (try 4–11)  —  weak or long cable / marginal HDMI.")
	fmt.Println("     •  CEA (TV) vs DMT (monitor) lists differ:  hdmi_group=1  (CEA)  /  hdmi_group=2  (DMT)  and a valid  hdmi_mode. Wrong group = wrong size.")
	fmt.Println("     •  Custom panel size:  hdmi_group=2  ;  hdmi_mode=87  ;  and one line  hdmi_cvt=WIDTH HEIGHT FRAMERATE  (example:  hdmi_cvt=1024 600 60 ).")
	fmt.Println("       Then  sudo sync  &&  sudo reboot   (never unplug during writes).")
	fmt.Println()
	fmt.Println("  3)  Keep using config.txt or raspi-config. Oceano’s kiosk does not use xrandr; ad‑hoc xrandr/\"randr --auto\" from login can black out some panels.")
	fmt.Println("  4)  Deeper help:  README  →  Troubleshooting  →  “HDMI: wrong or stretched resolution (kiosk or desktop)”.")
	fmt.Println()
}
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
	CardID int    // ALSA card index; set for arecord, -1 if unknown
	IsUSB  bool   // /sys/.../sound/cardN → USB; typical for external interfaces
}

func listALSADevices(tool string) []alsaDevice {
	return listALSAByTool(tool)
}

// listALSAByTool returns playback or capture device entries. Each card is probed
// in sysfs to tag USB (typical for the REC loop line/ADC) vs on-board/HDMI.
func listALSAByTool(tool string) []alsaDevice {
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
		cardID, _ := strconv.Atoi(m[1])
		dev := "plughw:CARD=" + m[2] + ",DEV=0"
		if !seen[dev] {
			seen[dev] = true
			d := alsaDevice{Label: m[3], Device: dev, CardID: cardID, IsUSB: soundCardIsUSB(cardID)}
			devices = append(devices, d)
		}
	}
	return devices
}

// soundCardIsUSB is true when the card path under /sys/.../sound/cardN/ links into
// the USB subsystem; USB mics, line/ADC, and the usual REC capture dongles; not on
// the Pi's on-board, VC4, or HDM capture nodes.
func soundCardIsUSB(card int) bool {
	if card < 0 {
		return false
	}
	p := filepath.Join("/sys/class/sound", fmt.Sprintf("card%d", card), "device")
	abs, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	abs = strings.ToLower(abs)
	return strings.Contains(abs, "/usb")
}

// listCaptureDevicesForREC lists capture endpoints and applies optional filtering: by default
// only cards that are USB and/or not obviously built-in/HDMI are shown when at least one exists.
// OCEANO_SETUP_LIST_ALL_CAPTURE=1 lists every card arecord reports.
func listCaptureDevicesForREC(previous string) (devices []alsaDevice, note string) {
	all := listALSAByTool("arecord")
	if len(all) == 0 {
		return nil, ""
	}
	if os.Getenv("OCEANO_SETUP_LIST_ALL_CAPTURE") == "1" {
		return orderCaptureDevsForDisplay(all), "Listing all arecord devices (OCEANO_SETUP_LIST_ALL_CAPTURE=1)."
	}
	var ext, onboard []alsaDevice
	for i := range all {
		if all[i].isLikelyExternalRECCard() {
			ext = append(ext, all[i])
		} else {
			onboard = append(onboard, all[i])
		}
	}
	prev := strings.TrimSpace(previous)
	prevInExt := prev != "" && deviceInListByPlughw(ext, prev)
	if len(ext) == 0 {
		// e.g. only on-board: nothing to filter
		return all, "No external/USB capture card found — use the list below or a USB line-in/ADC; built-in/HDMI are rarely correct for REC OUT."
	}
	if len(onboard) == 0 {
		return orderCaptureDevsForDisplay(all), ""
	}
	if prev == "" || prevInExt {
		n := len(onboard)
		who := "built-in/HDMI/VC4"
		if n == 1 {
			who = "entry"
		}
		return orderCaptureDevsForDisplay(ext),
			fmt.Sprintf("Hiding %d %s capture (USB/external interfaces only; run with OCEANO_SETUP_LIST_ALL_CAPTURE=1 to list all).", n, who)
	}
	// User config points to a non-recommended (on-board) device — show external first, then the rest
	return orderCaptureDevsForDisplay(append(append([]alsaDevice{}, ext...), onboard...)),
		"Your saved config uses a non-USB or built-in capture. External/USB choices are listed first; switch to the USB line from the amplifier if possible."
}

// orderCaptureDevsForDisplay: USB first, then others (on-board, HDMI) for readability.
func orderCaptureDevsForDisplay(devs []alsaDevice) []alsaDevice {
	// arecord -l is usually card-index order; keep USB before non-USB for consistency
	if len(devs) < 2 {
		return devs
	}
	out := make([]alsaDevice, 0, len(devs))
	var rest []alsaDevice
	for _, d := range devs {
		if d.IsUSB {
			out = append(out, d)
		} else {
			rest = append(rest, d)
		}
	}
	out = append(out, rest...)
	return out
}

// isLikelyExternalRECCard: USB, or at least not clearly built-in/HDMI/VC4. Anything else
// (PCIe, Firewire, I2S hat) is kept in the "external" bucket so we do not hide it.
func (d *alsaDevice) isLikelyExternalRECCard() bool {
	if d == nil {
		return false
	}
	if d.IsUSB {
		return true
	}
	if captureLabelLooksOnboard(*d) {
		return false
	}
	return true
}

func deviceInListByPlughw(devs []alsaDevice, plughw string) bool {
	if strings.TrimSpace(plughw) == "" {
		return false
	}
	for i := range devs {
		if devs[i].Device == plughw {
			return true
		}
	}
	// plughw:2,0 vs plughw:CARD=foo,DEV=0
	pc := plughwCardName(plughw)
	if pc == "" {
		return false
	}
	for i := range devs {
		if m := regexp.MustCompile(`plughw:CARD=([^,]+),`).FindStringSubmatch(devs[i].Device); len(m) > 1 && m[1] == pc {
			return true
		}
	}
	return false
}

// plughwCardName returns the short CARD= token from a plughw/hw string, or "".
func plughwCardName(alsa string) string {
	alsa = strings.TrimSpace(alsa)
	if m := regexp.MustCompile(`(?i)(?:plughw|hw):CARD=([^,]+),`).FindStringSubmatch(alsa); len(m) > 1 {
		return m[1]
	}
	// e.g. plughw:2,0  — no CARD= token; resolve via card id below.
	if m := regexp.MustCompile(`(?i)(?:plughw|hw):([0-9]+),[0-9]+`).FindStringSubmatch(alsa); len(m) == 0 {
		return ""
	} else {
		// map numeric card id to the same plughw we build in listALSADevices
		return cardShortNameByID(strings.TrimSpace(m[1]))
	}
}

// cardShortNameByID returns the "card" short id from aplay/arecord -l for a numeric card N.
func cardShortNameByID(idStr string) string {
	for _, which := range []string{"aplay", "arecord"} {
		out, err := exec.Command(which, "-l").Output()
		if err != nil {
			continue
		}
		prefix := "card " + idStr + ":"
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			// line: card 2: Device [Name]
			re := regexp.MustCompile(`^card [0-9]+:\s*(\S+)\s+\[`)
			if m := re.FindStringSubmatch(line); len(m) > 1 {
				return m[1]
			}
		}
	}
	return ""
}

// defaultDeviceChoice returns a 1-based list index to use as the prompt default, or 1.
func defaultDeviceChoice(devices []alsaDevice, previous string) int {
	if len(devices) == 0 {
		return 1
	}
	prev := strings.TrimSpace(previous)
	if prev == "" {
		return 1
	}
	// exact plughw string from last run
	for i, d := range devices {
		if d.Device == prev {
			return i + 1
		}
	}
	pc := plughwCardName(prev)
	if pc == "" {
		return 1
	}
	for i, d := range devices {
		if m := regexp.MustCompile(`plughw:CARD=([^,]+),`).FindStringSubmatch(d.Device); len(m) > 1 && m[1] == pc {
			return i + 1
		}
	}
	return 1
}

// captureLabelLooksOnboard heuristics: Pi lists HDMI/VC4/bcm before USB — warn if the user
// may have selected built-in instead of the REC OUT capture interface.
func captureLabelLooksOnboard(d alsaDevice) bool {
	joined := strings.ToLower(d.Label + " " + d.Device)
	// "Headphones" (bcm2835) and vc4-hdmi / HDMI are common false picks for "microphone" problems.
	needle := []string{
		"vc4", "hdmi", "bcm2835", "bcm",
		"headphones", "headset", "broadcom", "3f00b840",
	}
	for _, s := range needle {
		if strings.Contains(joined, s) {
			return true
		}
	}
	return false
}

func pickDevice(label string, devices []alsaDevice, previous string) string {
	if len(devices) == 0 {
		manual := prompt(label+" (e.g. plughw:2,0) — arecord -l did not list any device", strings.TrimSpace(previous))
		if manual != "" {
			return manual
		}
		return strings.TrimSpace(previous)
	}
	defN := defaultDeviceChoice(devices, previous)
	for i, d := range devices {
		usbTag := ""
		if d.IsUSB {
			usbTag = "  " + green + "[USB]" + reset
		}
		fmt.Printf("  %d. %-46s (%s)%s\n", i+1, d.Device, d.Label, usbTag)
	}
	fmt.Printf("  %d. Enter manually\n", len(devices)+1)

	defStr := fmt.Sprintf("%d", defN)
	if p := strings.TrimSpace(previous); p != "" {
		if defN >= 1 && defN <= len(devices) {
			if devices[defN-1].Device == p {
				fmt.Printf("%s(Previous config: %s — press Enter to keep it)%s\n", cyan, p, reset)
			} else {
				fmt.Printf("%s(Previous config %s is not in the list — check USB / replug; default choice may not match the old path)%s\n", yellow, p, reset)
			}
		}
	}
	choice := prompt(fmt.Sprintf("Select %s", label), defStr)

	for i, d := range devices {
		if choice == fmt.Sprintf("%d", i+1) {
			if strings.Contains(label, "capture") && captureLabelLooksOnboard(d) {
				logWarn("This capture device looks like built-in/HDMI, not a USB line/mic from REC OUT. Wrong choice here breaks recognition; pick the USB interface used for the amplifier output loop.")
			}
			return d.Device
		}
	}
	if choice == fmt.Sprintf("%d", len(devices)+1) || strings.HasPrefix(choice, "plughw:") || strings.HasPrefix(choice, "hw:") {
		if strings.HasPrefix(choice, "plughw:") || strings.HasPrefix(choice, "hw:") {
			return choice
		}
		return prompt(label+" (e.g. plughw:2,0)", "")
	}
	// unrecognised — prefer keeping previous if it still valid
	if p := strings.TrimSpace(previous); p != "" {
		for _, d := range devices {
			if d.Device == p {
				logWarn("Unrecognised choice — keeping previous " + p)
				return p
			}
		}
	}
	if defN >= 1 && defN <= len(devices) {
		logWarn("Unrecognised choice — using default index " + defStr)
		return devices[defN-1].Device
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

// ── shairport-sync: see internal/shairport (ALSA direct; system user cannot use user PipeWire)

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
	_ = exec.Command("bluetoothctl", "power", "on").Run()
	time.Sleep(500 * time.Millisecond)
	_ = exec.Command("bluetoothctl", "pairable", "on").Run()
	_ = exec.Command("bluetoothctl", "discoverable", "on").Run()
	logOK("Bluetooth: adapter on, discoverable, pairable (visible in system Bluetooth lists)")

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
// ensureAvahiForAirPlay installs and starts mDNS. Without it, shairport-sync can run
// but iPhones will not list the AirPlay target (Debian omits Recommends when
// install.sh or minimal apt paths use --no-install-recommends).
func ensureAvahiForAirPlay() {
	_, err := os.Stat("/usr/sbin/avahi-daemon")
	if err != nil {
		logWarn("avahi-daemon not installed — installing (required for AirPlay / Bonjour on iPhone)…")
		cmd := exec.Command("apt-get", "install", "-y", "avahi-daemon")
		cmd.Env = withDebianNoninteractive()
		if e := cmd.Run(); e != nil {
			logWarn("Could not install avahi-daemon: " + e.Error())
			return
		}
	}
	run("systemctl", "enable", "--no-block", "avahi-daemon.service")
	run("systemctl", "start", "--no-block", "avahi-daemon.service")
	logOK("avahi-daemon (mDNS) — AirPlay discovery for iOS/macOS")
}

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

// patchMainLightdmKioskOverride updates /etc/lightdm/lightdm.conf so that active user-session
// and autologin-session lines are oceano-kiosk. On Raspberry Pi OS, the main file sets
// user-session=rpd-labwc and is merged after lightdm.conf.d, so a drop-in alone is ignored
// and autologin still starts Wayland (rpd-labwc) — see: grep user-session /etc/lightdm/lightdm.conf
func patchMainLightdmKioskOverride() {
	const path = "/etc/lightdm/lightdm.conf"
	b, err := os.ReadFile(path)
	if err != nil {
		logWarn("LightDM main conf: " + err.Error())
		return
	}
	bak := path + ".oceano.bak"
	if _, e := os.Stat(bak); os.IsNotExist(e) {
		if werr := os.WriteFile(bak, b, 0600); werr != nil {
			logWarn("LightDM main conf backup: " + werr.Error())
		} else {
			logOK("Backed up " + path + " → " + bak)
		}
	}
	orig := string(b)
	lines := strings.Split(orig, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "user-session=") && !strings.HasPrefix(t, "#") {
			lines[i] = "user-session=oceano-kiosk"
		}
		if strings.HasPrefix(t, "autologin-session=") && !strings.HasPrefix(t, "#") {
			lines[i] = "autologin-session=oceano-kiosk"
		}
	}
	out := strings.Join(lines, "\n")
	if out == orig {
		return
	}
	if werr := os.WriteFile(path, []byte(out), 0644); werr != nil {
		logWarn("Could not patch " + path + ": " + werr.Error())
		return
	}
	logOK("Patched " + path + " (user-session + autologin-session = oceano-kiosk; overrides Pi rpd-labwc in main file)")
}

// writeAccountsKioskSession sets Session and XSession in AccountsService so Raspberry Pi OS
// does not force labwc-pi over the oceano-kiosk LightDM entry.
func writeAccountsKioskSession(kioskUser, sessionName string) {
	_ = os.MkdirAll(accountsServiceUsers, 0755)
	p := filepath.Join(accountsServiceUsers, kioskUser)
	raw, err := os.ReadFile(p)
	sep := "\n"
	if err == nil {
		if strings.Contains(string(raw), "\r\n") {
			sep = "\r\n"
		}
	} else if !os.IsNotExist(err) {
		logWarn("AccountsService: " + err.Error())
		return
	}
	var out []string
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), sep) {
			if strings.TrimSpace(line) == "" {
				continue
			}
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "Session=") || strings.HasPrefix(t, "XSession=") {
				continue
			}
			out = append(out, line)
		}
	}
	merged := false
	var finalOut []string
	for _, line := range out {
		if strings.TrimSpace(line) == "" {
			continue
		}
		finalOut = append(finalOut, line)
		if strings.TrimSpace(line) == "[User]" {
			if !merged {
				finalOut = append(finalOut, "Session="+sessionName, "XSession="+sessionName)
				merged = true
			}
		}
	}
	if !merged {
		if len(finalOut) == 0 {
			finalOut = []string{"[User]", "Session=" + sessionName, "XSession=" + sessionName, "SystemAccount=false"}
		} else {
			finalOut = append([]string{"[User]", "Session=" + sessionName, "XSession=" + sessionName, "SystemAccount=false"}, finalOut...)
		}
	} else {
		hasSys := false
		for _, l := range finalOut {
			if strings.HasPrefix(strings.TrimSpace(l), "SystemAccount=") {
				hasSys = true
			}
		}
		if !hasSys {
			finalOut = append(finalOut, "SystemAccount=false")
		}
	}
	if werr := os.WriteFile(p, []byte(strings.Join(finalOut, "\n")+"\n"), 0600); werr != nil {
		logWarn("Could not write AccountsService " + p + ": " + werr.Error())
		return
	}
	logOK("Wrote " + p + " (Session/XSession = " + sessionName + " for RPi autologin)")
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
	chromeQ := strconv.Quote(chromium)
	// OCEANO_FORCE_XVFB=1 is set only by the systemd oceano-display unit. When LightDM
	// or startx provides a real X (DISPLAY, typically :0) for this same script, skip Xvfb
	// and draw on the physical connector.
	displayLaunch := fmt.Sprintf(`#!/bin/bash
set -e
CHROME_BIN=%s
NOWPLAYING_URL=%s
CHROME_DATA=${HOME}/.config/chromium
[[ -d "${CHROME_DATA}" ]] && rm -f "${CHROME_DATA}/SingletonLock"
# Do not run xrandr here: "xrandr --auto" can pick an invalid HDMI mode on some Pi+panel
# setups and black the display. Rely on Xorg and --kiosk; tune HDMI in /boot/firmware/config.txt if needed.
run_chromium() {
  exec "${CHROME_BIN}" \
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
  --hide-cursor \
  --app="${NOWPLAYING_URL}"
}
apply_display_power_settings() {
  # Keep kiosk displays awake: disable X11 screensaver + DPMS blanking.
  xset s off >/dev/null 2>&1 || true
  xset -dpms >/dev/null 2>&1 || true
  xset s noblank >/dev/null 2>&1 || true
}
if [ -z "${OCEANO_FORCE_XVFB:-}" ]; then
  if [ -z "${DISPLAY:-}" ] && [ -S /tmp/.X11-unix/X0 ] && [ -f "${HOME}/.Xauthority" ]; then
    export DISPLAY=:0
  fi
  if [ -n "${DISPLAY:-}" ]; then
    d="${DISPLAY#:}"
    d="${d%%%%.*}"
    if [ -S "/tmp/.X11-unix/X${d}" ]; then
      apply_display_power_settings
      run_chromium
    fi
  fi
fi
cleanup() { [[ -n "${XVFB_PID:-}" ]] && kill "${XVFB_PID}" 2>/dev/null; }
trap cleanup EXIT
Xvfb :99 -screen 0 1024x768x24 -nolisten tcp &
XVFB_PID=$!
export DISPLAY=:99
sleep 2
apply_display_power_settings
run_chromium
`, chromeQ, nowBash)
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

		xprofile := "#!/bin/sh\n" +
			"# Prevent local HDMI/DSI kiosk panel from blanking after idle.\n" +
			"xset s off\n" +
			"xset -dpms\n" +
			"xset s noblank\n"
		xprofilePath := filepath.Join(home, ".xprofile")
		if err := os.WriteFile(xprofilePath, []byte(xprofile), 0755); err != nil {
			logWarn("Could not write .xprofile: " + err.Error())
		} else {
			chownToUser(xprofilePath, kioskUser)
			logOK("Wrote " + xprofilePath)
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
			// zz- loads after Raspberry Pi (r* / labwc) defaults; overrides user-session / autologin.
			_ = os.MkdirAll(filepath.Dir(lightdmKioskOverride), 0755)
			_ = os.Remove("/etc/lightdm/lightdm.conf.d/oceano-kiosk.conf")
			ldm := fmt.Sprintf(`[Seat:*]
autologin-user=%s
autologin-user-timeout=0
autologin-session=oceano-kiosk
user-session=oceano-kiosk
`, kioskUser)
			if err := os.WriteFile(lightdmKioskOverride, []byte(ldm), 0644); err != nil {
				logWarn("Could not write LightDM conf: " + err.Error())
			} else {
				logOK("Wrote " + lightdmKioskOverride)
			}
			patchMainLightdmKioskOverride()
			writeAccountsKioskSession(kioskUser, "oceano-kiosk")
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

	// oceano-display (systemd) is only for headless/kiosk without LightDM. When LightDM autologs
	// to oceano-kiosk, the same launch script runs on the real :0; keep Xvfb forced only here.
	displaySvc := fmt.Sprintf(`[Unit]
Description=Oceano Display — now playing kiosk (Xvfb fallback)
After=network.target oceano-web.service
Wants=oceano-web.service
ConditionPathExists=/sys/class/drm

[Service]
Type=simple
User=%s
Environment=OCEANO_FORCE_XVFB=1
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
	if lightAutologin && haveLightdm() {
		run("systemctl", "disable", "--no-block", "oceano-display.service")
		run("systemctl", "stop", "--no-block", "oceano-display.service")
		logOK("oceano-display (systemd) disabled: kiosk is started on the local display by the LightDM " +
			"oceano-kiosk session. Reboot to apply, then: journalctl -b -u lightdm")
	} else {
		run("systemctl", "enable", "--no-block", "oceano-display.service")
		run("systemctl", "start", "--no-block", "oceano-display.service")
		logOK("Display service enabled and started (oceano-display.service) — see journalctl -u oceano-display -b")
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
	warnIfMultipleUSBAudioPlayback()
	outputDevs := listALSADevices("aplay")
	outputDevice := pickDevice("output device (DAC)", outputDevs, getString(cfg, "audio_output", "device", ""))

	// ── Capture device ───────────────────────────────────────────────────────
	section("Capture Device (REC OUT → track recognition)")

	fmt.Println("On Raspberry Pi, the first entry in arecord is often the built-in or HDMI device.")
	fmt.Println("Oceano needs the USB line-in / USB capture card wired from the amplifier REC OUT — not HDMI or analog headphone jack.")
	fmt.Println("Detecting ALSA capture devices (USB/line and on-board/HDMI are marked when possible)…")
	captureDevs, captureNote := listCaptureDevicesForREC(getString(cfg, "audio_input", "device", ""))
	if strings.TrimSpace(captureNote) != "" {
		logWarn(captureNote)
	}
	captureDevice := pickDevice("capture device (REC OUT loop)", captureDevs, getString(cfg, "audio_input", "device", ""))

	// ── Bluetooth ────────────────────────────────────────────────────────────
	section("Bluetooth")

	btName := prompt("Bluetooth device name", airplayName)

	// ── Apply ────────────────────────────────────────────────────────────────
	section("Applying configuration")

	ensureAvahiForAirPlay()
	if err := shairport.WriteConfig(shairportConf, airplayName, outputDevice); err != nil {
		logWarn("Could not write shairport-sync.conf: " + err.Error())
	} else {
		logOK("Written " + shairportConf + " (ALSA output for system shairport; AirPlay mDNS + playback)")
	}

	setKey(cfg, "audio_output", "airplay_name", airplayName)
	if outputDevice != "" {
		setKey(cfg, "audio_output", "device", outputDevice)
		if getString(cfg, "audio_output", "device_match", "") == "" {
			if m := regexp.MustCompile(`plughw:CARD=([^,]+),`).FindStringSubmatch(outputDevice); len(m) > 1 {
				setKey(cfg, "audio_output", "device_match", m[1])
			}
		}
	}
	if captureDevice != "" {
		setKey(cfg, "audio_input", "device", captureDevice)
	}
	// Bridge CLI setup completion to the web onboarding checklist.
	setKey(cfg, "advanced", "oceano_setup_acknowledged", true)
	if err := writeConfig(cfg); err != nil {
		logWarn("Could not update config.json: " + err.Error())
	} else {
		logOK("Updated " + configPath + " (including oceano_setup_acknowledged=true)")
	}

	configureBluetooth(btName)
	logOK(fmt.Sprintf("Bluetooth configured: name=%q, always discoverable", btName))

	// ── Display ────────────────────────────────────────────────────────────────
	section("Display (HDMI/DSI kiosk)")

	displayEnabled := prompt("Install display service", "n")
	displayUser := "pi"
	if u := strings.TrimSpace(os.Getenv("SUDO_USER")); u != "" {
		displayUser = u
	}
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

	// PipeWire default sink: route Bluetooth to the same ALSA output as shairport when multiple
	// USB devices are present. Target user: graphical session (display) or sudoer (SSH).
	pwUser := strings.TrimSpace(os.Getenv("SUDO_USER"))
	if pwUser == "" {
		pwUser = "pi"
	}
	if strings.ToLower(displayEnabled) == "y" && strings.TrimSpace(displayUser) != "" {
		pwUser = displayUser
	}
	if outputDevice != "" {
		installWirePlumberBTResilience(pwUser, outputDevice, getString(cfg, "audio_output", "device_match", ""))
	}

	logOK("Services restarted")

	fmt.Printf("\n%sSetup complete!%s\n", bold, reset)
	out, _ := exec.Command("hostname", "-I").Output()
	if fields := strings.Fields(string(out)); len(fields) > 0 {
		fmt.Printf("Open %shttp://%s:8080%s to review your configuration.\n",
			cyan, fields[0], reset)
		fmt.Printf("Then open %shttp://%s:8080/config%s to continue the web checklist (physical media first).\n",
			cyan, fields[0], reset)
	}
	fmt.Println("Next recommended web steps: Capture → Track Recognition (third-party APIs are bring-your-own-account; typically ACRCloud today) → Amplifier topology (optional IR) → Calibration → Stylus.")
	printDisplayResolutionHints()
}
