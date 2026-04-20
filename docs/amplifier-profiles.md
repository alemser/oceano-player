# Amplifier Profiles: Design Proposal

## Purpose

This document proposes a profile system for amplifier behavior in Oceano Player, with:

- Built-in profiles (starting with Magnat MR 780)
- User-created custom profiles
- JSON import/export for profile portability
- Explicit modeling of amplifier inputs
- Support for both cycle-based and direct input IR control

The goal is to make amplifier integration data-driven, reduce model-specific logic in code, and keep behavior predictable on Raspberry Pi deployments.

## Scope

This proposal targets the amplifier control and detection layer used by `oceano-web` and `internal/amplifier`.

In scope:

- Profile catalog and active profile selection
- Input definitions and input control strategy
- IR command layout (cycle mode and per-input direct mode)
- Import/export API and JSON schema
- Validation and migration rules

Out of scope:

- Generic media source detection changes (`AirPlay`, `Physical`, etc.)
- UI redesign beyond adding profile management screens

## Profile Types

### Built-in profile

- Shipped with the app
- Read-only baseline
- Example: `magnat_mr780`

### Custom profile

- Created by user from scratch or by cloning a built-in profile
- Fully editable
- Stored in local config storage

### Derived profile

- A custom profile cloned from a built-in profile
- Keeps user-specific tuning without changing upstream default

## Core Concepts

### 1) Active profile

Only one amplifier profile is active at runtime.

- `active_profile_id` is resolved to one profile definition
- Runtime builder consumes the resolved profile to construct amplifier behavior

### 2) Input strategy

Each profile declares one input control strategy:

- `cycle`: amplifier navigates inputs using next/prev IR commands
- `direct`: amplifier supports dedicated IR command per input (for example, `USB`, `CD`, `AUX`)

### 3) Inputs list

A profile may include a pre-defined input list or let users define it.

- Built-in profile can ship with known input names/order
- User can rename/reorder/add/remove inputs in custom profile

Input modeling rules:

- Each input has a logical user-facing name (`logical_name`)
- Each input has a hidden internal ID (`id`) used by runtime logic
- Internal ID can be numeric or string and does not need to match array index
- All physical amplifier inputs must be registered in the profile
- A `visible` flag controls whether an input is shown in UI selectors
- Input at index `0` is treated as the default input

This supports the requirement that inputs can either come from the profile or be user-registered.

## Data Model (Proposed)

```json
{
  "schema_version": "1.0",
  "active_profile_id": "magnat_mr780",
  "profiles": [
    {
      "id": "magnat_mr780",
      "name": "Magnat MR 780",
      "origin": "builtin",
      "maker": "Magnat",
      "model": "MR 780",
      "input_mode": "cycle",
      "inputs": [
        { "id": 10, "logical_name": "Phono", "visible": false },
        { "id": 20, "logical_name": "CD", "visible": true },
        { "id": 30, "logical_name": "Aux", "visible": true },
        { "id": 40, "logical_name": "USB Audio", "visible": true }
      ],
      "ir": {
        "common": {
          "power_on": "...",
          "power_off": "...",
          "volume_up": "...",
          "volume_down": "..."
        },
        "cycle": {
          "next_input": "...",
          "prev_input": "..."
        }
      },
      "timings": {
        "warm_up_secs": 30,
        "standby_timeout_mins": 20,
        "usb_reset_max_attempts": 13,
        "usb_reset_first_step_settle_ms": 150,
        "usb_reset_step_wait_ms": 2400,
        "input_cycling_enabled": true,
        "input_cycling_direction": "prev",
        "input_cycling_max_cycles": 8,
        "input_cycling_step_wait_secs": 3,
        "input_cycling_min_silence_secs": 120
      },
      "detection": {
        "usb_match": "MR 780"
      }
    }
  ]
}
```

## Mini JSON Schema (Input + Profile Core)

