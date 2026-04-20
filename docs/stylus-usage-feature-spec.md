# Feature Specification: Stylus Usage and Wear Tracking

## Status
Draft for validation

## Summary
Add a new feature to track turntable stylus usage in hours based on already recorded vinyl listening time.

The user can:
- Enable/disable stylus tracking in the web UI
- Select a stylus model from a curated catalog of popular models
- Enter a custom stylus if their model is not listed
- Set initial consumed hours (or mark stylus as new)

The system will:
- Aggregate vinyl listening hours
- Estimate stylus wear progression against model lifetime specs
- Expose stylus health metrics for dashboards and alerts

This feature is incremental and low-risk because the project already tracks listening history and listened seconds for playback sessions.

## Goals
- Provide practical maintenance insight: estimated stylus wear in hours and percentage
- Reuse existing vinyl listening telemetry (no new audio capture complexity)
- Keep initial UX simple and explicit
- Support both catalog models and custom user-defined models
- Prepare foundation for future advanced metrics and recommendations

## Non-Goals (v1)
- Automatic stylus model detection
- Per-record wear correction by groove condition
- ML-based wear prediction
- Hard enforcement logic (feature is advisory, not blocking)

## User Stories
1. As a user, I want to enable stylus tracking so I can monitor usage over time.
2. As a user, I want to choose my stylus from popular models.
3. As a user, I want to add a custom stylus model and its estimated lifetime.
4. As a user, I want to tell the system if my stylus is new or already used.
5. As a user, I want to see wear progress and estimated remaining hours.
6. As a user, I want warning states when stylus usage approaches/exceeds estimated lifetime.

## UX Proposal

## Placement
Recommended placement: Amplifier Settings. Without the amplifier path active, no vinyl listening lifecycle exists in this system, so stylus setup should live in the same configuration domain.

Suggested navigation:
- Add a new section/tab inside Amplifier Settings: `Stylus` (or `Needle`)
- Keep a compact read-only stylus status card on the History page summary

## Main Screen: Amplifier Settings > Stylus Tracking
Sections:
- Feature toggle: `Enable stylus wear tracking`
- Active stylus selector:
  - `Choose from catalog`
  - `Use custom stylus`
- Initial usage setup:
  - `Stylus is new` (sets initial hours to 0)
  - `Stylus already used` + numeric field `Initial used hours`
- Current metrics:
  - Total stylus hours
  - Estimated lifetime hours
  - Remaining hours
  - Wear percentage with progress bar
  - State badge: `Healthy`, `Plan replacement`, `Replace soon`, `Overdue`
- Actions:
  - `Replace stylus now` (closes current lifecycle and starts a new one)
  - `Adjust initial hours` (admin correction)

UX behavior notes:
- If amplifier control is not configured, show stylus section in disabled state with setup guidance.
- If stylus tracking is disabled, hide warnings from global status surfaces and keep data intact.

## Icons and Visual Direction
Use consistent iconography with a cartridge/stylus visual language:
- Main feature icon: cartridge body + cantilever + tip
- States:
  - Healthy: stylus icon + green ring
  - Warning: stylus icon + amber ring
  - Critical: stylus icon + red ring

Suggested icon sources:
- Phosphor Icons: needle/record metaphors
- Tabler Icons: maintain line style consistency
- Custom inline SVG for cartridge silhouette if no exact icon exists

UI note: keep icon set monochrome with semantic accent colors to match current design system.

## Data Model

## New Table: `stylus_catalog`
Stores curated popular stylus models and normalized lifetime estimates.

```sql
CREATE TABLE IF NOT EXISTS stylus_catalog (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  brand TEXT NOT NULL,
  model TEXT NOT NULL,
  stylus_profile TEXT NOT NULL,         -- Conical | Elliptical | Nude Elliptical | Fine Line | Shibata | MicroLine | Other
  min_hours INTEGER,                    -- nullable when unknown
  max_hours INTEGER,                    -- nullable when unknown
  recommended_hours INTEGER NOT NULL,   -- primary number used by UI and wear logic
  source_name TEXT NOT NULL,
  source_url TEXT NOT NULL,
  source_note TEXT NOT NULL,
  confidence TEXT NOT NULL DEFAULT 'medium',  -- low | medium | high
  is_active INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(brand, model)
);
```

## New Table: `stylus_profiles`
Stores user-specific selected stylus setup and lifecycle context.

