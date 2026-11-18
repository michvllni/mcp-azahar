// SPDX-License-Identifier: GPL-3.0-or-later

// Package capture takes screenshots of the emulated 3DS screens. The only
// backend uses Azahar's RPC screenshot command (DECISIONS.md D4); the
// Capturer interface leaves room for others.
package capture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"strings"

	"github.com/michvllni/mcp-azahar/internal/rpc"
)

// Screens selects what to capture.
type Screens int

// Screen selections.
const (
	Top Screens = iota
	Bottom
	// Both captures the top screen, then the bottom screen: two
	// consecutive rendered frames, not one (Azahar captures one screen per
	// request), so input sent in between can show on the bottom only.
	Both
)

func (s Screens) String() string {
	switch s {
	case Bottom:
		return "bottom"
	case Both:
		return "both"
	default:
		return "top"
	}
}

// ParseScreens parses "top", "bottom" or "both" (case-insensitive).
func ParseScreens(v string) (Screens, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "top", "":
		return Top, nil
	case "bottom":
		return Bottom, nil
	case "both":
		return Both, nil
	}
	return Top, fmt.Errorf("unknown screen %q (valid: top, bottom, both)", v)
}

// Request describes a capture.
type Request struct {
	Screens Screens
	// Scale is the resolution scale 1..10; 0 uses Azahar's current internal
	// resolution (PROTOCOL.md §5.5).
	Scale int
}

// Shot is one captured screen as PNG.
type Shot struct {
	Screen string `json:"screen"` // "top" or "bottom"
	Width  int    `json:"width"`
	Height int    `json:"height"`
	PNG    []byte `json:"-"`
}

// Capturer takes screenshots of a running emulator.
type Capturer interface {
	// Capture returns one shot per requested screen, top first.
	Capture(ctx context.Context, req Request) ([]Shot, error)
}

// RPC captures through Azahar's RPC server. It needs Azahar's
// single-screen layout ([Layout] layout_option=1, which launched sessions
// pin): then a normal capture is the top screen (400x240 at scale 1) and a
// secondary one the bottom screen (320x240). Other layouts put several
// screens into one image; captures whose aspect ratio does not match a
// single screen fail with ErrLayout.
type RPC struct{ Client *rpc.Client }

// ErrLayout means Azahar's screen layout is not single-screen.
var ErrLayout = errors.New("azahar's screen layout is not single-screen: set [Layout] layout_option=1 in its qt-config.ini (View > Screen Layout > Single Screen)")

// Native screen sizes at scale 1.
var nativeSize = map[rpc.Screen][2]int{rpc.ScreenTop: {400, 240}, rpc.ScreenBottom: {320, 240}}

// Capture implements Capturer.
func (c RPC) Capture(ctx context.Context, req Request) ([]Shot, error) {
	if req.Scale < 0 || req.Scale > rpc.MaxScreenshotScale {
		return nil, fmt.Errorf("scale %d outside 0..%d", req.Scale, rpc.MaxScreenshotScale)
	}
	var screens []rpc.Screen
	switch req.Screens {
	case Top:
		screens = []rpc.Screen{rpc.ScreenTop}
	case Bottom:
		screens = []rpc.Screen{rpc.ScreenBottom}
	case Both:
		screens = []rpc.Screen{rpc.ScreenTop, rpc.ScreenBottom}
	default:
		return nil, fmt.Errorf("unknown screen selection %d", req.Screens)
	}
	shots := make([]Shot, 0, len(screens))
	for _, sc := range screens {
		img, err := c.Client.Screenshot(ctx, rpc.ScreenshotRequest{Screen: sc, Scale: req.Scale})
		if err != nil {
			return nil, explain(sc, err)
		}
		if err := checkShot(sc, img); err != nil {
			return nil, err
		}
		shots = append(shots, Shot{Screen: sc.String(), Width: img.Width, Height: img.Height, PNG: img.Data})
	}
	return shots, nil
}

// checkShot verifies the PNG matches the reported size and that the size
// has the aspect ratio of the requested screen (single-screen layout).
func checkShot(sc rpc.Screen, img *rpc.Image) error {
	cfg, err := png.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil {
		return fmt.Errorf("%v screen: invalid PNG: %w", sc, err)
	}
	if cfg.Width != img.Width || cfg.Height != img.Height {
		return fmt.Errorf("%v screen: PNG is %dx%d, Azahar reported %dx%d", sc, cfg.Width, cfg.Height, img.Width, img.Height)
	}
	n := nativeSize[sc]
	if img.Width*n[1] != img.Height*n[0] {
		return fmt.Errorf("%v screen: got a %dx%d image, want %dx%d times the scale: %w", sc, img.Width, img.Height, n[0], n[1], ErrLayout)
	}
	return nil
}

// explain turns screenshot failures into actionable errors.
func explain(sc rpc.Screen, err error) error {
	var se *rpc.ScreenshotError
	if !errors.As(err, &se) {
		return fmt.Errorf("%v screen: %w", sc, err)
	}
	hint := ""
	switch se.Result {
	case rpc.ScreenshotUnsupported:
		hint = "; switch Azahar's graphics API to OpenGL or Vulkan (the software renderer cannot take screenshots)"
	case rpc.ScreenshotBusy:
		hint = "; another screenshot of this emulator is in progress, retry"
	case rpc.ScreenshotTimeout:
		hint = "; is emulation paused or stuck behind a dialog?"
	}
	return fmt.Errorf("%v screen: %w%s", sc, err, hint)
}

// Compose combines shots into one PNG in the 3DS arrangement: top screen
// above the bottom screen, each centered horizontally, on opaque black.
// The shots of a Both capture are consecutive frames, not one. A single
// shot is returned unchanged.
func Compose(shots []Shot) ([]byte, int, int, error) {
	switch len(shots) {
	case 0:
		return nil, 0, 0, errors.New("no screenshots to compose")
	case 1:
		return shots[0].PNG, shots[0].Width, shots[0].Height, nil
	}
	w, h := 0, 0
	for _, s := range shots {
		cfg, err := png.DecodeConfig(bytes.NewReader(s.PNG))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("decode %s screenshot: %w", s.Screen, err)
		}
		w = max(w, cfg.Width)
		h += cfg.Height
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Bounds(), image.Black, image.Point{}, draw.Src)
	y := 0
	// One decoded shot at a time keeps the peak memory low at high scales.
	for _, s := range shots {
		img, err := png.Decode(bytes.NewReader(s.PNG))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("decode %s screenshot: %w", s.Screen, err)
		}
		b := img.Bounds()
		x := (w - b.Dx()) / 2
		draw.Draw(out, image.Rect(x, y, x+b.Dx(), y+b.Dy()), img, b.Min, draw.Src)
		y += b.Dy()
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), w, h, nil
}
