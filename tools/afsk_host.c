// Host build of fw/afsk.c (tools/afskmodel.py --c): samples n (0-8) as bytes on stdin, one frame a
// line as hex on stdout, with the sample index at which it was handed out.
#include <stdio.h>
#include "../fw/afsk.h"

int main(void)
{
    int c;
    unsigned long i = 0;
    static uint8_t b0[AFSK_MAX], b1[AFSK_MAX];
    afsk_init(b0, b1);
    while ((c = getchar()) != EOF) {
        afsk_sample((uint8_t)c);
        i++;
        if (afsk_len) {
            printf("%lu ", i);
            for (uint16_t k = 0; k < afsk_len; k++) printf("%02x", afsk_frame[k]);
            printf("\n");
            afsk_release();
        }
    }
    return 0;
}