```sql
CREATE TABLE IF NOT EXISTS stylus_profiles (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  enabled INTEGER NOT NULL DEFAULT 0,
  catalog_id INTEGER,                   -- nullable when custom
  brand TEXT NOT NULL,
  model TEXT NOT NULL,
  stylus_profile TEXT NOT NULL,
  lifetime_hours INTEGER NOT NULL,
  initial_used_hours REAL NOT NULL DEFAULT 0,
  installed_at TEXT NOT NULL,           -- when this stylus lifecycle started
  replaced_at TEXT,                     -- nullable for active stylus
  notes TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (catalog_id) REFERENCES stylus_catalog(id)
);
```

## New Table: `stylus_usage_snapshots`
Optional but recommended for historical analytics and timeline charting.

```sql
CREATE TABLE IF NOT EXISTS stylus_usage_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  stylus_profile_id INTEGER NOT NULL,
  measured_at TEXT NOT NULL,
  vinyl_hours_total REAL NOT NULL,
  stylus_hours_total REAL NOT NULL,
  wear_percent REAL NOT NULL,
  remaining_hours REAL NOT NULL,
  state TEXT NOT NULL,                  -- healthy | plan | soon | overdue
  FOREIGN KEY (stylus_profile_id) REFERENCES stylus_profiles(id)
);
```

## Source of Truth for Hours
Primary source: existing `play_history.listened_seconds` filtered to vinyl media.

Recommended SQL aggregation:

```sql
SELECT COALESCE(SUM(listened_seconds), 0) / 3600.0
FROM play_history
WHERE LOWER(COALESCE(media_format, '')) = 'vinyl';
```

For active stylus lifecycle only:

```sql
SELECT COALESCE(SUM(listened_seconds), 0) / 3600.0
FROM play_history
WHERE LOWER(COALESCE(media_format, '')) = 'vinyl'
  AND started_at >= ?; -- stylus_profiles.installed_at
```

## Wear Computation
Given:
- `H_initial` = `initial_used_hours`
- `H_since_install` = aggregated vinyl hours since `installed_at`
- `H_lifetime` = configured `lifetime_hours`

Compute:
- `H_total = H_initial + H_since_install`
- `H_remaining = max(H_lifetime - H_total, 0)`
- `P_wear = min((H_total / H_lifetime) * 100, 999)`

State thresholds (v1):
- `healthy`: `< 70%`
- `plan`: `>= 70% and < 90%`
- `soon`: `>= 90% and <= 100%`
- `overdue`: `> 100%`

## API Additions (cmd/oceano-web)

## GET `/api/stylus`
Returns active stylus config and computed metrics.

```json
{
  "enabled": true,
  "stylus": {
    "id": 3,
    "brand": "Ortofon",
    "model": "2M Blue",
    "stylus_profile": "Nude Elliptical",
    "lifetime_hours": 800,
    "initial_used_hours": 120,
    "installed_at": "2026-04-20T10:00:00Z",
    "is_custom": false
  },
  "metrics": {
    "vinyl_hours_since_install": 84.5,
    "stylus_hours_total": 204.5,
    "remaining_hours": 595.5,
    "wear_percent": 25.56,
    "state": "healthy"
  }
}
```

## PUT `/api/stylus`
Upserts stylus settings for active lifecycle.

## POST `/api/stylus/replace`
Closes current stylus lifecycle (`replaced_at`) and creates a new active one.

## GET `/api/stylus/catalog`
Returns curated list from `stylus_catalog`.

## Data Seeding Strategy
At service startup or migration step:
- Create `stylus_catalog` if missing
- Seed known rows with idempotent upsert (`brand, model` uniqueness)
- Keep `source_name`, `source_url`, and `confidence`

## Initial Catalog (Seed v1)
Note: Public, machine-extractable model-level lifetime data is sparse. Seed v1 combines direct model-level statements where available plus conservative profile-based defaults for popular models.

### Direct model-level hour source
1. LP Gear CFN3600LE
- Brand: LP Gear / Audio-Technica-compatible
- Model: CFN3600LE
- Stylus profile: Elliptical
- Lifetime: 500-1000 h (recommended 750 h)
- Source: LP Gear product page
- URL: https://www.lpgear.com/product/LPGCFN3600LE.html
- Confidence: high

