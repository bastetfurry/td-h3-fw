// AFSK 1200 (Bell 202) receive for P4.6's 1-bit audio: the demodulator and AX.25 HDLC (afsk.c).
// Plain C that SDCC builds for the radio and gcc for the host test (tools/afskmodel.py --c).
#ifndef AFSK_H
#define AFSK_H
#include <stdint.h>

#ifndef __SDCC                              // the host build: SDCC's memory spaces mean nothing there
#define __code
#define __xdata
#define __data
#define __reentrant
#endif

#define AFSK_NSL 2                          // slicers: tone weights 2:3 (favours 2200 Hz) and 1:1
#ifndef AFSK_MAX
#define AFSK_MAX 332                        // a frame with its FCS, at most: AX.25's 330 (8 digipeaters, 256 B
#endif                                      // of info) and the FCS. A longer one is dropped

void afsk_init(__xdata uint8_t *buf0, __xdata uint8_t *buf1) __reentrant;   // everything to its start, AGC peaks
                                            // included; the slicers' frame buffers, AFSK_MAX bytes each
void afsk_agc_reset(void);                  // the AGC peaks only: for a new carrier
void afsk_sample(uint8_t n);                // one sample at 9592 Hz: n = how many of P4.6's 8 reads were 1
                                            // (C behind C_AFSK_SAMPLE in afsk.c; fw/asm/afsk_sample.asm)
void afsk_bit(uint8_t s, uint8_t b);        // slicer s's next bit: afsk_sample calls it

// A good frame (FCS checked and stripped, any AX.25 frame: UI, I, S, U) waits in afsk_frame[0..afsk_len) until
// afsk_release(); another one meanwhile is lost. The slicer that found it collects on into the same
// buffer, so the flag that closed the frame can open the next: read it out faster than that refills it.
// The next frame's byte i lands no sooner than 6.67 ms x (i + 1) after the hand-out: KISS at 38400 from
// the UART interrupt (tnc.c) stays ahead from the start, as afsk_host.c's release right after the sample.
extern __xdata uint16_t afsk_len;           // 0: nothing waiting
extern __xdata uint8_t * __xdata afsk_frame;
void afsk_release(void);
uint8_t afsk_dcd(void);                     // nonzero while a packet comes in (a preamble's flags, then its bytes)
uint16_t afsk_fcs(__xdata uint8_t *f, uint16_t n) __reentrant;   // the FCS to send, low byte first
#endif
