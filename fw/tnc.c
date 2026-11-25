// The TNC (make tnc): MENU 31 TNC, or long 8 (that build has no FM radio). AX.25 at AFSK 1200 heard on the
// selected slot's frequency, every frame (APRS's UI frames and connected mode's I, S and U frames
// alike: afsk.c is_ax25), goes to the computer or phone on USB-C as KISS, 38400 8N1 (APRSdroid,
// Xastir, Direwolf's KISS clients, a BBS terminal), and the frames they send go out on the air: on the slot's TX
// frequency through radio_tx, so only within TX BANDS, FM, with the factory power calibration, as PTT.
//
// Receive: the audio comes in on P4.6 (BEEP), 1 bit at a time: docs/HARDWARE.md "P4.6 as an audio input".
//  - The BK4819 puts the receiver's audio out whatever the squelch says, at full gain (REG_48 B3FF)
//    and without de-emphasis (REG_2B bit 8), so 2200 Hz comes out as strong as 1200 Hz.
//  - P4.6 is a plain input that C47 couples to AUDIO. Its DC level is C47's charge: a 4 us drive moves it
//    one step. On the way in it walks up to the input's threshold; while running, every 16 samples it
//    steps toward reading half ones, which follows a carrier's DC.
//  - 8 reads of P4.6 spread over each 104 us sample make n = 0-8 for the demodulator (afsk.c, its hot
//    path fw/asm/afsk_sample.asm), 9592 samples a second. Timer2 (high priority) interrupts for each
//    read, 13 us apart: 52 counts at Fsys/12, 53 every eighth (417 a sample). The eighth hands n over
//    and sets TF1 by software: Timer1, stopped while receiving (the beep's, so no beeps meanwhile),
//    then interrupts at low priority and demodulates (tnc_isr). Reads 1.5 us apart in one go, as
//    before, all but repeated each other through EARO's ~1.25 kHz RC: on WA8LMF track 1 the model
//    (tools/afsksim.py) decodes 787 frames with them spread, 608 with them together, and the waits
//    between them had taken a third of the interrupt's time.
//  - A frame goes to the host as KISS from the interrupts: tnc_isr starts it the sample it's found and
//    uart1_isr sends each next byte as the last one leaves (kiss_next). The slicer meanwhile collects on
//    into the same buffer, ~12x slower than KISS reads it (afsk.h), so a frame right behind another, a
//    digipeater's repeat on the closing flag, is kept: WA8LMF track 1 566 -> 608 frames. The main loop's
//    KISS had held the slicer for ~40 ms and more.
// KISS from the host: data (port 0), and ACKMODE (G8BPQ's command 0x0C: two id bytes before the frame,
// sent back as C0 0C id id C0 once its last bit is out); TXDELAY, P, SLOTTIME, TXTAIL (1-4), FULLDUPLEX
// (5: send without waiting for a clear channel), SetHardware's text queries TNC: and TXBUF: (Dire Wolf's).
// Anything else (other ports, RETURN, unknown commands) is skipped; a broken escape drops its frame.
// Transmit: uart1_isr hands each byte to tnc_rx, which parses KISS into a queue of TXQ_N frames of up to
// 330 B (AX.25's most), so a connected-mode window (MAXFRAME up to 4) all goes out; a frame while the
// queue is full is dropped. Both slicers listen meanwhile. When the channel is clear (no packet
// coming in by afsk.c's DCD, no carrier by SQUELCH: carrier()), p-persistence (KISS P, SLOTTIME) picks the moment; then Timer1 becomes the 1200 Hz bit clock: TXDELAY flags, the frames
// bit-stuffed with their FCS, one flag between two (every frame queued by the time the one before ends
// goes in the same key-up), TXTAIL's flags after the last (3 at least), NRZI on the BK4819's tone
// generator 1 (a 0 swaps 1200 and 2200 Hz), the mic muted.
#include "fw.h"
#include "afsk.h"

#define BIT_RELOAD (65536u - 3333u)         // 1200 Hz: 3333 1/3 counts, a count more every third bit
#define STOPPED 2                           // counts Timer1 misses while its reload is added (stopped)
// Tone 1's gain (REG_70 14:8) for 1200 and 2200 Hz, per the slot's bandwidth; 2200 Hz a little stronger
// (twist: a KISS TNC on the other end decoded twist -2 2 of 15, 0 13/15, +2 to +4 all). Measured on an
// RT-890 TNC that records its audio input (2026-11-25): on wide, a 2200 Hz gain over 96 overloads the
// tone path: the tone collapses (-8 dB against 1200 Hz) and the carrier steps at every tone swap, a
// square wave at the swap rate that receivers with a high-pass shrug off and the RT-890 (none) can't
// decode (0 of 3 at 96/104 up to 96/127, 3 of 3 at 96/96, 80/96, 72/96, 64/85). On narrow 127 is clean.
// The levels below put both tones at about a UV-K5's on that receiver.
#define TX_L1_WIDE   80                     // wide: 1200 Hz
#define TX_L2_WIDE   96                     //       2200 Hz, the most before the overload (+1.6 dB)
#define TX_L1_NARROW 112                    // narrow: 1200 Hz
#define TX_L2_NARROW 127                    //         2200 Hz (+1.1 dB)
#define FEND 0xC0
#define FESC 0xDB

volatile __bit tnc_run;
__bit tnc_bt;                               // KISS over Bluetooth (MENU 49 on: BT_ON) instead of USB-C
static __bit tnc_tx;                        // Timer1 is the bit clock (else the sample clock)
static volatile __bit tx_done;              // the key-up's bits are all out
static __xdata uint8_t buf0[AFSK_MAX], buf1[AFSK_MAX];   // the slicers' frames
// The frames from the host: slots used as a ring, the oldest at q_head. tnc_rx fills slot q_head + q_n
// and counts it in q_n at its closing FEND; tx_tick frees q_head when its last byte is out. Both run
// from interrupts of one priority (UART0/1 and Timer1), so neither cuts into the other; main reads.
#define TXQ_N 4                             // a window of 4 (MAXFRAME 4, the usual): XRAM is 4 KB (docs/HARDWARE.md,
                                            // MCU). A power of 2: the ring's indices wrap with a mask
