## Integrating Oceano Player (AirPlay) with `spi-now-playing`

Goal: when you stream to **AirPlay (shairport-sync)**, `spi-now-playing` should show:

- **title / artist / album**
- **artwork** (preferred) or external fallback artwork
- **basic playback status** (play/stop) and (if available) progress

This document is written against the current `spi-now-playing` architecture:

- It uses a `MediaPlayer` abstraction (`src/media_players/base.py`) with:
  - `connect()`, `receive_message()`, `is_connected()`, `close()`
  - optional `get_state()`
- The renderer expects a **state dict** with keys like:
  - `title`, `artist`, `album`, `status`
  - optional `duration` (seconds), `seek` (ms), `samplerate`, `bitdepth`
  - optional `_resolved_artwork` as `{"cache_key": str, "image": PIL.Image, "source": str}`

### Recommended approach (minimal + robust)

Keep Oceano Player minimal and **read metadata directly from `shairport-sync`** inside `spi-now-playing` by adding a new media player implementation.

Why:

- `shairport-sync` already exposes the metadata you need (including cover art if enabled).
- Avoids building and maintaining an API surface in Oceano Player before it’s needed.
- Fits the existing `MediaPlayer` model cleanly (it’s just another “backend”).

### Step 1: Enable shairport-sync metadata (title/artist/album + cover art)

On Raspberry Pi OS, `shairport-sync` reads configuration from a system config file (path varies by distro/package, commonly one of):

- `/etc/shairport-sync.conf`
- `/etc/shairport-sync/shairport-sync.conf`

Add or update the `metadata` section to enable the **metadata pipe** and **cover art**:

```conf
metadata =
{
  enabled = "yes";
  include_cover_art = "yes";
  pipe_name = "/tmp/shairport-sync-metadata";
  pipe_timeout = 5000;
  cover_art_cache_directory = "/tmp/shairport-sync/.cache/coverart";
};
```

Then restart:

```bash
sudo systemctl restart shairport-sync
```

Notes:

- Cover art availability depends on how `shairport-sync` is built/packaged (some builds include more metadata features than others).
- Even without cover art, you still get **track text metadata**, and `spi-now-playing` can use its existing external artwork fallback (`ArtworkLookup`) when `EXTERNAL_ARTWORK_ENABLED=true`.

### Step 2: Add a new `MediaPlayer` in `spi-now-playing`

Create a new file in `spi-now-playing`:

- `src/media_players/shairport_sync.py`

Implement a `ShairportSyncClient(MediaPlayer)` that:

- `connect()` opens the FIFO at `/tmp/shairport-sync-metadata` (or a configured path)
- `receive_message(timeout)` reads and parses metadata events
- `close()` closes the file descriptor
- `is_connected()` checks the fd is open

#### What to parse from shairport-sync

`shairport-sync` emits metadata “records” with *type* and *code* fields. You’ll typically need:

- **Text metadata**
  - `core/asar` (artist)
  - `core/asal` (album)
  - `core/minm` (track title)
- **Cover art**
  - `ssnc/PICT` (binary image bytes, usually JPEG/PNG)
- **Playback status / timing (optional)**
  - `ssnc/pbeg` and `ssnc/pend` (playback begin/end)
  - `ssnc/prgr` (progress) on some builds

The easiest way to learn the exact codes your build emits is to temporarily install the official reader and watch output:

```bash
# optional helper (from mikebrady/shairport-sync-metadata-reader)
# run it in a terminal to observe live metadata events:
shairport-sync-metadata-reader < /tmp/shairport-sync-metadata
```

#### How to map into `spi-now-playing` state dict

Maintain an internal “current state” object, and every time you have enough fields (or when something changes), emit a dict like:

```python
state = {
  "title": "…",
  "artist": "…",
  "album": "…",
  "status": "play",           # or "stop"/"pause"
  "seek": 0,                  # ms (optional)
  "duration": 0,              # seconds (optional)
  "_resolved_artwork": {
    "cache_key": "airplay:<some stable id>",
    "image": pil_image,       # PIL.Image.Image
    "source": "airplay",
  },
}
```

`renderer.py` will display:

- text mode: `title/artist/album`
- cover mode: uses `_resolved_artwork.image` if present; otherwise it will render a placeholder card (and `MediaPlayer.resolve_artwork()` can still do external fallbacks if you call it)

#### Artwork handling

When you receive `ssnc/PICT` bytes:

- detect image type (JPEG/PNG) from magic bytes
- decode into a `PIL.Image` (RGB)
- set `_resolved_artwork` with a cache key that changes only when the artwork changes (e.g. hash of bytes, or incrementing counter)

### Step 3: Add it to auto-detection / config in `spi-now-playing`

Update `spi-now-playing/src/config.py`:

- add a new allowed `MEDIA_PLAYER` value (e.g. `shairport` or `airplay`)
- add `SHAIRPORT_METADATA_PIPE` env var (default `/tmp/shairport-sync-metadata`)

Update `spi-now-playing/src/app/main.py`:

- add a candidate in `auto_detect_media_player()` and `detect_media_player()` that instantiates `ShairportSyncClient`

Example desired config usage:

- `MEDIA_PLAYER=shairport`
- `SHAIRPORT_METADATA_PIPE=/tmp/shairport-sync-metadata`

### Step 4: Run `spi-now-playing` for AirPlay

Edit `spi-now-playing`’s systemd unit and set:

```ini
Environment="MEDIA_PLAYER=shairport"
Environment="SHAIRPORT_METADATA_PIPE=/tmp/shairport-sync-metadata"
```

Then:

```bash
sudo systemctl daemon-reload
sudo systemctl restart spi-now-playing.service
```

### Where Oceano Player fits

Right now, Oceano Player’s job is to **start/supervise** `shairport-sync`. The integration above requires no changes in Oceano Player beyond ensuring `shairport-sync` is installed and configured.

If you later want a cleaner “single source” for all protocols (AirPlay, UPnP, Bluetooth), the next evolution is:

- Oceano Player reads `shairport-sync` metadata
- Oceano Player exposes a small local interface for “now playing”
  - UNIX socket or HTTP on `localhost`
  - payload compatible with `spi-now-playing`’s expected state keys

At that point, `spi-now-playing` can switch from parsing `shairport-sync` directly to simply consuming Oceano Player’s “now playing” endpoint.

