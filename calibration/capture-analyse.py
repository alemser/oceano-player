#!/usr/bin/env python3
import sys, struct, math, csv

csv_file      = sys.argv[1]
label         = sys.argv[2]
buf_size      = int(sys.argv[3])
sample_rate   = int(sys.argv[4])
total_windows = int(sys.argv[5])

try:
    import numpy as np
    USE_NUMPY = True
except ImportError:
    USE_NUMPY = False

bin_hz           = sample_rate / buf_size
low_min          = max(1, int(20.0 / bin_hz))
low_max          = int(80.0 / bin_hz)
bytes_per_window = buf_size * 4

def analyse(raw_bytes):
    frames = struct.unpack(f'<{buf_size * 2}h', raw_bytes)
    mono   = [(frames[i*2] + frames[i*2+1]) / 2.0 / 32768.0 for i in range(buf_size)]
    if USE_NUMPY:
        arr      = np.array(mono)
        rms      = float(math.sqrt(np.mean(arr ** 2)))
        spec     = np.fft.rfft(arr)
        energies = np.abs(spec) ** 2
        low_e    = float(np.sum(energies[low_min:low_max + 1]))
        total_e  = float(np.sum(energies))
    else:
        import cmath
        rms = math.sqrt(sum(s * s for s in mono) / buf_size)
        low_e = total_e = 0.0
        for k in range(buf_size // 2):
            c = sum(mono[t] * cmath.exp(-2j * math.pi * k * t / buf_size) for t in range(buf_size))
            e = abs(c) ** 2
            total_e += e
            if low_min <= k <= low_max:
                low_e += e
    ratio = low_e / total_e if total_e > 0 else 0.0
    return rms, ratio

with open(csv_file, 'w', newline='') as cf:
    writer = csv.writer(cf)
    writer.writerow(['window', 'label', 'rms', 'low_freq_ratio'])
    window_n = 0
    while True:
        raw = sys.stdin.buffer.read(bytes_per_window)
        if len(raw) < bytes_per_window:
            break
        rms, ratio = analyse(raw)
        writer.writerow([window_n, label, f'{rms:.6f}', f'{ratio:.6f}'])
        if window_n % 10 == 0:
            pct = int(window_n / total_windows * 100) if total_windows > 0 else 0
            print(f"  [{pct:3d}%] w={window_n:5d}  rms={rms:.4f}  ratio={ratio:.4f}", flush=True)
        window_n += 1
print(f"\n  Done. {window_n} windows written to {csv_file}")
