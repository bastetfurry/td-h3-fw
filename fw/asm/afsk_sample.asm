; asm-for: afsk.c afsk_sample
; c-sha256: ac7d21a9213d434c
; afsk_sample: one sample of P4.6's 1-bit audio through the AFSK 1200 demodulator (afsk.c has the C
; original). void afsk_sample(uint8_t n): n (how many of 8 reads were 1, 0-8) in DPL.
; Runs 9592 times a second, so it is written for time: the state is in internal RAM (afsk_acc, _x1,
; _x2, _y1, _y2, _r, _k1, _k2, _pk1, _pk2, _ph, _dprev, _last), only the ring of band-pass outputs
; (afsk_yr) is XDATA. Every register, B and DPTR are the caller's to lose; afsk_bit (C) is called
; at each bit clock with the decisions pushed. Signed products are never negated: each factor's
; magnitude and sign are found once a sample, the tables are sign-magnitude, and MUL AB's unsigned
; product is added or subtracted (TERM). 478 instructions a sample with a frame on (624 before that,
; with the products and MAG out of line: tools/afskbench.py).
; MIT (LICENSE) but for this part, adapted from Apache-2.0 code and changed for an 8051 and a 1-bit
; input (rewritten in our own form): the demodulator's chain (band-pass at 1625 Hz, Q 0.9; sliding
; I/Q correlators over one bit; magnitudes max + 3/8 min; per-tone peak AGC; slicers weighted 2:3,
; 1:1, 3:2, each with its DPLL) is Demod in Armel / F4HWN's APRS RX model,
; App/apps/aprsrx/test/model_rx.py in https://github.com/armel/uv-k1-k5v3-firmware-custom.
; Copyright 2026 Armel F4HWN. Licensed under the Apache License, Version 2.0 (the "License"); you may
; not use this part except in compliance with the License. You may obtain a copy of the License at
; http://www.apache.org/licenses/LICENSE-2.0 (also LICENSE-APACHE). Unless required by applicable law
; or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
; WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
; specific language governing permissions and limitations under the License.
; Facts the C keeps implicit and this relies on (tools/afskbench.py checks state after every sample):
;   the band-pass sum stays within +-30960 (|Y| <= 100), so it is 16 bits and >> 8 is its high byte;
;   y - yo is within +-200: its magnitude fits a byte; a sum (8 products of at most 100 x 7) stays
;   within +-5600, so max + 3/8 min of two is at most 7700, 13 bits: its >> 5 is at most 240 (the C's
;   clamp at 255 never acts) and fits a byte;
;   a peak only rises from 16.0, so t > peak is m > its high byte and the C's floor never acts.

	.module afsk_sample
	.optsdcc -mmcs51 --model-large
	.include "../build/asm.inc"	; ar0-ar7 (tools/asmcheck.py inc)

	.globl _afsk_sample
	.globl _afsk_bit
	.globl _afsk_bit_PARM_2
	.globl _afsk_acc
	.globl _afsk_x1
	.globl _afsk_x2
	.globl _afsk_y1
	.globl _afsk_y2
	.globl _afsk_yr
	.globl _afsk_r
	.globl _afsk_k1
	.globl _afsk_k2
	.globl _afsk_pk1
	.globl _afsk_pk2
	.globl _afsk_ph
	.globl _afsk_dprev
	.globl _afsk_last

; sum (16 bits, internal RAM) += or -= |c| x mag: A holds c sign-magnitude (bit 7 the sign, bits 0-2
; |c|, from c1200 or c2200 below), sgn 0x00 or 0x80 the sign of the factor whose magnitude is mag
; (both registers). Subtracting |p| leaves the same 16 bits as adding -p. Changes A, B.
	.macro	TERM mag, sgn, sum, ?neg, ?done
	xrl	a,sgn
	jb	acc.7,neg
	mov	b,mag
	mul	ab
	add	a,sum
	mov	sum,a
	mov	a,b
	addc	a,sum+1
	mov	sum+1,a
	sjmp	done
neg:	anl	a,#0x7F
	mov	b,mag
	mul	ab
	xch	a,sum
	subb	a,sum
	mov	sum,a
	mov	a,sum+1
	subb	a,b
	mov	sum+1,a
done:
	.endm

