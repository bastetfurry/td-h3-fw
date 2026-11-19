// AFSK 1200 receive from P4.6's 1-bit audio (docs/HARDWARE.md, P4.6 as an audio input).
// tools/afskmodel.py Demod51 is the same computation in Python, bit for bit; the chain is F4HWN's APRS RX
// demodulator (below) cut down for the 8051 and the 1-bit input:
//   x = n - 4 -> band-pass (1625 Hz, Q 0.9; Y = (670 (x - x2) + 167 Y1 - 89 Y2) >> 8, ~8x, at most +-100)
//   -> 4 sliding correlators over one bit (8 samples): I/Q at 1200 and 2200 Hz, tables of +-7
//   -> magnitudes max + 3/8 min, >> 5, at most 255 -> per-tone peaks (8.8, attack 1/32, no decay: any
//   decay faster than ~7 s lost long frames) -> a = mag1200 * peak2200 / 2, b = mag2200 * peak1200 / 2
//   -> slicers 2a > 3b and a > b, each with its DPLL -> NRZI -> HDLC -> CRC-16 -> AX.25 address check.
// The band-pass: WA8LMF track 1 (tools/afsksim.py) 509 -> 608 unique frames.
// afsk_sample runs 9592 times a second: fw/asm/afsk_sample.asm is its twin (tools/afskbench.py
// proves them equal, state after every sample). What it names is global here, prefixed afsk_.
//
// MIT (LICENSE) but for this part, adapted from Apache-2.0 code and changed for an 8051 and a 1-bit
// input (rewritten in our own form): the demodulator's chain (band-pass at 1625 Hz, Q 0.9; sliding
// I/Q correlators over one bit; magnitudes max + 3/8 min; per-tone peak AGC; slicers weighted 2:3,
// 1:1, 3:2, each with its DPLL) is Demod in Armel / F4HWN's APRS RX model,
// App/apps/aprsrx/test/model_rx.py in https://github.com/armel/uv-k1-k5v3-firmware-custom.
// Copyright 2026 Armel F4HWN. Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this part except in compliance with the License. You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0 (also LICENSE-APACHE). Unless required by applicable law
// or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
#include "afsk.h"

#define FS_STEP 8199u                       // DPLL phase a sample: 65536 * 1200 / 9592

// round(7 cos(2 pi f k / 9600)): tools/afskmodel.py tab()
const __code int8_t afsk_c1200[8] = {7, 5, 0, -5, -7, -5, 0, 5};
const __code int8_t afsk_c2200[48] = {
     7,  1, -7, -3,  6,  4, -5, -6,  4,  6, -2, -7,  0,  7,  2, -6, -3,  6,  5, -4, -6,  3,  7, -1,
    -7, -1,  7,  3, -6, -4,  5,  6, -4, -6,  2,  7,  0, -7, -2,  6,  3, -6, -5,  4,  6, -3, -7,  1};
__xdata int8_t afsk_yr[8];                  // the last 8 band-pass outputs: the correlators' window
// The per-sample state in internal RAM (27 B of the stack's room: docs/DEVELOPING.md): XDATA cost
// most of the 8051's time here. The sums stay within +-5600 (8 products of at most 100 x 7).
__data int16_t afsk_acc[4];                 // I1200, Q1200, I2200, Q2200
__data int8_t afsk_x1, afsk_x2, afsk_y1, afsk_y2;   // the band-pass's last inputs and outputs
__data uint8_t afsk_r, afsk_k1, afsk_k2;    // ring slot, table positions
__data uint16_t afsk_pk1, afsk_pk2;         // tone peaks, 8.8: they only ever rise from 16.0
__data uint16_t afsk_ph[AFSK_NSL];          // each slicer's DPLL phase: its carry samples the bit
__data uint8_t afsk_dprev[AFSK_NSL], afsk_last[AFSK_NSL];   // the decision before; the level at the last bit

// Each slicer's HDLC state, one small array per field: a struct through a pointer cost SDCC ~20 B of
// spill temporaries in DATA (the stack's room).
static __xdata uint8_t h_sr[AFSK_NSL], h_ones[AFSK_NSL], h_byte[AFSK_NSL], h_nb[AFSK_NSL];
static __xdata uint8_t h_inframe[AFSK_NSL];
static __xdata uint16_t h_len[AFSK_NSL], h_crc[AFSK_NSL];
static __xdata uint8_t * __xdata h_buf[AFSK_NSL];   // AFSK_MAX bytes each, the caller's (afsk_init)
// DCD, for the TNC's "packet coming in" light (it plays no part in decoding): a slicer that has seen 3
// flags in a row, 8 bits apart (a preamble), is valid till an abort; while valid, each flag and each
// byte of a frame keep its countdown going for DCD_BITS more bits.
#define DCD_BITS 200                        // ~170 ms
static __xdata uint8_t h_since[AFSK_NSL], h_run[AFSK_NSL], h_valid[AFSK_NSL], h_dcd[AFSK_NSL];

