// SPDX-License-Identifier: GPL-3.0-or-later
//
// Test fixture for mcp-azahar's integration tests (docs/PLAN.md Phase 7): a
// tiny libctru homebrew whose state the tests read by symbol name.
//
// - test_state (global) records frames, buttons, touch and circle pad.
//   test_state.scratch is never written by the app: tests use it for
//   memory writes and save state checks.
// - The top screen shows four fixed color bars (red, green, blue, white,
//   100 px each); the bottom screen is dark gray with a white 8x8 square
//   at the last touch. Both are the same every frame.
// - It logs "fixture: ..." lines with svcOutputDebugString (Azahar logs
//   them as Debug.Emulated).
#include <3ds.h>
#include <stdio.h>
#include <string.h>

#define FIXTURE_MAGIC 0x58494654u // "TFIX" in memory

typedef enum { MODE_BOOT = 0, MODE_RUN = 1 } fixture_mode;

typedef struct {
    uint32_t magic;
    uint32_t frame;        // frames since start
    uint32_t last_buttons; // keys of the last frame with a new press
    uint32_t held;         // keys held this frame
    uint32_t presses;      // frames with a new press
    uint16_t touch_x, touch_y; // last touch
    uint32_t touches;      // touches started
    bool touching;
    int16_t circle_x, circle_y;
    fixture_mode mode;
    uint32_t scratch;      // never written by the app
    char message[24];
} fixture_state;

volatile fixture_state test_state = {.magic = FIXTURE_MAGIC, .mode = MODE_BOOT, .message = "fixture booting"};

static void debug(const char *fmt, unsigned a, unsigned b) {
    char buf[64];
    int n = snprintf(buf, sizeof buf, fmt, a, b);
    if (n > 0)
        svcOutputDebugString(buf, n < (int)sizeof buf ? n : (int)sizeof buf - 1);
}

// put sets pixel (x, y) of a BGR8 framebuffer; the 3DS screens are rotated,
// so columns of 240 pixels run from bottom to top.
static void put(u8 *fb, int x, int y, u8 r, u8 g, u8 b) {
    u8 *p = fb + ((x * 240) + (239 - y)) * 3;
    p[0] = b;
    p[1] = g;
    p[2] = r;
}

static void draw_top(void) {
    static const u8 bars[4][3] = {{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {255, 255, 255}};
    u8 *fb = gfxGetFramebuffer(GFX_TOP, GFX_LEFT, NULL, NULL);
    for (int x = 0; x < 400; x++) {
        const u8 *c = bars[x / 100];
        for (int y = 0; y < 240; y++)
            put(fb, x, y, c[0], c[1], c[2]);
    }
}

static void draw_bottom(void) {
    u8 *fb = gfxGetFramebuffer(GFX_BOTTOM, GFX_LEFT, NULL, NULL);
    memset(fb, 0x20, 320 * 240 * 3);
    if (test_state.touches == 0)
        return;
    for (int x = test_state.touch_x - 4; x < test_state.touch_x + 4; x++)
        for (int y = test_state.touch_y - 4; y < test_state.touch_y + 4; y++)
            if (x >= 0 && x < 320 && y >= 0 && y < 240)
                put(fb, x, y, 255, 255, 255);
}

int main(void) {
    gfxInitDefault();
    test_state.mode = MODE_RUN;
    strcpy((char *)test_state.message, "fixture ready");
    debug("fixture: ready (magic 0x%08x, build %u)", FIXTURE_MAGIC, 1);

    while (aptMainLoop()) {
        hidScanInput();
        u32 down = hidKeysDown();
        test_state.held = hidKeysHeld();
        if (down) {
            test_state.last_buttons = down;
            test_state.presses++;
            debug("fixture: down 0x%08x (press %u)", down, test_state.presses);
        }
        if (test_state.held & KEY_TOUCH) {
            touchPosition t;
            hidTouchRead(&t);
            test_state.touch_x = t.px;
            test_state.touch_y = t.py;
            if (!test_state.touching) {
                test_state.touches++;
                debug("fixture: touch %u %u", t.px, t.py);
            }
            test_state.touching = true;
        } else {
            test_state.touching = false;
        }
        circlePosition c;
        hidCircleRead(&c);
        test_state.circle_x = c.dx;
        test_state.circle_y = c.dy;

        draw_top();
        draw_bottom();
        gfxFlushBuffers();
        gfxSwapBuffers();
        gspWaitForVBlank();
        test_state.frame++;
    }
    gfxExit();
    return 0;
}
