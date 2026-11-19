#!/usr/bin/env python3
"""Integer model of fw/afsk.c, the 8051's AFSK 1200 demodulator for P4.6's 1-bit audio.

Input per sample: n = how many of the 8 reads of P4.6 were 1 (0-8), at FS = 4 MHz / 417 = 9592 Hz
(the bias servo keeps n near 4). F4HWN's APRS RX model (Demod in App/apps/aprsrx/test/model_rx.py,
https://github.com/armel/uv-k1-k5v3-firmware-custom) decodes the same input; this is the same chain
cut down for an 8051 and kept to what the 1-bit input needs:

    x = n - 4 (-4..4)
    -> optional 2nd-order band-pass around 1625 Hz (Q14 integers)
    -> 4 sliding correlators over one bit (8 samples): I/Q at 1200 and 2200 Hz, tables of +-7
    -> magnitudes max + 3/8 min
    -> per-tone peak AGC (optional) -> slicers sign(wa*Mm*Ps - wb*Ms*Pm), each with a DPLL
    -> NRZI, HDLC (flags, unstuffing, abort), CRC-16, UI check

Every step is integer, shifts only, so the C can match it bit for bit (tools/afskmodel.py --c
compares them).

MIT (LICENSE) but for this part, adapted from Apache-2.0 code and changed for an 8051 and a 1-bit
input (rewritten in our own form): the demodulator's chain (band-pass at 1625 Hz, Q 0.9; sliding
I/Q correlators over one bit; magnitudes max + 3/8 min; per-tone peak AGC; slicers weighted
2:3, 1:1, 3:2, each with its DPLL) is Demod in Armel / F4HWN's APRS RX model,
App/apps/aprsrx/test/model_rx.py in https://github.com/armel/uv-k1-k5v3-firmware-custom.
Copyright 2026 Armel F4HWN. Licensed under the Apache License, Version 2.0 (the "License"); you may
not use this part except in compliance with the License. You may obtain a copy of the License at
http://www.apache.org/licenses/LICENSE-2.0 (also LICENSE-APACHE). Unless required by applicable law
or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.

    tools/afskmodel.py synth x.n.gz      # frames each variant finds
    tools/afskmodel.py --c synth x.n.gz  # fw/afsk.c (gcc) against Demod51: the same frames?

Input: an .n.gz (tools/afsksim.py -o makes one from a recording) or synth (load).
"""
import math
import os
import sys

FS = 4_000_000 / 417
SLICERS = ((2, 3), (1, 1), (3, 2))


def crc16(data):
    c = 0xFFFF
    for b in data:
        c ^= b
        for _ in range(8):
            c = (c >> 1) ^ 0x8408 if c & 1 else c >> 1
    return c ^ 0xFFFF


def is_ax25(f):
    """afsk.c is_ax25: 2-10 addresses, the last one's bit 0 set, then a control byte (f: with the FCS)."""
    end = len(f) - 2
    e = 13
    while not (f[e] & 1) and e < 69 and e + 7 < end:
        e += 7
    return bool(f[e] & 1) and e + 1 < end


def is_ui(f):
    end = len(f) - 2
    e = 13
    while not (f[e] & 1) and e + 7 < end:
        e += 7
    return bool(f[e] & 1) and e + 2 < end and f[e + 1] == 0x03 and f[e + 2] == 0xF0


def tab(f, amp=7):
    """cos table over the shortest whole number of periods at FS ~ 9600 Hz (8 for 1200, 48 for 2200)."""
    fs = 9600                                 # the period of the table: 9592 is within 0.1 %
    per = fs // math.gcd(fs, f)
    return [int(round(amp * math.cos(2 * math.pi * f * k / fs))) for k in range(per)]


C1, C2 = tab(1200), tab(2200)                 # 8 and 48 entries; sin = cos 3/4 period on (6 of 8, 36 of 48)


