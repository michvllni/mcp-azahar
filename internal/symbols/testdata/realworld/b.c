/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "state.h"

struct pair shared = {0x1234, 0x5678};
struct pair x = {1, 2}; /* global x; a.c has a static x */
uint8_t hijack = 7;     /* a.c has a local of the same name */
enum big bigv = B200;
struct colors colors = {WHITE, GREEN, NEG};

int main(void) { return shared.lo + x.hi + hijack; }
