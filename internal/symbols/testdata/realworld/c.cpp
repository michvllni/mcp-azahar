/* SPDX-License-Identifier: GPL-3.0-or-later */
#include <stdint.h>

namespace ns {
int32_t counter = 42;
}

class K {
public:
    static uint16_t s;
};
uint16_t K::s = 9;

extern "C" int fc(void) { return ns::counter + K::s; }