class Slicer:
    def __init__(self, dm, wa, wb):
        self.dm, self.wa, self.wb = dm, wa, wb
        self.dprev = 0; self.phase = 0; self.last = 0
        self.sr = 0; self.ones = 0; self.byte = 0; self.nb = 0
        self.buf = bytearray(); self.inframe = False

    def step(self, a, b):
        d = a * self.wa - b * self.wb
        self.phase += self.dm.step
        if (d > 0) != (self.dprev > 0):
            self.phase += (0x8000 - self.phase) >> 2   # pull a transition toward mid-bit
        self.dprev = d
        if self.phase >= 0x10000:
            self.phase -= 0x10000
            lv = 1 if d > 0 else 0
            self.bit(1 if lv == self.last else 0)
            self.last = lv

    def bit(self, b):
        self.sr = (self.sr >> 1) | (b << 7)
        if self.sr == 0x7E:
            if self.inframe and len(self.buf) >= 18 and \
                    crc16(self.buf[:-2]) == self.buf[-2] | (self.buf[-1] << 8) and is_ui(self.buf):
                self.dm.frame(bytes(self.buf[:-2]))
            self.buf = bytearray(); self.inframe = True
            self.ones = self.byte = self.nb = 0
            return
        if b:
            self.ones += 1
            if self.ones > 6:
                self.inframe = False
                return
        else:
            if self.ones == 5:
                self.ones = 0
                return
            self.ones = 0
        if not self.inframe:
            return
        self.byte |= b << self.nb
        self.nb += 1
        if self.nb == 8:
            if len(self.buf) < self.dm.maxlen:
                self.buf.append(self.byte)
            else:
                self.inframe = False
            self.byte = self.nb = 0


class Demod:
    def __init__(self, bp=False, agc=True, slicers=SLICERS):
        self.bp, self.agc = bp, agc
        w0 = 2 * math.pi * 1625 / FS
        al = math.sin(w0) / (2 * 0.9)
        a0 = 1 + al
        self.b0 = int(round(al / a0 * 16384))
        self.a1 = int(round(-2 * math.cos(w0) / a0 * 16384))
        self.a2 = int(round((1 - al) / a0 * 16384))
        self.x1 = self.x2 = self.y1 = self.y2 = 0
        self.ring = [[0, 0, 0, 0] for _ in range(8)]
        self.r = self.k1 = self.k2 = 0
        self.acc = [0, 0, 0, 0]
        self.pm = self.ps = 64
        self.step = round(65536 * 1200 / FS)  # DPLL phase per sample (8199)
        self.sl = [Slicer(self, wa, wb) for wa, wb in slicers]
        self.frames = []
        self.maxlen = 330                     # bytes a frame may have, FCS included (AX.25's most)

    def frame(self, f):
        if f not in self.frames:
            self.frames.append(f)

    @staticmethod
    def mag(i, q):
        a, b = abs(i), abs(q)
        if a < b:
            a, b = b, a
        return a + (b >> 2) + (b >> 3)

    def sample(self, n):
        x = n - 4
        if self.bp:
            y = (self.b0 * (x * 64 - self.x2) - self.a1 * self.y1 - self.a2 * self.y2) >> 14
            self.x2, self.x1, self.y2, self.y1 = self.x1, x * 64, self.y1, y
            x = y >> 4                        # back to about +-4..+-16
        k1, k2 = self.k1, self.k2
        p = (x * C1[k1], x * C1[(k1 + 6) & 7], x * C2[k2], x * C2[(k2 + 36) % 48])
        o = self.ring[self.r]
        for j in range(4):
            self.acc[j] += p[j] - o[j]
        self.ring[self.r] = p
        self.r = (self.r + 1) & 7
        self.k1 = (k1 + 1) & 7
        self.k2 = k2 + 1 if k2 < 47 else 0
        mm, ms = self.mag(self.acc[0], self.acc[1]), self.mag(self.acc[2], self.acc[3])
        if self.agc:
            self.pm += (mm - self.pm) >> 5 if mm > self.pm else -(self.pm >> 10)
            self.ps += (ms - self.ps) >> 5 if ms > self.ps else -(self.ps >> 10)
            self.pm, self.ps = max(self.pm, 16), max(self.ps, 16)
            a, b = mm * self.ps, ms * self.pm
        else:
            a, b = mm, ms
        for s in self.sl:
            s.step(a, b)


# The band-pass in front of the correlators (Demod51): RBJ band-pass, 1625 Hz, Q 0.9, at 9592 Hz,
# coefficients in Q8 so that >> 8 takes a high byte; the output Y is ~8x the input, a signed byte.
_w0 = 2 * math.pi * 1625 / FS
_al = math.sin(_w0) / (2 * 0.9)
BP_B0 = round(_al / (1 + _al) * 2048)                 # 670: b0 x 8 (the output's scale) x 256
BP_A1 = round(-2 * math.cos(_w0) / (1 + _al) * 256)   # -167
BP_A2 = round((1 - _al) / (1 + _al) * 256)            # 89
Y_MAX = 100                                           # Y's clamp: the sum then fits 16 bits (seen: |Y| <= 44)
MAG_SHIFT = 5                                         # magnitudes >> 5 to 8 bits (WA8LMF track 1: 3, 4, 5 give
                                                      # 602, 599, 608 unique frames)


