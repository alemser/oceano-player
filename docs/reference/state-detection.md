# State Detection & Unified State Management Reference

## Overview
This document details the state detection pipeline implemented in `oceano-state-manager`, which aggregates inputs from physical source detection, AirPlay, Bluetooth, and track recognition into a single unified state contract for downstream consumers (NowPlaying UI, iOS app via `/api/stream`, `/api/status`).

The state manager is the single source of truth for what is currently playing, overriding lower-priority sources when higher-priority audio is detected. All state changes are propagated in real time via Server-Sent Events (SSE) and written atomically to `/tmp/oceano-state.json`.

## Architecture
The state manager integrates with all Oceano subsystems as follows (matching the top-level architecture diagram):

```
oceano-state-manager
  ├── reads /tmp/oceano-source.json      (physical source polling)
  ├── reads /tmp/oceano-vu.sock          (VU monitor: silence→audio = track boundary trigger; optional RMS percentile learning → `rms_learning` in library SQLite)
  ├── reads /tmp/oceano-pcm.sock         (recognition capture — no second arecord needed)
  ├── reads shairport-sync metadata pipe (AirPlay metadata)
  ├── dbus-monitor subprocess            (Bluetooth: BlueZ AVRCP metadata + MediaTransport1 codec)
  ├── internal/recognition               (provider clients + chain orchestration)
  ├── internal/library                   (SQLite collection and artwork paths)
  ├── recognition coordinator            (trigger loop + confirmation + persistence policies)
  └── writes /tmp/oceano-state.json      (unified state for UI)
```

## Source Priority Rules
Source priority is fixed and enforced at every state evaluation cycle. Higher-priority sources override lower-priority sources unconditionally:

1. **Physical** (highest priority): Reported by `oceano-source-detector` via `/tmp/oceano-source.json`. Takes priority over all other sources, even if AirPlay or Bluetooth are active. This aligns with the Magnat MR 780 amplifier behavior: manual input switching means physical audio on REC OUT is always the intended source.
2. **AirPlay**: Reported via shairport-sync metadata pipe. Active only when Physical source is `None`.
3. **Bluetooth**: Reported via BlueZ AVRCP dbus monitor. Active only when Physical is `None` and AirPlay is inactive.
4. **None** (lowest priority): No active source detected.

### Priority Enforcement Example
- If Physical source is active, AirPlay starts playing: State remains `Physical`, NowPlaying continues showing physical media info.
- If Physical stops (source detector reports `None`) and AirPlay is active: State switches to `AirPlay` immediately.
- If both AirPlay and Bluetooth are active (no Physical): AirPlay takes priority, Bluetooth metadata is ignored.

## Input Ingestion
The state manager polls or subscribes to four categories of inputs:

### 1. Physical Source & VU/PCM (oceano-source-detector)
- **/tmp/oceano-source.json**: Polled every 500ms. Contains committed `Physical`/`None` state from the source detector's majority-vote RMS window.
- **/tmp/oceano-vu.sock**: Stream socket delivering ~22 FPS float32 L+R VU frames. Used for:
  - Silence detection: Gaps >2s trigger track boundary events for Physical source.
  - State transitions: RMS above silence threshold → `state: "playing"`; below → `state: "idle"` after `idle-delay` (default 60s).
- **/tmp/oceano-pcm.sock**: Stream socket delivering S16_LE stereo 44100Hz PCM. Used exclusively by the recognition coordinator to capture audio for ACRCloud/Shazam, with no impact on state detection beyond providing track metadata for Physical source.

### 2. AirPlay Metadata (shairport-sync)
- Reads the shairport-sync metadata pipe (default `/tmp/shairport-sync-metadata`) for:
  - Track metadata: `title`, `artist`, `album`, `artwork_url`, `duration_ms`, `seek_ms`, `seek_updated_at`
  - Audio format: `samplerate`, `bitdepth`
  - Playback state: `playing`/`stopped`/`paused` (derived from metadata updates and pipe activity)
- Shairport-sync state is independent of DAC availability: even if the DAC is missing (silent fallback mode), AirPlay metadata continues to flow and state will reflect AirPlay if Physical is inactive.

### 3. Bluetooth (BlueZ AVRCP)
- Runs a `dbus-monitor` subprocess listening for:
  - `org.bluez.MediaPlayer1` property changes: `Track` (metadata), `Status` (playing/stopped/paused)
  - `org.bluez.MediaTransport1` property changes: `Codec` (AAC, SBC, LDAC, AptX, Opus)
- Metadata is mapped to the unified track schema; codec info is added as a format chip in NowPlaying.

### 4. Recognition Coordinator
- Provides resolved track metadata for Physical source only, after running the ACRCloud/Shazam provider chain.
- Updates `state.track` for Physical source only when recognition succeeds; failed recognitions leave the last known track (or null) until the next successful attempt or idle timeout.

