#!/usr/bin/env python3
"""
AcoustID proof-of-concept: fingerprint a local WAV (or similar) with fpcalc,
then look up metadata via api.acoustid.org.

Not used by Oceano services — for manual validation on the Pi before a Go integration.

Why lookups often return empty results (even for popular music)
---------------------------------------------------------------
1. **Short clips:** AcoustID’s FAQ states the service is **not** designed for short
   snippets — it targets **full audio files** (e.g. rips). Community reports match:
   many **5–60 s** segments return **no hit** while the **same** material as a full
   file matches. See: https://acoustid.org/faq and chromaprint issue #146
   (https://github.com/acoustid/chromaprint/issues/146).

2. **Duration parameter:** The API documents ``duration`` as the **whole file**
   length in seconds. Tools like pyacoustid send the **fingerprinted segment**
   length. That works for **full-file** fingerprints; for **partial** captures,
   trying the **known track duration** (when available) is sometimes suggested in
   the wild, but it is **not** reliable for analog captures or snippet matching.

3. **Analog vs digital master:** Audio from REC OUT (vinyl/CD through the amp) is
   **not bit-identical** to the digital releases AcoustID users usually fingerprint,
   so matches can be weak or absent even with long captures.

**Implication for Oceano:** default capture is a **few seconds** of PCM — a poor
fit for AcoustID compared to **ACRCloud / AudD**-style acoustic ID. Treat AcoustID
as complementary (e.g. after a long capture or offline file workflow), not a
drop-in replacement for short-window recognition.

Prerequisites (Raspberry Pi OS / Debian):
  sudo apt install libchromaprint-tools python3-pip python3-venv
  python3 -m venv .venv && source .venv/bin/activate
  pip install -r scripts/requirements-acoustid-poc.txt

API key (free): https://acoustid.org/register
  export ACOUSTID_API_KEY='your-client-key'

Example:
  ./scripts/poc_acoustid.py /path/to/sample.wav
  arecord -d 12 -f cd /tmp/test.wav && ./scripts/poc_acoustid.py /tmp/test.wav
"""

from __future__ import annotations

import argparse
import json
import os
import sys


def main() -> int:
    parser = argparse.ArgumentParser(description="AcoustID POC (fpcalc + lookup)")
    parser.add_argument(
        "audio_path",
        help="Path to audio file (WAV from Oceano capture works: S16_LE stereo 44100)",
    )
    parser.add_argument(
        "--max-length",
        type=int,
        default=20,
        help="Seconds of audio for fpcalc -length (default: 20)",
    )
    parser.add_argument(
        "--api-key",
        default=os.environ.get("ACOUSTID_API_KEY", ""),
        help="AcoustID client API key (default: env ACOUSTID_API_KEY)",
    )
    parser.add_argument(
        "--raw-json",
        action="store_true",
        help="Print full AcoustID JSON response instead of parsed rows",
    )
    args = parser.parse_args()

    if not args.api_key:
        print(
            "error: missing API key. Register at https://acoustid.org/register "
            "and set ACOUSTID_API_KEY or pass --api-key",
            file=sys.stderr,
        )
        return 2

    try:
        import acoustid
    except ImportError:
        print(
            "error: pyacoustid not installed. pip install -r scripts/requirements-acoustid-poc.txt",
            file=sys.stderr,
        )
        return 2

    path = os.path.abspath(os.path.expanduser(args.audio_path))
    if not os.path.isfile(path):
        print(f"error: not a file: {path}", file=sys.stderr)
        return 2

    print(f"fingerprinting via fpcalc (max {args.max_length}s): {path}", file=sys.stderr)
    try:
        duration, fp = acoustid.fingerprint_file(
            path, maxlength=args.max_length, force_fpcalc=True
        )
    except acoustid.NoBackendError:
        print(
            "error: fpcalc not found. Install: sudo apt install libchromaprint-tools",
            file=sys.stderr,
        )
        return 3
    except acoustid.FingerprintGenerationError as exc:
        print(f"error: fingerprint failed: {exc}", file=sys.stderr)
        return 3

    print(f"duration={duration:.2f}s (sent to API as {int(duration)}), lookup…", file=sys.stderr)

    meta = ["recordings", "releasegroups", "compress"]
    try:
        data = acoustid.lookup(args.api_key, fp, int(duration), meta=meta, timeout=30)
    except acoustid.WebServiceError as exc:
        print(f"error: AcoustID lookup failed: {exc}", file=sys.stderr)
        return 4

    if args.raw_json:
        print(json.dumps(data, indent=2))
        return 0

    try:
        rows = list(acoustid.parse_lookup_result(data))
    except acoustid.WebServiceError as exc:
        print(f"error: could not parse results: {exc}", file=sys.stderr)
        if data.get("results"):
            print(json.dumps(data, indent=2), file=sys.stderr)
        return 4

    if not rows:
        print("no recording matches (try longer clip, skip silence at start, or check capture level)")
        return 1

    print(f"{'score':>8}  {'recording_id':>36}  artist / title")
    for score, rec_id, title, artist in rows[:15]:
        t = title or "?"
        a = artist or "?"
        print(f"{score:8.3f}  {rec_id}  {a} — {t}")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
