// tools/afskbench.py's harness: fw/afsk.c (and fw/asm/afsk_sample.asm) on the 8051, in tools/emu51.py.
#include "afsk.h"
#define IN   ((volatile __xdata uint8_t *)0x1000)   // [0..1] the count, then the samples (the emulator puts them)
#define IDX  (*(volatile __xdata uint16_t *)0xF000) // samples done: the lockstep watches it
#define OUT  ((volatile __xdata uint8_t *)0xF800)   // [0] frames found, then each one's length; [0x7F] 0xA5 done
static __xdata uint8_t b0[AFSK_MAX], b1[AFSK_MAX];

void main(void)
{
    uint16_t i, n = IN[0] | IN[1] << 8;
    OUT[0] = 0;
    afsk_init(b0, b1);
    for (i = 0; i < n; i++) {
        afsk_sample(IN[2 + i]);
        IDX = i + 1;
        if (afsk_len) { OUT[1 + OUT[0]++] = afsk_len; afsk_release(); }
    }
    OUT[0x7F] = 0xA5;
    for (;;) ;
}