static __xdata uint8_t txq[TXQ_N][AFSK_MAX];   // 1328 B
static __xdata uint16_t q_len[TXQ_N];       // each slot's frame, bytes without the FCS
static __xdata uint8_t q_ack[TXQ_N][3];     // ACKMODE: 1 and the id to send back, or 0
static __xdata uint8_t acks[TXQ_N][2];      // ids of frames sent, waiting to go back to the host (a ring)
static volatile __xdata uint8_t a_head, a_n;
static volatile __xdata uint8_t q_head, q_n;   // the oldest frame's slot; frames complete in the queue
static volatile __xdata uint16_t q_bytes;   // their bytes (SetHardware TXBUF:)
static __xdata uint8_t * __xdata k_p;       // the slot tnc_rx fills
#define wrap(i) ((i) & (TXQ_N - 1))         // a slot index past the end, back to the start
static __xdata uint8_t k_slot, k_id;        // its index; ACKMODE id bytes still to come (2, 1, 0)
static __data uint8_t ones, si;             // the servo: reads that were 1 over 16 samples; sample count
static __data uint8_t rd_n, rd_k, rd_mail;  // t2_isr: ones so far, reads left in this sample, the last n
static __bit rd_long;                       // this period was the 53-count one: back to 52
#define RLD_SHORT 0xCC                      // RLDL: 65536 - 52 (RLDH 0xFF)
#define RLD_LONG  0xCB                      // 65536 - 53

extern __xdata char buf[48];                // main.c: a text line
__xdata char *u2s(__xdata char *p, uint16_t v, uint8_t width) __reentrant;
__xdata char *cat(__xdata char *d, const __code char *s) __reentrant;
void pad_to(__xdata char *p, uint8_t n) __reentrant;
void text1(uint8_t x, uint8_t y, uint8_t c) __reentrant;
extern __bit open, tx, locked;              // main.c: a signal in, sending (lights() keeps the light on for both),
                                            // KEY LOCK (hold *)
static __bit spk;                           // key #: the speaker on (radio_tx turns it off: on again after)
uint16_t batt_cal(void);                    // main.c: the battery in mV, BATTERY CAL applied
uint8_t batt_pct(uint16_t mv) __reentrant;

// udelay's own copy for tnc_isr and what it calls: udelay keeps its count in XDATA (not reentrant), so
// an interrupt inside the main loop's udelay left it 0 there, and --i ran 255 more: the emulator's TNC
// test found the main loop never got through key_scan.
static void idelay(void)
{
    volatile uint8_t i = 6;
    while (--i) ;
}

static void pulse(uint8_t hi)               // P4.6 driven to hi ~4 us: C47's charge one step that way
{
    BEEP = hi; P4TRIS |= 0x40; idelay(); idelay(); idelay(); P4TRIS &= ~0x40; BEEP = 0;
}

static void reload(uint16_t r)              // the next overflow r counts after this one, however late this ran
{
    uint16_t t;
    TCON &= ~0x40;
    t = ((uint16_t)TH1 << 8 | TL1) + (uint16_t)(r + STOPPED);
    TH1 = t >> 8; TL1 = t;
    TCON |= 0x40;
}

// ---- the bits out, from Timer1 at 1200 Hz ----
static __xdata uint8_t tx_phase, tx_left, tx_bit, tx_ones, tx_tone, tx_frac;
static __xdata uint8_t tx_l1, tx_l2;        // this key-up's tone gains (the slot's bandwidth)
static __xdata uint8_t tx_tail;             // flags after the last frame: TXTAIL's, 3 at least
static __xdata uint16_t tx_i, tx_n;         // the frame's next byte; its bytes with the FCS
static __xdata uint8_t * __xdata tx_p;      // the frame going out: txq[q_head]
static volatile __xdata uint16_t tx_nlen;   // the next frame's bytes with its FCS, once send() has computed
                                            // it (0: not yet): it follows after one flag
static volatile __xdata uint8_t tx_cnt;     // frames out in this key-up
static void tx_tick(void)
{
    uint8_t b;
    if (++tx_frac == 3) { tx_frac = 0; reload(BIT_RELOAD - 1); } else reload(BIT_RELOAD);
    if (tx_phase == 1) {                    // the frame: LSB first, a 0 after five 1s
        if (tx_ones == 5) { b = 0; tx_ones = 0; }
        else {
            b = tx_p[tx_i] >> tx_bit & 1;
            tx_ones = b ? tx_ones + 1 : 0;
            if (++tx_bit == 8) {
                tx_bit = 0;
                if (++tx_i == tx_n) {       // its slot is free again
                    tx_phase = 2; tx_left = tx_tail;
                    if (q_ack[q_head][0]) {   // ACKMODE: its id back to the host (kiss_next)
                        uint8_t a = wrap(a_head + a_n);
                        acks[a][0] = q_ack[q_head][1]; acks[a][1] = q_ack[q_head][2]; a_n++;
                    }
                    q_bytes -= tx_n - 2;
                    q_head = wrap(q_head + 1); q_n--; tx_cnt++;
                }
            }
        }
    } else if (tx_phase < 3) {              // 0 TXDELAY's flags, 2 the closing ones: never stuffed
        if (tx_phase == 2 && tx_ones == 5) { b = 0; tx_ones = 0; }   // the frame's last stuffing first
        else {
            b = 0x7E >> tx_bit & 1;
            if (++tx_bit == 8) {
                tx_bit = 0;
                if (tx_phase == 2 && tx_nlen) {   // the next frame after this one flag
                    tx_p = txq[q_head]; tx_n = tx_nlen; tx_nlen = 0;
                    tx_i = 0; tx_ones = 0; tx_phase = 1;
                } else if (!--tx_left) tx_phase++;
            }
        }
    } else { tx_done = 1; return; }
    if (!b) {                               // NRZI: a 0 swaps the tones, each at its own gain (twist)
        tx_tone ^= 1;
        bk_write(0x71, tx_tone ? TONE_HZ(2200) : TONE_HZ(1200));
        bk_write(0x70, 0x8000 | (uint16_t)(tx_tone ? tx_l2 : tx_l1) << 8);
    }
}

