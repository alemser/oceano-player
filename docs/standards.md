# Engineering Standards (Agent Policy)

This file defines the software engineering standards expected in `oceano-player`.
Use it as a practical policy when implementing backend changes.

## 1) Preserve behavior unless change is explicit

- Prefer behavior-preserving refactors before functional rewrites.
- If behavior must change, state it clearly and document the user/runtime impact.
- Avoid hidden semantic drift in endpoint responses and state transitions.

## 2) No-regression discipline

- Run relevant tests for touched areas; run full repo tests for broad refactors.
- Treat failing tests as blockers; fix or revert before finishing.
- Keep compatibility paths for API/config contracts whenever possible.

## 3) Contract-first backend changes

- This repo is the backend contract owner.
- Assume downstream impact on `oceano-player-ios` for API/config/state changes.
- For any contract change, run `docs/cross-repo-sync.md` checklist.

## 4) Cohesion and loose coupling

- Organize code by responsibility (capture, detection, recognition, persistence, output).
- Avoid implicit coupling between components through shared hidden assumptions.
- Keep boundaries explicit: narrow interfaces, clear payloads, clear ownership.

## 5) Simplicity over over-engineering

- Use standard library and straightforward designs by default.
- Introduce abstractions only when they reduce real complexity.
- Prefer incremental extraction over large structural rewrites.

## 6) Raspberry Pi operational reliability first

- Prioritize long-running stability and predictable retry/backoff behavior.
- Never block critical audio loops on slow downstream consumers.
- Preserve atomic write patterns for state output and robust restart behavior.

## 7) Documentation must ship with changes

- Keep `README.md`, `CLAUDE.md`, and install/help text aligned with behavior.
- If an architecture/workflow statement becomes stale, update it in the same change set.
- Do not leave docs for "later" when user-visible behavior changed now.

## 8) Migration and compatibility expectations

- Prefer additive fields to breaking renames/removals.
- If breaking is unavoidable, provide migration notes and compatibility fallback when feasible.
- Call out removed defaults/flags explicitly in docs and release notes.

## 9) Agent completion criteria

A backend task is only complete when all are true:

1. Code change is implemented and verified.
2. Tests/lints relevant to the change pass.
3. Docs are updated for any behavior/config/contract changes.
4. Cross-repo impact (`oceano-player-ios`) is evaluated and documented.