class Slicer51(Slicer):
    """afsk.c's HDLC. A slicer whose frame was handed out goes on (the flag that closed it may open the
    next frame)."""

    def bit(self, b):
        self.sr = (self.sr >> 1) | (b << 7)
        if self.sr == 0x7E:
            if self.inframe and len(self.buf) >= 17 and \
                    crc16(self.buf[:-2]) == self.buf[-2] | (self.buf[-1] << 8) and is_ax25(self.buf):
                self.dm.frame(bytes(self.buf[:-2]), self)
            self.buf = bytearray(); self.inframe = True
            self.ones = self.byte = self.nb = 0
            return
        if b:
            self.ones += 1
            if self.ones > 6:
                self.inframe = False
                return
        else:
            if self.ones == 5:
                self.ones = 0
                return
            self.ones = 0
        if not self.inframe:
            return
        self.byte |= b << self.nb
        self.nb += 1
        if self.nb == 8:
            if len(self.buf) < self.dm.maxlen:
                self.buf.append(self.byte)
            else:
                self.inframe = False
            self.byte = self.nb = 0


class Demod51(Demod):
    """What fw/afsk.c computes, bit for bit. x = n - 4 through the band-pass: Y = (670 (x - x2) + 167 Y1
    - 89 Y2) >> 8, at most +-100. Four correlators over the last 8 Y (their ring holds Y; a sum takes
    the product leaving out again: at 1200 Hz the table repeats every 8, so (Y - Y_old) c; at 2200 Hz
    the old product is Y_old times the table 8 places back). Magnitudes max + 3/8 min, >> 5, at most
    255; tone peaks in 8.8 fixed point (attack 1/32, no decay: anything faster than ~7 s lost the long
    frames), a = m1200 * peak2200 >> 1 and b likewise, two slicers 2a > 3b and a > b (the third, 3:2,
    never decoded a frame alone), a 16-bit DPLL phase whose carry samples the bit (the transition pull
    after the wrap test). The band-pass took WA8LMF track 1 from 509 to 608 unique frames
    (tools/afsksim.py)."""
    def __init__(self):
        super().__init__(bp=False, agc=True, slicers=((2, 3), (1, 1)))
        self.sl = [Slicer51(self, wa, wb) for wa, wb in ((2, 3), (1, 1))]
        self.maxlen = 332                     # fw/afsk.h AFSK_MAX
        self.pm = self.ps = 16 << 8
        self.ph = [0, 0]; self.dp = [0, 0]
        self.x1 = self.x2 = self.y1 = self.y2 = 0
        self.yr = [0] * 8                     # the last 8 Y
        self.acc = [0, 0, 0, 0]
        self.last, self.last_bits = None, 0   # afsk.c: the other slicer's copy within 16 slicer bits is dropped
        for sl in self.sl:                    # count every slicer bit, as afsk_bit does
            sl.bit = (lambda f: lambda b: (self._tick(), f(b)))(sl.bit)

    def _tick(self):
        if self.last_bits:
            self.last_bits -= 1

    def frame(self, f, sl=None):              # every frame, a station's repeats too (afsk.c got_frame)
        if self.last_bits and f == self.last:
            return
        self.last, self.last_bits = f, 16
        self.frames.append(f)

    def sample(self, n):
        x = n - 4
        y = (BP_B0 * (x - self.x2) - BP_A1 * self.y1 - BP_A2 * self.y2) >> 8
        y = max(-Y_MAX, min(Y_MAX, y))
        self.x2, self.x1, self.y2, self.y1 = self.x1, x, self.y1, y
        k1, k2, r = self.k1, self.k2, self.r
        yo = self.yr[r]                       # Y of 8 samples ago, leaving the window
        ko = k2 - 8 if k2 >= 8 else k2 + 40   # 2200 Hz table position 8 samples ago
        c, s = C1[k1], C1[(k1 + 6) & 7]
        self.acc[0] += (y - yo) * c
        self.acc[1] += (y - yo) * s
        self.acc[2] += y * C2[k2] - yo * C2[ko]
        self.acc[3] += y * C2[(k2 + 36) % 48] - yo * C2[(ko + 36) % 48]
        self.yr[r] = y
        self.r = (r + 1) & 7
        self.k1 = (k1 + 1) & 7
        self.k2 = k2 + 1 if k2 < 47 else 0
        mm = min(self.mag(self.acc[0], self.acc[1]) >> MAG_SHIFT, 255)
        ms = min(self.mag(self.acc[2], self.acc[3]) >> MAG_SHIFT, 255)
        for t, m in (('pm', mm), ('ps', ms)):
            v = getattr(self, t)
            if (m << 8) > v:
                v += ((m << 8) - v) >> 5
            setattr(self, t, max(v, 16 << 8))
        a, b = (mm * (self.ps >> 8)) >> 1, (ms * (self.pm >> 8)) >> 1
        for i, (sl, d) in enumerate(zip(self.sl, (2 * a > 3 * b, a > b))):
            ph = self.ph[i] + self.step
            wrap = ph >= 0x10000
            ph &= 0xFFFF
            if d != self.dp[i]:
                ph = (ph + ((0x8000 - ph) >> 2)) & 0xFFFF   # toward mid-bit
            self.dp[i] = d
            self.ph[i] = ph
            if wrap:
                lv = 1 if d else 0
                sl.bit(1 if lv == sl.last else 0)
                sl.last = lv