// ---- KISS to the host, from the interrupts: FEND, port 0 data, the frame (no FCS) with FEND and FESC
// escaped, FEND; or an ACKMODE ack, FEND 0C id id FEND. Timer1 and UART1 share a priority, so neither
// cuts into the other's half.
static __xdata uint8_t k_out;               // 0 idle, 1 the command byte next, 2 data, 3 an escape's second byte,
static __xdata uint16_t k_i, k_n;           // 4 the closing FEND sent: done at its TI; the next byte; how many
static __xdata uint8_t * __xdata k_src;     // a frame (afsk_frame), an ack's id (acks) or a query's answer
static __xdata uint8_t k_kind;              // its KISS command: 0x00 a frame, 0x0C an ack, 0x06 an answer
static __xdata char hw_q[2];                // SetHardware's text from the host: its first 2 bytes
static __xdata uint8_t hw_i, hw_n;          // how many came; the answer's length (0: none waiting)
static __xdata char hw_r[24];               // the answer: "TNC:TD-H3 0.68.0" or "TXBUF:01234"
static __xdata uint8_t k_esc;               // the escaped byte's second half
static volatile __xdata uint8_t k_count;    // frames sent to the host: the main loop counts and shows them
static __xdata uint8_t src[7];              // the last one's source address (bytes 7-13): heard()

#define KOUT(c) do { if (tnc_bt) SBUF0 = (c); else SBUF1 = (c); } while (0)
static void hw_start(void);

// The Bluetooth module buffers about 264 bytes and sends them on once UART0 goes quiet; it takes
// them in faster than it gets them over the air, so a longer run of bytes overflows it (frames over
// 264 B came through cut short or not at all). After every BT_PACE bytes UART0 rests 5-10 ms (two
// Timer0 ticks; t0_isr resumes it), and the module sends on what it has. Every length then comes
// through whole, for 6 % of 9600's rate.
#define BT_PACE 128
static __xdata uint8_t bt_cnt;              // bytes since the last rest
volatile __data uint8_t bt_wait;            // ticks of rest left: t0_isr counts them down
void tnc_bt_ti(void)                        // from uart0_isr: its last byte out
{
    if (++bt_cnt == BT_PACE) { bt_cnt = 0; bt_wait = 2; return; }
    kiss_next();
}
void kiss_next(void)                        // from uart1_isr or uart0_isr (the port's), its last byte out
{
    uint8_t c;
    switch (k_out) {
    case 1: KOUT(k_kind); k_out = 2; k_i = 0; return;
    case 3: KOUT(k_esc); k_out = 2; return;
    case 2:
        if (k_i == k_n) { KOUT(FEND); k_out = 4; return; }
        c = k_src[k_i++];
        if (c == FEND) { k_esc = 0xDC; k_out = 3; c = FESC; }
        else if (c == FESC) { k_esc = 0xDD; k_out = 3; }
        KOUT(c);
        return;
    case 4:
        k_out = 0;
        if (k_kind == 0x0C) { a_head = wrap(a_head + 1); a_n--; }
        else if (k_kind == 0x06) hw_n = 0;
        else { k_count++; afsk_release(); }
        if (hw_n) hw_start();               // an answer waits: now, even while sending (tnc_isr doesn't run then)
    }
}

static void out_go(void)                    // k_src, k_n, k_kind set; the UART is idle (k_out 0)
{
    k_out = 1;
    KOUT(FEND);
}

static void kiss_start(void)                // afsk_len: a frame to send
{
    uint8_t i;
    for (i = 0; i < 7; i++) src[i] = afsk_frame[7 + i];
    k_src = afsk_frame; k_n = afsk_len; k_kind = 0x00;
    out_go();
}

// One read of P4.6, at high priority: it interrupts the demodulator. Every instruction here leaves
// A, the registers and PSW alone (INC, DJNZ and ANL on direct bytes, bit moves), so nothing is saved.
// The eighth read: n to rd_mail, Timer1's flag set (tnc_isr runs once this returns), the next period
// 53 counts.
void t2_isr(void) __interrupt(5) __naked
{
    __asm
    anl     0xC9,#0x7F                      ; TF2 off (T2IF)
    jnb     0xC6,00001$                     ; P4.6
    inc     _rd_n
00001$:
    djnz    _rd_k,00002$
    mov     _rd_k,#8
    mov     _rd_mail,_rd_n
    mov     _rd_n,#0
    mov     0xCA,#RLD_LONG                  ; RLDL
    setb    _rd_long
    setb    0x8F                            ; TF1: the sample to tnc_isr
    reti
00002$:
    jbc     _rd_long,00003$
    reti
00003$:
    mov     0xCA,#RLD_SHORT
    reti
    __endasm;
}

void tnc_isr(void)
{
    uint8_t n = rd_mail;                    // the next sample's is 104 us away
    if (tnc_tx) { tx_tick(); return; }
    ones += n;
    if (!(++si & 15)) {                     // 16 samples, 128 reads: aim at 64 ones
        IE &= ~0x20;                        // no read while the pulse drives the pin: it waits ~4 us
        if (ones < 48) pulse(1); else if (ones > 80) pulse(0);
        IE |= 0x20;
        ones = 0;
    }
    afsk_sample(n);
    if (!k_out) {
        if (afsk_len) kiss_start();
        else if (a_n) { k_src = acks[a_head]; k_n = 2; k_kind = 0x0C; out_go(); }
        else if (hw_n) hw_start();
    }
}