; A = (max + (min >> 2) + (min >> 3)) >> 5 of |i| and |q|, the 16-bit sums at i and q (internal RAM).
; Uses r1-r5. The >> 5 is the high byte << 3 or the low byte >> 5: max + 3/8 min <= 7700 (13 bits).
	.macro	MAG si, sq, ?pi, ?pq, ?big
	mov	r2,si
	mov	a,si+1
	jnb	acc.7,pi
	clr	c
	clr	a
	subb	a,r2
	mov	r2,a
	clr	a
	subb	a,si+1
pi:	mov	r3,a
	mov	r4,sq
	mov	a,sq+1
	jnb	acc.7,pq
	clr	c
	clr	a
	subb	a,r4
	mov	r4,a
	clr	a
	subb	a,sq+1
pq:	mov	r5,a
	clr	c
	mov	a,r2
	subb	a,r4
	mov	a,r3
	subb	a,r5
	jnc	big
	mov	a,r2
	xch	a,r4
	mov	r2,a
	mov	a,r3
	xch	a,r5
	mov	r3,a
big:	clr	c
	mov	a,r5
	rrc	a
	mov	r5,a
	mov	a,r4
	rrc	a
	mov	r4,a
	clr	c
	mov	a,r5
	rrc	a
	mov	r5,a
	mov	a,r4
	rrc	a
	mov	r4,a
	add	a,r2
	mov	r2,a
	mov	a,r5
	addc	a,r3
	mov	r3,a
	clr	c
	mov	a,r5
	rrc	a
	mov	r1,a
	mov	a,r4
	rrc	a
	add	a,r2
	swap	a
	rr	a
	anl	a,#0x07
	mov	r2,a
	mov	a,r1
	addc	a,r3
	swap	a
	rr	a
	anl	a,#0xF8
	orl	a,r2
	.endm

	.area CONST   (CODE)
; afsk.c's tables in sign-magnitude, each twice over, so an index 8 or 48 places on needs no wrap:
; c1200 at k1 and k1 + 6 (k1 <= 7), c2200 at k2, k2 + 28, + 36, + 40 (k2 <= 47)
c1200:	.db	0x07, 0x05, 0x00, 0x85, 0x87, 0x85, 0x00, 0x05
	.db	0x07, 0x05, 0x00, 0x85, 0x87, 0x85, 0x00, 0x05
c2200:	.db	0x07, 0x01, 0x87, 0x83, 0x06, 0x04, 0x85, 0x86, 0x04, 0x06, 0x82, 0x87
	.db	0x00, 0x07, 0x02, 0x86, 0x83, 0x06, 0x05, 0x84, 0x86, 0x03, 0x07, 0x81
	.db	0x87, 0x81, 0x07, 0x03, 0x86, 0x84, 0x05, 0x06, 0x84, 0x86, 0x02, 0x07
	.db	0x00, 0x87, 0x82, 0x06, 0x03, 0x86, 0x85, 0x04, 0x06, 0x83, 0x87, 0x01
	.db	0x07, 0x01, 0x87, 0x83, 0x06, 0x04, 0x85, 0x86, 0x04, 0x06, 0x82, 0x87
	.db	0x00, 0x07, 0x02, 0x86, 0x83, 0x06, 0x05, 0x84, 0x86, 0x03, 0x07, 0x81
	.db	0x87, 0x81, 0x07, 0x03, 0x86, 0x84, 0x05, 0x06, 0x84, 0x86, 0x02, 0x07
	.db	0x00, 0x87, 0x82, 0x06, 0x03, 0x86, 0x85, 0x04, 0x06, 0x83, 0x87, 0x01

	.area CSEG    (CODE)
_afsk_sample:
; x = n - 4 -> r7; d = x - x2 (-8..8) -> r6
	mov	a,dpl
	add	a,#-4
	mov	r7,a
	clr	c
	subb	a,_afsk_x2
	mov	r6,a
; sum = 670 d + 167 y1 - 89 y2 -> r3:r2. 670 |d| = 158 |d| + (2 |d| << 8), |d| <= 8
	jnb	acc.7,00101$
	cpl	a
	inc	a
