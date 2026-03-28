#!/usr/bin/env python3
"""
Oceano Calibration Analyser
Reads CSV files from calibration-data/ and suggests optimal thresholds.

Usage (called by analyse-session.sh):
  python3 analyse-session.py <data_dir>
"""
import csv
import os
import sys
import math
from collections import defaultdict

data_dir = sys.argv[1]

rows_by_label = defaultdict(list)
for fname in sorted(os.listdir(data_dir)):
    if not fname.endswith('.csv'):
        continue
    path = os.path.join(data_dir, fname)
    with open(path) as f:
        for row in csv.DictReader(f):
            try:
                rows_by_label[row['label']].append(
                    (float(row['rms']), float(row['low_freq_ratio']))
                )
            except (ValueError, KeyError):
                pass

if not rows_by_label:
    print("No CSV data found in", data_dir)
    sys.exit(1)


def stats(values):
    values = sorted(values)
    n = len(values)
    mean = sum(values) / n
    std = math.sqrt(sum((v - mean) ** 2 for v in values) / n)
    return {
        'n':    n,
        'min':  values[0],
        'p5':   values[max(0, int(n * 0.05))],
        'p25':  values[max(0, int(n * 0.25))],
        'mean': mean,
        'p75':  values[min(n - 1, int(n * 0.75))],
        'p95':  values[min(n - 1, int(n * 0.95))],
        'max':  values[-1],
        'std':  std,
    }


print()
print("=" * 72)
print("  Per-label statistics")
print("=" * 72)
fmt = "{:<10} {:<8} {:>8} {:>8} {:>8} {:>8} {:>8} {:>8}"
print(fmt.format("Label", "Metric", "Min", "P5", "P25", "Mean", "P75", "P95"))
print("-" * 72)

all_stats = {}
for label, rows in sorted(rows_by_label.items()):
    rms_s   = stats([r for r, _ in rows])
    ratio_s = stats([r for _, r in rows])
    all_stats[label] = {'rms': rms_s, 'ratio': ratio_s}

    print(fmt.format(label, "rms",
        f"{rms_s['min']:.4f}", f"{rms_s['p5']:.4f}", f"{rms_s['p25']:.4f}",
        f"{rms_s['mean']:.4f}", f"{rms_s['p75']:.4f}", f"{rms_s['p95']:.4f}"))
    print(fmt.format("", "ratio",
        f"{ratio_s['min']:.4f}", f"{ratio_s['p5']:.4f}", f"{ratio_s['p25']:.4f}",
        f"{ratio_s['mean']:.4f}", f"{ratio_s['p75']:.4f}", f"{ratio_s['p95']:.4f}"))
    print(fmt.format("", "windows", rms_s['n'], "", "", "", "", ""))
    print("-" * 72)

# ─── Threshold suggestions ────────────────────────────────────────────────────
print()
print("=" * 72)
print("  Threshold suggestions")
print("=" * 72)

issues = []

# silence-threshold
if 'silence' in all_stats:
    sil_p95 = all_stats['silence']['rms']['p95']
    silence_threshold = round(sil_p95 * 1.5, 5)
    print(f"  --silence-threshold  {silence_threshold:<10}  (silence p95 rms={sil_p95:.5f} × 1.5)")
else:
    min_p5 = min(v['rms']['p5'] for v in all_stats.values())
    silence_threshold = round(min_p5 * 0.25, 5)
    print(f"  --silence-threshold  {silence_threshold:<10}  (estimated — run silence capture for accuracy)")
    issues.append("No silence session. Run: ./capture-session.sh --label silence --duration 60")

# vinyl-threshold
vinyl_threshold = None
if 'cd' in all_stats and 'vinyl' in all_stats:
    cd_p95   = all_stats['cd']['ratio']['p95']
    vinyl_p5 = all_stats['vinyl']['ratio']['p5']
    gap      = vinyl_p5 - cd_p95

    if gap > 0:
        vinyl_threshold = round((cd_p95 + vinyl_p5) / 2, 4)
        marker = '✓ clean separation' if gap >= 0.05 else '⚠ small gap — consider reducing mic volume'
        print(f"  --vinyl-threshold    {vinyl_threshold:<10}  (CD p95={cd_p95:.4f}  Vinyl p5={vinyl_p5:.4f}  gap={gap:.4f}  {marker})")
        if gap < 0.05:
            issues.append(
                f"Gap between CD and Vinyl is small ({gap:.4f}). "
                "Reduce mic volume and re-capture: amixer -c 2 set 'Mic Capture Volume' 1,1"
            )
    else:
        print(f"  --vinyl-threshold    ⚠ CANNOT SUGGEST — CD and Vinyl ratio ranges OVERLAP")
        issues.append(
            f"CD ratio p95 ({cd_p95:.4f}) >= Vinyl ratio p5 ({vinyl_p5:.4f}). "
            "Ranges overlap — detector cannot separate them reliably at this mic volume. "
            "Try: amixer -c 2 set 'Mic Capture Volume' 1,1 and re-capture."
        )
elif 'vinyl' in all_stats:
    vinyl_threshold = round(all_stats['vinyl']['ratio']['p5'] * 0.8, 4)
    print(f"  --vinyl-threshold    {vinyl_threshold:<10}  (estimated from vinyl p5 — run CD capture for accuracy)")
    issues.append("No CD session found. Run: ./capture-session.sh --label cd --duration 300")
else:
    print(f"  --vinyl-threshold    ⚠ not enough data")

# min-vinyl-rms
if 'silence' in all_stats:
    min_vinyl_rms = round(all_stats['silence']['rms']['p95'] * 3.0, 4)
    print(f"  --min-vinyl-rms      {min_vinyl_rms:<10}  (silence p95 rms × 3)")
elif 'cd' in all_stats:
    min_vinyl_rms = round(all_stats['cd']['rms']['p5'] * 0.4, 4)
    print(f"  --min-vinyl-rms      {min_vinyl_rms:<10}  (CD p5 rms × 0.4 — estimated)")
else:
    min_vinyl_rms = 0.05
    print(f"  --min-vinyl-rms      {min_vinyl_rms:<10}  (default — not enough data)")

# Overlap analysis
if 'cd' in all_stats and 'vinyl' in all_stats and vinyl_threshold:
    cd_rows    = [r for _, r in rows_by_label['cd']]
    vinyl_rows = [r for _, r in rows_by_label['vinyl']]
    cd_overlap    = sum(1 for r in cd_rows    if r > vinyl_threshold)
    vinyl_overlap = sum(1 for r in vinyl_rows if r < vinyl_threshold)
    print()
    print("  Overlap at suggested threshold:")
    print(f"    CD windows above threshold:    {cd_overlap}/{len(cd_rows)} ({100*cd_overlap/len(cd_rows):.1f}%) ← false Vinyl")
    print(f"    Vinyl windows below threshold: {vinyl_overlap}/{len(vinyl_rows)} ({100*vinyl_overlap/len(vinyl_rows):.1f}%) ← false CD")

# Issues
if issues:
    print()
    print("  ⚠ Issues:")
    for i, issue in enumerate(issues, 1):
        print(f"    {i}. {issue}")

# Apply command
print()
print("=" * 72)
print("  Apply with:")
print("=" * 72)
cmd = "  sudo ./install-source-detector.sh"
cmd += f" \\\n    --silence-threshold {silence_threshold}"
if vinyl_threshold:
    cmd += f" \\\n    --vinyl-threshold {vinyl_threshold}"
cmd += f" \\\n    --min-vinyl-rms {min_vinyl_rms}"
print(cmd)
print()