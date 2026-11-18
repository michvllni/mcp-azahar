// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package capture_test

// Live test against a running Azahar (app loaded, RPC enabled, single-screen
// layout). Skipped unless AZAHAR_RPC_ADDR is set.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/michvllni/mcp-azahar/internal/capture"
	"github.com/michvllni/mcp-azahar/internal/rpc"
)

func TestLiveCapture(t *testing.T) {
	addr := os.Getenv("AZAHAR_RPC_ADDR")
	if addr == "" {
		t.Skip("AZAHAR_RPC_ADDR not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := rpc.Dial(ctx, rpc.UDPDialer(addr), rpc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	shots, err := capture.RPC{Client: c}.Capture(ctx, capture.Request{Screens: capture.Both, Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(shots) != 2 || shots[0].Width != 400 || shots[0].Height != 240 || shots[1].Width != 320 || shots[1].Height != 240 {
		t.Fatalf("shots: %+v", shots)
	}
	if _, w, h, err := capture.Compose(shots); err != nil || w != 400 || h != 480 {
		t.Fatalf("Compose: %dx%d, %v", w, h, err)
	}
}
