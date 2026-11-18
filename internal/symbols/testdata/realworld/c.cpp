/* SPDX-License-Identifier: GPL-3.0-or-later */
#include <stdint.h>

namespace ns {
int32_t counter = 42;
}

namespace ns {
int nf();
}

class K {
public:
    static uint16_t s;
    int m();
};
uint16_t K::s = 9;

/* Out-of-line definitions: the DIE has only DW_AT_specification. */
int K::m() {
    static int calls = 5; /* K::m::calls */
    return ++calls;
}

int ns::nf() {
    static int calls = 6; /* ns::nf::calls */
    return ++calls;
}

extern "C" int fc(void) { K k;
    return ns::counter + K::s + k.m() + ns::nf(); }