00101$:	mov	r5,a
	mov	b,#158
	mul	ab
	mov	r2,a
	mov	a,r5
	add	a,r5
	add	a,b
	mov	r3,a
	mov	a,r6
	jnb	acc.7,00102$		; d < 0: the sum so far negated
	clr	c
	clr	a
	subb	a,r2
	mov	r2,a
	clr	a
	subb	a,r3
	mov	r3,a
00102$:	mov	a,_afsk_y1		; + 167 y1: 167 |y1| added, or subtracted when y1 < 0
	jb	acc.7,00103$
	mov	b,#167
	mul	ab
	add	a,r2
	mov	r2,a
	mov	a,b
	addc	a,r3
	mov	r3,a
	sjmp	00104$
00103$:	cpl	a
	inc	a
	mov	b,#167
	mul	ab			; C clear
	xch	a,r2
	subb	a,r2
	mov	r2,a
	mov	a,r3
	subb	a,b
	mov	r3,a
00104$:	mov	a,_afsk_y2		; - 89 y2: 89 |y2| subtracted, or added when y2 < 0
	jb	acc.7,00109$
	mov	b,#89
	mul	ab
	xch	a,r2
	subb	a,r2
	mov	r2,a
	mov	a,r3
	subb	a,b
	mov	r3,a
	sjmp	00119$
00109$:	cpl	a
	inc	a
	mov	b,#89
	mul	ab
	add	a,r2
	mov	r2,a
	mov	a,b
	addc	a,r3
	mov	r3,a
00119$:	mov	a,r3
; y = sum >> 8 (the high byte), at most +-100 -> r3
	xrl	a,#0x80			; offset: -128..127 as 0..255
	cjne	a,#229,00105$
00105$:	jc	00106$			; > 100
	mov	r3,#100
	sjmp	00108$
00106$:	cjne	a,#28,00107$
00107$:	jnc	00108$			; < -100
	mov	r3,#-100
; x2 = x1; x1 = x; y2 = y1; y1 = y
00108$:	mov	_afsk_x2,_afsk_x1
	mov	_afsk_x1,r7
	mov	_afsk_y2,_afsk_y1
	mov	_afsk_y1,r3
; yo = yr[r] -> r4; yr[r] = y
	mov	a,_afsk_r
	add	a,#_afsk_yr
	mov	dpl,a
	clr	a
	addc	a,#(_afsk_yr >> 8)
	mov	dph,a
	movx	a,@dptr
	mov	r4,a
	mov	a,r3
	movx	@dptr,a
