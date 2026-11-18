/* SPDX-License-Identifier: GPL-3.0-or-later */
/* Test input for internal/symbols: globals of many DWARF type shapes,
 * linked for ARM at the 3DSX code address. Built by
 * scripts/build-testdata.sh into symbols.elf (committed). */
#include <stdint.h>
#include <stdbool.h>

enum mode { MODE_IDLE = 0, MODE_RUN = 2 };

typedef struct {
    uint16_t x, y;
} point;

struct test_state {
    uint32_t magic;
    uint32_t frame;
    uint32_t last_buttons;
    point touch;
    int16_t circle[2];
    char name[8];
    float speed;
    bool ok;
    enum mode mode;
    const uint8_t *ptr;
};

struct test_state test_state = {
    .magic = 0x54455354, /* "TEST" */
    .frame = 7,
    .last_buttons = 0,
    .touch = {160, 120},
    .circle = {-154, 12},
    .name = "fixture",
    .speed = 1.5f,
    .ok = true,
    .mode = MODE_RUN,
};

volatile uint8_t counter = 3;
const int32_t negative = -42;
uint32_t table[4] = {10, 20, 30, 40};

int main(void) {
    counter++;
    return (int)test_state.frame + table[1];
}
