// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package rpc_test

// Live test against an Azahar that is already running with an app loaded
// and the RPC server enabled. Skipped unless AZAHAR_RPC_ADDR is set.
//
//	AZAHAR_RPC_ADDR     host:port of the RPC server, e.g. 127.0.0.1:45987
//	AZAHAR_RPC_SCRATCH  optional hex address of 8 writable bytes the app
//	                    does not use; enables the write and save/load checks
//
// Screenshot sizes assume the single-screen layout sessions pin
// ([Layout] layout_option=1).

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/michvllni/mcp-azahar/internal/rpc"
)

func TestLiveAzahar(t *testing.T) {
	addr := os.Getenv("AZAHAR_RPC_ADDR")
	if addr == "" {
		t.Skip("AZAHAR_RPC_ADDR not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := rpc.Dial(ctx, rpc.UDPDialer(addr), rpc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	procs, err := c.Processes(ctx)
	if err != nil || len(procs) == 0 {
		t.Fatalf("Processes = %v, %v", procs, err)
	}
	app := procs[len(procs)-1]
	t.Logf("processes: %+v", procs)
	if err := c.SelectProcess(ctx, app.PID); err != nil {
		t.Fatal(err)
	}
	if pid, err := c.Process(ctx); err != nil || pid != app.PID {
		t.Fatalf("Process = %d, %v", pid, err)
	}
	code, err := c.ReadMemory(ctx, 0x00100000, 2*rpc.MaxReadSize+16)
	if err != nil || bytes.Equal(code[:16], make([]byte, 16)) {
		t.Fatalf("code segment read: % x, %v", code[:16], err)
	}

	for _, tc := range []struct {
		screen rpc.Screen
		w, h   int
	}{{rpc.ScreenTop, 400, 240}, {rpc.ScreenBottom, 320, 240}} {
		img, err := c.Screenshot(ctx, rpc.ScreenshotRequest{Screen: tc.screen, Scale: 1})
		if err != nil || img.Width != tc.w || img.Height != tc.h || !bytes.HasPrefix(img.Data, []byte("\x89PNG")) {
			t.Fatalf("%v screenshot: %+v, %v", tc.screen, img, err)
		}
	}
	if s, err := c.PerfStats(ctx, false); err != nil {
		t.Fatalf("PerfStats: %v", err)
	} else {
		t.Logf("perf: %+v", s)
	}
	if err := c.SetInput(ctx, rpc.Input{Buttons: rpc.ButtonA, Touch: &rpc.Point{X: 10, Y: 10}}); err != nil {
		t.Fatal(err)
	}
	if err := c.ReleaseInput(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteMemoryVerified(ctx, 0x1FF80000, []byte{0xA5}); err == nil {
		t.Fatal("write outside the writable regions reported success")
	}

	s := os.Getenv("AZAHAR_RPC_SCRATCH")
	if s == "" {
		t.Log("AZAHAR_RPC_SCRATCH not set: skipping write and save/load checks")
		return
	}
	a, err := strconv.ParseUint(s, 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	addr32 := uint32(a)
	if err := c.WriteMemoryVerified(ctx, addr32, []byte("saved!!!")); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveState(ctx, 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second) // the save runs after the reply
	if err := c.WriteMemoryVerified(ctx, addr32, []byte("changed!")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := c.LoadState(ctx, 1); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	t.Logf("LoadState incl. RPC restart took %v", time.Since(start))
	got, err := c.ReadMemory(ctx, addr32, 8)
	if err != nil || string(got) != "saved!!!" {
		t.Fatalf("after load: %q, %v", got, err)
	}
	if pid, _ := c.Process(ctx); pid != app.PID {
		t.Fatalf("process not re-selected after load: %d", pid)
	}
}
