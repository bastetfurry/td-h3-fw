#!/usr/bin/env python3
"""fw/afsk.c on the 8051, in tools/emu51.py, around tools/afskbench/bench.c, with the firmware's SDCC
flags: two images, afsk_sample from its C (C_AFSK_SAMPLE) and from fw/asm/afsk_sample.asm.

    tools/afskbench.py [--lockstep N] synth|capture.n.gz [...]   (tools/afskmodel.py load)

For each capture: the frames of both images against tools/afskmodel.py's (all must agree), the state
both leave, and the instructions a sample (a sample is 104 us at 9592 Hz; this 1T core takes ~2
clocks an instruction, 48 a us). --lockstep N also compares the demodulator's whole state after each
of the first N samples (slow: CPython steps both images in turn), plus N random samples.
"""
import os
import random
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import afskmodel  # noqa: E402
import emu51      # noqa: E402

FW = os.path.join(HERE, '../fw')
OUT = os.path.join(FW, 'build/afskbench')
SDCC = os.path.join(HERE, '../.toolchain/sdcc-4.5.0/bin')
FLAGS = ('-mmcs51 --model-large --opt-code-size --std-c11 --noinvariant --fomit-frame-pointer '
         '--no-xinit-opt --xram-size 4096 --iram-size 256').split()
IN, IDX, RES = 0x1000, 0xF000, 0xF800
# The state afsk_sample owns: (symbol, bytes, D internal RAM or X XDATA)
STATE = [('_afsk_acc', 8, 'D'), ('_afsk_r', 1, 'D'), ('_afsk_k1', 1, 'D'), ('_afsk_k2', 1, 'D'),
         ('_afsk_pk1', 2, 'D'), ('_afsk_pk2', 2, 'D'), ('_afsk_ph', 4, 'D'), ('_afsk_dprev', 2, 'D'),
         ('_afsk_last', 2, 'D'), ('_afsk_x1', 1, 'D'), ('_afsk_x2', 1, 'D'), ('_afsk_y1', 1, 'D'),
         ('_afsk_y2', 1, 'D'), ('_afsk_yr', 8, 'X'), ('_afsk_len', 2, 'X')]


def build(asm):
    """One image: afsk_sample from the asm, or from its C. -> (code, {symbol: address})."""
    d = os.path.join(OUT, 'asm' if asm else 'c')
    os.makedirs(d, exist_ok=True)
    run = lambda *a: subprocess.run(list(a), cwd=d, check=True)
    inc = ['-I', FW]
    run(SDCC + '/sdcc', *FLAGS, *inc, *([] if asm else ['-DC_AFSK_SAMPLE']), '-c', os.path.join(FW, 'afsk.c'), '-o', 'afsk.rel')
    run(SDCC + '/sdcc', *FLAGS, *inc, '-c', os.path.join(HERE, 'afskbench/bench.c'), '-o', 'bench.rel')
    rels = ['bench.rel', 'afsk.rel']
    if asm:
        subprocess.run([sys.executable, os.path.join(HERE, 'asmcheck.py'), 'inc'], check=True)   # fw/build/asm.inc
        run(SDCC + '/sdas8051', '-plosgffw', 'afsk_sample.rel', os.path.join(FW, 'asm/afsk_sample.asm'))
        rels.append('afsk_sample.rel')
    run(SDCC + '/sdcc', *FLAGS, *rels, '-o', 'bench.ihx')
    run(SDCC + '/makebin', '-p', 'bench.ihx', 'bench.bin')
    syms = {}
    for line in open(os.path.join(d, 'bench.map')):
        m = re.match(r'(?:[A-Z]:)?\s+([0-9A-F]{8})\s+(_\w+)\s', line)   # the area letter is on its first symbol only
        if m:
            syms[m.group(2)] = int(m.group(1), 16)
    return open(os.path.join(d, 'bench.bin'), 'rb').read(), syms


def sizes(asm):
    d = os.path.join(OUT, 'asm' if asm else 'c')
    size = {}
    for rel in ('afsk.rel', 'afsk_sample.rel'):
        p = os.path.join(d, rel)
        if os.path.exists(p) and (asm or rel == 'afsk.rel'):
            for line in open(p):
                f = line.split()
                if f[:1] == ['A'] and f[1] in ('CSEG', 'CONST', 'XSEG', 'DSEG'):
                    size[f[1]] = size.get(f[1], 0) + int(f[3], 16)
    return size


def start(code, n):
    cpu = emu51.CPU(code)
    cpu.xram[IN], cpu.xram[IN + 1] = len(n) & 255, len(n) >> 8
    cpu.xram[IN + 2:IN + 2 + len(n)] = bytes(n)
    return cpu


def state(cpu, syms):
    out = []
    for name, size, space in STATE:
        a = syms[name]
        mem = cpu.iram if space == 'D' else cpu.xram
        out.append((name, bytes(mem[a:a + size])))
    return out


def finish(cpu):
    if not cpu.run(600_000_000, until=lambda: cpu.xram[RES + 0x7F] == 0xA5):
        sys.exit('the bench never finished')
    return list(cpu.xram[RES + 1:RES + 1 + cpu.xram[RES]])


def lockstep(img, n, label):
    cpus = [start(code, n) for code, _ in img]
    for i in range(1, len(n) + 1):
        for cpu in cpus:
            if not cpu.run(100_000, until=lambda: cpu.xram[IDX] | cpu.xram[IDX + 1] << 8 == i):
                sys.exit('%s: sample %d never finished' % (label, i))
        sc, sa = state(cpus[0], img[0][1]), state(cpus[1], img[1][1])
        if sc != sa:
            diff = [(c[0], c[1].hex(), a[1].hex()) for c, a in zip(sc, sa) if c != a]
            sys.exit('%s: sample %d (n=%d): C and asm differ: %s' % (label, i, n[i - 1], diff))
    print('   lockstep %-14s %d samples: the same state after every one' % (label, len(n)))


def main():
    args = sys.argv[1:]
    ls = 0
    if args[:1] == ['--lockstep']:
        ls, args = int(args[1]), args[2:]
    img = [build(False), build(True)]
    for asm in (False, True):
        print('%-4s afsk_sample: afsk.c%s: %s' % ('asm' if asm else 'C', ' + afsk_sample.asm' if asm else '',
              ' '.join('%s %d B' % kv for kv in sizes(asm).items())))
    bad = 0
    for path in args:
        n = afskmodel.load(path)
        d = afskmodel.Demod51()
        for v in n:
            d.sample(v)
        want = [len(f) for f in d.frames]
        res = []
        for code, syms in img:
            cpu = start(code, n)
            got = finish(cpu)
            res.append((got, cpu.icount / len(n), state(cpu, syms)))
        ok = res[0][0] == want and res[1][0] == want and res[0][2] == res[1][2]
        bad += not ok
        print('%-16s frames C %s asm %s (model %s)%s  instructions a sample: C %.0f, asm %.0f (%.0f-%.0f%% of the CPU)' % (
            os.path.basename(path), res[0][0], res[1][0], want, '' if ok else '  DIFFERENT', res[0][1], res[1][1],
            res[1][1] * 2 / 48 / 104.25 * 100, res[1][1] * 3 / 48 / 104.25 * 100))
        if ls:
            lockstep(img, n[:ls], os.path.basename(path))
    if ls:
        rnd = random.Random(1)
        lockstep(img, [rnd.choice((0, 8, 8, 0, rnd.randrange(9))) for _ in range(ls)], 'random')
    sys.exit(1 if bad else 0)


if __name__ == '__main__':
    main()