// ---- KISS from the host, in uart1_isr (Timer1's priority: afsk_bit can't run meanwhile) ----
static __xdata uint8_t k_state, k_cmd;      // 0 till a FEND, 1 its command, 2 data, 3 after FESC, 4 a
static __xdata uint16_t k_len;              // parameter's byte, 5 skipping to the next FEND
// SetHardware queries, as Dire Wolf answers them (its kiss_frame.c): "TNC:" gets "TNC:TD-H3 <version>",
// "TXBUF:" the bytes still waiting to go out, "TXBUF:00660", so a host can pace its frames. Anything else
// is ignored; "TN" and "TX" tell the two apart. Only the UART interrupts run these (one priority):
// their statics are theirs alone.
static void hw_answer(void)
{
    __xdata char *p;
    if (hw_n || hw_q[0] != 'T') return;     // the last answer still waits; or not a query
    if (hw_q[1] == 'X') p = u2s(cat(hw_r, "TXBUF:"), q_bytes, 5) + 5;   // hosts read a number
    else if (hw_q[1] == 'N') p = cat(cat(hw_r, "TNC:TD-H3 "), VERSION);
    else return;
    hw_n = p - hw_r;
    if (!k_out) hw_start();
}
static void hw_start(void) { k_src = (__xdata uint8_t *)hw_r; k_n = hw_n; k_kind = 0x06; out_go(); }

// Is the channel in use? A packet coming in (afsk.c's DCD: 3 flags in a row, ~25 ms into a preamble,
// held ~170 ms after its last byte; it hears packets too weak to decode, and never lit on hiss or a
// carrier in the emulator or the radio's captures), or a carrier as SQUELCH sets it: the chip's squelch
// (1-9); that and the S-meter's level (S1-S9, as the home screen, 3 dB hysteresis); at SQUELCH 0, a
// signal FLOOR_DB over the band's noise floor, tracked here: down 1 dB each 50 ms the RSSI reads under
// it, up 1 dB each 2 s it reads over (a carrier that never ends stops counting after a while).
#define FLOOR_DB 12
#define FLOOR_START (-110)                  // after a retune: it walks down to the real floor in about a second
static __xdata int16_t floor_db;            // bk_dbm's range, -160 and up: an int8_t wrapped under -128
static __xdata uint16_t t_fdown, t_fup;
static uint8_t carrier(uint8_t was)         // was: the last answer (the S-meter's hysteresis)
{
    int16_t s = tnc_sql_dbm(), d;
    if (s == 1) return bk_sql_open();
    d = bk_dbm();
    if (s) return bk_sql_open() && d >= s - (was ? 3 : 0);
    if (d < floor_db && (uint16_t)(now() - t_fdown) >= 10) { t_fdown = now(); floor_db--; }
    if (d > floor_db && (uint16_t)(now() - t_fup) >= 400) { t_fup = now(); floor_db++; }
    return d >= floor_db + FLOOR_DB;
}
static void floor_reset(void) { floor_db = FLOOR_START; t_fdown = t_fup = now(); }

// p-persistence's dice: a 16-bit Galois LFSR (period 65535) stirred with Timer0's low byte.
static __xdata uint16_t lfsr;
static uint8_t rnd(void)
{
    lfsr = lfsr >> 1 ^ (lfsr & 1 ? 0xB400u : 0);
    return (uint8_t)lfsr ^ TL0;
}

static __xdata uint8_t txdelay, persist, slottime, txtail, duplex;   // KISS 1-5: TXDELAY, TXTAIL, SLOTTIME
                                            // (10 ms), P (/256), FULLDUPLEX (send whatever is on the channel)
static __xdata uint16_t frames, sent, dropped, refused;
void tnc_rx(uint8_t c)
{
    switch (k_state) {
    case 1:
        if (c == FEND) break;               // FEND FEND
        k_cmd = c;
        if (c == 0 || c == 0x0C) {          // data, port 0; with ACKMODE's id first
            if (q_n == TXQ_N) { if (++dropped == 0) dropped--; k_state = 5; break; }   // the queue is full
            k_slot = wrap(q_head + q_n);    // stays this slot: tx_tick moves q_head and q_n together
            k_p = txq[k_slot];
            q_ack[k_slot][0] = c != 0;
            k_id = c ? 2 : 0;
            k_len = 0; k_state = 2;
        } else if (c == 6) { hw_i = 0; hw_q[0] = 0; k_state = 6; }   // SetHardware: a text query
        else k_state = c >= 1 && c <= 5 ? 4 : 5;   // TXDELAY..FULLDUPLEX: their byte; others skipped
        break;
    case 3:                                 // after FESC: TFEND or TFESC, nothing else
        if (c == FEND) { k_state = 1; break; }   // a frame cut short: the FEND starts the next
        if (c != 0xDC && c != 0xDD) { k_state = 5; break; }   // a broken escape: the frame is dropped
        c = c == 0xDC ? FEND : FESC;
        k_state = 2;
        goto put;
    case 2:
        if (c == FEND) {
            if (k_len >= 15) {              // two addresses and a control byte at least
                q_len[k_slot] = k_len;
                q_bytes += k_len;
                q_n++;
            }
            k_state = 1;
            break;
        }
        if (c == FESC) { k_state = 3; break; }
    put:
        if (k_id) { q_ack[k_slot][3 - k_id--] = c; break; }   // ACKMODE's two id bytes
        if (k_len < AFSK_MAX - 2) k_p[k_len++] = c;   // room for the FCS
        else { if (++dropped == 0) dropped--; k_state = 5; }
        break;
    case 4:
        if (k_cmd == 1) txdelay = c; else if (k_cmd == 2) persist = c; else if (k_cmd == 3) slottime = c;
        else if (k_cmd == 4) txtail = c; else duplex = c;
        k_state = 5;
        break;
    case 6:
        if (c != FEND) { if (hw_i < 2) hw_q[hw_i++] = c; break; }
        hw_answer();
        k_state = 1;
        break;
    default:                                // 0, 5
        if (c == FEND) k_state = 1;
    }
}

