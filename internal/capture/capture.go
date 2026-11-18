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
	Screen        string `json:"screen"` // "top" or "bottom"
	Width, Height int
	PNG           []byte `json:"-"`
}

// Capturer takes screenshots of a running emulator.
type Capturer interface {
	// Capture returns one shot per requested screen, top first.
	Capture(ctx context.Context, req Request) ([]Shot, error)
}

// RPC captures through Azahar's RPC server. With the single-screen layout
// sessions pin, a normal capture is the top screen (400x240 at scale 1)
// and a secondary one the bottom screen (320x240). Attached instances may
// use another layout; the reported size is what Azahar returned.
type RPC struct{ Client *rpc.Client }

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
		shots = append(shots, Shot{Screen: sc.String(), Width: img.Width, Height: img.Height, PNG: img.Data})
	}
	return shots, nil
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
// above the bottom screen, each centered horizontally. A single shot is
// returned unchanged.
func Compose(shots []Shot) ([]byte, int, int, error) {
	switch len(shots) {
	case 0:
		return nil, 0, 0, errors.New("no screenshots to compose")
	case 1:
		return shots[0].PNG, shots[0].Width, shots[0].Height, nil
	}
	imgs := make([]image.Image, len(shots))
	w, h := 0, 0
	for i, s := range shots {
		img, err := png.Decode(bytes.NewReader(s.PNG))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("decode %s screenshot: %w", s.Screen, err)
		}
		imgs[i] = img
		w = max(w, img.Bounds().Dx())
		h += img.Bounds().Dy()
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	y := 0
	for _, img := range imgs {
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
