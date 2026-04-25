# Oceano Player — Technical Assessment and Incremental Refactor Roadmap

## Context

This document consolidates:

- A concise technical assessment of the current project state.
- An incremental, behavior-preserving refactor plan in 3 small PRs.
- Execution strategy and regression checklist.
- A ready-to-use issue-style roadmap with actionable checkboxes.

The goal is to improve maintainability, testability, and long-term reliability without changing runtime behavior.

---

## Technical Assessment

### Overall opinion

The project is strong and already close to real-world production operation on Raspberry Pi. Architecture boundaries are mostly clear, operational concerns are well covered, and there is visible engineering discipline in testing and deployment.

### Architecture summary

The system follows a practical 3-binary design:

- `cmd/oceano-source-detector`: physical/none source detection + VU + PCM relay.
- `cmd/oceano-state-manager`: state aggregation + recognition orchestration + persistence.
- `cmd/oceano-web`: config UI + status API + SSE stream + now playing page.

Supporting domain packages (`internal/recognition`, `internal/library`, `internal/amplifier`) keep important logic separated from entrypoints.

### Strengths observed

- Operational reliability mindset (atomic writes, systemd integration).
- Recognition pipeline with fallback/backoff and practical orchestration.
- Good test presence in critical parts.
- Config model is rich and explicit.
- End-to-end deployment workflow is realistic for Pi environments.

### Risks and technical debt

- Large, multi-responsibility files increase change risk and review cost.
- Runtime coupling to external commands/environment reduces unit testability.
- SQLite migration history needs extra care for long-term schema evolution.
- Some defaults appear environment-specific rather than neutral.
- Untracked helper file indicates possible partially staged work (`cmd/oceano-web/static/recognition/helpers.js`).

### Maturity level

**Early production / advanced beta**.

The project appears operationally useful today, with clear opportunities to improve structure and maintainability incrementally.

---

## Refactor Plan (3 Small PRs)

## PR 1 — Split large entrypoint files by responsibility (no behavior changes)

### Goal

Improve readability and maintainability by extracting cohesive units from large `main.go` files while preserving current behavior.

### Scope

- `cmd/oceano-web/main.go`
  - Extract HTTP handlers into dedicated files:
    - `handlers_status.go`
    - `handlers_config.go`
    - `handlers_stream.go`
    - `handlers_devices.go`
  - Keep `main.go` focused on bootstrap, wiring, and route registration.
- `cmd/oceano-state-manager/main.go`
  - Extract watchers/loops into dedicated files by concern:
    - source polling
    - VU monitoring
    - metadata ingestion
    - output writing
- Optional small utility extractions where obvious (`state_io.go`, `timeouts.go`, etc.).

### Acceptance criteria

- No change to flags, endpoints, JSON contracts, or runtime behavior.
- Existing tests remain green.
- Add small smoke tests for extracted handlers where currently missing.

### Risk / impact

- **Risk:** Low
- **Impact:** High

---

## PR 2 — External command adapters and dependency injection

### Goal

Reduce environment coupling and increase testability by introducing narrow interfaces around shell/system operations.

### Scope

- Add interfaces such as:
  - `CommandRunner` (exec + output + timeout handling)
  - `ServiceManager` (system service restart/status)
  - `BluetoothController` (scan/list/connect/disconnect)
- Keep current production behavior by providing default concrete adapters that still call existing commands.
- Inject adapters into `oceano-web` code paths currently making direct external calls.
- Add fake implementations for deterministic unit tests.

### Acceptance criteria

- Runtime behavior remains unchanged in production.
- New unit tests cover success and error branches without requiring host system commands.
- Error handling/logging remains clear and actionable.

### Risk / impact

- **Risk:** Low to Medium
- **Impact:** High

---

## PR 3 — Persistence/config hygiene and docs sync

### Goal

Stabilize long-term maintainability of schema/config and ensure docs/scripts stay consistent with implementation.

### Scope

- `internal/library` (SQLite):
  - Normalize migration flow for idempotence and auditable ordering.
  - Remove or isolate ambiguous/destructive migration steps in historical chain where possible.
  - Document expected schema version/strategy.
- `cmd/oceano-web/config.go`:
  - Review defaults and prefer neutral safe defaults.
  - Keep environment-specific values as optional presets/template paths.
- Documentation and scripts:
  - Update `README.md` and `CLAUDE.md` whenever behavior/flags/workflow are touched.
  - Ensure installer `--help` output matches accepted flags.

### Acceptance criteria

- No regressions in existing library read/write behavior.
- Migrations remain safe for upgrades from prior versions (where applicable).
- Documentation and scripts are aligned with current behavior.

### Risk / impact

- **Risk:** Medium
- **Impact:** High

---

## Execution Strategy

- Recommended order: **PR1 -> PR2 -> PR3**
- Keep each PR thematic and reviewable (avoid multi-theme mega PRs).
- Preserve behavior during refactor; any functional changes should go to separate PRs.
- In each PR:
  - run package-level tests for touched code;
  - run full repository test suite before merge;
  - do a quick manual verification of critical endpoints/flows.

---

## Regression Checklist (Run in every PR)

- [ ] Unified state publishing still works correctly.
- [ ] SSE stream still sends updates and keepalive.
- [ ] Source transition behavior remains unchanged.
- [ ] Recognition trigger and track update flow remain unchanged.
- [ ] Config save flow still writes config and triggers service restart path.
- [ ] No installer flag/help mismatch introduced.
- [ ] No docs drift introduced for changed behavior.

---

## Issue-Ready Roadmap (Copy/Paste Template)

## Title

Incremental maintainability refactor (3 PRs, behavior-preserving)

## Description

This roadmap improves maintainability and testability without changing runtime behavior. Delivery is split into 3 small reviewable PRs.

## Deliverables

### PR 1 — File decomposition (entrypoints)

- [ ] Extract web handlers from `cmd/oceano-web/main.go` into dedicated files.
- [ ] Keep `main.go` focused on bootstrap/wiring/routes.
- [ ] Extract state-manager loops/watchers from `cmd/oceano-state-manager/main.go` by concern.
- [ ] Add/update smoke tests for extracted handlers/paths as needed.
- [ ] Confirm zero behavior/API contract changes.

### PR 2 — External command adapters

- [ ] Introduce `CommandRunner` abstraction and default implementation.
- [ ] Introduce service control abstraction for restart/status calls.
- [ ] Introduce Bluetooth command abstraction for web control paths.
- [ ] Inject abstractions into call sites currently using direct command execution.
- [ ] Add unit tests using fakes for success/error cases.
- [ ] Confirm production behavior parity.

### PR 3 — SQLite/config/docs hygiene

- [ ] Normalize SQLite migration flow for idempotence and auditable sequence.
- [ ] Review migration safety for existing users/upgrades.
- [ ] Replace environment-specific defaults with neutral safe defaults where appropriate.
- [ ] Keep any specific calibration/preset values as optional templates.
- [ ] Sync `README.md` and `CLAUDE.md` with changed workflows/flags.
- [ ] Verify installer script `--help` output remains accurate.

## Definition of Done

- [ ] All existing tests pass.
- [ ] New tests added for newly isolated seams.
- [ ] Manual checks for `/api/status`, `/api/stream`, and config save/restart path pass.
- [ ] No external behavior regression observed.
- [ ] PRs are small, focused, and independently mergeable.

---

## Notes

- Prefer behavior-preserving refactors before feature work.
- Avoid mixing unrelated functional changes into these PRs.
- If runtime behavior must change, open a separate scoped PR with explicit migration notes.