static __xdata char last[10];               // the last frame heard: its source callsign, "N0CALL-7"
// The send side's state, a word in a fixed field on its own line (it never shifts what's around it):
// IDLE, WAIT (frames queued, the channel not yet free), KEYING, SENDING, REFUSED (outside TX BANDS:
// it stays until a frame goes out)
enum { TX_IDLE, TX_WAIT, TX_KEYING, TX_SENDING, TX_REFUSED };
static __xdata uint8_t tx_st, tx_drawn;     // the state; the one on the screen (0xFF: draw it)

static void heard(void)                     // last = the frame's source: address 2, src (kiss_start)
{
    uint8_t i, c, n = 0, ssid;
    EA = 0;
    for (i = 0; i < 7; i++) last[i] = src[i];   // a copy: the next frame may overwrite src
    EA = 1;
    ssid = last[6] >> 1 & 15;
    for (i = 0; i < 6; i++) if ((c = last[i] >> 1) != ' ') last[n++] = c;
    if (ssid) { last[n++] = '-'; if (ssid > 9) { last[n++] = '1'; ssid -= 10; } last[n++] = '0' + ssid; }
    last[n] = 0;
}

static __xdata int16_t sig, last_dbm;       // the RSSI while the squelch is open; the last frame's, dBm

// A line of text holds 20 characters: x 4 to 124, 6 pixels each (a 21st loses its last column).
#define COLS 20
// 0-65535 without leading zeros: right-aligned in 5 places (pad), or just its digits; returns the end
static __xdata char *count(__xdata char *p, uint16_t v, uint8_t pad)
{
    uint8_t i, j;
    u2s(p, v, 5);
    for (i = 0; i < 4 && p[i] == '0'; i++) p[i] = ' ';
    if (pad) return p + 5;
    for (j = 0; p[i]; ) p[j++] = p[i++];
    p[j] = 0;
    return p + j;
}

static void tx_line(void)                   // "TX SENDING": the state word in place, its colour
{
    static const __code char *const __code w[] = {"IDLE   ", "WAIT   ", "KEYING ", "SENDING", "REFUSED"};
    static const __code uint8_t c[] = {P_DIM, P_DIM, P_HI, P_HI, P_RED};
    if (tx_st == tx_drawn) return;
    tx_drawn = tx_st;
    lcd_text(4, 92, "TX", 1, P_DIM);
    cat(buf, w[tx_st]);
    text1(4 + 6 * 3, 92, c[tx_st]);
}

static void draw(void)
{
    __xdata char *p;
    p = cat(buf, "RX ");                    // "RX     1  TX     0": both counts to 65535, columns that stay put
    p = count(p, frames, 1);
    p = cat(p, "  TX ");
    count(p, sent, 1);
    text1(4, 64, P_FG);
    p = buf;                                // "LAST N0CALL-9 -43dBm": LAST only if all of it fits
    if (last[0]) {
        uint8_t n = 0, d = last_dbm < 0 ? -last_dbm : last_dbm;
        while (last[n]) n++;
        if (5 + n + 1 + (last_dbm < 0) + (d > 99 ? 3 : d > 9 ? 2 : 1) + 3 <= COLS) p = cat(p, "LAST ");
        for (uint8_t i = 0; i < n; i++) *p++ = last[i];
        *p++ = ' ';
        if (last_dbm < 0) *p++ = '-';
        p = u2s(p, d, d > 99 ? 3 : d > 9 ? 2 : 1);
        while (*p) p++;
        p = cat(p, "dBm");
    } else p = cat(p, "NOTHING HEARD YET");
    pad_to(p, COLS);
    text1(4, 78, P_HI);
    tx_line();
}

// The status bar's battery (main.c draw_status), top right: percent and a 12x7 case filled in 4 steps,
// red at 20 % or less. Not while sending: the voltage sags under the transmitter's load.
static void battery(void)
{
    uint8_t pct = batt_pct(batt_cal()), c = pct <= 20 ? P_RED : P_DIM, n;
    n = num(buf, pct) - buf;
    buf[n] = '%'; buf[n + 1] = 0;
    lcd_fillp(70, 0, 34, 9, P_BG);          // the lock icon, then 100% at most: 4 characters from 80
    if (locked) lcd_icon(72, 1, icon_lock, P_ACC);
    text1(104 - 6 * n, 0, c);
    lcd_fillp(112, 1, 12, 7, c);
    lcd_fillp(113, 2, 10, 5, P_BG);
    lcd_fillp(124, 3, 2, 3, c);
    lcd_fillp(114, 3, (uint8_t)(pct + 12) / 25 * 2, 3, c);
}

