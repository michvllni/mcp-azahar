// SPDX-License-Identifier: GPL-3.0-or-later

package capture_test

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/michvllni/mcp-azahar/internal/capture"
	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/rpc/fakeserver"
)

func setup(t *testing.T) (*fakeserver.Server, capture.RPC) {
	t.Helper()
	srv, err := fakeserver.New(fakeserver.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	c, err := rpc.Dial(context.Background(), rpc.UDPDialer(srv.Addr()), rpc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return srv, capture.RPC{Client: c}
}

func TestParseScreens(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want capture.Screens
	}{{"top", capture.Top}, {"", capture.Top}, {"Bottom", capture.Bottom}, {" both ", capture.Both}} {
		if got, err := capture.ParseScreens(tc.in); err != nil || got != tc.want {
			t.Errorf("ParseScreens(%q) = %v, %v", tc.in, got, err)
		}
	}
	if _, err := capture.ParseScreens("left"); err == nil {
		t.Error("left accepted")
	}
}

func TestCapture(t *testing.T) {
	_, c := setup(t)
	ctx := context.Background()
	for _, tc := range []struct {
		req   capture.Request
		sizes [][2]int
	}{
		{capture.Request{Screens: capture.Top}, [][2]int{{400, 240}}},
		{capture.Request{Screens: capture.Bottom}, [][2]int{{320, 240}}},
		{capture.Request{Screens: capture.Both, Scale: 2}, [][2]int{{800, 480}, {640, 480}}},
	} {
		shots, err := c.Capture(ctx, tc.req)
		if err != nil {
			t.Fatalf("%+v: %v", tc.req, err)
		}
		if len(shots) != len(tc.sizes) {
			t.Fatalf("%+v: %d shots", tc.req, len(shots))
		}
		for i, s := range shots {
			img, err := png.Decode(bytes.NewReader(s.PNG))
			if err != nil || s.Width != tc.sizes[i][0] || s.Height != tc.sizes[i][1] ||
				img.Bounds().Dx() != s.Width || img.Bounds().Dy() != s.Height {
				t.Fatalf("%+v shot %d: %dx%d, %v", tc.req, i, s.Width, s.Height, err)
			}
		}
		if tc.req.Screens == capture.Both && (shots[0].Screen != "top" || shots[1].Screen != "bottom") {
			t.Fatalf("order: %s, %s", shots[0].Screen, shots[1].Screen)
		}
	}
	if _, err := c.Capture(ctx, capture.Request{Scale: 11}); err == nil {
		t.Fatal("scale 11 accepted")
	}
}

func TestCaptureErrorsExplained(t *testing.T) {
	srv, c := setup(t)
	for r, want := range map[rpc.ScreenshotResult]string{
		rpc.ScreenshotUnsupported: "OpenGL or Vulkan",
		rpc.ScreenshotBusy:        "retry",
		rpc.ScreenshotTimeout:     "paused",
	} {
		srv.SetScreenshotResult(r)
		_, err := c.Capture(context.Background(), capture.Request{Screens: capture.Bottom})
		var se *rpc.ScreenshotError
		if !errors.As(err, &se) || se.Result != r || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "bottom") {
			t.Errorf("%v: %v", r, err)
		}
	}
}

func TestCompose(t *testing.T) {
	_, c := setup(t)
	shots, err := c.Capture(context.Background(), capture.Request{Screens: capture.Both})
	if err != nil {
		t.Fatal(err)
	}
	b, w, h, err := capture.Compose(shots)
	if err != nil || w != 400 || h != 480 {
		t.Fatalf("Compose: %dx%d, %v", w, h, err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	rgba := func(x, y int) color.RGBA { return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) }
	red, blue, black := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{0, 0, 0, 0}
	for _, p := range []struct {
		x, y int
		want color.RGBA
	}{
		{0, 0, red}, {399, 239, red}, // top screen fills the width
		{40, 240, blue}, {359, 479, blue}, // bottom screen centered: x 40..359
		{10, 300, black}, {390, 300, black}, // margins beside the bottom screen
	} {
		if got := rgba(p.x, p.y); got != p.want {
			t.Errorf("pixel (%d,%d) = %v, want %v", p.x, p.y, got, p.want)
		}
	}
	single, w, h, err := capture.Compose(shots[:1])
	if err != nil || !bytes.Equal(single, shots[0].PNG) || w != 400 || h != 240 {
		t.Fatalf("single shot not passed through: %dx%d, %v", w, h, err)
	}
	if _, _, _, err := capture.Compose(nil); err == nil {
		t.Fatal("empty compose accepted")
	}
}
