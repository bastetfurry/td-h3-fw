#!/usr/bin/env python3
"""Recorded receiver audio through a model of the TD-H3's P4.6 front end, then fw/afsk.c: how many
packets of a test recording (the WA8LMF TNC Test CD, http://wa8lmf.net/TNCtest/) the TNC would get.

The front end (docs/HARDWARE.md, "P4.6 as an audio input"):
  audio -> EARO's RC low-pass (2K7, 47 nF: ~1.25 kHz, first order) -> P4.6, a Schmitt input whose DC
  is C47's charge (the bias) -> 8 reads spread over each sample, 13 us apart (fw/tnc.c t2_isr; --read-us,
  1.5 before), 9592 times a second: n = how many read 1
  -> every 16 samples the servo pulses the bias one step toward half ones; a pulse also latches the
  input at its own level (seen on the radio).
Units are bias steps: the radio measured a hysteresis band of 3-8 steps and an AFSK swing of 100+.
--scale sets the audio's level: its 99.9th percentile becomes that many steps (default 100).

The decoder is fw/afsk.c built for the host (tools/afskmodel.py c_frames: bit-exact with the
firmware). --ref also runs F4HWN's reference demodulator (model_rx.py; K1_REPO: a checkout of
https://github.com/armel/uv-k1-k5v3-firmware-custom) on the same audio as 12-bit samples at 9600 Hz:
what an ADC on the audio would get, the ceiling for this front end.

    tools/afsksim.py track1.flac [--scale 100] [--band 5] [--start S --seconds S] [--ref]
"""
import argparse
import math
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import afskmodel  # noqa: E402

FS = 4_000_000 / 417
READ_US = 417 / 8 / 4               # Timer2: 52-53 counts at 4 MHz between reads


def load(path, start=0.0, seconds=None):
    """Mono float audio at 44.1 kHz through ffmpeg (WAV, FLAC, anything it reads)."""
    cmd = ['ffmpeg', '-v', 'error', '-ss', str(start)] + (['-t', str(seconds)] if seconds else []) + \
          ['-i', path, '-ac', '1', '-ar', '44100', '-f', 's16le', '-']
    raw = subprocess.run(cmd, capture_output=True, check=True).stdout
    import numpy as np                      # only for recordings: the suite runs on PyPy without packages
    return np.frombuffer(raw, dtype='<i2').astype(np.float64) / 32768.0, 44100


def rc_lowpass(x, fs, fc=1254.0):
    """First-order RC (EARO's R80 2K7 and C56 47 nF)."""
    a = math.exp(-2 * math.pi * fc / fs)
    y = []
    acc = 0.0
    for v in x:
        acc = a * acc + (1 - a) * v
        y.append(acc)
    return y


def percentile(x, q):
    """numpy.percentile's default (linear between the two nearest ranks)."""
    s = sorted(x)
    if not s:
        return 0.0
    pos = (len(s) - 1) * q / 100
    i = int(pos)
    return s[i] + (s[min(i + 1, len(s) - 1)] - s[i]) * (pos - i)


def front_end(x, fs, scale=100.0, band=5.0, period=16, dead=16, step=1.0):
    """Audio -> n per sample (0-8), as P4.6 and tnc_isr make it. The servo: every period samples (a power
    of 2), a step toward half ones when the reads that were 1 are off half by more than dead (of 128 at
    period 16: tnc_isr's 48 and 80)."""
    mid = 4 * period
    lo, hi = mid - dead * period // 16, mid + dead * period // 16
    x = rc_lowpass(x.tolist() if hasattr(x, 'tolist') else x, fs)
    g = scale / (percentile([abs(v) for v in x], 99.9) or 1.0)
    x = [v * g for v in x]
    ns = int(len(x) / fs * FS) - 1
    last = len(x) - 1
    h = band / 2
    out = bytearray(ns)
    bias, state, ones = 0.0, 0, 0
    for k in range(ns):
        n = 0
        for j in range(8):
            t = (k / FS + j * READ_US * 1e-6) * fs   # the read's instant, in input samples
            i0 = int(t)
            f = t - i0
            p = x[i0] * (1 - f) + x[min(i0 + 1, last)] * f + bias   # the audio at the read
            if p > h:
                state = 1
            elif p < -h:
                state = 0
            n += state
        out[k] = n
        ones += n
        if k % period == period - 1:           # tnc_isr's servo: ones over 16 samples, aim 64 of 128
            if ones < lo:
                bias += step; state = 1        # the pulse drives the pin: the input latches its level
            elif ones > hi:
                bias -= step; state = 0
            ones = 0
    return bytes(out)