// The queue line, "QUEUE 4 BUSY DROP 99": three fields at fixed columns, always there, each drawn
// only when it changes; dim while there's nothing to say, lit when there is.
static __xdata uint8_t shown_q, shown_b, shown_d;   // what's on the screen (0xFF: draw it)
static void status(uint8_t busy)            // the send side: frames queued, the channel busy, frames dropped
{
    uint8_t d = dropped > 99 ? 99 : dropped;
    if (q_n != shown_q) {                   // columns 0-6
        shown_q = q_n;
        buf[0] = '0' + q_n; buf[1] = 0;
        lcd_text(4, 106, "QUEUE", 1, q_n ? P_FG : P_DIM);
        text1(4 + 6 * 6, 106, q_n ? P_FG : P_DIM);
    }
    if (busy != shown_b) {                  // columns 8-11
        shown_b = busy;
        lcd_text(4 + 6 * 8, 106, "BUSY", 1, busy ? P_HI : P_DIM);
    }
    if (d != shown_d) {                     // columns 13-19: "DROP 99", the count right-aligned
        shown_d = d;
        u2s(buf, d, 2);
        if (buf[0] == '0') buf[0] = ' ';
        lcd_text(4 + 6 * 13, 106, "DROP", 1, d ? P_RED : P_DIM);
        text1(4 + 6 * 18, 106, d ? P_RED : P_DIM);
    }
    if (tx_st != TX_REFUSED || q_n) { if (tx_st < TX_KEYING || tx_st == TX_REFUSED) tx_st = q_n ? TX_WAIT : TX_IDLE; }
    tx_line();
}

// Receive narrow whatever the slot says (REG_43, bk_mode's two values): on wide the FM detector's audio
// for the same deviation is about half, too little for P4.6 (0 of 4 heard on the radio). Send as the slot
// says, wide or narrow, each with its tone gains (TX_L1_WIDE...): radio_tx doesn't write REG_43, so send()
// sets it just before. (It used to send wide always, for a narrow TX low-pass thought to cut 2200 Hz; on
// the RT-890's recordings narrow's 2200 Hz is there, and wide's deviation is too much for a narrow
// receiver.) NARROW kHz 6.25 changes only the receive filter (bits 14:12): narrow sends as 12.5.
#define BW_NARROW 0x4848                    // bk_mode: narrow, 12.5 kHz
#define BW_WIDE   0x3628

static void rx_on(void)                     // the receiver as the TNC wants it (radio_tune undoes it)
{
    bk_af(1);
    bk_write(0x48, 0xB3FF);
    bk_write(0x2B, 0x0100);
    bk_write(0x43, BW_NARROW);
}

static void sampling(void)                  // Timer2's reads on, Timer1 stopped: its flag is t2_isr's
{
    tnc_tx = 0;
    TCON &= ~0xC0;                          // TR1 and TF1 off
    T2CON = 0;
    RLDH = TH2 = 0xFF; RLDL = TL2 = RLD_SHORT;
    rd_n = 0; rd_k = 8; rd_long = 0;
    T2IF &= ~0x80;
    T2IE |= 0x80;
    IP |= 0x20;                             // PT2: above Timer1, UART1 and Timer0
    IE |= 0x20;                             // ET2
    T2CON = 0x11;                           // Fsys/12, reload at overflow: running
}

static void reads_off(void)                 // Timer2 stopped, its interrupt off
{
    T2CON = 0;
    IE &= ~0x20;
    T2IF &= ~0x80;
}

static void say(uint8_t st)                 // a step of send() on the screen at once (the USB link may
{                                           // be gone: the screen is what's left to tell)
    tx_st = st;
    tx_line();
}

// A queued frame's FCS into its slot, after its bytes. Only with sampling off: afsk_fcs shares crc_byte
// (its c and b at fixed addresses) with afsk_bit, and a slicer collecting noise from the sample
// interrupt wrote over them (a right frame with a wrong FCS on the air, ~1 in 3 at 178 B, 2026-11-18).
static uint16_t with_fcs(uint8_t slot)      // its bytes with the FCS
{
    __xdata uint8_t *p = txq[slot];
    uint16_t n = q_len[slot];
    uint16_t f = afsk_fcs(p, n);
    p[n] = f; p[n + 1] = f >> 8;
    return n + 2;
}