def to_n(x):
    """12-bit probe samples (n x 512, 4095 for 8) back to n."""
    return [8 if v >= 4095 else v >> 9 for v in x]


def synth(info=b'>TD-H3 RX test: 0123456789 abcdefghijklmnopqrstuvwxyz'):
    """n per sample for one APRS UI frame from N0CALL: tools/afsksim.py's Bell 202 audio, as a TNC sends
    it, through its model of the P4.6 front end."""
    import afsksim
    call = lambda c, last: bytes(x << 1 for x in c.ljust(6).encode()) + bytes([0x60 | last])
    return list(afsksim.front_end(*afsksim.modulate([call('APDR16', 0) + call('N0CALL', 1) + b'\x03\xf0' + info])))


def load(path):
    """n per sample: synth (above), an .n.gz (bytes of n, tools/afsksim.py -o) or a .npy of 12-bit probe samples."""
    if path == 'synth':
        return synth()
    if path.endswith('.n.gz'):
        import gzip
        return list(gzip.open(path).read())
    import numpy as np
    return to_n(np.load(path))


VARIANTS = {
    'full (bp, agc, 3 slicers)': dict(bp=True, agc=True),
    'agc, 3 slicers': dict(bp=False, agc=True),
    'agc, 1 slicer': dict(bp=False, agc=True, slicers=((1, 1),)),
    'no agc, 3 slicers': dict(bp=False, agc=False),
    'no agc, 1 slicer': dict(bp=False, agc=False, slicers=((1, 1),)),
}


HOST = os.path.join(os.path.dirname(os.path.abspath(__file__)), '../fw/build/afsk_host')


def c_frames(n):
    """fw/afsk.c built with gcc (tools/afsk_host.c): the frames it hands out for samples n."""
    import subprocess
    here = os.path.dirname(os.path.abspath(__file__))
    src = [os.path.join(here, 'afsk_host.c'), os.path.join(here, '../fw/afsk.c')]
    if not os.path.exists(HOST) or any(os.path.getmtime(f) > os.path.getmtime(HOST) for f in src + [os.path.join(here, '../fw/afsk.h')]):
        os.makedirs(os.path.dirname(HOST), exist_ok=True)
        subprocess.run(['gcc', '-O2', '-Wall', '-Wextra', '-Werror', '-DC_AFSK_SAMPLE', '-o', HOST] + src, check=True)
    out = subprocess.run([HOST], input=bytes(n), capture_output=True, check=True).stdout.decode()
    return [bytes.fromhex(l.split()[1]) for l in out.splitlines()]


def main():
    if sys.argv[1:2] == ['--c']:              # fw/afsk.c against Demod51, capture by capture
        bad = 0
        for path in sys.argv[2:]:
            n = load(path)
            d = Demod51()
            for v in n:
                d.sample(v)
            c = c_frames(n)
            ok = c == d.frames
            bad += not ok
            print('%-14s model %d frame(s), C %d frame(s): %s' % (os.path.basename(path), len(d.frames), len(c), 'same' if ok else 'DIFFERENT'))
        sys.exit(1 if bad else 0)
    for path in sys.argv[1:]:
        n = load(path)
        print(os.path.basename(path))
        for name, kw in list(VARIANTS.items()) + [('8051 (fw/afsk.c)', None)]:
            d = Demod51() if kw is None else Demod(**kw)
            for v in n:
                d.sample(v)
            print('   %-26s %d frame(s) %s' % (name, len(d.frames), [f[16:40] for f in d.frames]))


if __name__ == '__main__':
    main()