__xdata uint16_t afsk_len;
__xdata uint8_t * __xdata afsk_frame;
static __xdata uint16_t last_crc, last_len; // the frame handed out last: the other slicer's copy is dropped
static __xdata uint8_t last_bits;           // for this many more slicer bits (~7 ms): a station's own repeat
                                            // later is a new frame (WA8LMF track 3 is one packet 100 times)

void afsk_agc_reset(void) { afsk_pk1 = afsk_pk2 = 16 << 8; }

void afsk_init(__xdata uint8_t *buf0, __xdata uint8_t *buf1) __reentrant   // its temporaries on the stack, not DATA
{
    uint8_t i;
    for (i = 0; i < 8; i++) afsk_yr[i] = 0;
    afsk_acc[0] = afsk_acc[1] = afsk_acc[2] = afsk_acc[3] = 0;
    afsk_x1 = afsk_x2 = afsk_y1 = afsk_y2 = 0;
    afsk_r = afsk_k1 = afsk_k2 = 0;
    afsk_agc_reset();
    h_buf[0] = buf0; h_buf[1] = buf1;
    for (i = 0; i < AFSK_NSL; i++) {
        afsk_ph[i] = 0; afsk_dprev[i] = afsk_last[i] = 0;
        h_sr[i] = h_ones[i] = h_byte[i] = h_nb[i] = h_inframe[i] = 0;
        h_since[i] = h_run[i] = h_valid[i] = h_dcd[i] = 0;
        h_len[i] = 0;
    }
    afsk_len = 0; last_len = 0; last_bits = 0;
}

uint8_t afsk_dcd(void)                      // a packet coming in: a valid slicer's countdown runs
{
    return h_dcd[0] | h_dcd[1];
}

void afsk_release(void) { afsk_len = 0; }

static uint16_t crc_byte(uint16_t c, uint8_t b)   // CRC-16/X.25 register, before the final complement
{
    c ^= b;
    for (uint8_t i = 0; i < 8; i++) c = c & 1 ? (c >> 1) ^ 0x8408 : c >> 1;
    return c;
}

uint16_t afsk_fcs(__xdata uint8_t *f, uint16_t n) __reentrant   // AX.25 FCS of n bytes: send it low byte first
{
    uint16_t c = 0xFFFF;
    while (n--) c = crc_byte(c, *f++);
    return ~c;
}

// Any AX.25 frame, not just UI (APRS): connected mode's I, S and U frames too, for a BBS or a node.
// 2 to 10 addresses, the last one's bit 0 set, then at least the control byte. n: without the FCS.
static uint8_t is_ax25(__xdata uint8_t *f, uint16_t n)
{
    uint8_t e = 13;
    while (!(f[e] & 1) && e < 69 && e + 7 < n) e += 7;
    return (f[e] & 1) && e + 1 < n;
}

static void got_frame(uint8_t s)
{
    __xdata uint8_t *f = h_buf[s];
    uint16_t n = h_len[s] - 2, fcs;
    // Over data and FCS, the register ends at 0xF0B8 exactly when the FCS is right.
    if (h_crc[s] != 0xF0B8 || !is_ax25(f, n)) return;
    fcs = f[n] | (uint16_t)f[n + 1] << 8;
    if (last_bits && n == last_len && fcs == last_crc) return;   // the other slicer's copy
    if (afsk_len) return;                   // one still waiting: this one is lost
    last_len = n; last_crc = fcs; last_bits = 16;
    afsk_frame = f; afsk_len = n;   // the slicer goes on: the flag that closed this frame
}                                           // may open the next (a digipeater's repeat right behind it)

void afsk_bit(uint8_t s, uint8_t b)         // slicer s's next bit after NRZI (1: no tone change)
{
    uint8_t sr = h_sr[s] >> 1 | b << 7, c;
    if (last_bits) last_bits--;
    if (h_dcd[s]) h_dcd[s]--;
    if (h_since[s] != 0xFF) h_since[s]++;
    h_sr[s] = sr;
    if (sr == 0x7E) {                       // a flag: the frame so far ends, a new one may start
        c = h_since[s] == 8 ? h_run[s] + 1 : 1;
        h_run[s] = c > 3 ? 3 : c;
        h_since[s] = 0;
        if (c >= 3) h_valid[s] = 1;
        if (h_valid[s]) h_dcd[s] = DCD_BITS;
        if (h_inframe[s] && h_len[s] >= 17) got_frame(s);   // with the FCS: 2 addresses, control (RR, UA)
        h_inframe[s] = 1; h_len[s] = 0; h_crc[s] = 0xFFFF;
        h_ones[s] = h_byte[s] = h_nb[s] = 0;
        return;
    }
    if (b) {
        if (++h_ones[s] > 6) { h_inframe[s] = 0; h_valid[s] = 0; return; }   // abort or noise: wait for a flag
    } else {
        if (h_ones[s] == 5) { h_ones[s] = 0; return; }        // a stuffed zero
        h_ones[s] = 0;
    }
    if (!h_inframe[s]) return;
    c = h_byte[s] | b << h_nb[s];
    if (++h_nb[s] == 8) {
        uint16_t n = h_len[s];
        if (n < AFSK_MAX) { h_buf[s][n] = c; h_len[s] = n + 1; h_crc[s] = crc_byte(h_crc[s], c); }
        else h_inframe[s] = 0;              // too long: dropped. Not DCD's: an FX.25 tag (2, 6, 8) holds an
                                            // abort, and one flag then opens the frame inside
        if (h_valid[s]) h_dcd[s] = DCD_BITS;
        c = 0; h_nb[s] = 0;
    }
    h_byte[s] = c;
}