static void send(void)                      // the queued frames on the air, if radio_tx allows it
{
    uint16_t t, len;
    uint8_t s, h, ph, n;
    say(TX_KEYING);
    reads_off();                            // no more samples: Timer1 is the bit clock from here
    TCON &= ~0xC0;
    tx_phase = tx_bit = tx_ones = tx_tone = tx_frac = 0;
    t = txdelay * 3u / 2;                   // TXDELAY in 10 ms: 1.5 flags each (6.67 ms a flag)
    tx_left = t > 255 ? 255 : t ? t : 1;
    t = txtail * 3u / 2;                    // TXTAIL likewise, after the last frame: 3 flags at least
    tx_tail = t > 255 ? 255 : t > 3 ? t : 3;
    tx_p = txq[q_head]; tx_i = 0; tx_n = with_fcs(q_head);
    tx_nlen = tx_cnt = 0;
    tx_done = 0;
    bk_write(0x43, ch.narrow ? BW_NARROW : BW_WIDE);   // the slot's bandwidth (rx_on: why)
    tx_l1 = ch.narrow ? TX_L1_NARROW : TX_L1_WIDE;
    tx_l2 = ch.narrow ? TX_L2_NARROW : TX_L2_WIDE;
    bk_write(0x48, 0xB2A8);                 // the receive tweaks off first: transmitting with them (REG_2B
    bk_write(0x2B, 0);                      // 0x0100, REG_48 B3FF) put a silent carrier on the air; DTMF's state
    tx = 1; lights(0);                      // the backlight on while sending, LIGHT's time after (main.c)
    if (radio_tx(1)) {                      // TX BANDS, FM, the factory calibration: or nothing goes out
        LED_RXU = LED_RXV = 0;
        LED_TXU = LED_TXV = 1;              // both red LEDs while sending: main.c's leds() doesn't run in here
        say(TX_SENDING);
        // Tones only, as DualTachyon's BK4819_TransmitTone and F4HWN's UV-K1 APRS TX: muted, REG_30 without
        // the mic ADC (bit 2) and with the AF DAC (bit 9), as stock's C3FA in its tone burst; the TX AGC off
        // (REG_19 bit 15, egzumer's bk4819.c: it levelled the tones); no CTCSS/DCS under the AFSK
        // (REG_51 0: radio_tx put the slot's TX tone on); the tone on, 50 ms to settle, then REG_50 3B20.
        // REG_50 BB20 (what this firmware's DTMF writes as "mic muted") is the TX mute: left on, the air
        // got a bare carrier (seen on the radio; DTMF sending is silent the same way). bk_tx_on writes
        // REG_19 1041 again for the next PTT; radio_tx(0)'s radio_tune puts REG_30 and REG_51 back.
        bk_write(0x50, 0xBB20);
        bk_write(0x30, 0xC3FA);
        bk_write(0x19, 0x9041);
        bk_write(0x51, 0);
        bk_write(0x71, TONE_HZ(1200));
        bk_write(0x70, 0x8000 | (uint16_t)tx_l1 << 8);   // tone 1 on
        delay_ms(50);
        bk_write(0x50, 0x3B20);
        tnc_tx = 1;
        TH1 = BIT_RELOAD >> 8; TL1 = (uint8_t)BIT_RELOAD;
        TCON |= 0x40;
        t = now(); h = q_head;
        while (!tx_done && (uint16_t)(now() - t) < 1000) {   // 5 s a frame at most: 332 B take 2.3 s
            WDT_KICK();
            if (q_head != h) { h = q_head; t = now(); }   // the next frame: its own 5 s
            // The next frame's FCS while this one goes out (sampling is off: crc_byte is free). The
            // next slot is q_head + 1 while a frame's bits go out, q_head itself during its closing flags.
            EA = 0; ph = tx_phase; s = q_head; n = q_n; EA = 1;
            if (tx_nlen || !(ph == 1 ? n >= 2 : ph == 2 && n)) continue;
            if (ph == 1) s = wrap(s + 1);
            len = with_fcs(s);
            EA = 0;                         // still the slot that comes next, and not too late for it
            if (tx_phase == 1 ? wrap(q_head + 1) == s : tx_phase == 2 && q_head == s) tx_nlen = len;
            EA = 1;
        }
        TCON &= ~0x40;
        tnc_tx = 0;
        bk_write(0x70, 0);
        bk_write(0x50, 0x3B20);
        radio_tx(0);                        // the tail, then radio_tune: the receiver as the slot has it
        LED_TXU = LED_TXV = 0;
        t = sent + tx_cnt;
        sent = t < sent ? 0xFFFF : t;
        tx_st = TX_IDLE;                    // status() makes it WAIT while frames are left
    } else {                                // every queued frame would be refused the same way
        EA = 0; n = q_n; q_head = wrap(q_head + n); q_n = 0; q_bytes = 0; EA = 1;
        t = refused + n;
        refused = t < refused ? 0xFFFF : t;
        tx_st = TX_REFUSED;
    }
    lights(0); tx = 0;
    AMP_ON = spk;                           // radio_tx turned the speaker off
    rx_on();
    afsk_agc_reset();
    sampling();
}

// The slot's frequency, "144.800" (with " MHz" it ran off the screen); while digits are typed, them
// with dashes for the rest: "145.5--", or "CH 12-" on a memory channel.
static void freq_line(void)
{
    __xdata char *p = buf;
    uint8_t i;
    if (entry_n) {
        uint8_t n = sl_mem() ? 3 : 6;
        if (n == 3) p = cat(p, "CH ");
        for (i = 0; i < n; i++) {
            if (n == 6 && i == 3) *p++ = '.';
            *p++ = i < entry_n ? entry[i] : '-';
        }
        *p = 0;
    } else {
        p = u2s(buf, ch.rx / 100000, 3) + 3;    // MHz.kHz
        *p++ = '.';
        u2s(p, ch.rx / 100 % 1000, 3);
    }
    lcd_fillp(64, 20, W - 64, 8, P_BG);
    text1(64, 20, P_FG);
}

static void keyline(void)                   // the bottom line: the keys, or how to unlock
{
    static const __code label_t keys[] = {{4, BOT_Y, 1, P_KEYS, "# spk 0-9\x7f\x80 V/M back"}};
    static const __code label_t lock[] = {{4, BOT_Y, 1, P_KEYS, "LOCKED  hold * off"}};
    lcd_fillp(0, BOT_Y, W, 8, P_BG);
    draw_labels(locked ? lock : keys, 1);
}

