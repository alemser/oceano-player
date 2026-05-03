# Static assets (oceano-web)

Embedded files for the **local HDMI/DSI Now Playing** page only.

## Files

- `nowplaying.html` — full-screen display (kiosk target)
- `nowplaying.css` — layout and theme for the display
- `icons.js` — source icons shared by the display
- `nowplaying/` — display scripts (SSE client, clock, weather, helpers)

There is **no** embedded configuration hub; use **`oceano-player-ios`** or
`POST /api/config` / `sudo oceano-setup` to change `/etc/oceano/config.json`.
