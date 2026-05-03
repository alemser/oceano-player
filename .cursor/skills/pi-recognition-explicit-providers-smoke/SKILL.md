---
name: pi-recognition-explicit-providers-smoke
description: >-
  Mandatory verification after edits to explicit recognition.providers /
  merge_policy wiring: run state-manager Go tests plus
  scripts/pi-recognition-provider-smoke.sh on a Pi (or --dry-run). Use with
  pi-access for SSH; pair pi-loopback-capture-sim for PCM-driven recognition.
disable-model-invocation: true
---

# Explicit provider list — smoke and regression policy

## When you must run this

**Any change** that can affect how a **non-empty** `recognition.providers[]` list or **`merge_policy`** is parsed, validated, saved, or turned into runtime recognizers **must** complete the checklist below before the change is considered done (including PR merge).

Treat as in-scope (non-exhaustive):

| Area | Examples |
|------|----------|
| State manager | `recognition_setup.go`, `recognition_config_load.go`, `recognition_providers*.go`, `recognition_chain_matrix_test.go`, `config_types.go` (`RecognitionProvider*`, merge policy fields) |
| Web config contract | `cmd/oceano-web/config.go` — `RecognitionConfig.Providers`, `MergePolicy`, save/load paths that touch `recognition` in JSON |
| Provider IDs / mapping | `internal/recognition/*` when it changes **which** `id` strings the explicit list binds to (e.g. `shazam` → Shazamio) |
| Smoke script | `scripts/pi-recognition-provider-smoke.sh` — after editing, re-run the script on a Pi |

Out of scope (no obligation to run this smoke, but run normal `go test` as usual):

- Pure coordinator timing/backoff changes with **no** change to provider list construction.
- Library / history-only changes that do not touch config load or plan build.

## Checklist (in order)

1. **Local — always**

   ```bash
   cd /path/to/oceano-player
   go test ./cmd/oceano-state-manager/... -count=1 -short
   go test ./cmd/oceano-state-manager -run TestBuildRecognitionPlanFromChain_matrix -count=1
   ```

2. **Pi — config path and logs** (uses real `systemd` + `journalctl`; backs up and restores `config.json`)

   ```bash
   sudo ./scripts/pi-recognition-provider-smoke.sh --dry-run
   sudo OCEANO_CONFIG=/etc/oceano/config.json ./scripts/pi-recognition-provider-smoke.sh
   ```

   If you cannot reach a Pi before merge, state that in the PR and run (1) only; follow up with (2) on hardware.

3. **Pi — full audio path (optional but recommended for recognition behavior)**

   Use **pi-loopback-capture-sim**: loopback capture + WAV loop + same provider smoke or manual trigger so PCM reaches the coordinator.

## Cross-repo

Edits here that change JSON keys, provider `id` values, or merge semantics may require updates in **`oceano-player-ios`** and **`docs/cross-repo-sync.md`** — see repository rules.