void tnc_mode(void)
{
    static const __code label_t l[] = {{16, 14, 2, P_HI, "TNC"}, {4, 36, 1, P_DIM, "AX.25 AFSK 1200"},
        {4, 46, 1, P_DIM, "KISS USB 38400"}};
    static const __code label_t lb[] = {{16, 14, 2, P_HI, "TNC"}, {4, 36, 1, P_DIM, "AX.25 AFSK 1200"},
        {4, 46, 1, P_DIM, "KISS BLUETOOTH 9600"}};
    uint8_t k, i, sq = 0, o, seen, ep = 0, got = 0, ok_led = 0, bad_led = 0;
    uint16_t t_slot = now(), t_batt = now(), t_in = 0, t_ok = 0, t_bad = 0, c16;

    // The selected slot as tuned, awake; its audio out whatever the squelch says, at full gain, flat.
    radio_tune();
    rx_on();
    AMP_ON = spk = 0;

    lcd_clear();
    tnc_bt = BT_ON;                         // MENU 49 BLUETOOTH: KISS over the module, not USB-C
    draw_labels(tnc_bt ? lb : l, 3);
    keyline();
    entry_n = 0;
    freq_line();
    frames = sent = dropped = refused = 0; last[0] = 0;
    tx_st = TX_IDLE; tx_drawn = shown_q = shown_b = shown_d = 0xFF;
    txdelay = 27; persist = 63; slottime = 10;   // ~KISS's defaults: 40 flags (267 ms), 1 in 4, 100 ms
    txtail = duplex = 0;                    // 3 closing flags; half duplex
    lfsr = (uint16_t)TL0 << 8 | (uint8_t)ticks | 1;   // never 0: the LFSR would stay there
    floor_reset();
    a_n = 0;
    k_state = 0; q_head = q_n = 0; q_bytes = 0; hw_n = 0;
    k_out = 0; k_count = seen = 0; bt_cnt = bt_wait = 0;

    // P4.6 from beep output to audio input: C47 to the low side, then up to the input's threshold.
    beep_left = 0; TCON &= ~0x40; BEEP = 0;
    P4TRIS |= 0x40; delay_ms(100); P4TRIS &= ~0x40;
    for (uint16_t s = 0; s < 4000 && !(P4 & 0x40); s++) {   // ~1000 steps on the test radio
        pulse(1);
        for (i = 200; i; i--) idelay();     // ~1 ms for the pin to settle back to the bias (counting up
                                            // to 200 drew SDCC 4.5 warning 158)
        WDT_KICK();
    }
    afsk_init(buf0, buf1);
    ones = si = 0;
    mirror = 0;                             // REMOTE's screen copy off: it would go down the KISS port (left on
                                            // by a tdh3 session, "FRAME QUEUED" came out as 't')
    if (tnc_bt) { SCON0 &= ~0x03; IE |= 0x10; }   // ES0: uart0_isr, KISS both ways
    tnc_run = 1;                            // key_scan: the port at 38400, no REMOTE; uart1_isr: tnc_rx
    sampling();                             // ET1 is on since sys_init
    draw();
    battery();

    for (;;) {
        WDT_KICK();
        k = lights(key_event());            // LIGHT's timeout as on the home screen; side key 2 the flashlight
        if (k == (K_STAR | K_LONG)) {       // KEY LOCK: every key but this one, side keys too (main.c torch_run)
            locked = !locked;
            keyline(); battery();
        } else if (locked) k = 0;
        if (k == K_EXIT && !entry_n) break;
        if (k == K_HASH) AMP_ON = spk = !spk;   // the speaker: hear the packets (the volume knob as ever)
        else if (k && !(k & K_LONG)) {      // the slot's frequency, as on the home screen
            if (tnc_tune_key(k)) {          // retuned: radio_tune undid the TNC's receiver settings
                rx_on();
                AMP_ON = spk;
                EA = 0; afsk_agc_reset(); EA = 1;
                floor_reset();              // another frequency, another noise floor
            }
            freq_line();
        }
        // A new carrier: the tone peaks start afresh. They never decay (afsk.c: a decay lost long
        // frames), so the hiss between packets had left them high: 7 of 10 on the air.
        o = carrier(sq);
        if (o && !sq) { EA = 0; afsk_agc_reset(); EA = 1; }
        sq = o;
        open = sq;                          // main.c's: lights() keeps the backlight on while a signal is in
        if (sq) sig = bk_dbm();             // the signal while it's there: the frame ends before the carrier
        status(sq || afsk_dcd());
        if ((uint16_t)(now() - t_batt) >= 2000) { t_batt = now(); battery(); }   // every 10 s
        if (k_count != seen) {              // frames gone to the host (kiss_next)
            c16 = frames + (uint8_t)(k_count - seen);   // the counts stop at 65535
            frames = c16 < frames ? 0xFFFF : c16;
            seen = k_count;
            got = 1; t_ok = now(); ok_led = 1;
            last_dbm = sq ? sig : bk_dbm(); // squelch 0 never opens: a reading now then
            heard();
            draw();
        }
        // The lights: left green while a packet comes in (afsk.c's DCD, through the squelch when it's
        // set: hiss can't light it), held 100 ms at least; right green 0.3 s for each one decoded;
        // right red 0.3 s when one came in (DCD 200 ms gone) and none was decoded. Both red: sending.
        if (afsk_dcd() && (sq || !sql_level)) { t_in = now(); if (!ep) { ep = 1; got = 0; } }
        if (ep && (uint16_t)(now() - t_in) >= 40) { ep = 0; if (!got) { t_bad = now(); bad_led = 1; } }
        if (ok_led && (uint16_t)(now() - t_ok) >= 60) ok_led = 0;
        if (bad_led && (uint16_t)(now() - t_bad) >= 60) bad_led = 0;
        LED_RXU = ep && (uint16_t)(now() - t_in) < 20;
        LED_RXV = ok_led;
        LED_TXV = bad_led && !ok_led;
        LED_TXU = 0;
        // A frame to send: when the channel is clear, each SLOTTIME a P/256 chance (p-persistence),
        // and the channel looked at once more right before keying up: this pass's drawing can take
        // tens of ms since the look above.
        if (q_n && (!(sq || afsk_dcd()) || duplex) && (uint16_t)(now() - t_slot) >= slottime * 2u) {
            t_slot = now();
            if (rnd() <= persist && (duplex || !(carrier(0) || afsk_dcd()))) { send(); draw(); }
        }
    }

    reads_off();                            // Timer1 back to the beep's, P4.6 back to its output
    IP &= ~0x20;
    TCON &= ~0xC0;
    for (uint16_t t = now(); k_out && (uint16_t)(now() - t) < 100; ) WDT_KICK();   // the frame on its way
                                            // to the host out first (95 ms at most; 380 ms at 9600)
    bt_wait = 0;                            // no rest's end to send on into PC LINK
    IE &= ~0x10; SCON0 &= ~0x03;            // UART0 back to PC LINK's polling
    tnc_run = 0;
    q_n = 0;                                // what the host queued and never went out
    LED_RXU = LED_RXV = LED_TXU = LED_TXV = 0;   // main.c's leds() takes them back
    BEEP = 0; P4TRIS |= 0x40;
    AMP_ON = 0;
}