; |y - yo| -> r5, its sign -> r6 as 0x00 or 0x80 (compared as offset bytes: the difference fits 8 bits
; unsigned); |y| -> r2, y's sign -> r7; |yo| -> r1, yo's sign turned -> r0 (yo's products are subtracted)
	xrl	a,#0x80
	mov	r5,a
	mov	a,r4
	xrl	a,#0x80
	xch	a,r5			; a = y + 128, r5 = yo + 128
	clr	c
	subb	a,r5
	mov	r5,a
	mov	r6,#0
	jnc	00110$
	cpl	a
	inc	a
	mov	r5,a
	mov	r6,#0x80
00110$:	mov	a,r3
	anl	a,#0x80
	mov	r7,a
	mov	a,r3
	jnb	acc.7,00111$
	cpl	a
	inc	a
00111$:	mov	r2,a
	mov	a,r4
	anl	a,#0x80
	xrl	a,#0x80
	mov	r0,a
	mov	a,r4
	jnb	acc.7,00112$
	cpl	a
	inc	a
00112$:	mov	r1,a
; acc[0] += (y - yo) c1200[k1]; acc[1] += (y - yo) c1200[(k1 + 6) & 7]   (the table repeats every 8)
	mov	dptr,#c1200
	mov	a,_afsk_k1
	movc	a,@a+dptr
	TERM	r5, r6, _afsk_acc
	mov	a,_afsk_k1
	add	a,#6
	movc	a,@a+dptr
	TERM	r5, r6, (_afsk_acc+2)
; acc[2] += y c2200[k2] - yo c2200[ko]; acc[3] += y c2200[g] - yo c2200[go]
; (ko = (k2 + 40) % 48, 8 places back; g = (k2 + 36) % 48; go = (k2 + 28) % 48: the doubled table)
	mov	dptr,#c2200
	mov	a,_afsk_k2
	movc	a,@a+dptr
	TERM	r2, r7, (_afsk_acc+4)
	mov	a,_afsk_k2
	add	a,#40
	movc	a,@a+dptr
	TERM	r1, r0, (_afsk_acc+4)
	mov	a,_afsk_k2
	add	a,#36
	movc	a,@a+dptr
	TERM	r2, r7, (_afsk_acc+6)
	mov	a,_afsk_k2
	add	a,#28
	movc	a,@a+dptr
	TERM	r1, r0, (_afsk_acc+6)
; r = (r + 1) & 7; k1 = (k1 + 1) & 7; k2 = k2 < 47 ? k2 + 1 : 0
	mov	a,_afsk_r
	inc	a
	anl	a,#7
	mov	_afsk_r,a
	mov	a,_afsk_k1
	inc	a
	anl	a,#7
	mov	_afsk_k1,a
	mov	a,_afsk_k2
	inc	a
	cjne	a,#48,00117$
	clr	a
00117$:	mov	_afsk_k2,a
; m1 = MAG(acc[0], acc[1]) -> r6, m2 = MAG(acc[2], acc[3]) -> r7
	MAG	_afsk_acc, (_afsk_acc+2)
	mov	r6,a
	MAG	(_afsk_acc+4), (_afsk_acc+6)
	mov	r7,a
; PEAK(pk1, m1): if m1 > pk1 high: pk1 += ((m1 << 8) - pk1) >> 5
	clr	c
	mov	a,(_afsk_pk1 + 1)
	subb	a,r6
	jnc	00020$
	clr	c
	clr	a
	subb	a,_afsk_pk1
	mov	r4,a			; (m << 8) - pk, low
	mov	a,r6
	subb	a,(_afsk_pk1 + 1)
	mov	r5,a			; high
	mov	a,r4			; >> 5: low = high << 3 | low >> 5, high = high >> 5
	swap	a
	rr	a
	anl	a,#0x07
	mov	r4,a
	mov	a,r5
	rl	a
	rl	a
	rl	a
	anl	a,#0xF8
	orl	a,r4
	add	a,_afsk_pk1
	mov	_afsk_pk1,a
	mov	a,r5
	swap	a
	rr	a
	anl	a,#0x07
	addc	a,(_afsk_pk1 + 1)
	mov	(_afsk_pk1 + 1),a
; PEAK(pk2, m2)
00020$:	clr	c
	mov	a,(_afsk_pk2 + 1)
	subb	a,r7
	jnc	00021$
	clr	c
	clr	a
	subb	a,_afsk_pk2
	mov	r4,a
	mov	a,r7
	subb	a,(_afsk_pk2 + 1)
	mov	r5,a
	mov	a,r4
	swap	a
	rr	a
	anl	a,#0x07
	mov	r4,a
	mov	a,r5
	rl	a
	rl	a
	rl	a
	anl	a,#0xF8
	orl	a,r4
	add	a,_afsk_pk2
	mov	_afsk_pk2,a
	mov	a,r5
	swap	a
	rr	a
	anl	a,#0x07
	addc	a,(_afsk_pk2 + 1)
	mov	(_afsk_pk2 + 1),a
; a = (m1 * (pk2 >> 8)) >> 1 -> r3:r2; b = (m2 * (pk1 >> 8)) >> 1 -> r5:r4
00021$:	mov	a,r6
	mov	b,(_afsk_pk2 + 1)
	mul	ab
	clr	c
	xch	a,b
	rrc	a
	xch	a,b
	rrc	a
	mov	r2,a
	mov	r3,b
	mov	a,r7
	mov	b,(_afsk_pk1 + 1)
	mul	ab
	clr	c
	xch	a,b
	rrc	a
	xch	a,b
	rrc	a
	mov	r4,a
	mov	r5,b
; r1 = the decisions: bit 0 slicer 0 (2a > 3b, as a > b && 2 (a - b) > b), bit 1 slicer 1 (a > b)
	mov	r1,#0
	clr	c
	mov	a,r4
	subb	a,r2
	mov	a,r5
	subb	a,r3			; b - a: borrow when a > b
	jnc	00030$
	mov	r1,#2
	clr	c			; r7:r6 = a - b
	mov	a,r2
	subb	a,r4
	mov	r6,a
	mov	a,r3
	subb	a,r5
	mov	r7,a
	mov	a,r6			; doubled; bit 16 drops, as the C's uint16_t (a - b <= 32512 anyway)
	add	a,r6
	mov	r6,a
	mov	a,r7
	rlc	a
	mov	r7,a
	clr	c
	mov	a,r4
	subb	a,r6
	mov	a,r5
	subb	a,r7			; b - 2 (a - b): borrow when 2 (a - b) > b
	jnc	00030$
	mov	r1,#3
00030$:
; slicer 0: ph = afsk_ph[0] + FS_STEP (8199), its carry the bit clock -> r4
	mov	a,_afsk_ph
	add	a,#0x07
	mov	r2,a
	mov	a,(_afsk_ph + 1)
	addc	a,#0x20
	mov	r3,a
	clr	a
	rlc	a
	mov	r4,a
	mov	a,r1
	anl	a,#1
	mov	r5,a			; d
	xrl	a,_afsk_dprev
	jz	00041$			; no transition
	lcall	00090$			; pull r3:r2 a quarter of the way to 0x8000
00041$:	mov	_afsk_dprev,r5
	mov	_afsk_ph,r2
	mov	(_afsk_ph + 1),r3
	mov	a,r4
	jz	00050$
; afsk_bit(0, d == last[0]); last[0] = d
	mov	a,r5
	xrl	a,_afsk_last
	mov	_afsk_last,r5
	add	a,#0xFF			; carry: d != last
	cpl	c
	clr	a
	rlc	a
	mov	dptr,#_afsk_bit_PARM_2
	movx	@dptr,a
	mov	dpl,#0
	push	ar1
	lcall	_afsk_bit
	pop	ar1
; slicer 1, the same with d = bit 1
00050$:	mov	a,(_afsk_ph + 2)
	add	a,#0x07
	mov	r2,a
	mov	a,(_afsk_ph + 3)
	addc	a,#0x20
	mov	r3,a
	clr	a
	rlc	a
	mov	r4,a
	mov	a,r1
	rr	a
	anl	a,#1
	mov	r5,a
	xrl	a,(_afsk_dprev + 1)
	jz	00051$
	lcall	00090$
00051$:	mov	(_afsk_dprev + 1),r5
	mov	(_afsk_ph + 2),r2
	mov	(_afsk_ph + 3),r3
	mov	a,r4
	jz	00060$
	mov	a,r5
	xrl	a,(_afsk_last + 1)
	mov	(_afsk_last + 1),r5
	add	a,#0xFF
	cpl	c
	clr	a
	rlc	a
	mov	dptr,#_afsk_bit_PARM_2
	movx	@dptr,a
	mov	dpl,#1
	lcall	_afsk_bit
00060$:	ret

; ph = ph >= 0x8000 ? ph - ((ph - 0x8000 + 3) >> 2) : ph + ((0x8000 - ph) >> 2)    ph in r3:r2;
; uses r6, r7 (the C's transition pull, out of line: at most a few per bit)
00090$:	mov	a,r3
	jnb	acc.7,00092$
	mov	a,r2			; v = (ph & 0x7FFF) + 3
	add	a,#3
	mov	r6,a
	mov	a,r3
	anl	a,#0x7F
	addc	a,#0
	clr	c			; v >> 2 (v < 0x8003: bit 15 clear)
	rrc	a
	xch	a,r6
	rrc	a
	xch	a,r6
	clr	c
	rrc	a
	mov	r7,a
	mov	a,r6
	rrc	a
	mov	r6,a
	clr	c			; ph -= v
	mov	a,r2
	subb	a,r6
	mov	r2,a
	mov	a,r3
	subb	a,r7
	mov	r3,a
	ret
00092$:	clr	c			; w = 0x8000 - ph
	clr	a
	subb	a,r2
	mov	r6,a
	mov	a,#0x80
	subb	a,r3
	clr	c			; w >> 2 (w <= 0x8000)
	rrc	a
	xch	a,r6
	rrc	a
	xch	a,r6
	clr	c
	rrc	a
	mov	r7,a
	mov	a,r6
	rrc	a
	mov	r6,a
	mov	a,r2			; ph += w
	add	a,r6
	mov	r2,a
	mov	a,r3
	addc	a,r7
	mov	r3,a
	ret
