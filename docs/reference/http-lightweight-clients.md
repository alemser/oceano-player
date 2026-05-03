# HTTP contract: lightweight clients (iOS, automation)

This document owns the **optional performance knobs** on `oceano-web` for LAN clients that poll or stream state. Downstream: **`oceano-player-ios`** (see `docs/cross-repo-sync.md`).

## `GET /api/stream` (SSE)

- **Default:** the `data:` JSON **omits** the top-level `vu` object (stereo meter snapshot) even when `oceano-state-manager` writes it to `/tmp/oceano-state.json`. This avoids high-frequency JSON decode on clients that do not render meters.
- **VU included:** `GET /api/stream?vu=1` — same payload shape as the on-disk state file (includes `vu` when present).
- **HDMI / Now Playing:** `static/nowplaying/main.js` uses **`/api/stream?vu=1`** so local meters keep working.

### Named SSE event: `library`

When the SQLite **`library_version`** counter changes, the server may emit:

```http
event: library
data: {"library_version":42}

```

Clients should treat unknown `event:` names as forward-compatible. A typical pattern: on `library`, call `GET /api/library/changes?since_version=…` or revalidate `GET /api/library` with `If-None-Match`.

## `GET /api/status`

Same **`vu`** rule as SSE: meters are **excluded by default**; use **`GET /api/status?vu=1`** to include `vu`.

## `GET /api/player/summary`

Compact JSON for **1–3 s foreground polling** (source, `state`, `format`, `physical_detector_active`, reduced `track`, `recognition`, `library_version`, `updated_at`).

- **`ETag`** + **`If-None-Match`** → **`304 Not Modified`** when unchanged.
- Response header **`X-Oceano-Library-Version`**: same integer as `library_version` in the body.

## `GET /api/library`

- Full collection array (unchanged shape).
- **`ETag`** + **`If-None-Match`** → **`304`** when the serialized list is unchanged.
- **`X-Oceano-Library-Version`**: monotonic counter bumped on every `collection` INSERT / UPDATE / DELETE (SQLite triggers + `library_changelog`).

## `GET /api/library/changes?since_version=N`

- **`since_version`:** last `library_version` the client applied (default `0`).
- Response: `{ "library_version": <current>, "deleted_ids": [...], "upserts": [ <LibraryEntry>, ... ] }`.
- **`upserts`** are full rows still present in `collection` after the window; **`deleted_ids`** are collection primary keys removed in that version window.

## `GET /api/config`

See existing **`ETag` / `304`** behaviour (`docs/cross-repo-sync.md`).
