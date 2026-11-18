/* SPDX-License-Identifier: GPL-3.0-or-later */
/* Real-world layout for internal/symbols tests: declarations in a header,
 * definitions elsewhere (DW_AT_specification), bitfields, a flexible
 * array member. */
#include <stdint.h>

typedef struct {
    uint8_t a : 3;
    uint8_t b : 5;
    uint16_t c : 9;
    int8_t s : 4;
} bits_t;

struct flex {
    uint32_t n;
    int32_t data[];
};

struct pair {
    uint16_t lo, hi;
};

extern bits_t bits;
extern struct pair shared;
extern const char *greeting;
