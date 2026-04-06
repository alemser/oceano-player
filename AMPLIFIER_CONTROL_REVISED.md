# Extensible Amplifier Control via IR (Broadlink RM4 Mini) — Revised

**Type**: Feature  
**Category**: Hardware Integration  
**Scope**: v2+  
**Priority**: Medium

## Problem

Oceano Player currently has no way to control the amplifier (e.g. Magnat MR 780) remotely—power, volume, and input selection must be done manually at the device. This creates friction when:
- User wants to power on/off the amplifier from the web UI or automatically (e.g., when AirPlay starts playing or switching to USB Audio input)
- Volume needs adjustment without leaving the listening room
- CD players connected to the amplifier can be controlled for track skipping, pause, stop, and resume

The implementation will be tested with:
- **Magnat MR 780**: tube-based integrated amplifier with valved pre-amp requiring ~30 seconds warm-up after power-on before audio is available. Input switching introduces ~2 seconds of silence before sound resumes.
- **Yamaha CD-S300**: CD player connected to the amplifier, used for testing transport controls and CD-specific operations.

Other amplifiers and CD players may have similar characteristics; the design accounts for this via configurable delay timings.

## Solution

Add an extensible **amplifier adapter framework** that:

1. Defines a standard `RemoteDevice` interface for generic IR-capable device control
2. Defines an `Amplifier` interface that extends `RemoteDevice` with amp-specific operations
3. Implements a Broadlink RM4 Mini adapter to control IR-capable amplifiers
4. Exposes REST API for state queries and remote commands
5. Provides pairing and configuration workflow in web UI
6. Supports future adapters (MQTT, IP control, serial) via the same interface

---

## Implementation

### Generic Remote Device Interface

Generic interface for any IR-controllable device (amp, CD player, receiver, etc.). Returns `ErrNotSupported` if the operation is not available on this device.

```go
// RemoteDevice defines common IR remote operations available on various devices.
// Methods return ErrNotSupported if the operation is not available for this device.
type RemoteDevice interface {
  // Query device identity
  Maker() string
  Model() string
  
  // Volume control (optional on amp, not on CD player)
  VolumeUp() error
  VolumeDown() error
  
  // Transport controls (optional on amp, standard on CD player)
  Play() error
  Pause() error
  Stop() error
  Next() error
  Previous() error
  
  // Power operations (optional)
  PowerOn() error
  PowerOff() error
}

// ErrNotSupported indicates the operation is not available on this device
var ErrNotSupported = errors.New("operation not supported on this device")
```

### Amplifier Interface

Amplifier-specific interface extending `RemoteDevice`. Includes input management and warm-up timing awareness.

```go
// Input represents an available input/source on the amplifier
type Input struct {
  Label string        // User-facing name: "USB Audio", "Phono", "CD", "AUX"
  ID    string        // Internal identifier used for IR commands: "USB", "PHONO", "CD", etc.
  device RemoteDevice // Existing remote devices can be configured for the input
}

type Amplifier interface {
  RemoteDevice  // Embedded: includes Maker(), Model(), VolumeUp(), VolumeDown(), PowerOn(), PowerOff()
  
  // Input management
  CurrentInput() (Input, error)
  InputList() []Input
  DefaultInput() Input
  SetInput(id string) error        // arguments: Input.ID, not label
  NextInput() error
  
  // State queries
  CurrentState() (powerOn bool, err error)
  
  // Timing information for warm-up and switching delays
  WarmupTimeSeconds() int          // e.g., 30 for tube amps like Magnat MR 780
  InputSwitchDelaySeconds() int    // e.g., 2 for settling time after input change
  
  // Audio readiness flag (false during warm-up or input switching, true when sound is available)
  AudioReady() bool
}
```

### CD Player Interface

CD player operations. Typically used as a standalone device controlled via Broadlink RM4 Mini configured for CD player IR codes.

```go
// CDPlayer defines operations on a CD player device (e.g., Yamaha CD-S300).
// Implements RemoteDevice for basic transport controls.
type CDPlayer interface {
  RemoteDevice  // Embedded: Maker(), Model(), Play(), Pause(), Stop(), Next(), Previous()
  
  // CD-specific queries
  CurrentTrack() (int, error)      // Track number (1-based), error if not supported
  TotalTracks() (int, error)
  IsPlaying() (bool, error)
  
  // Optional: time display if the CD player reports it via IR
  CurrentTimeSeconds() (int, error) // error if not supported
  TotalTimeSeconds() (int, error)
}
```