#ifdef C_AFSK_SAMPLE
// max + 3/8 min of |i|, |q| (each at most 5600), >> 5, at most 255
#define MAG(out, i, q) do { \
        a = (i) < 0 ? -(i) : (i); b = (q) < 0 ? -(q) : (q); \
        if (a < b) { t = a; a = b; b = t; } \
        t = (a + (b >> 2) + (b >> 3)) >> 5; \
        out = t > 255 ? 255 : t; \
    } while (0)
// the peak follows m up by 1/32 of the gap, never down
#define PEAK(p, m) do { t = (uint16_t)(m) << 8; if (t > p) p += (t - p) >> 5; } while (0)

void afsk_sample(uint8_t n)
{
    static __data int8_t x, y, yo;
    static __data int16_t sum;
    static __data uint8_t m1, m2, s, d, ko, k, g;
    static __data uint16_t a, b, t, ph;

    x = (int8_t)n - 4;                      // the band-pass: |sum| <= 5360 + 16700 + 8900, 16 bits
    sum = 670 * (int16_t)(x - afsk_x2) + 167 * (int16_t)afsk_y1 - 89 * (int16_t)afsk_y2;
    sum >>= 8;                              // arithmetic: floor, as Python's
    y = sum > 100 ? 100 : sum < -100 ? -100 : (int8_t)sum;
    afsk_x2 = afsk_x1; afsk_x1 = x; afsk_y2 = afsk_y1; afsk_y1 = y;

    yo = afsk_yr[afsk_r];                   // the Y leaving the window
    k = afsk_k1; ko = afsk_k2 < 8 ? afsk_k2 + 40 : afsk_k2 - 8;
    afsk_acc[0] += (int16_t)(y - yo) * afsk_c1200[k];   // 1200 Hz: the table repeats every 8
    afsk_acc[1] += (int16_t)(y - yo) * afsk_c1200[(k + 6) & 7];
    afsk_acc[2] += (int16_t)y * afsk_c2200[afsk_k2] - (int16_t)yo * afsk_c2200[ko];
    g = afsk_k2 < 12 ? afsk_k2 + 36 : afsk_k2 - 12;
    ko = ko < 12 ? ko + 36 : ko - 12;
    afsk_acc[3] += (int16_t)y * afsk_c2200[g] - (int16_t)yo * afsk_c2200[ko];
    afsk_yr[afsk_r] = y;
    afsk_r = (afsk_r + 1) & 7; afsk_k1 = (afsk_k1 + 1) & 7; afsk_k2 = afsk_k2 < 47 ? afsk_k2 + 1 : 0;

    MAG(m1, afsk_acc[0], afsk_acc[1]); MAG(m2, afsk_acc[2], afsk_acc[3]);
    PEAK(afsk_pk1, m1); PEAK(afsk_pk2, m2);
    x = afsk_pk2 >> 8; a = (uint16_t)(m1 * (uint8_t)x) >> 1;   // uint8 x uint8: MUL AB; each <= 32512
    x = afsk_pk1 >> 8; b = (uint16_t)(m2 * (uint8_t)x) >> 1;

    for (s = 0; s < AFSK_NSL; s++) {
        d = s ? a > b : a > b && (uint16_t)((a - b) << 1) > b;   // 2a > 3b, kept in 16 bits
        ph = afsk_ph[s] + FS_STEP;
        t = ph < FS_STEP;                   // the carry: mid-bit, sample now
        if (d != afsk_dprev[s])             // a transition: pull the phase 1/4 of the way to 0x8000
            ph = ph >= 0x8000 ? ph - ((ph - 0x8000 + 3) >> 2) : ph + ((0x8000 - ph) >> 2);
        afsk_dprev[s] = d; afsk_ph[s] = ph;
        if (t) { afsk_bit(s, d == afsk_last[s]); afsk_last[s] = d; }
    }
}
#endif
