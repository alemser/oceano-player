# Code Review Findings - 2026-05-01

This document records the review findings from the May 1, 2026 architecture and maintainability review.

## Summary

- `go test ./...` passed when run outside the sandbox.
- `go test ./... -cover` passed, with notable coverage gaps in setup, recognition, and source-detector packages.
- `node --test cmd/oceano-web/static/nowplaying/helpers.test.js` failed because the test imports a missing module.

## Findings

### [P1] Physical Source Priority Is Inverted

**Location:** `cmd/oceano-state-manager/state_output.go:44-52`

The repository contract says physical audio detected on REC OUT must take priority over simultaneous AirPlay activity. However, `buildState` currently selects AirPlay before Physical. The recognition coordinator also skips recognition when AirPlay or Bluetooth is active even when `physicalSource == "Physical"`, and the tests encode that inverted behavior.

**Impact:** When the Magnat MR 780 is routing a physical source while an AirPlay session remains active, the UI and iOS consumer can receive AirPlay state instead of Physical/CD/Vinyl state, and physical recognition will not run.

**Recommended fix:** Make physical detector activity win over streaming sources in state construction and recognition eligibility, then update tests to match the documented priority.

### [P1] Arbitrary Artwork Path Can Be Served

**Location:** `cmd/oceano-web/library.go:1156-1194`

`PUT /api/library/{id}` accepts `artwork_path` without restricting it to the managed artwork directory. Later, `/api/library/{id}/artwork` and `/api/artwork` serve that path through `http.ServeFile`.

**Impact:** On an unauthenticated local network, a client can set `artwork_path` to a local file readable by the web process and then retrieve it through the artwork endpoints. Backup generation already protects against this class of issue, but the online read/edit path does not.

**Recommended fix:** Validate edited artwork paths against the configured artwork directory, reject symlinks and paths outside that directory, and consider storing only managed artwork references rather than arbitrary absolute paths.

### [P2] Web Routes Keep Stale Config Paths After Save

**Location:** `cmd/oceano-web/main.go:197-205`

`main` loads `cfg` once and registers library, backup, history, stylus, and calibration routes with paths captured at startup. `apiPostConfig` recognizes changes to `advanced.library_db`, `advanced.state_file`, `advanced.artwork_dir`, and `advanced.vu_socket`, then restarts detector/manager services, but it does not restart or reconfigure `oceano-web`.

**Impact:** After saving those advanced settings, `/api/library`, backups, history, stylus, and calibration endpoints may continue using the old database, socket, state file, or artwork directory until `oceano-web` is restarted.

**Recommended fix:** Resolve config-dependent paths per request through a shared config provider, or restart/reload `oceano-web` when web-owned path settings change.

### [P2] HTTP Server Has No Timeouts

**Location:** `cmd/oceano-web/main.go:258-260`

`http.ListenAndServe` uses the default server without `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, or `IdleTimeout`. `oceano-web` exposes operational actions, multipart upload, and SSE endpoints, so slow or stuck clients can keep goroutines and file descriptors alive indefinitely.

**Impact:** A small number of slow connections can degrade the web service, especially on a Raspberry Pi.

**Recommended fix:** Use an explicit `http.Server` with conservative read/header/idle timeouts. Keep SSE behavior in mind by using a compatible write timeout strategy for `/api/stream`.

### [P2] Browser Test Is Broken And Not Run By CI Script

**Location:** `cmd/oceano-web/static/nowplaying/helpers.test.js:4-8`

`helpers.test.js` imports `./nowplaying_helpers.js`, but the repository contains `helpers.js`. In addition, `scripts/test.sh` and `make test` only run `go test ./...`, so the JavaScript Now Playing test is not part of the normal test path.

**Impact:** The existing browser helper test fails immediately when run with Node and does not currently protect the Now Playing UI from regressions.

**Recommended fix:** Update the test import to `./helpers.js`, then add `node --test cmd/oceano-web/static/nowplaying/helpers.test.js` to `scripts/test.sh` and `make test`.

## Architecture And Maintainability Notes

`cmd/oceano-state-manager` concentrates many mutable concerns inside `mgr`: AirPlay, Bluetooth, physical source state, recognition, library sync, history, continuity, and output. This remains testable, but behavior changes increasingly require reasoning across many fields. A low-risk improvement would be to extract source-specific snapshots or reducers, starting with `buildState` and recognition priority.

`cmd/oceano-web` mixes bootstrap, config reconciliation, systemd writes, route registration, and scheduled tasks. The stale config-path issue comes from some routes reading config per request while others capture startup values. A small shared runtime config provider would reduce this inconsistency without requiring a large rewrite.
