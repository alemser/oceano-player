# Local Audio Fingerprint Cache for Physical-Media Recognition

## Problem

Every time a physical track is played, the system performs a full ACRCloud lookup
even if the track has been recognized before. This wastes API quota, adds latency,
and fails silently for albums that are not in the ACRCloud database — leaving the
user with no way to attach metadata to those tracks.

## Goal

Generate and store local audio fingerprints so the system can identify a track
from the local database before querying ACRCloud, and so users can manually
annotate tracks that ACRCloud does not know about.

## Proposed Behavior

1. On every recognition attempt, capture a WAV segment and generate **multiple
   fingerprints** from overlapping windows within that segment using `fpcalc -raw`.

2. Before querying ACRCloud, run a **sliding-window BER (bit error rate)** search
   against all fingerprints in the local database.
   - If a match is found **and** the entry has user-confirmed metadata
     (`user_confirmed = 1`), skip ACRCloud and use the stored metadata.
   - Otherwise, fall through to ACRCloud.

3. If ACRCloud identifies the track, store the result plus all fingerprints
   generated from this capture. Set `user_confirmed = 1`.

4. If ACRCloud returns no match, still store the fingerprints as a stub entry
   with empty metadata and `user_confirmed = 0`. The user can then open the
   library editor and fill in title, artist, album, etc. Once saved, the entry
   is promoted to `user_confirmed = 1` and future plays skip ACRCloud entirely.

5. On a local fingerprint hit where `user_confirmed = 0` (stub not yet edited),
   fall through to ACRCloud as usual — the track may be recognized on a later
   attempt.

## Similarity Matching

Exact fingerprint comparison is not reliable for analog captures due to timing
jitter and vinyl wow & flutter. The implementation must use **sliding-window BER**:

```
for shift in [-S, +S]:
    BER(shift) = average bit differences between a[0..] and b[shift..]
result = min(BER across all shifts)
match  = result < threshold (default: 0.35, same as AcoustID)
```

Multiple stored fingerprints per track (captured at different offsets within the
same WAV) increase the probability that a future capture overlaps with at least
one stored window, directly compensating for timing jitter.

Expected hit rates with sliding-window BER and multiple fingerprints:

| Source   | Condition        | Hit rate |
|----------|------------------|----------|
| CD       | Normal           | > 90%    |
| Vinyl    | Good setup       | ~75–80%  |
| Vinyl    | Worn/noisy       | ~50–60%  |

Misses always fall back to ACRCloud gracefully — no regressions.

## Database Schema Changes

New table for fingerprints (multiple per track):

```sql
CREATE TABLE fingerprints (
    id       INTEGER PRIMARY KEY,
    entry_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
    data     TEXT    NOT NULL  -- comma-separated uint32 values from fpcalc -raw
);
CREATE INDEX fingerprints_entry_id ON fingerprints(entry_id);
```

New column on `collection`:

```sql
ALTER TABLE collection ADD COLUMN user_confirmed INTEGER NOT NULL DEFAULT 0;
```

`user_confirmed = 1` means either ACRCloud identified the track or the user has
manually filled in the metadata. Only entries with `user_confirmed = 1` cause
ACRCloud to be skipped.

Existing rows default to `user_confirmed = 0` — no behaviour change on upgrade.

## Recognition Flow (updated)

```
capture WAV (existing RecognizerCaptureDuration)
    │
    ▼
generate N fingerprints at offsets 0, stride, 2×stride, …
    │
    ▼
sliding-window BER scan over local fingerprints table
    │
    ├── hit + user_confirmed=1 ──► use local metadata, done
    │
    └── miss or user_confirmed=0
            │
            ▼
        ACRCloud lookup
            │
            ├── match ──► store/update entry (user_confirmed=1) + store fingerprints
            │
            └── no match ──► upsert stub entry (user_confirmed=0) + store fingerprints
                             (user can edit in library UI to promote to user_confirmed=1)
```

## Implementation Components

### `Fingerprinter` interface + `fpcalcFingerprinter`

```go
type Fingerprint []uint32

type Fingerprinter interface {
    Generate(wavPath string, offsetSec, lengthSec int) (Fingerprint, error)
}
```

Real implementation shells out to `fpcalc -raw -offset N -length M`.
Mock implementation used in tests.

### Sliding-window BER function

```go
// BER returns the minimum bit error rate between a and b over all shifts in [-maxShift, +maxShift].
func BER(a, b Fingerprint, maxShift int) float64
```

Pure function, no I/O, fully unit-testable with synthetic data.

### `FingerprintStore` (DB layer)

```go
type FingerprintStore interface {
    Save(entryID int64, fps []Fingerprint) error
    FindMatch(fp Fingerprint, threshold float64, maxShift int) (entryID int64, ber float64, found bool, err error)
}
```

`FindMatch` scans all stored fingerprints, running `BER` against each, and
returns the entry with the lowest BER below the threshold.

### Integration in `runRecognizer`

- After capturing WAV, call `Fingerprinter.Generate` for N windows.
- Call `FingerprintStore.FindMatch`.
- Branch on hit/miss as described above.
- On ACRCloud result or no-match stub, call `FingerprintStore.Save`.

## New Dependency

`fpcalc` from the `chromaprint-tools` package:

```bash
apt-get install -y chromaprint-tools
```

Must be added to `install.sh` alongside `shairport-sync`.

## Configuration

Two new optional parameters (with sensible defaults, no required changes):

| Flag | Default | Description |
|------|---------|-------------|
| `--fingerprint-windows` | `3` | Number of fingerprint windows to generate per capture |
| `--fingerprint-stride` | `5` | Stride in seconds between windows |
| `--fingerprint-length` | `15` | Length of each window in seconds |
| `--fingerprint-threshold` | `0.35` | BER threshold for a local match |

## Testing

| Test case | Method |
|-----------|--------|
| BER with identical fingerprints | unit, synthetic data |
| BER with shifted fingerprints | unit, synthetic data |
| BER above threshold (no match) | unit, synthetic data |
| Local hit skips ACRCloud | mock Fingerprinter + mock FingerprintStore |
| Local miss falls through to ACRCloud | mock |
| ACRCloud match stores fingerprints | mock |
| ACRCloud no-match stores stub | mock |
| user_confirmed=0 stub falls through | mock |

## Out of Scope

- Approximate matching beyond sliding-window BER (e.g. LSH indexing)
- UI changes to surface stub entries (separate issue)
- Bulk re-fingerprinting of existing library entries