## State Transition Flow
Every 500ms, the state manager runs an evaluation cycle to update the unified state:

### Cycle 1: Evaluate Physical Source
1. Read latest `/tmp/oceano-source.json`.
2. If source is `Physical`:
   - Set `state.source = "Physical"` unconditionally.
   - Read VU frames to determine playback state:
     - RMS > silence threshold → `state.state = "playing"`
     - RMS ≤ silence threshold for < `idle-delay` → `state.state = "playing"` (track still active)
     - RMS ≤ silence threshold for ≥ `idle-delay` → `state.state = "idle"`
   - If VU detects a silence→audio boundary (track change): trigger recognition coordinator.
   - Apply latest recognition result to `state.track` if available.
3. If source is `None`: proceed to Cycle 2.

### Cycle 2: Evaluate AirPlay
1. Check shairport-sync metadata pipe for active playback.
2. If AirPlay is playing/active:
   - Set `state.source = "AirPlay"`
   - Populate `state.track` with shairport-sync metadata (including seek interpolation fields)
   - Set `state.state` to match shairport-sync status (`playing`/`stopped`/`paused`)
3. If no active AirPlay: proceed to Cycle 3.

### Cycle 3: Evaluate Bluetooth
1. Check BlueZ AVRCP dbus for active playback.
2. If Bluetooth is playing/active:
   - Set `state.source = "Bluetooth"`
   - Populate `state.track` with AVRCP metadata + codec info
   - Set `state.state` to match MediaPlayer1 status
3. If no active Bluetooth: set `state.source = "None"`, `state.track = null`, `state.state = "idle"`.

### Cycle 4: Finalize & Emit
1. Update VU fields in state from latest `/tmp/oceano-vu.sock` frame (throttled writes so disk + SSE fan-out stay bounded).
2. Write updated state atomically to `/tmp/oceano-state.json` (write to tmp file → rename to avoid partial reads).
3. Notify oceano-web to push updated state via SSE to all connected clients (NowPlaying, iOS app). **`oceano-web`** may **strip** the top-level `vu` object from SSE and **`GET /api/status`** unless the client passes **`?vu=1`** (see [`http-lightweight-clients.md`](http-lightweight-clients.md)); **`/nowplaying.html`** requests **`/api/stream?vu=1`** for meters.

## Output Contract
The unified state follows the UI contract defined in `AGENTS.md`, with VU data added:

```json
{
  "source": "AirPlay | Bluetooth | Physical | None",
  "track": {
    "title": "string",
    "artist": "string",
    "album": "string",
    "artwork_url": "string | null",
    "duration_ms": 0,
    "seek_ms": 0,
    "seek_updated_at": "ISO8601 timestamp",
    "samplerate": "string | null",
    "bitdepth": "string | null",
    "codec": "string | null"
  },
  "vu": {
    "left": 0.0,
    "right": 0.0
  },
  "state": "playing | stopped | idle",
  "updated_at": "ISO8601 timestamp"
}
```

### NowPlaying UI Consumption
The NowPlaying UI (`nowplaying.html`) connects to `/api/stream` SSE endpoint, which pushes state updates immediately on change. It renders:
- Source logo (AirPlay, Bluetooth, Vinyl, CD, None idle clock)
- Track metadata + artwork (with placeholder for unknown tracks)
- Format chips: AirPlay (samplerate + bitdepth), Bluetooth (codec + samplerate + bitdepth), Physical (CD track, Vinyl side + track)
- VU meters from `state.vu`
- Identifying animation when recognition coordinator is running for Physical source
- Reconnects with exponential backoff if SSE connection is lost.

## Edge Cases & Resilience
### Source Oscillation (Physical ↔ None)
The source detector uses a rolling majority-vote window to commit Physical/None changes, preventing rapid oscillation. The state manager only updates `state.source` after the detector commits a change, so UI flicker is avoided.

### Phono Hum Persisting Physical Source
If the phono stage has residual hum keeping RMS above the silence threshold during record changes:
- `state.source` remains `Physical` even though no music is playing
- Old track info persists until the next VU boundary trigger (silence → audio) or the `RecognizerMaxInterval` (default 5min) elapses.

### AirPlay Silent Fallback
When the DAC is missing, shairport-sync outputs to ALSA null. This does not affect state detection: AirPlay metadata still flows, so `state.source` will be `AirPlay` if Physical is inactive, even though no audio is audible.

### Track Info Persistence
- Physical: Track info persists until a new recognition succeeds, or `idle-delay` elapses with no audio (then `track` is set to null)
- AirPlay/Bluetooth: Track info updates immediately on metadata change; null when playback stops.

## Cross-Repo Impact
Any changes to state detection, source priority, or output contract fields must be reflected in `oceano-player-ios`, as it consumes the same `/api/stream` and `/api/status` endpoints. Follow the cross-repo sync checklist in `docs/cross-repo-sync.md` for all state-related changes.
