/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "state.h"

bits_t bits = {5, 17, 300, -3};
struct flex flexible = {3, {11, 22, 33}};
const char *greeting = "hi";
static int shared_static = 1; /* only file-local */
static int x = 1;             /* same name as b.c's global x */

int fa(void) {
    char hijack[16] = "local";   /* local: must not shadow b.c's global */
    static uint32_t st_local = 77; /* static local: fa::st_local */
    st_local++;
    return hijack[0] + x + shared_static + (int)st_local;
}