The snippet below is a compact schema intended for import validation.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://oceano-player.local/schema/amplifier-profile.v1.json",
  "type": "object",
  "required": ["schema_version", "profile"],
  "properties": {
    "schema_version": { "const": "1.0" },
    "profile": {
      "type": "object",
      "required": ["id", "name", "maker", "model", "input_mode", "inputs", "ir"],
      "properties": {
        "id": { "type": "string", "minLength": 1 },
        "name": { "type": "string", "minLength": 1 },
        "maker": { "type": "string", "minLength": 1 },
        "model": { "type": "string", "minLength": 1 },
        "input_mode": { "enum": ["cycle", "direct"] },
        "inputs": {
          "type": "array",
          "minItems": 1,
          "items": {
            "type": "object",
            "required": ["id", "logical_name", "visible"],
            "properties": {
              "id": {
                "oneOf": [
                  { "type": "string", "minLength": 1 },
                  { "type": "integer" }
                ]
              },
              "logical_name": { "type": "string", "minLength": 1 },
              "visible": { "type": "boolean" }
            },
            "additionalProperties": false
          }
        },
        "ir": {
          "type": "object",
          "required": ["common"],
          "properties": {
            "common": {
              "type": "object",
              "properties": {
                "power_on": { "type": "string" },
                "power_off": { "type": "string" },
                "volume_up": { "type": "string" },
                "volume_down": { "type": "string" }
              }
            },
            "cycle": {
              "type": "object",
              "properties": {
                "next_input": { "type": "string" },
                "prev_input": { "type": "string" }
              }
            },
            "direct": {
              "type": "object",
              "required": ["select_input"],
              "properties": {
                "select_input": {
                  "type": "object",
                  "additionalProperties": { "type": "string" }
                }
              }
            }
          }
        }
      },
      "additionalProperties": true
    }
  },
  "additionalProperties": false
}
```

Semantic rules applied in code (in addition to schema):

1. Every real amplifier input must be present in `inputs`
2. `inputs[0]` is the default input
3. Input IDs must be unique after normalization to string
4. `direct.select_input` must cover all registered inputs (including hidden)
5. `visible=false` affects UI only, never runtime routing logic

## IR Command Model

### Shared/common IR commands

Used in both input modes:

- `power_on`
- `power_off`
- `volume_up`
- `volume_down`

Optional extras can be added later (mute, power_toggle).

### Cycle mode IR commands

Required when `input_mode = cycle`:

- `next_input` and/or `prev_input`

Cycle mode assumes the current input is reached by stepping through input order.

### Direct mode IR commands (per input)

Required when `input_mode = direct`:

- One IR code per input target
- Keys reference input IDs

Example:

```json
{
  "input_mode": "direct",
  "inputs": [
    { "id": 100, "logical_name": "USB Audio", "visible": true },
    { "id": 200, "logical_name": "CD", "visible": true },
    { "id": 300, "logical_name": "Aux", "visible": false }
  ],
  "ir": {
    "common": {
      "power_on": "...",
      "power_off": "...",
      "volume_up": "...",
      "volume_down": "..."
    },
    "direct": {
      "select_input": {
        "100": "IR_CODE_USB",
        "200": "IR_CODE_CD",
        "300": "IR_CODE_AUX"
      }
    }
  }
}
```

This satisfies the requirement that non-cycle amplifiers can have IR per input.

## Input Definition Rules

### Built-in defaults

- Built-in profiles may include known factory input list and ordering
- This improves out-of-box behavior for known models
- Built-in input order must reflect real amplifier navigation order
- Input at array index `0` is the default/fallback input

### User-managed input list

Users can override inputs in custom profiles:

- Add a new input ID/label
- Reorder inputs
- Remove unused inputs
- Toggle `visible` to hide rarely used inputs from UI without removing runtime mapping

Operational requirement:

- Do not omit real hardware inputs from profile registration
- Hidden inputs still participate in cycle traversal and direct IR selection

Validation:

- Input IDs must be unique
- Input IDs are internal and must not be shown as UI label
- Input ID may be numeric or string, independent from position index
- `logical_name` is required and is the only user-facing identifier
- At least one input is required
- Input at index `0` is considered default and must always exist
- Direct mode requires a mapped IR code for each registered input ID (including hidden ones)
- Cycle mode requires complete ordered input list to keep traversal deterministic

## Import / Export

### Export

Support exporting one profile as JSON:

- `safe` export (default): excludes sensitive Broadlink credentials
- `full` export (optional): includes credentials only with explicit user action

Sensitive values to exclude by default:

- Broadlink token
- Broadlink device_id

### Import

Import flow:

1. Upload profile JSON
2. Validate schema/version/required fields
3. Validate input mode consistency (`cycle` vs `direct`)
4. Resolve ID conflict

ID conflict policies:

- `reject`
- `overwrite`
- `rename` (generate new ID)

### JSON Schema Versioning

`schema_version` is mandatory. Importer must:

- Accept known versions
- Run migration adapters for older versions
- Reject unsupported future major versions

## Security and Reliability

- Enforce max import size (for example, 256 KB)
- Strict JSON parsing (reject unknown critical fields if needed)
- Never execute imported content
- Normalize line endings and whitespace
- Keep atomic write semantics (`tmp` + `rename`)

## Runtime Resolution

At startup and when applying profile updates:

1. Load profile store
2. Resolve `active_profile_id`
3. Merge optional local overrides
4. Build `AmplifierSettings`
5. Start/refresh power monitor

If profile resolution fails:

- Keep amplifier API disabled
- Return explicit error in logs/UI
- Do not impact source detector or state manager behavior

## Suggested API Endpoints

- `GET /api/amplifier/profiles`
- `POST /api/amplifier/profiles` (create)
- `PUT /api/amplifier/profiles/{id}` (update)
- `DELETE /api/amplifier/profiles/{id}` (delete custom only)
- `POST /api/amplifier/profiles/{id}/activate`
- `POST /api/amplifier/profiles/import`
- `GET /api/amplifier/profiles/{id}/export?mode=safe|full`

## UI Flow (Proposed)

1. Profile selector in Amplifier section
2. "New profile" and "Clone profile" actions
3. Input editor:
  - hidden internal ID + logical_name + visible flag + order
   - Mode toggle: `cycle` or `direct`
4. IR editor:
   - Common commands table
  - In direct mode, per-input IR mapping table (all registered inputs, including hidden)
  - In cycle mode, order preview with default input highlighted (index `0`)
5. Import/Export buttons
6. Validation summary before save/activate

## Backward Compatibility

Existing config fields remain supported during migration.

Migration strategy:

1. Read legacy `amplifier` fields
2. Generate implicit custom profile from those fields
3. Set it as `active_profile_id`
4. Preserve behavior exactly

This keeps current installations working without manual reconfiguration.

## Minimal Delivery Plan

### Phase 1

- Add profile storage model and active profile selection
- Ship built-in `magnat_mr780`
- Resolve active profile in amplifier builder

### Phase 2

- Add profile CRUD APIs
- Add import/export APIs
- Add validation and migration framework

### Phase 3

- Add UI for profile management and input/IR editing
- Add guided validation checks (power, USB reset, input switching)

## Open Decisions

- Whether built-in profiles are hardcoded in Go or loaded from embedded JSON
- Whether direct mode should support optional fallback cycling when per-input IR fails
- Whether safe export should include host IP or redact all transport details
