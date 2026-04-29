# AI Cross-Repo Sync Checklist

Use this file whenever backend changes are made in `oceano-player`.

The iOS app (`oceano-player-ios`) is a direct consumer of backend endpoints and config semantics. Backend work is not complete until cross-repo impact is reviewed.

## When this checklist is mandatory

Run this checklist for any change touching:

- HTTP APIs (`cmd/oceano-web` handlers, routes, status codes, payload fields)
- playback/state semantics (`/tmp/oceano-state.json`, SSE event meaning, source priority)
- config schema/defaults (`/etc/oceano/config.json`, save/migration behavior)
- stylus, history/insights, recognition, amplifier/topology, IR payloads
- install/setup flows that change runtime behavior seen by clients

## Required steps

1. **Contract diff**
   - List all changed endpoints/fields/semantics.
   - Mark each item as `compatible`, `additive`, or `breaking`.

2. **Compatibility policy**
   - Prefer additive changes.
   - For removals/renames, keep compatibility aliases when possible.
   - If breaking behavior is unavoidable, document migration notes clearly.

3. **Documentation sync (same PR/commit)**
   - Update `README.md` sections that expose user-visible behavior.
   - Update `CLAUDE.md` if architecture/workflow assumptions changed.
   - Update any endpoint-specific docs/comments that became stale.

4. **iOS impact note**
   - Produce a short "iOS follow-up" note with:
     - affected screens/modules (e.g. Amplifier, Stylus, Insights, Config)
     - required app-side updates
     - risk level (`low` / `medium` / `high`)

5. **Verification**
   - Run backend tests (`go test ./...` or `make test`).
   - Validate changed endpoints manually (or with existing tests).
   - Confirm no accidental contract drift remains.

## Suggested iOS impact template

```md
## iOS follow-up (oceano-player-ios)

- Scope:
  - [ ] API payload changes:
  - [ ] Config schema/default changes:
  - [ ] Playback/state semantic changes:

- Affected app modules:
  - [ ] Now Playing
  - [ ] Config
  - [ ] Amplifier
  - [ ] Stylus
  - [ ] Insights
  - [ ] Other:

- Actions required:
  - 1)
  - 2)

- Risk:
  - [ ] low
  - [ ] medium
  - [ ] high
```

## Rule of thumb for agents

If you changed backend behavior and did not explicitly evaluate iOS impact, the task is incomplete.