### YamahaRemoteDevice Implementation

`YamahaRemoteDevice` is a concrete implementation of `RemoteDevice` that wraps Broadlink RM4 Mini commands for the Yamaha CD-S300:

```go
// YamahaRemoteDevice implements RemoteDevice for Yamaha CD-S300.
// It queues IR commands to Broadlink RM4 Mini device.
type YamahaRemoteDevice struct {
  broadlink *BroadlinkClient
  // device pairing info, IR codes, etc.
}

// Implements RemoteDevice interface
func (y *YamahaRemoteDevice) Maker() string { return "Yamaha" }
func (y *YamahaRemoteDevice) Model() string { return "CD-S300" }
func (y *YamahaRemoteDevice) Play() error { /* send IR code */ }
func (y *YamahaRemoteDevice) Pause() error { /* send IR code */ }
// ... etc
```

## Architecture Pattern

The design uses **interface-based polymorphism** to decouple device capabilities from implementation:

1. **RemoteDevice** (generic interface) → common IR operations (power, volume, transport)
2. **Device implementations** (BroadlinkAmplifier, YamahaRemoteDevice) → wrap Broadlink API + device-specific IR codes
3. **Amplifier + CDPlayer** (specialized interfaces) → device-specific operations as needed
4. **Config** (data) → declares enabled devices and their pairing credentials (no code references)

This allows:
- Easy addition of new adapters (MQTT, IP, serial) without touching existing code
- Mixed environments (Broadlink RM4 Mini for both amp and CD player, or different IR protocols)
- State-manager to treat amplifier/CD player as abstract controllable devices

---

### BroadlinkAmplifier Adapter

Wraps Broadlink RM4 Mini SDK to control IR-capable amplifiers.

**Key responsibilities:**
- Pairing with RM4 Mini device (one-time setup via web UI)
- Storing device token, IP, and device ID in config
- Queueing IR commands to Broadlink cloud/local API
- Tracking warm-up timer and input switch delay via internal state machine
- Falling back gracefully if device goes offline

**For Magnat MR 780:**
- `WarmupTimeSeconds()` → 30
- `InputSwitchDelaySeconds()` → 2
- Supported inputs: USB Audio, Phono, CD, AUX
- `AudioReady()` → false immediately after `PowerOn()`; becomes true after internal timer expires

