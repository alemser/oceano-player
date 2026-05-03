# Code review notes — lightweight HTTP / library sync branch

**Purpose:** Annotations from an internal review of the `ios-performance-changes` work (e.g. commits around `b53f6a5`, `8581112`). **Not a release gate:** items below are fact-checked and reclassified where the original write-up was wrong or overstated.

**Canonical contract:** [`docs/reference/http-lightweight-clients.md`](reference/http-lightweight-clients.md)  
**Cross-repo log:** [`docs/cross-repo-sync.md`](cross-repo-sync.md)

---

## What actually shipped (high level)

| Area | Notes |
|------|--------|
| `oceano-web` | `/api/player/summary`, `GET /api/library` ETag/304, `GET /api/library/changes`, SSE `event: library`, strip top-level `vu` unless `?vu=1`, `GET /api/config` ETag, recognition POST restart dedup (earlier commit). |
| `oceano-state-manager` | `PlayerState.vu` (`VuLevels`) + throttled `markDirty` from VU socket — **no** `library_version` in unified state JSON. |
| `internal/library` | Migrations: `oceano_library_sync`, `library_changelog`, triggers on `collection`, baseline backfill, `Library()` `LibraryVersion()`. |
| `oceano-web` + `library_sync_web.go` | Same DDL applied in `openLibraryDB` so web-only opens get triggers before first writer. |
| Docs | `http-lightweight-clients.md`, `cross-repo-sync`, README/AGENTS/CLAUDE, `state-detection.md`, optional `backend-recognition-providers-contract.md` mirror. |

---

## Fact-check vs original review claims

### Original: “Critical — race if baseline runs before triggers”

**Verdict:** Does not match the migration order in code.

Migrations (and `ensureLibrarySyncSchema`) create **`collection` triggers first**, then run the baseline:

`INSERT INTO library_changelog … SELECT 1, id, 'upsert' FROM collection WHERE NOT EXISTS (SELECT 1 FROM library_changelog LIMIT 1)`  
followed by syncing `oceano_library_sync.library_version` from `MAX(version)`.

So the failure mode “triggers created after baseline” is **not** the implemented order. Residual risk is only **partial / hand-patched DBs** (out of band DDL), not the normal migrate path.

**Annotation:** Keep as a *theoretical* ops note if someone applies DDL piecemeal; no mandatory code change for the happy path.

---

### Original: “Critical — `loadConfig` on every `/api/player/summary`”

**Verdict:** Valid **performance** observation, not a correctness bug.

`loadConfig` reads and unmarshals full `config.json` mainly to obtain `Advanced.StateFile`. Under 1–3 s polling this is extra I/O/CPU on the Pi.

**Annotation:** Optional follow-up — cache config by file `mtime`/`size`, or read state path from a slimmer source if the product wants to squeeze Pi CPU.

---

### Original: “Significant — `since_version == current` empty + header mismatch”

**Verdict:** `libraryChangesSince` returning empty deltas when `since_version >= current` is **intended** (`library_version` is still set on the response struct).

The “header might not match if `getLibraryVersion` fails” part was speculative; confirm in code if `lib == nil` paths omit `X-Oceano-Library-Version` on 200 responses — minor polish only.

**Annotation:** UX/doc only, not a protocol break.

---

### Original: “Significant — ETag vs `X-Oceano-Library-Version` mismatch”

**Verdict:** Mostly **theory**.

ETag is SHA-256 of the **JSON body** of the library list. Any `UPDATE` that bumps `library_version` normally changes at least one serialized column (e.g. `play_count`, `last_played`), so body and ETag move together.

**Edge case:** SQLite `AFTER UPDATE` trigger still fires if a write touches a row with **no effective value change**; then `library_version` could advance while marshalled `entries` bytes stay identical — rare.

**Annotation:** Optional hardening — fold `library_version` into the hashed bytes or document “prefer header + ETag together”; not required for typical flows.

---

### Original: “Medium — `rewriteStateJSONForClient` unmarshals whole state”

**Verdict:** Valid **micro-optimization** note.

Top-level `vu` removal via `map[string]json.RawMessage` allocates and re-marshals. Contract keeps `vu` top-level, so “nested vu” is out of scope.

**Annotation:** Optional faster path (byte strip, tokenizer, or strip in state-manager behind a flag) if profiling shows hot path.

---

### Original: “Medium — drop `updated_at` from summary”

**Verdict:** Product / payload size preference only.

**Annotation:** Keep unless iOS explicitly wants one fewer field; ETag already covers change detection.

---

### Original: “Medium — `SetMaxOpenConns(1)` defeats WAL”

**Verdict:** **Oversimplified.**

WAL still helps **across processes** (e.g. `oceano-state-manager` writer vs `oceano-web` reader). Inside a single `oceano-web` process, one connection serializes SQLite access and avoids `BUSY` surprises from concurrent statements on one `*sql.DB`.

**Annotation:** Raising max open conns is a **concurrency tradeoff**, not an automatic win — measure before changing.

---

## Incorrect lines in the earlier summary table (do not propagate)

The following were **wrong** in an earlier draft and are **not** in the codebase:

- `PlayerState` / unified state JSON does **not** include `library_version`.
- `source_vu_monitor` does **not** bump `library_version` (library triggers do).
- `LibraryVersion` is not a new field on `PlayerState` in `config_types.go`.

---

## Optional follow-ups (engineering backlog)

1. Mtime-based config cache for `/api/player/summary` state path resolution.  
2. Tests for `libraryChangesSince` (empty DB, `since_version=0` after backfill, delete-then-recreate same id, `since == current`).  
3. Pi smoke: SSE default without `vu`, `?vu=1`, `event: library`, summary 304, library 304/changes.  
4. If edge-case (2) in ETag section ever hits production, include `library_version` in ETag input.

---

## Compliance / verification notes

| Item | Note |
|------|------|
| `go test ./...` | Run in CI / pre-commit; update this line when verified on a given tag. |
| Pi / iOS | Manual validation of new endpoints still recommended before App Store / field rollouts. |
| Contract | Single source for HTTP semantics: `docs/reference/http-lightweight-clients.md`. |