def modulate(frames, flags=30, between=1, twist=1.0, fs=44100, pre=b''):
    """Bell 202 audio, as a TNC sends: flags 0x7E, each frame (bytes without the FCS) with its FCS, LSB
    first, a 0 after five 1s, then between flags, the last frame 3; NRZI (a 0 swaps 1200 and 2200 Hz),
    phase-continuous, 2200 Hz at twist times 1200 Hz's level. between=1: the frames share one flag (a digipeater's repeat in one
    transmission), the case that a slicer held after its frame loses. Silence 50 ms either side.
    pre: bytes sent after the preamble as they are (LSB first, not stuffed), then one flag: an FX.25
    correlation tag in front of the frame inside."""
    bits = []
    def flag(k):
        for _ in range(k):
            bits.extend(0x7E >> i & 1 for i in range(8))
    flag(flags)
    if pre:
        bits.extend(byte >> i & 1 for byte in pre for i in range(8))
        flag(1)
    for j, f in enumerate(frames):
        c = afskmodel.crc16(f)
        ones = 0
        for byte in bytes(f) + bytes([c & 0xFF, c >> 8]):
            for i in range(8):
                b = byte >> i & 1
                bits.append(b)
                ones = ones + 1 if b else 0
                if ones == 5:
                    bits.append(0)
                    ones = 0
        flag(between if j < len(frames) - 1 else 3)
    per = fs / 1200
    out, ph, tone, pos = [0.0] * int(fs * 0.05), 0.0, 1200, 0.0
    for b in bits:
        if not b:
            tone = 3400 - tone
        k = int(round(pos + per)) - int(round(pos))
        pos += per
        w, g = 2 * math.pi * tone / fs, 0.5 * (twist if tone == 2200 else 1.0)
        for m in range(1, k + 1):
            out.append(math.sin(ph + w * m) * g)
        ph = (ph + w * k) % (2 * math.pi)
    out.extend([0.0] * int(fs * 0.05))
    return out, fs


def reference(x, fs):
    """F4HWN's model_rx demodulator on the audio as an ADC would give it: 12 bits at 9600 Hz. Every frame,
    a station's repeats too: its slicers' copies of one frame (within 100 samples) count once."""
    if not os.environ.get('K1_REPO'):
        sys.exit('--ref needs K1_REPO: a checkout of https://github.com/armel/uv-k1-k5v3-firmware-custom')
    k1 = os.path.join(os.environ['K1_REPO'], 'App/apps/aprsrx/test')
    sys.path.insert(0, k1)
    import model_rx
    import numpy as np
    x = np.asarray(x)
    n = int(len(x) / fs * 9600)
    y = np.interp(np.arange(n) / 9600 * fs, np.arange(len(x)), x)
    y = np.clip(2048 + y * (1500 / (np.percentile(np.abs(y), 99.9) or 1.0)), 0, 4095).astype(int).tolist()
    d = model_rx.Demod()
    got, at = [], [0]
    def frame(f):
        if got and got[-1][0] == f and at[0] - got[-1][1] < 100:
            return
        got.append((f, at[0]))
    d.frame = frame
    for i, v in enumerate(y):
        at[0] = i
        d.sample(v)
    return [f for f, _ in got]


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument('audio')
    p.add_argument('--scale', type=float, default=100.0, help='the audio peak, in bias steps')
    p.add_argument('--band', type=float, default=5.0, help='the input hysteresis, in bias steps')
    p.add_argument('--start', type=float, default=0.0)
    p.add_argument('--seconds', type=float)
    p.add_argument('--period', type=int, default=16, help='servo: samples between steps (tnc_isr: 16)')
    p.add_argument('--dead', type=int, default=16, help='servo: dead band, ones off half of 128 (tnc_isr: 16)')
    p.add_argument('--step', type=float, default=1.0, help='servo: bias steps a pulse (tnc_isr: 1)')
    p.add_argument('--read-us', type=float, default=READ_US, help='the reads\' spacing in us (fw/tnc.c: 13.03; 1.5 before)')
    p.add_argument('--ref', action='store_true', help='also the reference demodulator on 12-bit samples')
    p.add_argument('-o', help='save the n stream (.n.gz, for tools/afskmodel.py and tools/afskbench.py)')
    a = p.parse_args()
    globals()['READ_US'] = a.read_us
    x, fs = load(a.audio, a.start, a.seconds)
    n = front_end(x, fs, a.scale, a.band, a.period, a.dead, a.step)
    if a.o:
        import gzip
        with gzip.open(a.o, 'wb', 9) as f:
            f.write(n)
    frames = afskmodel.c_frames(n)
    print('%s: %.0f s, scale %g, band %g, servo %d/%d/%g: fw/afsk.c %d packets (%d different)' % (
        os.path.basename(a.audio), len(x) / fs, a.scale, a.band, a.period, a.dead, a.step, len(frames), len(set(frames))))
    if a.ref:
        ref = [f[:-2] for f in reference(x, fs)]
        print('   reference (12-bit ADC): %d packets (%d different)' % (len(ref), len(set(ref))))


if __name__ == '__main__':
    main()