**For Yamaha CD-S300 (or other CD player):**
- Implements `CDPlayer` interface
- Supported commands: Play, Pause, Stop, Next, Previous
- Track/time queries return 0 or nil (if IR protocol doesn't support feedback)

---

## Configuration

### Sample Config Schema

Edit `/etc/oceano/config.json`:

**Note**: The config declares *what* devices are enabled and their pairing credentials. The Go code instantiates the appropriate *implementations* (BroadlinkAmplifier, YamahaRemoteDevice, etc.) based on the `"type"` field. No code is embedded in config.

```json
{
  "amplifier": {
    "enabled": true,
    "maker": "Magnat",
    "model": "MR 780",
    "inputs": [
      {
        "label": "USB Audio",
        "id": "USB"
      },
      {
        "label": "Phono",
        "id": "PHONO"
      },
      {
        "label": "CD",
        "id": "CD",
        "device": YamahaRemoteDevice
      },
      {
        "label": "AUX",
        "id": "AUX"
      }
    ],
    "default_input": "USB Audio",
    "warmup_seconds": 30,
    "input_switch_delay_seconds": 2,
    "broadlink": {
      "host": "192.168.1.100",
      "port": 80,
      "token": "<hex-token-from-pairing>",
      "device_id": "<hex-device-id>",
      "device_type": "rm4mini"
    }
  },
  "cd_player": {
    "enabled": true,
    "maker": "Yamaha",
    "model": "CD-S300",
    "broadlink": {
      "host": "192.168.1.100",
      "port": 80,
      "token": "<hex-token-from-pairing>",
      "device_id": "<hex-device-id-for-cd-player>",
      "device_type": "rm4mini"
    }
  }
}
```

---

## REST API Endpoints (`/api/amplifier/*`)

### Amplifier Endpoints

| Endpoint | Method | Body | Response | Notes |
|----------|--------|------|----------|-------|
| `/api/amplifier/state` | GET | — | See below | Returns current state, input list, warm-up status |
| `/api/amplifier/power` | POST | `{"action": "on"\|"off"}` | 200 OK or error | May queue delayed state updates for warm-up |
| `/api/amplifier/volume` | POST | `{"direction": "up"\|"down"}` | 200 OK or error | No state feedback (IR-only); command queued |
| `/api/amplifier/input` | POST | `{"id": "USB"\|"PHONO"\|"CD"}` | 200 OK or error | Changes input; delays audio-ready timer |
| `/api/amplifier/next-input` | POST | — | 200 OK or error | Cycles to next input in InputList |

**`/api/amplifier/state` Response:**
```json
{
  "maker": "Magnat",
  "model": "MR 780",
  "power_on": true,
  "current_input": {
    "label": "USB Audio",
    "id": "USB"
  },
  "input_list": [
    { "label": "USB Audio", "id": "USB" },
    { "label": "Phono", "id": "PHONO" },
    { "label": "CD", "id": "CD" },
    { "label": "AUX", "id": "AUX" }
  ],
  "default_input": { "label": "USB Audio", "id": "USB" },
  "audio_ready": false,
  "audio_ready_at": "2026-04-06T12:00:30Z",
  "warmup_seconds": 30,
  "input_switch_delay_seconds": 2,
  "last_updated": "2026-04-06T12:00:00Z"
}
```

**Error Responses:**
- `400 Bad Request` — invalid input ID, malformed JSON
- `404 Not Found` — amplifier not configured/not enabled
- `503 Service Unavailable` — Broadlink device offline or unreachable
- `500 Internal Server Error` — pairing incomplete or corrupted config

### CD Player Endpoints

| Endpoint | Method | Body | Response | Notes |
|----------|--------|------|----------|-------|
| `/api/cdplayer/state` | GET | — | See below | Track/time info if supported by IR profile |
| `/api/cdplayer/transport` | POST | `{"action": "play"\|"pause"\|"stop"\|"next"\|"prev"}` | 200 OK or error | Queue IR command |

**`/api/cdplayer/state` Response:**
```json
{
  "maker": "Yamaha",
  "model": "CD-S300",
  "track": 3,
  "total_tracks": 12,
  "is_playing": true,
  "current_time_seconds": 145,
  "total_time_seconds": 3200,
  "last_updated": "2026-04-06T12:00:00Z"
}
```

> Note that it may not be possible to know the current_time_seconds, total_time_seconds, total_tracks and track. The CD player may be just able to be IR controlled.

**Error Responses:**
- Same as amplifier endpoints
- Track/time fields may be `null` if IR protocol doesn't support queries

### Pairing Endpoints (Web UI Flow)

| Endpoint | Method | Body | Response | Purpose |
|----------|--------|------|----------|---------|
| `/api/amplifier/pair-start` | POST | `{"host": "192.168.1.100"}` | `{"pairing_id": "...", "status": "waiting"}` | Initiate pairing handshake |
| `/api/amplifier/pair-status` | GET | — | `{"pairing_id": "...", "status": "pairing\|success\|failure", "message": "..."}` | Poll pairing progress |
| `/api/amplifier/pair-complete` | POST | `{"pairing_id": "...", "token": "...", "device_id": "..."}` | 200 OK or error | Finalize pairing; store in config; restart services |

---

## Pairing Workflow (Web UI)

There must be a exclusive UI for adding/configuring the hardware.

### User-Facing Flow

1. **Preparation Screen**
   - Display instructions: "Put Broadlink RM4 Mini into pairing mode (LED flashing)"
   - Show: "Hold power button for 3 seconds until LED alternates red/green"
   - User enters Broadlink device IP address (text input)
   - Also enter Broadlink device PIN if required (usually printed on device)

2. **Pairing In Progress**
   - Click "Start Pairing"
   - UI calls `POST /api/amplifier/pair-start` with device IP
   - Shows: "Waiting for device...", spinner
   - Polls `GET /api/amplifier/pair-status` every 500ms

3. **Pairing Result**
   - **Success**: "Pairing successful! Token: [hex]. Device ID: [hex]. Restarting services..."
     - UI calls `POST /api/amplifier/pair-complete` to finalize
     - Web service auto-restarts
   - **Timeout** (5 seconds): "Device not found. Check IP and try again."
   - **Error**: Display specific error (e.g., "Device already paired", "Invalid PIN")

### Backend Pairing Logic

```
pair-start request (IP, optional PIN)
  │
  ├─→ Check device reachable on local network
  │    └─→ 503 if unreachable
  │
  ├─→ Initiate Broadlink pairing handshake
  │    └─→ Block until pairing completes or 5s timeout
  │
  ├─→ Extract token + device_id from pairing response
  │
  └─→ Return pairing_id (for status polling)

pair-status request (pairing_id)
  └─→ Return cached pairing result or "still waiting"

pair-complete request (pairing_id, token, device_id)
  ├─→ Validate token format
  ├─→ Store in /etc/oceano/config.json → amplifier section
  ├─→ Trigger systemd restart: oceano-web
  └─→ Return 200 OK
```

---

## Broadlink SDK / Library Selection

**⚠️ RESEARCH NEEDED**: Broadlink does not officially provide a Go SDK. Options:

1. **Python → subprocess bridge** (simplest)
   - Use `python-broadlink` package (well-maintained, supports RM4 Mini)
   - Call from Go via `os/exec`
   - JSON lines over stdout
   - No external Go dependency
   - **Downside**: subprocess overhead, Python runtime dependency

2. **Go bindings to Python** (via CFFI or shared library)
   - Wrap `python-broadlink` C extension
   - **Downside**: complex build, platform-specific

3. **Reverse-engineer Broadlink protocol** (ambitious)
   - Implement RM4 Mini pairing + command protocol in pure Go
   - Protocol is semi-documented (RM4 Mini uses HTTPS + AES-128-CBC)
   - **Reference**: https://github.com/mjg59/python-broadlink/blob/master/protocol.md
   - **Downside**: high complexity, potential for bugs

4. **Use generic HTTP client to Broadlink's local API** (middle ground)
   - RM4 Mini exposes local HTTPS endpoint (no cloud requirement)
   - Can send raw IR codes if device is already paired
   - **Requires**: working device + manual pairing (via mobile app first)
   - **Advantage**: minimal Go dependency

**Recommendation**: Start with **option 1 (Python subprocess)** for v1 to validate architecture. Post-v1, evaluate switching to option 3 (pure Go) if performance/dependency size is critical.

---

## Acceptance Criteria

### Must Have (v1)

- [ ] `RemoteDevice` interface defined, exported, documented
  - [ ] All methods return `(error)` or `(value, error)`
  - [ ] `ErrNotSupported` error constant defined
- [ ] `Amplifier` interface defined, exported, documented
  - [ ] Extends `RemoteDevice`
  - [ ] Includes `AudioReady()` flag
  - [ ] Includes timing methods: `WarmupTimeSeconds()`, `InputSwitchDelaySeconds()`
  - [ ] Input management: `SetInput(id string)`, `NextInput()`, `InputList()`
- [ ] `CDPlayer` interface defined (optional but recommended for decoupling)
- [ ] `Input` type defined (Label + ID + optional Device)
- [ ] `BroadlinkAmplifier` adapter implemented
  - [ ] Pairing workflow: manual IP + PIN input → token extraction → config storage
  - [ ] Command queueing to Broadlink local API
  - [ ] State queries (current input, power state)
  - [ ] Warm-up timer: `AudioReady()` false → true after delay
  - [ ] Input switch delay: queued state resets on input change
  - [ ] Error handling: device offline → 503, invalid input → 400
  - [ ] Rate limiting: queue commands, don't flood device
- [ ] Configuration schema
  - [ ] `amplifier` section with inputs, timings, Broadlink creds
  - [ ] `cd_player` section (minimal but present)
  - [ ] Both sections optional (enabled flag)
- [ ] REST API endpoints implemented + tested
  - [ ] `GET /api/amplifier/state` (query)
  - [ ] `POST /api/amplifier/power`, `/volume`, `/input`, `/next-input` (control)
  - [ ] `POST /api/amplifier/pair-start`, `/pair-status`, `/pair-complete` (pairing)
  - [ ] `GET /api/cdplayer/state`, `POST /api/cdplayer/transport` (CD player)
- [ ] Web UI: Amplifier configuration panel
  - [ ] Enable/disable toggle
  - [ ] Manual pairing page (IP + PIN input, start button, progress display)
  - [ ] Input selection dropdown
  - [ ] Power on/off buttons
  - [ ] Volume +/− buttons
  - [ ] Status display (current input, power, audio ready)
- [ ] Integration tests
  - [ ] Mock Broadlink API responses
  - [ ] Pairing success/failure scenarios
  - [ ] State transitions (power on → warm-up → audio ready)
  - [ ] Input switching with delay
- [ ] Documentation
  - [ ] Setup guide: hardware prerequisites, network setup
  - [ ] Pairing instructions: how to put RM4 Mini into pairing mode
  - [ ] Configuration reference: all config options explained
  - [ ] API reference: endpoint schemas and error codes

### Out of Scope (v1)

- [ ] Auto-switching amplifier input based on detected audio source (separate feature; see **open question** below)
- [ ] Volume level display or slider (only +/− buttons)
- [ ] IR code learning workflow (use Broadlink's pre-programmed database first)
- [ ] Additional adapter implementations (MQTT, IP, serial deferred to v2+)
- [ ] Real hardware testing on Pi (manual/v2+)
- [ ] CD player track/time feedback (if IR protocol doesn't support queries)

---

## Timing Diagrams

### Power-On Timeline (Magnat MR 780)

```
Action: PowerOn() via IR

T+0s    ├─ Send IR command to Broadlink
        ├─ AudioReady() = false
        ├─ Start internal 30-second timer
        │
T+30s   ├─ Timer expires
        ├─ AudioReady() = true
        └─ UI can signal "Amplifier Ready"

State API Response:
  "power_on": true
  "audio_ready": false
  "audio_ready_at": "2026-04-06T12:00:30Z"
```

### Input-Switch Timeline (e.g., USB → Phono)

```
Action: SetInput("PHONO")

T+0s    ├─ Send IR command to Broadlink
        ├─ AudioReady() = false
        ├─ Start internal 2-second timer
        │
T+0–2s  ├─ Audio bus silent (input settling)
        │
T+2s    ├─ Timer expires
        ├─ AudioReady() = true  [only if power_on also true]
        └─ Audio resumes

Note: This 2-second silence can be leveraged in v2+ for
confirming successful input switch (monitor VU frames).
```

---

## Error Handling & Retry Logic

| Scenario | Behavior | HTTP Response |
|----------|----------|---------------|
| Broadlink device offline | Command queued; retry polling after 5s, give up after 30s | 503 Service Unavailable |
| Pairing timeout (>5s) | Display user error; user must click "Retry" | 408 Request Timeout |
| Invalid input ID (e.g., "INVALID") | Reject command immediately | 400 Bad Request |
| Device already paired (via UI) | Prevent re-pairing; direct to config editor | 409 Conflict |
| Warm-up interrupted (unplug during warmup) | `AudioReady()` remains false; user must retry power-on | (state unchanged) |
| Config corrupted or missing | Amplifier section disabled on startup; UI shows "Not configured" | 404 Not Found |

---

## Integration Points

### With oceano-state-manager

- **Future (separate feature)**: Auto-switch input when physical media detected
  - State manager reads `amplifier/state` API
  - On `source="Physical"`, calls `/api/amplifier/input` to set "Phono" or "CD"
  - Waits for `audio_ready=true` before triggering recognition
  - ⚠️ **Open question**: Does successful input change trigger immediate re-recognition, or wait for track boundary? (To be determined in v2 design.)

### With oceano-web

- Configuration UI: Amplifier section in settings form
- Pairing workflow: dedicated page in web UI
- State polling: `/api/amplifier/state` queried by now-playing display (optional)
- Power controls: idle screen can include "Amp Power" button (deferred to v2 UI enhancements)

---

## Open Questions & Future Considerations

### Q1: Broadlink Device Profiles for Testing

**Question**: Does Broadlink maintain a pre-programmed IR code database that includes the Magnat MR 780 and Yamaha CD-S300?

**Context**: Broadlink RM4 Mini can either:
- Use pre-loaded IR codes from Broadlink's cloud database (requires device pairing + cloud sync)
- Learn IR codes manually (point remote at RM4 Mini, capture sequence)

**Action Needed**: 
- [ ] Confirm if Magnat MR 780 and Yamaha CD-S300 are in Broadlink's database
- [ ] If not, document manual IR learning workflow (deferred to v2)
- [ ] If yes, ensure pairing workflow automatically syncs device profile

### Q2: Input Switching & Re-Recognition

**Question**: When amplifier input switches (e.g., Phono → USB Audio), should the state manager immediately trigger track re-recognition?

**Rationale**: 
- User might swap physical records without pressing any button
- Input switch introduces brief silence, which we can detect via VU frames
- Could use this as a "track boundary" signal for fingerprinting

**Options**:
1. **No auto-trigger** — only recognize on manual trigger or silence-based VU boundary
2. **Immediate trigger** — any input change fires recognition (might be false-positives during searching)
3. **After settling delay** — wait `InputSwitchDelaySeconds()` + brief audio resume, then trigger if audio resumes

**Decision**: To be determined in v2 feature design (auto-input-switching issue).

### Q3: Volume State & Feedback

**Context**: Most IR amplifiers don't report current volume over IR protocol. We can only send +/− commands with no feedback.

**Options for v2+**:
- [ ] Heuristic: track volume state locally (count +/− commands, decay on silence)
- [ ] IP control: Some amps (Denon, Onkyo) expose EISCP protocol for full state queries
- [ ] Dedicated volume display: always show "+5" on screen after volume-up, fade after 3s

**Recommendation**: Defer volume display to v2. v1 provides +/− buttons only.

### Q4: CD Player Track/Time Queries

**Context**: Not all IR protocols support querying track number or elapsed time. Yamaha CD-S300 may or may not report this via Broadlink.

**Action**: 
- [ ] Test against Yamaha CD-S300 post-pairing
- [ ] If not supported: CD player state endpoint returns `null` for track/time fields
- [ ] If supported: parse IR query responses and return values

---

## Testing Strategy

### Unit Tests
- Config validation (missing/invalid pairing token, malformed JSON)
- Mock `Amplifier` and `CDPlayer` implementations
  - Verify state transitions (power on → warm-up → audio ready)
  - Verify input switching resets delays
  - Verify `ErrNotSupported` returned for unsupported ops
- Error cases (invalid input ID, device offline)

### Integration Tests
- Mock Broadlink HTTP API responses
- Full pairing flow: IP → handshake → token extraction → config store
- Command queueing: multiple rapid requests don't overflow device
- Retry logic: offline device recovers after reconnection

### Manual Testing (v2+)
- [ ] Real Magnat MR 780 + RM4 Mini on same network
- [ ] Real Yamaha CD-S300 (if available)
- [ ] Verify all IR commands execute
- [ ] Measure actual warm-up time (confirm 30s)
- [ ] Measure actual input-switch delay (confirm 2s)

---

## Deployment Notes

1. **Pi Network**: Broadlink RM4 Mini and Raspberry Pi must be on same LAN (no cross-subnet).
2. **Firewall**: Ensure local network allows mDNS (port 5353) if using device discovery.
3. **Config Persistence**: `/etc/oceano/config.json` permissions must be `0644` so web service can write pairing results.
4. **Systemd Restart**: Pairing completion triggers `systemctl restart oceano-web` (must run as root or via `sudo`).

---

## Related Issues & Blockers

**Blocks:**
- Auto-switching amplifier input when physical media detected (separate v2+ feature)

**Related:**
- Improved track boundary detection via VU silence monitoring
- Phono vs. CD auto-detection (requires reliable calibration)

**Blocked By:** (none)

## Useful notes about the testing hardware

### Magnat MR 780

- There is no direct set via remote control for a especifc input. The input is changing by rotating a knob. There must be a start setting to so that it can behave predictably.

### Yamaha CD-S300

- There won't be a way to check the track, times or total lenght. Just the usual standard commands availble in the remote. track information will be available in the Oceano state which is added by the user or detected.