### Referenced profile/model context source
2. Denon DL-103
- Brand: Denon
- Model: DL-103
- Stylus profile: Conical/Spherical
- Lifetime reference used for conservative cap in v1: 500 h
- Source note: Stereophile references the spherical stylus profile context used by many users for lower-life assumptions
- URL: https://www.stereophile.com/content/denon-dl-103-phono-cartridge
- Confidence: medium

### Popular model seed rows (profile-based recommended hours, editable)
These rows are intentionally marked `confidence=low` until replaced by explicit manufacturer-hour statements.

| Brand | Model | Stylus profile | Min | Max | Recommended |
|---|---|---|---:|---:|---:|
| Audio-Technica | AT-VM95C | Conical | 300 | 600 | 500 |
| Audio-Technica | AT-VM95E | Elliptical | 300 | 700 | 600 |
| Audio-Technica | AT-VM95EN | Nude Elliptical | 500 | 1000 | 800 |
| Audio-Technica | AT-VM95ML | MicroLine | 800 | 1200 | 1000 |
| Audio-Technica | AT-VM95SH | Shibata | 700 | 1200 | 1000 |
| Ortofon | 2M Red | Elliptical | 300 | 700 | 600 |
| Ortofon | 2M Blue | Nude Elliptical | 500 | 1000 | 800 |
| Ortofon | 2M Bronze | Fine Line | 700 | 1200 | 1000 |
| Ortofon | 2M Black | Shibata | 700 | 1200 | 1000 |
| Nagaoka | MP-110 | Elliptical | 300 | 700 | 600 |
| Nagaoka | MP-150 | Elliptical | 300 | 700 | 600 |
| Sumiko | Oyster Rainier | Elliptical | 300 | 700 | 600 |
| Sumiko | Oyster Moonstone | Elliptical | 300 | 700 | 600 |
| Goldring | E3 | Elliptical | 300 | 700 | 600 |
| Rega | Nd3 | Elliptical | 300 | 700 | 600 |
| Grado | Prestige Blue3 | Elliptical | 300 | 700 | 600 |
| Denon | DL-103 | Conical | 300 | 500 | 500 |

## Why this seed is acceptable for v1
- It gives users immediate value and a complete UX flow
- Every row is transparent about confidence
- Rows can be corrected/upgraded without schema changes
- Future curation is a data operation, not a code rewrite

## Admin Curation Workflow (v1.1)
Add internal/admin flow to:
- Edit catalog lifetime numbers
- Update source URL and confidence
- Add/remove models
- Export/import catalog JSON for community-maintained updates

## Metrics and Future Enrichment
Once feature is live, expose stylus analytics in two places:
- Amplifier Settings > Stylus (full control and lifecycle actions)
- History dashboard (read-only summary card)

Amplifier Settings > Stylus metrics:
- Active stylus hours
- Wear progression (7d/30d trend)
- Estimated date to 90% and 100% wear based on recent listening pace
- Lifetime totals per stylus replacement cycle

Potential additional metrics:
- Average monthly vinyl hours
- Projected replacement date
- Replacement frequency trend

## Migration Plan
1. Add DB migrations for three new tables.
2. Seed `stylus_catalog` with initial rows.
3. Add API handlers in `cmd/oceano-web`.
4. Add Amplifier Settings > Stylus section and History summary card.
5. Add tests:
   - Aggregation from `play_history`
   - Wear threshold classification
   - Config upsert and lifecycle replacement flow

## Testing Plan
- Unit tests for wear calculation function and thresholds
- Integration test for SQL aggregation using fixture `play_history` rows
- API tests for `GET/PUT /api/stylus`, `POST /api/stylus/replace`, `GET /api/stylus/catalog`
- Frontend tests for form defaults and progress-state rendering

## Backward Compatibility
- Feature is opt-in (`enabled=0` by default)
- No change to existing playback detection pipeline
- Existing history endpoints remain unchanged

## Open Decisions for Validation
1. Label naming: `Stylus` vs `Needle` in Amplifier Settings.
2. Thresholds: keep 70/90/100 defaults or make user-configurable.
3. Whether `stylus_usage_snapshots` is needed in v1 or deferred.
4. Whether custom model creation should require source URL (recommended optional).

## Security and Privacy
- No new external telemetry required
- No personal data beyond existing local usage history
- All calculations remain local in existing SQLite-backed architecture

## Implementation Readiness
This feature is implementation-ready after validation of:
- Label naming in Amplifier Settings
- Threshold defaults
- Seed catalog policy (profile-based fallback accepted in v1)
